package pp

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"unicode"
)

func fingerprint(b Balance) string {
	return fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%#v", b)))[:6]
}

func TestBalanceChangeHasChangelog(t *testing.T) {
	latest := Changelog[0]
	if got := fingerprint(Default); latest.Balance != got {
		t.Fatalf("the balance changed: add a changelog entry saying how, with Balance %q", got)
	}
	if latest.Version != Default.Version {
		t.Errorf("newest changelog entry is %s, the balance says %s", latest.Version, Default.Version)
	}
}

// Changelog lines are read by members, so they keep the bird voice.
func TestChangelogVoice(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Changelog {
		if seen[e.Version] {
			t.Errorf("version %s is in the changelog twice", e.Version)
		}
		seen[e.Version] = true
		for _, l := range e.Lines {
			switch {
			case strings.IndexFunc(l, unicode.IsUpper) >= 0:
				t.Errorf("%s: %q has a capital", e.Version, l)
			case strings.HasSuffix(l, "."), strings.Contains(l, "!"):
				t.Errorf("%s: %q ends with a stop or exclaims", e.Version, l)
			case strings.Count(l, ";") > 1:
				t.Errorf("%s: %q chains semicolons", e.Version, l)
			}
		}
	}
}
