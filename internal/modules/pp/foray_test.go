package pp

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// Every walk ends, and the same bird on the same day walks it the same
// way: every place, reach, lean, class, arc stage and seed, with and
// without company, meets its events, its arc while it's untold and its
// company, throws only where an option asks it to and ends on an ending.
func TestWalkEveryWay(t *testing.T) {
	for p := range places {
		for _, far := range []bool{false, true} {
			reach := 0
			if far {
				reach = 1
			}
			for lean := Writ; lean <= Fold; lean++ {
				for _, c := range Classes {
					for stage := range 4 {
						for seed := range uint64(40) {
							w := Walker{Seed: seed, Guild: 7, User: seed * 31, Day: 20000 + int64(seed), Place: p, Far: far, Lean: lean, Class: c.Key, Stage: stage}
							if seed%2 == 1 {
								w.Company = "steve"
							}
							d := walk(w)
							want := forayEvents[reach]
							if stage < 3 {
								want++
							}
							if w.Company != "" {
								want++
							}
							if len(d.Steps) != want || d.Ending == nil || d.Finds < 0 {
								t.Fatalf("%+v: %d steps, ending %v", w, len(d.Steps), d.Ending)
							}
							for _, st := range d.Steps {
								if (st.Option.Need == 0) != (st.Roll == 0) || st.Roll > 100 || st.Out.Text == "" {
									t.Fatalf("%+v: step %s threw %d for %d", w, st.Event.Key, st.Roll, st.Option.Need)
								}
								if !far && st.Event.Far {
									t.Fatalf("%+v: met a far event near", w)
								}
							}
							if again := walk(w); !reflect.DeepEqual(again, d) {
								t.Fatalf("%+v: walked two ways", w)
							}
						}
					}
				}
			}
		}
	}
}

// The day's deck is the server's: every bird at a place meets the same
// events that day, and the next day deals them again.
func TestDeckIsShared(t *testing.T) {
	a := walk(Walker{Seed: 9, Guild: 7, User: 1, Day: 20001, Lean: Writ, Class: "knight", Stage: 3})
	b := walk(Walker{Seed: 9, Guild: 7, User: 2, Day: 20001, Lean: Lot, Class: "thief", Stage: 3})
	for i := range a.Steps {
		if a.Steps[i].Event != b.Steps[i].Event {
			t.Fatal("two birds out the same day met different events")
		}
	}
	differs := false
	for day := int64(20002); day < 20010; day++ {
		if !reflect.DeepEqual(deck(9, 7, day, 0), deck(9, 7, 20001, 0)) {
			differs = true
		}
	}
	if !differs {
		t.Error("every day deals the same deck")
	}
}

// The story comes a third at a time, the ending only once the bird is back.
func TestToldOf(t *testing.T) {
	for _, c := range []struct {
		quiet, steps int
		back         bool
	}{
		{0, 0, false}, {49, 0, false}, {50, 1, false}, {99, 1, false}, {100, 2, false}, {149, 2, false}, {150, 3, true}, {400, 3, true},
	} {
		if s, b := toldOf(3, c.quiet, 150); s != c.steps || b != c.back {
			t.Errorf("after %d quiet: %d steps, back %v", c.quiet, s, b)
		}
	}
}

// A bird goes out once a day, walks only the minutes the pot doesn't pay
// it for, and comes back with its finds, its pages, its ending and its
// class arc moved on if the arc's throw passed.
func TestQuietClock(t *testing.T) {
	st, s := testState()
	a := old(1)
	today := DayOf(t0)
	m := st.member(s, guild, a, today)
	m.class = "monk"
	if err := st.setOut(s, a, 0, false, Fold, today); err != nil {
		t.Fatal(err)
	}
	if err := st.setOut(s, a, 1, false, Fold, today); err != errForayOut {
		t.Errorf("set out twice at once: %v", err)
	}
	f := s.forays[a]
	now := t0
	st.forayTick(s, map[snowflake.ID]bool{a: true}, now)
	m.lastPay = now
	st.forayTick(s, nil, now.Add(30*time.Second))
	if f.out.quiet != 0 {
		t.Fatalf("walked %d minutes while earning", f.out.quiet)
	}
	for i := range forayQuiet[0] {
		st.forayTick(s, nil, now.Add(time.Duration(i+2)*time.Minute))
	}
	if !f.out.back || f.out.quiet != forayQuiet[0] {
		t.Fatalf("after the road: back %v, %d quiet", f.out.back, f.out.quiet)
	}
	d := walk(st.walker(s, a, f.out))
	if f.peat != int64(d.Finds) || !f.endings["mire/"+d.Ending.Key] || len(f.pages) == 0 || len(f.out.path) != forayEvents[0] {
		t.Errorf("came back with %d peat for %d found, endings %v, pages %v, path %v", f.peat, d.Finds, f.endings, f.pages, f.out.path)
	}
	if got, want := f.arcs["mire/monk"], map[bool]int{true: 1, false: 0}[d.ArcPassed]; got != want {
		t.Errorf("the arc is at %d; passed %v", got, d.ArcPassed)
	}
	if err := st.setOut(s, a, 0, true, Writ, today); err != errForayToday {
		t.Errorf("set out a second time in a day: %v", err)
	}
	if err := st.setOut(s, a, 0, true, Writ, today+1); err != nil {
		t.Errorf("the next day: %v", err)
	}
	if err := st.setOut(s, old(2), 5, false, Writ, today); err != errForayWhere {
		t.Errorf("an unknown road: %v", err)
	}
}

// Two fold birds out to the same place the same day meet: each is named in
// the other's story, and a third finds them already paired.
func TestFoldCompany(t *testing.T) {
	st, s := testState()
	today := DayOf(t0)
	a, b, c := old(1), old(2), old(3)
	st.member(s, guild, a, today).name = "heavywinds"
	st.member(s, guild, b, today).name = "steve"
	for _, u := range []snowflake.ID{a, b, c} {
		if err := st.setOut(s, u, 1, false, Fold, today); err != nil {
			t.Fatal(err)
		}
	}
	if s.forays[a].out.companion != b || s.forays[b].out.partner != "heavywinds" || s.forays[c].out.companion != 0 {
		t.Errorf("paired %v and %v; third %v", s.forays[a].out.companion, s.forays[b].out.partner, s.forays[c].out.companion)
	}
	d := walk(st.walker(s, a, s.forays[a].out))
	last := d.Steps[len(d.Steps)-1]
	if last.Event != &wood.Company || !strings.Contains(last.Event.Text, "{name}") {
		t.Error("the company isn't met")
	}
}

// Peat on the fire counts toward the server's day and can light it; with
// none to give there's nothing to put on.
func TestFeedPeat(t *testing.T) {
	st, s := testState()
	today := DayOf(t0)
	a := old(1)
	if err := st.feedPeat(s, a, 1, today); err != errNoPeat {
		t.Errorf("fed peat nobody had: %v", err)
	}
	s.forayOf(a).peat = 3
	st.rollServer(s, today)
	s.raw, s.history = 0, []int64{peatRaw * 2}
	if err := st.feedPeat(s, a, 3, today); err != nil {
		t.Fatal(err)
	}
	if s.peatRaw != 3*peatRaw || s.raw != 0 || s.forays[a].peat != 0 || s.lit == 0 {
		t.Errorf("peat on the fire %d, raw %d, peat left %d, lit %d", s.peatRaw, s.raw, s.forays[a].peat, s.lit)
	}
}

// Amber stakes a candle on the house's marker: nothing leaves the purse,
// the bird keeps what the bones win past the candle, and the purser takes
// one piece from a bird a day.
func TestAmberCandle(t *testing.T) {
	m, _ := houseModule(t)
	s := m.st.server(guild)
	a := old(5)
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := m.st.member(s, guild, a, DayOf(t0))
	mem.total, mem.purse = 1000*unit, 10*unit
	tb, _ := m.st.open(s, 4, t0, false)
	if err := m.st.sitOnAmber(s, tb, guild, a, "x", t0); err != errNoAmber {
		t.Errorf("sat on no amber: %v", err)
	}
	s.forayOf(a).amber = amberCandle - 1
	if err := m.st.sitOnAmber(s, tb, guild, a, "x", t0); err != errNoAmber {
		t.Errorf("sat on less than a candle's worth: %v", err)
	}
	s.forayOf(a).amber = amberCandle + 1
	if err := m.st.sitOnAmber(s, tb, guild, a, "x", t0); err != nil {
		t.Fatal(err)
	}
	if mem.purse != 10*unit || s.forays[a].amber != 1 || tb.seats[0].stake != stakes[1].pp || !tb.seats[0].marker {
		t.Errorf("purse %d, amber %d, seat %+v", mem.purse, s.forays[a].amber, tb.seats[0])
	}
	m.st.throw(s, tb, t0)
	if want := max(payout(tb.seats[0].throw, stakes[1].pp, false)-stakes[1].pp, 0); mem.purse != 10*unit+want {
		t.Errorf("threw %d: purse %d, want %d more", tb.seats[0].throw, mem.purse, want)
	}
	tb2, _ := m.st.open(s, 4, t0, false)
	if err := m.st.sitOnAmber(s, tb2, guild, a, "x", t0); err != errAmberDone {
		t.Errorf("a second amber the same day: %v", err)
	}
}

// What a member's row keeps of their arcs reads back as it was.
func TestArcsRoundTrip(t *testing.T) {
	arcs := map[string]int{"mire/knight": 2, "wood/knight": 1}
	if got := decodeArcs(encodeArcs(arcs)); !reflect.DeepEqual(got, arcs) {
		t.Errorf("%v came back %v", arcs, got)
	}
	if got := decodeArcs(""); len(got) != 0 {
		t.Errorf("nothing came back %v", got)
	}
}

// A save and a load give back a member's forays as they were, out on the
// road or home, so a deploy carries the bird on mid-walk.
func TestForaySaveLoad(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	st := newState(&Default, 42)
	s := st.server(g)
	a := old(1)
	st.member(s, g, a, DayOf(t0)).name = "heavywinds"
	if err := st.setOut(s, a, 1, true, Stray, DayOf(t0)); err != nil {
		t.Fatal(err)
	}
	f := s.forays[a]
	f.out.quiet, f.peat, f.amber, f.amberDay = 77, 3, 2, 9
	f.pages, f.endings, f.arcs = map[string]bool{"wood/count": true, "mire/raft": true}, map[string]bool{"wood/coin": true}, map[string]int{"wood/wanderer": 2}
	if _, err := st.flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	back := newState(&Default, 42)
	if err := back.loadForays(ctx, db); err != nil {
		t.Fatal(err)
	}
	got := back.server(g).forays[a]
	if got == nil || !reflect.DeepEqual(*got.out, *f.out) || got.peat != 3 || got.amber != 2 || got.amberDay != 9 ||
		!reflect.DeepEqual(got.pages, f.pages) || !reflect.DeepEqual(got.endings, f.endings) || !reflect.DeepEqual(got.arcs, f.arcs) || got.lastOut != f.lastOut {
		t.Errorf("came back %+v (out %+v); was %+v (out %+v)", got, got.out, f, f.out)
	}
}

// A bird's day is the events it was dealt when it set out, by key: a deck
// that grows or moves under it doesn't change it, and an event gone from
// the deck is passed by rather than told blank.
func TestPathByKey(t *testing.T) {
	w := Walker{Seed: 9, Guild: 7, User: 1, Day: 20001, Lean: Stray, Class: "thief", Stage: 3}
	dealt := walk(w)
	w.Path = dayPath(9, 7, 20001, 0, false)
	if !reflect.DeepEqual(walk(w), dealt) {
		t.Error("the dealt path walks differently from the deck it came from")
	}
	w.Path = append([]string{"nowhere"}, w.Path...)
	if d := walk(w); len(d.Steps) != len(dealt.Steps) || d.Steps[0].Event != dealt.Steps[0].Event {
		t.Errorf("an event gone from the deck was told: %d steps", len(d.Steps))
	}
}

// Meeting a fold bird only ever adds to a day already under way: a last
// step that throws nothing and leaves no mark, so the ending and the dice
// before it are as they were.
func TestCompanyOnlyAdds(t *testing.T) {
	for _, p := range places {
		for _, o := range p.Company.Options {
			if o.Need != 0 || o.Pass.Mark != "" || o.After != "" || o.Pass.Finds < 0 {
				t.Errorf("%s: company could change the day: %+v", p.Key, o)
			}
		}
	}
	w := Walker{Seed: 3, Guild: 7, User: 1, Day: 20005, Place: 1, Lean: Fold, Class: "bard", Stage: 1}
	alone := walk(w)
	w.Company = "steve"
	met := walk(w)
	if met.Ending != alone.Ending || !reflect.DeepEqual(met.Steps[:len(alone.Steps)], alone.Steps) || met.Finds < alone.Finds {
		t.Error("meeting company changed the day before it")
	}
}

// Peat lights today's fire and never the history: tomorrow's usual day is
// read from what was earned, so a day of peat doesn't raise the bar.
func TestPeatStaysOutOfHistory(t *testing.T) {
	st, s := testState()
	today := DayOf(t0)
	st.rollServer(s, today)
	s.raw = 1000 * unit
	s.forayOf(old(1)).peat = 40
	if err := st.feedPeat(s, old(1), 40, today); err != nil {
		t.Fatal(err)
	}
	st.rollServer(s, today+1)
	if got := s.history[len(s.history)-1]; got != 1000*unit || s.peatRaw != 0 {
		t.Errorf("history took %d, peat left %d", got, s.peatRaw)
	}
}

// A table shut on the way down gives amber back, and the day's chance to
// stake it.
func TestShutGivesAmberBack(t *testing.T) {
	m, _ := houseModule(t)
	s := m.st.server(guild)
	a := old(5)
	s.mu.Lock()
	mem := m.st.member(s, guild, a, DayOf(t0))
	mem.total = 1000 * unit
	tb, _ := m.st.open(s, 4, t0, false)
	s.forayOf(a).amber = amberCandle
	if err := m.st.sitOnAmber(s, tb, guild, a, "x", t0); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	m.Shut(context.Background())
	if f := s.forays[a]; f.amber != amberCandle || f.amberDay != 0 || mem.seat != 0 {
		t.Errorf("amber %d, day %d, seat %d after the shut", f.amber, f.amberDay, mem.seat)
	}
}
