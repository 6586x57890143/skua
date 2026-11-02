package pp

import (
	"fmt"
	"strings"
	"testing"
)

// saysNew is the nth of a run of messages that each say something new, for
// tests that need chat to pay again and again.
func saysNew(n int) string { return fmt.Sprintf("tide%d brings gull%d over pier%d", n, n, n) }

// Chat pays what says something: not a wall of one key, a row of emoji or
// a laugh however long, not one of the last lines again, alternated or
// padded, and not the same words shuffled. A conversation that moves on
// pays, common words and all.
func TestJudge(t *testing.T) {
	cases := []struct {
		text  string
		pays  bool
		about string
	}{
		{"good morning everyone", true, "a first line"},
		{"good morning everyone!!!", false, "the same line padded"},
		{"GOOD   morning, everyone", false, "the same line shouted"},
		{"how did the match go last night", true, "a new line"},
		{"good morning everyone", false, "alternating back to an old line"},
		{"everyone good morning", false, "the same words shuffled"},
		{"hahahahahahahaha", false, "a laugh, however long"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false, "a held key"},
		{"😂😂😂😂😂😂", false, "only emoji"},
		{"lol", false, "too little to say"},
		{"the match went badly but the night was good", true, "new words beside old ones"},
		{" sticker 175928847299117063", true, "a sticker"},
		{" sticker 175928847299117063", false, "the same sticker again"},
	}
	var c chatMemory
	for _, k := range cases {
		n, stale := c.judge(k.text)
		if pays := n >= Default.ChatMinRunes && !stale; pays != k.pays {
			t.Errorf("%s (%q): pays %v, %d letters, stale %v", k.about, k.text, pays, n, stale)
		}
	}
	// Long enough apart, a line can be said again.
	for i := range 2*wordGen + recentMessages {
		c.judge(saysNew(i))
	}
	if _, stale := c.judge("good morning everyone"); stale {
		t.Error("a line from long ago is still stale")
	}
}

// What a message is judged on costs no allocation, however long.
func TestJudgeAllocs(t *testing.T) {
	var c chatMemory
	long := strings.Repeat("a wall of text with words in it ", 60)
	if n := testing.AllocsPerRun(100, func() { c.judge(long) }); n != 0 {
		t.Errorf("judging a message allocated %v times", n)
	}
}

func BenchmarkJudge(b *testing.B) {
	var c chatMemory
	msgs := []string{"good morning everyone", "how did the match go last night 😂", strings.Repeat("spam ", 200), saysNew(3)}
	for i := 0; b.Loop(); i++ {
		c.judge(msgs[i%len(msgs)])
	}
}
