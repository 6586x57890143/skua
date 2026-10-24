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
	m.classDay, m.livery, m.liveryUntil = 12, "ember", 20800
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
		gm.day != m.day || gm.dayN != m.dayN || gm.weekAct != m.weekAct || gm.seasonPP != m.seasonPP || gm.messages != m.messages ||
		gm.classDay != 12 || gm.livery != "ember" || gm.liveryUntil != 20800 {
		t.Errorf("came back as %+v; want %+v", *gm, *m)
	}
	if got.day != s.day || got.raw != s.raw || len(got.history) != 2 || got.lit != s.lit {
		t.Errorf("the server came back as day %d raw %d history %v lit %d", got.day, got.raw, got.history, got.lit)
	}
}

// The day turns at Turn, and a balance shipped mid-day waits for it: a boot
// on the same day keeps the saved one, the turn brings in the new one, and
// a boot on a later day runs the new one straight away.
func TestBalanceWaitsForTheTurn(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 20, h, m, 0, 0, time.UTC) }
	if DayOf(at(5, 29)) != DayOf(at(5, 30))-1 || DayOf(at(5, 30)) != DayOf(at(23, 59)) || !dayStart(DayOf(at(12, 0))).Equal(at(5, 30)) {
		t.Fatal("the day doesn't turn at 05:30 utc")
	}

	db := testDB(t)
	ctx := context.Background()
	today := DayOf(t0)
	before := Default
	before.Pool, before.Version = 1, "before"
	if err := saveBalance(ctx, db, today, &before); err != nil {
		t.Fatal(err)
	}
	m := testModule(db)
	if err := m.st.loadBalance(ctx, db, &Default, today); err != nil {
		t.Fatal(err)
	}
	if b := m.st.bal(); b.Pool != 1 || b.Version != "before" || len(b.QuestKinds) != len(Default.QuestKinds) {
		t.Fatalf("a mid-day boot runs %s with pool %d; want the saved balance", b.Version, b.Pool)
	}

	last := today
	m.now = func() time.Time { return t0.Add(24 * time.Hour) }
	m.tick(ctx, &last)
	if m.st.bal() != &Default {
		t.Fatal("the turn kept the old balance")
	}
	later := newState(&before, 42)
	if err := later.loadBalance(ctx, db, &Default, today+1); err != nil || later.bal().Version != Default.Version {
		t.Errorf("a boot after the turn runs %s, %v", later.bal().Version, err)
	}
	if err := later.loadBalance(ctx, db, &Default, today+2); err != nil || later.bal() != &Default {
		t.Errorf("a boot on a later day runs %s, %v", later.bal().Version, err)
	}
}
