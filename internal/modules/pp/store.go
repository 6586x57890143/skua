package pp

import (
	"context"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the slice of pgxpool.Pool /pp uses.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// load reads every member and server /pp has kept into st.
func (st *state) load(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select guild_id, user_id, total, purse, season, season_pp, week, week_voice, week_chat,
		day, day_voice, day_chat, day_talk, rested, class, class_season, class_changes, voice_min, messages from pp_member`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var guild, user int64
		m := &member{}
		if err := rows.Scan(&guild, &user, &m.total, &m.purse, &m.season, &m.seasonPP, &m.week, &m.weekAct[BoardVoice], &m.weekAct[BoardChat],
			&m.dayN, &m.day.Voice, &m.day.Chat, &m.day.Talk, &m.day.Rested, &m.class, &m.classSeason, &m.classChanges, &m.voiceMin, &m.messages); err != nil {
			rows.Close()
			return err
		}
		st.server(snowflake.ID(guild)).members[snowflake.ID(user)] = m
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = db.Query(ctx, `select guild_id, day, raw, history, lit, lit_before from pp_guild`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guild int64
		var s server
		if err := rows.Scan(&guild, &s.day, &s.raw, &s.history, &s.lit, &s.litBefore); err != nil {
			return err
		}
		into := st.server(snowflake.ID(guild))
		into.mu.Lock()
		into.day, into.raw, into.history, into.lit, into.litBefore = s.day, s.raw, s.history, s.lit, s.litBefore
		into.mu.Unlock()
	}
	return rows.Err()
}

// memberRows is a batch of members as columns, for one unnest.
type memberRows struct {
	guild, user, total, purse, season, seasonPP, week, weekVoice []int64
	weekChat, day, dayVoice, dayChat, dayTalk, rested            []int64
	classSeason, classChanges                                    []int64
	voiceMin, messages                                           []int64
	class                                                        []string
}

func (r *memberRows) add(guild, user snowflake.ID, m *member) {
	r.guild = append(r.guild, int64(guild))
	r.user = append(r.user, int64(user))
	r.total = append(r.total, m.total)
	r.purse = append(r.purse, m.purse)
	r.season = append(r.season, m.season)
	r.seasonPP = append(r.seasonPP, m.seasonPP)
	r.week = append(r.week, m.week)
	r.weekVoice = append(r.weekVoice, m.weekAct[BoardVoice])
	r.weekChat = append(r.weekChat, m.weekAct[BoardChat])
	r.day = append(r.day, m.dayN)
	r.dayVoice = append(r.dayVoice, m.day.Voice)
	r.dayChat = append(r.dayChat, m.day.Chat)
	r.dayTalk = append(r.dayTalk, m.day.Talk)
	r.rested = append(r.rested, m.day.Rested)
	r.class = append(r.class, m.class)
	r.classSeason = append(r.classSeason, m.classSeason)
	r.classChanges = append(r.classChanges, m.classChanges)
	r.voiceMin = append(r.voiceMin, m.voiceMin)
	r.messages = append(r.messages, m.messages)
}

// flush writes every member and server changed since the last flush, one
// statement for each table however many changed. What fails to write
// stays marked and goes with the next.
func (st *state) flush(ctx context.Context, db DB) (int, error) {
	var rows memberRows
	var guilds, days, raws, lits, before []int64
	var histories [][]int64
	type mark struct {
		s *server
		m *member
	}
	var marked []mark
	for g, s := range st.servers() {
		s.mu.Lock()
		for u, m := range s.members {
			if m.dirty {
				rows.add(g, u, m)
				m.dirty = false
				marked = append(marked, mark{m: m})
			}
		}
		if s.dirty {
			guilds = append(guilds, int64(g))
			days = append(days, s.day)
			raws = append(raws, s.raw)
			histories = append(histories, append([]int64{}, s.history...))
			lits = append(lits, int64(s.lit))
			before = append(before, int64(s.litBefore))
			s.dirty = false
			marked = append(marked, mark{s: s})
		}
		s.mu.Unlock()
	}
	remark := func() {
		for _, k := range marked {
			if k.m != nil {
				k.m.dirty = true
			} else {
				k.s.dirty = true
			}
		}
	}
	if len(rows.user) > 0 {
		_, err := db.Exec(ctx, `insert into pp_member (guild_id, user_id, total, purse, season, season_pp, week, week_voice,
				week_chat, day, day_voice, day_chat, day_talk, rested, class, class_season, class_changes, voice_min, messages)
			select * from unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::bigint[], $5::bigint[], $6::bigint[], $7::bigint[],
				$8::bigint[], $9::bigint[], $10::bigint[], $11::bigint[], $12::bigint[], $13::bigint[], $14::bigint[], $15::text[],
				$16::bigint[], $17::bigint[], $18::bigint[], $19::bigint[])
			on conflict (guild_id, user_id) do update set total = excluded.total, purse = excluded.purse, season = excluded.season,
				season_pp = excluded.season_pp, week = excluded.week, week_voice = excluded.week_voice,
				week_chat = excluded.week_chat, day = excluded.day, day_voice = excluded.day_voice,
				day_chat = excluded.day_chat, day_talk = excluded.day_talk, rested = excluded.rested,
				class = excluded.class, class_season = excluded.class_season, class_changes = excluded.class_changes,
				voice_min = excluded.voice_min,
				messages = excluded.messages`,
			rows.guild, rows.user, rows.total, rows.purse, rows.season, rows.seasonPP, rows.week, rows.weekVoice,
			rows.weekChat, rows.day, rows.dayVoice, rows.dayChat, rows.dayTalk, rows.rested, rows.class, rows.classSeason,
			rows.classChanges, rows.voiceMin, rows.messages)
		if err != nil {
			remark()
			return 0, err
		}
	}
	// A server's history is an array, which unnest would flatten, so
	// servers go one statement each; there are few, and few change.
	for i, g := range guilds {
		_, err := db.Exec(ctx, `insert into pp_guild (guild_id, day, raw, history, lit, lit_before) values ($1, $2, $3, $4, $5, $6)
			on conflict (guild_id) do update set day = excluded.day, raw = excluded.raw, history = excluded.history,
				lit = excluded.lit, lit_before = excluded.lit_before`,
			g, days[i], raws[i], histories[i], lits[i], before[i])
		if err != nil {
			remark()
			return 0, err
		}
	}
	return len(rows.user), nil
}
