package pp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// A member gets one dispatch a day, the same one whoever asks, and another
// the next day.
func TestDispatchOncePerDay(t *testing.T) {
	m := testModule(&fakeDB{})
	first := m.morning(3, 5)
	if first == "" {
		t.Fatal("no dispatch on the first /pp of the day")
	}
	if again := m.morning(3, 5); again != "" {
		t.Errorf("a second dispatch the same day: %q", again)
	}
	if other := testModule(&fakeDB{}).morning(3, 5); other != first {
		t.Errorf("the same member's day drew %q and %q", first, other)
	}
	m.now = func() time.Time { return t0.Add(24 * time.Hour) }
	if m.morning(3, 5) == "" {
		t.Error("no dispatch the next day")
	}
}

// The world's line follows the season's days and runs out past them.
func TestWorldLine(t *testing.T) {
	b := &Default
	start := b.SeasonStart
	for d, want := range worldLines[1] {
		if got := worldLine(b, start+int64(d)); got != want {
			t.Errorf("day %d reads %q", d, got)
		}
	}
	for _, day := range []int64{start - 1, start + int64(len(worldLines[1])), start + 7*b.SeasonWeeks} {
		if got := worldLine(b, day); got != "" {
			t.Errorf("day %d reads %q", day-start, got)
		}
	}
}

// Over many members on a season's first day all three kinds turn up, and
// everyone who gets the world's news gets the same line.
func TestDispatchKinds(t *testing.T) {
	m := testModule(&fakeDB{})
	day := Default.SeasonStart
	m.now = func() time.Time { return dayStart(day).Add(time.Hour) }
	s := m.st.server(3)
	m.st.member(s, 3, old(1), day) // rolls the day, so the fire stays lit
	s.lit = 1
	world, own, rumour := 0, 0, 0
	for u := uint64(1); u <= 400; u++ {
		switch l := m.morning(3, old(int(u))); {
		case l == worldLines[1][0]:
			world++
		case strings.Contains(l, "hearth"):
			own++
		case l != "":
			rumour++
		default:
			t.Fatalf("member %d got nothing", u)
		}
	}
	if world < 150 || own < 60 || rumour < 60 {
		t.Errorf("world %d, own %d, rumour %d of 400", world, own, rumour)
	}
}

// Their own news is what's true of them, and nothing when nothing is.
func TestOwnLine(t *testing.T) {
	m := testModule(&fakeDB{})
	today := DayOf(t0)
	s := m.st.server(3)
	mem := m.st.member(s, 3, 5, today)
	if got := m.ownLine(s, mem, 0, today); got != "" {
		t.Errorf("a new member's own news: %q", got)
	}
	mem.livery, mem.liveryUntil = "tanners", today
	if got := m.ownLine(s, mem, 0, today); !strings.Contains(got, "livery fades tonight") {
		t.Errorf("a lease running out reads %q", got)
	}
	mem.livery = ""
	_, _, need := m.st.bal().Level(0)
	mem.total = need - unit
	if got := m.ownLine(s, mem, 0, today); got != "a raven at the rim has you 1 pp short of level 1" {
		t.Errorf("close to a level reads %q", got)
	}
	mem.total = 0
	s.lit = 2
	if got := m.ownLine(s, mem, 0, today); !strings.Contains(got, "hearth") {
		t.Errorf("a lit hearth reads %q", got)
	}
}

// The dispatch sits under the card's heading, on the title screen too.
func TestDispatchOnTheCard(t *testing.T) {
	realScreens(t)
	m := testModule(&fakeDB{})
	e, sent := coretest.Event(t, "pp", nil)
	if err := m.Commands()[0].Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	head := (*sent)[0].Components[0].(discord.ContainerComponent).Components[0].(discord.TextDisplayComponent).Content
	if lines := strings.Split(head, "\n"); len(lines) != 2 || !strings.HasPrefix(lines[1], "-# ") {
		t.Errorf("the title's heading reads %q", head)
	}
	if got := withDispatch(discord.MessageCreate{}, "x"); len(got.Components) != 0 {
		t.Error("a dispatch made a card")
	}
}

// Every line is in the bird voice: lowercase, no semicolons.
func TestDispatchVoice(t *testing.T) {
	all := append(append([]string{}, rumours...), worldLines[1]...)
	for _, l := range all {
		if l != strings.ToLower(l) || strings.Contains(l, ";") || strings.HasSuffix(l, ".") {
			t.Errorf("off voice: %q", l)
		}
	}
}
