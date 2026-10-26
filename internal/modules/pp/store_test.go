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
	// Another version saved for today is never run: its numbers may be in
	// units this build reads otherwise. Today runs this build's.
	stale := Default
	stale.Joinder, stale.Version = [3]int64{6000, 9000, 12000}, "0.10.6"
	if err := saveBalance(ctx, db, today, &stale); err != nil {
		t.Fatal(err)
	}
	m := testModule(db)
	if err := m.st.loadBalance(ctx, db, &Default, today); err != nil {
		t.Fatal(err)
	}
	if b := m.st.bal(); b != &Default {
		t.Fatalf("a mid-day boot ran the saved %s, joinder %v", b.Version, b.Joinder)
	}
	// The same version saved for today is kept: a restart mid-day carries on.
	before := Default
	before.Pool = 1
	if err := saveBalance(ctx, db, today, &before); err != nil {
		t.Fatal(err)
	}
	if err := m.st.loadBalance(ctx, db, &Default, today); err != nil {
		t.Fatal(err)
	}
	if b := m.st.bal(); b.Pool != 1 || len(b.QuestKinds) != len(Default.QuestKinds) {
		t.Fatalf("a mid-day boot runs pool %d; want the saved balance", b.Pool)
	}
	before.Version = "before"

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

// The gate book comes back as written, a leave over the join.
func TestGuestsRoundTrip(t *testing.T) {
	db := testDB(t)
	m := testModule(db)
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	ctx := context.Background()
	m.saveGuest(ctx, g, old(1), guest{by: old(2), day: 5, seen: 6})
	m.saveGuest(ctx, g, old(1), guest{by: old(2), day: 5, gone: 9})
	back := newState(&Default, 42)
	if err := back.load(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if got := back.server(g).guests[old(1)]; got == nil || *got != (guest{by: old(2), day: 5, gone: 9, seen: 6}) {
		t.Errorf("came back as %+v", got)
	}
}

// Reverse puts a bad joinder right in the database, and the audit after it
// finds every purse and the treasury agree with their rows.
func TestReverseInStore(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	st := newState(&Default, 42)
	s := st.server(g)
	u := old(1)
	today := DayOf(time.Now())
	m := st.member(s, g, u, today)
	m.purse = 10 * unit
	s.append(u, today, reasonOpen, m.purse, nil)
	lv, kept := st.labour(s, m, 7595640)
	s.append(u, today, reasonJoinder, 7595640-lv-kept, []int64{4, 7595640, lv, kept})
	from := s.seq
	if err := st.climb(s, g, u, today); err != nil {
		t.Fatal(err)
	}
	if _, err := st.flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := Reverse(ctx, db, g, from, []snowflake.ID{old(2)}); err == nil {
		t.Error("reversed a member with no joinder")
	}
	got, err := Reverse(ctx, db, g, from, []snowflake.ID{u})
	if err != nil || len(got) != 1 || got[0].RungTo != 0 {
		t.Fatalf("reversing: %+v %v", got, err)
	}
	back := newState(&Default, 42)
	if err := back.load(ctx, db); err != nil {
		t.Fatal(err)
	}
	if bad, err := back.audit(ctx, db); err != nil || bad != 0 {
		t.Errorf("the audit after: %d disagree, %v", bad, err)
	}
	if bm := back.server(g).members[u]; bm.purse != 10*unit+got[0].Paid || bm.rung != 0 {
		t.Errorf("came back with purse %d rung %d", bm.purse, bm.rung)
	}
}
