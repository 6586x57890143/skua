package pp

import (
	"bytes"
	"context"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// The chain is walkable from the rows alone, and changing any row breaks
// every hash after it.
func TestLedgerChain(t *testing.T) {
	s := newServer(guild)
	s.append(old(1), 10, reasonOpen, 500, nil)
	s.append(old(1), 11, reasonLabour, 1200, []int64{1, 2, 3})
	s.append(old(2), 11, reasonClass, -300, nil)
	walk := func(es []entry) bool {
		var prev []byte
		for _, e := range es {
			if !bytes.Equal(chain(prev, guild, e), e.hash) {
				return false
			}
			prev = e.hash
		}
		return true
	}
	if !walk(s.pending) || !bytes.Equal(s.head, s.pending[2].hash) || s.seq != 3 {
		t.Fatal("the chain doesn't walk")
	}
	forged := append([]entry{}, s.pending...)
	forged[1].delta = 120000
	if walk(forged) {
		t.Error("a rewritten row still walks")
	}
	if bytes.Equal(chain(nil, guild, s.pending[0]), chain(nil, guild+1, s.pending[0])) {
		t.Error("another server's chain gives the same hash")
	}
}

// What a member earns depends on what they do that day, never on how long
// they've played, what they hold or their class: a veteran and a newcomer
// doing the same things are paid the same, to the hundredth.
func TestTenureBlind(t *testing.T) {
	play := func(veteran bool) (pays []int64, day int64) {
		st := newState(&Default, 42)
		s := st.server(guild)
		a, b := old(1), old(2)
		if veteran {
			m := st.member(s, guild, a, DayOf(t0))
			m.total, m.purse, m.class, m.classSeason, m.classChanges, m.voiceMin, m.messages = 9e9, 5e8, "knight", 1, 4, 1e5, 1e5
			m.seasonPP, m.weekAct = 7e7, [Boards]int64{1e6, 1e6}
		}
		st.sit(s, guild, a, in(room), false, t0)
		st.sit(s, guild, b, in(room), false, t0)
		for i := range 600 {
			now := t0.Add(time.Duration(i) * time.Minute)
			before := s.members[a].dayGot
			st.minute(s, guild, afkCh, now)
			if i%3 == 0 {
				st.chat(s, guild, a, "a real thing to say, number "+string(rune('a'+i%26)), now.Add(30*time.Second))
			}
			pays = append(pays, s.members[a].dayGot-before)
		}
		return pays, s.members[a].dayGot
	}
	newcomer, n := play(false)
	veteran, v := play(true)
	if n != v || n == 0 {
		t.Fatalf("a day paid a newcomer %d and a veteran %d", n, v)
	}
	for i := range newcomer {
		if newcomer[i] != veteran[i] {
			t.Fatalf("minute %d paid a newcomer %d and a veteran %d", i, newcomer[i], veteran[i])
		}
	}
}

// However a day's minutes and messages fell, and whatever buff each was
// paid under, what it paid lies in the bracket its ledger row gives.
func TestBracket(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	for range 2000 {
		pool := b.Pool * (85 + rng.Int64N(31)) / 100
		d := Day{Rested: rng.Int64N(b.Pool*b.RestedMax/100 + 1)}
		p := Pools{Pool: pool}
		lo, hi := rng.Int64N(20), int64(0)
		hi = lo + rng.Int64N(30)
		var got int64
		for range rng.IntN(900) {
			buff := lo + rng.Int64N(hi-lo+1)
			if rng.IntN(3) == 0 {
				got += b.Earn(&d, p, true, b.ChatRaw(rng.Uint64()), buff)
			} else {
				got += b.Earn(&d, p, false, b.VoiceBase*(5+rng.Int64N(146))/100, buff)
			}
		}
		least, most := b.Bracket(d.Voice, d.Chat, d.Rested, pool, lo, hi, d.Minutes+d.Paid)
		if got < least || got > most {
			t.Fatalf("a day paid %d, outside %d..%d (%+v, pool %d, buff %d..%d)", got, least, most, d, pool, lo, hi)
		}
	}
}

// A member's ledger rows sum to their purse less the open day, through
// earning, a day turning and a class change paid for, and a member with
// no rows opens the ledger at their purse.
func TestLedgerConserves(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	a := old(1)
	sum := func() (n int64) {
		for _, e := range s.pending {
			if e.user == a {
				n += e.delta
			}
		}
		return n
	}
	check := func(when string) {
		t.Helper()
		m := s.members[a]
		if sum() != m.purse-m.dayGot {
			t.Fatalf("%s: rows sum to %d, purse %d less the open day %d", when, sum(), m.purse, m.dayGot)
		}
	}
	for i := range 40 {
		st.chat(s, guild, a, "something worth paying for", t0.Add(time.Duration(i)*time.Minute))
	}
	check("mid-day")
	if s.members[a].dayGot == 0 {
		t.Fatal("the day paid nothing")
	}
	// The next day's first event closes the day before into one row, and
	// the treasury's levy into another.
	next := t0.Add(24 * time.Hour)
	st.chat(s, guild, a, "a new day", next)
	check("the day after")
	if len(s.pending) != 2 || s.pending[0].reason != reasonLabour || len(s.pending[0].inputs) != 11 ||
		s.pending[1].reason != reasonLevy || s.pending[1].user != 0 || s.pending[1].delta != s.pending[0].inputs[9] {
		t.Fatalf("the closed day wrote %+v", s.pending)
	}
	if s.treasury-s.levyOpen != s.pending[1].delta || s.pending[1].delta == 0 {
		t.Errorf("the treasury holds %d with %d of today's, its row says %d", s.treasury, s.levyOpen, s.pending[1].delta)
	}
	// The row's pay and levy together lie in the bracket of what the day
	// was earned from.
	in := s.pending[0].inputs
	least, most := b.Bracket(in[0], in[1], in[3], in[4], in[5], in[6], in[7]+in[8])
	if d := s.pending[0].delta + in[9]; d < least || d > most {
		t.Errorf("the day's row says %d, outside its bracket %d..%d", d, least, most)
	}

	// A server's day turning closes everyone's, not only whoever shows up.
	st.chat(s, guild, old(2), "here too", next)
	st.minute(s, guild, afkCh, next.Add(24*time.Hour))
	if s.members[old(2)].dayGot != 0 || s.members[a].dayGot != 0 {
		t.Error("the day turned with days left open")
	}
	check("after the server's day turned")

	m := s.members[a]
	m.purse += 1_000_000 // a class change they can afford
	s.append(a, s.day, reasonOpen, 1_000_000, nil)
	if err := (&Module{st: st, now: func() time.Time { return next.Add(24 * time.Hour) }}).pick(guild, a, "knight"); err != nil {
		t.Fatal(err)
	}
	// Past the grace, so it's paid for.
	if err := (&Module{st: st, now: func() time.Time { return next.Add(72 * time.Hour) }}).pick(guild, a, "monk"); err != nil {
		t.Fatal(err)
	}
	if last := s.pending[len(s.pending)-1]; last.reason != reasonClass || last.delta >= 0 {
		t.Errorf("a paid change wrote %+v", last)
	}
	check("after a class change")
}

// Against a real database: a flush writes the rows with the members, a load
// picks the chain up where it stopped, and the audit opens ledgers for
// members who have none and finds a purse that disagrees.
func TestLedgerRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	st := newState(&Default, 42)
	s := st.server(g)
	a, b2 := old(1), old(2)
	st.chat(s, g, a, "a real thing to say", t0)
	st.chat(s, g, a, "a new day", t0.Add(24*time.Hour))
	// b2 had a purse before the ledger began. Their row has no inputs, and
	// a flush of only such rows writes too.
	st.member(s, g, b2, DayOf(t0)).purse = 4200
	if _, err := st.flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	if n, err := st.audit(ctx, db); err != nil || n != 0 {
		t.Fatalf("audit: %d, %v", n, err)
	}
	if _, err := st.flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	if len(s.pending) != 0 {
		t.Fatal("rows left after a flush")
	}

	back := newState(&Default, 42)
	if err := back.load(ctx, db); err != nil {
		t.Fatal(err)
	}
	bs := back.server(g)
	if bs.seq != s.seq || !bytes.Equal(bs.head, s.head) {
		t.Fatalf("the chain came back at %d %x; want %d %x", bs.seq, bs.head, s.seq, s.head)
	}
	if n, err := back.audit(ctx, db); err != nil || n != 0 || len(bs.pending) != 0 {
		t.Fatalf("the audit after a load: %d, %v, %d new rows", n, err, len(bs.pending))
	}
	// Each row keeps its own inputs out of the flat array they went in.
	rows, err := db.Query(ctx, `select reason, inputs from pp_ledger where guild_id = $1 order by seq`, int64(g))
	if err != nil {
		t.Fatal(err)
	}
	var shapes []int
	for rows.Next() {
		var reason string
		var in []int64
		if err := rows.Scan(&reason, &in); err != nil {
			t.Fatal(err)
		}
		shapes = append(shapes, len(in))
	}
	rows.Close()
	if len(shapes) < 3 || shapes[0] != 11 || slices.Max(shapes[1:]) != 0 {
		t.Errorf("rows came back with %v inputs", shapes)
	}
	bs.members[a].purse += 100
	if n, _ := back.audit(ctx, db); n != 1 {
		t.Errorf("a purse that disagrees passed the audit: %d", n)
	}
}
