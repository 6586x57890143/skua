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

// A 2500 member community server, shaped on a real one: four weeks of the
// melting pot as merlin's statistics counted it (aggregates only), where
// about 220 members turn up on a day (150 to 350), a member-day holds a
// median of 12 messages (p90 142, p99 540) and of 44 voice minutes (p90
// 417, p99 962), and voice peaks on fridays. messages here are earning
// ones, after the minute's cooldown; heavy chatters send about twice that.
var population = []kind{
	{persona{name: "ghost"}, 560, 0, 0},
	{persona{name: "drifter", muted: 20, messages: 6, engage: 10}, 240, 7, 1},
	{persona{name: "casual", unmuted: 45, messages: 15, engage: 15}, 120, 25, 1},
	{persona{name: "regular", unmuted: 150, messages: 50, engage: 10}, 48, 75, 1},
	{persona{name: "voicer", unmuted: 420, messages: 40, engage: 20}, 20, 85, 2},
	{persona{name: "chatter", messages: 200}, 8, 90, 2},
	{persona{name: "afk", unmuted: 960}, 4, 90, 0},
}

// busy is how much more or less a weekday turns up than the week's mean,
// in percent, monday first: messages and voice minutes averaged.
var busy = [7]int{110, 89, 82, 98, 123, 97, 104}

type member struct {
	kind          *kind
	id            uint64
	prev          Day
	prevPools     Pools
	seen          int64 // day last played, -1 never
	held          int
	total, quests int64
	week          int64 // activity this week, what checkpoints rank
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
	var turnedUp int64
	for day := range days {
		var raw int64
		for _, m := range members {
			if m.held < b.QuestHold {
				m.held++
			}
			if rng.IntN(100) >= m.kind.odds*busy[day%7]/100 {
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
			m.week += d.Activity()
			minted += got
			raw += d.Voice + d.Chat
			turnedUp++
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
	fmt.Fprintf(&report, "%d turn up a day; hearth days by tier %v; checkpoint places %v, %d distinct\n", turnedUp/days, tiers, winners, len(distinct))
	fmt.Fprintf(&report, "minted %d pp a day; prizes %.1f%%, quests %.1f%%\n",
		minted/unit/days, float64(prizes)*100/float64(minted), float64(questPP)*100/float64(minted))
	t.Log("\n" + report.String())

	p50 := func(k string) int { return levels[k][len(levels[k])/2] }
	if perDay := turnedUp / days; perDay < 150 || perDay > 350 {
		t.Errorf("%d members turn up a day; the melting pot sees 150 to 350", perDay)
	}
	if p50("voicer") <= p50("regular") || p50("regular") <= p50("casual") || p50("casual") < p50("drifter") {
		t.Error("levels don't follow effort")
	}
	if top := levels["voicer"][len(levels["voicer"])-1]; top < 10 || top > 16 {
		t.Errorf("the top member is level %d after a season; want 10 to 16", top)
	}
	if p50("afk") >= p50("regular") {
		t.Error("sitting in voice all day afk keeps pace with a regular")
	}
	if p50("chatter")+2 < p50("regular") {
		t.Errorf("a heavy chatter is level %d to a regular's %d; talking shouldn't fall that far behind voice", p50("chatter"), p50("regular"))
	}
	if tiers[0] == 0 || tiers[0] > int(days)*3/4 || tiers[3] > int(days)/4 {
		t.Errorf("hearth tiers by day %v: it should light on good days only, brightest on few", tiers)
	}
	if winners["afk"] > 0 {
		t.Errorf("an afk placed at a checkpoint: %v", winners)
	}
	if len(distinct) < 10 {
		t.Errorf("only %d members ever placed in four checkpoints; the same few win every week", len(distinct))
	}
	if prizes*100 > minted*2 {
		t.Errorf("prizes are %d%% of all pp; a checkpoint should be a sliver", prizes*100/minted)
	}
}
