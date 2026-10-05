// Package guard is skua's own throttle on writes to Discord, independent of
// Discord's rate limits: a guild driving the bot far past anything a
// correct feature does is a bug, a bad config or an attack, and none of
// those should get the application rate limited or flagged by Discord.
//
// It is built for the hot path. Each
// (guild, op) budget is a GCRA cell: a single int64 holding the
// theoretical arrival time, advanced with one compare-and-swap. No mutex,
// no allocation after a key's first use, and no background refill
// goroutine, since the refill is arithmetic on the clock.
package guard

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

var (
	ErrRateLimited = errors.New("guard: per-guild cap exceeded")
	ErrCircuitOpen = errors.New("guard: circuit open for this guild")
)

// Op names a kind of write.
type Op uint8

const (
	MessageSend Op = iota
	MessageDelete
	ChannelEdit
	MemberEdit
	WebhookExecute
	WebhookCreate
	// WhisperMember is keyed by member ID, not guild: one member's share of
	// the guild's WebhookExecute budget.
	WhisperMember
	// Purge is one /purge write call, so a bulk delete of 100 counts once.
	Purge
	// PurgeMember is keyed by member ID: how often one member can start
	// /purge now.
	PurgeMember
	// Reaction is one reaction added or removed.
	Reaction
	// CommandSync is one overwrite of a guild's command list after a module
	// is turned on or off.
	CommandSync
	opCount
)

// caps are per guild per hour: well above a
// bad afternoon, well below "the bot did something nobody asked for".
var caps = [opCount]int64{
	MessageSend:    120,
	MessageDelete:  300,
	ChannelEdit:    60,
	MemberEdit:     120,
	WebhookExecute: 300,
	// One per channel, ever, in normal use.
	WebhookCreate: 20,
	WhisperMember: 30,
	// A first sweep of a busy guild is thousands of single deletes of old
	// messages; Discord's per-channel limit is the real ceiling, this only
	// stops a runaway loop.
	Purge:       100000,
	PurgeMember: 6,
	// preen spends thirteen per self-react, twelve birds and the removal:
	// about ninety-seven a guild an hour. Kept high on purpose; it is a
	// runaway stop, not a pace, and never trims a burst.
	Reaction: 1260,
	// Discord allows 200 command creates a guild a day.
	CommandSync: 8,
}

const (
	window = time.Hour
	// tripAfter consecutive 429/5xx answers open a guild's breaker for
	// coolDown. Writes fail fast while it is open rather than queueing
	// behind an API that is already refusing.
	tripAfter = 5
	coolDown  = 30 * time.Second
)

type key struct {
	guild snowflake.ID
	op    Op
}

type breaker struct {
	fails     atomic.Int32
	openUntil atomic.Int64
}

// Guard is safe for concurrent use. The zero value is not; use New.
type Guard struct {
	cells    sync.Map // key -> *atomic.Int64 (TAT, unix nanos)
	breakers sync.Map // snowflake.ID -> *breaker
	now      func() time.Time
}

func New() *Guard { return &Guard{now: time.Now} }

// Allow spends one unit of guild's op budget, or reports why it cannot.
func (g *Guard) Allow(guild snowflake.ID, op Op) error {
	now := g.now().UnixNano()
	if b, ok := g.breakers.Load(guild); ok && b.(*breaker).openUntil.Load() > now {
		return ErrCircuitOpen
	}
	v, ok := g.cells.Load(key{guild, op})
	if !ok {
		v, _ = g.cells.LoadOrStore(key{guild, op}, new(atomic.Int64))
	}
	cell := v.(*atomic.Int64)
	step := int64(Step(op))
	for {
		tat := cell.Load()
		next := max(tat, now) + step
		// A burst of the whole hour's cap is allowed; past that, one per
		// step as the window drains.
		if next-now > int64(window) {
			return ErrRateLimited
		}
		if cell.CompareAndSwap(tat, next) {
			return nil
		}
	}
}

// Step is how long one unit of op's budget takes to come back.
func Step(op Op) time.Duration { return window / time.Duration(caps[op]) }

// Report feeds a write's outcome back to the breaker. failed should be true
// only for answers that say Discord is struggling (429, 5xx), never for a
// 4xx that says the request itself was wrong.
func (g *Guard) Report(guild snowflake.ID, failed bool) {
	v, ok := g.breakers.Load(guild)
	if !ok {
		if !failed {
			return
		}
		v, _ = g.breakers.LoadOrStore(guild, new(breaker))
	}
	b := v.(*breaker)
	if !failed {
		b.fails.Store(0)
		return
	}
	if b.fails.Add(1) >= tripAfter {
		b.fails.Store(0)
		b.openUntil.Store(g.now().Add(coolDown).UnixNano())
	}
}

// ponytail: cells and breakers are never evicted; a few hundred bytes per
// guild that has ever written. Add a sweep if skua joins thousands of guilds.
