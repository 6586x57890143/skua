package forever

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/disgoorg/disgo/events"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/store"
)

// pool is Postgres from SKUA_TEST_DATABASE_URL, migrated, as CI runs it.
func pool(t *testing.T) DB {
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
	return p
}

// TestStoreRoundTrip is everything forever keeps surviving a restart, and
// a server's leaving forgetting its part.
func TestStoreRoundTrip(t *testing.T) {
	db := pool(t)
	ctx := context.Background()
	const guild = 912345
	if err := dropGuild(ctx, db, guild); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	c := char{crafts: map[string]int{"tailoring": 141}, key: key{"eu", 77001}, name: "Thrall", surname: "Earthcaller", class: "shaman", level: 30, ruleset: "normal", reached: at, avatar: "https://a", modified: "m"}
	if err := saveChar(ctx, db, c); err != nil {
		t.Fatal(err)
	}
	if err := saveDings(ctx, db, c.key, 28, 30, at); err != nil {
		t.Fatal(err)
	}
	if err := saveClaim(ctx, db, claim{guild: guild, user: 5, k: c.key, at: at}); err != nil {
		t.Fatal(err)
	}
	if err := saveSetup(ctx, db, guild, setup{feed: 40, role: 70}); err != nil {
		t.Fatal(err)
	}
	if err := saveDay(ctx, db, guild, "2026-11-11", map[key]int{c.key: 2}); err != nil {
		t.Fatal(err)
	}
	st, err := load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if st.days[guild][c.key] != 2 || st.dayOf[guild] == "" {
		t.Fatalf("day's chart: %v %q", st.days[guild], st.dayOf[guild])
	}
	got := st.chars[c.key]
	if got == nil || got.level != 30 || got.crafts["tailoring"] != 141 || got.surname != "Earthcaller" || !got.reached.Equal(at) || st.guilds[guild] != (setup{feed: 40, role: 70}) {
		t.Fatalf("loaded %+v, %+v", got, st.guilds[guild])
	}
	if err := dropClaim(ctx, db, guild, c.key); err != nil {
		t.Fatal(err)
	}
	if err := dropGuild(ctx, db, guild); err != nil {
		t.Fatal(err)
	}
	if st, _ := load(ctx, db); st.guilds[guild] != (setup{}) {
		t.Fatal("guild not forgotten")
	}
}

// TestLifecycle is the add button, a server leaving and the round loop
// stopping with its context.
func TestLifecycle(t *testing.T) {
	m, a, _ := newTest(t)
	a.set("a-b", prof(1, "A", "B", "Mage", 12), "x")
	if _, err := m.list(context.Background(), 3, 5, "us", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	b, _ := coretest.Button(t, "forever:add", nil)
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	m.OnEvent(&events.GuildLeave{GenericGuild: &events.GenericGuild{GuildID: 3}})
	if len(m.claims) != 0 {
		t.Fatal("left server still listed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.loop(ctx)
	if m.round.IsZero() {
		t.Fatal("the loop didn't run its first round before stopping")
	}
	if FromEnv().Configured() != (os.Getenv("SKUA_BNET_ID") != "" && os.Getenv("SKUA_BNET_SECRET") != "") {
		t.Fatal("Configured")
	}
	if where(char{key: key{region: "kr"}}) != "🇰🇷" || where(char{key: key{region: "xx"}}) != "xx" {
		t.Fatal("blank ruleset")
	}
}
