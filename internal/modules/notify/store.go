package notify

import (
	"context"
	"encoding/json"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the slice of pgxpool.Pool notify uses. Without one, follows live
// in memory and a restart forgets them.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// follow is one account a guild follows. channel 0 is the guild's bound
// channel, wherever that is when a card goes.
type follow struct {
	guild, channel    snowflake.ID
	platform, account string
	name              string
	role              snowflake.ID // 0 pings no one
}

// key is an account on a platform, polled once however many follow it.
type key struct{ platform, account string }

// posted is a live card skua put up, kept until its stream ends.
type posted struct {
	k                             key
	guild, channel, message, role snowflake.ID
	it                            item
	at                            time.Time
	edited                        time.Time // its last freshen; not stored, so a restart starts from at
	peak                          int       // the most viewers seen; not stored either
}

// stream is a stream's live cards are found by: an account and the
// stream's ID.
type stream struct {
	k  key
	id string
}

// state is everything notify keeps.
type state struct {
	follows []follow
	seen    map[key][]string
	bound   map[snowflake.ID]snowflake.ID // guild -> its channel
	live    map[stream][]posted
}

// load reads every follow, everything seen, each guild's channel and the
// live cards still up.
func load(ctx context.Context, db DB) (state, error) {
	st := state{seen: map[key][]string{}, bound: map[snowflake.ID]snowflake.ID{}, live: map[stream][]posted{}}
	if db == nil {
		return st, nil
	}
	rows, err := db.Query(ctx, "select platform, account, live_id, guild_id, channel_id, message_id, role_id, item, posted_at from notify_live")
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var p posted
		var id string
		var g, c, msg, r int64
		var raw []byte
		if err := rows.Scan(&p.k.platform, &p.k.account, &id, &g, &c, &msg, &r, &raw, &p.at); err != nil {
			rows.Close()
			return st, err
		}
		if err := json.Unmarshal(raw, &p.it); err != nil {
			rows.Close()
			return st, err
		}
		p.guild, p.channel, p.message, p.role = snowflake.ID(g), snowflake.ID(c), snowflake.ID(msg), snowflake.ID(r)
		s := stream{p.k, id}
		st.live[s] = append(st.live[s], p)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = db.Query(ctx, "select guild_id, channel_id from notify_guild")
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var g, c int64
		if err := rows.Scan(&g, &c); err != nil {
			rows.Close()
			return st, err
		}
		st.bound[snowflake.ID(g)] = snowflake.ID(c)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = db.Query(ctx, "select guild_id, channel_id, platform, account, name, role_id from notify_follow")
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var f follow
		var g, c, r int64
		if err := rows.Scan(&g, &c, &f.platform, &f.account, &f.name, &r); err != nil {
			rows.Close()
			return st, err
		}
		f.guild, f.channel, f.role = snowflake.ID(g), snowflake.ID(c), snowflake.ID(r)
		st.follows = append(st.follows, f)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = db.Query(ctx, "select platform, account, ids from notify_seen")
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var k key
		var ids []string
		if err := rows.Scan(&k.platform, &k.account, &ids); err != nil {
			rows.Close()
			return st, err
		}
		st.seen[k] = ids
	}
	return st, rows.Err()
}

func saveBound(ctx context.Context, db DB, guild, channel snowflake.ID) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, `insert into notify_guild (guild_id, channel_id) values ($1, $2)
		on conflict (guild_id) do update set channel_id = excluded.channel_id`, int64(guild), int64(channel))
	return err
}

func saveFollow(ctx context.Context, db DB, f follow) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, `insert into notify_follow (guild_id, channel_id, platform, account, name, role_id)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (guild_id, platform, account, channel_id) do update set name = excluded.name, role_id = excluded.role_id`,
		int64(f.guild), int64(f.channel), f.platform, f.account, f.name, int64(f.role))
	return err
}

func dropFollow(ctx context.Context, db DB, f follow) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, "delete from notify_follow where guild_id = $1 and platform = $2 and account = $3 and channel_id = $4",
		int64(f.guild), f.platform, f.account, int64(f.channel))
	return err
}

func dropGuild(ctx context.Context, db DB, guild snowflake.ID) error {
	if db == nil {
		return nil
	}
	for _, table := range []string{"notify_follow", "notify_guild", "notify_live"} {
		if _, err := db.Exec(ctx, "delete from "+table+" where guild_id = $1", int64(guild)); err != nil {
			return err
		}
	}
	return nil
}

func saveLive(ctx context.Context, db DB, p posted) error {
	if db == nil {
		return nil
	}
	raw, err := json.Marshal(p.it)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `insert into notify_live (message_id, platform, account, live_id, guild_id, channel_id, role_id, item, posted_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9) on conflict (message_id) do nothing`,
		int64(p.message), p.k.platform, p.k.account, p.it.ID, int64(p.guild), int64(p.channel), int64(p.role), raw, p.at)
	return err
}

// dropLive forgets a stream's live cards, or every one of an account's
// when id is empty.
func dropLive(ctx context.Context, db DB, k key, id string) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, "delete from notify_live where platform = $1 and account = $2 and ($3 = '' or live_id = $3)", k.platform, k.account, id)
	return err
}

// saveSeen records what an account showed; nil ids forgets it, so a new
// follow starts from a fresh baseline.
func saveSeen(ctx context.Context, db DB, k key, ids []string) error {
	if db == nil {
		return nil
	}
	if ids == nil {
		_, err := db.Exec(ctx, "delete from notify_seen where platform = $1 and account = $2", k.platform, k.account)
		return err
	}
	_, err := db.Exec(ctx, `insert into notify_seen (platform, account, ids) values ($1, $2, $3)
		on conflict (platform, account) do update set ids = excluded.ids`, k.platform, k.account, ids)
	return err
}
