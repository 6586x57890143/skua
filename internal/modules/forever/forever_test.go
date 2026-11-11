package forever

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
)

// armory is a fake Blizzard: characters by slug, each a profile and its
// Last-Modified.
type armory struct {
	mu      sync.Mutex
	chars   map[string]map[string]any
	stamp   map[string]string
	skill   map[string]int // each character's enchanting
	tokens  int
	denied  bool // every call 401s
	failing bool // every call 503s
}

func (a *armory) set(slug string, p map[string]any, stamp string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.chars[slug], a.stamp[slug] = p, stamp
}

func (a *armory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.URL.Path == "/token" {
		a.tokens++
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":86400}`)
		return
	}
	switch {
	case a.denied:
		w.WriteHeader(http.StatusUnauthorized)
		return
	case a.failing:
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	case r.Header.Get("Authorization") != "Bearer tok", !strings.HasPrefix(r.URL.Query().Get("namespace"), "profile-forever-"):
		w.WriteHeader(http.StatusForbidden)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/profile/wow/character/")
	s, sub, _ := strings.Cut(path, "/")
	p, ok := a.chars[s]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if sub == "professions" {
		_ = json.NewEncoder(w).Encode(map[string]any{"primaries": []any{
			map[string]any{"profession": map[string]any{"name": "Enchanting"}, "tiers": []any{map[string]any{"skill_points": a.skill[s]}}},
			map[string]any{"profession": map[string]any{"name": "Herbalism"}, "tiers": []any{map[string]any{"skill_points": 300}}},
		}})
		return
	}
	if sub == "character-media" {
		_, _ = io.WriteString(w, `{"assets":[{"key":"avatar","value":"https://render.example/`+s+`-avatar.jpg"},{"key":"main-raw","value":"https://render.example/`+s+`.png"},{"key":"inset","value":"http://insecure"}]}`)
		return
	}
	if r.Header.Get("If-Modified-Since") == a.stamp[s] {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Last-Modified", a.stamp[s])
	_ = json.NewEncoder(w).Encode(p)
}

func prof(id int64, name, surname, class string, level int) map[string]any {
	return map[string]any{
		"id": id, "name": name, "surname": surname, "level": level, "achievement_points": level * 10, "equipped_item_level": level / 2,
		"character_class": map[string]any{"id": 1, "name": class}, "race": map[string]any{"id": 2, "name": "Orc"},
		"ruleset": map[string]any{"id": 1, "name": "Normal"}, "faction": map[string]any{"type": "HORDE"},
	}
}

// fake is the REST forever calls, recorded.
type fake struct {
	rest.Rest
	mu       sync.Mutex
	posts    []discord.MessageCreate
	updates  []discord.MessageUpdate
	added    []snowflake.ID
	removed  []snowflake.ID
	created  []discord.RoleCreate
	features []discord.GuildFeature
	perms    discord.Permissions
}

func (f *fake) CreateMessage(_ snowflake.ID, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, m)
	return &discord.Message{}, nil
}

func (f *fake) UpdateInteractionResponse(_ snowflake.ID, _ string, u discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, u)
	return &discord.Message{}, nil
}

func (f *fake) GetGuild(guild snowflake.ID, _ bool, _ ...rest.RequestOpt) (*discord.RestGuild, error) {
	return &discord.RestGuild{Guild: discord.Guild{ID: guild, Features: f.features}}, nil
}

func (f *fake) CreateRole(_ snowflake.ID, c discord.RoleCreate, _ ...rest.RequestOpt) (*discord.Role, error) {
	f.created = append(f.created, c)
	return &discord.Role{ID: 70}, nil
}

func (f *fake) GetRoles(guild snowflake.ID, _ ...rest.RequestOpt) ([]discord.Role, error) {
	return []discord.Role{{ID: guild, Permissions: f.perms}, {ID: 70, Position: 1}, {ID: 80, Position: 5}}, nil
}

func (f *fake) GetMember(_, user snowflake.ID, _ ...rest.RequestOpt) (*discord.Member, error) {
	return &discord.Member{User: discord.User{ID: user}, RoleIDs: []snowflake.ID{80}}, nil
}

func (f *fake) GetGuildChannels(snowflake.ID, ...rest.RequestOpt) ([]discord.GuildChannel, error) {
	return nil, nil
}

func (f *fake) AddMemberRole(_, _, role snowflake.ID, _ ...rest.RequestOpt) error {
	f.added = append(f.added, role)
	return nil
}

func (f *fake) RemoveMemberRole(_, _, role snowflake.ID, _ ...rest.RequestOpt) error {
	f.removed = append(f.removed, role)
	return nil
}

func newTest(t *testing.T) (*Module, *armory, *fake) {
	t.Helper()
	a := &armory{chars: map[string]map[string]any{}, stamp: map[string]string{}, skill: map[string]int{}}
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	m, err := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), nil, Config{ID: "id", Secret: "s"}, func(i discord.Interaction) bool {
		return i.Member() != nil && i.Member().Permissions.Has(discord.PermissionAdministrator)
	})
	if err != nil {
		t.Fatal(err)
	}
	m.api.oauth = srv.URL + "/token"
	m.api.api = func(string) string { return srv.URL }
	f := &fake{perms: discord.PermissionManageRoles}
	m.rest, m.self = f, 1
	return m, a, f
}

func TestParseName(t *testing.T) {
	for in, want := range map[string]bool{
		"thrall earthcaller": true, "Thrall-Earthcaller": true, "  jaina   proud ": true,
		"thrall": false, "a b c": false, "x/y z": false, strings.Repeat("a", 25) + " b": false,
	} {
		if _, _, ok := parseName(in); ok != want {
			t.Errorf("parseName(%q) = %v, want %v", in, ok, want)
		}
	}
	if slug("Thrall", "Earthcaller") != "thrall-earthcaller" {
		t.Fatal("slug")
	}
}

func TestBarMovesNumbers(t *testing.T) {
	if bar(0) != "▱▱▱▱▱▱▱▱▱▱ 0%" || bar(60) != "▰▰▰▰▰▰▰▰▰▰ 100%" || bar(33) != "▰▰▰▰▰▱▱▱▱▱ 55%" || bar(99) != bar(60) {
		t.Fatal(bar(33))
	}
	for _, c := range []struct {
		was, now int
		want     string
	}{{0, 3, "new"}, {5, 2, "▲3"}, {2, 4, "▼2"}, {3, 3, "="}} {
		if got := move(c.was, c.now); got != c.want {
			t.Errorf("move(%d, %d) = %q, want %q", c.was, c.now, got, c.want)
		}
	}
	for n, want := range map[int]string{0: "0", 999: "999", 20590: "20,590", 1234567: "1,234,567", -4200: "-4,200"} {
		if got := num(n); got != want {
			t.Errorf("num(%d) = %q", n, got)
		}
	}
	if where(char{key: key{region: "eu"}, ruleset: "hardcore"}) != "🇪🇺 hardcore" || accent("nope") != accent("") || accent("mage") != 0x3FC7EB {
		t.Fatal("where or accent")
	}
}

func TestListLadderAndDrop(t *testing.T) {
	m, a, _ := newTest(t)
	ctx := context.Background()
	a.set("thrall-earthcaller", prof(1, "Thrall", "Earthcaller", "Shaman", 30), "a")
	a.set("jaina-proud", prof(2, "Jaina", "Proud", "Mage", 44), "a")
	if _, err := m.list(ctx, 3, 5, "eu", "thrall", "earthcaller"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.list(ctx, 3, 6, "us", "jaina", "proud"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.list(ctx, 3, 5, "eu", "thrall", "earthcaller"); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("listed twice: %v", err)
	}
	if _, err := m.list(ctx, 3, 6, "eu", "thrall", "earthcaller"); err == nil || !strings.Contains(err.Error(), "someone else") {
		t.Fatalf("taken: %v", err)
	}
	if _, err := m.list(ctx, 3, 5, "eu", "nobody", "here"); err == nil || !strings.Contains(err.Error(), "level 10") {
		t.Fatalf("unknown: %v", err)
	}
	board := text(m.ladder(3, 0))
	if !strings.Contains(board, "**1** `new` **Jaina Proud** · lv 44 · 🇺🇸 normal") || !strings.Contains(board, "**2** `new` **Thrall Earthcaller** · lv 30 · 🇪🇺 normal") {
		t.Fatalf("ladder:\n%s", board)
	}
	m.turnDay(time.Date(2026, 11, 12, 0, 1, 0, 0, time.UTC))
	m.chars[key{"eu", 1}].level = 50
	if moved := text(m.ladder(3, 0)); !strings.Contains(moved, "**1** `▲1` **Thrall") || !strings.Contains(moved, "**2** `▼1` **Jaina") {
		t.Fatalf("moves:\n%s", moved)
	}
	if paged := text(m.ladder(3, 9)); !strings.Contains(paged, "page 1 of 1") {
		t.Fatalf("a page past the end:\n%s", paged)
	}
	if empty := text(m.ladder(9, 0)); !strings.Contains(empty, "nobody here yet") {
		t.Fatal(empty)
	}
	who := coretest.Text("", []discord.LayoutComponent{m.who(3)})
	if !strings.Contains(who, "**🇪🇺 normal** · 1") || !strings.Contains(who, "**🇺🇸 normal** · 1") {
		t.Fatalf("who:\n%s", who)
	}
	if !strings.Contains(coretest.Text("", []discord.LayoutComponent{m.who(9)}), "nobody here yet") {
		t.Fatal("empty who")
	}
	hub := text(m.hub(3, 5, "note"))
	if !strings.Contains(hub, "note") || !strings.Contains(hub, "#1 here") || !strings.Contains(hub, "-# legacy · 25 ✦ · 45 ✦ · 60") {
		t.Fatalf("hub:\n%s", hub)
	}
	if !strings.Contains(text(m.hub(3, 99, "")), "haven't added") {
		t.Fatal("empty hub")
	}
	if r := m.Report(3); len(r.Rows) == 0 || len(r.Notes) == 0 || len(r.Controls) != 2 {
		t.Fatalf("report: %+v", r)
	}
	if r := m.Report(9); len(r.Rows) != 0 {
		t.Fatalf("report of an empty server: %+v", r)
	}
}

func TestPollDingsLegacyAndFeed(t *testing.T) {
	m, a, f := newTest(t)
	ctx := context.Background()
	m.guilds[3] = setup{feed: 40}
	a.set("thrall-earthcaller", prof(1, "Thrall", "Earthcaller", "Shaman", 24), "a")
	a.set("garrosh-hellscream", prof(2, "Garrosh", "Hellscream", "Warrior", 27), "a")
	a.set("rexxar-beast", prof(3, "Rexxar", "Beast", "Shaman", 58), "a")
	for _, c := range []struct {
		user snowflake.ID
		s    [2]string
	}{{5, [2]string{"thrall", "earthcaller"}}, {6, [2]string{"garrosh", "hellscream"}}, {5, [2]string{"rexxar", "beast"}}} {
		if _, err := m.list(ctx, 3, c.user, "eu", c.s[0], c.s[1]); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing changed: every profile is a 304 and nothing is posted.
	m.poll(ctx)
	if len(f.posts) != 0 {
		t.Fatalf("posted on a quiet round: %d", len(f.posts))
	}
	a.set("thrall-earthcaller", prof(1, "Thrall", "Earthcaller", "Shaman", 25), "b")
	a.set("rexxar-beast", prof(3, "Rexxar", "Beast", "Shaman", 60), "b")
	m.poll(ctx)
	if len(f.posts) != 2 {
		t.Fatalf("want rexxar's 60 alone and a card for the rest, got %d", len(f.posts))
	}
	rexCard, rest := text(f.posts[0].Components[0].(discord.ContainerComponent)), text(f.posts[1].Components[0].(discord.ContainerComponent))
	all := rest + "\n" + rexCard
	// Thrall's 25 isn't his player's first shaman at 25 (rexxar was past
	// it), and garrosh got there first; rexxar's 60 is a first on both.
	if !strings.Contains(all, "**Thrall Earthcaller** reached 25") || strings.Contains(strings.Split(all, "\n")[1], "✦") {
		t.Fatalf("thrall's line:\n%s", all)
	}
	rex := all[strings.Index(all, "**Rexxar"):]
	if !strings.Contains(rex, "reached 60") || !strings.Contains(rex, "✦ legacy point") || !strings.Contains(rex, "first in the server") || !strings.Contains(rex, "after launch") {
		t.Fatalf("rexxar's line:\n%s", all)
	}
	// Renamed away: the name answers with another character's id.
	a.set("garrosh-hellscream", prof(99, "Garrosh", "Hellscream", "Warrior", 40), "c")
	delete(a.chars, "thrall-earthcaller")
	m.poll(ctx)
	if m.chars[key{"eu", 2}].level != 27 || m.chars[key{"eu", 1}].missing != 1 || m.health["eu"].gone != 2 {
		t.Fatalf("gone: %+v %+v %+v", m.chars[key{"eu", 2}], m.chars[key{"eu", 1}], m.health["eu"])
	}
	if !strings.Contains(text(m.hub(3, 5, "")), "hasn't answered") {
		t.Fatal("hub doesn't say it's missing")
	}
	a.failing = true
	m.poll(ctx)
	if m.health["eu"].err == nil || m.health["eu"].ok.IsZero() {
		t.Fatalf("failing round: %+v", m.health["eu"])
	}
	if r := m.Report(3); !strings.Contains(r.Rows[2][1], "failing") {
		t.Fatalf("report: %+v", r.Rows)
	}
}

func TestFeedCaps(t *testing.T) {
	m, _, f := newTest(t)
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "x"
	}
	ms := make([]moment, len(lines))
	for i, l := range lines {
		ms[i] = moment{line: l}
	}
	ms = append(ms, moment{line: "big", big: true, c: char{class: "mage", avatar: "https://a"}}, moment{line: "big", big: true}, moment{line: "big", big: true}, moment{line: "big", big: true})
	m.post(context.Background(), f, map[snowflake.ID]setup{3: {feed: 40}, 4: {}}, map[snowflake.ID][]moment{3: ms, 4: {{line: "y"}}})
	if len(f.posts) != bigCards+1 || !strings.Contains(text(f.posts[bigCards].Components[0].(discord.ContainerComponent)), "and 6 more") {
		t.Fatalf("posts: %d", len(f.posts))
	}
}

func TestTokenRefresh(t *testing.T) {
	m, a, _ := newTest(t)
	a.set("a-b", prof(1, "A", "B", "Mage", 10), "x")
	var p profile
	if _, _, err := m.api.get(context.Background(), "us", "profile", charPath("a-b"), "", &p); err != nil || p.Level != 10 {
		t.Fatal(err)
	}
	a.denied = true
	if _, _, err := m.api.get(context.Background(), "us", "profile", charPath("a-b"), "", &p); err == nil {
		t.Fatal("401 passed")
	}
	a.denied = false
	if _, _, err := m.api.get(context.Background(), "us", "profile", charPath("a-b"), "", &p); err != nil || a.tokens != 2 {
		t.Fatalf("token not refetched: %v, %d", err, a.tokens)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.api.token = ""
	if _, _, err := m.api.get(ctx, "us", "profile", charPath("a-b"), "", &p); err == nil {
		t.Fatal("cancelled call passed")
	}
}

func TestCommandsAndButtons(t *testing.T) {
	m, a, f := newTest(t)
	a.set("thrall-earthcaller", prof(1, "Thrall", "Earthcaller", "Shaman", 30), "a")
	sub := func(name string) func(p map[string]any) {
		return func(p map[string]any) {
			p["data"].(map[string]any)["options"] = []any{map[string]any{"name": name, "type": 1}}
		}
	}
	for _, c := range []struct{ sub, want string }{{"me", "haven't added"}, {"ladder", "nobody here yet"}, {"who", "nobody here yet"}} {
		e, sent := coretest.Event(t, Name, sub(c.sub))
		if err := m.command(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		if len(*sent) != 1 || !strings.Contains(coretest.Text("", (*sent)[0].Components), c.want) {
			t.Fatalf("/forever %s: %+v", c.sub, *sent)
		}
	}
	if len(m.Commands()) != 1 || len(m.Components()) != 1 || len(m.Modals()) != 1 || m.Name() != Name || m.Perms() == 0 || m.Help().About == "" || m.Want().Required == 0 {
		t.Fatal("module surface")
	}

	// The add form, submitted: the character is listed, the role made and
	// given, and the hub redrawn.
	e, _ := coretest.Modal(t, addID, map[string]string{"name": "thrall earthcaller"}, func(p map[string]any) {
		d := p["data"].(map[string]any)
		d["components"] = append(d["components"].([]any), map[string]any{"type": 18, "component": map[string]any{"type": 3, "custom_id": "region", "values": []any{"eu"}}})
	})
	e.Client().Rest = f
	f.features = []discord.GuildFeature{discord.GuildFeatureRoleIcons}
	if err := m.add(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || f.created[0].Icon == nil || *f.created[0].Permissions != 0 || len(f.added) != 1 || f.added[0] != 70 {
		t.Fatalf("role: created %+v, added %v", f.created, f.added)
	}
	if len(f.updates) != 1 || !strings.Contains(coretest.Text("", *f.updates[0].Components), "✓ added") {
		t.Fatal("hub not redrawn")
	}

	// Refusals from the form.
	for _, c := range []struct {
		vals   map[string]string
		region string
		want   string
	}{{map[string]string{"name": "thrall"}, "eu", "name and surname"}, {map[string]string{"name": "a b"}, "", "region"}} {
		e, _ := coretest.Modal(t, addID, c.vals, func(p map[string]any) {
			if c.region == "" {
				return
			}
			d := p["data"].(map[string]any)
			d["components"] = append(d["components"].([]any), map[string]any{"type": 18, "component": map[string]any{"type": 3, "custom_id": "region", "values": []any{c.region}}})
		})
		if err := m.add(context.Background(), e); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("want %q, got %v", c.want, err)
		}
	}

	// Buttons.
	for _, c := range []struct{ id, want string }{{"forever:ladder", "Thrall"}, {"forever:who", "Thrall"}} {
		b, sent := coretest.Button(t, c.id, nil)
		if err := m.click(context.Background(), b); err != nil {
			t.Fatal(err)
		}
		if len(*sent) != 1 {
			t.Fatalf("%s: %+v", c.id, *sent)
		}
	}
	b, _ := coretest.Button(t, "forever:nope", nil)
	if err := m.click(context.Background(), b); err == nil {
		t.Fatal("unknown verb passed")
	}
	b, _ = coretest.Button(t, "forever", nil)
	if err := m.click(context.Background(), b); err == nil {
		t.Fatal("bare id passed")
	}

	// The role button: given, then taken back.
	b, _ = coretest.Button(t, "forever:role", nil)
	b.Client().Rest = f
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if len(f.added) != 2 {
		t.Fatalf("role press: %v", f.added)
	}
	b, _ = coretest.Button(t, "forever:role", func(p map[string]any) {
		p["member"].(map[string]any)["roles"] = []any{"70"}
	})
	b.Client().Rest = f
	if err := m.click(context.Background(), b); err != nil || len(f.removed) != 1 {
		t.Fatalf("role press back: %v %v", err, f.removed)
	}

	// Dropping the last character takes the role back too.
	b, _ = coretest.Button(t, "forever:drop", func(p map[string]any) {
		p["data"] = map[string]any{"custom_id": "forever:drop", "component_type": 3, "values": []any{"eu.1", "bogus"}}
	})
	b.Client().Rest = f
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if len(m.claims) != 0 || len(f.removed) != 2 {
		t.Fatalf("drop: %d claims, removed %v", len(m.claims), f.removed)
	}
}

func TestSettings(t *testing.T) {
	m, _, _ := newTest(t)
	admin := func(p map[string]any) { p["member"].(map[string]any)["permissions"] = "8" }
	b, _ := coretest.Button(t, "forever:feed", nil)
	if err := m.click(context.Background(), b); err == nil {
		t.Fatal("a member changed the feed")
	}
	b, sent := coretest.Button(t, "forever:feed", func(p map[string]any) {
		admin(p)
		p["data"] = map[string]any{"custom_id": "forever:feed", "component_type": 8, "values": []any{"40"}}
	})
	if err := m.click(context.Background(), b); err != nil || len(*sent) != 1 || m.guilds[3].feed != 40 {
		t.Fatalf("feed: %v %+v", err, m.guilds[3])
	}
	b, _ = coretest.Button(t, "forever:setrole", func(p map[string]any) {
		admin(p)
		p["data"] = map[string]any{"custom_id": "forever:setrole", "component_type": 6, "values": []any{"80"}}
	})
	if err := m.click(context.Background(), b); err != nil || m.guilds[3].role != 80 {
		t.Fatalf("role: %v %+v", err, m.guilds[3])
	}
	// A role skua can't hand out (above hers) is refused at the press.
	b, _ = coretest.Button(t, "forever:role", nil)
	b.Client().Rest = &fake{perms: discord.PermissionManageRoles}
	if err := m.click(context.Background(), b); err == nil {
		t.Fatal("handed out a role above skua's")
	}
	b, _ = coretest.Button(t, "forever:page:1", nil)
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

// text is everything a card says, its sections' text included.
func text(c discord.ContainerComponent) string {
	var out []string
	for _, s := range c.Components {
		switch x := s.(type) {
		case discord.TextDisplayComponent:
			out = append(out, x.Content)
		case discord.SectionComponent:
			for _, t := range x.Components {
				if td, ok := t.(discord.TextDisplayComponent); ok {
					out = append(out, td.Content)
				}
			}
		}
	}
	return strings.Join(out, "\n")
}
