package forever

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// TestSharedCharacter is a character two servers list: the second add
// keeps skua's record, so the next round's climb reaches both feeds, and a
// character skua didn't see reach its level has no time from launch.
func TestSharedCharacter(t *testing.T) {
	m, a, f := newTest(t)
	ctx := context.Background()
	m.guilds[3], m.guilds[4] = setup{feed: 40}, setup{feed: 41}
	a.set("thrall-earthcaller", prof(1, "Thrall", "Earthcaller", "Shaman", 59), "a")
	if _, err := m.list(ctx, 3, 5, "eu", "thrall", "earthcaller", false); err != nil {
		t.Fatal(err)
	}
	if !m.chars[key{"eu", 1}].reached.IsZero() {
		t.Fatal("a new character's reach time was made up")
	}
	a.set("thrall-earthcaller", prof(1, "Thrall", "Earthcaller", "Shaman", 60), "b")
	if _, err := m.list(ctx, 4, 6, "eu", "thrall", "earthcaller", false); err != nil {
		t.Fatal(err)
	}
	if m.chars[key{"eu", 1}].level != 59 {
		t.Fatal("the second add overwrote the shared record")
	}
	m.poll(ctx)
	if len(f.posts) != 2 {
		t.Fatalf("want a feed card in both servers, got %d", len(f.posts))
	}
	if !strings.Contains(text(f.posts[0].Components[0].(discord.ContainerComponent)), "after launch") {
		t.Fatal("a climb skua saw has no time from launch")
	}
}

// TestGatedOff is a server that switched forever off: its characters
// aren't read and its feed stays quiet.
func TestGatedOff(t *testing.T) {
	m, a, f := newTest(t)
	ctx := context.Background()
	m.guilds[3] = setup{feed: 40}
	a.set("a-b", prof(1, "A", "B", "Mage", 19), "a")
	if _, err := m.list(ctx, 3, 5, "us", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	m.Gate(func(snowflake.ID) bool { return false })
	a.set("a-b", prof(1, "A", "B", "Mage", 20), "b")
	m.poll(ctx)
	if len(f.posts) != 0 || m.chars[key{"us", 1}].level != 19 {
		t.Fatal("a switched-off server was read or posted to")
	}
}

// TestLegacyMidClimb is a climb from 44 to 50 in one logout: the feed says
// 50, and still marks the Legacy point earned at 45.
func TestLegacyMidClimb(t *testing.T) {
	m, a, f := newTest(t)
	ctx := context.Background()
	m.guilds[3] = setup{feed: 40}
	a.set("a-b", prof(1, "A", "B", "Mage", 44), "a")
	if _, err := m.list(ctx, 3, 5, "us", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	a.set("a-b", prof(1, "A", "B", "Mage", 50), "b")
	m.poll(ctx)
	got := text(f.posts[0].Components[0].(discord.ContainerComponent))
	if !strings.Contains(got, "reached 50") || !strings.Contains(got, "✦ legacy point") {
		t.Fatalf("feed:\n%s", got)
	}
}

// TestReportIsThisServers is the not-answering count kept to the server
// whose page it is.
func TestReportIsThisServers(t *testing.T) {
	m, a, _ := newTest(t)
	ctx := context.Background()
	a.set("a-b", prof(1, "A", "B", "Mage", 20), "a")
	a.set("c-d", prof(2, "C", "D", "Mage", 20), "a")
	if _, err := m.list(ctx, 3, 5, "eu", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.list(ctx, 4, 5, "eu", "c", "d", false); err != nil {
		t.Fatal(err)
	}
	delete(a.chars, "c-d")
	m.poll(ctx)
	if r := m.Report(3); !strings.Contains(r.Rows[len(r.Rows)-1][1], "ok") {
		t.Fatalf("server 3 sees another server's missing character: %+v", r.Rows)
	}
	if r := m.Report(4); !strings.Contains(r.Rows[len(r.Rows)-1][1], "1 not answering") {
		t.Fatalf("server 4: %+v", r.Rows)
	}
}

// TestLimits is the per-member cap held at the add, after Blizzard answers.
func TestLimits(t *testing.T) {
	m, a, _ := newTest(t)
	ctx := context.Background()
	a.set("a-b", prof(1, "A", "B", "Mage", 20), "a")
	for i := range perMember {
		m.claims = append(m.claims, claim{guild: 3, user: 5, k: key{"us", int64(100 + i)}})
	}
	if _, err := m.list(ctx, 3, 5, "us", "a", "b", false); err == nil || !strings.Contains(err.Error(), "take one off") {
		t.Fatalf("over the cap: %v", err)
	}
	e, _ := coretest.Modal(t, addID, map[string]string{"name": "a b"}, func(p map[string]any) {
		d := p["data"].(map[string]any)
		d["components"] = append(d["components"].([]any), map[string]any{"type": 18, "component": map[string]any{"type": 3, "custom_id": "region", "values": []any{"us"}}})
	})
	if err := m.add(ctx, e); err == nil || !strings.Contains(err.Error(), "take one off") {
		t.Fatalf("form over the cap: %v", err)
	}
}

// TestDeletedRole is the forever role deleted by an admin: the next add
// makes a new one.
func TestDeletedRole(t *testing.T) {
	m, _, f := newTest(t)
	m.guilds[3] = setup{role: 999}
	if got := m.ensureRole(context.Background(), f, 3); got != 70 || len(f.created) != 1 || m.guilds[3].role != 70 {
		t.Fatalf("role: %v, created %d", got, len(f.created))
	}
	if got := m.ensureRole(context.Background(), f, 3); got != 70 || len(f.created) != 1 {
		t.Fatal("made the role twice")
	}
}

// TestLiveShape is what the live probe of 2026-11-11 showed: a ding is
// dated by the logout that saved it, and two on one level are ordered by
// how far into it they are.
func TestLiveShape(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	out := now.Add(-3 * time.Minute)
	if got := reachedAt(profile{Logout: out.UnixMilli()}, now); !got.Equal(out) {
		t.Fatalf("dated %v, want the logout %v", got, out)
	}
	if got := reachedAt(profile{Logout: now.Add(time.Hour).UnixMilli()}, now); !got.Equal(now) {
		t.Fatal("a logout ahead of skua's clock was believed")
	}
	if got := reachedAt(profile{}, now); !got.Equal(now) {
		t.Fatal("no logout")
	}
	a, b := &char{level: 42, xp: 900}, &char{level: 42, xp: 100, reached: now}
	if byLevel(a, b) >= 0 {
		t.Fatal("less of the level ranked first")
	}
	var p profile
	if err := json.Unmarshal([]byte(`{"id":5,"name":"Rok","surname":"Androl","level":25,"experience":5824,"last_login_timestamp":1794351772000,"equipped_item_level":17,"character_class":{"name":"Warrior","id":1},"ruleset":{"name":"Roleplay PvP","id":15},"faction":{"type":"ALLIANCE"}}`), &p); err != nil {
		t.Fatal(err)
	}
	var c char
	apply(&c, p)
	if c.xp != 5824 || c.ruleset != "roleplay pvp" || c.class != "warrior" {
		t.Fatalf("live profile read as %+v", c)
	}
}

// TestCraftCheckpoints is a crafting profession crossing a Legacy
// checkpoint: the feed tells it with its ✦, the card shows the track, and
// gathering professions are left out.
func TestCraftCheckpoints(t *testing.T) {
	m, a, f := newTest(t)
	ctx := context.Background()
	m.guilds[3] = setup{feed: 40}
	a.set("a-b", prof(1, "A", "B", "Mage", 31), "a")
	a.skill["a-b"] = 150
	if _, err := m.list(ctx, 3, 5, "eu", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	// list keeps a new character's professions only once a round reads them.
	a.set("a-b", prof(1, "A", "B", "Mage", 32), "b")
	m.poll(ctx)
	a.set("a-b", prof(1, "A", "B", "Mage", 33), "c")
	a.skill["a-b"] = 226
	f.posts = nil
	m.poll(ctx)
	if len(f.posts) != 1 {
		t.Fatalf("posts: %d", len(f.posts))
	}
	got := text(f.posts[0].Components[0].(discord.ContainerComponent))
	if !strings.Contains(got, "reached enchanting 225 · ✦ legacy point") {
		t.Fatalf("feed:\n%s", got)
	}
	card := text(m.hub(3, 5, ""))
	if !strings.Contains(card, "enchanting 225 ✦ 226/300") || strings.Contains(card, "herbalism") {
		t.Fatalf("card:\n%s", card)
	}
	c := m.chars[key{"eu", 1}]
	if got := parseCrafts(formatCrafts(c.crafts)); got["enchanting"] != 226 || len(got) != 1 {
		t.Fatalf("crafts round trip: %v", got)
	}
	if parseCrafts("")["x"] != 0 || len(parseCrafts("bad,=,a=b")) != 0 {
		t.Fatal("bad crafts parsed")
	}
	if legacyLine(char{level: 60, crafts: map[string]int{"tailoring": 300, "alchemy": 40}}) != "-# legacy · 25 ✦ · 45 ✦ · 60 ✦ · alchemy 40/150 · tailoring 300 ✦" {
		t.Fatal(legacyLine(char{level: 60, crafts: map[string]int{"tailoring": 300, "alchemy": 40}}))
	}
	if legacyPoints([]*char{c, {class: "mage", level: 30, crafts: map[string]int{"enchanting": 230}}}) != 3 {
		t.Fatal("legacy points count a checkpoint twice")
	}
}

// TestRenamedReAdd is a renamed character added again: skua's record takes
// the new name, so its polls find it.
func TestRenamedReAdd(t *testing.T) {
	m, a, _ := newTest(t)
	ctx := context.Background()
	a.set("thrall-earthcaller", prof(1, "Thrall", "Earthcaller", "Shaman", 30), "a")
	if _, err := m.list(ctx, 3, 5, "eu", "thrall", "earthcaller", false); err != nil {
		t.Fatal(err)
	}
	b, _ := coretest.Button(t, "forever:drop", func(p map[string]any) {
		p["data"] = map[string]any{"custom_id": "forever:drop", "component_type": 3, "values": []any{"eu.1"}}
	})
	b.Client().Rest = &fake{}
	if err := m.click(ctx, b); err != nil {
		t.Fatal(err)
	}
	if len(m.chars) != 0 {
		t.Fatal("a character nobody lists was kept")
	}
	if _, err := m.list(ctx, 4, 6, "eu", "thrall", "earthcaller", false); err != nil {
		t.Fatal(err)
	}
	delete(a.chars, "thrall-earthcaller")
	a.set("thrall-stormborn", prof(1, "Thrall", "Stormborn", "Shaman", 31), "b")
	if _, err := m.list(ctx, 3, 5, "eu", "thrall", "stormborn", false); err != nil {
		t.Fatal(err)
	}
	if c := m.chars[key{"eu", 1}]; c.surname != "Stormborn" || c.slug() != "thrall-stormborn" {
		t.Fatalf("renamed record: %+v", c)
	}
}

// TestStaleIsBaseline is a character unread for a while (its server was
// switched off): its next change is a quiet baseline, not a feed post.
func TestStaleIsBaseline(t *testing.T) {
	m, a, f := newTest(t)
	ctx := context.Background()
	m.guilds[3] = setup{feed: 40}
	a.set("a-b", prof(1, "A", "B", "Mage", 30), "a")
	if _, err := m.list(ctx, 3, 5, "us", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	m.poll(ctx)
	clock := time.Now()
	m.now = func() time.Time { return clock.Add(2 * time.Hour) }
	a.set("a-b", prof(1, "A", "B", "Mage", 60), "b")
	m.poll(ctx)
	if len(f.posts) != 0 || m.chars[key{"us", 1}].level != 60 {
		t.Fatalf("a stale climb posted %d cards", len(f.posts))
	}
}

// TestRoleRefusalInPlace is the role button refused after it was deferred:
// the refusal edits the deferred reply instead of leaving it thinking.
func TestRoleRefusalInPlace(t *testing.T) {
	m, _, _ := newTest(t)
	m.guilds[3] = setup{role: 80}
	f := &fake{perms: discord.PermissionManageRoles}
	b, _ := coretest.Button(t, "forever:role", nil)
	b.Client().Rest = f
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if len(f.updates) != 1 || !strings.Contains(text((*f.updates[0].Components)[0].(discord.ContainerComponent)), "✗ skua can't hand out that role") {
		t.Fatalf("updates: %+v", f.updates)
	}
}

// TestSettingsDontClobber is the role made while an admin picks the feed:
// both changes stay.
func TestSettingsDontClobber(t *testing.T) {
	m, _, _ := newTest(t)
	ctx := context.Background()
	if err := m.setGuild(ctx, 3, func(s *setup) { s.feed = 40 }); err != nil {
		t.Fatal(err)
	}
	if err := m.setGuild(ctx, 3, func(s *setup) { s.role = 70 }); err != nil {
		t.Fatal(err)
	}
	if m.guilds[3] != (setup{feed: 40, role: 70}) {
		t.Fatalf("settings: %+v", m.guilds[3])
	}
}
