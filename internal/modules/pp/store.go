package pp

import (
	"context"
	"encoding/json"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the slice of pgxpool.Pool /pp uses.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// load reads every member, server, ledger head and season seed /pp has
// kept into st.
func (st *state) load(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select guild_id, user_id, total, purse, season, season_pp, week, week_voice, week_chat,
		day, day_voice, day_chat, day_talk, rested, class, class_season, class_changes, class_day, livery, livery_until, voice_min, messages,
		day_got, buff_lo, buff_hi, day_minutes, day_paid, day_levy, granted, grant_season, rung, day_withheld, quests, quest_day, quests_done, name, pact, signs, laurels, pact_channel, pact_started, withdraws, pact_at from pp_member`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var guild, user int64
		var quests, pact, signs string
		var pactChannel, withdraws, pactAt int64
		var started bool
		m := &member{}
		if err := rows.Scan(&guild, &user, &m.total, &m.purse, &m.season, &m.seasonPP, &m.week, &m.weekAct[BoardVoice], &m.weekAct[BoardChat],
			&m.dayN, &m.day.Voice, &m.day.Chat, &m.day.Talk, &m.day.Rested, &m.class, &m.classSeason, &m.classChanges, &m.classDay, &m.livery, &m.liveryUntil, &m.voiceMin, &m.messages,
			&m.dayGot, &m.buffLo, &m.buffHi, &m.day.Minutes, &m.day.Paid, &m.dayLevy, &m.granted, &m.grantSeason, &m.rung, &m.dayWithheld, &quests, &m.questDay, &m.questsDone, &m.name, &pact, &signs, &m.laurels, &pactChannel, &started, &withdraws, &pactAt); err != nil {
			rows.Close()
			return err
		}
		m.quests = decodeQuests(st.bal(), quests)
		m.pact, m.signs = decodePact(pact), decodeSigns(signs)
		if m.pact != nil {
			// One saved before pacts kept this had started if it had
			// minutes, or was full.
			m.pact.channel, m.pact.at = snowflake.ID(pactChannel), pactAt
			m.pact.started = started || m.pact.minutes > 0 || len(m.pact.signers) == m.pact.size()
		}
		m.withdrawDay, m.withdrawn = withdraws/8, withdraws%8
		st.server(snowflake.ID(guild)).members[snowflake.ID(user)] = m
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = db.Query(ctx, `select guild_id, day, raw, history, lit, lit_before, beta_role, log_channel,
		treasury, levy_open, levy_full, levy_days, fund, fund_seeded, season_mint, season_burn, sink_last, heavy, selfie_channel, livery_anchor, heralds_channel, yard_open, livery_slots from pp_guild`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var guild, beta, logCh, selfies, anchor, heralds int64
		var s server
		if err := rows.Scan(&guild, &s.day, &s.raw, &s.history, &s.lit, &s.litBefore, &beta, &logCh,
			&s.treasury, &s.levyOpen, &s.levyFull, &s.levyDays, &s.fund, &s.seeded, &s.seasonMint, &s.seasonBurn, &s.sinkLast, &s.heavy, &selfies, &anchor, &heralds, &s.yardOpen, &s.slots); err != nil {
			rows.Close()
			return err
		}
		into := st.server(snowflake.ID(guild))
		into.mu.Lock()
		into.day, into.raw, into.history, into.lit, into.litBefore = s.day, s.raw, s.history, s.lit, s.litBefore
		into.beta, into.logChannel = snowflake.ID(beta), snowflake.ID(logCh)
		into.treasury, into.levyOpen, into.levyFull, into.levyDays = s.treasury, s.levyOpen, s.levyFull, s.levyDays
		into.fund, into.seeded = s.fund, s.seeded
		into.seasonMint, into.seasonBurn, into.sinkLast, into.heavy = s.seasonMint, s.seasonBurn, s.sinkLast, s.heavy
		into.selfies = snowflake.ID(selfies)
		into.anchor, into.heralds = snowflake.ID(anchor), snowflake.ID(heralds)
		into.yardOpen, into.slots = s.yardOpen, s.slots
		into.mu.Unlock()
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range st.servers() {
		s.mu.Lock()
		joinPacts(s)
		s.mu.Unlock()
	}
	if err := st.loadDyes(ctx, db); err != nil {
		return err
	}
	if err := st.loadPetitions(ctx, db); err != nil {
		return err
	}
	if err := st.loadLedger(ctx, db); err != nil {
		return err
	}
	// A grant still paying never reached a flush, which would have marked
	// it paid with its ledger rows, so it was never paid: it waits again.
	if _, err := db.Exec(ctx, `update pp_grant set state = 'waiting', second_id = 0 where state = 'paying'`); err != nil {
		return err
	}
	if err := st.loadGuests(ctx, db); err != nil {
		return err
	}
	return st.loadSeeds(ctx, db)
}

// memberRows is a batch of members as columns, for one unnest.
type memberRows struct {
	guild, user, total, purse, season, seasonPP, week, weekVoice  []int64
	weekChat, day, dayVoice, dayChat, dayTalk, rested             []int64
	classSeason, classChanges, classDay, liveryUntil              []int64
	voiceMin, messages                                            []int64
	dayGot, buffLo, buffHi, dayMinutes, dayPaid, dayLevy          []int64
	granted, grantSeason, rung, dayWithheld                       []int64
	questDay, questsDone, laurels, pactChannel, withdraws, pactAt []int64
	pactStarted                                                   []bool
	quests, name, livery, pact, signs                             []string
	class                                                         []string
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
	r.classDay = append(r.classDay, m.classDay)
	r.livery = append(r.livery, m.livery)
	r.liveryUntil = append(r.liveryUntil, m.liveryUntil)
	r.voiceMin = append(r.voiceMin, m.voiceMin)
	r.messages = append(r.messages, m.messages)
	r.dayGot = append(r.dayGot, m.dayGot)
	r.buffLo = append(r.buffLo, m.buffLo)
	r.buffHi = append(r.buffHi, m.buffHi)
	r.dayMinutes = append(r.dayMinutes, m.day.Minutes)
	r.dayPaid = append(r.dayPaid, m.day.Paid)
	r.dayLevy = append(r.dayLevy, m.dayLevy)
	r.granted = append(r.granted, m.granted)
	r.grantSeason = append(r.grantSeason, m.grantSeason)
	r.rung = append(r.rung, m.rung)
	r.dayWithheld = append(r.dayWithheld, m.dayWithheld)
	r.quests = append(r.quests, encodeQuests(m.quests))
	r.questDay = append(r.questDay, m.questDay)
	r.questsDone = append(r.questsDone, m.questsDone)
	r.name = append(r.name, m.name)
	r.pact = append(r.pact, encodePact(m.pact))
	r.signs = append(r.signs, encodeSigns(m.signs))
	r.laurels = append(r.laurels, m.laurels)
	var ch snowflake.ID
	var at int64
	started := false
	if m.pact != nil {
		ch, started, at = m.pact.channel, m.pact.started, m.pact.at
	}
	r.pactAt = append(r.pactAt, at)
	r.pactChannel = append(r.pactChannel, int64(ch))
	r.pactStarted = append(r.pactStarted, started)
	// A day and a count under 8, in one column.
	r.withdraws = append(r.withdraws, m.withdrawDay*8+min(m.withdrawn, 7))
}

// flush writes every member, server and grant paid since the last flush in
// one transaction, so a purse, its ledger rows, the treasury and the grant
// that moved them land together or not at all. What fails to write stays
// marked and goes with the next.
func (st *state) flush(ctx context.Context, db DB) (int, error) {
	st.flushing.Lock()
	defer st.flushing.Unlock()
	var rows memberRows
	var paid []int64
	tookPaid := map[*server][]int64{}
	var ledger ledgerRows
	took := map[*server][]entry{}
	var stats statRows
	var guests guestRows
	tookGuests := map[*server][]snowflake.ID{}
	tookStats := map[*server]map[statKey]int64{}
	var guilds, days, raws, lits, before []int64
	var treasuries, open, full, funds, smint, sburn, slast []int64
	var heavy, seeded []bool
	var levyDays [][]int64
	var histories [][]int64
	type mark struct {
		s *server
		m *member
	}
	var marked []mark
	for g, s := range st.servers() {
		s.mu.Lock()
		for _, e := range s.pending {
			ledger.add(g, e)
		}
		if len(s.pending) > 0 {
			took[s], s.pending = s.pending, nil
		}
		if len(s.paid) > 0 {
			paid = append(paid, s.paid...)
			tookPaid[s], s.paid = s.paid, nil
		}
		for k, n := range s.stats {
			stats.add(g, k, n)
		}
		if len(s.stats) > 0 {
			tookStats[s], s.stats = s.stats, nil
		}
		for u, m := range s.members {
			if m.dirty {
				rows.add(g, u, m)
				m.dirty = false
				marked = append(marked, mark{m: m})
			}
		}
		for _, u := range s.seenGuests {
			guests.add(g, u, s.guests[u])
		}
		if len(s.seenGuests) > 0 {
			tookGuests[s], s.seenGuests = s.seenGuests, nil
		}
		if s.dirty {
			guilds = append(guilds, int64(g))
			days = append(days, s.day)
			raws = append(raws, s.raw)
			histories = append(histories, append([]int64{}, s.history...))
			lits = append(lits, int64(s.lit))
			before = append(before, int64(s.litBefore))
			treasuries, open, full = append(treasuries, s.treasury), append(open, s.levyOpen), append(full, s.levyFull)
			funds, seeded = append(funds, s.fund), append(seeded, s.seeded)
			smint, sburn, slast, heavy = append(smint, s.seasonMint), append(sburn, s.seasonBurn), append(slast, s.sinkLast), append(heavy, s.heavy)
			levyDays = append(levyDays, append([]int64{}, s.levyDays...))
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
		// Rows taken go back ahead of any chained on since, keeping order.
		for s, es := range took {
			s.mu.Lock()
			s.pending = append(es, s.pending...)
			s.mu.Unlock()
		}
		for s, ids := range tookPaid {
			s.mu.Lock()
			s.paid = append(ids, s.paid...)
			s.mu.Unlock()
		}
		for s, ids := range tookGuests {
			s.mu.Lock()
			s.seenGuests = append(ids, s.seenGuests...)
			s.mu.Unlock()
		}
		// Counters taken go back onto whatever's counted since.
		for s, kept := range tookStats {
			s.mu.Lock()
			if s.stats == nil {
				s.stats = map[statKey]int64{}
			}
			for k, n := range kept {
				s.stats[k] += n
			}
			s.mu.Unlock()
		}
	}
	if len(rows.user) == 0 && len(ledger.seq) == 0 && len(guilds) == 0 && len(paid) == 0 && len(stats.n) == 0 && len(guests.user) == 0 {
		return 0, nil
	}
	if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if err := writeFlush(ctx, tx, &rows, &ledger, paid, guilds, func(i int) []any {
			return []any{guilds[i], days[i], raws[i], histories[i], lits[i], before[i], treasuries[i], open[i], full[i], levyDays[i], funds[i], seeded[i], smint[i], sburn[i], slast[i], heavy[i]}
		}); err != nil {
			return err
		}
		if err := guests.write(ctx, tx); err != nil {
			return err
		}
		return writeStats(ctx, tx, &stats)
	}); err != nil {
		remark()
		return 0, err
	}
	// What's committed now may be told.
	for s, es := range took {
		s.mu.Lock()
		s.saved = max(s.saved, es[len(es)-1].seq)
		s.mu.Unlock()
	}
	return len(rows.user), nil
}

// writeStats adds the counters taken to pp_stat, inside the flush's
// transaction.
func writeStats(ctx context.Context, tx pgx.Tx, r *statRows) error {
	if len(r.n) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `insert into pp_stat (guild_id, day, what, a, b, n)
		select * from unnest($1::bigint[], $2::bigint[], $3::text[], $4::bigint[], $5::bigint[], $6::bigint[])
		on conflict (guild_id, day, what, a, b) do update set n = pp_stat.n + excluded.n`,
		r.guild, r.day, r.what, r.a, r.b, r.n)
	return err
}

// writeFlush is flush's statements, inside its transaction; guild gives the
// i'th server's row.
func writeFlush(ctx context.Context, tx pgx.Tx, rows *memberRows, ledger *ledgerRows, paid, guilds []int64, guild func(int) []any) error {
	if len(rows.user) > 0 || len(ledger.seq) > 0 {
		_, err := tx.Exec(ctx, `with ledger as (
				insert into pp_ledger (guild_id, seq, user_id, day, reason, delta, inputs, hash)
				select l.g, l.s, l.u, l.d, l.r, l.dl, coalesce(($27::bigint[])[l.o + 1 : l.o + l.n], '{}'), l.h
				from unnest($20::bigint[], $21::bigint[], $22::bigint[], $23::bigint[], $24::text[], $25::bigint[],
					$28::bigint[], $29::bigint[], $26::bytea[]) as l(g, s, u, d, r, dl, o, n, h)
			)
			insert into pp_member (guild_id, user_id, total, purse, season, season_pp, week, week_voice,
				week_chat, day, day_voice, day_chat, day_talk, rested, class, class_season, class_changes, voice_min, messages,
				day_got, buff_lo, buff_hi, day_minutes, day_paid, day_levy, granted, grant_season, rung, day_withheld, quests, quest_day, quests_done, name, class_day, livery, livery_until, pact, signs, laurels, pact_channel, pact_started, withdraws, pact_at)
			select * from unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::bigint[], $5::bigint[], $6::bigint[], $7::bigint[],
				$8::bigint[], $9::bigint[], $10::bigint[], $11::bigint[], $12::bigint[], $13::bigint[], $14::bigint[], $15::text[],
				$16::bigint[], $17::bigint[], $18::bigint[], $19::bigint[],
				$30::bigint[], $31::bigint[], $32::bigint[], $33::bigint[], $34::bigint[], $35::bigint[], $36::bigint[], $37::bigint[], $38::bigint[], $39::bigint[], $40::text[], $41::bigint[], $42::bigint[], $43::text[], $44::bigint[], $45::text[], $46::bigint[], $47::text[], $48::text[], $49::bigint[], $50::bigint[], $51::boolean[], $52::bigint[], $53::bigint[])
			on conflict (guild_id, user_id) do update set total = excluded.total, purse = excluded.purse, season = excluded.season,
				season_pp = excluded.season_pp, week = excluded.week, week_voice = excluded.week_voice,
				week_chat = excluded.week_chat, day = excluded.day, day_voice = excluded.day_voice,
				day_chat = excluded.day_chat, day_talk = excluded.day_talk, rested = excluded.rested,
				class = excluded.class, class_season = excluded.class_season, class_changes = excluded.class_changes,
				voice_min = excluded.voice_min, messages = excluded.messages,
				day_got = excluded.day_got, buff_lo = excluded.buff_lo, buff_hi = excluded.buff_hi,
				day_minutes = excluded.day_minutes, day_paid = excluded.day_paid, day_levy = excluded.day_levy,
				granted = excluded.granted, grant_season = excluded.grant_season, rung = excluded.rung, day_withheld = excluded.day_withheld,
				quests = excluded.quests, quest_day = excluded.quest_day, quests_done = excluded.quests_done,
				name = excluded.name, class_day = excluded.class_day,
				livery = excluded.livery, livery_until = excluded.livery_until,
				pact = excluded.pact, signs = excluded.signs, laurels = excluded.laurels,
				pact_channel = excluded.pact_channel, pact_started = excluded.pact_started,
				withdraws = excluded.withdraws, pact_at = excluded.pact_at`,
			rows.guild, rows.user, rows.total, rows.purse, rows.season, rows.seasonPP, rows.week, rows.weekVoice,
			rows.weekChat, rows.day, rows.dayVoice, rows.dayChat, rows.dayTalk, rows.rested, rows.class, rows.classSeason,
			rows.classChanges, rows.voiceMin, rows.messages,
			ledger.guild, ledger.seq, ledger.user, ledger.day, ledger.reason, ledger.delta, ledger.hash, ledger.inputs,
			ledger.off, ledger.n,
			rows.dayGot, rows.buffLo, rows.buffHi, rows.dayMinutes, rows.dayPaid, rows.dayLevy, rows.granted, rows.grantSeason, rows.rung, rows.dayWithheld, rows.quests, rows.questDay, rows.questsDone, rows.name, rows.classDay, rows.livery, rows.liveryUntil, rows.pact, rows.signs, rows.laurels, rows.pactChannel, rows.pactStarted, rows.withdraws, rows.pactAt)
		if err != nil {
			return err
		}
	}
	if len(paid) > 0 {
		if _, err := tx.Exec(ctx, `update pp_grant set state = 'paid' where id = any($1) and state = 'paying'`, paid); err != nil {
			return err
		}
	}
	// A server's history is an array, which unnest would flatten, so
	// servers go one statement each; there are few, and few change.
	for i := range guilds {
		_, err := tx.Exec(ctx, `insert into pp_guild (guild_id, day, raw, history, lit, lit_before, treasury, levy_open, levy_full, levy_days,
				fund, fund_seeded, season_mint, season_burn, sink_last, heavy)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			on conflict (guild_id) do update set day = excluded.day, raw = excluded.raw, history = excluded.history,
				lit = excluded.lit, lit_before = excluded.lit_before, treasury = excluded.treasury,
				levy_open = excluded.levy_open, levy_full = excluded.levy_full, levy_days = excluded.levy_days,
				fund = excluded.fund, fund_seeded = excluded.fund_seeded,
				season_mint = excluded.season_mint, season_burn = excluded.season_burn, sink_last = excluded.sink_last, heavy = excluded.heavy`,
			guild(i)...)
		if err != nil {
			return err
		}
	}
	return nil
}

// loadBalance sets the balance today runs under: the one saved for today,
// if this boot is a deploy mid-day of the same version, or else next, which
// it saves for today. A saved balance of another version is never run: its
// numbers were written for that build's code, and this build may read a
// field in other units (a joinder saved in pp, read as a share of a pot
// day, once paid 700 times over). The saved one is read over next, so a
// field it predates keeps next's value.
func (st *state) loadBalance(ctx context.Context, db DB, next *Balance, today int64) error {
	rows, err := db.Query(ctx, `select day, body from pp_balance`)
	if err != nil {
		return err
	}
	day, body := int64(-1), []byte(nil)
	for rows.Next() {
		if err := rows.Scan(&day, &body); err != nil {
			rows.Close()
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if day == today {
		b := *next
		if err := json.Unmarshal(body, &b); err != nil {
			return err
		}
		if b.Version == next.Version && b.Sane() == nil {
			st.use(&b)
			return nil
		}
	}
	if err := next.Sane(); err != nil {
		return err
	}
	st.use(next)
	return saveBalance(ctx, db, today, next)
}

// saveBalance records b as the balance day runs under.
func saveBalance(ctx context.Context, db DB, day int64, b *Balance) error {
	body, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `insert into pp_balance (day, body) values ($1, $2)
		on conflict (id) do update set day = excluded.day, body = excluded.body`, day, body)
	return err
}
