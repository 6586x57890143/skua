package pp

import "github.com/disgoorg/snowflake/v2"

// Stats are counts of what members do that the ledger doesn't hold, a day
// at a time, for tuning: quests dealt, rerolled and finished by tier and
// kind, and notices posted, finished and lapsed by kind. They're saved
// with the next flush into pp_stat, added to what's there, and read only
// by hand, as aggregates.

// What a stat counts. For a quest a and b are its tier and kind; for a
// notice a is 1 for a round robin and b how many signed or were paid.
const (
	statDealt    = "quest_dealt"
	statRerolled = "quest_rerolled" // the quest a reroll replaced
	statFinished = "quest_finished"
	statPosted   = "notice_posted"
	statJoined   = "notice_finished"  // b is how many were paid
	statLapsed   = "notice_lapsed"    // b is how many had signed
	statLeft     = "notice_withdrawn" // b is 1 once its work had started
)

// statKey is one counter: a day, what it counts and its two particulars.
type statKey struct {
	day  int64
	what string
	a, b int
}

// count adds one to s's counter. s.mu held.
func (s *server) count(day int64, what string, a, b int) {
	if s.stats == nil {
		s.stats = map[statKey]int64{}
	}
	s.stats[statKey{day, what, a, b}]++
}

// statRows are counters taken for a flush, as columns for one unnest.
type statRows struct {
	guild, day []int64
	what       []string
	a, b, n    []int64
}

func (r *statRows) add(guild snowflake.ID, k statKey, n int64) {
	r.guild = append(r.guild, int64(guild))
	r.day = append(r.day, k.day)
	r.what = append(r.what, k.what)
	r.a = append(r.a, int64(k.a))
	r.b = append(r.b, int64(k.b))
	r.n = append(r.n, n)
}

// bit is b as a stat's particular has it: 1 for true.
func bit(b bool) int {
	if b {
		return 1
	}
	return 0
}
