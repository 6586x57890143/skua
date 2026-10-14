package pp

import "time"

// DayOf is the game day t falls in. Days turn at 00:00 UTC.
func DayOf(t time.Time) int64 { return t.Unix() / 86400 }

// Pools is one member's pools for one day: the jittered base pool, the
// part of it chat may fill, and how long they may idle in voice before it
// fades.
type Pools struct {
	Pool, Chat int64
	Grace      time.Duration
}

// Day is what one member has counted against today's pools, and the rested
// pp carried into it.
type Day struct{ Voice, Chat, Rested int64 }

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
		Pool:  pool,
		Chat:  pool * (b.ChatShare + jitter(mix(h), b.ChatJitter)) / 100,
		Grace: b.IdleGrace * time.Duration(100+jitter(mix(mix(h)), b.IdleJitter)) / 100,
	}
}

// Earn counts raw pp from voice or chat against d and returns what the member
// gets: tapered by how full the day is, then buffed by buff percent. Chat past
// its share pays ChatTail and counts against nothing, so chatting can never
// spend the pool voice would earn from.
func (b *Balance) Earn(d *Day, p Pools, chat bool, raw, buff int64) int64 {
	full := p.Pool + d.Rested
	used := d.Voice + d.Chat
	tail, count := b.VoiceTail, &d.Voice
	var got int64
	if chat {
		tail, count = b.ChatTail, &d.Chat
		if over := min(d.Chat+raw-p.Chat, raw); over > 0 {
			got += over * b.ChatTail / 100
			raw -= over
		}
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

// VoiceRaw is one minute's raw pp for me in a channel with others, given
// my grace for the day. Nothing unless someone real is there to hear, more
// with the mic open, less deafened, and fading the longer me sits muted or
// deafened without a sign of life.
func (b *Balance) VoiceRaw(me Seat, others []Seat, grace time.Duration) int64 {
	if me.Bot || me.Muted || me.Suppressed || !b.heard(others) {
		return 0
	}
	if !me.Deaf && !me.SelfMute {
		return b.VoiceBase * b.Unmuted / 100
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

// Buff is the percent added to everything but quests: the server tag worn,
// .gg/melted in the status, and the hearth tier the server is at.
func (b *Balance) Buff(crest, banner bool, hearth int) int64 {
	var p int64
	if crest {
		p += b.Crest
	}
	if banner {
		p += b.Banner
	}
	if hearth > 0 {
		p += b.Hearth[hearth-1]
	}
	return p
}

// HearthTier is the tier a server's raw pp today reaches against its usual
// day, the mean of the days in history it earned anything.
func (b *Balance) HearthTier(today int64, history []int64) int {
	var sum, n int64
	for _, h := range history {
		if h > 0 {
			sum, n = sum+h, n+1
		}
	}
	usual := b.HearthFloor
	if n > 0 {
		usual = sum / n
	}
	tier := 0
	for i, at := range b.HearthAt {
		if today >= usual*at/100 {
			tier = i + 1
		}
	}
	return tier
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

// QuestReward is a dealt quest's reward, from a random r.
func (b *Balance) QuestReward(r uint64) int64 {
	if r%b.RareOneIn == 0 {
		return b.QuestRare
	}
	return b.QuestCommon
}

var ranks = []struct {
	from int
	name string
}{{50, "great skua"}, {30, "raider"}, {20, "skimmer"}, {10, "glider"}, {5, "fledgling"}, {0, "hatchling"}}

// Rank is the bird a level makes you.
func Rank(l int) string {
	for _, r := range ranks {
		if l >= r.from {
			return r.name
		}
	}
	return ranks[len(ranks)-1].name
}

// Job is the title for how someone earns, from what they have earned it
// by: voice minutes, earning messages and quests done, each weighed by
// what it pays at base.
func (b *Balance) Job(voiceMin, chatN, questN int64) string {
	v := voiceMin * b.VoiceBase
	c := chatN * (b.ChatMin + b.ChatMax) / 2
	q := questN * b.QuestCommon
	switch {
	case v == 0 && c == 0 && q == 0:
		return "wanderer"
	case v >= c && v >= q:
		return "bard"
	case c >= q:
		return "scribe"
	}
	return "ranger"
}
