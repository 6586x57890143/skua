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
		if share := p.Chat * 100 / p.Pool; share < b.ChatShare-b.ChatJitter-1 || share > b.ChatShare+b.ChatJitter {
			t.Fatalf("day %d: chat share %d%%", day, share)
		}
		seen[p.Pool] = true
	}
	if len(seen) < 10 {
		t.Errorf("only %d distinct pools in 200 days; the cap is guessable", len(seen))
	}
	if b.Pools(7, 1, 2, 3) == b.Pools(8, 1, 2, 3) && b.Pools(7, 1, 2, 4) == b.Pools(8, 1, 2, 4) {
		t.Error("the seed doesn't move the pools")
	}
}

func TestEarnTapers(t *testing.T) {
	p := Pools{Pool: 1000, Chat: 400}
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

func TestChatPastItsShareSparesVoice(t *testing.T) {
	p := Pools{Pool: 1000, Chat: 400}
	var d Day
	if got := b.Earn(&d, p, true, 500, 0); got != 400+100*b.ChatTail/100 {
		t.Errorf("chat over its share paid %d", got)
	}
	if d.Chat != 400 {
		t.Errorf("chat counted %d against the pool; want its share, 400", d.Chat)
	}
	if got := b.Earn(&d, p, false, 200, 0); got != 200 {
		t.Errorf("voice after a chat flood paid %d; want 200 at full rate", got)
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
		{time.Minute, 10, false, true},
		{59 * time.Second, 10, false, false},
		{time.Minute, 2, false, false},
		{time.Minute, 10, true, false},
		{time.Minute, -1, true, true},
	}
	for _, c := range cases {
		if got := b.ChatPays(c.since, c.runes, c.repeat); got != c.want {
			t.Errorf("ChatPays(%v, %d, %v) = %v", c.since, c.runes, c.repeat, got)
		}
	}
	for r := range uint64(1000) {
		if x := b.ChatRaw(r); x < b.ChatMin || x > b.ChatMax {
			t.Fatalf("ChatRaw(%d) = %d", r, x)
		}
	}
}

func TestVoiceRaw(t *testing.T) {
	friend := Seat{Age: b.CompanionAge}
	cases := []struct {
		name   string
		me     Seat
		others []Seat
		want   int64
	}{
		{"alone", Seat{}, nil, 0},
		{"with a friend", Seat{}, []Seat{friend}, b.VoiceBase * b.Unmuted / 100},
		{"muted self", Seat{SelfMute: true}, []Seat{friend}, b.VoiceBase},
		{"deaf", Seat{Deaf: true}, []Seat{friend}, 0},
		{"server muted", Seat{Muted: true}, []Seat{friend}, 0},
		{"stage audience", Seat{Suppressed: true}, []Seat{friend}, 0},
		{"a bot", Seat{Bot: true}, []Seat{friend}, 0},
		{"with a bot", Seat{}, []Seat{{Bot: true, Age: b.CompanionAge}}, 0},
		{"with a fresh alt", Seat{}, []Seat{{Age: time.Hour}}, 0},
		{"with someone deaf", Seat{}, []Seat{{Deaf: true, Age: b.CompanionAge}}, 0},
	}
	for _, c := range cases {
		if got := b.VoiceRaw(c.me, c.others); got != c.want {
			t.Errorf("%s: %d; want %d", c.name, got, c.want)
		}
	}
}

func TestBuffAndHearth(t *testing.T) {
	if got := b.Buff(true, true, 3); got != b.Crest+b.Banner+b.Hearth[2] {
		t.Errorf("every buff: %d", got)
	}
	if got := b.Buff(false, false, 0); got != 0 {
		t.Errorf("no buff: %d", got)
	}
	if got := b.HearthTier(b.HearthFloor, nil); got != 2 {
		t.Errorf("a usual day with no history is tier %d; want 2", got)
	}
	history := []int64{0, 1000, 3000}
	for today, want := range map[int64]int{999: 0, 1000: 1, 2000: 2, 3000: 3} {
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

func TestQuestReward(t *testing.T) {
	var rare int
	for r := range uint64(400) {
		if b.QuestReward(r) == b.QuestRare {
			rare++
		}
	}
	if rare != 100 {
		t.Errorf("%d rare in 400; want 100", rare)
	}
}

func TestRankAndJob(t *testing.T) {
	for l, want := range map[int]string{0: "hatchling", 4: "hatchling", 5: "fledgling", 19: "glider", 50: "great skua", 99: "great skua"} {
		if got := Rank(l); got != want {
			t.Errorf("Rank(%d) = %s; want %s", l, got, want)
		}
	}
	for _, c := range []struct {
		v, c, q int64
		want    string
	}{{0, 0, 0, "wanderer"}, {100, 10, 1, "bard"}, {1, 100, 1, "scribe"}, {1, 1, 10, "ranger"}} {
		if got := b.Job(c.v, c.c, c.q); got != c.want {
			t.Errorf("Job(%d, %d, %d) = %s; want %s", c.v, c.c, c.q, got, c.want)
		}
	}
}

func TestDayTurnsAtUTCMidnight(t *testing.T) {
	before := time.Date(2026, 10, 14, 23, 59, 59, 0, time.UTC)
	if DayOf(before)+1 != DayOf(before.Add(time.Second)) {
		t.Error("the day doesn't turn at 00:00 utc")
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	if DayOf(before) != DayOf(before.In(berlin)) {
		t.Error("the day depends on the clock's zone")
	}
}
