package pp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// The switch shows on a worn livery's card, flips and is kept, and isn't
// there for a livery not worn.
func TestRenewSwitch(t *testing.T) {
	m, y := yardServer(1, 10000)
	s := m.st.server(3)
	s.open = true // launched
	s.yardOpen = true
	db := m.db.(*fakeDB)
	press := func(id string) string {
		e, sent := coretest.Button(t, id, nil)
		e.Client().Rest = y
		if err := m.press(context.Background(), e); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return js(*sent) + strings.Join(y.edits, "")
	}
	if err := m.toggleRenew(context.Background(), 3, 5); !errors.Is(err, errNoLivery) {
		t.Errorf("renewing with no livery: %v", err)
	}
	mem := s.members[5]
	mem.livery, mem.liveryUntil = "tallow", DayOf(m.now())+3
	if got := press("pp:yardrenew"); !mem.renew || !strings.Contains(got, "renews each week") {
		t.Errorf("switched on: %v %s", mem.renew, got)
	}
	if !strings.Contains(strings.Join(db.execs, ";"), "insert into pp_renew") {
		t.Errorf("not kept: %v", db.execs)
	}
	if got := press("pp:yardrenew"); mem.renew || !strings.Contains(got, "renew each week") {
		t.Errorf("switched off: %v %s", mem.renew, got)
	}
	d, _ := dyeOf("hide")
	msg, err := m.st.dyeMessage(mem, d, DayOf(m.now()), true)
	if err != nil || strings.Contains(js(msg), "pp:yardrenew") {
		t.Errorf("the switch on a livery not worn: %v", err)
	}
	// One of their own has the switch on its own page.
	mem.livery = ownKey
	if err := m.toggleRenew(context.Background(), 3, 5); err != nil || !mem.renew {
		t.Errorf("their own: %v %v", err, mem.renew)
	}
	own, err := m.st.ownMessage(mem, nil, ownState{wearing: true, renews: true, days: leaseDays, ahead: leaseCap})
	if err != nil || !strings.Contains(js(own), "renews each week") {
		t.Errorf("their own page: %v %s", err, js(own))
	}
}

// On a lease's last day the turn pays another term for a bird who asked,
// at that day's price, and leaves alone anyone who didn't, can't pay,
// may no longer wear it, or isn't near the end.
func TestRenewLeases(t *testing.T) {
	m, _ := yardServer(1, 10000)
	s := m.st.server(3)
	s.open = true // launched
	today := DayOf(m.now())
	d, _ := dyeOf("tallow")
	price := m.st.leasePrice(d)
	mem := s.members[5]
	mem.livery, mem.liveryUntil, mem.renew = "tallow", today+1, true
	purse := mem.purse
	m.renewLeases(today)
	if mem.liveryUntil != today+1+leaseDays || mem.purse != purse-price || !mem.dirty {
		t.Fatalf("renewed to %d (want %d), purse %d (want %d)", mem.liveryUntil, today+1+leaseDays, mem.purse, purse-price)
	}
	if e := s.pending[len(s.pending)-1]; e.reason != reasonLease || e.inputs[1] != mem.liveryUntil {
		t.Errorf("the ledger: %+v", e)
	}
	for _, c := range []struct {
		name  string
		setup func()
	}{
		{"not near the end", func() {}},
		{"not asked", func() { mem.liveryUntil, mem.renew = today, false }},
		{"can't pay", func() { mem.liveryUntil, mem.purse = today, price-1 }},
		{"faded", func() { mem.liveryUntil, mem.purse = today-fadeDays, purse }},
		{"perch too low", func() { mem.liveryUntil, mem.livery = today, "ink" }},
		{"own, no role", func() { mem.livery = ownKey }},
	} {
		mem.renew = true
		c.setup()
		until, have := mem.liveryUntil, mem.purse
		m.renewLeases(today)
		if mem.liveryUntil != until || mem.purse != have {
			t.Errorf("%s: renewed", c.name)
		}
	}
	// One of their own at its own price.
	s.own = map[snowflake.ID]ownWorn{5: {role: 9, week: 100}}
	mem.livery, mem.liveryUntil, mem.purse = ownKey, today, purse
	own, _, _ := m.st.ownLease(s, 5, 100, mem.rung)
	m.renewLeases(today)
	if mem.liveryUntil != today+leaseDays || mem.purse != purse-own {
		t.Errorf("their own: to %d, purse %d", mem.liveryUntil, mem.purse)
	}
}

// Who renews comes back after a restart.
func TestRenewLoads(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	st := newTestState()
	g := old(900)
	s := st.server(g)
	st.chat(s, g, old(1), "a real thing to say", t0)
	if _, err := st.flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `insert into pp_renew (guild_id, user_id) values ($1, $2) on conflict do nothing`, int64(g), int64(old(1))); err != nil {
		t.Fatal(err)
	}
	back := newTestState()
	if err := back.load(ctx, db); err != nil {
		t.Fatal(err)
	}
	if !back.server(g).members[old(1)].renew {
		t.Error("renewing didn't come back")
	}
}

// The morning sheet never warns a bird whose standing order went through,
// and tells one whose purse was short why the livery fades.
func TestRenewOnTheSheet(t *testing.T) {
	m, _ := yardServer(1, 10000)
	s := m.st.server(3)
	s.open = true // launched
	today := DayOf(m.now())
	mem := s.members[5]
	mem.livery, mem.liveryUntil, mem.renew = "tallow", today, true
	m.renewLeases(today)
	for _, n := range m.notes(s, 0, mem, today) {
		if strings.Contains(n, "livery") {
			t.Errorf("paid, and still told %q", n)
		}
	}
	mem.liveryUntil, mem.purse = today, 0
	m.renewLeases(today)
	if n := m.notes(s, 0, mem, today); len(n) == 0 || !strings.Contains(n[0], "purse short") {
		t.Errorf("short, and told %q", n)
	}
	mem.renew = false
	if n := m.notes(s, 0, mem, today); len(n) == 0 || !strings.Contains(n[0], "unless the yard is paid") {
		t.Errorf("no order, and told %q", n)
	}
}
