package purge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"

	"github.com/6586x57890143/skua/internal/core"
)

// scheduleTick is how often skua looks for sweeps that are due.
const scheduleTick = time.Minute

// everyChoices are the schedules /purge every offers. Each sweep after the
// first reads only what is new since the one before, so even 6h is cheap.
var everyChoices = []delay{
	{"off", 0}, {"6h", 6 * time.Hour}, {"12h", 12 * time.Hour}, {"1d", 24 * time.Hour}, {"7d", 7 * 24 * time.Hour},
}

var errNotAChoice = core.Tell("that isn't one of the choices; pick one from the list")

// start runs once, on the first Ready: it reads who is live, queues a
// catch-up sweep for each of them (whatever they posted while skua was down
// is still up), and starts the scheduler.
func (m *Module) start(r rest.Rest) {
	if m.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := m.loadLive(ctx); err != nil {
		m.log.Warn("purge: loading live members", "err", err)
	}
	if _, err := m.db.Exec(ctx, `update purge_subs set next_run = now()
		where live_delay_s is not null and (next_run is null or next_run > now())`); err != nil {
		m.log.Warn("purge: queueing catch-up sweeps", "err", err)
	}
	cancel()
	go func() {
		for range time.Tick(m.schedTick) {
			m.due(r)
		}
	}()
}

// due claims every subscription whose sweep is due, moving its next run on
// in the same statement, and starts one job per guild for all of them: one
// catch-up of the guild serves every member in it. A member whose own
// purge is still running here goes back in the queue, as does a guild
// whose last scheduled job is still going: two purges for one member would
// both write back the same index blocks.
//
// ponytail: every due guild sweeps at once, all sharing the pacer. Cap
// concurrent guilds if thousands come due in the same minute.
func (m *Module) due(r rest.Rest) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := m.db.Query(ctx, `update purge_subs
		set next_run = case when every_s is null then null else now() + every_s * interval '1 second' end
		where next_run <= now()
		returning guild_id, user_id`)
	if err != nil {
		m.log.Warn("purge: claiming due sweeps", "err", err)
		return
	}
	byGuild := map[snowflake.ID][]snowflake.ID{}
	for rows.Next() {
		var g, u int64
		if err := rows.Scan(&g, &u); err != nil {
			rows.Close()
			m.log.Warn("purge: claiming due sweeps", "err", err)
			return
		}
		byGuild[snowflake.ID(g)] = append(byGuild[snowflake.ID(g)], snowflake.ID(u))
	}
	rows.Close()
	requeue := func(g snowflake.ID, users []snowflake.ID) {
		ids := make([]int64, len(users))
		for i, u := range users {
			ids[i] = int64(u)
		}
		if _, err := m.db.Exec(ctx, `update purge_subs set next_run = now() where guild_id = $1 and user_id = any($2)`, int64(g), ids); err != nil {
			m.log.Warn("purge: requeueing", "guild", g, "err", err)
		}
	}
	for g, users := range byGuild {
		if !m.on(g) {
			continue // turned off here: this run is skipped, not queued
		}
		if _, busy := m.sweeping.LoadOrStore(g, struct{}{}); busy {
			requeue(g, users) // its last scheduled job is still going
			continue
		}
		jctx, jcancel := context.WithCancel(context.Background())
		j := m.newJob(r, g, nil)
		var mine, busy []snowflake.ID
		for _, u := range users {
			if _, loaded := m.running.LoadOrStore(target{g, u}, &run{jcancel, j, true}); loaded {
				busy = append(busy, u)
			} else {
				mine = append(mine, u)
			}
		}
		if len(busy) > 0 {
			requeue(g, busy)
		}
		if len(mine) == 0 {
			jcancel()
			m.sweeping.Delete(g)
			continue
		}
		for _, u := range mine {
			j.sweep.authors[u] = new(atomic.Int64)
		}
		go func() {
			defer m.sweeping.Delete(g)
			defer jcancel()
			j.err = m.work(jctx, j)
			for _, u := range mine {
				m.running.Delete(target{g, u})
			}
			m.record(j, j.err)
			close(j.done)
		}()
	}
}

// record writes how a job went for each of its authors, each seeing only
// their own count.
func (m *Module) record(j *job, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ok := err == nil
	ids := j.unreachable()
	unreachable := make([]int64, len(ids))
	for i, id := range ids {
		unreachable[i] = int64(id)
	}
	s := j.sweep
	for user, n := range s.authors {
		_, err := m.db.Exec(ctx, `insert into purge_subs (guild_id, user_id, last_deleted, unreachable, last_ok, last_finished)
			values ($1, $2, $3, $4, $5, now())
			on conflict (guild_id, user_id) do update set
				last_deleted = excluded.last_deleted, unreachable = excluded.unreachable,
				last_ok = excluded.last_ok, last_finished = excluded.last_finished`,
			int64(s.guild), int64(user), int32(n.Load()), unreachable, ok)
		if err != nil {
			m.log.Warn("purge: recording a sweep", "guild", s.guild, "user", user, "err", err)
		}
	}
}

func (m *Module) setEveryCmd(ctx context.Context, e *events.ApplicationCommandInteractionCreate, k target, every string) error {
	i := slices.IndexFunc(everyChoices, func(d delay) bool { return d.name == every })
	if i < 0 {
		return errNotAChoice
	}
	if m.db == nil {
		return errNoDB
	}
	var secs *int32
	if d := everyChoices[i].d; d > 0 {
		s := int32(d / time.Second)
		secs = &s
	}
	_, err := m.db.Exec(ctx, `insert into purge_subs (guild_id, user_id, every_s, next_run)
		values ($1, $2, $3::int, case when $3::int is null then null else now() end)
		on conflict (guild_id, user_id) do update set every_s = excluded.every_s, next_run = excluded.next_run`,
		int64(k.guild), int64(k.user), secs)
	if err != nil {
		return err
	}
	if secs == nil {
		return reply(e, "✓ no more scheduled sweeps here")
	}
	return reply(e, "✓ every "+every+": the first sweep starts within a minute")
}

// statusCmd is the member's purge setup here and how their last sweep went.
func (m *Module) statusCmd(ctx context.Context, e *events.ApplicationCommandInteractionCreate, k target) error {
	if m.db == nil {
		return errNoDB
	}
	var live, every, deleted *int32
	var next, finished *time.Time
	var ok *bool
	var unreachable []int64
	err := m.db.QueryRow(ctx, `select live_delay_s, every_s, next_run, last_finished, last_deleted, last_ok, unreachable
		from purge_subs where guild_id = $1 and user_id = $2`, int64(k.guild), int64(k.user)).
		Scan(&live, &every, &next, &finished, &deleted, &ok, &unreachable)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	now := m.now()
	last, count := "never", "none yet"
	if finished != nil {
		last = span(now.Sub(*finished)) + " ago"
		if ok != nil && !*ok {
			last += ", stopped"
		}
	}
	if deleted != nil {
		count = fmt.Sprint(*deleted)
	}
	upcoming := "none"
	switch {
	case next != nil && next.After(now):
		upcoming = "in " + span(next.Sub(now))
	case next != nil:
		upcoming = "within a minute"
	}
	text := grid([][2]string{
		{"live", choiceName(delays, live)},
		{"every", choiceName(everyChoices, every)},
		{"last sweep", last},
		{"deleted", count},
		{"next sweep", upcoming},
		{"unreachable", fmt.Sprint(len(unreachable))},
	})
	if v, running := m.running.Load(k); running {
		what := "a /purge now"
		if v.(*run).scheduled {
			what = "a scheduled sweep"
		}
		text += "\n-# " + what + " is running for you here"
	} else if _, running := m.sweeping.Load(k.guild); running {
		text += "\n-# a sweep is running in this server"
	}
	ids := make([]snowflake.ID, len(unreachable))
	for i, id := range unreachable {
		ids[i] = snowflake.ID(id)
	}
	return reply(e, text+couldnt(ids))
}

// choiceName is how a stored number of seconds reads: its choice's name,
// or "off".
func choiceName(list []delay, secs *int32) string {
	if secs == nil {
		return "off"
	}
	d := time.Duration(*secs) * time.Second
	if i := slices.IndexFunc(list, func(c delay) bool { return c.d == d }); i >= 0 {
		return list[i].name
	}
	return span(d)
}
