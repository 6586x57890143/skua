package pp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// A bird calls another out from the slack tide's picker: the call-out
// pings only them, only the two sit, at one stake, and the bones say who
// takes the honour.
func TestCallOut(t *testing.T) {
	m, r := houseModule(t)
	s := m.st.server(guild)
	s.mu.Lock()
	called := m.st.member(s, guild, 6, DayOf(t0))
	called.purse, called.total = 1000*unit, 1000*unit
	stranger := m.st.member(s, guild, 7, DayOf(t0))
	stranger.purse, stranger.total = 1000*unit, 1000*unit
	s.mu.Unlock()

	e, sent := coretest.Button(t, "pp:tide", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js(*sent), "pp:rail") {
		t.Fatal("the slack tide has no rail picker")
	}

	pick := func(who string) error {
		e, _ := coretest.Button(t, "pp:rail", func(p map[string]any) {
			p["data"] = map[string]any{"custom_id": "pp:rail", "component_type": 5, "values": []any{who},
				"resolved": map[string]any{"users": map[string]any{who: map[string]any{"id": who, "username": "tester", "discriminator": "0"}}}}
		})
		return m.press(context.Background(), e)
	}
	if err := pick("5"); !errors.Is(err, errRailSelf) {
		t.Errorf("a bird called themselves out: %v", err)
	}
	if err := pick("99"); !errors.Is(err, errRailWho) {
		t.Errorf("a stranger was called out: %v", err)
	}
	if err := pick("6"); err != nil {
		t.Fatal(err)
	}
	tb := s.table
	if tb == nil || tb.rail != 6 || tb.caller != 5 || len(r.posts) != 1 {
		t.Fatalf("no call-out posted: %+v %v", tb, r.posts)
	}
	if got := r.posts[0]; !strings.Contains(got, "\\u003c@6\\u003e") || !strings.Contains(got, "a duel at the rail") {
		t.Errorf("the call-out doesn't call them: %s", got)
	}
	if err := pick("6"); !errors.Is(err, errTableOpen) {
		t.Errorf("a second call-out opened: %v", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	candle, wick := stakes[1].pp, stakes[0].pp
	if err := m.st.sitDown(s, tb, guild, 7, "c", candle, t0); !errors.Is(err, errNotCalled) {
		t.Errorf("a third bird sat at the rail: %v", err)
	}
	if err := m.st.sitDown(s, tb, guild, 5, "a", candle, t0); err != nil {
		t.Fatal(err)
	}
	if msg, _ := m.tableMessage(s, tb); !strings.Contains(js(msg), "pp:sit") {
		t.Error("the rail's message lost its stakes")
	}
	if err := m.st.sitDown(s, tb, guild, 6, "b", wick, t0); !errors.Is(err, errRailStake) {
		t.Errorf("they stood at the rail for different stakes: %v", err)
	}
	if err := m.st.sitDown(s, tb, guild, 6, "b", candle, t0); err != nil {
		t.Fatal(err)
	}
	if !tb.due(t0) || tb.benches() != 2 {
		t.Error("two at the rail don't throw at once")
	}
	if !m.st.throw(s, tb, t0) {
		t.Fatal("the rail never threw")
	}
	msg, err := m.tableMessage(s, tb)
	if err != nil {
		t.Fatal(err)
	}
	if got := js(msg); !strings.Contains(got, fmt.Sprintf("%x", tb.seed)) {
		t.Errorf("the thrown rail doesn't turn its slate: %s", got)
	}
	if a, b := s.members[5], s.members[6]; a.purse+b.purse != 2000*unit-2*candle+tb.seats[0].won+tb.seats[1].won {
		t.Error("pp passed between the two at the rail")
	}
}

// Nobody coming to the rail: the caller throws alone when the glass runs
// out.
func TestCallOutAlone(t *testing.T) {
	m, _ := houseModule(t)
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	other := m.st.member(s, guild, 6, DayOf(t0))
	other.total = unit
	tb, err := m.st.callOut(s, 4, t0, false, 5, 6, "steve")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.st.sitDown(s, tb, guild, 5, "a", stakes[0].pp, t0); err != nil {
		t.Fatal(err)
	}
	if tb.due(t0) || !tb.due(t0.Add(tableGlass)) {
		t.Error("a lone caller's glass doesn't run")
	}
	m.st.throw(s, tb, t0.Add(tableGlass))
	if msg, _ := m.tableMessage(s, tb); !strings.Contains(js(msg), "nobody came to the rail") {
		t.Error("a lone throw isn't told")
	}
}

// The rail moves in every light, and its runnel and lantern change from
// frame to frame.
func TestRailScreens(t *testing.T) {
	seats := []TableSeat{{Name: "steve", Class: Classes[1], Stake: stakes[1].pp, Throw: 70, Won: stakes[1].pp * 3 / 2}, {Name: "tester", Class: Classes[2], Stake: stakes[1].pp, Throw: 12}}
	for light := range railArts {
		frames, _ := railFrames(Rail{Light: light, Seats: seats[:1], Called: "tester"})
		if len(frames) != ambientFrames || bytes.Equal(frames[0].Pix, frames[1].Pix) {
			t.Errorf("the rail in light %d doesn't move", light)
		}
	}
	frames, _ := railFrames(Rail{Seats: seats, Thrown: true})
	if len(frames) != len(tumble)+ambientFrames {
		t.Errorf("a thrown rail has %d frames", len(frames))
	}
	realScreens(t)
	for _, r := range []Rail{{Seats: seats[:1]}, {Seats: seats, Thrown: true, Bell: true, Slate: "ab12cd34"}} {
		if pic, err := RailScreen(r); err != nil || !bytes.HasPrefix(pic, []byte("RIFF")) {
			t.Errorf("a rail screen isn't moving: %v", err)
		}
	}
}

// With a news channel of its own the slack tide tells its news there, each
// with a way to the table: the bell, all hands and a duel called out. The
// duel pings the bird called out once, there and not on the table, and a
// second call-out within the hour doesn't ping them again.
func TestTideNews(t *testing.T) {
	m, r := houseModule(t)
	s := m.st.server(guild)
	s.mu.Lock()
	s.newsCh = 7
	called := m.st.member(s, guild, 6, DayOf(t0))
	called.purse, called.total = 1000*unit, 1000*unit
	s.mu.Unlock()

	callOut := func() {
		t.Helper()
		e, _ := coretest.Button(t, "pp:rail", func(p map[string]any) {
			p["data"] = map[string]any{"custom_id": "pp:rail", "component_type": 5, "values": []any{"6"},
				"resolved": map[string]any{"users": map[string]any{"6": map[string]any{"id": "6", "username": "tester", "discriminator": "0"}}}}
		})
		if err := m.press(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	callOut()
	if len(r.posts) != 2 || r.to[0] != 4 || r.to[1] != 7 {
		t.Fatalf("the call-out went to %v: %v", r.to, r.posts)
	}
	if len(r.pings[0]) != 0 || len(r.pings[1]) != 1 || r.pings[1][0] != 6 {
		t.Errorf("the call-out pinged %v on the table and %v in the news", r.pings[0], r.pings[1])
	}
	news := r.posts[1]
	if !strings.Contains(news, "has called") || !strings.Contains(news, fmt.Sprintf("discord.com/channels/%s/4/", guild)) {
		t.Errorf("the duel's news: %s", news)
	}
	tb := s.table
	m.settleTable(context.Background(), guild, tb)
	if last := r.edits[len(r.edits)-1]; !strings.Contains(last, "nobody came to the rail") {
		t.Errorf("the duel's news after the throw: %s", last)
	}

	// Called out again within the hour: told, not pinged.
	callOut()
	if p := r.pings[len(r.pings)-1]; len(p) != 0 {
		t.Errorf("a second call-out pinged %v", p)
	}
	if p := r.pings[len(r.pings)-2]; len(p) != 0 {
		t.Errorf("the second table pinged %v", p)
	}

	// A hundred in the drowning hour calls all hands in the news.
	s.mu.Lock()
	s.table = nil
	tb, _ = m.st.open(s, 4, t0, true)
	tb.msg = 900
	for i := 0; ; i++ {
		tb.seed = []byte(fmt.Sprint("slate", i))
		if throwOf(tb.seed, tb.id, 5) == 100 {
			break
		}
	}
	if err := m.st.sitDown(s, tb, guild, 5, "steve", stakes[0].pp, t0); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	m.settleTable(context.Background(), guild, tb)
	if last := r.posts[len(r.posts)-1]; r.to[len(r.to)-1] != 7 || !strings.Contains(last, "all hands") || !strings.Contains(last, "/4/900") {
		t.Errorf("all hands went to %d: %s", r.to[len(r.to)-1], last)
	}

	// The bell rings in the news, with a way to the tables.
	at := Bells(m.st.seedOf(DayOf(t0)+1), DayOf(t0)+1)[0]
	m.now = func() time.Time { return at.Add(5 * time.Minute) }
	m.bells(context.Background())
	if last := r.posts[len(r.posts)-1]; r.to[len(r.to)-1] != 7 || !strings.Contains(last, "go to the tables") || !strings.Contains(last, "pp:table") {
		t.Errorf("the bell went to %d: %s", r.to[len(r.to)-1], last)
	}
}
