package pp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// The gate is shut to members until an admin opens it, and then it's
// everyone's: a member is written out, walks the quiet, comes back, reads
// the day a screen at a time, puts the peat on the fire and reads the out
// pages.
func TestGateFlow(t *testing.T) {
	m := launched(testModule(&fakeDB{}))
	s := m.st.server(guild)
	user := snowflake.ID(5)
	mem := m.st.member(s, guild, user, DayOf(t0))
	mem.classSeason, mem.class = 1, "monk"
	press := func(id string, mutate func(map[string]any)) string {
		t.Helper()
		e, sent := coretest.Button(t, id, mutate)
		if err := m.press(context.Background(), e); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return js((*sent)[0].Components)
	}

	m.Admin = func(discord.Interaction) bool { return false }
	e, _ := coretest.Button(t, "pp:gate", nil)
	if err := m.press(context.Background(), e); err != errGateShut {
		t.Fatalf("a member at a shut gate: %v", err)
	}
	if strings.Contains(js(menuRows("monk", false, "pp:status", false, false, false, false, false)), "pp:gate") {
		t.Error("the menu shows a shut gate")
	}
	s.gateOpen = true

	card := press("pp:gate", nil)
	if !strings.Contains(card, "pp:fwhere") {
		t.Fatalf("the gate: %s", card)
	}
	card = press("pp:fwhere", func(p map[string]any) {
		p["data"] = map[string]any{"custom_id": "pp:fwhere", "component_type": 3, "values": []any{"0:0"}}
	})
	if !strings.Contains(card, "pp:fout:0:0:4") || !strings.Contains(card, "the mire") {
		t.Fatalf("choosing the mire: %s", card)
	}
	press("pp:fout:0:0:4", nil)
	f := s.forays[user]
	if f == nil || f.out == nil || f.out.lean != Fold || f.out.far {
		t.Fatalf("not out: %+v", f)
	}
	if card = press("pp:gate", nil); strings.Contains(card, "pp:fwhere") || !strings.Contains(card, "the day so far") {
		t.Errorf("out on the road: %s", card)
	}
	for i := range forayQuiet[0] {
		s.mu.Lock()
		m.st.forayTick(s, nil, t0.Add(time.Duration(i+2)*time.Minute))
		s.mu.Unlock()
	}
	card = press("pp:fread:0", nil)
	if !strings.Contains(card, "pp:fread:1") || !strings.Contains(card, "pp:gate:back") {
		t.Errorf("the day's first screen: %s", card)
	}
	card = press("pp:fread:99", nil)
	if strings.Contains(card, "pp:fread:99") {
		t.Errorf("a page past the last: %s", card)
	}
	if f.peat > 0 {
		before := f.peat
		press("pp:peat", nil)
		if f.peat != 0 || s.peatRaw != before*peatRaw {
			t.Errorf("peat left %d, on the fire %d", f.peat, s.peatRaw)
		}
	}
	if card = press("pp:book:0", nil); !strings.Contains(card, "the out pages") && !strings.Contains(card, "pp:book") {
		t.Errorf("the out pages: %s", card)
	}
	if card = press("pp:gate:back", nil); !strings.Contains(card, "the out pages") {
		t.Errorf("back at the gate: %s", card)
	}
}

// Every pane lays out clean with the longest text it can be given: a whole
// day told, every page of the book and the gate's words.
func TestForayPanes(t *testing.T) {
	st, s := testState()
	today := DayOf(t0)
	a := old(1)
	st.member(s, guild, a, today).class = "knight"
	for p := range places {
		for _, far := range []bool{false, true} {
			o := &outing{place: p, far: far, lean: Fold, class: "knight", day: today, need: 1, quiet: 1, back: true, partner: "a very long display name indeed"}
			for _, page := range setLines(st.storyOf(s, a, o), places[p].Key) {
				checkLayout(t, places[p].Key, forayCanvas(ForayView{Scene: places[p].Key, Tab: places[p].Name, Lines: page, Foot: reachName(far)}))
			}
		}
	}
	f := s.forayOf(a)
	for _, p := range places {
		for _, e := range p.Events {
			f.pages[p.Key+"/"+e.Key] = true
		}
	}
	for _, l := range bookOf(f) {
		checkLayout(t, "book", forayCanvas(ForayView{Scene: l.scene, Tab: "the out pages", Lines: l.lines, Foot: l.tab}))
	}
	f.peat, f.amber = 999, 999
	words := setLines(st.gateWords(s, a, today), "gate")
	if len(words) != 1 {
		t.Errorf("the gate's words run to %d screens", len(words))
	}
	checkLayout(t, "gate", forayCanvas(ForayView{Scene: "gate", Tab: "the north gate", Lines: words[0]}))
}

// Every painting is there in every light, the size of the screen.
func TestForayArt(t *testing.T) {
	for name, arts := range forayArts {
		for light, art := range arts {
			if b := art().img.Bounds(); b.Dx() != screenW || b.Dy() != screenH {
				t.Errorf("%s in light %d is %v", name, light, b)
			}
		}
	}
}
