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
		"a few photos":       {time.Second, repeat(6, func(int) Message { return Message{} })},
		"one ping, a thread": {2 * time.Second, append([]Message{{Text: "look at this", Pings: []snowflake.ID{9}}}, texts("it's a gull", "eating a whole pasty", "on the pier")...)},
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
			return Message{Text: strings.Repeat("​", i) + "free nitro at the li​nk"}
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
		"a mention bomb": {time.Second, []Message{{Text: "hey", Pings: []snowflake.ID{1, 2, 3, 4, 5}}}, 1},
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
	// Someone else in the same guild is their own score.
	if trip, _ := d.Observe(Message{Guild: 3, User: 8, Channel: 4, At: t0.Add(time.Minute), Text: "spam spam spam"}); trip {
		t.Fatal("member 8 tripped on member 7's score")
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
