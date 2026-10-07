// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"context"
	"slices"
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
	// ReplyTo is the rumor an Armada message replies to, kept so an edit
	// can redraw its reply line.
	ReplyTo string
}

// mappings is where rows live: Postgres when skua has one, memory when not.
type mappings interface {
	insert(ctx context.Context, r row) error
	byMessage(ctx context.Context, id snowflake.ID) ([]row, error)
	// byRumor is scoped to one Discord channel, so a link only ever
	// resolves inside itself.
	byRumor(ctx context.Context, rumor string, channel snowflake.ID) ([]row, error)

	addReaction(ctx context.Context, r reaction) error
	dropReaction(ctx context.Context, rumor string) error
	// clearReactions drops a message's rows for one emoji, or all of them
	// when emoji is empty.
	clearReactions(ctx context.Context, message snowflake.ID, emoji string) error
	// reactionByRumor is scoped to one Discord channel, like byRumor.
	reactionByRumor(ctx context.Context, rumor string, channel snowflake.ID) (reaction, bool, error)
	// reactionByDiscord is a Discord member's own reaction, never an
	// Armada one wearing skua's.
	reactionByDiscord(ctx context.Context, message snowflake.ID, user, emoji string) (reaction, bool, error)
	// armadaReactions counts the Armada reactors skua's reaction stands for.
	armadaReactions(ctx context.Context, message snowflake.ID, emoji string) (int, error)
}

// reaction ties one kind 7 to one reaction on a Discord message.
type reaction struct {
	Rumor            string
	Channel, Message snowflake.ID
	Emoji            string // the unicode, or name:id
	Origin           string // "discord" or "armada"
	Author           string // a Discord user id, or a pubkey
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
		(discord_message_id, discord_channel_id, webhook_id, rumor_id, origin, author, part, reply_to)
		values ($1, $2, $3, $4, $5, $6, $7, $8) on conflict do nothing`,
		int64(r.Message), int64(r.Channel), hook, r.Rumor, r.Origin, r.Author, r.Part, r.ReplyTo)
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
		rumor_id, origin, author, part, reply_to from armada_messages `+where+` order by part`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		var msg, ch, hook int64
		err := r.Scan(&msg, &ch, &hook, &x.Rumor, &x.Origin, &x.Author, &x.Part, &x.ReplyTo)
		x.Message, x.Channel, x.Webhook = snowflake.ID(msg), snowflake.ID(ch), snowflake.ID(hook)
		return x, err
	})
}

func (p pgMappings) addReaction(ctx context.Context, r reaction) error {
	_, err := p.db.Exec(ctx, `insert into armada_reactions
		(rumor_id, discord_channel_id, discord_message_id, emoji, origin, author)
		values ($1, $2, $3, $4, $5, $6) on conflict do nothing`,
		r.Rumor, int64(r.Channel), int64(r.Message), r.Emoji, r.Origin, r.Author)
	return err
}

func (p pgMappings) dropReaction(ctx context.Context, rumor string) error {
	_, err := p.db.Exec(ctx, `delete from armada_reactions where rumor_id = $1`, rumor)
	return err
}

func (p pgMappings) clearReactions(ctx context.Context, message snowflake.ID, emoji string) error {
	_, err := p.db.Exec(ctx, `delete from armada_reactions
		where discord_message_id = $1 and ($2 = '' or emoji = $2)`, int64(message), emoji)
	return err
}

func (p pgMappings) reactionByRumor(ctx context.Context, rumor string, channel snowflake.ID) (reaction, bool, error) {
	return p.reaction(ctx, `where rumor_id = $1 and discord_channel_id = $2`, rumor, int64(channel))
}

func (p pgMappings) reactionByDiscord(ctx context.Context, message snowflake.ID, user, emoji string) (reaction, bool, error) {
	return p.reaction(ctx, `where discord_message_id = $1 and emoji = $2 and author = $3 and origin = 'discord'`,
		int64(message), emoji, user)
}

func (p pgMappings) reaction(ctx context.Context, where string, args ...any) (reaction, bool, error) {
	rows, err := p.db.Query(ctx, `select rumor_id, discord_channel_id, discord_message_id, emoji, origin, author
		from armada_reactions `+where+` limit 1`, args...)
	if err != nil {
		return reaction{}, false, err
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (reaction, error) {
		var x reaction
		var ch, msg int64
		err := r.Scan(&x.Rumor, &ch, &msg, &x.Emoji, &x.Origin, &x.Author)
		x.Channel, x.Message = snowflake.ID(ch), snowflake.ID(msg)
		return x, err
	})
	if err != nil || len(all) == 0 {
		return reaction{}, false, err
	}
	return all[0], true, nil
}

func (p pgMappings) armadaReactions(ctx context.Context, message snowflake.ID, emoji string) (int, error) {
	rows, err := p.db.Query(ctx, `select count(*)::int from armada_reactions
		where discord_message_id = $1 and emoji = $2 and origin = 'armada'`, int64(message), emoji)
	if err != nil {
		return 0, err
	}
	return pgx.CollectExactlyOneRow(rows, pgx.RowTo[int])
}

// memMappings keeps the newest rows in memory.
//
// ponytail: forgets everything on restart and past memCap rows, so a reply
// to or delete of an older message stops crossing; set SKUA_DATABASE_URL to
// keep them.
type memMappings struct {
	mu    sync.Mutex
	rows  []row
	reacs []reaction
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

func (m *memMappings) addReaction(_ context.Context, r reaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.reacs) >= memCap {
		m.reacs = m.reacs[len(m.reacs)-memCap/2:]
	}
	m.reacs = append(m.reacs, r)
	return nil
}

func (m *memMappings) dropReaction(_ context.Context, rumor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reacs = slices.DeleteFunc(m.reacs, func(r reaction) bool { return r.Rumor == rumor })
	return nil
}

func (m *memMappings) clearReactions(_ context.Context, message snowflake.ID, emoji string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reacs = slices.DeleteFunc(m.reacs, func(r reaction) bool {
		return r.Message == message && (emoji == "" || r.Emoji == emoji)
	})
	return nil
}

func (m *memMappings) findReaction(keep func(reaction) bool) (reaction, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i := slices.IndexFunc(m.reacs, keep); i >= 0 {
		return m.reacs[i], true, nil
	}
	return reaction{}, false, nil
}

func (m *memMappings) reactionByRumor(_ context.Context, rumor string, channel snowflake.ID) (reaction, bool, error) {
	return m.findReaction(func(r reaction) bool { return r.Rumor == rumor && r.Channel == channel })
}

func (m *memMappings) reactionByDiscord(_ context.Context, message snowflake.ID, user, emoji string) (reaction, bool, error) {
	return m.findReaction(func(r reaction) bool {
		return r.Message == message && r.Emoji == emoji && r.Author == user && r.Origin == "discord"
	})
}

func (m *memMappings) armadaReactions(_ context.Context, message snowflake.ID, emoji string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.reacs {
		if r.Message == message && r.Emoji == emoji && r.Origin == "armada" {
			n++
		}
	}
	return n, nil
}
