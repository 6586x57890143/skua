package pp

import (
	"slices"
	"strings"
	"testing"
	"unicode"
)

// The lean's preference is the ring: itself, the two it gets on with, then
// the two it can't stand, every wind once.
func TestWindPrefers(t *testing.T) {
	if got := Lot.prefers(); got != [5]Wind{Lot, Stray, Vaunt, Fold, Writ} {
		t.Errorf("lot prefers %v", got)
	}
	for w := Writ; w <= Fold; w++ {
		seen := map[Wind]bool{}
		for _, p := range w.prefers() {
			seen[p] = true
		}
		if len(seen) != 5 || w.prefers()[0] != w || w.String() == "" {
			t.Errorf("%v prefers %v", w, w.prefers())
		}
	}
}

// Every place is whole: a road out and back for each reach, a deck with
// enough near and far events that a day rarely repeats, every class's arc
// in three stages, company for fold birds and an ending for every wind,
// good and hard, with every rare ending reachable.
func TestForayPlacesWhole(t *testing.T) {
	classes := map[string]bool{}
	for _, c := range Classes {
		classes[c.Key] = true
	}
	for _, p := range places {
		if p.Key == "" || p.Name == "" || p.Find == "" || len(p.Home) == 0 {
			t.Errorf("%s: unnamed or homeless", p.Key)
		}
		for _, h := range p.Home {
			if !classes[h] {
				t.Errorf("%s: home to %q, no class", p.Key, h)
			}
		}
		for i := range 2 {
			if p.Out[i] == "" || p.In[i] == "" {
				t.Errorf("%s: no road for reach %d", p.Key, i)
			}
		}
		marks := map[string]bool{}
		keys := map[string]bool{}
		near, far := 0, 0
		check := func(e Event, arc string) {
			t.Helper()
			if keys[e.Key] {
				t.Errorf("%s: %s twice", p.Key, e.Key)
			}
			keys[e.Key] = true
			for c := range e.Class {
				if !classes[c] {
					t.Errorf("%s/%s: a line for %q, no class", p.Key, e.Key, c)
				}
			}
			for _, o := range e.Options {
				if o.Need != 0 && (o.Need < 30 || o.Need > 70 || o.Need%5 != 0) {
					t.Errorf("%s/%s: %q needs %d", p.Key, e.Key, o.Try, o.Need)
				}
				if (o.Need == 0) != (o.Fail.Text == "") || o.Pass.Text == "" {
					t.Errorf("%s/%s: %q has a fail exactly when it throws", p.Key, e.Key, o.Try)
				}
				for _, out := range []Outcome{o.Pass, o.Fail} {
					if out.Finds < 0 || out.Finds > 3 {
						t.Errorf("%s/%s: %q finds %d", p.Key, e.Key, o.Try, out.Finds)
					}
					if out.Mark != "" {
						marks[out.Mark] = true
					}
				}
				if arc != "" && (o.Class != arc || o.Need == 0) {
					t.Errorf("%s/%s: an arc's option is its class's, and thrown for", p.Key, e.Key)
				}
			}
			if arc != "" && len(e.Options) != 1 {
				t.Errorf("%s/%s: an arc stage has %d options", p.Key, e.Key, len(e.Options))
			}
		}
		for _, e := range p.Events {
			check(e, "")
			if e.Far {
				far++
			} else {
				near++
			}
			// Leans must part ways: at least two winds open to any bird.
			ws := map[Wind]bool{}
			for _, o := range e.Options {
				if o.Class == "" && o.After == "" {
					ws[o.Wind] = true
				}
			}
			if len(ws) < 2 || len(ws) != len(e.Options) {
				t.Errorf("%s/%s: %d distinct winds over %d options", p.Key, e.Key, len(ws), len(e.Options))
			}
		}
		if near < 7 || far < 3 {
			t.Errorf("%s: %d near and %d far events", p.Key, near, far)
		}
		for c := range classes {
			arc, ok := p.Arcs[c]
			if !ok {
				t.Errorf("%s: no arc for %s", p.Key, c)
			}
			for _, e := range arc {
				check(e, c)
			}
		}
		check(p.Company, "")
		if !strings.Contains(p.Company.Text, "{name}") || len(p.Company.Options) != 1 || p.Company.Options[0].Wind != Fold {
			t.Errorf("%s: company doesn't name the other bird, or isn't fold's", p.Key)
		}
		got := map[[2]int]bool{}
		for _, end := range p.Endings {
			switch {
			case end.Mark != "" && !marks[end.Mark]:
				t.Errorf("%s: %s needs a mark nothing leaves", p.Key, end.Key)
			case end.Mark == "":
				good := 0
				if end.Good {
					good = 1
				}
				if got[[2]int{int(end.Wind), good}] {
					t.Errorf("%s: two endings for %v good %v", p.Key, end.Wind, end.Good)
				}
				got[[2]int{int(end.Wind), good}] = true
			}
		}
		if len(got) != 10 {
			t.Errorf("%s: %d of the ten wind endings", p.Key, len(got))
		}
	}
}

// Every bird has a way through every event: whatever its lean, its class
// and its marks, it takes an option, and its class's own wherever there is
// one.
func TestEveryLeanFindsAWay(t *testing.T) {
	for _, p := range places {
		for _, e := range p.Events {
			for w := Writ; w <= Fold; w++ {
				for _, c := range Classes {
					o, ok := taken(e, w, c.Key, nil)
					if !ok {
						t.Fatalf("%s/%s: a %s %v takes nothing", p.Key, e.Key, c.Key, w)
					}
					if o.Class == "" && slices.ContainsFunc(e.Options, func(x Option) bool { return x.Class == c.Key }) {
						t.Errorf("%s/%s: a %s passed over its own option", p.Key, e.Key, c.Key)
					}
				}
			}
		}
		for c, arc := range p.Arcs {
			for _, e := range arc {
				if o, ok := taken(e, Writ, c, nil); !ok || o.Class != c {
					t.Errorf("%s/%s: a %s doesn't take its arc", p.Key, e.Key, c)
				}
			}
		}
	}
}

// Every word a bird reads out there is in the house voice: lowercase, no
// exclaiming and no semicolon chains. A passage ends a thought with a stop;
// what the screen shows as a label (an option, a title) doesn't.
func TestForayVoice(t *testing.T) {
	passage := func(where, s string) {
		t.Helper()
		bare := strings.ReplaceAll(s, "{name}", "")
		switch {
		case strings.IndexFunc(bare, unicode.IsUpper) >= 0:
			t.Errorf("%s: a capital in %q", where, s)
		case strings.Contains(s, "!"), strings.Contains(s, ";"):
			t.Errorf("%s: exclaims or chains in %q", where, s)
		case !strings.HasSuffix(s, "."):
			t.Errorf("%s: %q doesn't end its thought", where, s)
		}
	}
	label := func(where, s string) {
		t.Helper()
		if s == "" || s != strings.ToLower(s) || strings.ContainsAny(s, ".!;") {
			t.Errorf("%s: label %q", where, s)
		}
	}
	event := func(where string, e Event) {
		passage(where, e.Text)
		for _, l := range e.Class {
			passage(where, l)
		}
		for _, o := range e.Options {
			label(where, o.Try)
			passage(where, o.Pass.Text)
			if o.Fail.Text != "" {
				passage(where, o.Fail.Text)
			}
		}
	}
	for _, p := range places {
		for _, s := range append(p.Out[:], p.In[:]...) {
			passage(p.Key, s)
		}
		for _, e := range p.Events {
			event(p.Key+"/"+e.Key, e)
		}
		for _, arc := range p.Arcs {
			for _, e := range arc {
				event(p.Key+"/"+e.Key, e)
			}
		}
		event(p.Key+"/company", p.Company)
		for _, end := range p.Endings {
			label(p.Key+"/"+end.Key, end.Title)
			passage(p.Key+"/"+end.Key, end.Text)
		}
	}
	for _, w := range winds {
		label("winds", w.Key)
		label("winds", w.Line)
	}
	for place, ps := range pages {
		for k, s := range ps {
			passage(place+"/page/"+k, s)
		}
	}
	for place, es := range epilogues {
		for k, s := range es {
			passage(place+"/epilogue/"+k, s)
		}
	}
	if homeBonus <= 0 {
		t.Error("a home country counts for nothing")
	}
}

// Reading is half of going out: every event a bird can meet has its page in
// the out pages and every class arc its epilogue, each a real read, and
// nothing in the book is for an event or a class that isn't there.
func TestForayPages(t *testing.T) {
	for _, p := range places {
		ps, es := pages[p.Key], epilogues[p.Key]
		keys := map[string]bool{}
		for _, e := range p.Events {
			keys[e.Key] = true
			if words := len(strings.Fields(ps[e.Key])); words < 120 {
				t.Errorf("%s/%s: a page of %d words", p.Key, e.Key, words)
			}
		}
		for k := range ps {
			if !keys[k] {
				t.Errorf("%s: a page for %s, no such event", p.Key, k)
			}
		}
		for _, c := range Classes {
			if words := len(strings.Fields(es[c.Key])); words < 120 {
				t.Errorf("%s/%s: an epilogue of %d words", p.Key, c.Key, words)
			}
		}
		if len(es) != len(Classes) {
			t.Errorf("%s: %d epilogues for %d classes", p.Key, len(es), len(Classes))
		}
	}
	if len(pages) != len(places) || len(epilogues) != len(places) {
		t.Error("a book for a place that isn't there")
	}
}
