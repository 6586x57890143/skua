package pp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// Chirographs and round robins (LORE.md): a notice taken in company. One
// bird posts it and others sign it from the post: a chirograph takes two,
// a round robin three or four. The work is time together: a minute counts
// when PactNeed of its signers, or more, earn in voice in the same room.
// At PactGoal minutes each signer there for at least half of them is paid
// the joinder for how many were, as labour. A bird holds one at a time, is
// paid one a day, and an unfinished one lapses PactLife days after it was
// posted.

// pact is one chirograph or round robin. Each signer's member holds the
// same pointer, and every signer's row saves it whole.
type pact struct {
	opener snowflake.ID
	day    int64 // the day it was posted
	// at is when it was posted, unix milliseconds, so a bird's two posts
	// in a day never share a key. Saved in pact_at; 0 on one from before.
	at      int64
	ring    bool  // a round robin, not a chirograph
	minutes int64 // minutes together so far
	signers []signer
	// channel is where it was last put up, which hears when it's done; 0
	// for nowhere. It's saved in its own column, pact_channel, so the
	// pact's text reads the same to a binary from before it.
	channel snowflake.ID
	// started is whether the work has begun: a chirograph's when its second
	// signs, a round robin's when its poster starts it with three or more,
	// or when the fourth signs. Nobody signs after, and minutes count only
	// from then. Saved in pact_started.
	started bool
	// last is the unix minute that last counted, so one room counts once,
	// and rallied when it was last put up in a channel, in memory only.
	last    int64
	rallied time.Time
}

// signer is one signature on a pact and the minutes they were there for.
type signer struct {
	user    snowflake.ID
	minutes int64
}

// size is how many may sign p.
func (p *pact) size() int {
	if p.ring {
		return 4
	}
	return 2
}

// need is how many of p's signers must be together for a minute to count:
// all of a chirograph's, three of a round robin's.
func (p *pact) need() int {
	if p.ring {
		return 3
	}
	return 2
}

// working is whether p has enough signatures to count minutes.
func (p *pact) working() bool { return len(p.signers) >= p.need() }

// startable is whether p's poster may start it now: a round robin with
// three signed, not yet begun.
func (p *pact) startable() bool { return p.ring && !p.started && p.working() }

// signed is whether user has signed p.
func (p *pact) signed(user snowflake.ID) bool {
	for _, s := range p.signers {
		if s.user == user {
			return true
		}
	}
	return false
}

// encodePact is p as a member's row saves it: opener, day, ring, minutes,
// then a user and their minutes per signer, comma separated.
func encodePact(p *pact) string {
	if p == nil {
		return ""
	}
	ring := 0
	if p.ring {
		ring = 1
	}
	parts := []string{p.opener.String(), strconv.FormatInt(p.day, 10), strconv.Itoa(ring), strconv.FormatInt(p.minutes, 10)}
	for _, s := range p.signers {
		parts = append(parts, s.user.String(), strconv.FormatInt(s.minutes, 10))
	}
	return strings.Join(parts, ",")
}

// decodePact reads a saved pact back, nil for none or one that won't read.
func decodePact(s string) *pact {
	f := strings.Split(s, ",")
	if len(f) < 6 || len(f)%2 != 0 {
		return nil
	}
	n := make([]int64, len(f))
	for i := range f {
		v, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil {
			return nil
		}
		n[i] = v
	}
	p := &pact{opener: snowflake.ID(n[0]), day: n[1], ring: n[2] == 1, minutes: n[3]}
	for i := 4; i < len(n); i += 2 {
		p.signers = append(p.signers, signer{snowflake.ID(n[i]), n[i+1]})
	}
	if len(p.signers) > p.size() {
		return nil
	}
	return p
}

// signs is what a member has signed in company, for the lore it teaches:
// chirographs finished, signatures paid and round robins closed with all
// four places filled, the day they were last paid a joinder and the day
// they last posted one, which keeps a notice's key its own.
type signs struct {
	chirographs, signatures, rings, paidDay, postedDay int64
}

func encodeSigns(s signs) string {
	if s == (signs{}) {
		return ""
	}
	return fmt.Sprintf("%d,%d,%d,%d,%d", s.chirographs, s.signatures, s.rings, s.paidDay, s.postedDay)
}

func decodeSigns(s string) signs {
	var out signs
	f := strings.Split(s, ",")
	if len(f) != 5 {
		return out
	}
	v := make([]int64, 5)
	for i := range f {
		v[i], _ = strconv.ParseInt(f[i], 10, 64)
	}
	return signs{v[0], v[1], v[2], v[3], v[4]}
}

// joinPacts points the signers of every saved pact in s at one copy, the
// first read, and drops a copy that doesn't name the member holding it.
// s.mu held.
func joinPacts(s *server) {
	one := map[[3]int64]*pact{}
	for u, m := range s.members {
		if m.pact == nil {
			continue
		}
		if !m.pact.signed(u) {
			m.pact = nil
			continue
		}
		k := [3]int64{int64(m.pact.opener), m.pact.day, m.pact.at}
		if p, ok := one[k]; ok {
			p.channel = max(p.channel, m.pact.channel)
			p.started = p.started || m.pact.started
			m.pact = p
		} else {
			one[k] = m.pact
		}
	}
}

// Why a pact can't be posted or signed.
var (
	errPactHeld   = core.Tell("you've already signed one; finish it or let it lapse first")
	errPactPaid   = core.Tell("you've had today's joinder; another comes at 05:30 utc")
	errPactYoung  = core.Tell("your account is too new to sign in company yet")
	errPactGone   = core.Tell("that notice has lapsed or been finished")
	errPactFull   = core.Tell("every place on that notice is signed")
	errPactYours  = core.Tell("you posted that one; somebody else has to sign it")
	errPactActive = core.Tell("that notice's work has started; no more signatures")
	errPactStart  = core.Tell("only the bird who posted it can start it, once three have signed")
	errPactRally  = core.Tell("it went up a few minutes ago; give the channel a moment")
	errPactPosted = core.Tell("you've posted one today; sign somebody else's, or post again at 05:30 utc")
	errPactNone   = core.Tell("you haven't signed a notice")
)

// mayPact is whether user may post or sign a pact today. s.mu held.
func (st *state) mayPact(m *member, user snowflake.ID, today int64) error {
	switch {
	case !st.bal().old(user, dayStart(today)):
		return errPactYoung
	case m.pact != nil:
		return errPactHeld
	case m.signs.paidDay == today:
		return errPactPaid
	}
	return nil
}

// mayPost is whether user may post a pact today: as mayPact, and one
// posted a day. s.mu held.
func (st *state) mayPost(m *member, user snowflake.ID, today int64) error {
	if err := st.mayPact(m, user, today); err != nil {
		return err
	}
	if m.signs.postedDay == today {
		return errPactPosted
	}
	return nil
}

// post opens a pact by user: a round robin if ring, else a chirograph.
// s.mu held.
func (st *state) post(s *server, guild, user snowflake.ID, ring bool, today int64) (*pact, error) {
	m := st.member(s, guild, user, today)
	if err := st.mayPost(m, user, today); err != nil {
		return nil, err
	}
	// Each post a moment after the bird's last, so two never share a key.
	m.lastPost = max(time.Now().UnixMilli(), m.lastPost+1)
	m.pact = &pact{opener: user, day: today, at: m.lastPost, ring: ring, signers: []signer{{user: user}}}
	m.signs.postedDay, m.dirty = today, true
	s.count(today, statPosted, bit(ring), 0)
	return m.pact, nil
}

// key names p in a button: its opener, the day and the moment it was
// posted, so an old post never signs or starts a newer notice by the same
// bird.
func (p *pact) key() string {
	return p.opener.String() + ":" + strconv.FormatInt(p.day, 10) + ":" + strconv.FormatInt(p.at, 10)
}

// pactKey is a key read back; at is 0 on a post from before keys had it.
type pactKey struct {
	opener  snowflake.ID
	day, at int64
}

// parseKey reads a key back.
func parseKey(k string) (pactKey, bool) {
	f := strings.Split(k, ":")
	if len(f) != 2 && len(f) != 3 {
		return pactKey{}, false
	}
	opener, err := strconv.ParseUint(f[0], 10, 64)
	day, err2 := strconv.ParseInt(f[1], 10, 64)
	var at int64
	var err3 error
	if len(f) == 3 {
		at, err3 = strconv.ParseInt(f[2], 10, 64)
	}
	return pactKey{snowflake.ID(opener), day, at}, err == nil && err2 == nil && err3 == nil
}

// pactOf is the pact k names, nil when it's gone. Its poster may have left
// it to the others, so it's found through whoever holds it. s.mu held.
func pactOf(s *server, k pactKey) *pact {
	names := func(p *pact) bool {
		return p != nil && p.opener == k.opener && p.day == k.day && (k.at == 0 || p.at == k.at)
	}
	if o, ok := s.members[k.opener]; ok && names(o.pact) {
		return o.pact
	}
	for _, m := range s.members {
		if names(m.pact) {
			return m.pact
		}
	}
	return nil
}

// sign puts user's name to the pact k names. The signer is moved into
// today first, which may turn the day and lapse the pact, so it's looked
// up after. s.mu held.
func (st *state) sign(s *server, guild snowflake.ID, k pactKey, user snowflake.ID, today int64) (*pact, error) {
	if user == k.opener {
		return nil, errPactYours
	}
	m := st.member(s, guild, user, today)
	p := pactOf(s, k)
	if p == nil {
		return nil, errPactGone
	}
	if err := st.mayPact(m, user, today); err != nil {
		return nil, err
	}
	switch {
	case len(p.signers) >= p.size():
		return nil, errPactFull
	case p.started:
		return nil, errPactActive
	}
	p.signers = append(p.signers, signer{user: user})
	// A chirograph starts with its second name, a round robin when full.
	p.started = len(p.signers) == p.size()
	m.pact = p
	st.touchPact(s, p)
	return p, nil
}

// freeWithdraws is how many times a day a poster taking their notice down
// before its work gets the day's post back. Past them a bird may still
// withdraw, but the post stays spent.
const freeWithdraws = 2

// withdraw takes user's name off the pact they hold: tearing up their half
// of a chirograph, or striking their name from a round robin. Before the
// work starts, the place opens again and the notice may go straight back
// up to fill it, and its poster withdrawing takes it down for everyone.
// After, one who leaves isn't paid, and a pact left with too few to count
// minutes ends for everyone. s.mu held.
func (st *state) withdraw(s *server, user snowflake.ID, today int64) error {
	m := s.members[user]
	if m == nil || m.pact == nil {
		return errPactNone
	}
	p := m.pact
	if m.withdrawDay != today {
		m.withdrawDay, m.withdrawn = today, 0
	}
	// The poster taking down today's notice before its work gets the post
	// back, freeWithdraws times a day.
	if !p.started && p.opener == user && p.day == today && m.withdrawn < freeWithdraws {
		m.signs.postedDay = 0
		m.withdrawn++
	}
	s.count(today, statLeft, bit(p.ring), bit(p.started))
	p.signers = slices.DeleteFunc(p.signers, func(g signer) bool { return g.user == user })
	m.pact, m.dirty = nil, true
	if !p.started && p.opener == user || p.started && len(p.signers) < p.need() {
		for _, g := range p.signers {
			if o := s.members[g.user]; o != nil {
				o.pact, o.dirty = nil, true
			}
		}
		return nil
	}
	p.rallied = time.Time{}
	st.touchPact(s, p)
	return nil
}

// start begins the work on the round robin user posted. s.mu held.
func (st *state) start(s *server, user snowflake.ID) (*pact, error) {
	m := s.members[user]
	if m == nil || m.pact == nil || m.pact.opener != user || !m.pact.startable() {
		return nil, errPactStart
	}
	m.pact.started = true
	st.touchPact(s, m.pact)
	return m.pact, nil
}

// touchPact marks every signer of p to be saved. s.mu held.
func (st *state) touchPact(s *server, p *pact) {
	for _, g := range p.signers {
		if m, ok := s.members[g.user]; ok {
			m.dirty = true
		}
	}
}

// together counts a minute for every pact with enough of its signers among
// earning, those who earned this minute in one room, and pays any that
// reaches its goal. s.mu held.
func (st *state) together(s *server, earning []snowflake.ID, minute int64) {
	here := map[*pact][]snowflake.ID{}
	for _, u := range earning {
		if m := s.members[u]; m != nil && m.pact != nil && m.pact.started && m.pact.working() {
			here[m.pact] = append(here[m.pact], u)
		}
	}
	for p, us := range here {
		if len(us) < p.need() || p.last == minute {
			continue
		}
		p.last = minute
		p.minutes++
		for i := range p.signers {
			for _, u := range us {
				if p.signers[i].user == u {
					p.signers[i].minutes++
				}
			}
		}
		st.touchPact(s, p)
		if p.minutes >= st.bal().PactGoal {
			st.finish(s, p, DayOf(time.Unix(minute*60, 0)))
		}
	}
}

// finish pays p's joinder today to each signer there for at least half
// its minutes, at the rate for how many were, and frees every signer.
// s.mu held.
func (st *state) finish(s *server, p *pact, today int64) {
	var paid []snowflake.ID
	for _, g := range p.signers {
		if g.minutes*2 >= p.minutes {
			paid = append(paid, g.user)
		}
	}
	got := int64(0)
	if n := len(paid); n >= 2 {
		got = st.bal().Joinder[min(n, 4)-2]
	}
	news := joinderNews{channel: p.channel, ring: p.ring, got: got}
	for _, g := range p.signers {
		if _, ok := s.members[g.user]; !ok {
			continue
		}
		// One who left before the end may still be in a day gone by.
		m := st.member(s, s.id, g.user, today)
		m.pact, m.dirty = nil, true
		if got == 0 || !slices.Contains(paid, g.user) {
			continue
		}
		knew := learned(m.signs)
		m.signs.signatures++
		if !p.ring {
			m.signs.chirographs++
		} else if len(paid) == 4 {
			m.signs.rings++
		}
		m.signs.paidDay = m.dayN
		lv, kept := st.labour(s, m, got)
		s.append(g.user, m.dayN, reasonJoinder, got-lv-kept, []int64{int64(len(paid)), got, lv, kept})
		news.paid = append(news.paid, g.user)
		if learned(m.signs) != knew {
			news.learned = append(news.learned, g.user)
		}
	}
	s.count(today, statJoined, bit(p.ring), len(news.paid))
	if news.channel != 0 && len(news.paid) > 0 {
		s.joinders = append(s.joinders, news)
	}
}

// joinderNews is a finished pact to tell the channel it went up in: who
// was paid, what each got and who learned something of their own kind.
type joinderNews struct {
	channel       snowflake.ID
	ring          bool
	got           int64
	paid, learned []snowflake.ID
}

// joinderText is n as its channel reads it.
func joinderText(n joinderNews) string {
	head := "the ravens fit the two halves together, and the teeth match"
	if n.ring {
		head = "the ring closes, and the ravens enter it without saying who led"
	}
	lines := []string{head, fmt.Sprintf("**joinder** %s pp each to %s", grouped(n.got/unit), mentions(n.paid))}
	if len(n.learned) > 0 {
		lines = append(lines, fmt.Sprintf("**learned** %s: a new page on their lore card", mentions(n.learned)))
	}
	return strings.Join(lines, "\n")
}

// mentions is us as a list a channel reads.
func mentions(us []snowflake.ID) string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = "<@" + u.String() + ">"
	}
	return andList(out)
}

// andList is xs as a reader says them: "a", "a and b", "a, b and c".
func andList(xs []string) string {
	if len(xs) < 2 {
		return strings.Join(xs, "")
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// lapse drops every pact posted PactLife days or more before today. s.mu
// held.
func (st *state) lapse(s *server, today int64) {
	gone := map[*pact]bool{}
	for _, m := range s.members {
		if m.pact != nil && today-m.pact.day >= st.bal().PactLife {
			if !gone[m.pact] {
				gone[m.pact] = true
				s.count(today, statLapsed, bit(m.pact.ring), len(m.pact.signers))
			}
			m.pact, m.dirty = nil, true
		}
	}
}
