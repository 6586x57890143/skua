package purge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

const (
	guildID         = 3
	me, them        = snowflake.ID(5), snowflake.ID(6)
	textCh, forumCh = snowflake.ID(10), snowflake.ID(11)
	voiceCh         = snowflake.ID(12)
	activeTh        = snowflake.ID(20)
	archivedTh      = snowflake.ID(21)
)

// fake is Discord's side of a sweep: channels, threads and their messages,
// with every delete recorded. A deleted message is gone from later reads,
// as it would be.
type fake struct {
	rest.Rest
	mu           sync.Mutex
	chans        []discord.GuildChannel
	active       []discord.GuildThread
	public       map[snowflake.ID][]discord.GuildThread
	msgs         map[snowflake.ID][]discord.Message // ascending
	denyRead     map[snowflake.ID]bool
	denyPrivate  bool
	privateAsked []snowflake.ID
	gone         map[snowflake.ID]bool
	bulks        [][]snowflake.ID
	singles      []snowflake.ID
	bulkErr      error
	deleteErr    map[snowflake.ID]error
	hold         chan struct{} // when set, listing channels waits for it
	guild        snowflake.ID  // when set, every other guild is empty
	updates      chan string
}

func newFake() *fake {
	return &fake{
		public: map[snowflake.ID][]discord.GuildThread{}, msgs: map[snowflake.ID][]discord.Message{},
		denyRead: map[snowflake.ID]bool{}, gone: map[snowflake.ID]bool{}, deleteErr: map[snowflake.ID]error{},
		updates: make(chan string, 64),
	}
}

func refusal(status int, code rest.JSONErrorCode) error {
	return &rest.Error{Response: &http.Response{StatusCode: status}, Code: code}
}

func (f *fake) GetGuildChannels(g snowflake.ID, _ ...rest.RequestOpt) ([]discord.GuildChannel, error) {
	if f.hold != nil {
		<-f.hold
	}
	if f.guild != 0 && g != f.guild {
		return nil, nil
	}
	return f.chans, nil
}

func (f *fake) GetActiveGuildThreads(g snowflake.ID, _ ...rest.RequestOpt) (*discord.GuildActiveThreads, error) {
	if f.guild != 0 && g != f.guild {
		return &discord.GuildActiveThreads{}, nil
	}
	return &discord.GuildActiveThreads{Threads: f.active}, nil
}

// The fake answers as Discord does, down to what disgo leaves out of a
// request when an argument is zero. A fake kinder than Discord is how a
// sweep that read only each channel's newest page passed every test.

// GetPublicArchivedThreads pages as Discord does: newest archived first,
// before taken to the second (disgo sends RFC 3339), has_more when the
// limit cut the list short.
func (f *fake) GetPublicArchivedThreads(ch snowflake.ID, before time.Time, limit int, _ ...rest.RequestOpt) (*discord.GetThreads, error) {
	ts := slices.Clone(f.public[ch])
	slices.SortStableFunc(ts, func(a, b discord.GuildThread) int {
		return b.ThreadMetadata.ArchiveTimestamp.Compare(a.ThreadMetadata.ArchiveTimestamp)
	})
	if !before.IsZero() {
		cut := before.Truncate(time.Second)
		ts = slices.DeleteFunc(ts, func(t discord.GuildThread) bool { return !t.ThreadMetadata.ArchiveTimestamp.Before(cut) })
	}
	if limit == 0 {
		limit = 50
	}
	more := len(ts) > limit
	return &discord.GetThreads{Threads: ts[:min(limit, len(ts))], HasMore: more}, nil
}

func (f *fake) GetPrivateArchivedThreads(ch snowflake.ID, _ time.Time, _ int, _ ...rest.RequestOpt) (*discord.GetThreads, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.privateAsked = append(f.privateAsked, ch)
	if f.denyPrivate {
		return nil, refusal(403, rest.JSONErrorCodeMissingAccess)
	}
	return &discord.GetThreads{}, nil
}

// GetMessages answers as Discord does: with an after, the limit messages
// right after it; without one (disgo leaves out an after of 0), the newest
// limit. Either way newest first.
func (f *fake) GetMessages(ch, _, _, after snowflake.ID, limit int, _ ...rest.RequestOpt) ([]discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.denyRead[ch] {
		return nil, refusal(403, rest.JSONErrorCodeMissingAccess)
	}
	var out []discord.Message
	for _, m := range f.msgs[ch] {
		if m.ID > after && !f.gone[m.ID] {
			out = append(out, m)
		}
	}
	if after == 0 {
		out = out[max(0, len(out)-limit):]
	} else {
		out = out[:min(limit, len(out))]
	}
	slices.Reverse(out)
	return out, nil
}

// BulkDeleteMessages refuses what Discord refuses: fewer than two IDs, or
// any message 14 days old.
func (f *fake) BulkDeleteMessages(_ snowflake.ID, ids []snowflake.ID, _ ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.bulkErr; err != nil {
		return err
	}
	if len(ids) < 2 || len(ids) > bulkMax {
		return refusal(400, rest.JSONErrorCodeInvalidFormBody)
	}
	for _, id := range ids {
		if time.Since(id.Time()) >= 14*24*time.Hour {
			return refusal(400, rest.JSONErrorCodeMessageTooOldToBulkDelete)
		}
	}
	f.bulks = append(f.bulks, slices.Clone(ids))
	for _, id := range ids {
		f.gone[id] = true
	}
	return nil
}

func (f *fake) DeleteMessage(_, id snowflake.ID, _ ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.deleteErr[id]; err != nil {
		return err
	}
	if f.gone[id] {
		return refusal(404, rest.JSONErrorCodeUnknownMessage)
	}
	f.singles = append(f.singles, id)
	f.gone[id] = true
	return nil
}

func (f *fake) UpdateInteractionResponse(_ snowflake.ID, _ string, u discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.updates <- *u.Content
	return nil, nil
}

func decode[T any](t testing.TB, raw string, into *T) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), into); err != nil {
		t.Fatal(err)
	}
}

func guildChannel(t testing.TB, id snowflake.ID, typ discord.ChannelType) discord.GuildChannel {
	var u discord.UnmarshalChannel
	decode(t, fmt.Sprintf(`{"id":"%d","type":%d,"guild_id":"%d"}`, id, typ, guildID), &u)
	return u.Channel.(discord.GuildChannel)
}

func thread(t testing.TB, id, parent snowflake.ID) discord.GuildThread {
	var th discord.GuildThread
	decode(t, fmt.Sprintf(`{"id":"%d","type":11,"guild_id":"%d","parent_id":"%d",
		"thread_metadata":{"archived":true,"archive_timestamp":"2026-01-01T00:00:00Z"}}`, id, guildID, parent), &th)
	return th
}

// seq hands out message IDs that are distinct and ascending from a time.
type seq struct{ n int }

func (s *seq) at(t time.Time, author snowflake.ID) discord.Message {
	s.n++
	return discord.Message{ID: snowflake.New(t.Add(time.Duration(s.n) * time.Millisecond)), Author: discord.User{ID: author}}
}

// server is a guild with something everywhere a sweep has to look: a text
// channel with 250 messages from two members, a quarter over two weeks old;
// an active thread; a forum's archived thread holding an old message; and
// a voice channel skua can't read.
func server(t testing.TB, now time.Time) *fake {
	f := newFake()
	f.chans = []discord.GuildChannel{
		guildChannel(t, textCh, discord.ChannelTypeGuildText),
		guildChannel(t, forumCh, discord.ChannelTypeGuildForum),
		guildChannel(t, voiceCh, discord.ChannelTypeGuildVoice),
		guildChannel(t, 13, discord.ChannelTypeGuildCategory),
	}
	f.active = []discord.GuildThread{thread(t, activeTh, textCh)}
	f.public[forumCh] = []discord.GuildThread{thread(t, archivedTh, forumCh)}
	f.denyRead[voiceCh] = true
	var s seq
	old, recent := now.Add(-30*24*time.Hour), now.Add(-time.Hour)
	for i := range 250 {
		at, who := recent, them
		if i < 60 {
			at = old
		}
		if i%2 == 0 {
			who = me
		}
		f.msgs[textCh] = append(f.msgs[textCh], s.at(at, who))
	}
	for range 3 {
		f.msgs[activeTh] = append(f.msgs[activeTh], s.at(recent, me))
	}
	f.msgs[archivedTh] = append(f.msgs[archivedTh], s.at(old, me), s.at(old, them))
	return f
}

func newSweep(f *fake, now time.Time) *sweep {
	return &sweep{
		r: f, guard: guard.New(), pace: newPacer(1e9), guild: guildID,
		authors: map[snowflake.ID]*atomic.Int64{me: new(atomic.Int64)}, cutoff: snowflake.New(now), now: func() time.Time { return now },
	}
}

func TestSweepDeletesOnlyTheAuthorsMessages(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	// Posted after the sweep started: not this sweep's to delete.
	late := discord.Message{ID: snowflake.New(now.Add(time.Second)), Author: discord.User{ID: me}}
	f.msgs[textCh] = append(f.msgs[textCh], late)
	s := newSweep(f, now)
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}

	var mine, all int
	for ch, msgs := range f.msgs {
		for _, m := range msgs {
			if m.ID == late.ID {
				if f.gone[m.ID] {
					t.Fatal("deleted a message posted after the sweep began")
				}
				continue
			}
			readable := ch != voiceCh
			if readable {
				all++
			}
			switch {
			case m.Author.ID == me && readable && !f.gone[m.ID]:
				t.Errorf("message %d by the member in %d survived", m.ID, ch)
			case m.Author.ID != me && f.gone[m.ID]:
				t.Errorf("deleted message %d by someone else", m.ID)
			case m.Author.ID == me && readable:
				mine++
			}
		}
	}
	if got := s.deleted.Load(); got != int64(mine) {
		t.Errorf("deleted counts %d, want %d", got, mine)
	}
	if got := s.scanned.Load(); got != int64(all) {
		t.Errorf("scanned %d, want %d", got, all)
	}
	for _, b := range f.bulks {
		if len(b) < 2 || len(b) > bulkMax {
			t.Errorf("a bulk delete of %d", len(b))
		}
		for _, id := range b {
			if now.Sub(id.Time()) >= young {
				t.Errorf("old message %d went in a bulk delete", id)
			}
		}
	}
	for _, id := range f.singles {
		if now.Sub(id.Time()) < young {
			t.Errorf("recent message %d deleted singly", id)
		}
	}
	if s.done.Load() != s.channels.Load() || s.channels.Load() != 4 {
		t.Errorf("channels %d of %d, want 4 of 4", s.done.Load(), s.channels.Load())
	}
	if !slices.Equal(s.unreachable, []snowflake.ID{voiceCh}) {
		t.Errorf("unreachable %v, want [%d]", s.unreachable, voiceCh)
	}
}

// A scheduled sweep starts after what an earlier one finished: nothing at or
// before from is read again.
func TestSweepReadsOnlyAfterFrom(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	s := newSweep(f, now)
	s.from = f.msgs[textCh][199].ID
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.gone[f.msgs[textCh][198].ID] || !f.gone[f.msgs[textCh][200].ID] {
		t.Fatal("from did not bound the read")
	}
}

func TestBulkRefusedFallsBackToSingles(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	f.bulkErr = refusal(400, rest.JSONErrorCodeMessageTooOldToBulkDelete)
	s := newSweep(f, now)
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.bulks) != 0 || s.missed.Load() != 0 {
		t.Fatalf("bulks %d, missed %d", len(f.bulks), s.missed.Load())
	}
	for _, m := range f.msgs[textCh] {
		if m.Author.ID == me && !f.gone[m.ID] {
			t.Fatal("a message stayed after the bulk fallback")
		}
	}
}

func TestDeleteOutcomes(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	f.chans = f.chans[:1]
	f.active = nil
	old := f.msgs[textCh][:6] // old: deleted singly
	f.deleteErr[old[0].ID] = refusal(404, rest.JSONErrorCodeUnknownMessage)
	f.deleteErr[old[2].ID] = refusal(400, rest.JSONErrorCodeCannotExecuteActionOnSystemMessage)
	f.deleteErr[old[4].ID] = refusal(500, 0)
	s := newSweep(f, now)
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.missed.Load() != 1 {
		t.Errorf("missed %d, want 1: only the 500", s.missed.Load())
	}

	// 403 on a delete: the channel is unreachable and its sweep ends there.
	// Pages come newest first and recent messages wait for a full bulk, so
	// the first delete sent is the newest old message of the member's, 58.
	f = server(t, now)
	f.chans = f.chans[:1]
	f.active = nil
	f.deleteErr[f.msgs[textCh][58].ID] = refusal(403, rest.JSONErrorCodeLackPermissionsToPerformAction)
	s = newSweep(f, now)
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.deleted.Load() != 0 || !slices.Contains(s.unreachable, textCh) {
		t.Fatalf("deleted %d, unreachable %v", s.deleted.Load(), s.unreachable)
	}
	f = server(t, now)
	f.chans = f.chans[:1]
	f.active = nil
	f.msgs[textCh] = f.msgs[textCh][60:] // recent only: bulk
	f.bulkErr = refusal(403, rest.JSONErrorCodeLackPermissionsToPerformAction)
	s = newSweep(f, now)
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.unreachable, textCh) {
		t.Fatal("a refused bulk delete didn't mark the channel")
	}
}

func TestPrivateThreadsRefusedMarksTheParent(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	f.denyPrivate = true
	s := newSweep(f, now)
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.unreachable, textCh) {
		t.Fatal("a refused private thread listing went unmentioned")
	}
}

func TestGuardRefusalStopsTheSweep(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	s := newSweep(f, now)
	for s.guard.Allow(guildID, guard.Purge) == nil {
	}
	if err := s.run(context.Background()); !errors.Is(err, errRefused) {
		t.Fatalf("got %v, want errRefused", err)
	}
	if s.deleted.Load() != 0 {
		t.Fatal("deleted past a refused budget")
	}
}

func TestCancelledSweepStops(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newSweep(f, now).run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if len(f.gone) != 0 {
		t.Fatal("a cancelled sweep deleted something")
	}
}

func TestPacerSpacesRequests(t *testing.T) {
	p := newPacer(1000)
	began := time.Now()
	var wg sync.WaitGroup
	for range 21 {
		wg.Go(func() {
			if err := p.wait(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if took := time.Since(began); took < 20*time.Millisecond {
		t.Fatalf("21 slots at 1000/s took %v, want at least 20ms", took)
	}
}

func TestPacerHonoursCancel(t *testing.T) {
	p := newPacer(1)
	if err := p.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := p.wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want the deadline", err)
	}
}

// A first sweep starts at a channel's oldest message, not at its newest
// page: a member who stopped posting long ago is still found.
func TestSweepReadsFromTheOldest(t *testing.T) {
	now := time.Now()
	f := newFake()
	f.chans = []discord.GuildChannel{guildChannel(t, textCh, discord.ChannelTypeGuildText)}
	var s seq
	for i := range 3 * page {
		who := them
		if i < 10 {
			who = me
		}
		f.msgs[textCh] = append(f.msgs[textCh], s.at(now.Add(-time.Hour), who))
	}
	sw := newSweep(f, now)
	if err := sw.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sw.deleted.Load(); got != 10 {
		t.Errorf("deleted %d, want 10", got)
	}
	if got := sw.scanned.Load(); got != 3*page {
		t.Errorf("scanned %d, want %d", got, 3*page)
	}
}

// Discord takes before to the second, so threads archived in the same
// second as the end of a page must still turn up on the next one, once.
// More than a page in one second loses only that second's overflow, never
// the older threads behind it.
func TestArchivedThreadsPageWithoutLoss(t *testing.T) {
	for _, tc := range []struct {
		name        string
		burst, lost int
	}{{"burst across a page", 60, 0}, {"burst over a page", 150, 50}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.chans = []discord.GuildChannel{guildChannel(t, forumCh, discord.ChannelTypeGuildForum)}
			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			older := map[snowflake.ID]bool{}
			for i := range 170 + tc.burst { // 70 newer threads put a page end inside the burst
				at := base.Add(time.Duration(i) * time.Second)
				switch {
				case i >= 100 && i < 100+tc.burst: // one second, spanning pages
					at = base.Add(100*time.Second + time.Duration(i)*time.Millisecond)
				case i >= 100:
					at = at.Add(time.Hour)
				default:
					older[snowflake.ID(1000+i)] = true
				}
				var th discord.GuildThread
				decode(t, fmt.Sprintf(`{"id":"%d","type":11,"guild_id":"%d","parent_id":"%d",
					"thread_metadata":{"archived":true,"archive_timestamp":"%s"}}`, 1000+i, guildID, forumCh, at.Format(time.RFC3339Nano)), &th)
				f.public[forumCh] = append(f.public[forumCh], th)
			}
			ids, err := newSweep(f, time.Now()).targets(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if want := 170 + tc.burst - tc.lost; len(ids) != want {
				t.Errorf("listed %d threads, want %d", len(ids), want)
			}
			for _, id := range ids {
				delete(older, id)
			}
			if len(older) > 0 {
				t.Errorf("%d threads older than the burst were never listed", len(older))
			}
		})
	}
}

// Only text channels hold private threads; asking a news channel for them
// is a wasted request at best.
func TestPrivateThreadsListedOnlyForText(t *testing.T) {
	f := newFake()
	f.chans = []discord.GuildChannel{
		guildChannel(t, textCh, discord.ChannelTypeGuildText),
		guildChannel(t, 14, discord.ChannelTypeGuildNews),
	}
	if _, err := newSweep(f, time.Now()).targets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.privateAsked, []snowflake.ID{textCh}) {
		t.Errorf("asked %v for private threads, want [%d]", f.privateAsked, textCh)
	}
}
