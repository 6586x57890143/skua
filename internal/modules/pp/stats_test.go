package pp

import (
	"errors"
	"testing"
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
	if _, err := st.post(s, guild, a, true, today); err != nil {
		t.Fatal(err)
	}
	st.lapse(s, today+Default.PactLife)
	for k, want := range map[statKey]int64{
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
