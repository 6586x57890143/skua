package pp

import (
	"context"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// A bird may ask the yard to renew their livery on its own: at the turn of
// its lease's last day, another term is paid from the purse at that day's
// price, as if they'd pressed for it. A purse that can't pay is left
// alone, and the livery fades as it would have. Opting in is kept in
// pp_renew, apart from the member's row.

var errNoLivery = core.Tell("you wear no livery to renew; lease one first")

// renewButton is the switch on a worn livery's card.
func renewButton(on bool, days int64) discord.ButtonComponent {
	if on {
		return withIcon(discord.NewSuccessButton("renews each "+termWord(days), "pp:yardrenew"), "pot")
	}
	return withIcon(discord.NewSecondaryButton("renew each "+termWord(days), "pp:yardrenew"), "pot")
}

// toggleRenew flips user's renewing and keeps it.
func (m *Module) toggleRenew(ctx context.Context, guild, user snowflake.ID) error {
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, user, DayOf(m.now()))
	if mem.livery == "" {
		s.mu.Unlock()
		return errNoLivery
	}
	on := !mem.renew
	s.mu.Unlock()
	q := `delete from pp_renew where guild_id = $1 and user_id = $2`
	if on {
		q = `insert into pp_renew (guild_id, user_id) values ($1, $2) on conflict do nothing`
	}
	if _, err := m.db.Exec(ctx, q, int64(guild), int64(user)); err != nil {
		return err
	}
	s.mu.Lock()
	m.st.member(s, guild, user, DayOf(m.now())).renew = on
	s.mu.Unlock()
	return nil
}

// loadRenew reads who renews.
func (st *state) loadRenew(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select guild_id, user_id from pp_renew`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guild, user int64
		if err := rows.Scan(&guild, &user); err != nil {
			return err
		}
		s := st.server(snowflake.ID(guild))
		m, ok := s.members[snowflake.ID(user)]
		if !ok {
			continue
		}
		m.renew = true
	}
	return rows.Err()
}

// renewLeases pays another term for every renewing livery on its last
// day, before fade looks. The role is still worn, so nothing goes to
// Discord.
func (m *Module) renewLeases(today int64) {
	for _, s := range m.st.servers() {
		s.leasing.Lock()
		s.mu.Lock()
		if !s.released(today) {
			s.mu.Unlock()
			s.leasing.Unlock()
			continue
		}
		for user, mem := range s.members {
			if !mem.renew || mem.livery == "" || mem.liveryUntil > today+1 || today >= mem.liveryUntil+fadeDays {
				continue
			}
			var price, days, dyed int64
			if mem.livery == ownKey {
				worn := s.own[user]
				if worn.role == 0 {
					continue
				}
				price, days, _ = m.st.ownLease(s, user, worn.week, mem.rung)
				dyed = -1
			} else {
				d, ok := dyeOf(mem.livery)
				if !ok || mem.rung < d.rung {
					continue
				}
				var weeks int64
				days, weeks, _ = term(mem.rung)
				price, dyed = m.st.leasePrice(d)*weeks, int64(dyeIndex(d.key))
			}
			if mem.purse < price {
				continue
			}
			mem.purse -= price
			mem.liveryUntil = max(today, mem.liveryUntil) + days
			m.st.burn(s, user, today, reasonLease, price, []int64{dyed, mem.liveryUntil})
			mem.dirty = true
		}
		s.mu.Unlock()
		s.leasing.Unlock()
	}
}
