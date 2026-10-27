// Package flood tells a member flooding a channel from one who is only
// talking fast. It reads no Discord payload, only the few facts of a
// message, so any bird can carry it: skua's spam module feeds it, and
// merlin can; what happens to who it catches is internal/sanction's.
//
// Each member has one score that every message adds to and that decays
// with a ten second time constant. A message adds 1, more
// when it pings someone, repeats what the member just said, pings the same
// person again, tries @everyone, is a wall of text or hops channel. Lively
// chatter, distinct lines a second or two apart, settles well under the
// limit; a split sentence is a few points; the same short reply pinging one
// member over and over crosses it in four or five. Scoring rate rather than
// counting a window is what keeps a spammer pacing just under one from
// getting through, and the content key (filter.Key) is what keeps padding,
// digits, zero-width runes and lookalikes from making repeats look new.
package flood

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/filter"
)

const (
	tau   = 10 * time.Second
	limit = 12.0
	// window is how far back repeats and repeat targets are counted.
	window = time.Minute
	// ring is how many recent messages a member's state remembers, and so
	// the most of a burst that comes down with it.
	ring = 16
	// pendingLimit bounds cleanup memory; hitting it triggers timeout fallback.
	pendingLimit = 100000
	// bomb distinct pings in one message trip at once.
	bomb = 5
	// drip messages that ping, inside dripIn, trip at once.
	drip, dripIn = 4, 6 * time.Second
	// Quiet is how long a member who tripped is left to the bird before
	// the detector looks at them again.
	Quiet = 10 * time.Minute
	// forget drops a member's state after this long without a message.
	forget = 10 * time.Minute
)

// Message is what the detector reads of one message.
type Message struct {
	Guild, User, Channel, ID snowflake.ID
	At                       time.Time
	Text                     string
	// Pings is who it notifies: users and roles mentioned, and the author
	// of the message it replies to when the reply pings.
	Pings []snowflake.ID
	// Everyone is an @everyone or @here in the text, allowed or not.
	Everyone bool
	// Media is what it carries besides text, one string per sticker or
	// attachment, the same string for the same one (filter.MediaKey).
	Media []string
}

// Seen is a message the detector remembers, for deleting a burst.
type Seen struct{ Channel, ID snowflake.ID }

type seen struct {
	Seen
	at         time.Time
	key, media uint64
	to         snowflake.ID // the first ping, 0 for none
}

type member struct {
	mu          sync.Mutex
	score       float64
	last        time.Time
	quiet       time.Time // ignored until
	n           int       // messages seen, so ring[n%ring] is the oldest
	overflow    bool      // bounded cleanup history dropped a message
	overflowAt  time.Time
	ring        [ring]seen
	recent      []seen // full one-minute history for deleting the initial burst
	recentStart int
	pending     []Seen // all messages since the trip, drained by spam cleanup
	cleanup     bool
}

type who struct{ guild, user snowflake.ID }

// Detector is every member's score. The zero value is ready.
type Detector struct {
	members sync.Map // who -> *member
	swept   atomic.Int64
}

// Observe scores one message and reports whether it tripped the limit,
// with the member's messages from the last minute when it did, newest
// last, so the burst can come down. A member who tripped is not scored
// again for Quiet, but what they send is still remembered, for Since.
func (d *Detector) Observe(m Message) (bool, []Seen) {
	d.sweep(m.At)
	k := who{m.Guild, m.User}
	v, ok := d.members.Load(k)
	if !ok {
		v, _ = d.members.LoadOrStore(k, new(member))
	}
	s := v.(*member)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := seen{Seen: Seen{m.Channel, m.ID}, at: m.At, key: filter.Key(m.Text), media: filter.MediaKey(m.Media)}
	s.remember(now)
	if s.overflow && m.At.After(s.overflowAt.Add(window)) {
		s.overflow = false
	}
	if len(m.Pings) > 0 {
		now.to = m.Pings[0]
	}
	if s.cleanup || m.At.Before(s.quiet) {
		if s.cleanup && len(s.pending) < pendingLimit {
			s.pending = append(s.pending, now.Seen)
		} else if s.cleanup {
			s.overflow = true
			s.overflowAt = m.At
		}
		s.ring[s.n%ring] = now
		s.n++
		return false, nil
	}
	w, trip := s.weigh(m, now)
	if !s.last.IsZero() {
		s.score *= math.Exp(-float64(m.At.Sub(s.last)) / float64(tau))
	}
	s.score += w
	s.last = m.At
	s.ring[s.n%ring] = now
	s.n++
	if !trip && s.score < limit {
		return false, nil
	}
	s.score, s.quiet = 0, m.At.Add(Quiet)
	s.cleanup = true
	s.pending = s.pending[:0]
	for i := s.recentStart; i < len(s.recent); i++ {
		s.pending = append(s.pending, s.recent[i].Seen)
	}
	return true, s.burst(m.At)
}

// remember keeps the full active window separately from the small scoring
// ring. A bot can trip after many unique messages, all of which still need
// to be available for bulk deletion.
func (s *member) remember(now seen) {
	cutoff := now.at.Add(-window)
	for s.recentStart < len(s.recent) && s.recent[s.recentStart].at.Before(cutoff) {
		s.recentStart++
	}
	if s.overflow && now.at.After(s.overflowAt.Add(window)) {
		s.overflow = false
	}
	if len(s.recent)-s.recentStart >= pendingLimit {
		s.recentStart++
		s.overflow = true
		s.overflowAt = now.at
	}
	s.recent = append(s.recent, now)
	if s.recentStart >= 1024 && s.recentStart*2 >= len(s.recent)-1 {
		copy(s.recent, s.recent[s.recentStart:])
		s.recent = s.recent[:len(s.recent)-s.recentStart]
		s.recentStart = 0
	}
}

// TakeOverflow reports whether this member's bounded cleanup history
// overflowed and clears the flag. Spam uses it around cleanup to trigger its
// timeout fallback if any message ID was dropped.
func (d *Detector) TakeOverflow(guild, user snowflake.ID) bool {
	v, ok := d.members.Load(who{guild, user})
	if !ok {
		return false
	}
	s := v.(*member)
	s.mu.Lock()
	defer s.mu.Unlock()
	overflow := s.overflow
	s.overflow = false
	return overflow
}

// weigh is what m adds to the score, and whether it trips on its own.
func (s *member) weigh(m Message, now seen) (float64, bool) {
	w := 1.0
	pings := distinct(m.Pings)
	if m.Everyone {
		w += 4
	}
	if len(m.Text) > 1200 || lines(m.Text) > 15 {
		w += 3
	}
	var same, short, to, pinged int
	var prev seen
	for i := range min(s.n, ring) {
		r := s.ring[i]
		if m.At.Sub(r.at) > window {
			continue
		}
		// The same sticker or image again is a repeat whatever the text,
		// and so is the same text; a short reply is easy to repeat by
		// accident, so it counts apart, at half.
		switch {
		case now.media != 0 && r.media == now.media:
			same++
		case now.key == filter.Short && r.key == now.key:
			short++
		case now.key != 0 && r.key == now.key:
			same++
		}
		if now.to != 0 && r.to == now.to {
			to++
		}
		if r.to != 0 && m.At.Sub(r.at) <= dripIn {
			pinged++
		}
	}
	if s.n > 0 {
		prev = s.ring[(s.n-1)%ring]
		if prev.Channel != m.Channel {
			w += 0.5
		}
	}
	w += float64(min(same, 5)) + float64(min(short, 4))/2
	// A ping is 2, but pinging the one member already pinged this minute
	// is a conversation: 1, and a half more for each time, up to 2.
	w += 2 * float64(min(pings, 10))
	if to > 0 {
		w += float64(min(to, 4))/2 - 1
	}
	return w, pings >= bomb || (now.to != 0 && pinged+1 >= drip)
}

// Since is what the member sent from at on, oldest first: what came in
// after a trip, while its sanction was being applied.
func (d *Detector) Since(guild, user snowflake.ID, at time.Time) []Seen {
	v, ok := d.members.Load(who{guild, user})
	if !ok {
		return nil
	}
	s := v.(*member)
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Seen
	for i := max(s.n-ring, 0); i < s.n; i++ {
		if r := s.ring[i%ring]; !r.at.Before(at) {
			out = append(out, r.Seen)
		}
	}
	return out
}

// DrainSince atomically takes all messages queued since the trip. Messages
// received during the delete calls are returned by the next drain.
func (d *Detector) DrainSince(guild, user snowflake.ID) []Seen {
	v, ok := d.members.Load(who{guild, user})
	if !ok {
		return nil
	}
	s := v.(*member)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.pending
	s.pending = nil
	return out
}

// RetainPending puts failed deletions back into an active cleanup queue. It
// is used when containment enters manual-intervention mode after bounded
// timeout retries, so the incident's remaining IDs and later messages stay
// attached to the detector state.
func (d *Detector) RetainPending(guild, user snowflake.ID, messages []Seen) bool {
	v, ok := d.members.Load(who{guild, user})
	if !ok {
		return len(messages) > 0
	}
	s := v.(*member)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cleanup {
		return len(messages) > 0
	}
	known := make(map[Seen]struct{}, len(s.pending)+len(messages))
	for _, message := range s.pending {
		known[message] = struct{}{}
	}
	for _, message := range messages {
		if _, ok := known[message]; ok {
			continue
		}
		if len(s.pending) >= pendingLimit {
			s.overflow = true
			s.overflowAt = s.last
			continue
		}
		s.pending = append(s.pending, message)
		known[message] = struct{}{}
	}
	return s.overflow
}

// Pending is whether cleanup still has queued messages for this member.
func (d *Detector) Pending(guild, user snowflake.ID) bool {
	v, ok := d.members.Load(who{guild, user})
	if !ok {
		return false
	}
	s := v.(*member)
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}

// FinishCleanup takes a final batch atomically. It closes queueing only when
// there is no tail or overflow, so Observe either queues before completion or
// sees cleanup off after a clean handoff.
func (d *Detector) FinishCleanup(guild, user snowflake.ID, resumeScoring bool) ([]Seen, bool, bool) {
	v, ok := d.members.Load(who{guild, user})
	if !ok {
		return nil, false, true
	}
	s := v.(*member)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.pending
	overflow := s.overflow
	s.pending = nil
	s.overflow = false
	if len(out) > 0 || overflow {
		return out, overflow, false
	}
	s.cleanup = false
	if resumeScoring {
		s.quiet = time.Time{}
		s.score = 0
	}
	return nil, false, true
}

// DiscardPending releases a trip's cleanup queue when policy lets the member go.
func (d *Detector) DiscardPending(guild, user snowflake.ID) {
	v, ok := d.members.Load(who{guild, user})
	if !ok {
		return
	}
	s := v.(*member)
	s.mu.Lock()
	s.pending = nil
	s.cleanup = false
	s.overflow = false
	s.mu.Unlock()
}

func (s *member) burst(at time.Time) []Seen {
	var out []Seen
	for i := max(s.n-ring, 0); i < s.n; i++ {
		if r := s.ring[i%ring]; at.Sub(r.at) <= window {
			out = append(out, r.Seen)
		}
	}
	return out
}

// sweep forgets members gone quiet, at most once a minute.
func (d *Detector) sweep(at time.Time) {
	last := d.swept.Load()
	if at.UnixNano()-last < int64(time.Minute) || !d.swept.CompareAndSwap(last, at.UnixNano()) {
		return
	}
	d.members.Range(func(k, v any) bool {
		s := v.(*member)
		s.mu.Lock()
		gone := !s.cleanup && at.Sub(s.last) > forget && !at.Before(s.quiet)
		s.mu.Unlock()
		if gone {
			d.members.CompareAndDelete(k, v)
		}
		return true
	})
}

func distinct(ids []snowflake.ID) int {
	n := 0
	for i, id := range ids {
		dup := false
		for _, o := range ids[:i] {
			if o == id {
				dup = true
				break
			}
		}
		if !dup {
			n++
		}
	}
	return n
}

func lines(s string) int {
	n := 1
	for i := range len(s) {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}
