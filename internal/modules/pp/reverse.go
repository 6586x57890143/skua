package pp

import (
	"context"
	"fmt"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// reasonCorrect is a row put right by an operator: inputs the seq of the
// row it undoes.
const reasonCorrect = "correct"

// Reversal is one member's bad joinder undone: what it took back, what it
// refunded and the joinder paid in its place.
type Reversal struct {
	User                     snowflake.ID
	TookBack, Refunded, Paid int64
	RungFrom, RungTo         int64
	Faded                    bool
}

// Reverse undoes a joinder paid wrong, at seq from or after, for each of
// users in guild, and everything they bought since, and pays the joinder
// that should have been. The ledger keeps every row: each one undone gets
// a correct row beside it naming its seq, so the chain still holds and the
// audit still balances. A livery leased since is set to fade on the bot's
// next turn, which takes its role off. Run it with the bot stopped: the
// bot's memory would overwrite it.
func Reverse(ctx context.Context, db DB, guild snowflake.ID, from int64, users []snowflake.ID) ([]Reversal, error) {
	st := newState(&Default, 0)
	if err := st.loadBalance(ctx, db, &Default, DayOf(time.Now())); err != nil {
		return nil, err
	}
	if err := st.load(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, `select seq, user_id, day, reason, delta, inputs from pp_ledger
		where guild_id = $1 and seq >= $2 and user_id = any($3) and reason in ('joinder', 'shop', 'lease') order by seq`,
		int64(guild), from, ids(users))
	if err != nil {
		return nil, err
	}
	byUser := map[snowflake.ID][]entry{}
	for rows.Next() {
		var e entry
		var user int64
		if err := rows.Scan(&e.seq, &user, &e.day, &e.reason, &e.delta, &e.inputs); err != nil {
			rows.Close()
			return nil, err
		}
		e.user = snowflake.ID(user)
		byUser[e.user] = append(byUser[e.user], e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s := st.server(guild)
	s.mu.Lock()
	var out []Reversal
	for _, u := range users {
		r, err := st.reverse(s, u, byUser[u], DayOf(time.Now()))
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		out = append(out, r)
	}
	s.mu.Unlock()
	if _, err := st.flush(ctx, db); err != nil {
		return nil, err
	}
	return out, nil
}

// reverse undoes user's rows, the bad joinder first among them, as
// Reverse says. s.mu held.
func (st *state) reverse(s *server, user snowflake.ID, rows []entry, today int64) (Reversal, error) {
	m, ok := s.members[user]
	r := Reversal{User: user}
	if !ok || len(rows) == 0 || rows[0].reason != reasonJoinder || len(rows[0].inputs) != 4 {
		return r, fmt.Errorf("pp: %d has no joinder to reverse at its first row", user)
	}
	bad := rows[0]
	n, got, lv := bad.inputs[0], bad.inputs[1], bad.inputs[2]
	if bad.day != s.day {
		// Its levy is still open only on the day it was paid.
		return r, fmt.Errorf("pp: %d's joinder was paid on day %d, not today", user, bad.day)
	}
	m.purse -= bad.delta
	m.total -= got
	m.seasonPP -= got
	s.seasonMint -= got
	s.treasury -= lv
	s.levyOpen -= lv
	s.levyFull -= Rules.levy(got)
	s.append(user, today, reasonCorrect, -bad.delta, []int64{bad.seq})
	r.TookBack, r.RungFrom, r.RungTo = bad.delta, m.rung, m.rung
	for _, e := range rows[1:] {
		refund := -e.delta
		switch {
		case e.reason == reasonShop && len(e.inputs) == 1:
			r.RungTo = min(r.RungTo, e.inputs[0]-1)
		case e.reason == reasonLease && len(e.inputs) == 2 && e.inputs[1] == m.liveryUntil:
			// ponytail: assumes the lease was their first; one that
			// extended an earlier lease would cut that short too.
			m.liveryUntil, r.Faded = today-fadeDays, true
		default:
			return r, fmt.Errorf("pp: %d's row %d (%s) isn't one Reverse undoes", user, e.seq, e.reason)
		}
		m.purse += refund
		s.seasonBurn -= refund
		r.Refunded += refund
		s.append(user, today, reasonCorrect, refund, []int64{e.seq})
	}
	m.rung = r.RungTo
	pay := st.bal().JoinderPay(int(min(max(n, 2), 4)))
	plv, kept := st.labour(s, m, pay)
	r.Paid = pay - plv - kept
	s.append(user, today, reasonJoinder, r.Paid, []int64{n, pay, plv, kept})
	m.dirty, s.dirty = true, true
	return r, nil
}

func ids(us []snowflake.ID) []int64 {
	out := make([]int64, len(us))
	for i, u := range us {
		out[i] = int64(u)
	}
	return out
}
