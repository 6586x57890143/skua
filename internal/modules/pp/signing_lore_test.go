package pp

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every class's signing lore is LORE.md's, word for word, and every class
// has its three pieces.
func TestSigningLoreIsLORE(t *testing.T) {
	b, err := os.ReadFile("../../../LORE.md")
	if err != nil {
		t.Fatal(err)
	}
	md := strings.Join(strings.Fields(string(b)), " ")
	for _, c := range Classes {
		pieces, ok := SigningLore[c.Key]
		if !ok {
			t.Errorf("%s has no signing lore", c.Key)
			continue
		}
		head := regexp.QuoteMeta("**" + strings.ToUpper(c.Key[:1]) + c.Key[1:] + ".** ")
		for i, label := range []string{"first: ", "tenth: ", "a full ring: "} {
			if !regexp.MustCompile(head + ".*?" + regexp.QuoteMeta(label+pieces[i])).MatchString(md) {
				t.Errorf("%s's %q isn't LORE.md's", c.Key, label)
			}
		}
	}
}

// What a member has signed teaches the three pieces in turn.
func TestLearned(t *testing.T) {
	if learned(signs{}) != [3]bool{} || learned(signs{chirographs: 1, signatures: 9}) != [3]bool{true, false, false} ||
		learned(signs{signatures: 10, rings: 1}) != [3]bool{false, true, true} {
		t.Error("what signing teaches")
	}
}
