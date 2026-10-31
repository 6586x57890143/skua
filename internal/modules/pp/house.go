package pp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// The slack tide is the wet guild's house in the hull of the gannet, and
// the one place pp is wagered. Every bird at a table throws against the
// house's hold, never against each other, so nothing a table does can move
// pp from one bird to another. The house keeps an edge on every throw, and
// pays every win out of stakes it has won: the hold was seeded once, by
// the counting house's note, and nothing tops it up after. At each turn of
// the day the purser pumps whatever is over the waterline into the sea,
// which burns it.

// The house's ledger rows.
const (
	reasonNote   = "note"   // the hold's one seed, minted (user houseID)
	reasonStake  = "stake"  // a stake, from a purse into the hold; inputs the table
	reasonWin    = "win"    // a win, from the hold into a purse; inputs the table, the throw, the stake
	reasonRefund = "refund" // a stake back from a table that never settled; inputs the table
	reasonPump   = "pump"   // what was over the waterline at the turn, burned (user houseID)
)

// houseID is the hold's user in the ledger, as the treasury is user 0. No
// Discord account has ID 1.
const houseID snowflake.ID = 1

// The stakes, chalked over the bar.
var stakes = []struct {
	name string
	pp   int64 // in hundredths
}{{"wick", 5 * unit}, {"candle", 25 * unit}, {"lamp", 100 * unit}, {"lantern", 500 * unit}}

// tableSeats is how many birds a table seats, and tableGlass how long its
// sand glass runs from the first bird sitting.
const (
	tableSeats = 6
	tableGlass = 90 * time.Second
	// tableStale is how long an open table may go unsettled before the
	// minute's work settles it anyway.
	tableStale = 10 * time.Minute
)

// payout is what a throw of 1 to 100 pays on stake, the stake included:
// 1-50 nothing, 51-90 one and a half, 91-99 two and a half, a hundred ten
// times, or fifteen in the drowning hour. That is 0.925 of the stake on
// average, 0.975 in the hour.
func payout(throw int, stake int64, bell bool) int64 {
	switch {
	case throw == 100 && bell:
		return stake * 15
	case throw == 100:
		return stake * 10
	case throw >= 91:
		return stake * 5 / 2
	case throw >= 51:
		return stake * 3 / 2
	}
	return 0
}

// topPay is the most a throw can pay on stake, for the worst case.
func topPay(stake int64, bell bool) int64 { return payout(100, stake, bell) }

// throwOf is what user's bones say at table, from its seed:
// sha256(seed || table || user), its first 8 bytes mod 100, plus one.
// Anyone holding the revealed seed can throw it again.
func throwOf(seed []byte, table int64, user snowflake.ID) int {
	var n [16]byte
	binary.BigEndian.PutUint64(n[:8], uint64(table))
	binary.BigEndian.PutUint64(n[8:], uint64(user))
	sum := sha256.Sum256(append(append([]byte{}, seed...), n[:]...))
	return int(binary.BigEndian.Uint64(sum[:8])%100) + 1
}

// seated is one bird at a table.
type seated struct {
	user  snowflake.ID
	name  string
	stake int64
	throw int   // once settled
	won   int64 // once settled
}

// table is one of the purser's tables: its slate (the seed, posted as its
// hash before anyone sits and turned round once the bones are thrown),
// who sits at it and for what.
type table struct {
	id           int64 // unix millis it opened, unique in its server
	channel, msg snowflake.ID
	seed         []byte
	opened       time.Time
	first        time.Time // when the first bird sat, which turns the glass
	bell         bool      // opened in the drowning hour, so a hundred pays fifteen
	seats        []seated
	settled      bool
}

// slate is the hash of t's seed, posted before anyone sits.
func (t *table) slate() string {
	sum := sha256.Sum256(t.seed)
	return hex.EncodeToString(sum[:])
}

// closes is when t's glass runs out, the zero time before anyone sits.
func (t *table) closes() time.Time {
	if t.first.IsZero() {
		return time.Time{}
	}
	return t.first.Add(tableGlass)
}

// slateRand fills a slate; crypto/rand, so nobody can bias the bones, and
// a variable so the economy sim can replay a run.
var slateRand = func(b []byte) { rand.Read(b) }

// newTable is a table opened now, with a seed from crypto/rand.
func newTable(channel snowflake.ID, now time.Time, bell bool) *table {
	t := &table{id: now.UnixMilli(), channel: channel, seed: make([]byte, 32), opened: now, bell: bell}
	slateRand(t.seed)
	return t
}

var (
	errHouseShut  = core.Tell("the slack tide isn't open here yet: an admin picks its channel on /help")
	errTableOpen  = core.Tell("there's a table open already; sit at that one")
	errTableShut  = core.Tell("that table's thrown; open another from /pp")
	errSeated     = core.Tell("you're sat at a table already; wait for the bones")
	errTideOut    = core.Tell("the tide's out: the purser won't take that stake until the hold fills")
	errStakeCap   = core.Tell("that's past what the purser takes from one bird in a day; come back after the turn")
	errStakeShort = core.Tell("your purse is short of that stake")
	errWorstCase  = core.Tell("the purser's covering enough at this table; try a smaller stake or the next one")
	errHouseNew   = core.Tell("the purser doesn't know you yet: earn about the city first, with an account at least a week old")
)

// line is the waterline, and maxStake the most the purser takes on one
// throw: a hundredth of the hold.
func houseLine() int64            { return Rules.HouseLine * unit }
func (s *server) maxStake() int64 { return s.hold * Rules.HouseStake / 10000 }

// stakeCap is the most one bird stakes in a day, all tables and bells.
func (st *state) stakeCap() int64 { return Rules.HouseDay * st.pot() }

// note seeds the hold the first time it's needed, once. s.mu held.
func (s *server) note(today int64) {
	if s.noted {
		return
	}
	n := Rules.HouseNote * unit
	s.hold += n
	s.noted, s.dirty = true, true
	s.append(houseID, today, reasonNote, n, nil)
}

// open opens a table in ch, unless one is open already. s.mu held.
func (st *state) open(s *server, ch snowflake.ID, now time.Time, bell bool) (*table, error) {
	if s.houseCh == 0 || ch != s.houseCh {
		return nil, errHouseShut
	}
	if s.table != nil && !s.table.settled {
		return nil, errTableOpen
	}
	s.note(DayOf(now))
	s.table = newTable(ch, now, bell)
	return s.table, nil
}

// sitDown seats user at t for stake: the stake leaves their purse for the
// hold in the same row that seats them, and a bird holds one seat at a
// time anywhere, so a double click or a retried press can't seat them
// twice. s.mu held.
func (st *state) sitDown(s *server, t *table, guild, user snowflake.ID, name string, stake int64, now time.Time) error {
	today := DayOf(now)
	m := st.member(s, guild, user, today)
	worst := topPay(stake, t.bell)
	for _, p := range t.seats {
		worst += topPay(p.stake, t.bell)
	}
	switch {
	case t != s.table || t.settled || len(t.seats) >= tableSeats || (!t.first.IsZero() && !now.Before(t.closes())):
		return errTableShut
	case m.seat != 0:
		return errSeated
	case !st.bal().old(user, now) || m.total == 0:
		return errHouseNew
	case stake > s.maxStake():
		return errTideOut
	case m.staked+stake > st.stakeCap():
		return errStakeCap
	case m.purse < stake:
		return errStakeShort
	case worst > (s.hold+stake)/2:
		return errWorstCase
	}
	m.purse -= stake
	m.seat, m.staked = stake, m.staked+stake
	m.dirty, s.dirty = true, true
	s.hold += stake
	s.append(user, today, reasonStake, -stake, []int64{t.id})
	if t.first.IsZero() {
		t.first = now
	}
	t.seats = append(t.seats, seated{user: user, name: name, stake: stake})
	return nil
}

// due is whether t should be thrown now: its glass is out or its benches
// full.
func (t *table) due(now time.Time) bool {
	return !t.settled && len(t.seats) > 0 && (len(t.seats) >= tableSeats || !now.Before(t.closes()))
}

// throw settles t: every seat's bones from the slate, each win paid out of
// the hold. It runs once; a second call does nothing. s.mu held.
func (st *state) throw(s *server, t *table, now time.Time) bool {
	if t.settled {
		return false
	}
	t.settled = true
	today := DayOf(now)
	for i := range t.seats {
		p := &t.seats[i]
		p.throw = throwOf(t.seed, t.id, p.user)
		p.won = payout(p.throw, p.stake, t.bell)
		m := st.member(s, s.id, p.user, today)
		m.seat = 0
		m.purse += p.won
		m.dirty = true
		s.hold -= p.won
		if p.won > 0 {
			s.append(p.user, today, reasonWin, p.won, []int64{t.id, int64(p.throw), p.stake})
		}
		s.count(today, statThrow, int(p.stake/unit), p.throw)
		if p.throw == 100 && t.bell {
			s.count(today, statAllHands, 0, 0)
		}
	}
	s.dirty = true
	return true
}

// refundSeats gives back every stake still sat at a table when skua
// stopped: the table went with the process, so its bones were never
// thrown. s.mu held.
func (s *server) refundSeats(today int64) {
	for u, m := range s.members {
		if m.seat == 0 {
			continue
		}
		s.hold -= m.seat
		m.purse += m.seat
		m.staked -= m.seat
		s.append(u, today, reasonRefund, m.seat, nil)
		m.seat, m.dirty, s.dirty = 0, true, true
	}
}

// pump works the pumps at the turn: everything over the waterline goes
// over the side, burned. s.mu held.
func (s *server) pump(day int64) {
	if over := s.hold - houseLine(); over > 0 {
		s.hold -= over
		s.seasonBurn += over
		s.append(houseID, day, reasonPump, -over, nil)
		s.dirty = true
	}
}

// statThrow counts throws, a the stake in pp and b the throw; statAllHands
// counts the purser's call, and statBell the bells rung.
const (
	statThrow    = "house_throw"
	statAllHands = "house_all_hands"
	statBell     = "house_bell"
)

// The drowning hour. Its start is drawn from the season's seed and the
// day, so it can't be foreseen and is the same on every restart: the first
// bell starts in the europe window (17:00-21:00 UTC) or the americas one
// (00:00-03:00 UTC) 45 times in a hundred each, at any other hour the
// other 10, at a uniform minute. On about two days in seven a second bell
// rings in the prime window the first didn't use, at least four hours
// from it.
const (
	slotBell = 1 << 42
	bellHour = time.Hour
	bellGap  = 4 * time.Hour
)

// window is a stretch of UTC hours a bell may start in.
type window struct{ from, to int } // hours, to exclusive

var (
	europe   = window{17, 21}
	americas = window{0, 3}
)

// in is whether hour h is in w.
func (w window) in(h int) bool { return h >= w.from && h < w.to }

// at is the time in w the draw r picks, on the pp day day.
func (w window) at(day int64, r uint64) time.Time {
	minutes := uint64(w.to-w.from) * 60
	return onDay(day, w.from*60+int(r%minutes))
}

// onDay is the first time at minute of a UTC day at or after day's start.
func onDay(day int64, minute int) time.Time {
	start := dayStart(day)
	t := start.UTC().Truncate(24 * time.Hour).Add(time.Duration(minute) * time.Minute)
	if t.Before(start) {
		t = t.Add(24 * time.Hour)
	}
	return t
}

// Bells is when the drowning hour starts on day, from seed: one start,
// or two.
func Bells(seed uint64, day int64) []time.Time {
	r := func(i uint64) uint64 { return Roll(seed, 0, 0, day, slotBell+i) }
	var first time.Time
	var other window
	switch w := r(0) % 100; {
	case w < 45:
		first, other = europe.at(day, r(1)), americas
	case w < 90:
		first, other = americas.at(day, r(1)), europe
	default:
		// Any hour outside both prime windows.
		var hours []int
		for h := range 24 {
			if !europe.in(h) && !americas.in(h) {
				hours = append(hours, h)
			}
		}
		h := hours[r(1)%uint64(len(hours))]
		first = onDay(day, h*60+int(r(2)%60))
		other = europe
		if r(3)%2 == 0 {
			other = americas
		}
	}
	if r(4)%7 >= 2 {
		return []time.Time{first}
	}
	// The second, drawn again until it's far enough from the first; the
	// window's far edge if no draw is.
	for i := range uint64(8) {
		if second := other.at(day, r(5+i)); apart(first, second) {
			return order(first, second)
		}
	}
	for _, minute := range []int{other.from * 60, other.to*60 - 1} {
		if second := onDay(day, minute); apart(first, second) {
			return order(first, second)
		}
	}
	return []time.Time{first}
}

func apart(a, b time.Time) bool { d := a.Sub(b); return d >= bellGap || d <= -bellGap }

func order(a, b time.Time) []time.Time {
	if b.Before(a) {
		a, b = b, a
	}
	return []time.Time{a, b}
}

// ringing is the bell ringing at now, if one is: its start. A bell
// belongs to the pp day it starts in, so the day before's is looked at
// too, for one that runs over the turn.
func (st *state) ringing(now time.Time) (time.Time, bool) {
	day := DayOf(now)
	for _, d := range []int64{day - 1, day} {
		for _, at := range Bells(st.seedOf(d), d) {
			if !now.Before(at) && now.Before(at.Add(bellHour)) {
				return at, true
			}
		}
	}
	return time.Time{}, false
}
