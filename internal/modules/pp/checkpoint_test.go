package pp

import (
	"strings"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/guard"
)

// When the week turns, each board's best are paid from the treasury into
// their purse but not their level; both sides go into the ledger, and the
// ledger channel hears about it. A treasury short of every prize pays each
// place the same share of its own.
func TestCheckpoint(t *testing.T) {
	m := testModule(&fakeDB{})
	r := &postRest{}
	m.rest, m.Guard = r, guard.New()
	st := m.st
	s := st.server(guild)
	s.logChannel = 88

	// Six talkers, each a little more than the one before, on a Tuesday.
	for i := range 6 {
		for k := range i + 1 {
			st.chat(s, guild, old(i+1), "something worth saying "+string(rune('a'+k)), t0.Add(time.Duration(k)*time.Minute))
		}
	}
	// The contest fund is in the treasury, but no prize comes out of it.
	s.fund = 900 * unit
	s.treasury = 3000*unit + s.fund
	before := map[int]int64{}
	for i := range 6 {
		m := s.members[old(i+1)]
		before[i] = m.purse
	}
	totals := s.members[old(6)].total

	// The next Monday turns the week.
	monday := t0.Add(6 * 24 * time.Hour)
	if weekOf(DayOf(monday)) == weekOf(DayOf(t0)) {
		t.Fatal("the test's monday is in the same week")
	}
	st.minute(s, guild, afkCh, monday)

	top := s.members[old(6)]
	if top.total != totals {
		t.Error("a prize counted toward the level")
	}
	// 3,000 pp can't pay the 4,000 the five places are owed, so each gets
	// three quarters of its prize.
	for i := 1; i < 6; i++ {
		if got, want := s.members[old(i+1)].purse-before[i], Default.Prizes[5-i]*3/4; got != want {
			t.Errorf("place %d got %d; want %d", 6-i, got, want)
		}
	}
	if s.treasury != s.fund || s.fund != 900*unit || s.members[old(1)].purse != before[0] {
		t.Errorf("a short treasury paid: treasury %d, sixth %d", s.treasury, s.members[old(1)].purse-before[0])
	}
	var prizes int
	for _, e := range s.pending {
		if e.reason == reasonPrize {
			prizes++
		}
	}
	if prizes != 10 {
		t.Errorf("%d prize rows; want two for each of five places", prizes)
	}
	// Each place wears a laurel, the sixth none.
	for i := range 6 {
		want := int64(1)
		if i == 0 {
			want = 0
		}
		if got := s.members[old(i+1)].laurels; got != want {
			t.Errorf("place %d has %d laurels; want %d", 6-i, got, want)
		}
	}

	last := DayOf(monday)
	m.now = func() time.Time { return monday.Add(time.Minute) }
	m.tick(t.Context(), &last)
	all := strings.Join(r.posts, " ")
	if !strings.Contains(all, "the week of") || !strings.Contains(all, "+1,125 pp") || !strings.Contains(all, "ran short") || !strings.Contains(all, "**chat**") {
		t.Errorf("posted %v", r.posts)
	}
	if len(s.checkpoints) != 0 {
		t.Error("a posted checkpoint stayed queued")
	}

	// An empty week pays nobody and posts nothing.
	st.minute(s, guild, afkCh, monday.Add(7*24*time.Hour))
	if len(s.checkpoints) != 0 {
		t.Errorf("an empty week queued %+v", s.checkpoints)
	}

	// A full treasury pays each place its own prize.
	s.treasury = 5000 * unit
	before[5] = top.purse
	st.checkpoint(s, top.week, DayOf(monday))
	if got := top.purse - before[5]; got != Default.Prizes[0] || s.treasury != 1000*unit {
		t.Errorf("a full treasury paid first %d and kept %d", got/unit, s.treasury/unit)
	}
}
