package remind

import (
	"context"
	"errors"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the slice of a pgx pool remind uses.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// reminder is one row of remind.
type reminder struct {
	id                    int64
	guild, owner, channel snowflake.ID
	target                string // "dm" or "channel"
	targetID              snowflake.ID
	text                  string
	due                   time.Time
	r                     rule
	tz                    string
	state                 string
	created               time.Time
}

const cols = "id, guild_id, owner_id, channel_id, target, target_id, text, due_at, mask, mday, at_min, tz, state, created_at"

// keepDone is how long a fired one-off rests, so its card's snooze still
// finds it.
const keepDone = 7 * 24 * time.Hour

func scan(row pgx.Row) (reminder, error) {
	var r reminder
	var guild, owner, channel, target int64
	var mask, mday, at int16
	err := row.Scan(&r.id, &guild, &owner, &channel, &r.target, &target, &r.text, &r.due, &mask, &mday, &at, &r.tz, &r.state, &r.created)
	r.guild, r.owner, r.channel, r.targetID = snowflake.ID(guild), snowflake.ID(owner), snowflake.ID(channel), snowflake.ID(target)
	r.r = rule{mask: uint8(mask), mday: int(mday), at: int(at)}
	return r, err
}

func (r reminder) loc() *time.Location { return zone(r.tz) }

func insert(ctx context.Context, db DB, r reminder) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, `insert into remind (guild_id, owner_id, channel_id, target, target_id, text, due_at, mask, mday, at_min, tz)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) returning id`,
		int64(r.guild), int64(r.owner), int64(r.channel), r.target, int64(r.targetID), r.text, r.due, int16(r.r.mask), int16(r.r.mday), int16(r.r.at), r.tz).Scan(&id)
	return id, err
}

// get is reminder id, or false when it is gone.
func get(ctx context.Context, db DB, id int64) (reminder, bool, error) {
	r, err := scan(db.QueryRow(ctx, "select "+cols+" from remind where id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// dueBy is every pending reminder due before t, soonest first.
func dueBy(ctx context.Context, db DB, t time.Time) ([]reminder, error) {
	rows, err := db.Query(ctx, "select "+cols+" from remind where state = 'pending' and due_at < $1 order by due_at", t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reminder
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// claim takes r to fire, if it is still pending and still due when it was
// queued: an edit or another process that got there first makes it miss.
// A one-off is marked sending until it is out; a repeat moves on to next
// in the same statement.
func claim(ctx context.Context, db DB, r reminder, next time.Time) (bool, error) {
	var tag pgconn.CommandTag
	var err error
	if r.r.once() {
		tag, err = db.Exec(ctx, "update remind set state = 'sending', sent_at = now() where id = $1 and due_at = $2 and state = 'pending'", r.id, r.due)
	} else {
		tag, err = db.Exec(ctx, "update remind set due_at = $3 where id = $1 and due_at = $2 and state = 'pending'", r.id, r.due, next)
	}
	return err == nil && tag.RowsAffected() == 1, err
}

// settle is a one-off's send: done once it is out, back to pending at
// retry when it isn't.
func settle(ctx context.Context, db DB, id int64, retry time.Time) error {
	if retry.IsZero() {
		_, err := db.Exec(ctx, "update remind set state = 'done' where id = $1 and state = 'sending'", id)
		return err
	}
	_, err := db.Exec(ctx, "update remind set state = 'pending', due_at = $2 where id = $1 and state = 'sending'", id, retry)
	return err
}

// stuck is how long a claimed send can take before it is taken to have
// died with its process: far past any send, so a deploy's other process
// mid send is never taken for dead.
const stuck = 2 * time.Minute

// tidyUp puts back sends a dead process left claimed, and forgets
// one-offs that have rested long enough. Sending twice beats never.
func tidyUp(ctx context.Context, db DB, now time.Time) error {
	if _, err := db.Exec(ctx, "update remind set state = 'pending' where state = 'sending' and sent_at < $1", now.Add(-stuck)); err != nil {
		return err
	}
	_, err := db.Exec(ctx, "delete from remind where state = 'done' and due_at < $1", now.Add(-keepDone))
	return err
}

// reschedule sets id's text, due time and rule, pending again. Only its
// owner's id matches.
func reschedule(ctx context.Context, db DB, r reminder) (bool, error) {
	tag, err := db.Exec(ctx, "update remind set text = $3, due_at = $4, mask = $5, mday = $6, at_min = $7, state = 'pending' where id = $1 and owner_id = $2",
		r.id, int64(r.owner), r.text, r.due, int16(r.r.mask), int16(r.r.mday), int16(r.r.at))
	return err == nil && tag.RowsAffected() == 1, err
}

// repeatsOf is user's waiting repeats.
func repeatsOf(ctx context.Context, db DB, user snowflake.ID) ([]reminder, error) {
	rows, err := db.Query(ctx, "select "+cols+" from remind where owner_id = $1 and state = 'pending' and (mask <> 0 or mday <> 0)", int64(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reminder
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// retime sets a pending reminder's zone and due time.
func retime(ctx context.Context, db DB, r reminder) (bool, error) {
	tag, err := db.Exec(ctx, "update remind set tz = $2, due_at = $3 where id = $1 and state = 'pending'", r.id, r.tz, r.due)
	return err == nil && tag.RowsAffected() == 1, err
}

func cancel(ctx context.Context, db DB, id int64, owner snowflake.ID) (bool, error) {
	tag, err := db.Exec(ctx, "delete from remind where id = $1 and owner_id = $2", id, int64(owner))
	return err == nil && tag.RowsAffected() == 1, err
}

func pendingOf(ctx context.Context, db DB, owner snowflake.ID) (int, error) {
	var n int
	err := db.QueryRow(ctx, "select count(*) from remind where owner_id = $1 and state = 'pending'", int64(owner)).Scan(&n)
	return n, err
}

// pendingByGuild is how many reminders each server has waiting.
func pendingByGuild(ctx context.Context, db DB) (map[snowflake.ID]int, error) {
	rows, err := db.Query(ctx, "select guild_id, count(*) from remind where state = 'pending' group by guild_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[snowflake.ID]int{}
	for rows.Next() {
		var g int64
		var n int
		if err := rows.Scan(&g, &n); err != nil {
			return nil, err
		}
		out[snowflake.ID(g)] = n
	}
	return out, rows.Err()
}

func dropGuild(ctx context.Context, db DB, guild snowflake.ID) error {
	_, err := db.Exec(ctx, "delete from remind where guild_id = $1", int64(guild))
	return err
}

// person is what remind keeps about a member wherever they are.
type person struct {
	tz        string
	dmChannel snowflake.ID
}

func personOf(ctx context.Context, db DB, user snowflake.ID) (person, error) {
	var p person
	var dm int64
	err := db.QueryRow(ctx, "select tz, dm_channel from remind_user where user_id = $1", int64(user)).Scan(&p.tz, &dm)
	if errors.Is(err, pgx.ErrNoRows) {
		return person{}, nil
	}
	p.dmChannel = snowflake.ID(dm)
	return p, err
}

func saveTZ(ctx context.Context, db DB, user snowflake.ID, tz string) error {
	_, err := db.Exec(ctx, "insert into remind_user (user_id, tz) values ($1, $2) on conflict (user_id) do update set tz = $2", int64(user), tz)
	return err
}

func saveDM(ctx context.Context, db DB, user, ch snowflake.ID) error {
	_, err := db.Exec(ctx, "insert into remind_user (user_id, dm_channel) values ($1, $2) on conflict (user_id) do update set dm_channel = $2", int64(user), int64(ch))
	return err
}
