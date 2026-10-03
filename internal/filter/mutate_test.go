package filter

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestMutatedSlurs writes every rule's word the way somebody dodging a
// filter does (leet, lookalikes, held letters, separators, a word glued on
// the front, a plural) and holds two properties over thousands of draws:
//
//   - A rewrite always keeps the sentence around it, is valid UTF-8, and
//     never itself matches, so it is posted once and left alone.
//   - Nothing written without a space gets through. Splitting a short word
//     at a real space ("goo k") is let through on purpose, since ordinary
//     words spell short rules across their boundary ("go ok").
func TestMutatedSlurs(t *testing.T) {
	f := Default()
	words := []string{"nigger", "faggot", "tranny", "troon", "gook", "chink", "kike", "cunny", "cuteandfunny"}
	subs := map[rune][]rune{
		'a': {'4', '@', 0x0430, 0xFF41}, 'e': {'3', 0x0435, 0x00E9}, 'i': {'1', '!', 'l', 0x0456, 0x00ED},
		'o': {'0', 0x03BF, 0x043E}, 'g': {'9', '6', 0x0262}, 't': {'7', 0x0442}, 'n': {0x0578, 0xFF4E, 0x0274}, 'u': {'v'},
	}
	seps := []string{".", " ", "-", "_", " . ", string(rune(0x200B)), string(rune(0x00AD)), "**"}
	r := rand.New(rand.NewPCG(3, 4))
	for range 20000 {
		w := words[r.IntN(len(words))]
		var b strings.Builder
		if r.IntN(4) == 0 {
			b.WriteString("space")
		}
		for j, c := range w {
			if j > 0 && r.IntN(5) == 0 {
				b.WriteString(seps[r.IntN(len(seps))])
			}
			if alts, ok := subs[c]; ok && r.IntN(3) == 0 {
				c = alts[r.IntN(len(alts))]
			}
			b.WriteRune(c)
			if r.IntN(8) == 0 {
				b.WriteRune(c)
			}
		}
		if r.IntN(4) == 0 {
			b.WriteString("s")
		}
		in := "hey " + b.String() + " ok"
		v := f.Check(in)
		switch {
		case !v.Rewrote && !strings.Contains(b.String(), " "):
			t.Fatalf("missed %q", in)
		case !utf8.ValidString(v.Text):
			t.Fatalf("Check(%q) = %q, invalid UTF-8", in, v.Text)
		case !strings.HasPrefix(v.Text, "hey ") || !strings.HasSuffix(v.Text, " ok"):
			t.Fatalf("Check(%q) = %q lost the sentence", in, v.Text)
		case f.Check(v.Text).Rewrote:
			t.Fatalf("Check(%q) = %q, which still matches", in, v.Text)
		}
	}
}

// TestKeysNeverHideAMatch is the prefilter's whole safety property: with
// every key removed, so every regex runs on every text, nothing comes out
// differently. Mutated slurs and random text from an alphabet of the
// letters, leet, separators and lookalikes the rules care about.
func TestKeysNeverHideAMatch(t *testing.T) {
	keyed := first()
	var rules []Rule
	for _, r := range Rules {
		r.Key = ""
		rules = append(rules, r)
	}
	bare := New(rules, Blocks)
	bare.pick = keyed.pick

	alpha := []rune("nigertayfoskuchdlbmpv 01345679!|*@$+.-_ \n")
	alpha = append(alpha, 0xFF4E, 0x0456, 0x0435, 0x200B, 0x00AD, 0x0336, 0x03BF, 0x0274, 0xFB00, 0x00ED, 0x3164, 0x0578)
	r := rand.New(rand.NewPCG(5, 6))
	var inputs []string
	for range 20000 {
		var b strings.Builder
		for range 1 + r.IntN(30) {
			b.WriteRune(alpha[r.IntN(len(alpha))])
		}
		inputs = append(inputs, b.String())
	}
	for _, w := range []string{"nigger", "faggot", "tranny", "troon", "gook", "chink", "kike", "cunny", "cute and funny"} {
		for range 2000 {
			var b strings.Builder
			for _, c := range w {
				if r.IntN(3) == 0 {
					b.WriteRune(alpha[r.IntN(len(alpha))])
				}
				b.WriteRune(c)
			}
			inputs = append(inputs, b.String())
		}
	}
	for _, in := range inputs {
		if a, b := keyed.Check(in), bare.Check(in); a != b {
			t.Fatalf("keys changed Check(%q): %+v without, %+v with", in, b, a)
		}
	}
}
