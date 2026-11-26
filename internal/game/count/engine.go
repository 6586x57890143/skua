package count

import (
	"encoding/binary"
	"time"

	"github.com/6586x57890143/skua/internal/game"
)

// domain names counting's chains, so no other game's hash collides.
const domain = "skua/count/v1"

// The arcade layer's numbers.
const (
	comboTau   = 10 * time.Second
	bossLength = 10   // the counts leading up to every thousandth
	bossEvery  = 1000 // the boss stretch ends on each multiple of this
	milestone  = 100
	bountyXP   = 5
	breakXP    = 10
	fuzzSteps  = 10  // how near a fuzzy word must land to be a miss
	memberSave = 100 // own counts per save a member earns
	memberMax  = 2
	bankSave   = 250 // counts in a run per save the channel earns
	bankMax    = 3
)

// comboTiers are the combo meter's tiers: at a count a second the meter
// sits near 10, at two a second near 20.
var comboTiers = []float64{4, 9, 16}

// Flock is the channel's level by lifetime counts and Ranks a member's by
// xp.
var (
	Flock = game.Curve{Base: 100}
	Ranks = game.Curve{Base: 25}
)

// Move is one message in a counting channel.
type Move struct {
	User uint64
	// Msg is the message's id. Ids rise with the time Discord gave each
	// message, which is the order the engine counts in.
	Msg  uint64
	Text string
	// At is the time in the message's id, never when it arrived.
	At time.Time
	// Forgive is a move nobody saw answered, read back after skua was
	// away: a correct count stands and a miss changes nothing, since the
	// channel couldn't know it had missed.
	Forgive bool
}

// Kind is what a move did.
type Kind uint8

const (
	// Ignore is chat, not a count.
	Ignore Kind = iota
	// Accept is the next number.
	Accept
	// Sniped is the number just counted, sent again by someone else
	// within the grace: a tie lost, not a miss.
	Sniped
	// Stale is a move older than the last count, arriving late.
	Stale
	// Early is a count inside the member's cooldown.
	Early
	// Saved is a miss a save covered.
	Saved
	// Lost is a miss that cost a life.
	Lost
	// Broken is a miss that ended the run.
	Broken
)

// Miss is why a count missed.
type Miss uint8

const (
	Wrong Miss = iota + 1
	Twice      // the same member twice in a row
	Boss       // a member counting twice in the boss stretch
)

// Saver is whose save covered a miss.
type Saver uint8

const (
	ByMember Saver = iota + 1
	ByChannel
)

// Verdict is what a move did and everything a caller shows for it.
type Verdict struct {
	Kind Kind
	Miss Miss
	// N is the count's place in its run after the move: 0 once a run
	// breaks, since the next run has begun. Run is the run it was in.
	N int64
	// Expected is the number that was due, as written.
	Expected string
	// Run is the run the move was in; Length how long it was when it
	// broke.
	Run    int64
	Length int64
	Best   int64
	// Hash is the accepted count's link, or a broken run's head.
	Hash game.Hash

	Milestone bool // every hundredth count
	Record    bool // the first count past the channel's best
	Bounty    bool // the run's hidden bounty number
	Boss      bool // a count in the boss stretch
	Combo     int  // the combo tier, 0 to 3
	Flock     int  // the channel's new level, 0 if it didn't level
	Rank      int  // the member's new rank, 0 if they didn't rank up

	Saver Saver
	Left  int // saves the saver has left, or lives the run has
	// Timeout is how long the caller should time out whoever broke the
	// run, before its own doubling; zero is none.
	Timeout time.Duration
}

// State is one counting channel. It is plain data so a caller can store it
// whole and give it back.
type State struct {
	Guild   uint64     `json:"guild"`
	Rules   Ruleset    `json:"rules"`
	Run     int64      `json:"run"`
	N       int64      `json:"n"`
	Start   int64      `json:"start,omitempty"` // where an imported run began
	Head    game.Hash  `json:"head"`
	Last    uint64     `json:"last,omitempty"`
	LastMsg uint64     `json:"last_msg,omitempty"`
	LastAt  time.Time  `json:"last_at"`
	Best    int64      `json:"best"`
	Beat    int64      `json:"beat,omitempty"` // the best when this run began
	Saves   int        `json:"saves"`
	Lives   int        `json:"lives,omitempty"`
	Total   int64      `json:"total"`
	Bounty  int64      `json:"bounty,omitempty"`
	Seen    []uint64   `json:"seen,omitempty"` // who counted in this boss stretch
	Combo   game.Combo `json:"combo"`
	// Waits is when each member last counted here, while their cooldown
	// runs.
	Waits map[uint64]time.Time `json:"waits,omitempty"`
	// Recorded is whether this run has passed Beat yet.
	Recorded bool `json:"recorded,omitempty"`
}

// wait notes when user last counted here, for the cooldown, forgetting
// anyone whose cooldown is over.
func (s *State) wait(user uint64, at time.Time) {
	if s.Waits == nil {
		s.Waits = map[uint64]time.Time{}
	}
	for u, t := range s.Waits {
		if at.Sub(t) >= s.Rules.Cooldown {
			delete(s.Waits, u)
		}
	}
	s.Waits[user] = at
}

// Next is the number due next, as written.
func (s State) Next() string { return Say((s.N+1)*s.Rules.Step, s.Rules) }

// Member is one member's counting in one server.
type Member struct {
	Counts    int64     `json:"counts"`
	Misses    int64     `json:"misses"`
	Breaks    int64     `json:"breaks"`
	SavesUsed int64     `json:"saves_used"`
	Saves     int       `json:"saves"`
	XP        int64     `json:"xp"`
	BestCombo int       `json:"best_combo"`
	LastAt    time.Time `json:"last_at"`
}

// Who is a member in a server.
type Who struct{ Guild, User uint64 }

// Entry is one accepted count, what a run's chain is made of.
type Entry struct {
	N         int64
	User, Msg uint64
}

// Engine is every counting channel. It is safe for concurrent use: each
// channel and each member has its own lock, taken channel first.
type Engine struct {
	chans   game.Table[uint64, State]
	members game.Table[Who, Member]
}

// Genesis is the first link of a run's chain.
func Genesis(guild, ch uint64, run, start int64) game.Hash {
	return game.Genesis(domain, guild, ch, run, start)
}

func link(prev game.Hash, run int64, e Entry) game.Hash {
	return game.Link(prev, run, e.N, e.User, e.Msg)
}

// Verify walks a run's entries from its genesis and reports whether they
// arrive at head, in order and with none missing.
func Verify(guild, ch uint64, run, start int64, entries []Entry, head game.Hash) bool {
	h := Genesis(guild, ch, run, start)
	for i, e := range entries {
		if e.N != start+int64(i)+1 {
			return false
		}
		h = link(h, run, e)
	}
	return h == head
}

// Open makes ch a counting channel under r, or changes its rules. A change
// to what the next number is starts a new run.
func (en *Engine) Open(guild, ch uint64, r Ruleset) {
	r = r.Normal()
	en.chans.Upsert(ch, func(s *State, fresh bool) {
		old := s.Rules
		s.Guild, s.Rules = guild, r
		if fresh || old.shapes(r) {
			s.restart(ch, 0)
		}
		if r.Fail == Lives && (fresh || old.Fail != Lives) {
			s.Lives = r.Lives
		}
	})
}

// Close stops counting in ch.
func (en *Engine) Close(ch uint64) { en.chans.Delete(ch) }

// Restore puts back a stored channel.
func (en *Engine) Restore(ch uint64, s State) {
	s.Rules = s.Rules.Normal()
	en.chans.Upsert(ch, func(d *State, _ bool) { *d = s })
}

// RestoreMember puts back a stored member.
func (en *Engine) RestoreMember(w Who, m Member) {
	en.members.Upsert(w, func(d *Member, _ bool) { *d = m })
}

// Channel is a copy of ch's state.
func (en *Engine) Channel(ch uint64) (State, bool) { return en.chans.Get(ch) }

// Channels is every counting channel.
func (en *Engine) Channels() []uint64 { return en.chans.Keys() }

// Member is a copy of a member's counting.
func (en *Engine) Member(w Who) Member { m, _ := en.members.Get(w); return m }

// Set starts a new run in ch from number, the last one counted, for a
// server moving from another bot; a number between steps rounds down. The
// run's genesis records where it began.
func (en *Engine) Set(ch uint64, number int64) (State, bool) {
	var out State
	ok := en.chans.Do(ch, func(s *State) {
		s.restart(ch, max(number, 0)/s.Rules.Step)
		out = *s
	})
	return out, ok
}

// restart begins the next run at start.
func (s *State) restart(ch uint64, start int64) {
	s.Run++
	s.N, s.Start, s.Beat, s.Recorded = start, start, s.Best, false
	s.Head = Genesis(s.Guild, ch, s.Run, start)
	s.Last, s.Bounty, s.Seen, s.Waits = 0, 0, nil, nil
	s.Combo = game.Combo{}
	if s.Rules.Fail == Lives {
		s.Lives = s.Rules.Lives
	}
}

// Submit is what m does in ch. Moves must come in the order of their ids;
// one that doesn't is Stale and changes nothing.
func (en *Engine) Submit(ch uint64, m Move) Verdict {
	v := Verdict{Kind: Ignore}
	en.chans.Do(ch, func(s *State) { v = en.submit(ch, s, m) })
	return v
}

func (en *Engine) submit(ch uint64, s *State, m Move) Verdict {
	r := s.Rules
	in := read(m.Text, r)
	if !in.counted {
		return Verdict{Kind: Ignore}
	}
	v := Verdict{Run: s.Run, N: s.N, Best: s.Best}
	if m.Msg <= s.LastMsg {
		v.Kind = Stale
		return v
	}
	due := (s.N + 1) * r.Step
	v.Expected = Say(due, r)
	right := matches(in, due, r)
	if !right && s.N > s.Start && matches(in, s.N*r.Step, r) && m.User != s.Last && m.At.Sub(s.LastAt) <= r.Grace {
		v.Kind = Sniped
		return v
	}
	if !right && (in.fuzzy || in.strict) && abs(in.value-due) > fuzzSteps*r.Step {
		return Verdict{Kind: Ignore}
	}
	if !right && in.letters && !in.strict {
		return Verdict{Kind: Ignore}
	}
	en.members.Upsert(Who{s.Guild, m.User}, func(mem *Member, _ bool) {
		v = en.decide(ch, s, mem, m, right, v)
	})
	return v
}

// decide settles a count under the channel's lock and the member's.
func (en *Engine) decide(ch uint64, s *State, mem *Member, m Move, right bool, v Verdict) Verdict {
	r := s.Rules
	if last, ok := s.Waits[m.User]; r.Cooldown > 0 && ok && m.At.Sub(last) < r.Cooldown {
		v.Kind = Early
		return v
	}
	if r.Cooldown > 0 {
		s.wait(m.User, m.At)
	}
	mem.LastAt = m.At
	s.LastMsg = m.Msg

	n := s.N + 1
	stretch := r.Arcade && n > bossEvery-bossLength && (n%bossEvery == 0 || n%bossEvery > bossEvery-bossLength)
	if stretch && n%bossEvery == bossEvery-bossLength+1 {
		s.Seen = nil
	}
	switch {
	case !right:
		v.Miss = Wrong
	case !r.Solo && m.User == s.Last:
		v.Miss = Twice
	case stretch && contains(s.Seen, m.User):
		v.Miss = Boss
	}
	if v.Miss != 0 && m.Forgive {
		v.Kind = Ignore
		return v
	}
	if v.Miss != 0 {
		en.miss(ch, s, mem, &v)
	} else {
		en.accept(s, mem, m, n, stretch, &v)
	}
	return v
}

func (en *Engine) accept(s *State, mem *Member, m Move, n int64, stretch bool, v *Verdict) {
	r := s.Rules
	e := Entry{N: n, User: m.User, Msg: m.Msg}
	s.Head = link(s.Head, s.Run, e)
	s.N, s.Last, s.LastAt = n, m.User, m.At
	s.Total++
	mem.Counts++
	v.Kind, v.N, v.Hash = Accept, n, s.Head

	if s.Beat > 0 && n > s.Beat && !s.Recorded {
		v.Record, s.Recorded = true, true
	}
	s.Best = max(s.Best, n)
	v.Best = s.Best
	v.Milestone = n%milestone == 0
	if r.Fail == Reset {
		if mem.Counts%memberSave == 0 && mem.Saves < memberMax {
			mem.Saves++
		}
		if (n-s.Start)%bankSave == 0 && s.Saves < bankMax {
			s.Saves++
		}
	}
	if r.Fail == Lives && v.Milestone {
		s.Lives = r.Lives
	}
	if !r.Arcade {
		return
	}
	xp := int64(1)
	if stretch {
		v.Boss = true
		s.Seen = append(s.Seen, m.User)
		if n%bossEvery == 0 {
			s.Seen = nil
		}
	}
	v.Combo = game.Tier(s.Combo.Hit(m.At, comboTau), comboTiers...)
	mem.BestCombo = max(mem.BestCombo, v.Combo)
	xp += int64(v.Combo)
	if s.Bounty != 0 && n == s.Bounty {
		v.Bounty = true
		s.Bounty = 0
		xp += bountyXP
	}
	if v.Milestone {
		// The next bounty hides in the chain: nobody can know it until
		// this count is made, and anyone can check it from the proof.
		s.Bounty = n + 1 + int64(binary.BigEndian.Uint64(s.Head[:8])%(milestone-1))
	}
	if Flock.Level(s.Total) > Flock.Level(s.Total-1) {
		v.Flock = Flock.Level(s.Total)
	}
	before := Ranks.Level(mem.XP)
	mem.XP += xp
	if after := Ranks.Level(mem.XP); after > before {
		v.Rank = after
	}
}

func (en *Engine) miss(ch uint64, s *State, mem *Member, v *Verdict) {
	r := s.Rules
	mem.Misses++
	s.Combo = game.Combo{}
	switch {
	case r.Fail == Reset && mem.Saves > 0:
		mem.Saves--
		mem.SavesUsed++
		v.Kind, v.Saver, v.Left = Saved, ByMember, mem.Saves
		return
	case r.Fail == Reset && s.Saves > 0:
		s.Saves--
		mem.SavesUsed++
		v.Kind, v.Saver, v.Left = Saved, ByChannel, s.Saves
		return
	case r.Fail == Lives && s.Lives > 1:
		s.Lives--
		v.Kind, v.Left = Lost, s.Lives
		return
	}
	mem.Breaks++
	if r.Arcade {
		mem.XP = max(mem.XP-breakXP, 0)
	}
	v.Kind, v.Length, v.Hash, v.Timeout = Broken, s.N-s.Start, s.Head, r.Timeout
	s.restart(ch, 0)
	v.N = s.N
}

// matches is whether what was said is v written under r: the word where
// fizzbuzz has one, a numeral where roman can write one.
func matches(in said, v int64, r Ruleset) bool {
	if in.word != "" {
		return r.Format == FizzBuzz && in.word == fizz(v)
	}
	switch {
	case in.value != v:
		return false
	case r.Format == FizzBuzz && fizz(v) != "":
		return false
	case r.Format == Roman && in.decimal && toRoman(v) != "":
		return false
	}
	return true
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func contains(s []uint64, x uint64) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
