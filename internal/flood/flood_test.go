package flood

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

var t0 = time.Date(2026, 10, 19, 12, 18, 0, 0, time.UTC)

// play sends lines from member 7 in guild 3, every gap apart, and returns
// the index of the message that tripped, or -1.
func play(d *Detector, gap time.Duration, msgs []Message) int {
	for i, m := range msgs {
		m.Guild, m.User, m.ID = 3, 7, snowflake.ID(i+1)
		if m.Channel == 0 {
			m.Channel = 4
		}
		m.At = t0.Add(time.Duration(i) * gap)
		if trip, _ := d.Observe(m); trip {
			return i
		}
	}
	return -1
}

func texts(lines ...string) []Message {
	out := make([]Message, len(lines))
	for i, l := range lines {
		out[i] = Message{Text: l}
	}
	return out
}

func repeat(n int, f func(i int) Message) []Message {
	out := make([]Message, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

func TestTalkIsNotSpam(t *testing.T) {
	chat := repeat(40, func(i int) Message {
		return Message{Text: fmt.Sprintf("line %s about the weather", strings.Repeat("ab", i%7+1))}
	})
	for i := range chat {
		chat[i].Text = fmt.Sprintf("%c%s", 'a'+rune(i%26), chat[i].Text) + strings.Repeat("x", i)
	}
	cases := map[string]struct {
		gap  time.Duration
		msgs []Message
	}{
		"lively chatter": {1500 * time.Millisecond, chat},
		"one a second":   {time.Second, chat},
		"a split sentence": {1200 * time.Millisecond, texts(
			"so i was walking", "down to the harbour", "and there was this", "enormous gull", "stealing chips")},
		"short replies": {2 * time.Second, texts("lol", "ya", "no", "ok", "lol", "ya", "hm", "ok")},
		"a back and forth with replies": {6 * time.Second, repeat(20, func(i int) Message {
			return Message{Text: fmt.Sprintf("answer number %c to you", 'a'+rune(i)), Pings: []snowflake.ID{9}}
		})},
		"a few photos":                    {time.Second, repeat(6, func(i int) Message { return Message{Media: []string{fmt.Sprintf("a:%d:image/jpeg", 100000+i)}} })},
		"a few stickers":                  {2 * time.Second, repeat(5, func(i int) Message { return Message{Media: []string{fmt.Sprintf("s:%d", 900+i)}} })},
		"a reaction sticker now and then": {8 * time.Second, repeat(6, func(int) Message { return Message{Media: []string{"s:900"}} })},
		"emoji reactions":                 {3 * time.Second, texts("\U0001F602", "lol", "\U0001F62D\U0001F62D", "no way", "\U0001F480")},
		"one ping, a thread":              {2 * time.Second, append([]Message{{Text: "look at this", Pings: []snowflake.ID{9}}}, texts("it's a gull", "eating a whole pasty", "on the pier")...)},
	}
	for name, c := range cases {
		if i := play(&Detector{}, c.gap, c.msgs); i >= 0 {
			t.Errorf("%s tripped at message %d", name, i+1)
		}
	}
}

func TestSpamTrips(t *testing.T) {
	cases := map[string]struct {
		gap  time.Duration
		msgs []Message
		by   int // trips by this message
	}{
		// The bahamas: one word replies, each pinging the same member.
		"reply pings": {2 * time.Second, repeat(17, func(i int) Message {
			return Message{Text: []string{"hi", "hi", "hu", "hu", "hu", "h"}[i%6], Pings: []snowflake.ID{9}}
		}), 5},
		"reply pings, slower": {3500 * time.Millisecond, repeat(17, func(i int) Message {
			return Message{Text: []string{"hi", "hi", "hu", "hu", "hu", "h"}[i%6], Pings: []snowflake.ID{9}}
		}), 6},
		"a flood": {300 * time.Millisecond, repeat(30, func(i int) Message { return Message{Text: fmt.Sprintf("buy %c now", 'a'+rune(i))} }), 15},
		"slow repeats, padded": {3 * time.Second, repeat(20, func(i int) Message {
			return Message{Text: fmt.Sprintf("free nitro %d at discord dot gift%s", i, strings.Repeat(" ", i))}
		}), 6},
		"zero width variants": {3 * time.Second, repeat(20, func(i int) Message {
			return Message{Text: strings.Repeat("\u200b", i) + "free nitro at the li\u200bnk"}
		}), 6},
		"fullwidth variants": {3 * time.Second, repeat(20, func(i int) Message {
			if i%2 == 0 {
				return Message{Text: "ｆｒｅｅ ｎｉｔｒｏ ｈｅｒｅ"}
			}
			return Message{Text: "free nitro here!!"}
		}), 6},
		"a channel hopper": {2 * time.Second, repeat(20, func(i int) Message {
			return Message{Text: "join my server for prizes", Channel: snowflake.ID(10 + i)}
		}), 5},
		// The melting pot: the same sticker six times in a row.
		"the same sticker": {time.Second, repeat(6, func(int) Message { return Message{Media: []string{"s:1234"}} }), 5},
		"the same image, captions varied": {2 * time.Second, repeat(10, func(i int) Message {
			return Message{Text: fmt.Sprintf("look %c", 'a'+rune(i)), Media: []string{"a:48211:image/png"}}
		}), 6},
		"emoji only": {time.Second, repeat(12, func(i int) Message {
			return Message{Text: strings.Repeat("\U0001F62D", i%3+1) + strings.Repeat("\U0001F525", i%2+1) + "\U0001F62D\U0001F525"}
		}), 6},
		"a lone emoji, over and over": {time.Second, repeat(15, func(int) Message { return Message{Text: "\U0001F62D"} }), 8},
		"a mention bomb":              {time.Second, []Message{{Text: "hey", Pings: []snowflake.ID{1, 2, 3, 4, 5}}}, 1},
		"a mention drip": {2 * time.Second, repeat(10, func(i int) Message {
			return Message{Text: fmt.Sprintf("hey %c you", 'a'+rune(i)), Pings: []snowflake.ID{snowflake.ID(20 + i)}}
		}), 4},
		"everyone": {2 * time.Second, repeat(10, func(i int) Message {
			return Message{Text: fmt.Sprintf("@everyone %c look", 'a'+rune(i)), Everyone: true}
		}), 3},
		"a text wall": {2 * time.Second, repeat(10, func(i int) Message { return Message{Text: strings.Repeat(fmt.Sprintf("%c\n", 'a'+rune(i)), 20)} }), 5},
	}
	for name, c := range cases {
		i := play(&Detector{}, c.gap, c.msgs)
		if i < 0 || i+1 > c.by {
			t.Errorf("%s tripped at message %d, want by %d", name, i+1, c.by)
		}
	}
}

func TestATripHandsBackTheBurstThenGoesQuiet(t *testing.T) {
	d := &Detector{}
	var burst []Seen
	for i := range 12 {
		trip, b := d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: snowflake.ID(i + 1), At: t0.Add(time.Duration(i) * 200 * time.Millisecond), Text: "spam spam spam"})
		if trip {
			burst = b
			break
		}
	}
	if len(burst) == 0 || len(burst) > ring || burst[len(burst)-1].ID != snowflake.ID(len(burst)) {
		t.Fatalf("burst %v", burst)
	}
	for i := range 20 {
		if trip, _ := d.Observe(Message{Guild: 3, User: 7, Channel: 4, At: t0.Add(time.Minute + time.Duration(i)*100*time.Millisecond), Text: "spam"}); trip {
			t.Fatal("tripped again inside the quiet")
		}
	}
	// What came in during the quiet is still remembered, for Since.
	after := d.Since(3, 7, t0.Add(time.Minute))
	if len(after) != ring || after[len(after)-1].ID != 0 || len(d.Since(3, 9, t0)) != 0 {
		t.Fatalf("since the trip: %v", after)
	}
	// Someone else in the same guild is their own score.
	if trip, _ := d.Observe(Message{Guild: 3, User: 8, Channel: 4, At: t0.Add(time.Minute), Text: "spam spam spam"}); trip {
		t.Fatal("member 8 tripped on member 7's score")
	}
}

func TestTripCleanupRetainsTheFullInitialWindow(t *testing.T) {
	d := &Detector{}
	for i := range 20 {
		d.Observe(Message{
			Guild: 3, User: 7, Channel: 4, ID: snowflake.ID(i + 1),
			At: t0.Add(time.Duration(i*3) * time.Second),
		})
	}
	trip, burst := d.Observe(Message{
		Guild: 3, User: 7, Channel: 4, ID: 21, At: t0.Add(59 * time.Second),
		Pings: []snowflake.ID{1, 2, 3, 4, 5},
	})
	if !trip || len(burst) > ring {
		t.Fatalf("trip %v with scoring burst %d", trip, len(burst))
	}
	initial := d.DrainSince(3, 7)
	if len(initial) != 21 || initial[0].ID != 1 || initial[len(initial)-1].ID != 21 {
		t.Fatalf("cleanup retained %d initial messages: first %v, last %v", len(initial), initial[0].ID, initial[len(initial)-1].ID)
	}
}

func TestCleanupQueueLifecycleAndMissingMembers(t *testing.T) {
	d := &Detector{}
	if d.TakeOverflow(3, 7) || d.DrainSince(3, 7) != nil || d.Pending(3, 7) {
		t.Fatal("an unknown member had cleanup state")
	}
	d.DiscardPending(3, 7)

	s := &member{overflow: true}
	d.members.Store(who{3, 7}, s)
	if !d.TakeOverflow(3, 7) || d.TakeOverflow(3, 7) {
		t.Fatal("overflow wasn't taken and cleared")
	}

	trip, _ := d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 1, At: t0, Pings: []snowflake.ID{1, 2, 3, 4, 5}})
	if !trip || !d.Pending(3, 7) {
		t.Fatal("trip didn't open a cleanup queue")
	}
	if got := d.DrainSince(3, 7); len(got) != 1 || got[0].ID != 1 || d.Pending(3, 7) {
		t.Fatalf("drain returned %v; queue pending %v", got, d.Pending(3, 7))
	}
	d.DiscardPending(3, 7)
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 2, At: t0.Add(time.Second)})
	if d.Pending(3, 7) || d.DrainSince(3, 7) != nil {
		t.Fatal("cleanup queue reopened after it was discarded")
	}
}

func TestFinishCleanupKeepsQueueOpenUntilTailIsDrained(t *testing.T) {
	d := &Detector{}
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 1, At: t0, Pings: []snowflake.ID{1, 2, 3, 4, 5}})
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 2, At: t0.Add(time.Second)})
	final, overflow, finished := d.FinishCleanup(3, 7, false)
	if overflow || finished || len(final) != 2 || final[0].ID != 1 || final[1].ID != 2 {
		t.Fatalf("final batch %v, overflow %v, finished %v", final, overflow, finished)
	}
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 3, At: t0.Add(2 * time.Second)})
	if !d.Pending(3, 7) {
		t.Fatal("message arrived before cleanup completed but was not queued")
	}
	if final, overflow, finished = d.FinishCleanup(3, 7, false); overflow || finished || len(final) != 1 || final[0].ID != 3 {
		t.Fatalf("second final batch %v, overflow %v, finished %v", final, overflow, finished)
	}
	if final, overflow, finished = d.FinishCleanup(3, 7, false); len(final) != 0 || overflow || !finished {
		t.Fatalf("empty final handoff returned %v, overflow %v, finished %v", final, overflow, finished)
	}
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 4, At: t0.Add(3 * time.Second)})
	if d.Pending(3, 7) {
		t.Fatal("message after completed cleanup was queued")
	}
}

func TestFinishCleanupResumesScoringWhenNoSanctionApplied(t *testing.T) {
	d := &Detector{}
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 1, At: t0, Pings: []snowflake.ID{1, 2, 3, 4, 5}})
	d.DrainSince(3, 7)
	if _, _, finished := d.FinishCleanup(3, 7, true); !finished {
		t.Fatal("empty cleanup did not finish")
	}
	trip, _ := d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 2, At: t0.Add(time.Second), Pings: []snowflake.ID{1, 2, 3, 4, 5}})
	if !trip {
		t.Fatal("scoring stayed quiet after no sanction was applied")
	}
}

func TestCleanupIntakeOutlivesQuietWindowUntilContainmentFinishes(t *testing.T) {
	d := &Detector{}
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 1, At: t0, Pings: []snowflake.ID{1, 2, 3, 4, 5}})
	d.DrainSince(3, 7)
	at := t0.Add(Quiet + time.Second)
	trip, _ := d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 2, At: at, Pings: []snowflake.ID{1, 2, 3, 4, 5}})
	if trip || !d.Pending(3, 7) {
		t.Fatal("cleanup expired with the quiet window instead of retaining the new message")
	}
	if got := d.DrainSince(3, 7); len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("long-running cleanup got %v, want message 2", got)
	}
}

func TestRetainPendingKeepsFailedDeletesAttachedToOpenIncident(t *testing.T) {
	d := &Detector{}
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 1, At: t0, Pings: []snowflake.ID{1, 2, 3, 4, 5}})
	d.DrainSince(3, 7)
	if overflow := d.RetainPending(3, 7, []Seen{{Channel: 4, ID: 1}}); overflow {
		t.Fatal("small unresolved queue overflowed")
	}
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 2, At: t0.Add(time.Second)})
	got := d.DrainSince(3, 7)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("unresolved incident retained %v, want failed ID 1 and new ID 2", got)
	}
}

func TestCleanupQueueOverflowAndRecentHistoryCompaction(t *testing.T) {
	now := t0.Add(2 * time.Minute)
	s := &member{}
	s.recent = make([]seen, pendingLimit)
	for i := range s.recent {
		s.recent[i].at = now
	}
	s.remember(seen{Seen: Seen{Channel: 4, ID: 1}, at: now})
	if !s.overflow {
		t.Fatal("recent history cap didn't mark overflow")
	}
	s.remember(seen{Seen: Seen{Channel: 4, ID: 2}, at: now.Add(2 * time.Minute)})
	if s.overflow {
		t.Fatal("expired overflow stayed active")
	}

	d := &Detector{}
	queued := &member{quiet: now.Add(time.Minute), cleanup: true, pending: make([]Seen, pendingLimit)}
	d.members.Store(who{3, 7}, queued)
	d.Observe(Message{Guild: 3, User: 7, Channel: 4, ID: 4, At: now})
	if !d.TakeOverflow(3, 7) {
		t.Fatal("cleanup queue cap didn't mark overflow")
	}

	compacted := &member{}
	compacted.recent = make([]seen, 2048)
	for i := range compacted.recent {
		compacted.recent[i].at = now.Add(-2 * time.Minute)
	}
	for i := 1024; i < len(compacted.recent); i++ {
		compacted.recent[i].at = now
	}
	compacted.remember(seen{Seen: Seen{Channel: 4, ID: 3}, at: now})
	if compacted.recentStart != 0 || len(compacted.recent) != 1025 {
		t.Fatalf("recent history wasn't compacted: start %d len %d", compacted.recentStart, len(compacted.recent))
	}
}

func TestSweepForgetsTheQuiet(t *testing.T) {
	d := &Detector{}
	d.Observe(Message{Guild: 3, User: 7, At: t0, Text: "hello there"})
	d.Observe(Message{Guild: 3, User: 8, At: t0.Add(forget + 2*time.Minute), Text: "hello"})
	if _, ok := d.members.Load(who{3, 7}); ok {
		t.Fatal("a member quiet for longer than forget was kept")
	}
	if _, ok := d.members.Load(who{3, 8}); !ok {
		t.Fatal("the member just seen was swept")
	}
}

func BenchmarkObserve(b *testing.B) {
	d := &Detector{}
	m := Message{Guild: 3, User: 7, Channel: 4, Text: "just a normal line of chat", Pings: []snowflake.ID{9}}
	for i := 0; b.Loop(); i++ {
		m.At = t0.Add(time.Duration(i) * 5 * time.Second)
		d.Observe(m)
	}
}
