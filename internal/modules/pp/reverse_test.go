package pp

import (
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

// badJoinder pays u the joinder today's stale balance paid, past the
// ceiling labour now holds pay to, as the build before it did. s.mu held.
func badJoinder(s *server, m *member, u snowflake.ID, today int64) {
	got := int64(7595640)
	lv := Rules.levy(got)
	m.purse, m.total, m.seasonPP = m.purse+got-lv, m.total+got, m.seasonPP+got
	s.seasonMint += got
	s.treasury, s.levyOpen, s.levyFull = s.treasury+lv, s.levyOpen+lv, s.levyFull+lv
	s.append(u, today, reasonJoinder, got-lv, []int64{4, got, lv, 0})
}

// No payout passes its ceiling, whatever asks for it, and the alarm says
// so; a balance that would pay past one is never sane.
func TestPayCeiling(t *testing.T) {
	st, s := testState()
	var alarms int
	st.alarm = func(string, ...any) { alarms++ }
	m := st.member(s, guild, old(1), DayOf(t0))
	lv, kept := st.labour(s, m, 7595640)
	if most := paysMost * st.pot(); m.total != most || lv != Rules.levy(most) || kept != 0 || alarms != 1 {
		t.Errorf("paid %d (levy %d), want %d; %d alarms", m.total, lv, most, alarms)
	}
	if err := Default.Sane(); err != nil {
		t.Fatalf("the balance shipped isn't sane: %v", err)
	}
	stale := Default
	stale.Joinder = [3]int64{6000, 9000, 12000}
	if stale.Sane() == nil {
		t.Error("joinders saved in pp, read as pot day shares, passed")
	}
	stale = Default
	stale.Prizes = [5]int64{150000, 100000, 75000, 50000, 25000}
	if stale.Sane() == nil {
		t.Error("prizes saved in pp, read as pot day hundredths, passed")
	}
	stale = Default
	stale.QuestPP[2] = 4 * Default.PotDay(Rules)
	if stale.Sane() == nil {
		t.Error("a golden quest of four pot days passed")
	}
}

// A joinder paid 700 times over and the rungs and lease bought with it
// come undone: the purse is what it was plus a regular joinder, the rung
// and the lease go back, the treasury gives back the levy it took, and
// the ledger still sums to the purse.
func TestReverse(t *testing.T) {
	st, s := testState()
	u := old(1)
	today := DayOf(t0)
	m := st.member(s, guild, u, today)
	m.purse, m.rung = 100*unit, 0
	s.append(u, today, reasonOpen, m.purse, nil)
	treasury := s.treasury

	badJoinder(s, m, u, today)
	from := s.seq
	for range 3 {
		if err := st.climb(s, guild, u, today); err != nil {
			t.Fatal(err)
		}
	}
	m.livery, m.liveryUntil = "juniper", today+28
	m.purse -= 94944
	st.burn(s, u, today, reasonLease, 94944, []int64{3, today + 28})

	var rows []entry
	for _, e := range s.pending {
		if e.user == u && e.seq >= from {
			rows = append(rows, e)
		}
	}
	r, err := st.reverse(s, u, rows, today)
	if err != nil {
		t.Fatal(err)
	}
	pay := st.bal().JoinderPay(4)
	plv := Rules.levy(pay)
	if m.rung != 0 || r.RungFrom != 3 || !r.Faded || m.liveryUntil != today-fadeDays {
		t.Errorf("rung %d, %+v, until %d", m.rung, r, m.liveryUntil)
	}
	if m.purse != 100*unit+pay-plv || r.Paid != pay-plv {
		t.Errorf("purse %d, want %d", m.purse, 100*unit+pay-plv)
	}
	if s.treasury != treasury+plv || s.treasury != s.levyOpen {
		t.Errorf("treasury %d, levy open %d, want %d", s.treasury, s.levyOpen, treasury+plv)
	}
	var sum int64
	for _, e := range s.pending {
		if e.user == u {
			sum += e.delta
		}
	}
	if sum != m.purse-m.dayGot || m.total != pay || s.seasonBurn != 0 {
		t.Errorf("ledger %d, purse %d, total %d, burn %d", sum, m.purse, m.total, s.seasonBurn)
	}
	if _, err := st.reverse(s, u, rows[1:], today); err == nil {
		t.Error("reversed rows that don't start at a joinder")
	}
}
