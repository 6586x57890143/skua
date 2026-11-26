package game

import (
	"math"
	"time"
)

// Curve is a levelling curve: level L takes Base*L*L in total, so the first
// comes quickly and each after takes a little longer than the last.
type Curve struct{ Base int64 }

// Level is the level xp reaches.
func (c Curve) Level(xp int64) int {
	if xp < c.Base || c.Base <= 0 {
		return 0
	}
	l := int(math.Sqrt(float64(xp) / float64(c.Base)))
	// Float rounding can land a step off either way near a square.
	for c.Need(l+1) <= xp {
		l++
	}
	for l > 0 && c.Need(l) > xp {
		l--
	}
	return l
}

// Need is the total xp level l takes.
func (c Curve) Need(l int) int64 { return c.Base * int64(l) * int64(l) }

// Combo is a meter that rises with every hit and decays between them, so
// it reads how fast the play is now rather than how much there has been.
// Its zero value is empty.
type Combo struct {
	Score float64   `json:"s"`
	At    time.Time `json:"at"`
}

// Hit adds one at t after decaying the meter by tau since the last hit, and
// returns the new score. A t before the last hit decays nothing.
func (c *Combo) Hit(t time.Time, tau time.Duration) float64 {
	c.Score = c.Now(t, tau) + 1
	if t.After(c.At) {
		c.At = t
	}
	return c.Score
}

// Now is the score as it has decayed by t, without hitting.
func (c *Combo) Now(t time.Time, tau time.Duration) float64 {
	if c.At.IsZero() || !t.After(c.At) {
		return c.Score
	}
	return c.Score * math.Exp(-float64(t.Sub(c.At))/float64(tau))
}

// Tier is how many of the ascending thresholds score has reached.
func Tier(score float64, thresholds ...float64) int {
	n := 0
	for _, th := range thresholds {
		if score >= th {
			n++
		}
	}
	return n
}
