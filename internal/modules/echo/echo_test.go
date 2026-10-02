package echo

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	ok := map[string]string{
		"hi":             "hi",
		"  padded  ":     "padded",
		"a # in the mid": "a # in the mid",
		"> quoted":       "> quoted",
	}
	for in, want := range ok {
		if got, err := check(in); err != nil || got != want {
			t.Errorf("check(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "   ",
		"a\nb", "a\rb", "a b", "a b", "a\tb",
		"# x", "-# x", "> -# x", "- -# x", "* ## x", "  -# echoed through skua by @mod",
		strings.Repeat("a", maxText+1),
	} {
		if got, err := check(in); err == nil {
			t.Errorf("check(%q) = %q, want refused", in, got)
		}
	}
}

func TestMarkerIsOneEscapedSubtextLine(t *testing.T) {
	got := marker("a_b_c")
	if got != "\n-# echoed through skua by @"+`a\_b\_c` {
		t.Fatalf("marker = %q", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("marker must be exactly one new line: %q", got)
	}
}
