package purge

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

// Blocks round-trip, and a realistic author's messages pack to under seven
// bytes each.
func TestPackRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var ids []snowflake.ID
	for range 5000 {
		// Gaps from a second to a week, evenly on a log scale as people
		// post in bursts and lulls, and every low bit pattern.
		at = at.Add(time.Duration(math.Exp(r.Float64()*math.Log(7*24*3600))) * time.Second)
		ids = append(ids, snowflake.New(at)|snowflake.ID(r.Uint64N(lowMask+1)))
	}
	ids = append(ids, ids[len(ids)-1]+1, ids[len(ids)-1]+lowMask+2) // same ms, then the low bits wrap
	slices.Sort(ids)
	ids = slices.Compact(ids)
	first, data := pack(ids)
	got, err := unpack(first, data)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, ids) {
		t.Fatal("the block didn't round-trip")
	}
	if per := float64(len(data)) / float64(len(ids)); per > 7 {
		t.Errorf("%.2f bytes an ID, want at most 7", per)
	}

	one, data := pack(ids[:1])
	if got, err := unpack(one, data); err != nil || !slices.Equal(got, ids[:1]) {
		t.Errorf("one ID: %v, %v", got, err)
	}
	for _, bad := range [][]byte{nil, {9}, {blockV1, 0x80}, {blockV1, 1, 2}} {
		if _, err := unpack(1, bad); !errors.Is(err, errBlock) {
			t.Errorf("unpack(%v): %v, want errBlock", bad, err)
		}
	}
}

// The point of the index: once a guild has been read, another member's
// purge reads nothing that hasn't changed, and a new message costs one page.
func TestSecondPurgeReadsOnlyWhatsNew(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	idx := newMemIndex()
	if err := newPass(f, now, idx).run(context.Background()); err != nil {
		t.Fatal(err)
	}

	f.reads.Store(0)
	p := newPass(f, now, idx, them)
	if err := p.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The voice channel was never readable, so it's tried again: that's all.
	if got := f.reads.Load(); got != 1 {
		t.Errorf("%d reads for a guild with nothing new, want 1", got)
	}
	if p.sc.scanned.Load() != 0 {
		t.Errorf("scanned %d, want 0", p.sc.scanned.Load())
	}
	for _, m := range f.msgs[textCh] {
		if !f.gone[m.ID] {
			t.Fatalf("message %d by %d survived both purges", m.ID, m.Author.ID)
		}
	}

	var s seq
	s.n = 1000
	late := s.at(now.Add(time.Minute), me)
	f.msgs[textCh] = append(f.msgs[textCh], late)
	f.reads.Store(0)
	if err := newPass(f, now.Add(2*time.Minute), idx).run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.reads.Load(); got != 2 { // the new page, and voice again
		t.Errorf("%d reads for one new message, want 2", got)
	}
	if !f.gone[late.ID] {
		t.Error("the new message survived")
	}
}

// A first read that stops part way keeps what it flushed: the next one
// starts there, not at the beginning.
func TestStoppedReadResumes(t *testing.T) {
	now := time.Now()
	f := newFake()
	f.chans = []discord.GuildChannel{guildChannel(t, textCh, discord.ChannelTypeGuildText)}
	var s seq
	for range 2*flushPages*page + 50 {
		f.msgs[textCh] = append(f.msgs[textCh], s.at(now.Add(-time.Hour), them))
	}
	idx := newMemIndex()
	ctx, cancel := context.WithCancel(context.Background())
	f.onRead = func(n int64) {
		if n == flushPages+10 {
			cancel()
		}
	}
	if err := newPass(f, now, idx).sc.run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want a cancel", err)
	}
	marks, _ := idx.marks(context.Background(), guildID)
	if want := f.msgs[textCh][flushPages*page-1].ID; marks[textCh].through != want {
		t.Fatalf("mark %d, want the last message of the first flush, %d", marks[textCh].through, want)
	}

	f.onRead = nil
	f.reads.Store(0)
	if err := newPass(f, now, idx).sc.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.reads.Load(); got != flushPages+1 {
		t.Errorf("%d reads to finish, want %d", got, flushPages+1)
	}
	if got := len(indexed(t, idx, them)); got != len(f.msgs[textCh]) {
		t.Errorf("indexed %d, want %d", got, len(f.msgs[textCh]))
	}
}

// A channel skua couldn't read keeps its mark, so once it can, it is read
// from the start and nothing in it is lost.
func TestUnreachableChannelIsReadOnceItOpens(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	f.denyRead[textCh] = true
	idx := newMemIndex()
	if err := newPass(f, now, idx).run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.gone[f.msgs[textCh][0].ID] {
		t.Fatal("deleted in a channel skua couldn't read")
	}
	f.denyRead[textCh] = false
	if err := newPass(f, now, idx).run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, m := range f.msgs[textCh] {
		if m.Author.ID == me && !f.gone[m.ID] {
			t.Fatal("a message in the reopened channel survived")
		}
	}
}

// A delete that fails stays indexed and goes on the next purge, without
// anything being read again.
func TestFailedDeleteIsRetried(t *testing.T) {
	now := time.Now()
	f := server(t, now)
	stuck := f.msgs[textCh][0].ID
	f.deleteErr[stuck] = refusal(500, 0)
	idx := newMemIndex()
	if err := newPass(f, now, idx).run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := indexed(t, idx, me); !slices.Equal(got, []snowflake.ID{stuck}) {
		t.Fatalf("indexed %v, want only the failed one", got)
	}
	delete(f.deleteErr, stuck)
	if err := newPass(f, now, idx).run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.gone[stuck] || len(indexed(t, idx, me)) != 0 {
		t.Fatalf("gone %v, indexed %v", f.gone[stuck], indexed(t, idx, me))
	}
}

// Deletes in different channels overlap: Discord's old-message limit is
// per channel, so that is where the speed is.
func TestLanesRunTogether(t *testing.T) {
	now := time.Now()
	f := newFake()
	var s seq
	for i := range 8 {
		ch := snowflake.ID(100 + i)
		f.chans = append(f.chans, guildChannel(t, ch, discord.ChannelTypeGuildText))
		for range 3 {
			f.msgs[ch] = append(f.msgs[ch], s.at(now.Add(-30*24*time.Hour), me))
		}
	}
	f.slow = 20 * time.Millisecond
	p := newPass(f, now, newMemIndex())
	if err := p.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.sw.deleted.Load() != 24 {
		t.Fatalf("deleted %d, want 24", p.sw.deleted.Load())
	}
	if got := f.peak.Load(); got < 4 {
		t.Errorf("at most %d deletes at once across 8 channels", got)
	}
}

// A parent whose archived threads were all listed is listed again only
// down to that moment.
func TestArchivedListingStopsAtTheLastFullOne(t *testing.T) {
	now := time.Now()
	f := newFake()
	f.chans = []discord.GuildChannel{guildChannel(t, forumCh, discord.ChannelTypeGuildForum)}
	for i := range 250 {
		f.public[forumCh] = append(f.public[forumCh], threadAt(t, snowflake.ID(1000+i), forumCh, now.Add(-time.Duration(250-i)*time.Hour)))
	}
	idx := newMemIndex()
	if err := newPass(f, now, idx).sc.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.listings.Load(); got != 3 {
		t.Fatalf("%d listings the first time, want 3", got)
	}
	f.listings.Store(0)
	if err := newPass(f, now.Add(time.Hour), idx).sc.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.listings.Load(); got != 1 {
		t.Errorf("%d listings with nothing archived since, want 1", got)
	}
}

func threadAt(t *testing.T, id, parent snowflake.ID, archived time.Time) discord.GuildThread {
	th := thread(t, id, parent)
	th.ThreadMetadata.ArchiveTimestamp = archived
	return th
}

// Two purges in one guild at once share one catch-up.
func TestCatchUpIsShared(t *testing.T) {
	m := newModule()
	f := server(t, time.Now())
	f.hold = make(chan struct{})
	a := m.newJob(f, guildID, []snowflake.ID{me})
	b := m.newJob(f, guildID, []snowflake.ID{them})
	b.sweep.cutoff = a.sweep.cutoff // asked at the same moment
	errs := make(chan error, 2)
	go func() { errs <- m.work(context.Background(), a) }()
	eventually(t, "the first catch-up started", func() bool { _, ok := m.catching.Load(snowflake.ID(guildID)); return ok })
	go func() { errs <- m.work(context.Background(), b) }()
	eventually(t, "the second purge joined it", func() bool { return b.scan.Load() != nil })
	close(f.hold)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if a.scan.Load() != b.scan.Load() {
		t.Error("two catch-ups for one guild at once")
	}
	for _, msg := range f.msgs[textCh] {
		if !f.gone[msg.ID] {
			t.Fatalf("message %d survived", msg.ID)
		}
	}
}

// A purge asked for after a catch-up began waits for another one, so a
// message sent in between is not missed.
func TestLaterPurgeCatchesUpAgain(t *testing.T) {
	m := newModule()
	f := server(t, time.Now())
	first := m.newJob(f, guildID, []snowflake.ID{them})
	if err := m.work(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	var s seq
	late := s.at(time.Now(), me)
	f.msgs[textCh] = append(f.msgs[textCh], late)
	time.Sleep(5 * time.Millisecond)
	j := m.newJob(f, guildID, []snowflake.ID{me})
	stale := &catchup{scan: first.scan.Load(), done: make(chan struct{})}
	close(stale.done)
	m.catching.Store(snowflake.ID(guildID), stale) // finished, but read before j began
	go func() {
		eventually(t, "the job waited on the stale catch-up", func() bool { return j.scan.Load() == stale.scan })
		m.catching.Delete(snowflake.ID(guildID))
	}()
	if err := m.work(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if !f.gone[late.ID] {
		t.Error("a message sent before the purge was asked for survived")
	}
}

// Leaving a guild forgets everything skua read there.
func TestGuildLeaveForgets(t *testing.T) {
	m := newModule()
	f := server(t, time.Now())
	if err := m.work(context.Background(), m.newJob(f, guildID, []snowflake.ID{them})); err != nil {
		t.Fatal(err)
	}
	if len(indexed(t, m.idx, me)) == 0 {
		t.Fatal("nothing indexed to forget")
	}
	m.OnEvent(&events.GuildLeave{GenericGuild: &events.GenericGuild{
		GenericEvent: events.NewGenericEvent(&bot.Client{Rest: f}, 0, 0), GuildID: guildID,
	}})
	eventually(t, "the guild was forgotten", func() bool {
		marks, _ := m.idx.marks(context.Background(), guildID)
		return len(marks) == 0 && len(indexedNoFail(m.idx, me)) == 0
	})
}

func indexedNoFail(idx index, author snowflake.ID) []block {
	bs, _ := idx.load(context.Background(), guildID, author)
	var out []block
	for _, b := range bs {
		out = append(out, b...)
	}
	return out
}

// The Postgres index does what the memory one does.
func TestPgIndex(t *testing.T) {
	db := testDB(t)
	idx := pgIndex{db}
	ctx := context.Background()
	g := freshGuild()
	ids := []snowflake.ID{snowflake.New(time.Now().Add(-time.Hour)), snowflake.New(time.Now())}
	if err := idx.flush(ctx, g, textCh, ids[1], map[snowflake.ID][]snowflake.ID{me: ids}); err != nil {
		t.Fatal(err)
	}
	// A flush with nothing found still moves the mark, and never backwards.
	if err := idx.flush(ctx, g, textCh, ids[0], nil); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Truncate(time.Microsecond)
	if err := idx.listed(ctx, g, forumCh, at); err != nil {
		t.Fatal(err)
	}
	marks, err := idx.marks(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if marks[textCh].through != ids[1] || !marks[forumCh].listed.Equal(at) {
		t.Fatalf("marks %+v", marks)
	}
	bs, err := idx.load(ctx, g, me)
	if err != nil || len(bs[textCh]) != 1 || !slices.Equal(bs[textCh][0].ids, ids) {
		t.Fatalf("load %+v, %v", bs, err)
	}
	if err := idx.settle(ctx, g, me, textCh, []snowflake.ID{bs[textCh][0].first}, ids[1:]); err != nil {
		t.Fatal(err)
	}
	if bs, _ := idx.load(ctx, g, me); len(bs[textCh]) != 1 || !slices.Equal(bs[textCh][0].ids, ids[1:]) {
		t.Fatalf("after settle %+v", bs)
	}
	if err := idx.drop(ctx, g); err != nil {
		t.Fatal(err)
	}
	if bs, _ := idx.load(ctx, g, me); len(bs) != 0 {
		t.Fatalf("after drop %+v", bs)
	}
	if marks, _ := idx.marks(ctx, g); len(marks) != 0 {
		t.Fatalf("marks after drop %+v", marks)
	}
}

// A whole purge through Postgres: read once, then another member's purge
// reads nothing.
func TestPurgeThroughPostgres(t *testing.T) {
	db := testDB(t)
	m := newModule()
	m.useDB(db)
	f := server(t, time.Now())
	g := freshGuild()
	f.guild = g
	run := func(author snowflake.ID) *job {
		j := m.newJob(f, g, []snowflake.ID{author})
		if err := m.work(context.Background(), j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	run(me)
	f.reads.Store(0)
	j := run(them)
	if got := f.reads.Load(); got != 1 { // voice, unreadable, tried again
		t.Errorf("%d reads the second time, want 1", got)
	}
	var n atomic.Int64
	n.Store(j.sweep.deleted.Load())
	if n.Load() == 0 {
		t.Error("the second member's purge deleted nothing")
	}
	for ch, msgs := range f.msgs {
		for _, msg := range msgs {
			if ch != voiceCh && !f.gone[msg.ID] {
				t.Fatalf("message %d survived", msg.ID)
			}
		}
	}
}
