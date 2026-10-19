package pp

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"github.com/disgoorg/snowflake/v2"
)

// The ledger is every change to a purse, in order per server, so that
// every pp can be traced to the day of time it was earned in or to where it
// went. Each row is chained to the one before by hash; a head posted
// outside the bot pins everything before it. The rows go to the database
// in the same statement as the members they change, so the two never
// disagree.

// What a ledger row is for.
const (
	reasonOpen   = "open"   // a purse as it stood when the ledger began
	reasonLabour = "labour" // a closed day of earning
	reasonClass  = "class"  // a class change paid for
	reasonLevy   = "levy"   // a day's levy, into the treasury (user 0)
	reasonGrant  = "grant"  // a grant: out of the contest fund, into a purse
	reasonFund   = "fund"   // the contest fund's share of a quest, minted into the treasury
	reasonSeed   = "seed"   // the contest fund's one seed, the same way
	reasonPrize  = "prize"  // a weekly checkpoint's prize, the same way
	reasonShop   = "shop"   // something bought, burned
	reasonQuest  = "quest"  // a finished quest, as labour
)

// entry is one ledger row.
type entry struct {
	user     snowflake.ID
	seq, day int64
	reason   string
	delta    int64
	inputs   []int64
	hash     []byte
}

// chain is e's hash: sha256 over the hash before it, the server and every
// field of e, each number as 8 bytes big endian and the reason after its
// length. It is all an auditor needs to walk the chain from the rows.
func chain(prev []byte, guild snowflake.ID, e entry) []byte {
	h := sha256.New()
	h.Write(prev)
	var n [8]byte
	put := func(v int64) {
		binary.BigEndian.PutUint64(n[:], uint64(v))
		h.Write(n[:])
	}
	put(int64(guild))
	put(e.seq)
	put(int64(e.user))
	put(e.day)
	put(int64(len(e.reason)))
	h.Write([]byte(e.reason))
	put(e.delta)
	put(int64(len(e.inputs)))
	for _, v := range e.inputs {
		put(v)
	}
	return h.Sum(nil)
}

// append chains a row onto s's ledger, to be written with the next flush.
// s.mu held.
func (s *server) append(user snowflake.ID, day int64, reason string, delta int64, inputs []int64) {
	s.seq++
	e := entry{user: user, seq: s.seq, day: day, reason: reason, delta: delta, inputs: inputs}
	e.hash = chain(s.head, s.id, e)
	s.head = e.hash
	s.pending = append(s.pending, e)
}

// close writes m's open day into the ledger: what it paid and everything it
// was earned from, the day's pool among them, so the pay can be checked
// against the engine once the season's seed is out. s.mu held.
func (st *state) close(s *server, user snowflake.ID, m *member) {
	if m.dayGot == 0 {
		return
	}
	d := m.day
	s.append(user, m.dayN, reasonLabour, m.dayGot, []int64{
		d.Voice, d.Chat, d.Talk, d.Rested, st.pools(s.id, user, m.dayN).Pool, m.buffLo, m.buffHi, d.Minutes, d.Paid, m.dayLevy, m.dayWithheld,
	})
	m.dayGot, m.dayLevy, m.dayWithheld, m.dirty = 0, 0, 0, true
}

// headHex is s's head as it is posted. s.mu held.
func (s *server) headHex() string { return hex.EncodeToString(s.head) }

// loadLedger reads where each server's chain stands.
func (st *state) loadLedger(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select distinct on (guild_id) guild_id, seq, hash from pp_ledger order by guild_id, seq desc`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guild, seq int64
		var head []byte
		if err := rows.Scan(&guild, &seq, &head); err != nil {
			return err
		}
		s := st.server(snowflake.ID(guild))
		s.mu.Lock()
		s.seq, s.head = seq, head
		s.mu.Unlock()
	}
	return rows.Err()
}

// audit holds every member's ledger to their purse: what their rows sum to,
// with those not yet written, has to be their purse less the day still
// open. A member with no rows yet opens the ledger at their purse. It
// returns how many members disagree, which should only ever be none.
// Call it from the goroutine that flushes, so no rows land mid-audit.
func (st *state) audit(ctx context.Context, db DB) (int, error) {
	type key struct{ guild, user snowflake.ID }
	sums := map[key]int64{}
	rows, err := db.Query(ctx, `select guild_id, user_id, sum(delta)::bigint from pp_ledger group by guild_id, user_id`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var guild, user, sum int64
		if err := rows.Scan(&guild, &user, &sum); err != nil {
			rows.Close()
			return 0, err
		}
		sums[key{snowflake.ID(guild), snowflake.ID(user)}] = sum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	bad := 0
	for g, s := range st.servers() {
		s.mu.Lock()
		for _, e := range s.pending {
			sums[key{g, e.user}] += e.delta
		}
		for u, m := range s.members {
			want := m.purse - m.dayGot
			got, ok := sums[key{g, u}]
			switch {
			case !ok && want != 0:
				s.append(u, m.dayN, reasonOpen, want, nil)
			case ok && got != want:
				bad++
			}
		}
		// The treasury is user 0: its rows sum to what it holds less the
		// levy not yet written.
		if sums[key{g, 0}] != s.treasury-s.levyOpen {
			bad++
		}
		s.mu.Unlock()
	}
	return bad, nil
}

// ledgerRows is a flush's ledger rows as columns, for one unnest.
type ledgerRows struct {
	guild, seq, user, day, delta []int64
	reason                       []string
	// inputs go as one flat array with where each row's start and how many
	// beside it, since unnest would flatten an array of arrays.
	inputs, off, n []int64
	hash           [][]byte
}

func (r *ledgerRows) add(guild snowflake.ID, e entry) {
	r.guild = append(r.guild, int64(guild))
	r.seq = append(r.seq, e.seq)
	r.user = append(r.user, int64(e.user))
	r.day = append(r.day, e.day)
	r.reason = append(r.reason, e.reason)
	r.delta = append(r.delta, e.delta)
	r.off = append(r.off, int64(len(r.inputs)))
	r.n = append(r.n, int64(len(e.inputs)))
	r.inputs = append(r.inputs, e.inputs...)
	r.hash = append(r.hash, e.hash)
}
