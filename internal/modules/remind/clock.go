package remind

import (
	"container/heap"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

// How a reminder gets out on time: every pending row lives in Postgres,
// and the ones due within horizon sit in memory in a heap, soonest first,
// under one timer armed for the top. The heap is refilled from the table
// every refill and pushed to straight from an add or an edit inside the
// horizon. When the timer fires, each row is claimed in the table first
// (claim), so a deploy's two processes or an edit racing the fire never
// send it twice.
//
// The timer is armed lead early. lead is learned from what Discord says:
// a message's snowflake is Discord's clock at the moment it was made, so
// snowflake minus the moment the send began is the round trip plus any
// offset between the two clocks, and sending that much early lands the
// message on the due millisecond by the clock everyone reads it by.
const (
	horizon   = 10 * time.Minute
	refill    = time.Minute
	firstLead = 120 * time.Millisecond
	maxLead   = 2 * time.Second
	// lateBy is how late a fire can be and still count towards how on
	// time skua is: past it, she was away, and it says so instead.
	lateBy = 5 * time.Second
	// skipAfter is how late a repeat can be before its turn is skipped
	// rather than sent: a daily 09:00 is no use at 15:00.
	skipAfter = time.Hour
	retryIn   = 30 * time.Second
	retries   = 3
)

// queue is a min-heap of reminders by due time.
type queue []reminder

func (q queue) Len() int           { return len(q) }
func (q queue) Less(i, j int) bool { return q[i].due.Before(q[j].due) }
func (q queue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *queue) Push(x any)        { *q = append(*q, x.(reminder)) }
func (q *queue) Pop() any {
	old := *q
	r := old[len(old)-1]
	*q = old[:len(old)-1]
	return r
}

// hold queues r if it is due within the horizon. held is each row's due
// time as last queued, so a row edited since reads as stale when its old
// entry comes up.
func (m *Module) hold(r reminder) {
	if r.state != "pending" || !r.due.Before(m.now().Add(horizon)) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.held[r.id]; ok && d.Equal(r.due) {
		return
	}
	m.held[r.id] = r.due
	heap.Push(&m.q, r)
	m.armLocked()
	if r.target == "dm" {
		go func() {
			// A miss here is tried again when it sends.
			if _, err := m.warm(r.owner); err != nil {
				m.log.Debug("remind: opening a dm early", "err", err)
			}
		}()
	}
}

// armLocked sets the one timer for the top of the heap, lead early.
func (m *Module) armLocked() {
	if m.timer != nil {
		m.timer.Stop()
	}
	if len(m.q) == 0 {
		m.timer = nil
		return
	}
	m.timer = m.after(max(m.q[0].due.Sub(m.now())-m.lead(), 0), m.wake)
}

// wake fires everything the timer is for.
func (m *Module) wake() {
	m.mu.Lock()
	now, lead := m.now(), m.lead()
	var due []reminder
	for len(m.q) > 0 && !m.q[0].due.After(now.Add(lead)) {
		r := heap.Pop(&m.q).(reminder)
		if d, ok := m.held[r.id]; ok && d.Equal(r.due) {
			delete(m.held, r.id)
			due = append(due, r)
		}
	}
	m.armLocked()
	m.mu.Unlock()
	for _, r := range due {
		go m.fire(context.Background(), r)
	}
}

// load queues everything due within the horizon, the overdue first.
func (m *Module) load(ctx context.Context) error {
	rs, err := dueBy(ctx, m.db, m.now().Add(horizon))
	if err != nil {
		return err
	}
	for _, r := range rs {
		m.hold(r)
	}
	counts, err := pendingByGuild(ctx, m.db)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.pending = counts
	m.mu.Unlock()
	return nil
}

// run keeps the heap filled for the life of the process.
func (m *Module) run(ctx context.Context) {
	t := time.NewTicker(refill)
	defer t.Stop()
	for {
		if err := tidyUp(ctx, m.db, m.now()); err != nil {
			m.log.Warn("remind: tidying", "err", err)
		}
		if err := m.load(ctx); err != nil {
			m.log.Warn("remind: loading what's due", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// fire sends r, once it has claimed it.
func (m *Module) fire(ctx context.Context, r reminder) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	late := m.now().Sub(r.due)
	if !r.r.once() && late > skipAfter {
		// Its turn passed while skua was away; the next one stands.
		if _, err := claim(ctx, m.db, r, r.r.next(m.now(), r.loc())); err != nil {
			m.log.Warn("remind: skipping a missed repeat", "id", r.id, "err", err)
		}
		return
	}
	next := time.Time{}
	if !r.r.once() {
		next = r.r.next(r.due, r.loc())
	}
	ok, err := claim(ctx, m.db, r, next)
	if err != nil {
		m.log.Warn("remind: claiming", "id", r.id, "err", err)
	}
	if !ok {
		return
	}
	if !next.IsZero() {
		m.hold(reminder{id: r.id, guild: r.guild, owner: r.owner, channel: r.channel, target: r.target, targetID: r.targetID, text: r.text, due: next, r: r.r, tz: r.tz, state: "pending", created: r.created})
	}
	at, err := m.send(ctx, r, late)
	if err == nil {
		if late < lateBy {
			m.learn(r.guild, at.Sub(r.due))
		}
		if r.r.once() {
			err = settle(ctx, m.db, r.id, time.Time{})
		}
		if err != nil {
			m.log.Warn("remind: marking sent", "id", r.id, "err", err)
		}
		return
	}
	m.log.Warn("remind: sending", "id", r.id, "err", err)
	if r.r.once() && refused(err) {
		if err := settle(ctx, m.db, r.id, time.Time{}); err != nil {
			m.log.Warn("remind: dropping a refused send", "id", r.id, "err", err)
		}
		return
	}
	if !r.r.once() {
		return // ponytail: a repeat's failed turn is dropped; the next one comes
	}
	m.mu.Lock()
	m.tries[r.id]++
	n := m.tries[r.id]
	if n >= retries {
		delete(m.tries, r.id)
	}
	m.mu.Unlock()
	retry := m.now().Add(retryIn)
	if n >= retries {
		retry = time.Time{}
	}
	if err := settle(ctx, m.db, r.id, retry); err != nil {
		m.log.Warn("remind: putting back", "id", r.id, "err", err)
		return
	}
	if !retry.IsZero() {
		r.due, r.state = retry, "pending"
		m.hold(r)
	}
}

// send posts r where it goes and is when Discord made the message. A dm
// that can't be delivered goes to the channel it was set in, pinging its
// owner there.
func (m *Module) send(ctx context.Context, r reminder, late time.Duration) (time.Time, error) {
	p := m.poster()
	if p == nil {
		return time.Time{}, errors.New("remind: not connected yet")
	}
	if err := m.guard.Allow(r.guild, guard.MessageSend); err != nil {
		return time.Time{}, err
	}
	to, inDM := r.channel, r.target == "dm"
	if r.target == "channel" && r.targetID != 0 {
		to = r.targetID
	}
	if inDM {
		ch, err := m.warm(r.owner)
		if err == nil {
			var msg *discord.Message
			msg, err = p.CreateMessage(ch, fired(r, late, false), rest.WithCtx(ctx))
			m.guard.Done(r.guild, err)
			if err == nil {
				return msg.ID.Time(), nil
			}
		}
		if !closed(err) {
			return time.Time{}, err
		}
		m.mu.Lock()
		m.fellBack[r.guild]++
		m.mu.Unlock()
		if err := m.guard.Allow(r.guild, guard.MessageSend); err != nil {
			return time.Time{}, err
		}
	}
	msg, err := p.CreateMessage(to, fired(r, late, true), rest.WithCtx(ctx))
	m.guard.Done(r.guild, err)
	if err != nil {
		return time.Time{}, err
	}
	return msg.ID.Time(), nil
}

// refused is whether err is Discord saying no in a way trying again won't
// change: a 4xx other than a rate limit, such as a deleted channel or
// skua's access to it taken away.
func refused(err error) bool {
	var re *rest.Error
	if !errors.As(err, &re) || re.Response == nil {
		return false
	}
	c := re.Response.StatusCode
	return c >= 400 && c < 500 && c != 429
}

// closed is whether err is Discord refusing a dm: the member turned them
// off, or shares no server with skua any more.
func closed(err error) bool {
	var re *rest.Error
	return errors.As(err, &re) && (re.Code == rest.JSONErrorCodeCannotSendMessagesToThisUser || re.Code == rest.JSONErrorCodeCannotSendMessagesToThisUserDueToHavingNoMutualGuilds)
}

// warm is user's dm channel, opened and kept ahead of time so a fire is
// one round trip.
func (m *Module) warm(user snowflake.ID) (snowflake.ID, error) {
	m.mu.Lock()
	ch, ok := m.dms[user]
	m.mu.Unlock()
	if ok {
		return ch, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	who, err := personOf(ctx, m.db, user)
	if err != nil {
		return 0, err
	}
	ch = who.dmChannel
	if ch == 0 {
		p := m.poster()
		if p == nil {
			return 0, errors.New("remind: not connected yet")
		}
		dm, err := p.CreateDMChannel(user, rest.WithCtx(ctx))
		if err != nil {
			return 0, err
		}
		ch = dm.ID()
		if err := saveDM(ctx, m.db, user, ch); err != nil {
			m.log.Warn("remind: keeping a dm channel", "err", err)
		}
	}
	m.mu.Lock()
	m.dms[user] = ch
	m.mu.Unlock()
	return ch, nil
}

// lead is how early to send.
func (m *Module) lead() time.Duration { return time.Duration(m.leadNS.Load()) }

// learn takes one on-time send that landed off its due time by
// Discord's clock: lead moves an eighth of it, so whatever stands between
// the timer and Discord (the claim, the round trip, the clocks' offset)
// is counted. off is kept for the server's report.
func (m *Module) learn(guild snowflake.ID, off time.Duration) {
	m.leadNS.Store(int64(min(max(m.lead()+off/8, 0), maxLead)))
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.offs[guild]
	if len(s) == offsKept {
		s = s[1:]
	}
	m.offs[guild] = append(s, off)
}

const offsKept = 64

// offsets is guild's last on-time sends' p50 and p99 distance from due,
// absolute, and how many there were.
func (m *Module) offsets(guild snowflake.ID) (p50, p99 time.Duration, n int) {
	m.mu.Lock()
	s := slices.Clone(m.offs[guild])
	m.mu.Unlock()
	if len(s) == 0 {
		return 0, 0, 0
	}
	for i, d := range s {
		s[i] = d.Abs()
	}
	slices.Sort(s)
	return s[len(s)/2], s[min(len(s)-1, len(s)*99/100)], len(s)
}

// poster is the REST skua sends with, once she is up.
func (m *Module) poster() Poster {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rest
}

// Poster is the REST remind sends with.
type Poster interface {
	CreateMessage(channel snowflake.ID, m discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error)
	CreateDMChannel(user snowflake.ID, opts ...rest.RequestOpt) (*discord.DMChannel, error)
}
