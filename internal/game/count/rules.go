// Package count is the counting game: a channel counts up together, one
// number a message, and the engine says what each message did. It knows
// nothing of Discord. The caller passes each move in the order Discord gave
// the messages ids, timed by those ids, and does the talking.
package count

import (
	"slices"
	"time"
)

// Fail is what a miss costs.
type Fail string

const (
	// Reset ends the run unless a save covers the miss: the member's own
	// first, then the channel's.
	Reset Fail = "reset"
	// Hardcore ends the run on any miss.
	Hardcore Fail = "hardcore"
	// Lives gives the run a few misses, refilled every hundred.
	Lives Fail = "lives"
)

// Format is how a number is written.
type Format string

const (
	Decimal  Format = "decimal"
	Binary   Format = "binary"
	Hex      Format = "hex"
	Roman    Format = "roman"
	FizzBuzz Format = "fizzbuzz"
)

// Math is whether a count may be an expression.
type Math string

const (
	MathOn   Math = "on"
	MathOff  Math = "off"
	MathOnly Math = "only"
)

// Ruleset is one channel's rules. Every field is data, so an admin switches
// rulesets by picking a preset and adjusting it, never by code.
type Ruleset struct {
	// Name is the preset it started from, for display.
	Name   string `json:"name"`
	Fail   Fail   `json:"fail"`
	Lives  int    `json:"lives,omitempty"`
	Step   int64  `json:"step"`
	Format Format `json:"format"`
	Math   Math   `json:"math"`
	// Solo lets one member count twice in a row.
	Solo bool `json:"solo,omitempty"`
	// Cooldown is how long a member waits between counts. A count sooner
	// is ignored, not a miss.
	Cooldown time.Duration `json:"cooldown,omitempty"`
	// Grace is the snipe window: the number just counted, sent again by
	// someone else this soon after, is ignored, not a miss.
	Grace time.Duration `json:"grace"`
	// Arcade is the game layer: combos, bounties, the boss stretch and
	// levels. The count itself never depends on it.
	Arcade bool `json:"arcade"`
	// Timeout is how long whoever ends a run is timed out the first time;
	// the caller doubles it for repeats. Zero is none.
	Timeout time.Duration `json:"timeout,omitempty"`
}

// The limits a ruleset is held to.
const (
	maxStep     = 1000
	maxLives    = 9
	maxCooldown = time.Hour
	maxGrace    = 5 * time.Second
	maxTimeout  = 24 * time.Hour
	stdGrace    = 1500 * time.Millisecond
	stdLives    = 3
)

var presets = map[string]Ruleset{
	"standard": {Fail: Reset, Format: Decimal, Math: MathOn, Grace: stdGrace, Arcade: true},
	"hardcore": {Fail: Hardcore, Format: Decimal, Math: MathOn, Grace: stdGrace, Arcade: true},
	"lives":    {Fail: Lives, Lives: stdLives, Format: Decimal, Math: MathOn, Grace: stdGrace, Arcade: true},
	"solo":     {Fail: Reset, Format: Decimal, Math: MathOn, Solo: true, Cooldown: 5 * time.Second, Grace: stdGrace, Arcade: true},
	"brain":    {Fail: Reset, Format: Decimal, Math: MathOnly, Grace: stdGrace, Arcade: true},
	"fizzbuzz": {Fail: Reset, Format: FizzBuzz, Math: MathOn, Grace: stdGrace, Arcade: true},
	"binary":   {Fail: Reset, Format: Binary, Math: MathOff, Grace: stdGrace, Arcade: true},
	"hex":      {Fail: Reset, Format: Hex, Math: MathOff, Grace: stdGrace, Arcade: true},
	"roman":    {Fail: Reset, Format: Roman, Math: MathOff, Grace: stdGrace, Arcade: true},
}

// Presets is every preset's name, standard first.
func Presets() []string {
	out := []string{"standard"}
	for k := range presets {
		if k != "standard" {
			out = append(out, k)
		}
	}
	slices.Sort(out[1:])
	return out
}

// Preset is the named preset, normalised.
func Preset(name string) (Ruleset, bool) {
	r, ok := presets[name]
	r.Name = name
	return r.Normal(), ok
}

// Standard is the default ruleset.
func Standard() Ruleset { r, _ := Preset("standard"); return r }

// Normal is r with every field inside its limits and every unknown value
// back at the standard one, so a stored ruleset can never break the engine.
func (r Ruleset) Normal() Ruleset {
	switch r.Fail {
	case Reset, Hardcore, Lives:
	default:
		r.Fail = Reset
	}
	switch r.Format {
	case Decimal, Binary, Hex, Roman, FizzBuzz:
	default:
		r.Format = Decimal
	}
	switch r.Math {
	case MathOn, MathOff, MathOnly:
	default:
		r.Math = MathOn
	}
	if r.Format != Decimal && r.Format != FizzBuzz {
		// Expressions are written in decimal.
		r.Math = MathOff
	}
	if r.Fail == Lives {
		r.Lives = min(max(r.Lives, 1), maxLives)
	} else {
		r.Lives = 0
	}
	r.Step = min(max(r.Step, 1), maxStep)
	if r.Format == Roman {
		// Roman numerals stop at 3999; a big step would get there in a
		// few counts.
		r.Step = min(r.Step, 10)
	}
	r.Cooldown = min(max(r.Cooldown, 0), maxCooldown)
	r.Grace = min(max(r.Grace, 0), maxGrace)
	r.Timeout = min(max(r.Timeout, 0), maxTimeout)
	if _, ok := presets[r.Name]; !ok {
		r.Name = "custom"
	}
	return r
}

// shapes reports whether going from r to o changes what the next number
// is, which starts a new run.
func (r Ruleset) shapes(o Ruleset) bool { return r.Step != o.Step || r.Format != o.Format }
