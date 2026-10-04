package purge

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// batchWindow is how long past its time a live delete may wait for the
// ones due right after it: a burst posted within it goes in one bulk call.
const batchWindow = 2 * time.Second

// removeBy bounds one live flush's deletes.
const removeBy = 30 * time.Second

// delays are the choices /purge live offers. All stay well under 14 days,
// so every live delete can go in a bulk call.
type delay struct {
	name string
	d    time.Duration
}

var delays = []delay{
	{"off", 0}, {"10s", 10 * time.Second}, {"1m", time.Minute}, {"10m", 10 * time.Minute}, {"1h", time.Hour},
}

func delayChoices() []discord.ApplicationCommandOptionChoiceString {
	var c []discord.ApplicationCommandOptionChoiceString
	for _, d := range delays {
		c = append(c, discord.ApplicationCommandOptionChoiceString{Name: d.name, Value: d.name})
	}
	return c
}

// due is one message and when it goes.
type due struct {
	id snowflake.ID
	at time.Time
}

// queue is one channel's messages waiting to go. Its timer fires a window
// after the earliest of them is due.
type queue struct {
	r     rest.Rest
	guild snowflake.ID
	items []due
	next  time.Time
	timer *time.Timer
}

// OnEvent loads the live set once the gateway is up, and queues each
// message a live member posts. A message from anyone else costs one atomic
// load, or a map lookup while anyone is live, and nothing else.
func (m *Module) OnEvent(ev bot.Event) {
	switch e := ev.(type) {
	case *events.Ready:
		m.boot.Do(func() { go m.start() })
	case *events.GuildMessageCreate:
		if m.liveN.Load() == 0 {
			return
		}
		v, ok := m.live.Load(target{e.GuildID, e.Message.Author.ID})
		if !ok {
			return
		}
		m.enqueue(e.Client().Rest, e.GuildID, e.Message.ChannelID, e.Message.ID, m.now().Add(v.(time.Duration)))
	}
}

// start reads who is live. Anything they post before it finishes stays up
// until their next sweep.
func (m *Module) start() {
	if m.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.loadLive(ctx); err != nil {
		m.log.Warn("purge: loading live members", "err", err)
	}
}

func (m *Module) setLive(k target, d time.Duration) {
	if d == 0 {
		if _, ok := m.live.LoadAndDelete(k); ok {
			m.liveN.Add(-1)
		}
		return
	}
	if _, loaded := m.live.Swap(k, d); !loaded {
		m.liveN.Add(1)
	}
}

func (m *Module) setLiveCmd(ctx context.Context, e *events.ApplicationCommandInteractionCreate, k target, after string) error {
	i := slices.IndexFunc(delays, func(d delay) bool { return d.name == after })
	if i < 0 {
		return core.Tell("that isn't one of the choices; pick one from the list")
	}
	if m.db == nil {
		return errNoDB
	}
	d := delays[i].d
	if err := m.saveLive(ctx, k, d); err != nil {
		return err
	}
	m.setLive(k, d)
	if d == 0 {
		return reply(e, "✓ live is off; your messages here stay up")
	}
	return reply(e, "✓ live: each message you send here goes "+after+" after you send it")
}

func (m *Module) enqueue(r rest.Rest, guild, ch, id snowflake.ID, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queues[ch]
	if q == nil {
		q = &queue{r: r, guild: guild}
		m.queues[ch] = q
	}
	q.items = append(q.items, due{id, at})
	switch {
	case q.timer == nil:
		q.next = at
		q.timer = time.AfterFunc(at.Sub(m.now())+m.window, func() { m.flush(ch) })
	case at.Before(q.next):
		// A shorter delay than anything queued: bring the timer forward.
		q.next = at
		q.timer.Reset(at.Sub(m.now()) + m.window)
	}
}

// flush deletes everything in ch that is due and sets the timer for the
// rest.
func (m *Module) flush(ch snowflake.ID) {
	m.mu.Lock()
	q := m.queues[ch]
	if q == nil {
		m.mu.Unlock()
		return
	}
	now := m.now()
	var ready []snowflake.ID
	keep := q.items[:0]
	q.next = time.Time{}
	for _, it := range q.items {
		if !it.at.After(now) {
			ready = append(ready, it.id)
			continue
		}
		keep = append(keep, it)
		if q.next.IsZero() || it.at.Before(q.next) {
			q.next = it.at
		}
	}
	q.items = keep
	if len(keep) == 0 {
		delete(m.queues, ch)
	} else {
		q.timer.Reset(q.next.Sub(now) + m.window)
	}
	r, guild := q.r, q.guild
	m.mu.Unlock()
	if len(ready) > 0 {
		m.remove(r, guild, ch, ready)
	}
}

// remove deletes ids in bulk calls of up to 100, through the same
// bulk-then-singles path a sweep takes, minus the pacer: live deletes are
// few and go first.
//
// ponytail: a delete the guard refuses is dropped, not retried; the
// member's next sweep catches it.
func (m *Module) remove(r rest.Rest, guild, ch snowflake.ID, ids []snowflake.ID) {
	ctx, cancel := context.WithTimeout(context.Background(), removeBy)
	defer cancel()
	s := &sweep{r: r, guard: m.guard, guild: guild}
	for chunk := range slices.Chunk(ids, bulkMax) {
		err := s.bulk(ctx, ch, chunk)
		if errors.Is(err, errChannelDone) {
			return
		}
		if err != nil {
			m.log.Warn("purge: live delete", "guild", guild, "channel", ch, "err", err)
			return
		}
	}
}

func (m *Module) saveLive(ctx context.Context, k target, d time.Duration) error {
	var secs *int32
	if d > 0 {
		s := int32(d / time.Second)
		secs = &s
	}
	_, err := m.db.Exec(ctx, `insert into purge_subs (guild_id, user_id, live_delay_s) values ($1, $2, $3)
		on conflict (guild_id, user_id) do update set live_delay_s = excluded.live_delay_s`,
		int64(k.guild), int64(k.user), secs)
	return err
}

func (m *Module) loadLive(ctx context.Context) error {
	rows, err := m.db.Query(ctx, `select guild_id, user_id, live_delay_s from purge_subs where live_delay_s is not null`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var g, u int64
		var secs int32
		if err := rows.Scan(&g, &u, &secs); err != nil {
			return err
		}
		m.setLive(target{snowflake.ID(g), snowflake.ID(u)}, time.Duration(secs)*time.Second)
	}
	return rows.Err()
}
