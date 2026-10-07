package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/store"
)

// fake is a platform whose accounts show whatever the test sets.
type fake struct {
	mu    sync.Mutex
	shows map[string][]item
	err   error
	drop  string // an ID keep refuses
	known map[string]string
}

func (f *fake) every() time.Duration { return time.Hour }

func (f *fake) resolve(_ context.Context, account string) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	name, ok := f.known[clean(account)]
	if !ok {
		return "", "", errUnknown
	}
	return clean(account), name, nil
}

func (f *fake) check(_ context.Context, accounts []string) (map[string][]item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	got := map[string][]item{}
	for _, a := range accounts {
		if items, ok := f.shows[a]; ok {
			got[a] = items
		}
	}
	return got, f.err
}

func (f *fake) keep(_ context.Context, it item) bool { return it.ID != f.drop }

func (f *fake) show(account string, items ...item) {
	f.mu.Lock()
	f.shows[account] = items
	f.mu.Unlock()
}

// poster records each card and fails a channel the test names.
type poster struct {
	sent    []sent
	edits   []edit
	fail    map[snowflake.ID]error
	editErr error
}

type edit struct {
	channel, message snowflake.ID
	msg              discord.MessageUpdate
}

type sent struct {
	channel snowflake.ID
	msg     discord.MessageCreate
}

func (p *poster) CreateMessage(channel snowflake.ID, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	if err := p.fail[channel]; err != nil {
		return nil, err
	}
	p.sent = append(p.sent, sent{channel, m})
	// Each card its own message ID, from 1.
	return &discord.Message{ID: snowflake.ID(len(p.sent))}, nil
}

func (p *poster) UpdateMessage(channel, message snowflake.ID, m discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	if p.editErr != nil {
		return nil, p.editErr
	}
	p.edits = append(p.edits, edit{channel, message, m})
	return &discord.Message{}, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func module(t *testing.T, src *fake) *Module {
	t.Helper()
	m, err := New(context.Background(), quiet(), guard.New(), nil, Config{}, func(discord.Interaction) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	m.sources = map[string]source{"fake": src}
	return m
}

func newFake() *fake {
	return &fake{shows: map[string][]item{}, known: map[string]string{"bird": "Bird"}}
}

func post(id string) item {
	return item{ID: id, Title: "t " + id, URL: "https://p/" + id, Author: "Bird"}
}

func ids(p *poster) []string {
	var out []string
	for _, s := range p.sent {
		b, _ := json.Marshal(s.msg.Components)
		for _, part := range strings.Split(string(b), "https://p/")[1:] {
			id := part[:strings.IndexAny(part, ")\"")]
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

func TestPollBaselinesThenAnnouncesWhatIsNew(t *testing.T) {
	src := newFake()
	m := module(t, src)
	m.follows = []follow{
		{guild: 1, channel: 10, platform: "fake", account: "bird", name: "Bird"},
		{guild: 2, channel: 20, platform: "fake", account: "bird", name: "Bird", role: 7},
		{guild: 3, channel: 30, platform: "fake", account: "bird", name: "Bird"},
	}
	m.Gate(func(g snowflake.ID) bool { return g != 3 })
	p := &poster{}
	ctx := context.Background()

	src.show("bird", post("b"), post("a"))
	m.poll(ctx, p, "fake", src)
	if len(p.sent) != 0 {
		t.Fatalf("the first look is a baseline: %v", ids(p))
	}

	src.show("bird", post("c"), post("b"), post("a"))
	m.poll(ctx, p, "fake", src)
	if len(p.sent) != 2 || !slices.Equal(ids(p), []string{"c"}) {
		t.Fatalf("c, to guilds 1 and 2 but not the one with notify off: %v", ids(p))
	}
	if p.sent[0].msg.AllowedMentions.Roles != nil || p.sent[1].msg.AllowedMentions.Roles[0] != 7 {
		t.Fatal("only the follow with a role pings it")
	}

	// Nothing new, then a deleted post doesn't bring an older one back.
	p.sent = nil
	m.poll(ctx, p, "fake", src)
	src.show("bird", post("b"), post("a"))
	m.poll(ctx, p, "fake", src)
	if len(p.sent) != 0 {
		t.Fatalf("nothing is new: %v", ids(p))
	}

	// A burst is capped and comes oldest first; keep can veto one.
	src.drop = "g"
	src.show("bird", post("h"), post("g"), post("f"), post("e"), post("d"), post("b"))
	m.poll(ctx, p, "fake", src)
	if got := ids(p); !slices.Equal(got, []string{"f", "h"}) {
		t.Fatalf("burst: %v", got)
	}
}

func TestPollAnnouncesEachStreamOnce(t *testing.T) {
	src := newFake()
	m := module(t, src)
	m.follows = []follow{{guild: 1, channel: 10, platform: "fake", account: "bird", name: "Bird"}}
	p := &poster{}
	ctx := context.Background()
	stream := item{ID: "live:1", Title: "on air", URL: "https://p/live1", Author: "Bird", Detail: "IRL", Image: "https://img"}

	src.show("bird")
	m.poll(ctx, p, "fake", src)
	src.drop = "live:1" // keep is for posts; a stream is never vetoed
	src.show("bird", stream)
	m.poll(ctx, p, "fake", src)
	m.poll(ctx, p, "fake", src)
	if len(p.sent) != 1 {
		t.Fatalf("one card a stream: %d", len(p.sent))
	}
	src.show("bird")
	m.poll(ctx, p, "fake", src)
	src.show("bird", stream)
	m.poll(ctx, p, "fake", src)
	if len(p.sent) != 2 {
		t.Fatalf("a stream that ended and started again is news: %d", len(p.sent))
	}
	b, _ := json.Marshal(p.sent[0].msg)
	for _, want := range []string{"live on fake", "watch", "https://img", "IRL", `"parse":[]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the live card has no %q: %s", want, b)
		}
	}
}

func TestPollRecordsHealthAndFailures(t *testing.T) {
	src := newFake()
	m := module(t, src)
	m.follows = []follow{
		{guild: 1, channel: 10, platform: "fake", account: "bird", name: "Bird"},
		{guild: 1, channel: 11, platform: "fake", account: "bird", name: "Bird"},
		{guild: 1, channel: 12, platform: "fake", account: "bird", name: "Bird"},
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	ctx := context.Background()
	p := &poster{fail: map[snowflake.ID]error{
		10: &rest.Error{Response: &http.Response{StatusCode: http.StatusForbidden}},
		11: &rest.Error{Response: &http.Response{StatusCode: http.StatusNotFound}},
		12: errors.New("boom"),
	}}

	if r := m.Report(1); len(r.Rows) != 2 || r.Rows[1][1] != "not checked yet" {
		t.Fatalf("before a round: %v", r.Rows)
	}
	src.show("bird")
	m.poll(ctx, p, "fake", src)
	src.show("bird", post("a"))
	m.poll(ctx, p, "fake", src)
	now = now.Add(2 * time.Minute)
	r := m.Report(1)
	if r.Rows[0] != [2]string{"following", "3"} || r.Rows[1] != [2]string{"fake", "ok 2m ago"} {
		t.Fatalf("rows: %v", r.Rows)
	}
	notes := strings.Join(r.Notes, "\n")
	for _, want := range []string{"<#10>: skua can't post there", "<#11>: that channel is gone", "<#12>: discord refused"} {
		if !strings.Contains(notes, want) {
			t.Errorf("no %q in %q", want, notes)
		}
	}

	src.err = errors.New("down")
	src.show("bird")
	src.mu.Lock()
	delete(src.shows, "bird")
	src.mu.Unlock()
	m.poll(ctx, p, "fake", src)
	if got := m.Report(1).Rows[1][1]; got != "failing, last ok 2m ago" {
		t.Fatal(got)
	}
	m.health["fake"] = health{err: errors.New("down")}
	if got := m.Report(1).Rows[1][1]; got != "failing" {
		t.Fatal(got)
	}
	if r := m.Report(2); len(r.Rows) != 0 {
		t.Fatalf("another server sees nothing: %v", r)
	}

	// A channel that works again loses its note.
	delete(p.fail, 10)
	src.err = nil
	src.show("bird", post("b"), post("a"))
	m.poll(ctx, p, "fake", src)
	if strings.Contains(strings.Join(m.Report(1).Notes, ""), "<#10>") {
		t.Fatal("a fixed channel still has its note")
	}
}

func TestGuardHoldsBackARunaway(t *testing.T) {
	src := newFake()
	m := module(t, src)
	m.follows = []follow{{guild: 1, channel: 10, platform: "fake", account: "bird", name: "Bird"}}
	g := guard.New()
	for g.Allow(1, guard.MessageSend) == nil {
	}
	m.guard = g
	m.announce(context.Background(), &poster{}, key{"fake", "bird"}, post("a"))
	if !strings.Contains(m.failed[10], "held back") {
		t.Fatal(m.failed[10])
	}
	if struggling(errors.New("x")) || !struggling(&rest.Error{Response: &http.Response{StatusCode: 503}}) {
		t.Fatal("struggling")
	}
}

func TestAlert(t *testing.T) {
	s := js(alert("youtube", item{ID: "v", Title: "a [weird]\ntitle", URL: "https://y/v", Author: "Bird", Image: "https://i"}, 0))
	for _, want := range []string{"new on youtube", `**[a (weird) title](https://y/v)**`, "-# Bird", `"label":"open"`, `"parse":[]`} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in %s", want, s)
		}
	}
	s = js(alert("x", item{ID: "v", URL: "javascript:x", Image: "http://i"}, 7))
	if !strings.Contains(s, "**something new**") || strings.Contains(s, "javascript") || strings.Contains(s, "http://i") {
		t.Fatalf("only https links and images: %s", s)
	}
	if !strings.Contains(s, `"content":"<@&7>"`) || !strings.Contains(s, `"roles":["7"]`) {
		t.Fatalf("the role is mentioned and allowed: %s", s)
	}
	// A title with an emoji isn't a masked link: Discord would show the
	// markdown. The button still links it.
	s = js(alert("youtube", item{ID: "live:v", Title: "24/7 ambience \U0001F383 lofi", URL: "https://y/v"}, 0))
	if !strings.Contains(s, "**24/7 ambience \U0001F383 lofi**") || strings.Contains(s, "](https://y/v)") || !strings.Contains(s, `"url":"https://y/v"`) {
		t.Fatalf("an emoji title: %s", s)
	}
	// A card wears its platform's muted tint; one without a tint, skua's notice.
	if a := alert("youtube", item{ID: "v"}, 0).Components[0].(discord.ContainerComponent).AccentColor; a != brand.PlatformColor("youtube") {
		t.Fatalf("youtube accent %v", a)
	}
	if a := alert("fake", item{ID: "v"}, 0).Components[0].(discord.ContainerComponent).AccentColor; a != brand.ColorNotice {
		t.Fatalf("fallback accent %v", a)
	}
	if emoji("24/7 plain words") || !emoji("done \u2713") {
		t.Fatal("emoji")
	}
	if got := line(strings.Repeat("é", 300), 10); got != "ééééééé..." {
		t.Fatal(got)
	}
}

func TestRemember(t *testing.T) {
	got := remember([]item{{ID: "b"}, {ID: "live:2"}}, []string{"a", "live:1", "b"})
	if !slices.Equal(got, []string{"b", "live:2", "a"}) {
		t.Fatal(got)
	}
	long := make([]string, keepIDs+5)
	for i := range long {
		long[i] = string(rune('a' + i%26))
	}
	if len(remember(nil, long)) > keepIDs {
		t.Fatal("remember is capped")
	}
}

// restRec is the REST the panel makes after it defers: the edit of the
// panel, and a followup for an error.
type restRec struct {
	rest.Rest
	updates   []discord.MessageUpdate
	followups []discord.MessageCreate
}

func (r *restRec) UpdateInteractionResponse(_ snowflake.ID, _ string, u discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	r.updates = append(r.updates, u)
	return &discord.Message{}, nil
}

func (r *restRec) CreateFollowupMessage(_ snowflake.ID, _ string, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	r.followups = append(r.followups, m)
	return &discord.Message{}, nil
}

// panelTest drives the panel through the router as the bootstrap admin
// (member 5 in guild 3), and reads back whatever skua answered: a reply,
// a panel redrawn in place, a form, an edit after a deferral or a followup.
type panelTest struct {
	t     *testing.T
	m     *Module
	r     *core.Router
	admin bool
}

func newPanel(t *testing.T, src *fake) *panelTest {
	t.Helper()
	m := module(t, src)
	pt := &panelTest{t: t, m: m, admin: true}
	m.admin = func(discord.Interaction) bool { return pt.admin }
	pt.r = core.NewRouter(5, func(snowflake.ID) (snowflake.ID, bool) { return 0, false }, quiet())
	if err := pt.r.Add(m); err != nil {
		t.Fatal(err)
	}
	return pt
}

func record(got *[]string) func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
	return func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		*got = append(*got, js(d))
		return nil
	}
}

func read(got []string, rr *restRec) string {
	for _, u := range rr.updates {
		got = append(got, js(u))
	}
	for _, f := range rr.followups {
		got = append(got, js(f))
	}
	return strings.Join(got, "\n")
}

func (pt *panelTest) open() string {
	e, _ := coretest.Event(pt.t, "notify", nil)
	var got []string
	e.Respond = record(&got)
	rr := &restRec{}
	e.Client().Rest = rr
	pt.r.OnCommand(e)
	return read(got, rr)
}

// click is a select on the panel: data is Discord's component data.
func (pt *panelTest) click(data map[string]any) string {
	e, _ := coretest.Button(pt.t, data["custom_id"].(string), func(p map[string]any) { p["data"] = data })
	var got []string
	e.Respond = record(&got)
	rr := &restRec{}
	e.Client().Rest = rr
	pt.r.OnComponent(e)
	return read(got, rr)
}

// submit is the follow form sent back with the given fields.
func (pt *panelTest) submit(platform, account, channel, role string, resolved map[string]any) string {
	ch, ro := []any{}, []any{}
	if channel != "" {
		ch = append(ch, channel)
	}
	if role != "" {
		ro = append(ro, role)
	}
	comps := []any{
		map[string]any{"type": 18, "component": map[string]any{"type": 4, "custom_id": "account", "value": account}},
		map[string]any{"type": 18, "component": map[string]any{"type": 8, "custom_id": "channel", "values": ch}},
		map[string]any{"type": 18, "component": map[string]any{"type": 6, "custom_id": "role", "values": ro}},
	}
	e, _ := coretest.Modal(pt.t, followID+":"+platform, nil, func(p map[string]any) {
		d := p["data"].(map[string]any)
		d["components"] = comps
		if resolved != nil {
			d["resolved"] = resolved
		}
	})
	var got []string
	e.Respond = record(&got)
	rr := &restRec{}
	e.Client().Rest = rr
	pt.r.OnModal(e)
	return read(got, rr)
}

func pick(customID string, values ...string) map[string]any {
	return map[string]any{"custom_id": customID, "component_type": 3, "values": values}
}

func roles(id string, mentionable bool) map[string]any {
	return map[string]any{"roles": map[string]any{id: map[string]any{"id": id, "name": "fans", "mentionable": mentionable, "permissions": "0", "color": 0, "position": 1}}}
}

func channels(id string) map[string]any {
	return map[string]any{"channels": map[string]any{id: map[string]any{"id": id, "type": 0, "name": "news", "permissions": "0"}}}
}

func TestPanel(t *testing.T) {
	src := newFake()
	pt := newPanel(t, src)
	m := pt.m

	got := pt.open()
	for _, want := range []string{"## notify", "no channel yet", "nobody followed yet", "follow someone on", `"value":"fake"`, "0 of 25"} {
		if !strings.Contains(got, want) {
			t.Errorf("the first panel has no %q: %s", want, got)
		}
	}
	if strings.Contains(got, "notify:drop") {
		t.Error("nothing to drop yet")
	}

	// Following needs somewhere for the cards to go.
	if got := pt.submit("fake", "bird", "", "", nil); !strings.Contains(got, "pick where cards go first") {
		t.Fatal(got)
	}
	got = pt.click(map[string]any{"custom_id": "notify:bind", "component_type": 8, "values": []string{"9"}, "resolved": channels("9")})
	if !strings.Contains(got, "✓ cards go to <#9>") || !strings.Contains(got, "**cards go to** <#9>") || m.bound[3] != 9 {
		t.Fatal(got)
	}
	if got := pt.click(map[string]any{"custom_id": "notify:bind", "component_type": 8, "values": []string{}}); !strings.Contains(got, "pick one channel") {
		t.Fatal(got)
	}

	if got := pt.click(pick("notify:add", "fake")); !strings.Contains(got, "follow someone on fake") || !strings.Contains(got, `"custom_id":"notify-follow:fake"`) {
		t.Fatalf("the form: %s", got)
	}
	got = pt.submit("fake", "@Bird", "", "7", roles("7", true))
	if !strings.Contains(got, "✓ following Bird on fake") || !strings.Contains(got, "**Bird** · fake") || !strings.Contains(got, "-# → <@&7>") {
		t.Fatal(got)
	}
	if len(m.follows) != 1 || m.follows[0] != (follow{guild: 3, platform: "fake", account: "bird", name: "Bird", role: 7}) {
		t.Fatalf("%+v", m.follows)
	}
	// Into a channel of its own as well, and again into the server's,
	// which replaces the first.
	pt.submit("fake", "bird", "12", "", channels("12"))
	pt.submit("fake", "bird", "", "", nil)
	if len(m.follows) != 2 || m.follows[1].role != 0 {
		t.Fatalf("%+v", m.follows)
	}
	got = pt.open()
	if !strings.Contains(got, "-# → <#12>") || !strings.Contains(got, "notify:drop") || !strings.Contains(got, "2 of 25") {
		t.Fatal(got)
	}

	for _, c := range []struct {
		platform, account, role string
		resolved                map[string]any
		want                    string
	}{
		{"mastodon", "a", "", nil, "isn't set up for mastodon"},
		{"fake", "a", "3", roles("3", true), "never pings everyone"},
		{"fake", "a", "8", roles("8", false), "@fans can't be mentioned"},
		{"fake", "nobody", "", nil, "can't find nobody on fake"},
	} {
		if got := pt.submit(c.platform, c.account, "", c.role, c.resolved); !strings.Contains(got, c.want) {
			t.Errorf("want %q, got %s", c.want, got)
		}
	}
	src.err = errors.New("down")
	if got := pt.submit("fake", "bird", "", "", nil); !strings.Contains(got, "fake isn't answering") {
		t.Error(got)
	}
	src.err = nil
	for in, want := range map[string]string{"notify:add": "isn't set up for mastodon", "notify:stale": "out of date"} {
		if got := pt.click(pick(in, "mastodon")); !strings.Contains(got, want) {
			t.Errorf("%s: %s", in, got)
		}
	}
	if got := pt.click(pick("notify:add")); !strings.Contains(got, "pick a platform") {
		t.Error(got)
	}

	m.seen[key{"fake", "bird"}] = []string{"a"}
	got = pt.click(pick("notify:drop", "fake:bird:0", "fake:bird:12", "fake:nobody:0"))
	if !strings.Contains(got, "✓ dropped Bird, Bird") || len(m.follows) != 0 || m.seen[key{"fake", "bird"}] != nil {
		t.Fatalf("dropped and forgotten: %s %+v %v", got, m.follows, m.seen)
	}
	if got := pt.click(pick("notify:drop", "fake:nobody:0")); !strings.Contains(got, "nobody followed yet") {
		t.Error(got)
	}

	pt.admin = false
	for _, got := range []string{pt.click(pick("notify:add", "fake")), pt.submit("fake", "bird", "", "", nil)} {
		if !strings.Contains(got, "only this server's admins") {
			t.Errorf("a member who isn't an admin: %s", got)
		}
	}
	pt.admin = true

	m.follows = make([]follow, perGuild)
	for i := range m.follows {
		m.follows[i] = follow{guild: 3, platform: "fake", account: string(rune('a' + i))}
	}
	if got := pt.submit("fake", "bird", "", "", nil); !strings.Contains(got, "the most it can") {
		t.Fatal(got)
	}
	if got := pt.open(); strings.Contains(got, "notify:add") {
		t.Fatal("a full server isn't offered another follow")
	}
}

func TestCardsGoToTheServersChannel(t *testing.T) {
	src := newFake()
	m := module(t, src)
	m.follows = []follow{{guild: 1, platform: "fake", account: "bird", name: "Bird"}}
	p := &poster{}
	m.announce(context.Background(), p, key{"fake", "bird"}, post("a"))
	if len(p.sent) != 0 {
		t.Fatal("a card with nowhere to go")
	}
	if r := m.Report(1); !strings.Contains(strings.Join(r.Notes, ""), "no channel for cards yet") {
		t.Fatal(r.Notes)
	}
	m.bound[1] = 9
	m.announce(context.Background(), p, key{"fake", "bird"}, post("a"))
	if len(p.sent) != 1 || p.sent[0].channel != 9 {
		t.Fatalf("%+v", p.sent)
	}
}

func TestLeaveForgetsAServer(t *testing.T) {
	m := module(t, newFake())
	m.follows = []follow{
		{guild: 1, platform: "fake", account: "a"},
		{guild: 1, platform: "fake", account: "b"},
		{guild: 2, platform: "fake", account: "b"},
	}
	m.seen[key{"fake", "a"}] = []string{"x"}
	m.seen[key{"fake", "b"}] = []string{"y"}
	m.leave(1)
	if len(m.follows) != 1 || m.seen[key{"fake", "a"}] != nil || m.seen[key{"fake", "b"}] == nil {
		t.Fatalf("%+v %v", m.follows, m.seen)
	}
}

func TestModule(t *testing.T) {
	m, err := New(context.Background(), quiet(), guard.New(), nil, Config{RSSHub: "https://hub"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name() != "notify" || m.Perms() == 0 || m.Want().Required == 0 {
		t.Fatal("contract")
	}
	if h := m.Help(); !strings.Contains(h.About, "youtube, x, tiktok") {
		t.Fatal(h.About)
	}
	if got := js(m.panel(1, "")); strings.Count(got, `"description":"new posts"`) != 2 || !strings.Contains(got, "youtube, x, tiktok") {
		t.Fatalf("a choice per platform set up: %s", got)
	}

	// start runs a loop per platform until its context ends.
	src := newFake()
	m.sources = map[string]source{"fake": src}
	m.follows = []follow{{guild: 1, channel: 10, platform: "fake", account: "bird"}}
	src.show("bird", post("a"))
	ctx, cancel := context.WithCancel(context.Background())
	m.start(ctx, &poster{})
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		_, done := m.seen[key{"fake", "bird"}]
		m.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if m.seen[key{"fake", "bird"}] == nil {
		t.Fatal("the loop never polled")
	}
}

// TestStore runs against Postgres from SKUA_TEST_DATABASE_URL, as CI does.
func TestStore(t *testing.T) {
	dsn := os.Getenv("SKUA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKUA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"delete from notify_follow", "delete from notify_seen", "delete from notify_guild", "delete from notify_live"} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	f := follow{guild: 1, channel: 2, platform: "x", account: "bird", name: "@bird", role: 3}
	if err := saveFollow(ctx, pool, f); err != nil {
		t.Fatal(err)
	}
	f.name = "@bird2"
	if err := saveFollow(ctx, pool, f); err != nil {
		t.Fatal(err)
	}
	if err := saveFollow(ctx, pool, follow{guild: 9, channel: 2, platform: "x", account: "other", name: "@o"}); err != nil {
		t.Fatal(err)
	}
	if err := saveSeen(ctx, pool, key{"x", "bird"}, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []snowflake.ID{8, 9} {
		if err := saveBound(ctx, pool, 9, ch); err != nil {
			t.Fatal(err)
		}
	}
	m, err := New(ctx, quiet(), guard.New(), pool, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.follows) != 2 || !slices.Contains(m.follows, f) || !slices.Equal(m.seen[key{"x", "bird"}], []string{"a", "b"}) || m.bound[9] != 9 {
		t.Fatalf("%+v %v %v", m.follows, m.seen, m.bound)
	}

	// A live card survives a restart, item and all, until its stream ends.
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	card := posted{k: key{"twitch", "bird"}, guild: 9, channel: 2, message: 77, role: 3, at: at,
		it: item{ID: "live:5", Title: "t", URL: "https://t/bird", Started: at.Add(-time.Hour)}}
	for range 2 {
		if err := saveLive(ctx, pool, card); err != nil {
			t.Fatal(err)
		}
	}
	if err := saveLive(ctx, pool, posted{k: key{"twitch", "bird"}, guild: 1, message: 78, it: item{ID: "live:6"}, at: at}); err != nil {
		t.Fatal(err)
	}
	st, err := load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	got := st.live[stream{key{"twitch", "bird"}, "live:5"}]
	if len(got) != 1 || got[0].message != 77 || got[0].role != 3 || !got[0].it.Started.Equal(card.it.Started) || !got[0].at.Equal(at) || got[0].it.Title != "t" {
		t.Fatalf("%+v", got)
	}
	if err := dropLive(ctx, pool, key{"twitch", "bird"}, "live:5"); err != nil {
		t.Fatal(err)
	}
	if st, _ := load(ctx, pool); len(st.live) != 1 {
		t.Fatalf("one stream's cards go, the other's stay: %+v", st.live)
	}
	if err := dropLive(ctx, pool, key{"twitch", "bird"}, ""); err != nil {
		t.Fatal(err)
	}

	if err := dropFollow(ctx, pool, f); err != nil {
		t.Fatal(err)
	}
	if err := saveSeen(ctx, pool, key{"x", "bird"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := dropGuild(ctx, pool, 9); err != nil {
		t.Fatal(err)
	}
	st, err = load(ctx, pool)
	if err != nil || len(st.follows) != 0 || len(st.seen) != 0 || len(st.bound) != 0 || len(st.live) != 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

// js is v as JSON with disgo's HTML escaping undone, so <#4> reads as it is:
// decoded and encoded again without it.
func js(v any) string {
	b, _ := json.Marshal(v)
	var raw any
	_ = json.Unmarshal(b, &raw)
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(raw)
	return out.String()
}
