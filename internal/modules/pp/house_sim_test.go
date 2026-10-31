package pp

import (
	"encoding/binary"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// The house in the economy: three years of gamblers at the slack tide,
// through the real seating, throwing and pumping, against A9 to A12.
//
//   - A9: the hold never goes below zero, and its own rows less every
//     stake, win and refund are what it holds, every day.
//   - A10: the house is a sink: over the run it takes more than it pays,
//     and in at least three seasons in four; the only pp it ever minted is
//     its one note; and no season's wins pass the stakes it took plus what
//     it held at the season's start. A season the bones go against the
//     house is luck, and the hold is there to ride it out.
//   - A11: no persona comes out of a season ahead by more than 2% of its
//     labour, on the median of seeded runs. The luckiest run is reported,
//     not held: luck is the point of a house.
//   - A12: seats and throws racing never debit or pay a bird twice.

// gambler is a persona: what it earns a day and how it plays.
type gambler struct {
	name   string
	birds  int     // an alt ring is five
	labour int64   // pp a day, in hundredths
	days   float64 // share of days it turns up
	stake  int     // the stake it reaches for, an index in stakes
	cap    bool    // plays to the day's cap
	bell   bool    // only in the drowning hour
	double bool    // martingale: doubles its stake after a loss
}

var gamblers = []gambler{
	{name: "casual", birds: 10, labour: 300 * unit, days: 2.0 / 7, stake: 1},
	{name: "regular", birds: 6, labour: 600 * unit, days: 0.8, stake: 2},
	{name: "whale", birds: 2, labour: 900 * unit, days: 1, stake: 3, cap: true},
	{name: "bell hunter", birds: 2, labour: 600 * unit, days: 1, stake: 3, cap: true, bell: true},
	{name: "alt ring", birds: 5, labour: 150 * unit, days: 1, stake: 3, cap: true, bell: true},
	{name: "martingale", birds: 2, labour: 600 * unit, days: 1, stake: 0, cap: true, double: true},
}

// houseRun is what one run measured.
type houseRun struct {
	negative, unbalanced bool  // A9
	seasons              int   // full seasons run
	sinkSeasons          int   // A10: seasons the house took more than it paid
	mint                 int64 // A10: pp minted for the house
	overpaid             bool  // A10
	pumped, held         int64 // what the pumps burned and the hold ended at
	staked, throwers     int64
	gain                 map[string][]float64 // A11: each persona's season gain over its labour
}

func runHouse(seed uint64, years int) houseRun {
	rng := rand.New(rand.NewPCG(seed, 7))
	slates := rand.New(rand.NewPCG(seed, 11))
	slateRand = func(b []byte) {
		for i := 0; i < len(b); i += 8 {
			binary.BigEndian.PutUint64(b[i:], slates.Uint64())
		}
	}
	defer func() { slateRand = defaultSlate }()

	st := newState(&Default, seed)
	s := st.server(guild)
	s.houseCh = 9
	type bird struct {
		id     snowflake.ID
		g      *gambler
		next   int64 // the martingale's next stake
		season int64 // house gain this season
		earned int64 // labour this season
	}
	var birds []*bird
	n := 0
	for i := range gamblers {
		for range gamblers[i].birds {
			n++
			birds = append(birds, &bird{id: old(n), g: &gamblers[i]})
		}
	}
	r := houseRun{gain: map[string][]float64{}}
	start := Default.SeasonStart
	seasonDays := 7 * Default.SeasonWeeks
	var seasonStake, seasonWin, holdAtStart, own, moved int64
	tableAt := dayStart(start)
	for day := start; day < start+int64(years)*365; day++ {
		now := dayStart(day).Add(time.Hour)
		s.mu.Lock()
		st.rollServer(s, day)
		s.note(day)
		if (day-start)%seasonDays == 0 {
			if day > start {
				r.seasons++
				if seasonStake > seasonWin {
					r.sinkSeasons++
				}
				if seasonWin > seasonStake+holdAtStart {
					r.overpaid = true
				}
				for _, b := range birds {
					if b.earned > 0 {
						r.gain[b.g.name] = append(r.gain[b.g.name], float64(b.season)/float64(b.earned))
					}
					b.season, b.earned = 0, 0
				}
			}
			seasonStake, seasonWin, holdAtStart = 0, 0, s.hold
		}
		var today []*bird
		for _, b := range birds {
			m := st.member(s, guild, b.id, day)
			if rng.Float64() >= b.g.days {
				continue
			}
			m.purse += b.g.labour
			m.total += b.g.labour
			b.earned += b.g.labour
			today = append(today, b)
		}
		// Tables until nobody who came wants another: up to 60 a day.
		for round := 0; round < 60 && len(today) > 0; round++ {
			bell := round%4 == 0 // the hour's tables among the day's
			tableAt = tableAt.Add(time.Second)
			tb, err := st.open(s, 9, tableAt, bell)
			if err != nil {
				panic(err)
			}
			var still []*bird
			for _, b := range today {
				if b.g.bell && !bell {
					still = append(still, b)
					continue
				}
				m := s.members[b.id]
				stake := stakes[b.g.stake].pp
				if b.g.double && b.next > 0 {
					stake = b.next
				}
				for stake > s.maxStake() && stake > stakes[0].pp {
					stake /= 2
				}
				if err := st.sitDown(s, tb, guild, b.id, b.g.name, stake, now); err != nil {
					if b.g.cap && m.staked < st.stakeCap() && err != errStakeCap && err != errStakeShort {
						still = append(still, b) // full table or worst case: the next one
					}
					continue
				}
				seasonStake += stake
				r.staked += stake
				if b.g.cap || rng.IntN(3) == 0 {
					still = append(still, b)
				}
			}
			st.throw(s, tb, now)
			for _, p := range tb.seats {
				for _, b := range birds {
					if b.id == p.user {
						b.season += p.won - p.stake
						if b.g.double {
							b.next = 0
							if p.won == 0 {
								b.next = min(p.stake*2, stakes[3].pp)
							}
						}
					}
				}
				seasonWin += p.won
			}
			if s.hold < 0 {
				r.negative = true
			}
			today = still
		}
		// A9: the hold's rows against the hold, the rows read once and let go.
		for _, e := range s.pending {
			switch {
			case e.user == houseID:
				own += e.delta
				if e.reason == reasonNote {
					r.mint += e.delta
				}
				if e.reason == reasonPump {
					r.pumped -= e.delta
				}
			case houseMove(e.reason):
				moved += e.delta
			}
		}
		if own-moved != s.hold {
			r.unbalanced = true
		}
		s.pending = s.pending[:0]
		s.mu.Unlock()
	}
	r.held = s.hold
	return r
}

var defaultSlate = slateRand

func median(xs []float64) float64 {
	xs = slices.Clone(xs)
	slices.Sort(xs)
	if len(xs) == 0 {
		return 0
	}
	return xs[len(xs)/2]
}

func TestHouseEconomy(t *testing.T) {
	if testing.Short() {
		t.Skip("three years of tables")
	}
	runs := 8
	gains := map[string][]float64{}
	best := map[string]float64{}
	for seed := uint64(1); seed <= uint64(runs); seed++ {
		r := runHouse(seed, 3)
		if r.negative || r.unbalanced {
			t.Errorf("run %d: A9 the hold went below zero (%v) or off its rows (%v)", seed, r.negative, r.unbalanced)
		}
		if r.mint != Rules.HouseNote*unit {
			t.Errorf("run %d: A10 the house minted %d, not one note", seed, r.mint)
		}
		if r.overpaid || r.sinkSeasons*4 < r.seasons*3 || r.pumped+r.held <= Rules.HouseNote*unit {
			t.Errorf("run %d: A10 the house paid more than it took in %d of %d seasons (overpaid %v)", seed, r.seasons-r.sinkSeasons, r.seasons, r.overpaid)
		}
		for name, g := range r.gain {
			gains[name] = append(gains[name], median(g))
			best[name] = max(best[name], slices.Max(g))
		}
		if seed == 1 {
			t.Logf("run 1: the house won %d of %d seasons", r.sinkSeasons, r.seasons)
			t.Logf("run 1: %d seasons, staked %s pp, pumped %s pp, the hold at %s pp", r.seasons, grouped(r.staked/unit), grouped(r.pumped/unit), grouped(r.held/unit))
		}
	}
	for _, g := range gamblers {
		m := median(gains[g.name])
		t.Logf("%-12s median season %+.1f%% of labour, luckiest %+.1f%%", g.name, m*100, best[g.name]*100)
		if m > 0.02 {
			t.Errorf("A11 %s comes out %.1f%% of its labour ahead on the median season", g.name, m*100)
		}
	}
}

// A12: birds hammering a table's stakes from many goroutines, with the
// glass and a stale settle racing them, each sit once and are paid once.
func TestHouseSeatRace(t *testing.T) {
	st, s := houseState(t)
	for round := range 50 {
		s.mu.Lock()
		tb, err := st.open(s, 9, t0.Add(time.Duration(round)*time.Second), round%5 == 0)
		before := map[snowflake.ID]int64{}
		for u, m := range s.members {
			before[u] = m.purse
		}
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 1; i <= 8; i++ {
			for range 4 {
				wg.Go(func() {
					s.mu.Lock()
					defer s.mu.Unlock()
					_ = st.sitDown(s, tb, guild, old(i), "x", stakes[0].pp, t0)
				})
			}
		}
		for range 3 {
			wg.Go(func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				st.throw(s, tb, t0)
			})
		}
		wg.Wait()
		s.mu.Lock()
		st.throw(s, tb, t0)
		seatedN := map[snowflake.ID]int{}
		for _, p := range tb.seats {
			seatedN[p.user]++
			if got, want := s.members[p.user].purse, before[p.user]-p.stake+p.won; got != want {
				t.Errorf("round %d: %d's purse %d, want %d", round, p.user, got, want)
			}
		}
		for u, n := range seatedN {
			if n > 1 {
				t.Errorf("round %d: %d sat %d times", round, u, n)
			}
		}
		for u, m := range s.members {
			if seatedN[u] == 0 && m.purse != before[u] {
				t.Errorf("round %d: %d never sat and their purse moved", round, u)
			}
			if m.seat != 0 {
				t.Errorf("round %d: %d still sat after the throw", round, u)
			}
			m.staked = 0
		}
		s.mu.Unlock()
	}
}
