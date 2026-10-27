package spam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/flood"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/sanction"
)

// fakeRest is the REST spam calls: roles, the deletes and its line.
type fakeRest struct {
	rest.Rest
	mu            sync.Mutex
	roles         []discord.Role
	rolesErr      error
	deleted       []snowflake.ID
	deleteErr     error
	timeout       *time.Time
	timeoutErr    error
	timeoutErrors []error
	bulk          [][]snowflake.ID
	bulkErr       error
	gone          map[snowflake.ID]bool // deleted before skua got to them
	posted        []discord.MessageCreate
	done          chan struct{}
}

func (f *fakeRest) GetRoles(snowflake.ID, ...rest.RequestOpt) ([]discord.Role, error) {
	return f.roles, f.rolesErr
}

func (f *fakeRest) DeleteMessage(_, id snowflake.ID, _ ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if f.gone[id] {
		return &rest.Error{Response: &http.Response{StatusCode: http.StatusNotFound}}
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeRest) UpdateMember(_, _ snowflake.ID, update discord.MemberUpdate, _ ...rest.RequestOpt) (*discord.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.timeoutErrors) > 0 {
		err := f.timeoutErrors[0]
		f.timeoutErrors = f.timeoutErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	if f.timeoutErr != nil {
		return nil, f.timeoutErr
	}
	if update.CommunicationDisabledUntil.Value != nil {
		until := *update.CommunicationDisabledUntil.Value
		f.timeout = &until
	}
	return &discord.Member{}, nil
}

func (f *fakeRest) BulkDeleteMessages(_ snowflake.ID, ids []snowflake.ID, _ ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bulkErr != nil {
		return f.bulkErr
	}
	f.bulk = append(f.bulk, ids)
	return nil
}

func (f *fakeRest) CreateMessage(_ snowflake.ID, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	f.posted = append(f.posted, m)
	f.mu.Unlock()
	if f.done != nil {
		close(f.done)
	}
	return &discord.Message{}, nil
}

type bird struct {
	got []sanction.Sentence
	err error
	// hold, when set, keeps the sanction applying until it closes: the
	// fetch a real bird waits on.
	hold chan struct{}
}

func (*bird) Name() string { return "bird" }
func (b *bird) Apply(_ context.Context, s sanction.Sentence) (string, error) {
	if b.hold != nil {
		<-b.hold
	}
	b.got = append(b.got, s)
	return "a common blackbird for 10m", b.err
}

type history struct{ priors int }

func (h *history) Priors(context.Context, snowflake.ID, snowflake.ID, time.Time) (int, error) {
	return h.priors, nil
}
func (h *history) Record(context.Context, sanction.Sentence) error { h.priors++; return nil }

func module(t *testing.T, s ...sanction.Sanction) *Module {
	t.Helper()
	m, err := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), nil, &history{}, func(snowflake.ID) (snowflake.ID, bool) { return 1, true }, core.Admins{2}, s...)
	if err != nil {
		t.Fatal(err)
	}
	m.Admin = func(discord.Interaction) bool { return true }
	return m
}

var t0 = time.Date(2026, 10, 19, 12, 18, 0, 0, time.UTC)

// message is member 7 in channel 4 of guild 3, the nth of a burst.
func message(r rest.Rest, n int, edit func(*discord.Message)) *events.GuildMessageCreate {
	c := &bot.Client{Rest: r, ApplicationID: 2}
	msg := discord.Message{
		ID: snowflake.ID(100 + n), ChannelID: 4, Type: discord.MessageTypeReply, Content: "hi",
		CreatedAt: t0.Add(time.Duration(n) * time.Second),
		Author:    discord.User{ID: 7, Username: "lachy_"}, Member: &discord.Member{RoleIDs: []snowflake.ID{50}},
		Mentions: []discord.User{{ID: 9}},
	}
	if edit != nil {
		edit(&msg)
	}
	return &events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{
		GenericEvent: events.NewGenericEvent(c, 0, 0), MessageID: msg.ID, Message: msg, ChannelID: msg.ChannelID, GuildID: 3,
	}}
}

func TestTheBahamasBurstBecomesABird(t *testing.T) {
	// The bird is still being fetched while the rest of the seventeen
	// arrive, as it is live; once it holds, the bird replaces anything later.
	b := &bird{hold: make(chan struct{})}
	m := module(t, b)
	if err := m.pick(context.Background(), 3, []snowflake.ID{4}); err != nil {
		t.Fatal(err)
	}
	r := &fakeRest{done: make(chan struct{}), roles: []discord.Role{{ID: 3, Permissions: discord.PermissionSendMessages}, {ID: 50}, {ID: 51, Permissions: discord.PermissionAdministrator}}}
	for n := range 17 {
		m.OnEvent(message(r, n, nil))
	}
	close(b.hold)
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("no bird")
	}
	if len(b.got) != 1 || b.got[0].Rule != "spam" || b.got[0].For != 10*time.Minute || b.got[0].User != 7 {
		t.Fatalf("sentenced %+v", b.got)
	}
	// Every one of the seventeen comes down: the burst, then the rest.
	r.mu.Lock()
	all := append([]snowflake.ID{}, r.deleted...)
	for _, b := range r.bulk {
		all = append(all, b...)
	}
	r.mu.Unlock()
	for n := range 17 {
		if !slices.Contains(all, snowflake.ID(100+n)) {
			t.Fatalf("message %d stayed up: cleared %v", 100+n, all)
		}
	}
	if got := r.posted[0].Content; got != `-# @lachy\_ got too loud and is a common blackbird for 10m` || r.posted[0].AllowedMentions == nil {
		t.Fatalf("said %q", got)
	}
	if rep := m.Report(3); rep.Rows[0][1] != "1 channel" || rep.Rows[1][1] != "1 since she woke" || len(rep.Controls) != 3 {
		t.Fatalf("report %+v", rep)
	}
}

// The melting pot: one sticker six times, no text.
func TestTheSameStickerBecomesABird(t *testing.T) {
	b := &bird{}
	m := module(t, b)
	if err := m.pick(context.Background(), 3, []snowflake.ID{4}); err != nil {
		t.Fatal(err)
	}
	r := &fakeRest{done: make(chan struct{}), roles: []discord.Role{{ID: 50}}}
	for n := range 6 {
		m.OnEvent(message(r, n, func(msg *discord.Message) {
			msg.Type, msg.Content, msg.Mentions = discord.MessageTypeDefault, "", nil
			msg.StickerItems = []discord.MessageSticker{{ID: 1234, Name: "crying"}}
		}))
	}
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("six of the same sticker didn't trip")
	}
}

func TestCarried(t *testing.T) {
	png := "image/png"
	text, media := carried(discord.Message{
		Content:      "look",
		StickerItems: []discord.MessageSticker{{ID: 7}},
		Attachments:  []discord.Attachment{{Size: 48211, ContentType: &png, Filename: "image.png"}, {Size: 9}},
		MessageSnapshots: []discord.MessageSnapshot{{Message: discord.PartialMessage{
			Content: " again", StickerItems: []discord.MessageSticker{{ID: 8}}, Attachments: []discord.Attachment{{Size: 10}},
		}}},
	})
	if text != "look again" || strings.Join(media, ",") != "s:7,s:8,a:48211:image/png,a:9:,a:10:" {
		t.Fatalf("carried %q %q", text, media)
	}
}

func TestOnlyWatchedChannelsAndRealMembers(t *testing.T) {
	b := &bird{}
	m := module(t, b)
	r := &fakeRest{}
	hook := snowflake.ID(1)
	for n := range 30 {
		m.OnEvent(message(r, n, nil)) // channel 4 isn't watched
		m.OnEvent(message(r, n, func(msg *discord.Message) { msg.Author.Bot = true }))
		m.OnEvent(message(r, n, func(msg *discord.Message) { msg.WebhookID = &hook }))
		m.OnEvent(message(r, n, func(msg *discord.Message) { msg.Type = discord.MessageTypeUserJoin }))
	}
	m.OnEvent(&events.GuildReady{})
	if v, ok := m.trips.Load(snowflake.ID(3)); ok {
		t.Fatalf("tripped %v", v)
	}
	if err := m.everywhere(context.Background(), 3, true); err != nil || !m.watched(3, 77) || m.Report(3).Rows[0][1] != "every channel" {
		t.Fatal("everywhere didn't watch everywhere")
	}
	if err := m.everywhere(context.Background(), 3, false); err != nil || m.watched(3, 77) || m.Report(3).Rows[0][1] != "nowhere yet" {
		t.Fatal("everywhere off still watched")
	}
}

func TestStaffAreLetGo(t *testing.T) {
	m := module(t)
	r := &fakeRest{roles: []discord.Role{{ID: 50, Permissions: discord.PermissionManageMessages}}}
	member := &discord.Member{RoleIDs: []snowflake.ID{50}}
	cases := []struct {
		name string
		user snowflake.ID
		mem  *discord.Member
		r    *fakeRest
		is   bool
		err  bool
	}{
		{"bootstrap", 2, nil, r, true, false},
		{"owner", 1, nil, r, true, false},
		{"a mod", 7, member, r, true, false},
		{"everyone can moderate", 7, &discord.Member{}, &fakeRest{roles: []discord.Role{{ID: 3, Permissions: discord.PermissionBanMembers}}}, true, false},
		{"a member", 7, &discord.Member{RoleIDs: []snowflake.ID{60}}, r, false, false},
		{"no member", 7, nil, r, false, true},
		{"roles unread", 7, member, &fakeRest{rolesErr: errors.New("x")}, false, true},
	}
	for _, c := range cases {
		is, err := m.staff(c.r, 3, c.user, c.mem)
		if is != c.is || (err != nil) != c.err {
			t.Errorf("%s: staff %v err %v", c.name, is, err)
		}
	}
	// A trip that can't read the roles is let go, with nothing deleted.
	b := &bird{}
	m = module(t, b)
	r = &fakeRest{rolesErr: errors.New("x")}
	m.act(r, 3, message(r, 0, nil).Message, []flood.Seen{{Channel: 4, ID: 100}})
	if len(b.got)+len(r.deleted)+len(r.bulk) != 0 {
		t.Fatal("acted on a member whose roles couldn't be read")
	}
}

func TestClearSplitsByChannel(t *testing.T) {
	m := module(t)
	r := &fakeRest{}
	gone := map[snowflake.ID]bool{}
	n := m.clear(context.Background(), r, 3, []flood.Seen{{Channel: 4, ID: 1}, {Channel: 5, ID: 2}, {Channel: 4, ID: 3}}, gone)
	if n != 3 || len(r.bulk) != 1 || len(r.bulk[0]) != 2 || len(r.deleted) != 1 || r.deleted[0] != 2 {
		t.Fatalf("cleared %d: bulk %v single %v", n, r.bulk, r.deleted)
	}
	// What is already cleared isn't asked for twice.
	if n := m.clear(context.Background(), r, 3, []flood.Seen{{Channel: 4, ID: 1}, {Channel: 4, ID: 9}}, gone); n != 1 || r.deleted[len(r.deleted)-1] != 9 {
		t.Fatalf("second pass cleared %d, single %v", n, r.deleted)
	}
}

func TestClearBulkDeletesInDiscordSizedChunks(t *testing.T) {
	m := module(t)
	r := &fakeRest{}
	burst := make([]flood.Seen, 250)
	for i := range burst {
		burst[i] = flood.Seen{Channel: 4, ID: snowflake.ID(i + 1)}
	}
	n := m.clear(context.Background(), r, 3, burst, map[snowflake.ID]bool{})
	var sizes []int
	for _, batch := range r.bulk {
		sizes = append(sizes, len(batch))
	}
	if n != 250 || !slices.Equal(sizes, []int{100, 100, 50}) {
		t.Fatalf("cleared %d in chunks %v", n, sizes)
	}
}

func TestCleanupFailureTimesOutForTheBirdSentence(t *testing.T) {
	m := module(t, &bird{})
	m.now = func() time.Time { return t0 }
	r := &fakeRest{roles: []discord.Role{{ID: 50}}, deleteErr: errors.New("delete refused")}
	msg := message(r, 0, nil).Message
	m.act(r, 3, msg, []flood.Seen{{Channel: 4, ID: msg.ID}})
	if r.timeout == nil || !r.timeout.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("timeout until %v, want %v", r.timeout, t0.Add(10*time.Minute))
	}
}

func TestFailedTimeoutIsRetriedWithoutClosingCleanup(t *testing.T) {
	var logs strings.Builder
	m := module(t, &bird{})
	m.log = slog.New(slog.NewTextHandler(&logs, nil))
	r := &fakeRest{
		roles:         []discord.Role{{ID: 50}},
		deleteErr:     errors.New("delete refused"),
		timeoutErrors: []error{errors.New("temporary Discord error"), nil},
	}
	msg := message(r, 0, nil).Message
	m.act(r, 3, msg, []flood.Seen{{Channel: 4, ID: msg.ID}})
	report := m.Report(3)
	if r.timeout == nil || report.Rows[2][1] != "1 since she woke" || !strings.Contains(logs.String(), "spam_containment_failure") || !strings.Contains(logs.String(), "timeout_failed") {
		t.Fatalf("failed timeout wasn't retried and surfaced: timeout %v report %+v logs %q", r.timeout, report, logs.String())
	}
}

// One message the member already deleted makes Discord refuse the whole
// bulk delete; the rest still come down, one at a time.
func TestClearFallsBackWhenABulkDeleteIsRefused(t *testing.T) {
	m := module(t)
	r := &fakeRest{bulkErr: &rest.Error{Response: &http.Response{StatusCode: http.StatusBadRequest}}, gone: map[snowflake.ID]bool{2: true}}
	n := m.clear(context.Background(), r, 3, []flood.Seen{{Channel: 4, ID: 1}, {Channel: 4, ID: 2}, {Channel: 4, ID: 3}}, map[snowflake.ID]bool{})
	if n != 3 || len(r.deleted) != 2 || r.deleted[0] != 1 || r.deleted[1] != 3 {
		t.Fatalf("cleared %d, single %v", n, r.deleted)
	}
	if !missing(&rest.Error{Response: &http.Response{StatusCode: 404}}) || missing(errors.New("x")) || missing(&rest.Error{}) {
		t.Fatal("missing")
	}
}

// Whatever they send while the bird is being fetched comes down too.
func TestTheSweepAfterTheSanction(t *testing.T) {
	m := module(t)
	r := &fakeRest{roles: []discord.Role{{ID: 50}}}
	slow := sanctionFunc(func() {
		// Two more while the sanction is applied, one in another channel.
		m.OnEvent(message(r, 20, nil))
		m.OnEvent(message(r, 21, func(msg *discord.Message) { msg.ChannelID = 5 }))
	})
	m.sanctions = []sanction.Sanction{slow}
	if err := m.everywhere(context.Background(), 3, true); err != nil {
		t.Fatal(err)
	}
	var burst []flood.Seen
	var tripped discord.Message
	for n := range 17 {
		e := message(r, n, nil)
		if trip, b := m.flood.Observe(flood.Message{Guild: 3, User: 7, Channel: 4, ID: e.Message.ID, At: e.Message.CreatedAt, Text: "hi", Pings: []snowflake.ID{9}}); trip {
			burst, tripped = b, e.Message
			break
		}
	}
	m.act(r, 3, tripped, burst)
	all := append([]snowflake.ID{}, r.deleted...)
	for _, b := range r.bulk {
		all = append(all, b...)
	}
	for _, want := range []snowflake.ID{120, 121, tripped.ID} {
		if !slices.Contains(all, want) {
			t.Errorf("message %d stayed up: deleted %v", want, all)
		}
	}
}

type sanctionFunc func()

func (sanctionFunc) Name() string { return "bird" }
func (f sanctionFunc) Apply(context.Context, sanction.Sentence) (string, error) {
	f()
	return "a bird for 10m", nil
}

func TestAFailedSanctionSaysNothing(t *testing.T) {
	b := &bird{err: errors.New("xeno-canto away")}
	m := module(t, b)
	r := &fakeRest{}
	m.act(r, 3, message(r, 0, nil).Message, nil)
	if len(b.got) != 1 || len(r.posted) != 0 {
		t.Fatalf("sentenced %d, posted %d", len(b.got), len(r.posted))
	}
}

func TestControls(t *testing.T) {
	m := module(t)
	press := func(id string, values ...string) []discord.MessageCreate {
		e, sent := coretest.Button(t, id, func(p map[string]any) {
			if values != nil {
				p["data"] = map[string]any{"custom_id": id, "component_type": 8, "values": values}
			}
		})
		if err := m.press(context.Background(), e); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return *sent
	}
	if s := press("spam:watch", "11", "12"); !strings.Contains(s[0].Content, "<#11> <#12>") || !m.watched(3, 11) || !m.watched(3, 12) {
		t.Fatalf("picked %+v", s)
	}
	press("spam:watch", "12")
	if m.watched(3, 11) || !m.watched(3, 12) {
		t.Fatal("a second pick didn't replace the first")
	}
	if s := press("spam:all:on"); !strings.Contains(s[0].Content, "every channel") || !m.watched(3, 99) {
		t.Fatal("everywhere")
	}
	if s := press("spam:watch", []string{}...); !strings.Contains(s[0].Content, "no channel") {
		t.Fatalf("cleared %+v", s)
	}
	ctl := m.controls(3)
	if row := ctl[2].(discord.ActionRowComponent); row.Components[0].(discord.ButtonComponent).CustomID != "spam:all:off" {
		t.Fatal("the button doesn't offer to switch everywhere off")
	}
	m.Admin = func(discord.Interaction) bool { return false }
	e, _ := coretest.Button(t, "spam:all:on", nil)
	if err := m.press(context.Background(), e); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member chose where she watches: %v", err)
	}
}

// fakeDB records what spam wrote and serves the scope back.
type fakeDB struct {
	rows  [][2]int64
	execs []string
	err   error
}

func (d *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.execs = append(d.execs, fmt.Sprint(sql, args))
	return pgconn.CommandTag{}, d.err
}

func (d *fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return &rows{rows: d.rows, i: -1, err: d.err}, d.err
}

type rows struct {
	pgx.Rows
	rows [][2]int64
	i    int
	err  error
}

// Next stops at once on an error, as pgx's rows do.
func (r *rows) Next() bool { r.i++; return r.err == nil && r.i < len(r.rows) }
func (r *rows) Scan(dest ...any) error {
	*dest[0].(*int64), *dest[1].(*int64) = r.rows[r.i][0], r.rows[r.i][1]
	return nil
}
func (*rows) Close()                        {}
func (r *rows) Err() error                  { return r.err }
func (*rows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func TestTheScopeOutlivesARestart(t *testing.T) {
	db := &fakeDB{rows: [][2]int64{{3, 4}, {3, 5}, {8, 0}}}
	m, err := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), db, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.watched(3, 4) || !m.watched(3, 5) || m.watched(3, 6) || !m.watched(8, 123) {
		t.Fatal("the scope didn't load")
	}
	if err := m.pick(context.Background(), 3, []snowflake.ID{6}); err != nil || len(db.execs) != 1 || !strings.Contains(db.execs[0], "unnest") {
		t.Fatalf("pick wrote %v (%v)", db.execs, err)
	}
	db.err = errors.New("down")
	if m.everywhere(context.Background(), 3, true) == nil || m.watched(3, 99) {
		t.Fatal("memory took a scope the store refused")
	}
	if _, err := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), db, nil, nil, nil); err == nil {
		t.Fatal("a scope that couldn't be read booted")
	}
}
