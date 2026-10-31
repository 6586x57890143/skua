package pp

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// The house's edge, pinned: a throw returns 0.925 of its stake on
// average, and 0.975 in the drowning hour. Every hour keeps an edge, so
// none can be farmed.
func TestHouseEdge(t *testing.T) {
	for _, c := range []struct {
		bell bool
		want int64
	}{{false, 92500}, {true, 97500}} {
		var sum int64
		for throw := 1; throw <= 100; throw++ {
			sum += payout(throw, 1000, c.bell)
		}
		if sum != c.want {
			t.Errorf("bell %v: 100 throws of 1000 pay %d, want %d", c.bell, sum, c.want)
		}
		if sum >= 100*1000 {
			t.Errorf("bell %v: the house has no edge", c.bell)
		}
	}
}

// Anyone with the revealed slate throws every bird's bones again:
// sha256(seed || table || user), the first 8 bytes mod 100, plus one.
func TestThrowFromSlate(t *testing.T) {
	seed := []byte("a slate turned round after the throw, thirty-two b")
	var n [16]byte
	binary.BigEndian.PutUint64(n[:8], 77)
	binary.BigEndian.PutUint64(n[8:], uint64(old(1)))
	sum := sha256.Sum256(append(append([]byte{}, seed...), n[:]...))
	want := int(binary.BigEndian.Uint64(sum[:8])%100) + 1
	if got := throwOf(seed, 77, old(1)); got != want {
		t.Fatalf("throw %d, the slate says %d", got, want)
	}
	seen := map[int]bool{}
	for u := range 2000 {
		th := throwOf(seed, 77, snowflake.ID(u))
		if th < 1 || th > 100 {
			t.Fatalf("a throw of %d", th)
		}
		seen[th] = true
	}
	if len(seen) != 100 {
		t.Errorf("2000 throws only showed %d numbers", len(seen))
	}
}

// houseState is a server with its house channel open and a bird with pp.
func houseState(t *testing.T) (*state, *server) {
	t.Helper()
	st, s := testState()
	s.houseCh = 9
	for i := 1; i <= 8; i++ {
		m := st.member(s, guild, old(i), DayOf(t0))
		m.purse, m.total = 2000*unit, 2000*unit
	}
	return st, s
}

func TestSitAndThrow(t *testing.T) {
	st, s := houseState(t)
	tb, err := st.open(s, 9, t0, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.hold != Rules.HouseNote*unit || !s.noted || s.pending[len(s.pending)-1].reason != reasonNote {
		t.Fatalf("the note didn't seed the hold: %d", s.hold)
	}
	if _, err := st.open(s, 9, t0, false); !errors.Is(err, errTableOpen) {
		t.Errorf("a second table opened: %v", err)
	}
	if _, err := st.open(s, 10, t0, false); !errors.Is(err, errHouseShut) {
		t.Errorf("a table opened outside the house: %v", err)
	}
	wick := stakes[0].pp
	if err := st.sitDown(s, tb, guild, old(1), "a", wick, t0); err != nil {
		t.Fatal(err)
	}
	if err := st.sitDown(s, tb, guild, old(1), "a", wick, t0); !errors.Is(err, errSeated) {
		t.Errorf("a bird sat twice: %v", err)
	}
	m := s.members[old(1)]
	if m.purse != 2000*unit-wick || m.seat != wick || m.staked != wick || s.hold != Rules.HouseNote*unit+wick {
		t.Errorf("sitting moved purse %d seat %d hold %d", m.purse, m.seat, s.hold)
	}
	if tb.due(t0) || !tb.due(t0.Add(tableGlass)) {
		t.Error("the glass doesn't run 90 seconds")
	}
	before := s.hold
	if !st.throw(s, tb, t0.Add(tableGlass)) || st.throw(s, tb, t0.Add(tableGlass)) {
		t.Fatal("a table threw other than once")
	}
	p := tb.seats[0]
	if p.throw != throwOf(tb.seed, tb.id, old(1)) || p.won != payout(p.throw, wick, false) {
		t.Errorf("the throw %+v isn't the slate's", p)
	}
	if m.seat != 0 || m.purse != 2000*unit-wick+p.won || s.hold != before-p.won {
		t.Errorf("the throw paid purse %d hold %d", m.purse, s.hold)
	}
	if err := st.sitDown(s, tb, guild, old(2), "b", wick, t0); !errors.Is(err, errTableShut) {
		t.Errorf("sat at a thrown table: %v", err)
	}

	// The glass out and the bones not yet thrown: nobody else sits.
	s.table = nil
	late, _ := st.open(s, 9, t0, false)
	if err := st.sitDown(s, late, guild, old(2), "b", wick, t0); err != nil {
		t.Fatal(err)
	}
	if err := st.sitDown(s, late, guild, old(3), "c", wick, t0.Add(tableGlass)); !errors.Is(err, errTableShut) {
		t.Errorf("sat after the glass ran out: %v", err)
	}
}

func TestSitRefusals(t *testing.T) {
	st, s := houseState(t)
	tb, _ := st.open(s, 9, t0, false)
	// The hold is 5,000 pp, so the purser takes 50 pp a throw at most.
	if err := st.sitDown(s, tb, guild, old(1), "a", stakes[2].pp, t0); !errors.Is(err, errTideOut) {
		t.Errorf("a lamp on a 5,000 hold: %v", err)
	}
	young := snowflake.New(t0.Add(-time.Hour))
	st.member(s, guild, young, DayOf(t0)).purse = 1000 * unit
	if err := st.sitDown(s, tb, guild, young, "y", stakes[0].pp, t0); !errors.Is(err, errHouseNew) {
		t.Errorf("a day-old account sat: %v", err)
	}
	s.members[old(2)].purse = unit
	if err := st.sitDown(s, tb, guild, old(2), "b", stakes[0].pp, t0); !errors.Is(err, errStakeShort) {
		t.Errorf("a short purse sat: %v", err)
	}
	s.members[old(3)].staked = st.stakeCap()
	if err := st.sitDown(s, tb, guild, old(3), "c", stakes[0].pp, t0); !errors.Is(err, errStakeCap) {
		t.Errorf("a bird past the day's cap sat: %v", err)
	}
	// Six candles at ten times is 1,500 pp against a 5,150 hold: fine.
	// In the bell, fifteen times: the table stops before half the hold.
	// With 2,500 in the hold a candle is the most it takes, and three
	// candles at fifteen times are as much as half of it covers.
	s.table, s.hold = nil, 2500*unit
	bell, _ := st.open(s, 9, t0, true)
	sat := 0
	for i := 4; i <= 8; i++ {
		if err := st.sitDown(s, bell, guild, old(i), "d", stakes[1].pp, t0); err == nil {
			sat++
		} else if !errors.Is(err, errWorstCase) {
			t.Fatal(err)
		}
	}
	var worst int64
	for _, p := range bell.seats {
		worst += topPay(p.stake, true)
	}
	if sat != 3 || worst > s.hold/2 {
		t.Errorf("the bell table covers %d of a %d hold", worst, s.hold)
	}
}

// The pumps burn what's over the line at the turn, and a stake sat at a
// table skua never threw comes back.
func TestPumpAndRefund(t *testing.T) {
	st, s := houseState(t)
	s.noted, s.hold = true, houseLine()+300*unit
	burn := s.seasonBurn
	s.pump(DayOf(t0))
	if s.hold != houseLine() || s.seasonBurn-burn != 300*unit || s.pending[len(s.pending)-1].delta != -300*unit {
		t.Errorf("the pumps left %d", s.hold)
	}
	s.pump(DayOf(t0))
	if s.pending[len(s.pending)-1].reason == reasonPump && s.pending[len(s.pending)-1].delta == 0 {
		t.Error("the pumps wrote an empty row")
	}
	tb, _ := st.open(s, 9, t0, false)
	if err := st.sitDown(s, tb, guild, old(1), "a", stakes[1].pp, t0); err != nil {
		t.Fatal(err)
	}
	s.refundSeats(DayOf(t0))
	m := s.members[old(1)]
	if m.seat != 0 || m.purse != 2000*unit || m.staked != 0 || s.hold != houseLine() {
		t.Errorf("the refund left purse %d hold %d", m.purse, s.hold)
	}
}

// The bell's schedule is the same for a seed and day every time, leans
// 45/45/10 across europe, the americas and the rest, and rings twice on
// about two days in seven, four hours apart.
func TestBells(t *testing.T) {
	var eu, am, off, twice, days, once int
	for seed := uint64(1); seed <= 20; seed++ {
		for day := int64(20759); day < 20759+140; day++ {
			bs := Bells(seed, day)
			if len(bs) != len(Bells(seed, day)) || !bs[0].Equal(Bells(seed, day)[0]) {
				t.Fatal("the bell moved between draws")
			}
			days++
			if len(bs) == 2 {
				twice++
				if !apart(bs[0], bs[1]) {
					t.Errorf("two bells %v apart", bs[1].Sub(bs[0]))
				}
			}
			for _, b := range bs {
				if b.Before(dayStart(day)) || !b.Before(dayStart(day+1)) {
					t.Errorf("a bell at %v outside its day", b)
				}
			}
			if len(bs) == 2 {
				continue // sorted, so the first drawn isn't always first
			}
			once++
			switch h := bs[0].UTC().Hour(); {
			case europe.in(h):
				eu++
			case americas.in(h):
				am++
			default:
				off++
			}
		}
	}
	pct := func(n int) int { return n * 100 / once }
	if pct(eu) < 40 || pct(eu) > 50 || pct(am) < 40 || pct(am) > 50 || pct(off) < 7 || pct(off) > 13 {
		t.Errorf("first bells %d/%d/%d%%", pct(eu), pct(am), pct(off))
	}
	if p := twice * 700 / days; p < 170 || p > 230 {
		t.Errorf("two bells on %d.%02d days in 7", p/100, p%100)
	}
}

func TestBellStages(t *testing.T) {
	at := t0
	for _, c := range []struct {
		d    time.Duration
		want int
	}{{-2 * time.Hour, bellNone}, {-time.Hour, bellDrawing}, {-15 * time.Minute, bellBoats}, {0, bellRinging}, {59 * time.Minute, bellRinging}, {time.Hour, bellStopped}, {2 * time.Hour, bellNone}} {
		if got := bellStage(at, at.Add(c.d)); got != c.want {
			t.Errorf("at %v the bell's at stage %d, want %d", c.d, got, c.want)
		}
	}
}

// The audit holds the hold to its rows.
func TestHouseAuditRows(t *testing.T) {
	st, s := houseState(t)
	tb, _ := st.open(s, 9, t0, false)
	for i := 1; i <= 3; i++ {
		if err := st.sitDown(s, tb, guild, old(i), "x", stakes[0].pp, t0); err != nil {
			t.Fatal(err)
		}
	}
	st.throw(s, tb, t0.Add(tableGlass))
	var own, moved int64
	for _, e := range s.pending {
		switch {
		case e.user == houseID:
			own += e.delta
		case houseMove(e.reason):
			moved += e.delta
		}
	}
	if own-moved != s.hold {
		t.Errorf("the hold's rows say %d, it holds %d", own-moved, s.hold)
	}
}

// Looking for the bell on a season's last day never reads, and so never
// fixes, the next season's seed before its beacon is drawn.
func TestBellLeavesNextSeedAlone(t *testing.T) {
	m := testModule(&fakeDB{})
	b := m.st.bal()
	next := int64(2)
	last := DayOf(b.seasonStart(next)) - 1
	ss := &seasonSeed{season: next, secret: make([]byte, 32), round: 1}
	m.st.seeds[next] = ss
	for minute := range 24 * 60 {
		m.nextBell(dayStart(last).Add(time.Duration(minute) * time.Minute))
	}
	if ss.settled {
		t.Fatal("the next season's seed was fixed before its beacon")
	}
}
