package pp

import (
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// thrown is a settled table of the throws given, one bird a seat, for
// reading luck from without a slate.
func thrown(s *server, bell bool, at time.Time, rail snowflake.ID, throws ...int) *table {
	t := &table{bell: bell, bellAt: at, rail: rail, settled: true}
	for i, n := range throws {
		u := old(i + 1)
		if s.members[u] == nil {
			s.members[u] = &member{}
		}
		t.seats = append(t.seats, seated{user: u, name: string(rune('a' + i)), stake: stakes[0].pp, throw: n, won: payout(n, stakes[0].pp, bell)})
	}
	return t
}

func got(es []earned) []int {
	var fs []int
	for _, e := range es {
		fs = append(fs, e.feat)
	}
	return fs
}

// Every feat nails up once, from the throws the lore names, and a bird's
// luck runs across tables and forgets with the day.
func TestDeckhead(t *testing.T) {
	st, s := houseState(t)
	day := DayOf(t0)
	read := func(tb *table, today int64) []int { return got(st.deckhead(s, tb, today)) }

	if fs := read(thrown(s, false, time.Time{}, 0, 50), day); len(fs) != 0 {
		t.Errorf("a plain throw nailed %v", fs)
	}
	if fs := read(thrown(s, true, t0, 0, 100), day); len(fs) != 1 || fs[0] != featBell {
		t.Errorf("a hundred in the bell nailed %v, want rang the bell", fs)
	}
	if fs := read(thrown(s, true, t0, 0, 100), day); len(fs) != 1 || fs[0] != featGannet {
		t.Errorf("a second hundred the same day nailed %v, want the gannet's luck alone", fs)
	}
	if fs := read(thrown(s, true, t0.Add(5*time.Hour), 0, 100), day); len(fs) != 1 || fs[0] != featBells {
		t.Errorf("a hundred in the second bell nailed %v, want both bells", fs)
	}
	if fs := read(thrown(s, false, time.Time{}, 0, 100), day+1); len(fs) != 0 {
		t.Errorf("a feat nailed twice: %v", fs)
	}

	// Back off the bottom and a run of nineties, for a second bird.
	b := old(2)
	s.members[b] = &member{}
	seat := func(n int) *table {
		return &table{settled: true, seats: []seated{{user: b, name: "b", throw: n}}}
	}
	read(seat(1), day)
	if fs := read(seat(100), day); len(fs) != 1 || fs[0] != featBottom {
		t.Errorf("a one then a hundred nailed %v", fs)
	}
	read(seat(91), day)
	read(seat(99), day)
	if fs := read(seat(95), day); len(fs) != 1 || fs[0] != featNineties {
		t.Errorf("three nineties nailed %v", fs)
	}

	// A dry bench and the dry one's nod, at fresh tables.
	st2, s2 := houseState(t)
	if fs := got(st2.deckhead(s2, thrown(s2, false, time.Time{}, 0, 77, 77, 77, 77, 77, 77), day)); len(fs) != tableSeats {
		t.Errorf("a dry bench nailed %d scraps, want one for each of six", len(fs))
	}
	if fs := got(st2.deckhead(s2, thrown(s2, false, time.Time{}, 0, 3, 3, 3, 3, 3, 3), day)); len(fs) != 0 {
		t.Errorf("a full table losing on the same throw nailed %v", fs)
	}
	st3, s3 := houseState(t)
	fs := got(st3.deckhead(s3, thrown(s3, false, time.Time{}, old(2), 100, 100), day))
	nods := 0
	for _, f := range fs {
		if f == featNod {
			nods++
		}
	}
	if nods != 2 {
		t.Errorf("two hundreds at the rail nailed %v", fs)
	}

	sc := st.scraps(s, day)
	if nailed(sc) != 5 || !sc[featBell].Up || sc[featBench].Up {
		t.Errorf("the deckhead shows %+v", sc)
	}
	if line := deckheadLine("", 0, sc); !strings.Contains(line, "5 of 7") || !strings.Contains(line, "wiped") {
		t.Errorf("deckhead line %q", line)
	}
	if !strings.Contains(nailedLine(earned{name: "steve", feat: featBell}), "rang the bell") {
		t.Error("a nailed scrap isn't named")
	}
}

// The beam keeps the day's highest throw and is wiped at the turn.
func TestBeam(t *testing.T) {
	_, s := houseState(t)
	day := DayOf(t0)
	s.chalk(thrown(s, false, time.Time{}, 0, 40, 88), day)
	s.chalk(thrown(s, false, time.Time{}, 0, 60), day)
	if name, n := s.onBeam(day); name != "b" || n != 88 {
		t.Errorf("the beam reads %s %d", name, n)
	}
	if name, _ := s.onBeam(day + 1); name != "" {
		t.Error("the beam wasn't wiped at the new tide")
	}
	s.chalk(thrown(s, false, time.Time{}, 0, 12), day+1)
	if name, n := s.onBeam(day + 1); name != "a" || n != 12 {
		t.Errorf("the new tide's beam reads %s %d", name, n)
	}
	if line := deckheadLine("a", 12, nil); !strings.Contains(line, "**a**, 12") {
		t.Errorf("beam line %q", line)
	}
}
