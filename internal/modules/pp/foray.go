package pp

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"

	"github.com/6586x57890143/skua/internal/core"
)

// The forays engine (LORE.md, beyond the north gate). A bird sets out once a
// pp day to a place, near or far, leaning into a wind, and the day out is
// decided there and then: which events are out there today is drawn once
// for the server from the season's seed, the same for every bird, and each
// bird's bones are drawn from it too, by bird, so nothing is rerolled by
// asking again and anyone holding the revealed seed can walk it again. What
// isn't decided is how much of it has been told: the road takes only quiet
// minutes, the ones the pot isn't paying the bird for, and the story comes
// a third at a time.
//
// walk is pure: a place, the deck, the bird and its bones in, the day out.
// The rest is the state round it: setting out, the quiet clock on the
// minute's tick, coming back with the finds, and spending them.

// The draws: the day's deck for a place, and a bird's bones on it.
const (
	slotForay     = 1 << 45
	slotForayDice = 1 << 46
)

// The road's length in quiet minutes, near and far, and how many events
// are met on it, before a class arc or company.
var (
	forayQuiet  = [2]int{150, 300}
	forayEvents = [2]int{2, 4}
)

// peatRaw is what one peat counts toward the server's day on the hearth,
// in hundredths of a pp.
// ponytail: a constant, not Balance, until a week of peat fed says what
// it should be; moving it into Balance is a changelog entry.
const peatRaw = 25 * unit

// amberCandle is how many beads of amber the purser takes for a candle: a
// far day in the wood brings back about one candle's worth.
const amberCandle = 3

// What a foray counts, for tuning (see stats.go). foray_out: a the place,
// b the lean. foray_back: a the place, b the ending's index. peat_fed: a
// how many. amber_staked: once each. foray_company: a the place.
const (
	statForayOut     = "foray_out"
	statForayBack    = "foray_back"
	statPeatFed      = "peat_fed"
	statAmberStaked  = "amber_staked"
	statForayCompany = "foray_company"
)

var (
	errForayOut   = core.Tell("your bird is still out; it comes back when the road is done")
	errForayToday = core.Tell("the warden has written you out once today already; come back after the turn")
	errForayWhere = core.Tell("the warden doesn't know that road")
	errNoPeat     = core.Tell("you have no peat to put on the fire")
	errNoAmber    = core.Tell("the purser takes three beads of amber for a candle, and you have fewer")
	errAmberDone  = core.Tell("the purser takes one piece of amber from a bird a day; come back after the turn")
)

// deck is place p's events in the order the day draws them on day in
// guild: the same for every bird out there that day.
func deck(seed, guild uint64, day int64, p int) []int {
	n := len(places[p].Events)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	r := Roll(seed, guild, 0, day, slotForay+uint64(p))
	for i := n - 1; i > 0; i-- {
		r = mix(r)
		j := int(r % uint64(i+1))
		order[i], order[j] = order[j], order[i]
	}
	return order
}

// Step is one event met on a day out and how it went.
type Step struct {
	Event  *Event
	Option Option
	// Roll is the bones, one to a hundred, and Bonus what the bird's class
	// adds at home; 0 and 0 when the option isn't thrown for.
	Roll, Bonus int
	Passed      bool
	Out         Outcome
	// Arc is whether this is the bird's class arc, and Index the event's
	// place in the deck, -1 for an arc or company.
	Arc   bool
	Index int
}

// Outing is a whole day out, as walk decides it.
type Outing struct {
	Place  *Place
	Far    bool
	Steps  []Step
	Ending *Ending
	// End is the ending's index in Place.Endings.
	End   int
	Finds int
	// ArcPassed is whether the class arc's stage was met and passed, which
	// moves the arc on.
	ArcPassed bool
}

// Walker is everything walk needs about the bird and its day.
type Walker struct {
	Seed, Guild, User uint64
	Day               int64
	Place             int
	Far               bool
	Lean              Wind
	Class             string
	// Stage is how many of the class arc's stages the bird has told here
	// before today, 3 once it's all told.
	Stage int
	// Company is the name of a fold bird met out there today, "" for none.
	Company string
}

// walk is the day out w has. It is pure.
func walk(w Walker) Outing {
	p := places[w.Place]
	far := 0
	if w.Far {
		far = 1
	}
	var picks []int
	for _, i := range deck(w.Seed, w.Guild, w.Day, w.Place) {
		if len(picks) == forayEvents[far] {
			break
		}
		if !p.Events[i].Far || w.Far {
			picks = append(picks, i)
		}
	}
	type met struct {
		e     *Event
		arc   bool
		index int
	}
	var path []met
	for k, i := range picks {
		path = append(path, met{&p.Events[i], false, i})
		if arc, ok := p.Arcs[w.Class]; ok && k == 0 && w.Stage < len(arc) {
			path = append(path, met{&arc[w.Stage], true, -1})
		}
	}
	if w.Company != "" {
		path = append(path, met{&p.Company, false, -1})
	}

	d := Outing{Place: p, Far: w.Far}
	marks := map[string]bool{}
	throws, passes := 0, 0
	home := slices.Contains(p.Home, w.Class)
	for n, m := range path {
		o, _ := taken(*m.e, w.Lean, w.Class, marks)
		st := Step{Event: m.e, Option: o, Arc: m.arc, Index: m.index, Passed: true}
		if o.Need > 0 {
			st.Roll = int(Roll(w.Seed, w.Guild, w.User, w.Day, slotForayDice+uint64(n))%100) + 1
			// A bird reads its own country, and its own story anywhere.
			if home || m.arc {
				st.Bonus = homeBonus
			}
			st.Passed = st.Roll+st.Bonus >= o.Need
			throws++
			if st.Passed {
				passes++
			}
		}
		st.Out = o.Pass
		if !st.Passed {
			st.Out = o.Fail
		}
		if st.Out.Mark != "" {
			marks[st.Out.Mark] = true
		}
		if m.arc && st.Passed {
			d.ArcPassed = true
		}
		d.Finds += st.Out.Finds
		d.Steps = append(d.Steps, st)
	}
	good := passes*2 >= throws
	d.End = -1
	for i, e := range p.Endings {
		if e.Mark != "" && marks[e.Mark] {
			d.End = i
			break
		}
	}
	if d.End < 0 {
		for i, e := range p.Endings {
			if e.Mark == "" && e.Wind == w.Lean && e.Good == good {
				d.End = i
				break
			}
		}
	}
	d.Ending = &p.Endings[d.End]
	d.Finds += d.Ending.Finds
	return d
}

// taken is the option a bird of class leaning lean takes at e, carrying
// marks: its class's own if e has one, else the first open option its lean
// prefers. Every event has one for every bird (foray_text_test).
func taken(e Event, lean Wind, class string, marks map[string]bool) (Option, bool) {
	open := func(o Option) bool { return o.After == "" || marks[o.After] }
	for _, o := range e.Options {
		if o.Class == class && open(o) {
			return o, true
		}
	}
	for _, w := range lean.prefers() {
		for _, o := range e.Options {
			if o.Class == "" && o.Wind == w && open(o) {
				return o, true
			}
		}
	}
	return Option{}, false
}

// toldOf is how much of n steps has been told after quiet of need minutes:
// a third, two thirds, then all of it with the ending once the bird is back.
func toldOf(n, quiet, need int) (steps int, back bool) {
	switch {
	case quiet >= need:
		return n, true
	case quiet*3 >= need*2:
		return (n*2 + 2) / 3, false
	case quiet*3 >= need:
		return (n + 2) / 3, false
	}
	return 0, false
}

// outing is a day out: set out on day, back once quiet reaches need.
type outing struct {
	place     int
	far       bool
	lean      Wind
	class     string
	stage     int
	day       int64
	quiet     int
	need      int
	companion snowflake.ID
	partner   string // the companion's name, as the story tells it
	back      bool
}

// forays are what a member has from going out: today's or the last day
// out, the pages and endings found at each place, how far each class arc
// has been told there, and the finds not yet spent.
type forays struct {
	out      *outing
	lastOut  int64 // the day they last set out
	pages    [2]int64
	endings  [2]int64
	arcs     map[string]int // place/class: stages told
	peat     int64
	amber    int64
	amberDay int64
	dirty    bool
}

// forayOf is user's forays in s, made if they have none. s.mu held.
func (s *server) forayOf(user snowflake.ID) *forays {
	if s.forays == nil {
		s.forays = map[snowflake.ID]*forays{}
	}
	f, ok := s.forays[user]
	if !ok {
		f = &forays{arcs: map[string]int{}}
		s.forays[user] = f
	}
	return f
}

func arcKey(place int, class string) string { return places[place].Key + "/" + class }

// walker is the Walker for user's outing o in s. s.mu held.
func (st *state) walker(s *server, user snowflake.ID, o *outing) Walker {
	return Walker{
		Seed: st.seedOf(o.day), Guild: uint64(s.id), User: uint64(user), Day: o.day,
		Place: o.place, Far: o.far, Lean: o.lean, Class: o.class, Stage: o.stage, Company: o.partner,
	}
}

// setOut sends user out today to place, near or far, leaning lean. A fold
// bird going where another fold bird has gone today, who hasn't met anyone,
// meets them out there. s.mu held.
func (st *state) setOut(s *server, user snowflake.ID, place int, far bool, lean Wind, today int64) error {
	if place < 0 || place >= len(places) || lean < Writ || lean > Fold {
		return errForayWhere
	}
	f := s.forayOf(user)
	switch {
	case f.out != nil && !f.out.back:
		return errForayOut
	case f.lastOut == today:
		return errForayToday
	}
	mem := st.member(s, s.id, user, today)
	class := ClassOf(mem.class).Key
	reach := 0
	if far {
		reach = 1
	}
	o := &outing{place: place, far: far, lean: lean, class: class, stage: f.arcs[arcKey(place, class)], day: today, need: forayQuiet[reach]}
	if lean == Fold {
		for u, g := range s.forays {
			if p := g.out; u != user && p != nil && !p.back && p.day == today && p.place == place && p.lean == Fold && p.companion == 0 {
				o.companion, o.partner = u, nameOf(s, u)
				p.companion, p.partner = user, nameOf(s, user)
				g.dirty = true
				s.count(today, statForayCompany, place, 0)
				break
			}
		}
	}
	f.out, f.lastOut, f.dirty = o, today, true
	s.count(today, statForayOut, place, int(lean))
	return nil
}

// nameOf is what the story calls user in s. s.mu held.
func nameOf(s *server, user snowflake.ID) string {
	if m, ok := s.members[user]; ok && m.name != "" {
		return m.name
	}
	return "another bird"
}

// forayTick is the quiet clock's minute: every bird out whom the pot paid
// nothing this minute walks a minute further, and one whose road is done
// comes back. voiced are who the minute's voice paid. s.mu held.
func (st *state) forayTick(s *server, voiced map[snowflake.ID]bool, now time.Time) {
	for u, f := range s.forays {
		o := f.out
		if o == nil || o.back {
			continue
		}
		if voiced[u] {
			continue
		}
		if m, ok := s.members[u]; ok && now.Sub(m.lastPay) < time.Minute {
			continue
		}
		o.quiet++
		f.dirty = true
		if o.quiet >= o.need {
			st.bringBack(s, u, f, DayOf(now))
		}
	}
}

// bringBack ends user's day out: what it found is theirs, its events'
// pages and its ending are in their book, and a class arc stage passed
// moves the arc on. s.mu held.
func (st *state) bringBack(s *server, user snowflake.ID, f *forays, today int64) {
	o := f.out
	d := walk(st.walker(s, user, o))
	o.back = true
	switch places[o.place].Find {
	case "peat":
		f.peat += int64(d.Finds)
	case "amber":
		f.amber += int64(d.Finds)
	}
	for _, step := range d.Steps {
		if step.Index >= 0 {
			f.pages[o.place] |= 1 << step.Index
		}
	}
	f.endings[o.place] |= 1 << d.End
	if d.ArcPassed {
		f.arcs[arcKey(o.place, o.class)] = o.stage + 1
	}
	f.dirty = true
	s.count(today, statForayBack, o.place, d.End)
}

// feedPeat puts n of user's peat on the hearth: each counts toward the
// server's day as peatRaw, and may light the fire or raise it. s.mu held.
func (st *state) feedPeat(s *server, user snowflake.ID, n int64, today int64) error {
	f := s.forayOf(user)
	if n <= 0 || f.peat < n {
		return errNoPeat
	}
	st.rollServer(s, today)
	f.peat -= n
	f.dirty = true
	s.raw += n * peatRaw
	if tier := st.bal().HearthTier(s.raw, s.history); tier > s.lit {
		s.lit = tier
	}
	s.dirty = true
	s.count(today, statPeatFed, int(n), 0)
	return nil
}

// sitOnAmber seats user at t on the house's marker for a candle, paid
// with a candle's worth of amber: the stake is the house's, nothing leaves
// the purse, and they keep what the bones win past it. Once a day.
// ponytail: a marker seat isn't refunded at a restart, so amber staked on
// a table a deploy cuts off is lost; mark amber seats if that bites.
// s.mu held.
func (st *state) sitOnAmber(s *server, t *table, guild, user snowflake.ID, name string, now time.Time) error {
	today := DayOf(now)
	f := s.forayOf(user)
	switch {
	case t.rail != 0:
		return errMarkerRail
	case f.amberDay == today:
		return errAmberDone
	case f.amber < amberCandle:
		return errNoAmber
	}
	if err := st.seat(s, t, guild, user, name, stakes[1].pp, now, true); err != nil {
		return err
	}
	f.amber -= amberCandle
	f.amberDay, f.dirty = today, true
	s.count(today, statAmberStaked, 0, 0)
	return nil
}

// encodeArcs is a member's arcs as their row keeps them.
func encodeArcs(arcs map[string]int) string {
	keys := make([]string, 0, len(arcs))
	for k := range arcs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%d", k, arcs[k])
	}
	return b.String()
}

func decodeArcs(s string) map[string]int {
	out := map[string]int{}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if n, err := strconv.Atoi(v); ok && err == nil {
			out[k] = n
		}
	}
	return out
}

// forayRows are forays taken for a flush, as columns for one unnest.
type forayRows struct {
	guild, user, lastOut, peat, amber, amberDay []int64
	place, lean, stage, quiet, need             []int64
	day, companion                              []int64
	far, back, out                              []bool
	class, partner, arcs                        []string
	pages, endings                              []string
}

func (r *forayRows) add(guild, user snowflake.ID, f *forays) {
	o := f.out
	if o == nil {
		o = &outing{}
	}
	r.guild, r.user = append(r.guild, int64(guild)), append(r.user, int64(user))
	r.lastOut, r.peat, r.amber, r.amberDay = append(r.lastOut, f.lastOut), append(r.peat, f.peat), append(r.amber, f.amber), append(r.amberDay, f.amberDay)
	r.out = append(r.out, f.out != nil)
	r.place, r.lean, r.stage = append(r.place, int64(o.place)), append(r.lean, int64(o.lean)), append(r.stage, int64(o.stage))
	r.quiet, r.need, r.day = append(r.quiet, int64(o.quiet)), append(r.need, int64(o.need)), append(r.day, o.day)
	r.companion, r.far, r.back = append(r.companion, int64(o.companion)), append(r.far, o.far), append(r.back, o.back)
	r.class, r.partner, r.arcs = append(r.class, o.class), append(r.partner, o.partner), append(r.arcs, encodeArcs(f.arcs))
	r.pages, r.endings = append(r.pages, masks(f.pages)), append(r.endings, masks(f.endings))
}

// masks is a per-place bitmask as its column keeps it, a bigint array.
func masks(b [2]int64) string { return fmt.Sprintf("{%d,%d}", b[0], b[1]) }

// write upserts the rows, inside the flush's transaction.
func (r *forayRows) write(ctx context.Context, tx pgx.Tx) error {
	if len(r.user) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `insert into pp_foray (guild_id, user_id, last_out, peat, amber, amber_day, out, place, lean, stage, quiet, need, day, companion, far, back, class, partner, arcs, pages, endings)
		select g, u, lo, pe, am, ad, o, pl, le, st, q, n, d, c, f, b, cl, pa, ar, pg::bigint[], en::bigint[]
		from unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::bigint[], $5::bigint[], $6::bigint[], $7::boolean[], $8::bigint[], $9::bigint[], $10::bigint[],
			$11::bigint[], $12::bigint[], $13::bigint[], $14::bigint[], $15::boolean[], $16::boolean[], $17::text[], $18::text[], $19::text[], $20::text[], $21::text[])
			as t(g, u, lo, pe, am, ad, o, pl, le, st, q, n, d, c, f, b, cl, pa, ar, pg, en)
		on conflict (guild_id, user_id) do update set last_out = excluded.last_out, peat = excluded.peat, amber = excluded.amber,
			amber_day = excluded.amber_day, out = excluded.out, place = excluded.place, lean = excluded.lean, stage = excluded.stage,
			quiet = excluded.quiet, need = excluded.need, day = excluded.day, companion = excluded.companion, far = excluded.far,
			back = excluded.back, class = excluded.class, partner = excluded.partner, arcs = excluded.arcs,
			pages = excluded.pages, endings = excluded.endings`,
		r.guild, r.user, r.lastOut, r.peat, r.amber, r.amberDay, r.out, r.place, r.lean, r.stage,
		r.quiet, r.need, r.day, r.companion, r.far, r.back, r.class, r.partner, r.arcs, r.pages, r.endings)
	return err
}

// loadForays reads every member's forays.
func (st *state) loadForays(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select guild_id, user_id, last_out, peat, amber, amber_day, out, place, lean, stage, quiet, need, day,
		companion, far, back, class, partner, arcs, pages, endings from pp_foray`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guild, user, place, lean, stage, quiet, need, companion int64
		var out bool
		var arcs string
		var pages, endings []int64
		f := &forays{}
		o := &outing{}
		if err := rows.Scan(&guild, &user, &f.lastOut, &f.peat, &f.amber, &f.amberDay, &out, &place, &lean, &stage, &quiet, &need, &o.day,
			&companion, &o.far, &o.back, &o.class, &o.partner, &arcs, &pages, &endings); err != nil {
			return err
		}
		o.place, o.lean, o.stage, o.quiet, o.need, o.companion = int(place), Wind(lean), int(stage), int(quiet), int(need), snowflake.ID(companion)
		if out && o.place >= 0 && o.place < len(places) {
			f.out = o
		}
		copy(f.pages[:], pages)
		copy(f.endings[:], endings)
		f.arcs = decodeArcs(arcs)
		s := st.server(snowflake.ID(guild))
		s.mu.Lock()
		if s.forays == nil {
			s.forays = map[snowflake.ID]*forays{}
		}
		s.forays[snowflake.ID(user)] = f
		s.mu.Unlock()
	}
	return rows.Err()
}
