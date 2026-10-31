package pp

import (
	"testing"
	"time"
)

var b = &Default

func TestPoolsJitterStaysInBounds(t *testing.T) {
	lo, hi := b.Pool*(100-b.Jitter)/100, b.Pool*(100+b.Jitter)/100
	seen := map[int64]bool{}
	for day := range int64(200) {
		p := b.Pools(7, 1, 2, day)
		if p.Pool < lo || p.Pool > hi {
			t.Fatalf("day %d: pool %d outside %d..%d", day, p.Pool, lo, hi)
		}
		seen[p.Pool] = true
	}
	if p := b.Pools(7, 1, 2, 9).Pace; p < 100-b.IdleJitter || p > 100+b.IdleJitter {
		t.Errorf("pace %d strays past the jitter", p)
	}
	if len(seen) < 10 {
		t.Errorf("only %d distinct pools in 200 days; the cap is guessable", len(seen))
	}
	if b.Pools(7, 1, 2, 3) == b.Pools(8, 1, 2, 3) && b.Pools(7, 1, 2, 4) == b.Pools(8, 1, 2, 4) {
		t.Error("the seed doesn't move the pools")
	}
}

func TestEarnTapers(t *testing.T) {
	p := Pools{Pool: 1000}
	var d Day
	if got := b.Earn(&d, p, false, 600, 0); got != 600 {
		t.Errorf("first 60%% paid %d; want all 600", got)
	}
	// 600..1000 pays half, past it the voice tail.
	if got := b.Earn(&d, p, false, 500, 0); got != 400*50/100+100*b.VoiceTail/100 {
		t.Errorf("across the edges paid %d", got)
	}
	if d.Voice != 1100 {
		t.Errorf("counted %d against the pool; want 1100", d.Voice)
	}
	if got := b.Earn(&Day{}, p, false, 100, 10); got != 110 {
		t.Errorf("a 10%% buff on 100 paid %d", got)
	}
}

// Chat and voice fill one pool, so either can top a day out.
func TestChatFillsThePoolLikeVoice(t *testing.T) {
	p := Pools{Pool: 1000}
	var d Day
	if got := b.Earn(&d, p, true, 600, 0); got != 600 {
		t.Errorf("chat's first 60%% paid %d; want all 600", got)
	}
	if got := b.Earn(&d, p, true, 500, 0); got != 400*50/100+100*b.ChatTail/100 {
		t.Errorf("chat across the edges paid %d", got)
	}
	if got := b.Earn(&d, p, false, 100, 0); got != 100*b.VoiceTail/100 {
		t.Errorf("voice after a full day of chat paid %d; want the voice tail", got)
	}
	if a := d.Activity(); a[BoardChat] != 1100 || a[BoardVoice] != 100 {
		t.Errorf("standing %v; want 1100 chat and 100 voice", a)
	}
}

func TestRested(t *testing.T) {
	p := Pools{Pool: 1000}
	most := b.Pool * b.RestedMax / 100
	cases := []struct {
		prev Day
		gap  int64
		want int64
	}{
		{Day{Rested: 7}, 0, 7},
		{Day{}, 3, most},
		{Day{Voice: 400}, 1, 300},
		{Day{Voice: 2000}, 1, 0},
		{Day{Rested: 1e9}, 1, most},
	}
	for _, c := range cases {
		if got := b.Rested(c.prev, p, c.gap); got != c.want {
			t.Errorf("Rested(%+v, gap %d) = %d; want %d", c.prev, c.gap, got, c.want)
		}
	}
}

func TestVigor(t *testing.T) {
	p := Pools{Pool: 1000}
	for _, c := range []struct {
		used  int64
		left  int64
		phase string
	}{{0, 100, "fresh"}, {400, 60, "warm"}, {800, 20, "tired"}, {1500, 0, "spent"}} {
		left, phase := b.Vigor(Day{Voice: c.used}, p)
		if left != c.left || phase != c.phase {
			t.Errorf("used %d: %d%% %s; want %d%% %s", c.used, left, phase, c.left, c.phase)
		}
	}
	if _, phase := b.Vigor(Day{}, Pools{}); phase != "spent" {
		t.Errorf("an empty pool reads %s", phase)
	}
}

func TestChat(t *testing.T) {
	cases := []struct {
		since  time.Duration
		runes  int
		repeat bool
		want   bool
	}{
		{b.ChatCooldown, 10, false, true},
		{b.ChatCooldown - time.Second, 10, false, false},
		{b.ChatCooldown, 2, false, false},
		{b.ChatCooldown, 10, true, false},
		{b.ChatCooldown, -1, true, true},
	}
	for _, c := range cases {
		if got := b.ChatPays(c.since, c.runes, c.repeat); got != c.want {
			t.Errorf("ChatPays(%v, %d, %v) = %v", c.since, c.runes, c.repeat, got)
		}
	}
	if !b.ChatCounts(b.ActivityGap, 10, false) || b.ChatCounts(b.ActivityGap-1, 10, false) || b.ChatCounts(time.Hour, 10, true) {
		t.Error("activity doesn't count every real message once per gap")
	}
	if b.ActivityGap >= b.ChatCooldown {
		t.Error("activity counts no more often than pay")
	}
	for r := range uint64(1000) {
		if x := b.ChatRaw(r); x < b.ChatMin || x > b.ChatMax {
			t.Fatalf("ChatRaw(%d) = %d", r, x)
		}
	}
}

func TestVoiceRaw(t *testing.T) {
	friend := Seat{Age: b.CompanionAge}
	grace, open := b.IdleGrace, b.OpenGrace
	cases := []struct {
		name   string
		me     Seat
		others []Seat
		want   int64
	}{
		{"alone", Seat{}, nil, 0},
		{"with a friend", Seat{}, []Seat{friend}, b.VoiceBase * b.Unmuted / 100},
		{"muted self", Seat{SelfMute: true}, []Seat{friend}, b.VoiceBase},
		{"muted through the grace", Seat{SelfMute: true, Idle: grace}, []Seat{friend}, b.VoiceBase},
		{"muted halfway down the fade", Seat{SelfMute: true, Idle: grace + b.IdleFade/2}, []Seat{friend}, b.VoiceBase * (100 - (100-b.IdleFloor)/2) / 100},
		{"muted for hours", Seat{SelfMute: true, Idle: 10 * time.Hour}, []Seat{friend}, b.VoiceBase * b.IdleFloor / 100},
		{"deaf", Seat{Deaf: true, SelfMute: true}, []Seat{friend}, b.VoiceBase * b.Deafened / 100},
		{"deaf past half the grace", Seat{Deaf: true, Idle: grace/2 + b.IdleFade}, []Seat{friend}, b.VoiceBase * b.Deafened / 100 * b.DeafFloor / 100},
		{"open mic through its long grace", Seat{Idle: open}, []Seat{friend}, b.VoiceBase * b.Unmuted / 100},
		{"open mic past it is muted", Seat{Idle: open + time.Minute}, []Seat{friend}, b.VoiceBase},
		{"silent open mic for hours", Seat{Idle: open + grace + b.IdleFade}, []Seat{friend}, b.VoiceBase * b.IdleFloor / 100},
		{"server muted", Seat{Muted: true}, []Seat{friend}, 0},
		{"stage audience", Seat{Suppressed: true}, []Seat{friend}, 0},
		{"a bot", Seat{Bot: true}, []Seat{friend}, 0},
		{"with a bot", Seat{}, []Seat{{Bot: true, Age: b.CompanionAge}}, 0},
		{"with a fresh alt", Seat{}, []Seat{{Age: time.Hour}}, 0},
		{"with someone deaf", Seat{}, []Seat{{Deaf: true, Age: b.CompanionAge}}, 0},
	}
	for _, c := range cases {
		if got := b.VoiceRaw(c.me, c.others, 100); got != c.want {
			t.Errorf("%s: %d; want %d", c.name, got, c.want)
		}
	}
}

func TestBuffAndHearth(t *testing.T) {
	if got := b.Buff(Wearing{Crest: true, Banner: true, Plume: true, Guests: 99}, 3); got != b.Crest+b.Banner+b.Plume+b.SuretyMax+b.Hearth[2] {
		t.Errorf("every buff: %d", got)
	}
	if got := b.Buff(Wearing{Guests: 2}, 0); got != 2*b.Surety {
		t.Errorf("two guests: %d", got)
	}
	if got := b.Buff(Wearing{}, 0); got != 0 {
		t.Errorf("no buff: %d", got)
	}
	if got := b.HearthTier(b.HearthFloor, nil); got != 1 {
		t.Errorf("a usual day with no history is tier %d; want 1", got)
	}
	// A usual day of 2000 lights at 2000, 2300 and 2600.
	history := []int64{0, 1000, 3000}
	for today, want := range map[int64]int{1999: 0, 2000: 1, 2300: 2, 2599: 2, 2600: 3} {
		if got := b.HearthTier(today, history); got != want {
			t.Errorf("%d against a usual 2000: tier %d; want %d", today, got, want)
		}
	}
}

func TestLevel(t *testing.T) {
	cases := []struct {
		total, into int64
		level       int
	}{
		{0, 0, 0},
		{b.Need(0) - 1, b.Need(0) - 1, 0},
		{b.Need(0), 0, 1},
		{b.Need(0) + b.Need(1) + 5, 5, 2},
	}
	for _, c := range cases {
		l, into, need := b.Level(c.total)
		if l != c.level || into != c.into || need != b.Need(l) {
			t.Errorf("Level(%d) = %d, %d, %d", c.total, l, into, need)
		}
	}
	if b.Need(0) != 300*unit || b.Need(1) != 475*unit {
		t.Errorf("curve: %d %d", b.Need(0), b.Need(1))
	}
}

func TestQuestRoll(t *testing.T) {
	var n [3]uint64
	for r := range uint64(2000) {
		tier, pp := b.QuestRoll(r)
		if pp != b.QuestPP[tier] {
			t.Fatalf("tier %d paid %d", tier, pp)
		}
		n[tier]++
	}
	if n != [3]uint64{1800, 150, 50} {
		t.Errorf("2000 rolls fell %v; want 1800, 150, 50", n)
	}
	for i := 1; i < len(b.QuestPP); i++ {
		if b.QuestPP[i] <= b.QuestPP[i-1] || b.QuestOdds[i] >= b.QuestOdds[i-1] {
			t.Errorf("tier %d isn't rarer and richer than tier %d", i, i-1)
		}
	}
}

// The warm side lasts a newcomer's first WarmDays days and adds WarmSide
// beside the other buffs.
func TestWarmSide(t *testing.T) {
	b := Default
	if !b.Warm(100, 100) || !b.Warm(100, 100+b.WarmDays-1) || b.Warm(100, 100+b.WarmDays) {
		t.Errorf("the warm side isn't the first %d days", b.WarmDays)
	}
	if got := b.Buff(Wearing{Warm: true, Crest: true}, 0); got != b.WarmSide+b.Crest {
		t.Errorf("warm and crest: %d", got)
	}
}
