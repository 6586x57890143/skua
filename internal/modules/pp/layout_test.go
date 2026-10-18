package pp

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"github.com/6586x57890143/skua/internal/pixel"
)

// layout is what's wrong with how c is laid out: anything set off a
// window's parchment or touching its rule, and anything overlapping
// anything else but the ground it's set on. A tab is the one mark that
// meets the rule, by design.
func layout(c *pixel.Canvas) []string {
	var bad []string
	for i, m := range c.Marks {
		pad := 1
		if strings.HasPrefix(m.What, "tab ") {
			pad = 0
		}
		on := false
		for _, p := range c.Panes {
			on = on || m.R.In(p.Inset(pad))
		}
		if !on {
			bad = append(bad, fmt.Sprintf("%q at %v isn't on parchment", m.What, m.R))
		}
		for _, o := range c.Marks[i+1:] {
			// A ground is under what's set on it, but never under another.
			if (m.What == "ground") != (o.What == "ground") {
				continue
			}
			if m.R.Overlaps(o.R) {
				bad = append(bad, fmt.Sprintf("%q at %v overlaps %q at %v", m.What, m.R, o.What, o.R))
			}
		}
	}
	return bad
}

func checkLayout(t *testing.T, name string, c *pixel.Canvas) {
	t.Helper()
	for _, b := range layout(c) {
		t.Errorf("%s: %s", name, b)
	}
}

// Every class at every title, with the longest name the screen takes, the
// most buffs and the widest numbers, lays out clean.
func TestStatusLayout(t *testing.T) {
	buffs := []Buff{{"crest", "crest"}, {"banner", "banner"}, {"plume", "plume"}, {"hearth", "hearth iii"}}
	for _, cl := range Classes {
		for i, from := range titleFrom {
			s := Sheet{Portrait: image.NewNRGBA(image.Rect(0, 0, 32, 48)), Name: "a very long display name for a sheet", Class: cl, Level: from,
				Into: 99999 * unit, Need: 999999 * unit, Purse: 9999999 * unit, Vigor: 50, Phase: "fresh", Buffs: buffs[:i%5], Season: 12, Week: 4,
				Place: 1234, Laurels: 12, Held: 3}
			checkLayout(t, cl.Name()+" "+cl.Titles[i], statusCanvas(s))
		}
	}
	checkLayout(t, "skua", statusCanvas(Sheet{Name: "steve", Class: ClassOf("wanderer"), Need: 1, Phase: "spent"}))
}

func TestQuestLayout(t *testing.T) {
	checkLayout(t, "quests", questCanvas("a very long display name indeed, longer", []QuestLine{
		{Icon: "quill", What: "send 400 messages", Progress: 23, Goal: 400, Reward: Default.QuestPP[0]},
		{Icon: "compass", What: "3 tasks", Progress: 2000, Goal: 4000, Tier: 1, Reward: Default.QuestPP[1]},
		{Icon: "mirror", What: "post a selfie", Progress: 0, Goal: 1, Tier: 2, Reward: Default.QuestPP[2]},
	}, true))
	checkLayout(t, "no quests", questCanvas("steve", nil, false))
}

// A class and its title give up words only when they run out of room, the
// same word in both.
func TestFitPair(t *testing.T) {
	width := 212 - pixel.IconCells - 4 // the status screen's class line
	for _, c := range []struct{ l, r, wl, wr string }{
		{"raven sorcerer", "hedge sorcerer", "raven sorc", "hedge sorc"},
		{"raven sorcerer", "magus", "raven sorcerer", "magus"},
		{"crane knight", "lord commander", "crane knight", "lord commander"},
		{"crane knight", "banneret", "crane knight", "banneret"},
		{"albatross wanderer", "storm wanderer", "albatross wndr", "storm wndr"},
	} {
		if l, r := fitPair(c.l, pixel.Regular, c.r, pixel.Serif, width); l != c.wl || r != c.wr {
			t.Errorf("%s over %s fits as %s over %s", c.l, c.r, l, r)
		}
	}
}

// Before the first season the status screen's board place is the levels
// board's: by everything earned, unplaced only with nothing earned.
func TestPreseasonPlace(t *testing.T) {
	m := testModule(&fakeDB{})
	today := m.st.b.SeasonStart - 3
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, total := range []int64{100, 500, 0} {
		m.st.member(s, guild, old(i+1), today).total = total * unit
	}
	for i, want := range []int{2, 1, 0} {
		if got := m.sheet(s, s.members[old(i+1)], guild, old(i+1), today).Place; got != want {
			t.Errorf("member %d is placed %d; want %d", i+1, got, want)
		}
	}
}
