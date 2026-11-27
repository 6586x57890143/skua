package games

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/game"
	"github.com/6586x57890143/skua/internal/game/count"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/store"
)

const (
	guildID = 3
	chanID  = 4
)

var t0 = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

// fakeRest is the REST the games call.
type fakeRest struct {
	rest.Rest
	mu        sync.Mutex
	reacts    []string // "message emoji"
	posts     []discord.MessageCreate
	timeouts  []time.Duration
	reactErr  error
	memberErr error
	postErr   error
	hold      chan struct{} // when set, the first reaction waits on it
	held      chan struct{} // closed once that reaction is waiting
	pages     [][]discord.Message
	afters    []snowflake.ID
	getErr    error
	gate      chan struct{} // when set, reading back waits on it
	updates   []discord.MessageUpdate
	now       time.Time
}

func (f *fakeRest) AddReaction(_, msg snowflake.ID, emoji string, _ ...rest.RequestOpt) error {
	f.mu.Lock()
	hold := f.hold
	f.hold = nil
	f.mu.Unlock()
	if hold != nil {
		close(f.held)
		<-hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reacts = append(f.reacts, msg.String()+" "+emoji)
	return f.reactErr
}

func (f *fakeRest) CreateMessage(_ snowflake.ID, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, m)
	return &discord.Message{}, f.postErr
}

func (f *fakeRest) UpdateMember(_, _ snowflake.ID, u discord.MemberUpdate, _ ...rest.RequestOpt) (*discord.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timeouts = append(f.timeouts, (*u.CommunicationDisabledUntil.Value).Sub(f.now).Round(time.Minute))
	return &discord.Member{}, f.memberErr
}

func (f *fakeRest) GetMessages(_, _, _, after snowflake.ID, _ int, _ ...rest.RequestOpt) ([]discord.Message, error) {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.afters = append(f.afters, after)
	if f.getErr != nil || len(f.pages) == 0 {
		return nil, f.getErr
	}
	p := f.pages[0]
	f.pages = f.pages[1:]
	return p, nil
}

func (f *fakeRest) UpdateInteractionResponse(_ snowflake.ID, _ string, u discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, u)
	return &discord.Message{}, nil
}

func (f *fakeRest) reactions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reacts)
}

func (f *fakeRest) said() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	for _, p := range f.posts {
		b.WriteString(coretest.Text(p.Content, p.Components) + "\n")
	}
	return b.String()
}

// table is the module counting in channel 4 of guild 3, a message a second.
type table struct {
	t  *testing.T
	m  *Module
	r  *fakeRest
	at time.Time
}

func newTable(t *testing.T, r count.Ruleset) *table {
	t.Helper()
	m, err := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Admin = func(discord.Interaction) bool { return true }
	m.en.Open(guildID, chanID, r)
	f := &fakeRest{now: t0}
	m.now = func() time.Time { return f.now }
	return &table{t: t, m: m, r: f, at: t0}
}

func (tb *table) msg(user int, text string) discord.Message {
	tb.at = tb.at.Add(time.Second)
	return discord.Message{ID: snowflake.New(tb.at), ChannelID: chanID, Type: discord.MessageTypeDefault, Content: text,
		Author: discord.User{ID: snowflake.ID(user), Username: "steve"}}
}

func (tb *table) event(msg discord.Message) *events.GuildMessageCreate {
	return &events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{
		GenericEvent: events.NewGenericEvent(&bot.Client{Rest: tb.r}, 0, 0), MessageID: msg.ID, Message: msg, ChannelID: msg.ChannelID, GuildID: guildID,
	}}
}

// say sends text as user and waits for every answer to land.
func (tb *table) say(user int, text string) discord.Message {
	tb.t.Helper()
	msg := tb.msg(user, text)
	tb.m.OnEvent(tb.event(msg))
	if !tb.m.Wait(5 * time.Second) {
		tb.t.Fatal("answers never landed")
	}
	return msg
}

func (tb *table) count(to int) {
	s, _ := tb.m.en.Channel(chanID)
	for n := int(s.N) + 1; n <= to; n++ {
		tb.say(100+n%2, strconv.Itoa(n))
	}
}

func TestModuleShape(t *testing.T) {
	m, _ := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), nil)
	if m.Name() != Name || len(m.Commands()) != 2 || len(m.Components()) != 1 {
		t.Fatal("shape")
	}
	if m.Want().Required == 0 || m.Perms()&discord.PermissionAddReactions == 0 {
		t.Fatal("asks for too little")
	}
	h := m.Help()
	if h.Line == "" || strings.ToLower(h.About) != h.About {
		t.Fatal("help")
	}
}

func TestCountingReacts(t *testing.T) {
	tb := newTable(t, count.Standard())
	one := tb.say(7, "1")
	two := tb.say(8, "2")
	chat := tb.say(7, "nice")
	got := tb.r.reactions()
	want := []string{one.ID.String() + " ✅", two.ID.String() + " ✅"}
	if !slices.Equal(got, want) {
		t.Fatalf("reactions %v, want %v (chat %s)", got, want, chat.ID)
	}
	// Bots, webhooks and system messages are never played.
	for _, edit := range []func(*discord.Message){
		func(m *discord.Message) { m.Author.Bot = true },
		func(m *discord.Message) { m.Author.System = true },
		func(m *discord.Message) { id := snowflake.ID(1); m.WebhookID = &id },
		func(m *discord.Message) { m.Type = discord.MessageTypeChannelPinnedMessage },
	} {
		msg := tb.msg(9, "3")
		edit(&msg)
		tb.m.OnEvent(tb.event(msg))
	}
	tb.m.Wait(time.Second)
	if s, _ := tb.m.en.Channel(chanID); s.N != 2 {
		t.Fatalf("a bot's or webhook's count was played: %d", s.N)
	}
	// A channel that isn't counting is left alone.
	other := tb.msg(7, "3")
	other.ChannelID = 99
	tb.m.OnEvent(tb.event(other))
	tb.m.Wait(time.Second)
	if len(tb.r.reactions()) != 2 {
		t.Fatal("reacted in a channel that doesn't count")
	}
}

func TestBreakPostsACardAndTimesOut(t *testing.T) {
	r := count.Standard()
	r.Timeout = baseTimeout
	tb := newTable(t, r)
	tb.count(3)
	bad := tb.say(7, "**5** lol")
	if got := tb.r.reactions(); got[len(got)-1] != bad.ID.String()+" ❌" {
		t.Fatalf("%v", got)
	}
	said := tb.r.said()
	for _, want := range []string{"<@7> said **\\*\\*5\\*\\***, but it was **4**", "run", "3", "sits out 30m", "start again at **1**"} {
		if !strings.Contains(said, want) {
			t.Fatalf("card lacks %q:\n%s", want, said)
		}
	}
	if len(tb.r.timeouts) != 1 || tb.r.timeouts[0] != 30*time.Minute {
		t.Fatalf("timeouts %v", tb.r.timeouts)
	}
	// Breaking again in the week doubles it.
	tb.say(7, "1")
	tb.say(7, "2")
	if tb.r.timeouts[1] != time.Hour {
		t.Fatalf("timeouts %v", tb.r.timeouts)
	}
	if !strings.Contains(tb.r.said(), "counted twice in a row") {
		t.Fatal(tb.r.said())
	}
	for _, p := range tb.r.posts {
		if p.AllowedMentions == nil || len(p.AllowedMentions.Parse) != 0 {
			t.Fatal("a post could ping")
		}
	}
}

func TestSavesAndLivesAreSaid(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.count(250) // the channel earns a save at 250
	tb.say(7, "999")
	if !strings.Contains(tb.r.said(), "a save from the channel caught it, 0 saves left. the next number is **251**") {
		t.Fatal(tb.r.said())
	}
	lv, _ := count.Preset("lives")
	tl := newTable(t, lv)
	tl.count(2)
	tl.say(7, "9")
	if !strings.Contains(tl.r.said(), "lost a life, 2 lives left. the next number is **3**") {
		t.Fatal(tl.r.said())
	}
}

func TestMilestonesAndExtras(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.count(100)
	s, _ := tb.m.en.Channel(chanID)
	var on []string
	for _, r := range tb.r.reactions() {
		if id, emoji, _ := strings.Cut(r, " "); id == strconv.FormatUint(s.CountMsg, 10) {
			on = append(on, emoji)
		}
	}
	if len(on) != 3 || on[1] != "🚩" || on[2] != "⬆️" {
		t.Fatalf("the hundredth wears %v", on)
	}
	if !strings.Contains(tb.r.said(), "flock reached level **1**") {
		t.Fatal(tb.r.said())
	}
}

func TestSnipeAndCooldownWearAnHourglass(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.say(7, "1")
	tb.at = tb.at.Add(-800 * time.Millisecond)
	snipe := tb.say(8, "1")
	if got := tb.r.reactions(); got[len(got)-1] != snipe.ID.String()+" ⌛" {
		t.Fatalf("%v", got)
	}
}

// A fast channel's ticks keep up: while one reaction is in flight, only
// the newest plain one waits, and every extra waits in order.
func TestPlainReactionsCoalesce(t *testing.T) {
	tb := newTable(t, count.Standard())
	hold := make(chan struct{})
	tb.r.hold, tb.r.held = hold, make(chan struct{})
	first := tb.msg(7, "1")
	tb.m.OnEvent(tb.event(first))
	<-tb.r.held
	var last discord.Message
	for n := 2; n <= 6; n++ {
		last = tb.msg(100+n%2, strconv.Itoa(n))
		tb.m.OnEvent(tb.event(last))
	}
	close(hold)
	tb.m.Wait(5 * time.Second)
	// The newest count is on a combo by now, so it wears a flame.
	want := []string{first.ID.String() + " ✅", last.ID.String() + " 🔥"}
	if got := tb.r.reactions(); !slices.Equal(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
}

func TestRefusedChannelIsLeftAlone(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.r.reactErr = &rest.Error{Response: &http.Response{StatusCode: http.StatusForbidden}}
	tb.say(7, "1")
	tb.say(8, "2")
	if n := len(tb.r.reactions()); n != 1 {
		t.Fatalf("asked %d times after a 403", n)
	}
	tb.r.now = tb.r.now.Add(refuseFor + time.Second)
	tb.say(7, "3")
	if n := len(tb.r.reactions()); n != 2 {
		t.Fatal("never tried again after an hour")
	}
}

// A member who blocked skua refuses only their own reaction: the channel
// goes on getting its ticks.
func TestBlockedMemberDoesntParkTheChannel(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.r.reactErr = &rest.Error{Response: &http.Response{StatusCode: http.StatusForbidden}, Code: reactionBlocked}
	tb.say(7, "1")
	tb.r.reactErr = nil
	tb.say(8, "2")
	if n := len(tb.r.reactions()); n != 2 {
		t.Fatalf("%d reactions", n)
	}
}

func TestTimeoutThatCantBeServed(t *testing.T) {
	r := count.Standard()
	r.Timeout = baseTimeout
	tb := newTable(t, r)
	tb.r.memberErr = &rest.Error{Response: &http.Response{StatusCode: http.StatusForbidden}, Code: 50013}
	tb.count(2)
	tb.say(7, "9")
	if !strings.Contains(tb.r.said(), "takes moderate members") || strings.Contains(tb.r.said(), "sits out") {
		t.Fatal(tb.r.said())
	}
	// Unserved, it doesn't double the next.
	tb.r.memberErr = nil
	tb.count(2)
	tb.say(7, "9")
	if len(tb.r.timeouts) != 2 || tb.r.timeouts[1] != baseTimeout {
		t.Fatalf("%v", tb.r.timeouts)
	}
}

func TestDeletedOrEditedCountIsCalledOut(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.say(7, "1")
	two := tb.say(8, "2")
	gen := events.NewGenericEvent(&bot.Client{Rest: tb.r}, 0, 0)
	edit := two
	edit.Content = "2 (still)"
	tb.m.OnEvent(&events.GuildMessageUpdate{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: gen, MessageID: two.ID, Message: edit, ChannelID: chanID, GuildID: guildID}})
	tb.m.Wait(time.Second)
	if tb.r.said() != "" {
		t.Fatal("an edit that still says the count was called out")
	}
	edit.Content = "7"
	tb.m.OnEvent(&events.GuildMessageUpdate{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: gen, MessageID: two.ID, Message: edit, ChannelID: chanID, GuildID: guildID}})
	tb.m.OnEvent(&events.GuildMessageDelete{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: gen, MessageID: two.ID, ChannelID: chanID, GuildID: guildID}})
	tb.m.Wait(time.Second)
	said := tb.r.said()
	if strings.Count(said, "the next number is **3**") != 1 || !strings.Contains(said, "<@8> edited their **2**") {
		t.Fatalf("%q", said)
	}
	// An older message going is nothing to say.
	tb.m.OnEvent(&events.GuildMessageDelete{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: gen, MessageID: 1, ChannelID: chanID, GuildID: guildID}})
	tb.m.Wait(time.Second)
	if tb.r.said() != said {
		t.Fatal("called out a message that wasn't the last count")
	}
}

func TestCatchUpForgivesWhatNobodySaw(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.say(7, "1")
	// Away: 2 and 3 were counted, someone missed, then 4.
	away := []discord.Message{tb.msg(8, "2"), tb.msg(7, "3"), tb.msg(9, "99"), tb.msg(8, "4")}
	four := away[3]
	slices.Reverse(away) // Discord gives newest first
	tb.r.pages = [][]discord.Message{away}
	// A live count arriving while she reads back waits its turn.
	live := tb.msg(7, "5")
	b := &backlog{}
	tb.m.catching.Store(snowflake.ID(chanID), b)
	tb.m.OnEvent(tb.event(live))
	if len(b.msgs) != 1 {
		t.Fatal("a live count was played before the read back")
	}
	tb.m.catching.Delete(snowflake.ID(chanID))
	b.mu.Lock()
	b.msgs = nil
	b.mu.Unlock()

	tb.m.OnEvent(&events.GuildReady{GenericGuild: &events.GenericGuild{GenericEvent: events.NewGenericEvent(&bot.Client{Rest: tb.r}, 0, 0), GuildID: guildID}})
	tb.m.Wait(5 * time.Second)
	s, _ := tb.m.en.Channel(chanID)
	if s.N != 4 || s.Run != 1 {
		t.Fatalf("caught up to %d in run %d", s.N, s.Run)
	}
	got := tb.r.reactions()
	if got[len(got)-1] != four.ID.String()+" ✅" {
		t.Fatalf("the last count caught wasn't ticked: %v, want %s", got, four.ID)
	}
	if tb.r.said() != "" {
		t.Fatal(tb.r.said())
	}
	tb.say(7, "5")
	if s, _ := tb.m.en.Channel(chanID); s.N != 5 {
		t.Fatal("counting didn't go on after the read back")
	}
}

// What arrives while she reads back waits and is played after, in order:
// the read is set up before GuildReady returns, so even the first live
// message can't get ahead of it.
func TestCatchUpPlaysTheBacklogAfter(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.say(7, "1")
	gone := tb.msg(8, "2")
	waiting := tb.msg(7, "3")
	hold := make(chan struct{})
	tb.r.pages = [][]discord.Message{{gone}}
	tb.r.gate = hold
	tb.m.OnEvent(&events.GuildReady{GenericGuild: &events.GenericGuild{GenericEvent: events.NewGenericEvent(&bot.Client{Rest: tb.r}, 0, 0), GuildID: guildID}})
	// A second ready while the first is reading is left to it.
	tb.m.catchUp(tb.r, guildID)
	tb.m.OnEvent(tb.event(waiting))
	close(hold)
	tb.m.Wait(5 * time.Second)
	if s, _ := tb.m.en.Channel(chanID); s.N != 3 {
		t.Fatalf("%d", s.N)
	}
	if _, ok := tb.m.catching.Load(snowflake.ID(chanID)); ok {
		t.Fatal("still catching")
	}
}

func TestCatchUpTooLong(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.say(7, "1")
	n := 1
	for range catchPages {
		var page []discord.Message
		for range 100 {
			n++
			page = append(page, tb.msg(100+n%2, strconv.Itoa(n)))
		}
		tb.r.pages = append(tb.r.pages, page)
	}
	// Counting went on past what she read: 900 was sent live while she read.
	live := tb.msg(7, "900")
	tb.m.catchChannel(tb.r, guildID, chanID, &backlog{msgs: []discord.Message{live}})
	tb.m.Wait(5 * time.Second)
	if !strings.Contains(tb.r.said(), "she picks up from the next count") || len(tb.r.afters) != catchPages {
		t.Fatalf("%q %v", tb.r.said(), len(tb.r.afters))
	}
	s, _ := tb.m.en.Channel(chanID)
	if s.N != 900 || s.Run != 2 || s.Start != 899 {
		t.Fatalf("the first live count wasn't taken as right: %+v", s)
	}
	tb.say(8, "901")
	if s, _ := tb.m.en.Channel(chanID); s.N != 901 {
		t.Fatal("counting didn't go on from the anchor")
	}
	// A read that fails can't know the number either.
	tb2 := newTable(t, count.Standard())
	tb2.say(7, "1")
	tb2.r.getErr = errors.New("discord down")
	tb2.m.catchChannel(tb2.r, guildID, chanID, &backlog{})
	tb2.m.Wait(time.Second)
	if !strings.Contains(tb2.r.said(), "picks up from the next count") {
		t.Fatal(tb2.r.said())
	}
	tb2.say(8, "hello")
	tb2.say(8, "40")
	if s, _ := tb2.m.en.Channel(chanID); s.N != 40 {
		t.Fatalf("%+v", s)
	}
	if _, lost := tb2.m.lost.Load(snowflake.ID(chanID)); lost {
		t.Fatal("still lost after a count")
	}
	// A channel with nothing counted yet has nothing to read back.
	tb3 := newTable(t, count.Standard())
	tb3.m.catchUp(tb3.r, guildID)
	tb3.m.Wait(time.Second)
	if len(tb3.r.afters) != 0 {
		t.Fatal("read back a fresh channel")
	}
}

func press(t *testing.T, m *Module, data map[string]any, admin bool) (string, error) {
	t.Helper()
	m.Admin = func(discord.Interaction) bool { return admin }
	e, sent := coretest.Button(t, "x", func(p map[string]any) { p["data"] = data })
	err := m.press(context.Background(), e)
	if len(*sent) == 0 {
		return "", err
	}
	return (*sent)[0].Content, err
}

func TestSetupFromTheHelpPage(t *testing.T) {
	m, _ := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), nil)
	sel := func(ids ...string) map[string]any {
		vals := make([]any, len(ids))
		for i, id := range ids {
			vals[i] = id
		}
		return map[string]any{"custom_id": "games:channels", "component_type": 8, "values": vals, "resolved": map[string]any{}}
	}
	if _, err := press(t, m, sel("40"), false); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member set up a game: %v", err)
	}
	said, err := press(t, m, sel("40", "41", "42", "43"), true)
	if err != nil || said != "✓ counting in <#40> <#41> <#42>" {
		t.Fatalf("%q %v", said, err)
	}
	if got := m.counting(guildID); !slices.Equal(got, []uint64{40, 41, 42}) {
		t.Fatalf("%v", got)
	}
	opts := map[string]any{"custom_id": "games:rules:41", "component_type": 3, "values": []any{"hardcore"}}
	if said, err := press(t, m, opts, true); err != nil || said != "✓ <#41> counts by hardcore rules" {
		t.Fatalf("%q %v", said, err)
	}
	sw := map[string]any{"custom_id": "games:opts:41", "component_type": 3, "values": []any{"solo", "timeout"}}
	if said, err := press(t, m, sw, true); err != nil || said != "✓ <#41> runs with solo and timeouts" {
		t.Fatalf("%q %v", said, err)
	}
	s, _ := m.en.Channel(41)
	if s.Rules.Fail != count.Hardcore || !s.Rules.Solo || s.Rules.Arcade || s.Rules.Timeout != baseTimeout || s.Rules.Name != "custom" {
		t.Fatalf("%+v", s.Rules)
	}
	// A preset keeps the switches.
	if _, err := press(t, m, map[string]any{"custom_id": "games:rules:41", "component_type": 3, "values": []any{"roman"}}, true); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.en.Channel(41); s.Rules.Timeout != baseTimeout || s.Rules.Arcade || s.Rules.Format != count.Roman {
		t.Fatalf("%+v", s.Rules)
	}
	sw["values"] = []any{}
	if said, _ := press(t, m, sw, true); said != "✓ <#41> runs with no extras" {
		t.Fatal(said)
	}
	// Another server's channel, a channel that doesn't count, a bad preset.
	m.en.Open(99, 77, count.Standard())
	for _, bad := range []map[string]any{
		{"custom_id": "games:rules:77", "component_type": 3, "values": []any{"standard"}},
		{"custom_id": "games:rules:55", "component_type": 3, "values": []any{"standard"}},
		{"custom_id": "games:rules:41", "component_type": 3, "values": []any{"nope"}},
		{"custom_id": "games:rules:x", "component_type": 3, "values": []any{"standard"}},
	} {
		if _, err := press(t, m, bad, true); !errors.Is(err, errNoGame) {
			t.Fatalf("%v: %v", bad, err)
		}
	}
	if _, err := press(t, m, map[string]any{"custom_id": "games:what", "component_type": 2}, true); !errors.Is(err, errNotAdmin) {
		t.Fatal(err)
	}
	// Report shows each channel and its controls.
	rep := m.Report(guildID)
	if len(rep.Rows) != 3 || len(rep.Controls) != 2+3*3 {
		t.Fatalf("%d rows %d controls", len(rep.Rows), len(rep.Controls))
	}
	// Dropping channels stops them counting.
	if said, _ := press(t, m, sel(), true); said != "✓ counting nowhere" {
		t.Fatal(said)
	}
	if len(m.counting(guildID)) != 0 || m.Report(guildID).Rows[0][1] != "nowhere yet" {
		t.Fatal("dropped channels still count")
	}
	if _, ok := m.en.Channel(77); !ok {
		t.Fatal("another server's channel was dropped")
	}
}

func TestPlural(t *testing.T) {
	if plural(1, "save") != "1 save" || plural(0, "save") != "0 saves" || plural(2, "life") != "2 lives" {
		t.Fatal("plural")
	}
	if got := firstWord(strings.Repeat("9", 40) + " x"); got != "**"+strings.Repeat("9", 24)+"...**" {
		t.Fatal(got)
	}
}

func TestRunWithoutADatabase(t *testing.T) {
	m, _ := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	cancel()
	<-done
	if !m.Wait(time.Second) {
		t.Fatal("wait")
	}
}

// pool is Postgres from SKUA_TEST_DATABASE_URL, migrated, as CI runs it.
func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SKUA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKUA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	p, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if err := store.Migrate(ctx, p); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"delete from count_channel where guild_id = 3",
		"delete from count_member where guild_id = 3",
		"delete from count_season where guild_id = 3",
		"delete from count_run where guild_id = 3",
		"delete from count_log where channel_id = 4",
	} {
		if _, err := p.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// Everything played survives a restart: channels, members, the log, the
// month's counts and the runs that ended.
func TestStoreRoundTrip(t *testing.T) {
	db := pool(t)
	ctx := context.Background()
	m, err := New(ctx, slog.New(slog.DiscardHandler), guard.New(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.pick(ctx, guildID, []snowflake.ID{chanID}); err != nil {
		t.Fatal(err)
	}
	tb := &table{t: t, m: m, r: &fakeRest{now: t0}, at: t0}
	tb.count(5)
	tb.say(7, "9") // breaks: run 1 is 5 long
	tb.count(2)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { m.Run(runCtx); close(done) }()
	stop()
	<-done

	again, err := New(ctx, slog.New(slog.DiscardHandler), guard.New(), db)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := again.en.Channel(chanID)
	if !ok || s.N != 2 || s.Run != 2 || s.Best != 5 {
		t.Fatalf("%+v", s)
	}
	if mem := again.en.Member(count.Who{Guild: guildID, User: 101}); mem.Counts != 4 {
		t.Fatalf("member %+v", mem)
	}
	var logs, season int64
	var head string
	var start, length int64
	if err := db.QueryRow(ctx, "select count(*) from count_log where channel_id = $1", chanID).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "select coalesce(sum(counts), 0) from count_season where guild_id = $1", guildID).Scan(&season); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "select head, start, length from count_run where channel_id = $1 and run = 1", chanID).Scan(&head, &start, &length); err != nil {
		t.Fatal(err)
	}
	if logs != 7 || season != 7 || length != 5 || start != 0 {
		t.Fatalf("logs %d season %d run %d+%d", logs, season, start, length)
	}
	// The run checks out against its log.
	rows, _ := db.Query(ctx, "select n, user_id, message_id from count_log where channel_id = $1 and run = 1 order by n", chanID)
	var entries []count.Entry
	for rows.Next() {
		var e count.Entry
		var u, msg int64
		if err := rows.Scan(&e.N, &u, &msg); err != nil {
			t.Fatal(err)
		}
		e.User, e.Msg = uint64(u), uint64(msg)
		entries = append(entries, e)
	}
	h, err := game.ParseHash(head)
	if err != nil || !count.Verify(guildID, chanID, 1, 0, entries, h) {
		t.Fatalf("run 1 doesn't verify: %v", err)
	}
	// Dropping the channel forgets it but keeps its history.
	if err := again.pick(ctx, guildID, nil); err != nil {
		t.Fatal(err)
	}
	third, _ := New(ctx, slog.New(slog.DiscardHandler), guard.New(), db)
	if _, ok := third.en.Channel(chanID); ok {
		t.Fatal("a dropped channel came back")
	}
	// Picked again, it numbers its runs after the ones it kept.
	if err := third.pick(ctx, guildID, []snowflake.ID{chanID}); err != nil {
		t.Fatal(err)
	}
	if s, _ := third.en.Channel(chanID); s.Run != 3 {
		t.Fatalf("run %d reuses a kept run's number", s.Run)
	}
}

// failDB refuses every write, for an admin's change that can't be saved.
type failDB struct{ DB }

func (failDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("db down")
}

func (failDB) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("db down") }

// A rules change that can't be saved leaves the channel exactly as it was.
func TestRulesChangeRollsBack(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.count(5)
	before, _ := tb.m.en.Channel(chanID)
	tb.m.db = failDB{}
	if _, err := tb.m.rules(context.Background(), guildID, chanID, "rules", []string{"binary"}); err == nil {
		t.Fatal("a failed save said it worked")
	}
	after, _ := tb.m.en.Channel(chanID)
	if after.N != before.N || after.Run != before.Run || after.Head != before.Head || after.Rules != before.Rules {
		t.Fatalf("%+v, want %+v", after, before)
	}
	if err := tb.m.pick(context.Background(), guildID, nil); err == nil {
		t.Fatal("a failed drop said it worked")
	}
}

// A write that fails keeps everything for the next one.
func TestFailedFlushKeepsTheJournal(t *testing.T) {
	db := pool(t)
	ctx := context.Background()
	m, _ := New(ctx, slog.New(slog.DiscardHandler), guard.New(), db)
	if err := m.pick(ctx, guildID, []snowflake.ID{chanID}); err != nil {
		t.Fatal(err)
	}
	tb := &table{t: t, m: m, r: &fakeRest{now: t0}, at: t0}
	tb.count(3)
	dead, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.flush(dead); err == nil {
		t.Fatal("a write on a dead context succeeded")
	}
	if len(m.journal.logs) != 3 {
		t.Fatalf("journal lost: %d", len(m.journal.logs))
	}
	if err := m.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.flush(ctx); err != nil {
		t.Fatal("an empty flush failed")
	}
	var logs int64
	_ = db.QueryRow(ctx, "select count(*) from count_log where channel_id = $1", chanID).Scan(&logs)
	if logs != 3 {
		t.Fatal(logs)
	}
}

// A channel that switched timeouts on at the old 10m gets the 30m floor.
func TestOldTimeoutTakesTheFloor(t *testing.T) {
	if l := ladder(10 * time.Minute); l.Base != 30*time.Minute {
		t.Fatalf("%v", l.Base)
	}
	if l := ladder(time.Hour); l.Base != time.Hour {
		t.Fatalf("%v", l.Base)
	}
}
