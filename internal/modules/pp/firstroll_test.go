package pp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// The turn into the first season takes the first roll: the top places on
// the level ladder as the preseason left it, ties sharing one, and only
// once. What's earned on the season's first day doesn't move it.
func TestFirstRoll(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	eve := Default.SeasonStart - 1
	s.mu.Lock()
	for i := 1; i <= 12; i++ {
		st.member(s, guild, old(i), eve).total = int64(100-i) * unit
	}
	st.member(s, guild, old(13), eve).total = int64(100-2) * unit // ties second
	st.member(s, guild, old(14), eve)                             // never earned
	s.mu.Unlock()

	s.mu.Lock()
	st.member(s, guild, old(12), Default.SeasonStart).total += 1000 * unit // the season's first earning
	s.mu.Unlock()
	want := map[int]int64{1: 1, 2: 2, 13: 2, 3: 4, 9: 10, 10: 0, 12: 0, 14: 0}
	for i, place := range want {
		if got := s.members[old(i)].firstRoll; got != place {
			t.Errorf("bird %d is %d on the first roll, want %d", i, got, place)
		}
	}

	// Later turns leave it be.
	s.mu.Lock()
	st.member(s, guild, old(12), Default.SeasonStart+7*Default.SeasonWeeks)
	s.mu.Unlock()
	if s.members[old(12)].firstRoll != 0 {
		t.Error("a later season took the first roll again")
	}
}

// The status screen carries the ribbon beside the portrait, its place in
// the alt text.
func TestFirstRollOnStatus(t *testing.T) {
	m := testModule(&fakeDB{})
	m.now = func() time.Time { return dayStart(Default.SeasonStart) }
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, 5, Default.SeasonStart)
	mem.classSeason, mem.class, mem.firstRoll = 1, "knight", 3
	s.mu.Unlock()
	e, sent := coretest.Event(t, "pp", nil)
	if err := m.Commands()[0].Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := js(*sent); !strings.Contains(got, "3rd on the first roll") {
		t.Errorf("the status doesn't tell the first roll: %s", got)
	}
}

// The ribbon is worn through the first season's first week only: the place
// is kept, the screen goes back to how it was.
func TestFirstRollWornOneWeek(t *testing.T) {
	sh := Sheet{Name: "steve", Class: ClassOf("knight"), Need: Default.Need(0), FirstRoll: 2}
	for _, c := range []struct {
		season, week int64
		worn         bool
	}{{1, 1, true}, {1, 2, false}, {2, 1, false}} {
		sh.Season, sh.Week = c.season, c.week
		with, without := statusCanvas(sh), statusCanvas(Sheet{Name: sh.Name, Class: sh.Class, Need: sh.Need, Season: c.season, Week: c.week})
		if differs := !bytes.Equal(with.Pix, without.Pix); differs != c.worn {
			t.Errorf("season %d week %d: the ribbon is worn %v, want %v", c.season, c.week, differs, c.worn)
		}
	}
}
