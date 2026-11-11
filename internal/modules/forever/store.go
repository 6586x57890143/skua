package forever

import (
	"context"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the slice of pgxpool.Pool forever uses. Without one, everything
// lives in memory and a restart forgets it.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// key is a character: its region and Blizzard's id for it there.
type key struct {
	region string
	id     int64
}

// char is what skua knows of one character, shared by every server that
// lists it.
type char struct {
	key
	name, surname           string
	class, race, ruleset    string
	classID                 int
	faction                 string
	level, xp, points, ilvl int
	avatar, render          string    // Blizzard's renders, for the cards
	modified                string    // the profile's Last-Modified
	missing                 int       // polls in a row Blizzard didn't answer
	reached                 time.Time // when it reached its level; zero when skua didn't see it
}

func (c *char) slug() string { return slug(c.name, c.surname) }

// claim is a character listed in a server, by the member who plays it.
type claim struct {
	guild, user snowflake.ID
	k           key
	verified    bool
	at          time.Time
}

// setup is a server's feed channel and self-pick role.
type setup struct{ feed, role snowflake.ID }

type state struct {
	chars  map[key]*char
	claims []claim
	guilds map[snowflake.ID]setup
}

func load(ctx context.Context, db DB) (state, error) {
	st := state{chars: map[key]*char{}, guilds: map[snowflake.ID]setup{}}
	if db == nil {
		return st, nil
	}
	rows, err := db.Query(ctx, `select region, id, name, surname, class, class_id, race, ruleset, faction, level, xp, points, ilvl, avatar, render, modified, reached from forever_char`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		c := &char{}
		if err := rows.Scan(&c.region, &c.id, &c.name, &c.surname, &c.class, &c.classID, &c.race, &c.ruleset, &c.faction, &c.level, &c.xp, &c.points, &c.ilvl, &c.avatar, &c.render, &c.modified, &c.reached); err != nil {
			rows.Close()
			return st, err
		}
		st.chars[c.key] = c
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = db.Query(ctx, `select guild_id, user_id, region, char_id, verified, added_at from forever_claim order by added_at`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var c claim
		if err := rows.Scan(&c.guild, &c.user, &c.k.region, &c.k.id, &c.verified, &c.at); err != nil {
			rows.Close()
			return st, err
		}
		st.claims = append(st.claims, c)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = db.Query(ctx, `select guild_id, feed_id, role_id from forever_guild`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var g snowflake.ID
		var s setup
		if err := rows.Scan(&g, &s.feed, &s.role); err != nil {
			rows.Close()
			return st, err
		}
		st.guilds[g] = s
	}
	return st, rows.Err()
}

func saveChar(ctx context.Context, db DB, c char) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, `insert into forever_char (region, id, name, surname, class, class_id, race, ruleset, faction, level, xp, points, ilvl, avatar, render, modified, reached)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		on conflict (region, id) do update set name=$3, surname=$4, class=$5, class_id=$6, race=$7, ruleset=$8, faction=$9, level=$10, xp=$11, points=$12, ilvl=$13, avatar=$14, render=$15, modified=$16, reached=$17`,
		c.region, c.id, c.name, c.surname, c.class, c.classID, c.race, c.ruleset, c.faction, c.level, c.xp, c.points, c.ilvl, c.avatar, c.render, c.modified, c.reached)
	return err
}

func saveDings(ctx context.Context, db DB, k key, from, to int, at time.Time) error {
	if db == nil || to <= from {
		return nil
	}
	_, err := db.Exec(ctx, `insert into forever_ding (region, char_id, level, at)
		select $1, $2, l, $5 from generate_series($3::int, $4::int) l on conflict do nothing`, k.region, k.id, from+1, to, at)
	return err
}

func saveClaim(ctx context.Context, db DB, c claim) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, `insert into forever_claim (guild_id, user_id, region, char_id, verified, added_at) values ($1,$2,$3,$4,$5,$6)
		on conflict (guild_id, region, char_id) do update set user_id=$2, verified=$5`, c.guild, c.user, c.k.region, c.k.id, c.verified, c.at)
	return err
}

func dropClaim(ctx context.Context, db DB, guild snowflake.ID, k key) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, `delete from forever_claim where guild_id=$1 and region=$2 and char_id=$3`, guild, k.region, k.id)
	return err
}

func dropGuild(ctx context.Context, db DB, guild snowflake.ID) error {
	if db == nil {
		return nil
	}
	if _, err := db.Exec(ctx, `delete from forever_claim where guild_id=$1`, guild); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `delete from forever_guild where guild_id=$1`, guild)
	return err
}

func saveSetup(ctx context.Context, db DB, guild snowflake.ID, s setup) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, `insert into forever_guild (guild_id, feed_id, role_id) values ($1,$2,$3)
		on conflict (guild_id) do update set feed_id=$2, role_id=$3`, guild, s.feed, s.role)
	return err
}
