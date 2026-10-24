package pp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// A livery of one's own costs more as the heralds' roll fills: nothing
// more empty, Rules.RollRise percent more full, with the square of how
// full, a member counted once.
func TestRollFactor(t *testing.T) {
	s := newServer(guild)
	if s.rollFactor(0) != 100 {
		t.Fatalf("an empty roll: %d", s.rollFactor(0))
	}
	s.slots = 10
	for u := range 5 {
		s.wearOwn(snowflakeOf(u), 1, 100, 0)
	}
	s.ask(snowflakeOf(0), 9) // a change by someone already on it
	if s.rolled(0) != 5 || s.rollFactor(0) != 100+Rules.RollRise/4 {
		t.Errorf("half full: %d names, %d", s.rolled(0), s.rollFactor(0))
	}
	// A petitioner is priced as on the roll, so a grant doesn't move their
	// price, and someone on it already counts once.
	if s.rollFactor(snowflakeOf(7)) != s.rollFactor(0)+Rules.RollRise*11/100 || s.rollFactor(snowflakeOf(1)) != s.rollFactor(0) {
		t.Errorf("a petitioner: %d, one on the roll: %d", s.rollFactor(snowflakeOf(7)), s.rollFactor(snowflakeOf(1)))
	}
	for u := 5; u < 10; u++ {
		s.ask(snowflakeOf(u), int64(u))
	}
	if s.rollFactor(0) != 100+Rules.RollRise {
		t.Errorf("full: %d", s.rollFactor(0))
	}
	s.slots = 0
	if s.rollFactor(0) != 100+Rules.RollRise*100/(rollSlots*rollSlots) {
		t.Errorf("the default room: %d", s.rollFactor(0))
	}
}

// Past the bell a lease runs a season for three weeks' price, and may run
// two seasons ahead.
func TestSeasonLease(t *testing.T) {
	if d, w, a := term(5); d != leaseDays || w != 1 || a != leaseCap {
		t.Errorf("the bell: %d %d %d", d, w, a)
	}
	top := int64(pastTheBell)
	if d, w, a := term(top); d != seasonDays || w != seasonWeeks || a != 2*seasonDays {
		t.Errorf("past the bell: %d %d %d", d, w, a)
	}
	m, _ := yardServer(top, 100000)
	tallow, _ := dyeOf("tallow")
	before := m.st.member(m.st.server(3), 3, 5, DayOf(m.now())).purse
	if _, err := m.leased(context.Background(), 3, 5, tallow); err != nil {
		t.Fatal(err)
	}
	mem := m.st.server(3).members[5]
	if mem.liveryUntil != DayOf(m.now())+seasonDays || before-mem.purse != m.st.leasePrice(tallow)*seasonWeeks {
		t.Errorf("a season: until %d, paid %d", mem.liveryUntil, before-mem.purse)
	}
	msg, _ := m.st.dyeMessage(mem, tallow, DayOf(m.now()))
	if !strings.Contains(js(msg), "another season") {
		t.Errorf("the card: %s", js(msg))
	}
	if _, err := m.leased(context.Background(), 3, 5, tallow); err != nil {
		t.Fatal(err)
	}
	if _, err := m.leased(context.Background(), 3, 5, tallow); !errors.Is(err, errDyeAhead) {
		t.Errorf("a third season ahead: %v", err)
	}
}

// An admin sets the roll's room from the livery settings, from the
// choices given and no other.
func TestRollSlots(t *testing.T) {
	m := testModule(&fakeDB{})
	if got := js(m.liverySettings(3)); !strings.Contains(got, "pp:slots") || !strings.Contains(got, `"value":"40","description":"liveries of one's own grow dearer as it fills","default":true`) {
		t.Fatalf("the settings: %s", got)
	}
	pick := func(v string) error {
		e, _ := coretest.Button(t, "pp:slots", func(p map[string]any) {
			p["data"] = map[string]any{"custom_id": "pp:slots", "component_type": 3, "values": []any{v}}
		})
		return m.press(context.Background(), e)
	}
	if err := pick("80"); err != nil || m.st.server(3).slots != 80 {
		t.Fatalf("80: %v %d", err, m.st.server(3).slots)
	}
	if err := pick("7"); !errors.Is(err, errRollRoom) || m.st.server(3).slots != 80 {
		t.Errorf("7: %v", err)
	}
	m.Admin = func(discord.Interaction) bool { return false }
	if err := pick("10"); !errors.Is(err, errNotAdmin) {
		t.Errorf("a member set the roll: %v", err)
	}
}

// snowflakeOf is member n of a test's roll.
func snowflakeOf(n int) snowflake.ID { return snowflake.ID(100 + n) }
