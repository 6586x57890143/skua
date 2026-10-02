package brand

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// generated is em and en dash, ellipsis and curly quotes, spelled as code
// points so this file passes CI's prose check itself.
var generated = string([]rune{0x2014, 0x2013, 0x2026, 0x2018, 0x2019, 0x201c, 0x201d})

// The voice is shared by every bird: lowercase, no em dashes, ellipses or
// curly quotes, no oxford commas. Discord cuts a custom status at 128.
func TestStatusKeepsTheVoice(t *testing.T) {
	oxford := regexp.MustCompile(`, [^,]+, (and|or) `)
	seen := map[string]bool{}
	for _, s := range Status {
		switch {
		case s == "" || utf8.RuneCountInString(s) > 128:
			t.Errorf("%q: empty or over 128", s)
		case s != strings.ToLower(s):
			t.Errorf("%q: not lowercase", s)
		case strings.ContainsAny(s, generated):
			t.Errorf("%q: em dash, ellipsis or curly quote", s)
		case oxford.MatchString(s):
			t.Errorf("%q: oxford comma", s)
		case strings.HasSuffix(s, "."):
			t.Errorf("%q: a status line doesn't end on a full stop", s)
		case seen[s]:
			t.Errorf("%q: twice", s)
		}
		seen[s] = true
	}
}

func TestStatusAtCyclesWithoutRepeatingBackToBack(t *testing.T) {
	for n := range 3 * len(Status) {
		if StatusAt(n) == StatusAt(n+1) {
			t.Fatalf("ticks %d and %d show the same line", n, n+1)
		}
	}
	if StatusAt(len(Status)) != Status[0] {
		t.Fatal("the rotation does not wrap")
	}
}
