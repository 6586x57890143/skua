package pp

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// kind is a slice of a server's members: how many, how often they turn up,
// what they do when they do and how many quests they finish in a sitting.
type kind struct {
	persona
	share  int // per thousand members
	odds   int // percent of days they turn up
	finish int
}

// A 2500 member community server: most never or barely speak, a few carry
// it, and a handful try to farm it.
var population = []kind{
	{persona{name: "ghost", messages: 2}, 550, 5, 0},
	{persona{name: "lurker", muted: 30, messages: 5}, 200, 30, 1},
	{persona{name: "casual", unmuted: 30, messages: 20}, 120, 50, 1},
	{persona{name: "regular", unmuted: 120, messages: 40}, 80, 80, 1},
	{persona{name: "heavy", unmuted: 300, messages: 80}, 30, 95, 2},
	{persona{name: "spammer", messages: 944}, 10, 90, 1},
	{persona{name: "idler", muted: 600}, 10, 90, 0},
}

type member struct {
	kind          *kind
	id            uint64
	prev          Day
	prevPools     Pools
	seen          int64 // day last played, -1 never
	held          int
	total, quests int64
	week          int64
	wears         Wearing
}

// TestServerMonth runs a 2500 member server for one season and holds the
// economy to what a healthy one looks like: levels spread by effort without
// anyone running away, the hearth lighting most days and burning brightest
// on few, checkpoints going to the active and never to a farmer, and prizes
// a sliver of what the server earns.
func TestServerMonth(t *testing.T) {
	b := &Default
	rng := rand.New(rand.NewPCG(3, 4))
	var members []*member
	for i := range population {
		k := &population[i]
		for range k.share * 2500 / 1000 {
			members = append(members, &member{
				kind: k, id: uint64(len(members) + 1), seen: -1,
				wears: Wearing{Crest: rng.IntN(4) == 0, Banner: rng.IntN(10) == 0, Plume: rng.IntN(33) == 0},
			})
		}
	}

	days := b.SeasonWeeks * 7
	var history []int64
	var tiers [4]int
	var minted, prizes int64
	winners := map[string]int{}
	distinct := map[uint64]bool{}
	hearth := 0
	for day := range days {
		// Seasons start on a Monday: weekdays run quiet, Friday to Sunday busy.
		busy := 90
		if day%7 >= 4 {
			busy = 125
		}
		var raw int64
		for _, m := range members {
			if m.held < b.QuestHold {
				m.held++
			}
			if rng.IntN(100) >= m.kind.odds*busy/100 {
				continue
			}
			pools := b.Pools(42, 1, m.id, day)
			gap := day - m.seen
			if m.seen < 0 {
				gap = 2
			}
			d := Day{Rested: b.Rested(m.prev, m.prevPools, gap)}
			got := playDay(b, rng, m.kind.persona, 50+rng.Int64N(101), pools, &d, b.Buff(m.wears, hearth))
			for range min(m.held, m.kind.finish) {
				q := quest(b, rng)
				got += q
				m.quests += q
				m.held--
			}
			m.total += got
			m.week += got
			minted += got
			raw += d.Voice + d.Chat
			m.prev, m.prevPools, m.seen = d, pools, day
		}
		// ponytail: the hearth lights here the day after it's reached; the
		// module lights it the moment it is.
		hearth = b.HearthTier(raw, history[max(0, len(history)-7):])
		tiers[hearth]++
		history = append(history, raw)

		if day%7 == 6 {
			scores := make([]int64, len(members))
			for i, m := range members {
				scores[i] = m.week
			}
			for i, place := range Places(scores) {
				if prize := b.Prize(place); prize > 0 {
					m := members[i]
					m.total += prize
					prizes += prize
					winners[m.kind.name]++
					distinct[m.id] = true
				}
			}
			for _, m := range members {
				m.week = 0
			}
		}
	}

	levels := map[string][]int{}
	var questPP int64
	for _, m := range members {
		l, _, _ := b.Level(m.total)
		levels[m.kind.name] = append(levels[m.kind.name], l)
		questPP += m.quests
	}
	var report strings.Builder
	fmt.Fprintf(&report, "%-8s %5s %4s %4s %4s %6s\n", "kind", "n", "p50", "p90", "max", "quest")
	for _, k := range population {
		ls := levels[k.name]
		slices.Sort(ls)
		var q, all int64
		for _, m := range members {
			if m.kind.name == k.name {
				q, all = q+m.quests, all+m.total
			}
		}
		fmt.Fprintf(&report, "%-8s %5d %4d %4d %4d %5d%%\n", k.name, len(ls),
			ls[len(ls)/2], ls[len(ls)*9/10], ls[len(ls)-1], q*100/max(all, 1))
	}
	fmt.Fprintf(&report, "hearth days by tier %v; checkpoint places %v, %d distinct\n", tiers, winners, len(distinct))
	fmt.Fprintf(&report, "minted %d pp a day; prizes %.1f%%, quests %.1f%%\n",
		minted/unit/days, float64(prizes)*100/float64(minted), float64(questPP)*100/float64(minted))
	t.Log("\n" + report.String())

	p50 := func(k string) int { return levels[k][len(levels[k])/2] }
	if p50("heavy") <= p50("regular") || p50("regular") <= p50("casual") || p50("casual") < p50("lurker") {
		t.Error("levels don't follow effort")
	}
	if top := levels["heavy"][len(levels["heavy"])-1]; top < 10 || top > 16 {
		t.Errorf("the top member is level %d after a season; want 10 to 16", top)
	}
	if p50("idler") >= p50("regular") || p50("spammer") >= p50("heavy") {
		t.Error("a farmer keeps pace with the people who show up")
	}
	if tiers[0] == 0 || tiers[0] > int(days)*3/4 || tiers[3] > int(days)/4 {
		t.Errorf("hearth tiers by day %v: it should light on good days only, brightest on few", tiers)
	}
	if winners["spammer"]+winners["idler"] > 0 {
		t.Errorf("a farmer placed at a checkpoint: %v", winners)
	}
	if len(distinct) < 12 {
		t.Errorf("only %d members ever placed in four checkpoints; the same few win every week", len(distinct))
	}
	if prizes*100 > minted*2 {
		t.Errorf("prizes are %d%% of all pp; a checkpoint should be a sliver", prizes*100/minted)
	}
}
