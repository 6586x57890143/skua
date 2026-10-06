// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"context"
	"sync"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// row ties one Discord message to one rumor.
type row struct {
	Message, Channel, Webhook snowflake.ID
	Rumor                     string
	Origin                    string // "discord" or "armada"
	Author                    string // a Discord user id, or a pubkey
	Part                      int
}

// mappings is where rows live: Postgres when skua has one, memory when not.
type mappings interface {
	insert(ctx context.Context, r row) error
	byMessage(ctx context.Context, id snowflake.ID) ([]row, error)
	// byRumor is scoped to one Discord channel, so a link only ever
	// resolves inside itself.
	byRumor(ctx context.Context, rumor string, channel snowflake.ID) ([]row, error)
}

// DB is the slice of pgxpool.Pool the bridge uses.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type pgMappings struct{ db DB }

func (p pgMappings) insert(ctx context.Context, r row) error {
	var hook *int64
	if r.Webhook != 0 {
		h := int64(r.Webhook)
		hook = &h
	}
	_, err := p.db.Exec(ctx, `insert into armada_messages
		(discord_message_id, discord_channel_id, webhook_id, rumor_id, origin, author, part)
		values ($1, $2, $3, $4, $5, $6, $7) on conflict do nothing`,
		int64(r.Message), int64(r.Channel), hook, r.Rumor, r.Origin, r.Author, r.Part)
	return err
}

func (p pgMappings) byMessage(ctx context.Context, id snowflake.ID) ([]row, error) {
	return p.query(ctx, `where discord_message_id = $1`, int64(id))
}

func (p pgMappings) byRumor(ctx context.Context, rumor string, channel snowflake.ID) ([]row, error) {
	return p.query(ctx, `where rumor_id = $1 and discord_channel_id = $2`, rumor, int64(channel))
}

func (p pgMappings) query(ctx context.Context, where string, args ...any) ([]row, error) {
	rows, err := p.db.Query(ctx, `select discord_message_id, discord_channel_id, coalesce(webhook_id, 0),
		rumor_id, origin, author, part from armada_messages `+where+` order by part`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		var msg, ch, hook int64
		err := r.Scan(&msg, &ch, &hook, &x.Rumor, &x.Origin, &x.Author, &x.Part)
		x.Message, x.Channel, x.Webhook = snowflake.ID(msg), snowflake.ID(ch), snowflake.ID(hook)
		return x, err
	})
}

// memMappings keeps the newest rows in memory.
//
// ponytail: forgets everything on restart and past memCap rows, so a reply
// to or delete of an older message stops crossing; set SKUA_DATABASE_URL to
// keep them.
type memMappings struct {
	mu   sync.Mutex
	rows []row
}

const memCap = 50_000

func (m *memMappings) insert(_ context.Context, r row) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.rows) >= memCap {
		m.rows = m.rows[len(m.rows)-memCap/2:]
	}
	m.rows = append(m.rows, r)
	return nil
}

func (m *memMappings) find(keep func(row) bool) []row {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []row
	for _, r := range m.rows {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

func (m *memMappings) byMessage(_ context.Context, id snowflake.ID) ([]row, error) {
	return m.find(func(r row) bool { return r.Message == id }), nil
}

func (m *memMappings) byRumor(_ context.Context, rumor string, channel snowflake.ID) ([]row, error) {
	return m.find(func(r row) bool { return r.Rumor == rumor && r.Channel == channel }), nil
}
