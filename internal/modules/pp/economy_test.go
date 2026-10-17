package pp

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The pot economy run for three years on the melting pot's real activity,
// held to the charter's acceptance tests. testdata/activity.json is merlin's
// statistics for a month, aggregates only: members by how many days they
// turned up, and their days binned by paying messages and voice minutes.
// The sim draws members from it, lets them come and go, and runs the levy,
// the treasury, the purse cap, the shop, prizes and grants on top of the
// engine, which earns every minute and message as the module does.
//
// What the data can't say is assumed, and named here: how long members
// stay, how many quests they finish, how they sit in voice and how much
// they spend. SKUA_SIM_REPS=n runs n seeds of every spending rate and
// churn the charter is checked against; SKUA_SIM_FIT=1 sweeps the cap and
// the quest size and prints what each gives.

//go:embed testdata/activity.json
var activityJSON []byte

type activityData struct {
	Days      int        `json:"days"`
	CellMean  [][4]int64 `json:"cellMean"`
	Tiers     []tierData `json:"tiers"`
	Attend    [][2]int   `json:"attend"`
	Reference struct {
		Voice    int64 `json:"voice"`
		Messages int64 `json:"messages"`
	} `json:"reference"`
}

type tierData struct {
	Name       string     `json:"name"`
	ActiveDays [2]int     `json:"activeDays"`
	Members    int        `json:"members"`
	MemberDays int        `json:"memberDays"`
	Cells      [][3]int64 `json:"cells"`
}

// Assumed per tier (visitor, occasional, regular, core): how many weeks a
// member stays on average, and how often a day they turn up finishes a
// quest.
var (
	lifetime = [4]float64{1, 8, 26, 52}
	finishes = [4]float64{0.3, 0.5, 0.7, 0.9}
)

// unmutedShare is the part of voice time spent with the mic open; the rest
// is muted, both with a sign of life often enough not to fade.
const unmutedShare = 60

// econ is one economy to run: the charter's numbers and the world's.
type econ struct {
	levy   int64 // basis points of every earning to the treasury
	weeks  int64 // the levy pauses while the treasury holds this many weeks of it
	h      int64 // the purse cap, in pot days
	spend  int64 // percent of a week's credit spent in the shop, and burned
	grants int64 // pot days a week the admins grant, from week 5
	quests int64 // percent of QuestPP a quest pays
	churn  int64 // percent of the assumed leaving rate
	// shock multiplies arrivals and turning up by pct percent over weeks
	// [from, to).
	shock struct{ from, to, pct int }
	run   int // weeks
	seed  uint64
}

// charter is the economy PR 3 ships, fitted with SKUA_SIM_FIT.
var charter = econ{levy: 200, weeks: 8, h: 45, spend: 30, grants: 20, quests: 100, churn: 100, run: 156}

// citizen is one member of the simulated server.
type citizen struct {
	tier                 int
	p                    float64 // chance of turning up on a day
	id                   uint64
	alive, tracked       bool
	joined, seen         int64
	prev                 Day
	prevPools            Pools
	purse, total         int64
	weekCredit, weekNet  int64
	income               [4]int64 // credit, the last four weeks
	week                 [Boards]int64
	granted, grantSeason int64
	wears                Wearing
}

// outcome is what one run measured, against A1 to A8.
type outcome struct {
	potDay, H                  int64
	conserved                  bool    // A1
	drift, band                float64 // A2: supply's monthly slope and widest swing in the third year
	parity                     int64   // A3: days a core newcomer takes to 90% of a core veteran's purse, -1 never
	giniW, giniI, topW, topI   float64 // A4
	transfers                  float64 // A5: prizes and grants, most of labour in any 4 weeks
	quests                     float64 // A5: quests' share of labour
	treasuryOK                 bool    // A6
	atCap, supply              float64
	perDay, mintDay, levyNeed  float64
	treasury, burned, withheld int64
	withheldShare              float64 // of everything minted
}

func loadActivity(t testing.TB) activityData {
	t.Helper()
	var a activityData
	if err := json.Unmarshal(activityJSON, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

// potDay is the unit prices are set in: what the reference day (a core
// member's median day: its voice minutes with the mic open, its messages at
// the mean pay) earns under b, unbuffed and unrested, and a quest at the
// economy's size, rerolled when plain.
func potDay(b *Balance, a activityData, quests int64) int64 {
	d, p := Day{}, Pools{Pool: b.Pool, Pace: 100}
	var got int64
	for range a.Reference.Voice {
		got += b.Earn(&d, p, false, b.VoiceBase*b.Unmuted/100, 0)
	}
	for range a.Reference.Messages {
		got += b.Earn(&d, p, true, (b.ChatMin+b.ChatMax)/2, 0)
	}
	return got + questEV(b)*quests/100
}

// floor is the raw pp a day must reach before its quest pays: the
// reference day's, so an account that only shows up to collect has to put
// in a real member's day for it.
func floor(b *Balance, a activityData) int64 {
	return a.Reference.Voice*b.VoiceBase*b.Unmuted/100 + a.Reference.Messages*(b.ChatMin+b.ChatMax)/2
}

// questEV is what a quest pays on average when a plain one is rerolled.
func questEV(b *Balance) int64 {
	var sum, mean int64
	for i, o := range b.QuestOdds {
		sum += int64(o)
		mean += int64(o) * b.QuestPP[i]
	}
	mean /= sum
	return (int64(b.QuestOdds[0])*mean + int64(b.QuestOdds[1])*b.QuestPP[1] + int64(b.QuestOdds[2])*b.QuestPP[2]) / sum
}

// day draws a member-day for tier from the data: a cell by its count, then
// messages and voice spread around the cell's mean.
func (a activityData) day(rng *rand.Rand, tier int) (msgs, voice int64) {
	cells := a.Tiers[tier].Cells
	var n int64
	for _, c := range cells {
		n += c[2]
	}
	r := rng.Int64N(n)
	for _, c := range cells {
		if r -= c[2]; r < 0 {
			for _, m := range a.CellMean {
				if m[0] == c[0] && m[1] == c[1] {
					return m[2] * (50 + rng.Int64N(101)) / 100, m[3] * (50 + rng.Int64N(101)) / 100
				}
			}
		}
	}
	return 0, 0
}

// attendance draws how often a new member of tier turns up, from how many
// of the window's days its members did; a visitor's days fall in its week.
func (a activityData) attendance(rng *rand.Rand, tier int) float64 {
	lo, hi := a.Tiers[tier].ActiveDays[0], a.Tiers[tier].ActiveDays[1]
	var n int
	for _, r := range a.Attend {
		if r[0] >= lo && r[0] <= hi {
			n += r[1]
		}
	}
	pick := rng.IntN(n)
	for _, r := range a.Attend {
		if r[0] >= lo && r[0] <= hi {
			if pick -= r[1]; pick < 0 {
				if tier == 0 {
					return float64(r[0]) / 7
				}
				return float64(r[0]) / float64(a.Days)
			}
		}
	}
	return 0
}

// arrivals is how many members of each tier join a week, so that with the
// assumed lifetimes the window would have seen the members it did.
func (a activityData) arrivals() [4]float64 {
	var out [4]float64
	window := float64(a.Days) / 7
	for i, t := range a.Tiers {
		out[i] = float64(t.Members) / (window + lifetime[i])
	}
	return out
}

// runEconomy runs e on a and measures it.
func runEconomy(b *Balance, a activityData, e econ) outcome {
	rng := rand.New(rand.NewPCG(e.seed, 99))
	pd := potDay(b, a, e.quests)
	H := e.h * pd
	minQuest := floor(b, a)
	o := outcome{potDay: pd, H: H, conserved: true, treasuryOK: true, parity: -1}
	join := a.arrivals()

	var people []*citizen
	add := func(tier int, day int64) *citizen {
		c := &citizen{tier: tier, p: a.attendance(rng, tier), id: uint64(len(people) + 1), alive: true, joined: day, seen: -1,
			grantSeason: -1, wears: Wearing{Crest: rng.IntN(4) == 0, Banner: rng.IntN(10) == 0, Plume: rng.IntN(33) == 0}}
		people = append(people, c)
		return c
	}
	// The server as it stands: every tier at its steady size.
	for tier, j := range join {
		for range int(j * lifetime[tier]) {
			add(tier, 0)
		}
	}

	var treasury, burned, withheld, mint, questMint, transfers int64
	var weekMint, weekTransfer [4]int64
	var trailing [7]int64 // gross earning, the last seven days
	var history []int64
	var supply []float64
	var cohort []*citizen
	hearth, turnedUp := 0, int64(0)
	factor := func(week int) float64 {
		if week >= e.shock.from && week < e.shock.to {
			return float64(e.shock.pct) / 100
		}
		return 1
	}
	days := int64(e.run * 7)
	for day := range days {
		week := int(day / 7)
		f := factor(week)
		var raw, gross int64
		for _, c := range people {
			if !c.alive || rng.Float64() >= min(c.p*f, 1)*float64(busy[day%7])/100 {
				continue
			}
			msgs, voice := a.day(rng, c.tier)
			pools := b.Pools(e.seed, 1, c.id, day)
			var d Day
			if c.seen >= 0 {
				d.Rested = b.Rested(c.prev, c.prevPools, day-c.seen)
			}
			got := playDay(b, rng, persona{unmuted: voice * unmutedShare / 100, muted: voice - voice*unmutedShare/100, engage: 25, messages: msgs},
				100, pools, &d, b.Buff(c.wears, hearth))
			if d.Voice+d.Chat >= minQuest && rng.Float64() < finishes[c.tier] {
				q := quest(b, rng) * e.quests / 100
				got += q
				questMint += q
			}
			c.prev, c.prevPools, c.seen = d, pools, day
			for i, act := range d.Activity() {
				c.week[i] += act
			}
			raw += d.Voice + d.Chat
			gross += got
			mint += got
			turnedUp++

			// The levy runs until the treasury holds e.weeks of it at the
			// rate of the last seven days.
			var full int64
			for _, g := range trailing {
				full += g
			}
			full = full * e.levy / 10000
			lv := int64(0)
			if full == 0 || treasury < e.weeks*full {
				lv = got * e.levy / 10000
			}
			treasury += lv
			net := got - lv
			credit := min(net, max(H-c.purse, 0))
			withheld += net - credit
			c.purse += credit
			c.weekCredit += credit
			c.weekNet += net
			c.total += net
		}
		trailing[day%7] = gross
		weekMint[week%4] += gross
		hearth = b.HearthTier(raw, history[max(0, len(history)-7):])
		history = append(history, raw)

		if day%7 != 6 {
			continue
		}
		weekTransfer[week%4] = 0
		pay := func(c *citizen, amount int64) int64 {
			amount = min(amount, treasury, max(H-c.purse, 0))
			treasury -= amount
			c.purse += amount
			c.weekCredit += amount
			weekTransfer[week%4] += amount
			transfers += amount
			return amount
		}
		// Checkpoints pay from the treasury, first.
		for board := range Boards {
			scores := make([]int64, len(people))
			for i, c := range people {
				scores[i] = c.week[board]
			}
			for i, place := range Places(scores) {
				pay(people[i], b.Prize(place))
			}
		}
		// Grants, once the treasury has had four weeks to fill: a quarter
		// of it a week at most, ten pot days a season to anyone.
		if week >= 4 {
			budget := min(e.grants*pd, treasury/4)
			var active []*citizen
			for _, c := range people {
				if c.alive && c.seen > day-7 {
					active = append(active, c)
				}
			}
			for try := 0; budget > 0 && len(active) > 0 && try < 200; try++ {
				c := active[rng.IntN(len(active))]
				if season := int64(week / 4); c.grantSeason != season {
					c.grantSeason, c.granted = season, 0
				}
				g := min(pd*(1+rng.Int64N(3)), budget, 10*pd-c.granted)
				if g <= 0 {
					continue
				}
				got := pay(c, g)
				c.granted += got
				budget -= got
			}
		}
		// The shop, by what members earned rather than what reached their
		// purse, so a full purse is spent from too; members leaving and
		// joining.
		for _, c := range people {
			spent := min(c.purse, c.weekNet*e.spend/100)
			c.purse -= spent
			burned += spent
			c.income[week%4] = c.weekCredit
			c.weekCredit, c.weekNet, c.week = 0, 0, [Boards]int64{}
			if c.alive && !c.tracked && rng.Float64() < float64(e.churn)/100/lifetime[c.tier] {
				c.alive = false
			}
		}
		for tier, j := range join {
			n := j * f
			k := int(n)
			if rng.Float64() < n-float64(k) {
				k++
			}
			for range k {
				add(tier, day+1)
			}
		}
		// A3: twenty core newcomers join at week 52 and stay.
		if week == 51 {
			for range 20 {
				c := add(3, day+1)
				c.tracked = true
				cohort = append(cohort, c)
			}
		}

		// A1: every pp minted is in a purse, the treasury, the shop's burn
		// or withheld at a cap.
		var held int64
		for _, c := range people {
			held += c.purse
		}
		if held+treasury+burned+withheld != mint {
			o.conserved = false
		}
		// A6: the treasury never runs dry into debt and never runs past
		// its pause by more than a week of levy.
		var full int64
		for _, g := range trailing {
			full += g
		}
		if treasury < 0 || full > 0 && treasury > (e.weeks+2)*full*e.levy/10000 {
			o.treasuryOK = false
		}
		// A5: transfers against labour, every four weeks.
		var m4, t4 int64
		for i := range 4 {
			m4, t4 = m4+weekMint[i], t4+weekTransfer[i]
		}
		if week >= 4 && m4 > 0 {
			o.transfers = max(o.transfers, float64(t4)/float64(m4))
		}

		// A2: supply per active member, in pot days.
		var sum int64
		var n int
		for _, c := range people {
			if c.seen > day-28 {
				sum, n = sum+c.purse, n+1
			}
		}
		if n > 0 {
			supply = append(supply, float64(sum)/float64(n)/float64(pd))
		}
		// A3: when the newcomers' median purse first reaches 90% of the
		// core veterans'.
		if len(cohort) > 0 && o.parity < 0 {
			var vets, fresh []int64
			for _, c := range people {
				if c.tier == 3 && c.alive && c.joined == 0 {
					vets = append(vets, c.purse)
				}
			}
			for _, c := range cohort {
				fresh = append(fresh, c.purse)
			}
			if median(fresh)*10 >= median(vets)*9 {
				o.parity = day + 1 - cohort[0].joined
			}
		}
	}

	// What the end looks like.
	var wealth, income []int64
	var capped int
	for _, c := range people {
		if c.seen > days-28 {
			var inc int64
			for _, i := range c.income {
				inc += i
			}
			wealth, income = append(wealth, c.purse), append(income, inc)
			if c.purse >= H-pd/10 {
				capped++
			}
		}
	}
	o.giniW, o.giniI = gini(wealth), gini(income)
	o.topW, o.topI = top(wealth, 100), top(income, 100)
	o.atCap = float64(capped) / float64(max(len(wealth), 1))
	o.quests = float64(questMint) / float64(max(mint, 1))
	o.perDay = float64(turnedUp) / float64(days)
	o.mintDay = float64(mint) / float64(days) / unit
	o.treasury, o.burned, o.withheld = treasury, burned, withheld
	o.withheldShare = float64(withheld) / float64(max(mint, 1))
	// The levy that would just fund what was paid out.
	o.levyNeed = float64(transfers) / float64(max(mint, 1)) * 10000
	// Every purse starts empty at launch and a core member stays about a
	// year, so the first two years are the economy filling, not drifting.
	if late := supply[min(104, len(supply)-1):]; len(late) > 1 {
		lo, hi := slices.Min(late), slices.Max(late)
		mean := 0.0
		for _, s := range late {
			mean += s
		}
		mean /= float64(len(late))
		o.band = max(hi-mean, mean-lo) / mean
		// The slope by least squares, per four weeks, against the mean.
		var sx, sy, sxy, sxx float64
		for i, s := range late {
			x := float64(i)
			sx, sy, sxy, sxx = sx+x, sy+s, sxy+x*s, sxx+x*x
		}
		k := float64(len(late))
		o.drift = (k*sxy - sx*sy) / (k*sxx - sx*sx) * 4 / mean
		o.supply = mean
	}
	return o
}

func median(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)/2]
}

// gini is the Gini coefficient of xs.
func gini(xs []int64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	var sum, weighted float64
	for i, x := range s {
		sum += float64(x)
		weighted += float64(i+1) * float64(x)
	}
	n := float64(len(s))
	if sum == 0 {
		return 0
	}
	return 2*weighted/(n*sum) - (n+1)/n
}

// top is the share of the total the top 1/per of xs hold.
func top(xs []int64, per int) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	slices.Reverse(s)
	var all, head int64
	for i, x := range s {
		all += x
		if i < max(len(s)/per, 1) {
			head += x
		}
	}
	return float64(head) / float64(max(all, 1))
}

// check holds o to the charter's acceptance tests and says what failed.
func (o outcome) check() []string {
	var bad []string
	fail := func(cond bool, f string, args ...any) {
		if !cond {
			bad = append(bad, fmt.Sprintf(f, args...))
		}
	}
	fail(o.conserved, "A1 a pp went missing")
	// One run's slope is noisy to about half a percent a month; over many
	// seeds the mean has to be under that (see TestEconomyThreeYears).
	fail(o.drift < 0.01 && o.drift > -0.01, "A2 supply drifts %.2f%% a month", o.drift*100)
	fail(o.band <= 0.20, "A2 supply swings %.0f%% from its mean", o.band*100)
	fail(o.parity >= 0 && o.parity <= 100, "A3 a core newcomer reaches parity in %d days", o.parity)
	fail(o.giniW <= 1.1*o.giniI, "A4 wealth gini %.2f against income %.2f", o.giniW, o.giniI)
	fail(o.topW <= 2*o.topI, "A4 the top 1%% hold %.1f%% of wealth, %.1f%% of income", o.topW*100, o.topI*100)
	fail(o.transfers <= 0.03, "A5 prizes and grants reach %.1f%% of labour", o.transfers*100)
	fail(o.quests <= 0.50, "A5 quests are %.0f%% of what's minted", o.quests*100)
	fail(o.treasuryOK, "A6 the treasury left its bounds")
	return bad
}

func (o outcome) String() string {
	return fmt.Sprintf("pot day %d pp, cap %s pp; %.0f turn up a day, %.0f pp minted a day, quests %.0f%%\n"+
		"supply %.1f pot days an active member, drift %+.2f%%/mo, swing %.0f%%; %.1f%% at the cap\n"+
		"core newcomer parity %d days; gini wealth %.2f income %.2f; top 1%% %.1f%% / %.1f%%\n"+
		"transfers peak %.2f%% of labour, levy needed %.0f bp; treasury %s, burned %s, withheld %s",
		o.potDay/unit, grouped(o.H/unit), o.perDay, o.mintDay, o.quests*100,
		o.supply, o.drift*100, o.band*100, o.atCap*100,
		o.parity, o.giniW, o.giniI, o.topW*100, o.topI*100,
		o.transfers*100, o.levyNeed, grouped(o.treasury/unit), grouped(o.burned/unit), grouped(o.withheld/unit))
}

// The charter's economy on the melting pot, three years, with a slump and
// a boom in the middle: A1 to A6 hold.
func TestEconomyThreeYears(t *testing.T) {
	a := loadActivity(t)
	runs := []econ{charter}
	slump, boom := charter, charter
	slump.shock = struct{ from, to, pct int }{60, 68, 65}
	boom.shock = struct{ from, to, pct int }{60, 68, 160}
	runs = append(runs, slump, boom)
	if n, _ := strconv.Atoi(os.Getenv("SKUA_SIM_REPS")); n > 0 {
		runs = runs[:0]
		for seed := range uint64(n) {
			for _, spend := range []int64{10, 30, 60} {
				for _, churn := range []int64{50, 100, 200} {
					e := charter
					e.seed, e.spend, e.churn = seed+1, spend, churn
					runs = append(runs, e)
				}
			}
		}
	}
	fails := map[string]int{}
	drift := map[[2]int64][]float64{}
	for i, e := range runs {
		o := runEconomy(&Default, a, e)
		drift[[2]int64{e.spend, e.churn}] = append(drift[[2]int64{e.spend, e.churn}], o.drift)
		if i == 0 || testing.Verbose() {
			t.Logf("seed %d spend %d%% churn %d%% shock %v\n%v", e.seed, e.spend, e.churn, e.shock, o)
		}
		for _, f := range o.check() {
			// Over many seeds a scenario's mean drift is held instead.
			if len(runs) > 3 && strings.Contains(f, "drifts") {
				continue
			}
			fails[f[:2]]++
			fails[fmt.Sprintf("%s spend %d churn %d", f[:2], e.spend, e.churn)]++
			if len(runs) <= 3 {
				t.Errorf("seed %d spend %d%% churn %d%% shock %v: %s", e.seed, e.spend, e.churn, e.shock, f)
			}
		}
	}
	// Over many seeds, each test has to hold in 95% of runs.
	if len(runs) > 3 {
		for k, n := range fails {
			if len(k) == 2 && n*20 > len(runs) {
				t.Errorf("%s failed %d of %d runs", k, n, len(runs))
			}
		}
		for k, ds := range drift {
			var mean float64
			for _, d := range ds {
				mean += d / float64(len(ds))
			}
			if mean > 0.005 || mean < -0.005 {
				t.Errorf("A2 at spend %d%% churn %d%% supply drifts %.2f%% a month over %d seeds", k[0], k[1], mean*100, len(ds))
			}
		}
		t.Logf("%d runs, failures by test %v", len(runs), fails)
	}
}

// A7: the levy moves no member's pay by more than a tenth, and A8: an
// account that only shows up, a message a day, never collects a quest.
func TestEconomyFair(t *testing.T) {
	a := loadActivity(t)
	if charter.levy > 1000 {
		t.Errorf("a levy of %d bp moves pay by more than a tenth", charter.levy)
	}
	alt := (Default.ChatMin + Default.ChatMax) / 2
	if alt >= floor(&Default, a) {
		t.Error("a message a day reaches the quest floor")
	}
	if pd := potDay(&Default, a, charter.quests); alt*4 > pd {
		t.Errorf("a message a day earns %d, over a quarter of a pot day %d", alt, pd)
	}
}

// SKUA_SIM_FIT=1 sweeps the cap and the quest size at the charter's other
// numbers and prints what each gives, to pick the charter by.
func TestEconomyFit(t *testing.T) {
	if os.Getenv("SKUA_SIM_FIT") == "" {
		t.Skip("SKUA_SIM_FIT is not set")
	}
	a := loadActivity(t)
	var out strings.Builder
	fmt.Fprintf(&out, "%4s %6s %7s %8s %6s %7s %6s %s\n", "h", "spend", "potday", "withheld", "parity", "quest%", "levybp", "fails")
	for _, spend := range []int64{10, 30, 60} {
		for _, h := range []int64{30, 45, 60, 90, 120} {
			e := charter
			e.h, e.spend, e.seed = h, spend, 7
			o := runEconomy(&Default, a, e)
			fmt.Fprintf(&out, "%4d %5d%% %7d %7.0f%% %6d %6.0f%% %6.0f %v\n", h, spend, o.potDay/unit, o.withheldShare*100, o.parity, o.quests*100, o.levyNeed, o.check())
		}
	}
	t.Log("\n" + out.String())
}
