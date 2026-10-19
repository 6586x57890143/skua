// Package sanction is what happens to a member once a module decides they
// broke a rule: how long for, given their record, and through what. It is
// merlin's shape (a pure doubling ladder, the record written before the
// sanction is tried, an unreadable record read as a clean one, a fallback
// when the first choice can't be applied) cut down to parts any bird can
// hold: a detector hands Judge an Offence, Judge reads the History and the
// Ladder into a Sentence, and the first Sanction in line that can carry it
// out does. Nothing here imports a module or a store.
//
// skua ships one Sanction, the bird. A timeout, a ban or a jail is another
// Sanction handed to Judge (merlin's jail-then-timeout is Judge(..., jail,
// timeout)), and none of them changes the detector or the ladder.
package sanction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// Offence is one thing a member did that a rule sanctions.
type Offence struct {
	Guild, User, Channel snowflake.ID
	// Rule names what was broken, "spam" or "spam.mentions", for the
	// record and the audit log.
	Rule string
	At   time.Time
}

// Sentence is an offence with what the ladder made of it.
type Sentence struct {
	Offence
	Priors int // sanctions on their record inside the ladder's window
	For    time.Duration
}

// Sanction carries a sentence out and says what it did in a few lowercase
// words for the channel ("a common blackbird for 10m"). ErrUnavailable
// means it couldn't here, and Judge tries the next.
type Sanction interface {
	Name() string
	Apply(ctx context.Context, s Sentence) (string, error)
}

// ErrUnavailable is a Sanction that can't be applied to this sentence at
// all, as against one that tried and failed.
var ErrUnavailable = errors.New("sanction: unavailable")

// Ladder is how long a sentence runs: Base, doubled for each prior inside
// Window, at most Doublings times, never past Max.
type Ladder struct {
	Base, Max time.Duration
	Doublings int
	Window    time.Duration
}

// For is the sentence after priors earlier sanctions.
func (l Ladder) For(priors int) time.Duration {
	return min(l.Base<<min(max(priors, 0), l.Doublings), l.Max)
}

// History is a member's record of sanctions, across rules: the signal is
// the person, not the message.
type History interface {
	Priors(ctx context.Context, guild, user snowflake.ID, since time.Time) (int, error)
	Record(ctx context.Context, s Sentence) error
}

// Verdict is what Judge did: the sentence, which sanction carried it out
// and its words, or why none did.
type Verdict struct {
	Sentence
	By   string
	Said string
	Err  error
}

// Judge sentences o on the ladder and applies it through the first of in
// that can: each is tried in order until one applies or fails for a reason
// other than ErrUnavailable, so merlin's jail falling back to a timeout is
// Judge(..., jail, timeout). The sentence is recorded before anything is
// applied, and whether or not it is, so a sanction that fails because
// Discord was briefly away doesn't wipe the record the next offence reads;
// an unreadable record counts as none, so a broken store leans lenient. A
// nil History keeps no record.
func Judge(ctx context.Context, h History, l Ladder, o Offence, in ...Sanction) Verdict {
	priors := 0
	if h != nil {
		if n, err := h.Priors(ctx, o.Guild, o.User, o.At.Add(-l.Window)); err == nil {
			priors = n
		}
	}
	v := Verdict{Sentence: Sentence{Offence: o, Priors: priors, For: l.For(priors)}}
	var recorded error
	if h != nil {
		if err := h.Record(ctx, v.Sentence); err != nil {
			recorded = fmt.Errorf("recording: %w", err)
		}
	}
	v.Err = ErrUnavailable
	for _, s := range in {
		v.Said, v.Err = s.Apply(ctx, v.Sentence)
		if !errors.Is(v.Err, ErrUnavailable) {
			v.By = s.Name()
			break
		}
	}
	// Words from a sanction that failed would announce what didn't happen.
	if v.Err != nil {
		v.Said = ""
	}
	v.Err = errors.Join(recorded, v.Err)
	return v
}
