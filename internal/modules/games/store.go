package games

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/6586x57890143/skua/internal/game"
	"github.com/6586x57890143/skua/internal/game/count"
)

const (
	// flushEvery is how often what was played is written.
	flushEvery = 250 * time.Millisecond
	// flushLast bounds the write at shutdown.
	flushLast = 5 * time.Second
)

// journal is what the next write adds to the history: the counts that
// stood, each member's counts this month and the runs that ended.
type journal struct {
	logs    []logRow
	seasons map[seasonKey]int64
	runs    []runRow
}

type logRow struct {
	ch, user, msg uint64
	run, n        int64
}

type seasonKey struct {
	guild  uint64
	period string
	user   uint64
}

type runRow struct {
	ch, guild, by      uint64
	run, start, length int64
	head               string
	at                 time.Time
}

func (j *journal) add(guild uint64, period string, user uint64) {
	if j.seasons == nil {
		j.seasons = map[seasonKey]int64{}
	}
	j.seasons[seasonKey{guild, period, user}]++
}

// take empties the journal into the caller's hands.
func (m *Module) take() journal {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.journal
	m.journal = journal{}
	return j
}

// putBack returns a journal whose write failed, ahead of anything newer.
func (m *Module) putBack(j journal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.journal.logs = append(j.logs, m.journal.logs...)
	m.journal.runs = append(j.runs, m.journal.runs...)
	for k, n := range j.seasons {
		if m.journal.seasons == nil {
			m.journal.seasons = map[seasonKey]int64{}
		}
		m.journal.seasons[k] += n
	}
}

// monthOf is the season a time falls in.
func monthOf(t time.Time) (time.Time, time.Time, string) { return game.Month(t) }

// load reads every counting channel and member into the engine.
func (m *Module) load(ctx context.Context) error {
	rows, _ := m.db.Query(ctx, "select channel_id, state from count_channel")
	var ch int64
	var raw []byte
	if _, err := pgx.ForEachRow(rows, []any{&ch, &raw}, func() error {
		var s count.State
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("channel %d: %w", ch, err)
		}
		m.en.Restore(uint64(ch), s)
		return nil
	}); err != nil {
		return fmt.Errorf("games: reading the counting channels: %w", err)
	}
	rows, _ = m.db.Query(ctx, "select guild_id, user_id, state from count_member")
	var guild, user int64
	if _, err := pgx.ForEachRow(rows, []any{&guild, &user, &raw}, func() error {
		var mem count.Member
		if err := json.Unmarshal(raw, &mem); err != nil {
			return fmt.Errorf("member %d: %w", user, err)
		}
		m.en.RestoreMember(count.Who{Guild: uint64(guild), User: uint64(user)}, mem)
		return nil
	}); err != nil {
		return fmt.Errorf("games: reading the counters: %w", err)
	}
	return nil
}

// lastRun is the highest run a channel kept history for, so one counting
// again after it was dropped never reuses a run's number.
func (m *Module) lastRun(ctx context.Context, ch uint64) (int64, error) {
	if m.db == nil {
		return 0, nil
	}
	rows, _ := m.db.Query(ctx, `select greatest(
		(select coalesce(max(run), 0) from count_log where channel_id = $1),
		(select coalesce(max(run), 0) from count_run where channel_id = $1))`, int64(ch))
	last, err := pgx.CollectExactlyOneRow(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, fmt.Errorf("games: reading a channel's runs: %w", err)
	}
	return last, nil
}

// saveChannel writes one channel now, for a change an admin made.
func (m *Module) saveChannel(ctx context.Context, ch uint64) error {
	if m.db == nil {
		return nil
	}
	s, ok := m.en.Channel(ch)
	if !ok {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err := m.db.Exec(ctx, `insert into count_channel (channel_id, guild_id, state) values ($1, $2, $3)
		on conflict (channel_id) do update set state = excluded.state`, int64(ch), int64(s.Guild), raw); err != nil {
		return fmt.Errorf("games: saving a counting channel: %w", err)
	}
	return nil
}

// Run writes what was played every flushEvery until ctx ends, then once
// more. Without a database it only waits.
func (m *Module) Run(ctx context.Context) {
	if m.db == nil {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			end, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushLast)
			if err := m.flush(end); err != nil {
				m.log.Error("games: the last write failed; the latest counts are lost", "err", err)
			}
			cancel()
			return
		case <-t.C:
			if ctx.Err() != nil {
				continue // the end's write, next turn, takes it
			}
			if err := m.flush(ctx); err != nil {
				m.log.Warn("games: writing what was played; trying again", "err", err)
			}
		}
	}
}

// flush writes every changed channel and member and the journal in one
// transaction. On failure everything is kept for the next try.
func (m *Module) flush(ctx context.Context) (err error) {
	chans, members, j := m.chans.Take(), m.members.Take(), m.take()
	if len(chans) == 0 && len(members) == 0 && len(j.logs) == 0 && len(j.runs) == 0 && len(j.seasons) == 0 {
		return nil
	}
	defer func() {
		if err != nil {
			for _, c := range chans {
				m.chans.Mark(c)
			}
			for _, w := range members {
				m.members.Mark(w)
			}
			m.putBack(j)
		}
	}()
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	b := &pgx.Batch{}
	for _, c := range chans {
		s, ok := m.en.Channel(c)
		if !ok {
			continue // dropped since; its row went with it
		}
		raw, err := json.Marshal(s)
		if err != nil {
			return err
		}
		// Only a channel still counting is written: one an admin just
		// dropped stays dropped.
		b.Queue(`update count_channel set state = $2 where channel_id = $1`, int64(c), raw)
	}
	for _, w := range members {
		mem := m.en.Member(w)
		raw, err := json.Marshal(mem)
		if err != nil {
			return err
		}
		b.Queue(`insert into count_member (guild_id, user_id, counts, xp, state) values ($1, $2, $3, $4, $5)
			on conflict (guild_id, user_id) do update set counts = excluded.counts, xp = excluded.xp, state = excluded.state`,
			int64(w.Guild), int64(w.User), mem.Counts, mem.XP, raw)
	}
	if len(j.logs) > 0 {
		var ch, run, n, user, msg []int64
		for _, l := range j.logs {
			ch, run, n, user, msg = append(ch, int64(l.ch)), append(run, l.run), append(n, l.n), append(user, int64(l.user)), append(msg, int64(l.msg))
		}
		b.Queue(`insert into count_log (channel_id, run, n, user_id, message_id)
			select * from unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::bigint[], $5::bigint[])
			on conflict do nothing`, ch, run, n, user, msg)
	}
	for k, n := range j.seasons {
		b.Queue(`insert into count_season (guild_id, period, user_id, counts) values ($1, $2, $3, $4)
			on conflict (guild_id, period, user_id) do update set counts = count_season.counts + excluded.counts`,
			int64(k.guild), k.period, int64(k.user), n)
	}
	for _, r := range j.runs {
		b.Queue(`insert into count_run (channel_id, run, guild_id, start, length, head, broke_by, ended)
			values ($1, $2, $3, $4, $5, $6, $7, $8) on conflict do nothing`,
			int64(r.ch), r.run, int64(r.guild), r.start, r.length, r.head, int64(r.by), r.at)
	}
	if err := tx.SendBatch(ctx, b).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// markdown escapes what a member typed, so a card shows it as typed.
var markdown = strings.NewReplacer(`\`, `\\`, `_`, `\_`, `*`, `\*`, `~`, `\~`, "`", "\\`", `|`, `\|`, `>`, `\>`, `[`, `\[`, `]`, `\]`, `<`, `\<`)
