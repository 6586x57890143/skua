package pp

import (
	"context"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// A member stands surety for the guests their own invite brings in
// (LORE.md, the gate book). Which invite a newcomer used is read off the
// server's invites: the one whose uses went up by one since the last look.
// That takes Manage Server, which skua doesn't ask for at install: a server
// that wants sureties gives it to her. Without it, or the server members
// intent, nobody is ever entered and the buff stays off.

// guest is one newcomer in the gate book.
type guest struct {
	// by is who stands surety for them, 0 for nobody: a join no invite
	// accounts for, a bird /pp already knew or an account too young.
	by snowflake.ID
	// day is the day they joined and gone the day they left, 0 while here.
	day, gone int64
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

// counts is whether g, user in s, counts toward their surety's buff
// today, and whether they ever did: they stayed GuestStay days and did
// anything at all. s.mu held.
func (st *state) counts(s *server, user snowflake.ID, g *guest, today int64) (now, ever bool) {
	b := st.bal()
	m, ok := s.members[user]
	if g.by == 0 || !ok || m.messages+m.voiceMin == 0 {
		return false, false
	}
	end := today
	if g.gone > 0 {
		end = g.gone
	}
	ever = end-g.day >= b.GuestStay
	return ever && g.gone == 0 && today-m.dayN < b.GuestSeen, ever
}

// vouched is how many of user's guests count now and how many ever did.
// s.mu held.
func (st *state) vouched(s *server, user snowflake.ID, today int64) (now, ever int64) {
	for u, g := range s.guests {
		if g.by != user {
			continue
		}
		n, e := st.counts(s, u, g, today)
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
	n := map[snowflake.ID]int64{}
	for u, g := range s.guests {
		if now, _ := st.counts(s, u, g, today); now {
			n[g.by]++
		}
	}
	for u, m := range s.members {
		m.wear.Guests = n[u]
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
// they're new: a bird who left and came back stays as they were, and one
// /pp already knew, by itself or with an account under GuestAge old when
// they joined, is entered under nobody. It returns the entry, nil for a
// return. s.mu held.
func (st *state) admit(s *server, user, by snowflake.ID, joined time.Time, today int64) *guest {
	if _, ok := s.guests[user]; ok {
		return nil
	}
	if _, known := s.members[user]; known || by == user || joined.Sub(user.Time()) < st.bal().GuestAge {
		by = 0
	}
	g := &guest{by: by, day: today}
	s.guests[user] = g
	return g
}

// primeInvites takes the first look at guild's invites, so the join after
// it can be told apart.
func (m *Module) primeInvites(guild snowflake.ID) {
	if m.rest == nil {
		return
	}
	s := m.st.server(guild)
	s.inviting.Lock()
	defer s.inviting.Unlock()
	invs, err := m.rest.GetGuildInvites(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.invitesErr = true
		return
	}
	s.invitesErr = false
	s.attribute(invs)
}

// joined enters a newcomer, with whoever's invite brought them in.
func (m *Module) joined(guild snowflake.ID, mem discord.Member) {
	if mem.User.Bot || m.rest == nil {
		return
	}
	s := m.st.server(guild)
	// One join's look at a time, so each is measured against the last.
	s.inviting.Lock()
	defer s.inviting.Unlock()
	invs, err := m.rest.GetGuildInvites(guild)
	if err != nil {
		m.log.Warn("pp: reading invites", "guild", guild, "err", err)
	}
	s.mu.Lock()
	var by snowflake.ID
	if s.invitesErr = err != nil; !s.invitesErr {
		by = s.attribute(invs)
	}
	at := m.now()
	if mem.JoinedAt != nil {
		at = *mem.JoinedAt
	}
	g := m.st.admit(s, mem.User.ID, by, at, DayOf(m.now()))
	s.mu.Unlock()
	if g == nil {
		return
	}
	m.log.Info("pp: guest", "guild", guild, "user", mem.User.ID, "by", g.by)
	m.saveGuest(guild, mem.User.ID, g)
}

// left closes a guest's entry: they never count again.
func (m *Module) left(guild, user snowflake.ID) {
	s := m.st.server(guild)
	today := DayOf(m.now())
	s.mu.Lock()
	g, ok := s.guests[user]
	if !ok || g.gone > 0 {
		s.mu.Unlock()
		return
	}
	g.gone = today
	saved := *g
	if g.by != 0 {
		m.st.sureties(s, today)
	}
	s.mu.Unlock()
	m.saveGuest(guild, user, &saved)
}

// saveGuest writes g's entry. A failed write is lost; a guest it loses
// only stops counting after a restart.
func (m *Module) saveGuest(guild, user snowflake.ID, g *guest) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := m.db.Exec(ctx, `insert into pp_guest (guild_id, user_id, by_id, day, gone) values ($1, $2, $3, $4, $5)
		on conflict (guild_id, user_id) do update set gone = excluded.gone`,
		int64(guild), int64(user), int64(g.by), g.day, g.gone); err != nil {
		m.log.Warn("pp: saving a guest", "guild", guild, "user", user, "err", err)
	}
}

// loadGuests reads the gate book.
func (st *state) loadGuests(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select guild_id, user_id, by_id, day, gone from pp_guest`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guild, user, by int64
		g := &guest{}
		if err := rows.Scan(&guild, &user, &by, &g.day, &g.gone); err != nil {
			return err
		}
		g.by = snowflake.ID(by)
		st.server(snowflake.ID(guild)).guests[snowflake.ID(user)] = g
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
	for u, g := range s.guests {
		if g.by == 0 {
			continue
		}
		vouched++
		if now, _ := m.st.counts(s, u, g, today); now {
			counting++
		}
	}
	return fmt.Sprintf("%d joined · %d vouched for · %d counting", len(s.guests), vouched, counting)
}
