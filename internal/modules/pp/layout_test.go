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
			// A ground is under what's set on it, but never under another,
			// and a frame's trims may lie over its portrait's edge.
			if (m.What == "ground") != (o.What == "ground") || frameOver(m, o) || frameOver(o, m) {
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
				Place: 1234, Laurels: 12, Held: 3, Vouched: 999, Key: "freedom", Surety: true}
			checkLayout(t, cl.Name()+" "+cl.Titles[i], statusCanvas(s))
		}
	}
	checkLayout(t, "skua", statusCanvas(Sheet{Name: "steve", Class: ClassOf("wanderer"), Need: 1, Phase: "spent"}))
}

func TestQuestLayout(t *testing.T) {
	// The most a panel holds: a golden quest asking three things and two
	// great ones asking two.
	task := func(what string, n, of int64) TaskLine { return TaskLine{What: what, Progress: n, Goal: of} }
	checkLayout(t, "quests", questCanvas("a very long display name indeed, longer", []QuestLine{
		{Icon: "lantern", Tier: 2, Reward: Default.QuestPP[2], Tasks: []TaskLine{task("minutes in voice", 9, 18), task("messages", 15, 15), task("minutes by the hearth", 0, 10)}},
		{Icon: "compass", Tier: 1, Reward: Default.QuestPP[1], Tasks: []TaskLine{task("minutes in voice", 20, 20), task("messages", 1, 17)}},
		{Icon: "compass", Tier: 1, Reward: Default.QuestPP[1], Tasks: []TaskLine{task("minutes in voice", 999, 999), task("a selfie that stays a day", 0, 1)}},
	}, 2))
	checkLayout(t, "no quests", questCanvas("steve", nil, 0))
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
	today := m.st.bal().SeasonStart - 3
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

// frameOver is whether a is a frame's trim lying over b, a portrait, by no
// more than its rule and a few cells of the picture.
func frameOver(a, b pixel.Mark) bool {
	return strings.HasPrefix(a.What, "trim ") && b.What == "portrait" && !a.R.Overlaps(b.R.Inset(10))
}
