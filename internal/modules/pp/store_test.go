package pp

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/store"
)

func testDB(t *testing.T) DB {
	t.Helper()
	dsn := os.Getenv("SKUA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKUA_TEST_DATABASE_URL is not set")
	}
	pool, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// A save and a load give back every member and server as they were, so a
// deploy carries on mid-day.
func TestSaveLoadRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	st := newState(&Default, 42)
	s := st.server(g)
	a := old(1)
	st.chat(s, g, a, "a real thing to say", t0)
	m := s.members[a]
	m.class, m.classSeason, m.voiceMin = "monk", 3, 17
	s.history = []int64{100, 200}
	if n, err := st.flush(ctx, db); err != nil || n == 0 {
		t.Fatalf("flush: %d, %v", n, err)
	}
	// A second save after a change overwrites rather than adding a row.
	m.total += 5
	m.dirty = true
	if _, err := st.flush(ctx, db); err != nil {
		t.Fatal(err)
	}

	back := newState(&Default, 42)
	if err := back.load(ctx, db); err != nil {
		t.Fatal(err)
	}
	got := back.server(g)
	gm := got.members[a]
	if gm == nil {
		t.Fatal("the member didn't come back")
	}
	if gm.total != m.total || gm.class != "monk" || gm.classSeason != 3 || gm.voiceMin != 17 ||
		gm.day != m.day || gm.dayN != m.dayN || gm.weekAct != m.weekAct || gm.seasonPP != m.seasonPP || gm.messages != m.messages {
		t.Errorf("came back as %+v; want %+v", *gm, *m)
	}
	if got.day != s.day || got.raw != s.raw || len(got.history) != 2 || got.lit != s.lit {
		t.Errorf("the server came back as day %d raw %d history %v lit %d", got.day, got.raw, got.history, got.lit)
	}
}
