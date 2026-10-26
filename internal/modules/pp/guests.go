package pp

import (
	"context"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
)

// A member stands surety for the guests their own invite brings in
// (LORE.md, the gate book). Which invite a newcomer used is read off the
// server's invites: the one whose uses went up by one since the last look.
// That takes Manage Server, which skua doesn't ask for at install: a server
// that wants sureties gives it to her. Without it, or the server members
// intent, nobody is ever entered under anyone and the buff stays off.

// lookAgain is how long a server that refused skua its invites waits
// before she asks again, so a server without Manage Server costs Discord
// one refusal an hour rather than one a join.
const lookAgain = time.Hour

// guest is one bird in the gate book: everyone who joined or left since
// sureties began.
type guest struct {
	// by is who stands surety for them, 0 for nobody: a join no invite
	// accounts for, a bird who was here before or an account too young.
	by snowflake.ID
	// day is the day they joined and gone the day they left, 0 while here;
	// seen is the last day they earned while here, 0 for never.
	day, gone, seen int64
}

// invite is what the last look saw of one invite: who made it and how
// often it's been used, of how many (0 for no limit).
type invite struct {
	by        snowflake.ID
	uses, max int
}

// keys are the badge a surety earns, by how many guests ever counted.
var keys = []struct {
	at         int64
	icon, name string
}{{1, "key", "iron key"}, {3, "key_gold", "gold key"}, {10, "freedom", "freedom of the city"}}

// keyOf is the badge for ever guests, "" before the first.
func keyOf(ever int64) (icon, name string) {
	for _, k := range keys {
		if ever >= k.at {
			icon, name = k.icon, k.name
		}
	}
	return icon, name
}

// counts is whether g counts toward their surety's buff today, and whether
// they ever did: they stayed GuestStay days and earned while here. Earning
// is the test, not a message or a seat: it takes company in voice or real
// talk, which an account parked in a channel doesn't have.
func (st *state) counts(g *guest, today int64) (now, ever bool) {
	b := st.bal()
	if g.by == 0 || g.seen == 0 {
		return false, false
	}
	end := today
	if g.gone > 0 {
		end = g.gone
	}
	ever = end-g.day >= b.GuestStay
	return ever && g.gone == 0 && today-g.seen < b.GuestSeen, ever
}

// vouched is how many of user's guests count now and how many ever did.
// s.mu held.
func (st *state) vouched(s *server, user snowflake.ID, today int64) (now, ever int64) {
	for _, u := range s.vouchers[user] {
		n, e := st.counts(s.guests[u], today)
		if n {
			now++
		}
		if e {
			ever++
		}
	}
	return now, ever
}

// sureties sets what every member in s wears for their guests today.
// s.mu held.
func (st *state) sureties(s *server, today int64) {
	for u, m := range s.members {
		m.wear.Guests, _ = st.vouched(s, u, today)
	}
}

// earned marks user seen today if they're a guest still here, and brings
// their surety's buff up to date with it. s.mu held.
func (st *state) earned(s *server, user snowflake.ID, today int64) {
	g, ok := s.guests[user]
	if !ok || g.gone > 0 || g.seen == today {
		return
	}
	g.seen = today
	s.seenGuests = append(s.seenGuests, user)
	if h, ok := s.members[g.by]; ok && g.by != 0 {
		h.wear.Guests, _ = st.vouched(s, g.by, today)
	}
}

// enter puts g in the gate book as user. s.mu held.
func (s *server) enter(user snowflake.ID, g *guest) {
	s.guests[user] = g
	if g.by != 0 {
		s.vouchers[g.by] = append(s.vouchers[g.by], user)
	}
}

// attribute takes the server's invites as they stand after a join and
// returns who made the one it used: the only one whose uses went up, by
// one, since the last look, or a limited one that's gone at its last use.
// Two joins between looks, a join by the vanity link and the first look
// since skua came up are nobody's. s.mu held.
func (s *server) attribute(invs []discord.ExtendedInvite) snowflake.ID {
	primed := s.invites != nil
	next := make(map[string]invite, len(invs))
	var by snowflake.ID
	hits := 0
	for _, inv := range invs {
		var who snowflake.ID
		if inv.Inviter != nil && !inv.Inviter.Bot {
			who = inv.Inviter.ID
		}
		next[inv.Code] = invite{by: who, uses: inv.Uses, max: inv.MaxUses}
		if d := inv.Uses - s.invites[inv.Code].uses; d > 0 {
			hits += d
			by = who
		}
	}
	// ponytail: a limited invite that expired one short of its last use
	// looks used; it only ever makes a join nobody's, never someone else's.
	for code, old := range s.invites {
		if _, ok := next[code]; !ok && old.max > 0 && old.uses == old.max-1 {
			hits++
			by = old.by
		}
	}
	s.invites = next
	if !primed || hits != 1 {
		return 0
	}
	return by
}

// admit enters user in the gate book with by standing surety, as long as
// they're new. A bird the book already has stays as they were, and one
// who was here before (Discord says so, or /pp knew them), with an account
// under GuestAge old when they joined or who would vouch for themselves is
// entered under nobody. It returns the entry, nil for one already there.
// s.mu held.
func (st *state) admit(s *server, user, by snowflake.ID, rejoin bool, joined time.Time, today int64) *guest {
	if _, ok := s.guests[user]; ok {
		return nil
	}
	if _, known := s.members[user]; known || rejoin || by == user || joined.Sub(user.Time()) < st.bal().GuestAge {
		by = 0
	}
	g := &guest{by: by, day: today}
	s.enter(user, g)
	return g
}

// look reads guild's invites for s, unless it was refused within
// lookAgain, and reports whether it got them. s.inviting held.
func (m *Module) look(ctx context.Context, guild snowflake.ID, s *server) ([]discord.ExtendedInvite, bool) {
	s.mu.Lock()
	wait := s.invitesErr && m.now().Sub(s.invitesAt) < lookAgain
	s.mu.Unlock()
	if wait || m.rest == nil {
		return nil, false
	}
	invs, err := m.rest.GetGuildInvites(guild, rest.WithCtx(ctx))
	s.mu.Lock()
	s.invitesErr, s.invitesAt = err != nil, m.now()
	s.mu.Unlock()
	if err != nil {
		m.log.Warn("pp: reading invites", "guild", guild, "err", err)
		return nil, false
	}
	return invs, true
}

// primeInvites takes the first look at guild's invites, so the join after
// it can be told apart.
func (m *Module) primeInvites(ctx context.Context, guild snowflake.ID) {
	s := m.st.server(guild)
	s.inviting.Lock()
	defer s.inviting.Unlock()
	if invs, ok := m.look(ctx, guild, s); ok {
		s.mu.Lock()
		s.attribute(invs)
		s.mu.Unlock()
	}
}

// joined enters a newcomer, with whoever's invite brought them in.
func (m *Module) joined(ctx context.Context, guild snowflake.ID, mem discord.Member) {
	if mem.User.Bot {
		return
	}
	s := m.st.server(guild)
	// One join's look at a time, so each is measured against the last.
	s.inviting.Lock()
	defer s.inviting.Unlock()
	invs, ok := m.look(ctx, guild, s)
	at := m.now()
	if mem.JoinedAt != nil {
		at = *mem.JoinedAt
	}
	s.mu.Lock()
	var by snowflake.ID
	if ok {
		by = s.attribute(invs)
	} else {
		s.invites = nil // a look missed, so the next can't be told apart
	}
	g := m.st.admit(s, mem.User.ID, by, mem.Flags.Has(discord.MemberFlagDidRejoin), at, DayOf(m.now()))
	s.mu.Unlock()
	if g == nil {
		return
	}
	m.log.Info("pp: guest", "guild", guild, "user", mem.User.ID, "by", g.by)
	m.saveGuest(ctx, guild, mem.User.ID, *g)
}

// left closes a bird's entry, or opens a closed one for a bird the book
// hadn't seen join, so they never count and can't be entered again: a
// leave handled before its join comes to the same.
func (m *Module) left(ctx context.Context, guild, user snowflake.ID) {
	s := m.st.server(guild)
	today := DayOf(m.now())
	s.mu.Lock()
	g, ok := s.guests[user]
	if !ok {
		g = &guest{day: today}
		s.enter(user, g)
	}
	if g.gone > 0 {
		s.mu.Unlock()
		return
	}
	g.gone = today
	saved := *g
	if h, ok := s.members[g.by]; ok && g.by != 0 {
		h.wear.Guests, _ = m.st.vouched(s, g.by, today)
	}
	s.mu.Unlock()
	m.saveGuest(ctx, guild, user, saved)
}

// saveGuest writes g's entry. A failed write is lost; a guest it loses
// only stops counting after a restart.
func (m *Module) saveGuest(ctx context.Context, guild, user snowflake.ID, g guest) {
	var r guestRows
	r.add(guild, user, &g)
	if _, err := m.db.Exec(ctx, upsertGuests, r.guild, r.user, r.by, r.day, r.gone, r.seen); err != nil {
		m.log.Warn("pp: saving a guest", "guild", guild, "user", user, "err", err)
	}
}

// loadGuests reads the gate book.
func (st *state) loadGuests(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select guild_id, user_id, by_id, day, gone, seen from pp_guest`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guild, user, by int64
		g := &guest{}
		if err := rows.Scan(&guild, &user, &by, &g.day, &g.gone, &g.seen); err != nil {
			return err
		}
		g.by = snowflake.ID(by)
		st.server(snowflake.ID(guild)).enter(snowflake.ID(user), g)
	}
	return rows.Err()
}

// gateRow is the gate book on the admins' report: who joined under whose
// surety and who counts, or why nobody can be entered. s.mu held.
func (m *Module) gateRow(s *server) string {
	switch {
	case s.invitesErr:
		return "can't read invites: give skua manage server"
	case len(s.guests) == 0:
		return "nobody yet; joins need the server members intent"
	}
	today := DayOf(m.now())
	var vouched, counting int
	for _, us := range s.vouchers {
		for _, u := range us {
			vouched++
			if now, _ := m.st.counts(s.guests[u], today); now {
				counting++
			}
		}
	}
	return fmt.Sprintf("%d in the book · %d vouched for · %d counting", len(s.guests), vouched, counting)
}

// upsertGuests writes gate book rows. A leave or a seen day only ever
// moves forward, so whichever write lands last keeps both.
const upsertGuests = `insert into pp_guest (guild_id, user_id, by_id, day, gone, seen)
	select * from unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::bigint[], $5::bigint[], $6::bigint[])
	on conflict (guild_id, user_id) do update set gone = greatest(pp_guest.gone, excluded.gone),
		seen = greatest(pp_guest.seen, excluded.seen)`

// guestRows is a batch of gate book rows as columns, for one unnest.
type guestRows struct{ guild, user, by, day, gone, seen []int64 }

func (r *guestRows) add(guild, user snowflake.ID, g *guest) {
	r.guild, r.user, r.by = append(r.guild, int64(guild)), append(r.user, int64(user)), append(r.by, int64(g.by))
	r.day, r.gone, r.seen = append(r.day, g.day), append(r.gone, g.gone), append(r.seen, g.seen)
}

// write upserts the rows, inside the flush's transaction.
func (r *guestRows) write(ctx context.Context, tx pgx.Tx) error {
	if len(r.user) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, upsertGuests, r.guild, r.user, r.by, r.day, r.gone, r.seen)
	return err
}
