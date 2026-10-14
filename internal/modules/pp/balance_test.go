package pp

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// persona is a kind of member, as one day of their habits.
type persona struct {
	name     string
	unmuted  int64 // minutes in voice with the mic open
	muted    int64 // minutes in voice muted
	deaf     int64 // minutes in voice deafened
	engage   int64 // shows signs of life every so many minutes; 0 never
	messages int64 // earning messages (the cooldown already applied)
	quests   int64 // quests done every questEvery days
	every    int64
	crest    bool
	banner   bool
}

var personas = []persona{
	{name: "lurker", muted: 30, messages: 5, quests: 1, every: 3},
	{name: "chatter", messages: 80, quests: 1, every: 1},
	{name: "regular", unmuted: 120, messages: 40, quests: 1, every: 1},
	{name: "spammer", messages: 944, quests: 1, every: 1},
	{name: "idler", muted: 600},
	{name: "deafidle", deaf: 600},
	{name: "farmer", muted: 600, engage: 20, messages: 30},
	{name: "wearer", unmuted: 120, messages: 40, quests: 1, every: 1, crest: true, banner: true},
}

// result is one persona's month.
type result struct {
	total, quests int64
	level         [31]int // level at the end of each day
}

// simulate plays p for days, earning as the module would: minute by minute
// in voice, message by message in chat, rested carried day to day. Every
// persona is the same member so they all meet the same pools.
func simulate(b *Balance, p persona, days int) result {
	rng := rand.New(rand.NewPCG(1, 2))
	var r result
	var prev Day
	var prevPools Pools
	buff := b.Buff(p.crest, p.banner, 0)
	me, other := Seat{}, Seat{Age: b.CompanionAge}
	for day := range int64(days) {
		pools := b.Pools(42, 1, 2, day)
		d := Day{Rested: b.Rested(prev, prevPools, min(day, 1))}
		voice := func(n int64, muted, deaf bool) {
			me.SelfMute, me.Deaf = muted, deaf
			for m := range n {
				if p.engage > 0 {
					m %= p.engage
				}
				me.Idle = time.Duration(m) * time.Minute
				r.total += b.Earn(&d, pools, false, b.VoiceRaw(me, []Seat{other}, pools.Grace), buff)
			}
		}
		voice(p.unmuted, false, false)
		voice(p.muted, true, false)
		voice(p.deaf, true, true)
		for range p.messages {
			r.total += b.Earn(&d, pools, true, b.ChatRaw(rng.Uint64()), buff)
		}
		if p.every > 0 && day%p.every == 0 {
			for range p.quests {
				q := b.QuestReward(rng.Uint64())
				r.total += q
				r.quests += q
			}
		}
		r.level[day+1], _, _ = b.Level(r.total)
		prev, prevPools = d, pools
	}
	return r
}

func TestBalanceHolds(t *testing.T) {
	b := &Default
	month := map[string]result{}
	var table strings.Builder
	fmt.Fprintf(&table, "%-8s %7s %5s %4s %4s %4s %4s\n", "persona", "pp/day", "quest", "d5", "d10", "d24", "d30")
	for _, p := range personas {
		r := simulate(b, p, 30)
		month[p.name] = r
		fmt.Fprintf(&table, "%-8s %7d %4d%% %4d %4d %4d %4d\n", p.name, r.total/unit/30,
			r.quests*100/max(r.total, 1), r.level[5], r.level[10], r.level[24], r.level[30])
	}
	t.Log("\n" + table.String())

	pp := func(n string) int64 { return month[n].total }
	ratio := func(a, b string) float64 { return float64(pp(a)) / float64(pp(b)) }
	if x := ratio("spammer", "chatter"); x > 1.25 {
		t.Errorf("spamming pays %.2fx steady chat; want at most 1.25x", x)
	}
	if pp("idler") >= pp("regular") {
		t.Errorf("idling muted all day (%d) out-earns a regular (%d)", pp("idler"), pp("regular"))
	}
	if pp("deafidle") >= pp("idler") {
		t.Errorf("idling deafened (%d) pays as much as idling muted (%d)", pp("deafidle"), pp("idler"))
	}
	if x := ratio("farmer", "idler"); x < 2 {
		t.Errorf("a muted member who chats now and then earns %.2fx a silent idler; want at least 2x", x)
	}
	if pp("farmer") >= pp("regular") {
		t.Errorf("light farming (%d) out-earns a regular (%d)", pp("farmer"), pp("regular"))
	}
	if x := ratio("wearer", "regular"); x <= 1 || x > 1.2 {
		t.Errorf("both buffs pay %.2fx; want more than 1x and at most 1.2x", x)
	}
	if pp("lurker") >= pp("chatter") || month["lurker"].level[30] < 2 {
		t.Errorf("a lurker reaches level %d; want them levelling, slower than a chatter", month["lurker"].level[30])
	}
	reg := month["regular"]
	if l := reg.level[5]; l < 4 || l > 6 {
		t.Errorf("a regular is level %d after 5 days; want 4 to 6", l)
	}
	if l := reg.level[24]; l < 9 || l > 11 {
		t.Errorf("a regular is level %d after 24 days; want 9 to 11", l)
	}
	if s := reg.quests * 100 / reg.total; s < 20 || s > 40 {
		t.Errorf("quests are %d%% of a regular's pp; want 20 to 40", s)
	}
}
