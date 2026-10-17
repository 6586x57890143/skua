package pp

import (
	"strings"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/guard"
)

// When the week turns, each board's best are paid from the treasury, in
// order of place, into their purse but not their level; both sides go into
// the ledger, and the ledger channel hears about it.
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
	s.treasury = 3000 * unit
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
	if got := top.purse - before[5]; got != Default.Prizes[0] {
		t.Errorf("first place got %d", got)
	}
	if top.total != totals {
		t.Error("a prize counted toward the level")
	}
	// 3,000 pp pays 1,500 and 1,000, then 500 of third's 750.
	if s.treasury != 0 || s.members[old(4)].purse-before[3] != 500*unit || s.members[old(3)].purse != before[2] {
		t.Errorf("a short treasury paid: treasury %d, third %d, fourth %d", s.treasury, s.members[old(4)].purse-before[3], s.members[old(3)].purse-before[2])
	}
	var prizes int
	for _, e := range s.pending {
		if e.reason == reasonPrize {
			prizes++
		}
	}
	if prizes != 6 {
		t.Errorf("%d prize rows; want two for each of three places", prizes)
	}

	last := DayOf(monday)
	m.now = func() time.Time { return monday.Add(time.Minute) }
	m.tick(t.Context(), &last)
	all := strings.Join(r.posts, " ")
	if !strings.Contains(all, "the week of") || !strings.Contains(all, "+1,500 pp") || !strings.Contains(all, "ran short") || !strings.Contains(all, "**chat**") {
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
}
