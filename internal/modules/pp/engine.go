package pp

import "time"

// Turn is when a day turns, past 00:00 UTC: a sweet spot between western
// Europe's morning and the US's night.
const Turn = 5*time.Hour + 30*time.Minute

// DayOf is the game day t falls in. Day n runs from Turn on its date to
// Turn on the next.
func DayOf(t time.Time) int64 { return (t.Unix() - int64(Turn/time.Second)) / 86400 }

// dayStart is when day begins.
func dayStart(day int64) time.Time { return time.Unix(day*86400, 0).Add(Turn) }

// Pools is one member's pools for one day: the jittered base pool and the
// percent their idle graces run to.
type Pools struct{ Pool, Pace int64 }

// Day is what one member has counted against today's pools, the rested pp
// carried into it, and Talk, every paying message's raw pp with no share
// held back, which the chat checkpoint ranks. Minutes and Paid count the
// voice minutes and paying messages it came in.
type Day struct{ Voice, Chat, Rested, Talk, Minutes, Paid int64 }

// The weekly checkpoints are two boards, so a server's talkers and its
// voice regulars each have one to win. Voice ranks what voice counted
// before the taper: real time past the cap still counts and voice gone idle
// has already faded. Chat ranks Talk: the minute's cooldown paces it, and
// the share that holds back its pay doesn't hold back its standing.
const (
	BoardVoice = iota
	BoardChat
	Boards
)

// Activity is what d adds to each board.
func (d Day) Activity() [Boards]int64 { return [Boards]int64{d.Voice, d.Talk} }

// mix is splitmix64's finaliser.
// ponytail: not a keyed hash; it only has to keep a cap from being read off
// a few days of earning, never from someone holding the seed.
func mix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	return x ^ x>>31
}

// jitter maps h onto -j..+j.
func jitter(h uint64, j int64) int64 { return int64(h%uint64(2*j+1)) - j }

// Pools is member user's pools in guild on day, jittered by seed so that
// nobody's cap is a round number or the same as anyone else's.
func (b *Balance) Pools(seed, guild, user uint64, day int64) Pools {
	h := mix(seed ^ mix(guild^mix(user^mix(uint64(day)))))
	pool := b.Pool * (100 + jitter(h, b.Jitter)) / 100
	return Pools{
		Pool: pool,
		Pace: 100 + jitter(mix(mix(h)), b.IdleJitter),
	}
}

// Earn counts raw pp from voice or chat against d and returns what the member
// gets: tapered by how full the day is, then buffed by buff percent. Voice
// and chat fill one pool; past it chat pays ChatTail, so a flood of messages
// buys a day's pool and very little more.
func (b *Balance) Earn(d *Day, p Pools, chat bool, raw, buff int64) int64 {
	full := p.Pool + d.Rested
	used := d.Voice + d.Chat
	tail, count := b.VoiceTail, &d.Voice
	var got int64
	if chat {
		d.Talk += raw
		d.Paid++
		tail, count = b.ChatTail, &d.Chat
	} else {
		d.Minutes++
	}
	for raw > 0 {
		rate, step := tail, raw
		for _, band := range b.Taper {
			if edge := full * band.Upto / 100; used < edge {
				rate, step = band.Rate, min(raw, edge-used)
				break
			}
		}
		got += step * rate / 100
		used += step
		*count += step
		raw -= step
	}
	return got * (100 + buff) / 100
}

// Bracket is the least and the most a day could have paid for voice and
// chat raw pp against its pool and rested pp, however its calls (minutes
// and messages) fell, at buffs between lo and hi. Only past the pool does
// the order matter: voice there pays VoiceTail and chat ChatTail, so chat
// first leaves the most voice past it and voice first the least. Each call
// rounds down on its own, losing at most a couple of hundredths.
func (b *Balance) Bracket(voice, chat, rested, pool, lo, hi, calls int64) (least, most int64) {
	order := func(chatFirst bool) int64 {
		d, p := Day{Rested: rested}, Pools{Pool: pool}
		if chatFirst {
			return b.Earn(&d, p, true, chat, 0) + b.Earn(&d, p, false, voice, 0)
		}
		return b.Earn(&d, p, false, voice, 0) + b.Earn(&d, p, true, chat, 0)
	}
	v, c := order(false), order(true)
	return min(v, c)*(100+lo)/100 - 2*calls - 4, max(v, c) * (100 + hi) / 100
}

// Roll is the draw for slot on member user's day in guild, from a season's
// seed, so anyone holding the seed once it is revealed can draw it again.
func Roll(seed, guild, user uint64, day int64, slot uint64) uint64 {
	return mix(seed ^ mix(guild^mix(user^mix(uint64(day)^mix(slot^0x636861)))))
}

// Rested is what carries into a day from prev, gap days after it: half of
// what prev left unused, and the most there is after a day away.
func (b *Balance) Rested(prev Day, p Pools, gap int64) int64 {
	most := b.Pool * b.RestedMax / 100
	switch {
	case gap <= 0:
		return prev.Rested
	case gap > 1:
		return most
	}
	unused := p.Pool + prev.Rested - prev.Voice - prev.Chat
	return min(max(unused*b.RestedCarry/100, 0), most)
}

// Vigor is how much of the day's pool is left, in percent, and the word the
// member sees for it. The numbers behind it never leave the engine.
func (b *Balance) Vigor(d Day, p Pools) (left int64, phase string) {
	full := p.Pool + d.Rested
	fill := int64(100)
	if full > 0 {
		fill = min((d.Voice+d.Chat)*100/full, 100)
	}
	switch {
	case fill < b.Taper[0].Upto/2:
		phase = "fresh"
	case fill < b.Taper[0].Upto:
		phase = "warm"
	case fill < b.Taper[1].Upto:
		phase = "tired"
	default:
		phase = "spent"
	}
	return 100 - fill, phase
}

// ChatPays is whether a message earns: the cooldown has passed, and it is
// neither too short nor the member's last message again. runes < 0 means
// skua can't read content, and only the cooldown applies.
func (b *Balance) ChatPays(since time.Duration, runes int, repeat bool) bool {
	return since >= b.ChatCooldown && (runes < 0 || runes >= b.ChatMinRunes && !repeat)
}

// ChatCounts is whether a message counts as activity: the same checks as
// pay, at ActivityGap's shorter pace.
func (b *Balance) ChatCounts(since time.Duration, runes int, repeat bool) bool {
	return since >= b.ActivityGap && (runes < 0 || runes >= b.ChatMinRunes && !repeat)
}

// ChatRaw is one earning message's raw pp, from a random r.
func (b *Balance) ChatRaw(r uint64) int64 {
	return b.ChatMin + int64(r%uint64(b.ChatMax-b.ChatMin+1))
}

// Seat is one member in a voice channel, as the engine needs to see them.
// Deaf covers deafened by anyone, Muted only by a moderator. Idle is how
// long since they last showed signs of life.
type Seat struct {
	Bot, Deaf, Muted, Suppressed, SelfMute bool
	Age, Idle                              time.Duration
}

// VoiceRaw is one minute's raw pp for me in a channel with others, at my
// pace for the day. Nothing unless someone real is there to hear, more with
// the mic open, less deafened, and fading the longer me goes without a sign
// of life: muted or deafened after a short grace, an open mic after a long
// one and then as if muted.
func (b *Balance) VoiceRaw(me Seat, others []Seat, pace int64) int64 {
	if me.Bot || me.Muted || me.Suppressed || !b.heard(others) {
		return 0
	}
	grace := b.IdleGrace * time.Duration(pace) / 100
	if !me.Deaf && !me.SelfMute {
		open := b.OpenGrace * time.Duration(pace) / 100
		if me.Idle <= open {
			return b.VoiceBase * b.Unmuted / 100
		}
		me.Idle -= open
	}
	if me.Deaf {
		return b.VoiceBase * b.Deafened / 100 * fade(me.Idle, grace/2, b.IdleFade, b.DeafFloor) / 100
	}
	return b.VoiceBase * fade(me.Idle, grace, b.IdleFade, b.IdleFloor) / 100
}

// heard is whether anyone in others counts as company.
func (b *Balance) heard(others []Seat) bool {
	for _, o := range others {
		if !o.Bot && !o.Deaf && o.Age >= b.CompanionAge {
			return true
		}
	}
	return false
}

// fade is the percent an idle stretch pays: all of it through grace, then
// a straight slope over span down to floor.
func fade(idle, grace, span time.Duration, floor int64) int64 {
	past := idle - grace
	switch {
	case past <= 0:
		return 100
	case past >= span:
		return floor
	}
	return 100 - (100-floor)*int64(past)/int64(span)
}

// Wearing is what a member shows that the server rewards: its tag (crest),
// .gg/melted in their status (banner), and a boost (plume).
type Wearing struct{ Crest, Banner, Plume bool }

// Buff is the percent added to everything but quests for what w shows and
// the hearth tier the server is at.
func (b *Balance) Buff(w Wearing, hearth int) int64 {
	var p int64
	if w.Crest {
		p += b.Crest
	}
	if w.Banner {
		p += b.Banner
	}
	if w.Plume {
		p += b.Plume
	}
	if hearth > 0 {
		p += b.Hearth[hearth-1]
	}
	return p
}

// HearthTier is the tier a server's raw pp today reaches against its usual
// day, the mean of the days in history it earned anything.
func (b *Balance) HearthTier(today int64, history []int64) int {
	usual := b.Usual(history)
	tier := 0
	for i, at := range b.HearthAt {
		if today >= usual*at/100 {
			tier = i + 1
		}
	}
	return tier
}

// Usual is a server's usual day, the mean of the days in history it
// earned anything, HearthFloor before it has one.
func (b *Balance) Usual(history []int64) int64 {
	var sum, n int64
	for _, h := range history {
		if h > 0 {
			sum, n = sum+h, n+1
		}
	}
	if n == 0 {
		return b.HearthFloor
	}
	return sum / n
}

// Need is what level l to l+1 costs.
func (b *Balance) Need(l int) int64 {
	L := int64(l)
	return (b.CurveA*L*L + b.CurveB*L + b.CurveC) * unit
}

// Level is the level total pp reaches, how far into it and what it costs.
func (b *Balance) Level(total int64) (l int, into, need int64) {
	for into, need = total, b.Need(0); into >= need; need = b.Need(l) {
		into -= need
		l++
	}
	return l, into, need
}

// Season is the season day falls in, counted from 1 (0 before the first),
// and its week, from 1.
func (b *Balance) Season(day int64) (season, week int64) {
	if day < b.SeasonStart {
		return 0, 0
	}
	since := day - b.SeasonStart
	return since/(7*b.SeasonWeeks) + 1, since%(7*b.SeasonWeeks)/7 + 1
}

// Places ranks scores, highest first, as standard competition ranking:
// a tie shares its place and the next place skips (1, 2, 2, 4). Nothing
// earned is no place at all.
// ponytail: quadratic; a weekly run over a server's earners is a few
// million compares at 2000 of them. Sort first if a server outgrows that.
func Places(scores []int64) []int {
	places := make([]int, len(scores))
	for i, s := range scores {
		if s <= 0 {
			continue
		}
		places[i] = 1
		for _, o := range scores {
			if o > s {
				places[i]++
			}
		}
	}
	return places
}

// Prize is what a checkpoint pays a place, 0 past the paid ones.
func (b *Balance) Prize(place int) int64 {
	if place < 1 || place > len(b.Prizes) {
		return 0
	}
	return b.Prizes[place-1]
}

// QuestTiers names the tiers, as members see them.
var QuestTiers = [3]string{"good", "great", "golden"}

// QuestRoll draws a dealt quest's tier and reward from a random r.
func (b *Balance) QuestRoll(r uint64) (tier int, pp int64) {
	var sum uint64
	for _, o := range b.QuestOdds {
		sum += o
	}
	r %= sum
	for tier = 0; r >= b.QuestOdds[tier]; tier++ {
		r -= b.QuestOdds[tier]
	}
	return tier, b.QuestPP[tier]
}
