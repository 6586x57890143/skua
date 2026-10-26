package pp

import (
	"errors"
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

// Quests dealt and rerolled and notices posted and lapsed are counted by
// day, and a save that fails keeps the counts for the next.
func TestStats(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	today := DayOf(t0)
	a := old(1)
	m := st.member(s, guild, a, today)
	q := m.quests[0]
	if err := st.reroll(s, m, guild, a, 0, today); err != nil {
		t.Fatal(err)
	}
	if _, err := st.post(s, guild, a, true, false, today); err != nil {
		t.Fatal(err)
	}
	st.lapse(s, today+Default.PactLife)
	// A withdrawal that ends a notice, before its work and during it; one
	// that leaves a ring going doesn't.
	b, c, d, e := old(2), old(3), old(4), old(5)
	if _, err := st.post(s, guild, b, false, true, today); err != nil {
		t.Fatal(err)
	}
	if err := st.withdraw(s, b, today); err != nil {
		t.Fatal(err)
	}
	p, _ := st.post(s, guild, c, false, false, today)
	if _, err := st.sign(s, guild, pactKey{opener: c, day: today, at: p.at}, d, today); err != nil {
		t.Fatal(err)
	}
	if err := st.withdraw(s, d, today); err != nil {
		t.Fatal(err)
	}
	if _, err := st.post(s, guild, e, true, false, today); err != nil {
		t.Fatal(err)
	}
	for _, u := range []snowflake.ID{old(6), old(7), old(8)} {
		if _, err := st.sign(s, guild, pactKey{opener: e, day: today}, u, today); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.withdraw(s, old(8), today); err != nil {
		t.Fatal(err)
	}
	if s.stats[statKey{today, statEnded, 1, 1}] != 0 {
		t.Error("a ring left with three counted as ended")
	}
	for k, want := range map[statKey]int64{
		{today, statEnded, 2, 0}:                     1,
		{today, statEnded, 0, 1}:                     1,
		{today, statDealt, q.tier, q.kind}:           1,
		{today, statRerolled, q.tier, q.kind}:        1,
		{today, statPosted, 1, 0}:                    1,
		{today + Default.PactLife, statLapsed, 1, 1}: 1,
	} {
		if s.stats[k] < want {
			t.Errorf("%+v counted %d", k, s.stats[k])
		}
	}
	db := &fakeDB{fail: errors.New("down")}
	n := len(s.stats)
	if _, err := st.flush(t.Context(), db); err == nil || len(s.stats) != n {
		t.Fatalf("a failed save lost counts: %v, %d of %d", err, len(s.stats), n)
	}
	db.fail = nil
	if _, err := st.flush(t.Context(), db); err != nil || len(s.stats) != 0 {
		t.Errorf("a save kept counts: %v", err)
	}
}
