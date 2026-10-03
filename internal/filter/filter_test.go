package filter

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// first is a Filter that always draws the first Sub, so rewrites are exact.
func first() *Filter {
	f := Default()
	f.pick = func(int) int { return 0 }
	return f
}

// runes builds a string from code points, so the evasions below are legible
// in source rather than written as the lookalikes they test.
func runes(rs ...rune) string { return string(rs) }

func TestRewritesExactly(t *testing.T) {
	f := first()
	cases := map[string]string{
		"shut up you faggot":            "shut up you frog",
		"f4ggots and n1gg3rs":           "frogs and ninjas",
		"tranny troon":                  "person person",
		"gook chink kike":               "goose chinchilla kite",
		"cunny cnny cunnies":            "cumin cumin cumins",
		"say hello to the spacenigger":  "say hello to the spaceninja",
		"a lil poem about niggernation": "a lil poem about ninjanation",
		"nigger":                        "ninja",
		"n.i.g.g.e.r and t r a n n y":   "ninja and person",
		"f*ggot":                        "frog",
	}
	for in, want := range cases {
		if v := f.Check(in); v.Text != want || !v.Rewrote || v.Block != "" {
			t.Errorf("Check(%q) = %+v, want %q", in, v, want)
		}
	}
}

// What gets past a filter that stops at ASCII and at single letters.
func TestEvasionsBeyondASCII(t *testing.T) {
	f := first()
	for name, in := range map[string]string{
		"held letters":         "niiiiggggerrrr",
		"held vowels, f-slur":  "faaaggot",
		"held letters, tranny": "trrrannny",
		"leet consonants":      "ni99er ni66er",
		"ph for f":             "phaggot",
		"7 for t":              "7ranny",
		"v for u":              "cvnny",
		"l for i":              "nlgger",
		"split into two words": "nig ger",
		"spaced out, short":    "g o o k",
		"three-character gaps": "n . i . g . g . e . r",
		"fullwidth":            runes(0xFF4E, 0xFF49, 0xFF47, 0xFF47, 0xFF45, 0xFF52),
		"math bold":            runes(0x1D427, 0x1D422, 0x1D420, 0x1D420, 0x1D41E, 0x1D42B),
		"circled":              runes(0x24DD, 0x24D8, 0x24D6, 0x24D6, 0x24D4, 0x24E1),
		"cyrillic i and e":     runes('n', 0x0456, 'g', 'g', 0x0435, 'r'),
		"greek o, troon":       runes('t', 'r', 0x03BF, 0x03BF, 'n'),
		"armenian n":           runes(0x0578, 'i', 'g', 'g', 'e', 'r'),
		"small capitals":       runes(0x0274, 0x026A, 0x0262, 0x0262, 0x1D07, 0x0280),
		"accents":              runes('n', 0x00ED, 'g', 'g', 0x00E9, 'r'),
		"zero-width spaces":    runes('n', 0x200B, 'i', 0x200B, 0x200B, 0x200B, 0x200B, 'g', 'g', 'e', 'r'),
		"soft hyphens":         runes('f', 0x00AD, 'a', 0x00AD, 'g', 'g', 'o', 't'),
		"zalgo":                runes('t', 0x0336, 0x0335, 'r', 0x0334, 'a', 'n', 0x0338, 'n', 'y', 0x0337),
		"hangul filler":        runes('n', 0x3164, 'i', 'g', 'g', 'e', 'r'),
	} {
		v := f.Check(in)
		if !v.Rewrote {
			t.Errorf("%s: %q was not rewritten", name, in)
			continue
		}
		if again := f.Check(v.Text); again.Rewrote {
			t.Errorf("%s: rewrite %q still matches", name, v.Text)
		}
	}
	// The original around a folded match survives byte for byte.
	in := "hey " + runes(0xFF4E, 0xFF49, 0xFF47, 0xFF47, 0xFF45, 0xFF52) + "s, " + runes(0x00E9) + "t" + runes(0x00E9)
	if got, want := f.Check(in).Text, "hey ninjas, "+runes(0x00E9)+"t"+runes(0x00E9); got != want {
		t.Errorf("folded rewrite = %q, want %q", got, want)
	}
}

func TestLeavesOrdinaryTextAlone(t *testing.T) {
	f := Default()
	for _, in := range []string{
		"you are an absolute clown and everyone here knows it",
		"he sniggered at the niggardly tip",
		"the whole contract is gobbledygook to me",
		"that reads like gobbledegook",
		"hangook food is the best food",
		"there is a chink in the armour of that argument",
		"the cny rate before CUNY term starts",
		"that was a cunning plan by Cunningham",
		"the cat used to be fine but now it is dull",
		"Niger and Nigeria share a border",
		"a big gerbil ate the begin block",
		"a fagot of sticks for the fire",
		"the troop marched past the tron arcade, trans rights",
		"goods and goofs",
		"let's go ok then, kik er off", // gook and kike across a word boundary
		"the patrol was tro on duty",
		"you absolute muppet",
		"the release was 2024-01-15",
		"come join https://discord.gg/abcdef we are nice",
		runes(0x0441, 0x0430, 0x0442) + " is Russian for nothing in particular",
		"",
	} {
		if v := f.Check(in); v.Rewrote || v.Block != "" || v.Text != in {
			t.Errorf("Check(%q) = %+v, want it untouched", in, v)
		}
	}
	// A phrase that spares one match spares only that match: saying "armor"
	// or "a chink in" elsewhere is not a way to post the rest.
	exact := first()
	for in, want := range map[string]string{
		"you chink, nice armor":                "you chinchilla, nice armor",
		"a chink in the armour, and you chink": "a chink in the armour, and you chinchilla",
		"a chink of light shone on the faggot": "a chink of light shone on the frog",
	} {
		if got := exact.Check(in).Text; got != want {
			t.Errorf("Check(%q) = %q, want %q", in, got, want)
		}
	}
	// The veto is the whole word: it spares that word, not whatever is
	// glued onto it.
	if !f.Check("sniggernation").Rewrote {
		t.Error("the innocent list is being used as an evasion")
	}
}

func TestBlocks(t *testing.T) {
	f := Default()
	// Assembled at run time: written out whole, a token-shaped string is
	// exactly what push protection and gitleaks exist to refuse.
	token := strings.Repeat("A", 26) + "." + strings.Repeat("B", 6) + "." + strings.Repeat("C", 30)
	for in, want := range map[string]string{
		"click here lol https://grabify.link/abc123":  "an IP grabber link",
		"free nitro at https://discord-gift.ru/claim": "a known Discord phishing link",
		"my token is " + token + " oops":              "a Discord bot token",
	} {
		if v := f.Check(in); v.Block != want {
			t.Errorf("Check(%q).Block = %q, want %q", in, v.Block, want)
		}
	}
}

func TestGluedMatchesTakeOneWord(t *testing.T) {
	f := Default()
	for range 100 {
		for _, in := range []string{
			"a lil poem about niggernation",
			"say hello to the spacenigger",
			"that whole faggotry thing again",
			"n1ggerdom is not a word you get to use",
			"gookish trannyism and some chinkland",
			"shitniggers",
		} {
			v := f.Check(in)
			if got, want := len(strings.Fields(v.Text)), len(strings.Fields(in)); !v.Rewrote || got != want {
				t.Fatalf("Check(%q) = %q: %d words, want %d", in, v.Text, got, want)
			}
		}
	}
}

func TestSpecsCompile(t *testing.T) {
	for _, r := range Rules {
		if len(r.Subs) == 0 {
			t.Errorf("%s has no subs", r.Name)
		}
		solo := false
		for _, s := range r.Subs {
			solo = solo || !strings.ContainsAny(s.One+s.Many, " ")
		}
		if !solo {
			t.Errorf("%s has no single-word sub for a glued match", r.Name)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("an unclosed group compiled")
		}
	}()
	spell("ab[cd")
}

func FuzzCheck(f *testing.F) {
	for _, s := range []string{
		"shut up you faggot", "n.i.g.g.e.r", "gobbledygook", "cute and funny",
		runes(0xFF4E, 0xFF49, 0xFF47, 0xFF47, 0xFF45, 0xFF52), "ﬀ", "a\nb",
	} {
		f.Add(s)
	}
	flt := Default()
	f.Fuzz(func(t *testing.T, in string) {
		v := flt.Check(in)
		if v.Block != "" {
			return
		}
		if utf8.ValidString(in) && !utf8.ValidString(v.Text) {
			t.Fatalf("Check(%q) produced invalid UTF-8 %q", in, v.Text)
		}
		if strings.Count(v.Text, "\n") > strings.Count(in, "\n") {
			t.Fatalf("Check(%q) added a line: %q", in, v.Text)
		}
		if again := flt.Check(v.Text); again.Rewrote {
			t.Fatalf("Check(%q) = %q, which still matches", in, v.Text)
		}
	})
}

// The cost every message pays. Most are clean ASCII.
var (
	clean = "honestly i think the new patch broke the inventory again, can someone check before the raid tonight"
	dirty = "honestly i think that faggot broke the inventory again, can someone check before the raid tonight"
	wide  = "ｈｏｎｅｓｔｌｙ i think the new patch broke the inventory again, café later? 🎉 " + runes(0x0441, 0x0430, 0x0442)
)

func BenchmarkCheck(b *testing.B) {
	f := Default()
	for name, s := range map[string]string{"clean": clean, "slur": dirty, "unicode": wide} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				f.Check(s)
			}
		})
	}
}

// BenchmarkPerRule is the plain shape, every rule's pattern run in turn with
// no prefilter, as the baseline the keys are measured against.
func BenchmarkPerRule(b *testing.B) {
	var each []*regexp.Regexp
	for _, r := range Rules {
		each = append(each, regexp.MustCompile(`(?i)`+spell(r.Spec)))
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, re := range each {
			re.FindAllStringIndex(clean, -1)
		}
	}
}
