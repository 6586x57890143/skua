// Package filter screens member-written text before skua posts it, with no
// model in the loop: rules that rewrite a word in place (slurs, replaced by
// something daft so the sentence still stands) and blocks that cannot be
// rewritten (credentials and malicious links, refused whole).
//
// It is a library rather than a core.Module. Whisper screens what it posts
// with it, and automod's rung 1 is meant to be the same Filter run over
// member messages, so the two can never disagree about what a slur is.
//
// The usual shape of such a filter stops at ASCII and leaves the rest to a
// model reading the sentence. skua has no model behind it, so this goes
// further: text is folded out of Unicode lookalikes, fullwidth
// and accented forms, and invisible characters before it is matched; every
// letter may repeat; and consonants have leet forms as well as vowels.
//
// For speed, each rule carries a Key: letters any match of it must leave in
// the text's skeleton (see skeleton). One byte pass builds the skeleton, and
// a rule's regex runs only when its key is in it, so a clean message usually
// runs no regex at all.
package filter

import (
	"bytes"
	"cmp"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Sub is one replacement: the singular form and the plural, picked by how
// the matched word ended.
type Sub struct{ One, Many string }

// Rule is a word the filter rewrites.
type Rule struct {
	// Name is what an audit log or a moderator reads.
	Name string
	// Spec is the spelling, compiled by spell: each letter becomes its
	// class of substitutes and may repeat, any letter may be separated
	// from the next by up to three non-alphanumerics, "?" after a letter
	// makes it optional, and a [class] or (group) passes through verbatim.
	Spec string
	// Key, when set, is a substring the skeleton of any text this rule
	// matches must contain, and the rule's regex is skipped when it does
	// not. It must be built only from letters the skeleton keeps reliably:
	// consonants other than t and s (whose leet forms + and $ are also
	// separators), with k written as c, and never the same letter twice in
	// a row, since the skeleton collapses repeats. TestKeysNeverHideAMatch
	// holds every key to that. Empty means the regex always runs.
	Key string
	// CrossWords lets a match span whitespace between pieces longer than
	// one letter ("nig ger"). Without it only the spaced-out style crosses
	// whitespace ("g o o k"), because ordinary words spell short rules
	// across their boundary: "go ok" is "gook". Only for rules long enough
	// that no innocent pair of words spells them.
	CrossWords bool
	// NotIf cancels one match when it matches the matched word and the
	// notIfWindow bytes after it, which is where the phrase that makes it
	// innocent sits ("chink in", "chink of"). Per match, never per text: a
	// text-wide veto would let any message that also says the phrase post
	// every other match in it untouched.
	NotIf *regexp.Regexp
	// Subs is drawn from at random per match, and must hold at least one
	// single-word Sub for matches glued into a longer word.
	Subs []Sub
}

// Block is a pattern that cannot be rewritten into anything publishable.
type Block struct {
	Reason  string
	Pattern *regexp.Regexp
	// Gate, when set, is a cheap test every text the pattern matches
	// passes. Most messages fail it, so the pattern never runs on them.
	Gate func(string) bool
}

// Verdict is what Check decided about one text.
type Verdict struct {
	// Text is the input with every rewrite applied; the input itself when
	// nothing matched.
	Text    string
	Rewrote bool
	// Block is the reason the text must not be posted at all, or "".
	Block string
}

// Filter is safe for concurrent use.
type Filter struct {
	rules  []Rule
	res    []*regexp.Regexp // per rule
	keys   [][]byte         // per rule, nil for none
	blocks []Block
	pick   func(n int) int
}

// New compiles rules and blocks. It panics on a spec that does not compile,
// which is to say at startup.
//
// One regex per rule rather than one alternation of all of them: Go's
// regexp runs a small program on its fast backtracker and a large one on the
// general NFA, so nine small passes measured faster than one combined pass
// (12.6us against 20.3us on a clean message, before keys).
func New(rules []Rule, blocks []Block) *Filter {
	f := &Filter{rules: rules, blocks: blocks, pick: rand.IntN}
	for _, r := range rules {
		f.res = append(f.res, regexp.MustCompile(`(?i)`+spell(r.Spec)))
		var key []byte
		if r.Key != "" {
			key = []byte(r.Key)
		}
		f.keys = append(f.keys, key)
	}
	return f
}

// Default is the built-in rules and blocks.
func Default() *Filter { return New(Rules, Blocks) }

// sep is what may sit between two letters of a word: up to three characters,
// enough for "n . i . g" (space, dot, space), and never a letter or a digit,
// which is what keeps "snigger" and "niggardly" out.
const sep = `[^\p{L}\p{N}]{0,3}`

// classes is what people type for a letter. Digits and symbols here are
// also legal separators; the regex settles which they are per match.
var classes = map[byte]string{
	'a': `[a4@*]`,
	'e': `[e3*]`,
	'i': `[i1!|l*]`,
	'o': `[o0*]`,
	'u': `[uv*]`,
	'g': `[g69]`,
	't': `[t7+]`,
	'f': `(?:f|ph)`,
}

// spell compiles a Spec into a pattern. Groups in the spec become
// non-capturing: nothing reads them, and captures cost.
func spell(spec string) string {
	var atoms []string
	for i := 0; i < len(spec); i++ {
		var atom string
		switch spec[i] {
		case '[', '(':
			closer := byte(']')
			if spec[i] == '(' {
				closer = ')'
			}
			j := strings.IndexByte(spec[i:], closer)
			if j < 0 {
				panic("filter: unclosed group in " + spec)
			}
			atom, i = spec[i:i+j+1], i+j
			if strings.HasPrefix(atom, "(") && !strings.HasPrefix(atom, "(?") {
				atom = "(?:" + atom[1:]
			}
		default:
			atom = classes[spec[i]]
			if atom == "" {
				atom = regexp.QuoteMeta(spec[i : i+1])
			}
			// Any letter may be held down: "niiigger" is "nigger".
			atom += "+"
		}
		// A trailing ? makes the atom optional; a + is already implied.
		for i+1 < len(spec) && (spec[i+1] == '?' || spec[i+1] == '+') {
			if spec[i+1] == '?' {
				atom = "(?:" + atom + ")?"
			}
			i++
		}
		atoms = append(atoms, atom)
	}
	return strings.Join(atoms, sep)
}

// Check screens s. Blocks are checked first: a text that must not be posted
// is not worth rewriting.
func (f *Filter) Check(s string) Verdict {
	for _, b := range f.blocks {
		if b.Gate != nil && !b.Gate(s) {
			continue
		}
		if b.Pattern.MatchString(s) {
			return Verdict{Text: s, Block: b.Reason}
		}
	}
	// A replacement next to the letters around it could, in principle,
	// spell another match, so rewrite until nothing matches. Two passes is
	// the most any real text needs; the cap keeps a pathological input from
	// spinning.
	out, rewrote := s, false
	for range 3 {
		next, hit := f.rewrite(out)
		if !hit {
			break
		}
		out, rewrote = next, true
	}
	return Verdict{Text: out, Rewrote: rewrote}
}

// notIfWindow is how far past a matched word a Rule's NotIf may look.
const notIfWindow = 16

// hit is one match to replace, in folded coordinates.
type hit struct{ a, z, ws, we, rule int }

// rewrite replaces every match in s once.
func (f *Filter) rewrite(s string) (string, bool) {
	fd := fold(s)
	var buf [512]byte
	sk := skeleton(fd.text, buf[:0])
	var hits []hit
	for i, r := range f.rules {
		if f.keys[i] != nil && !bytes.Contains(sk, f.keys[i]) {
			continue
		}
		for _, m := range f.res[i].FindAllStringIndex(fd.text, -1) {
			a, z := m[0], trimRight(fd.text, m[0], m[1])
			if a == z || !r.CrossWords && spansWords(fd.text[a:z]) {
				continue
			}
			ws, we := wordBounds(fd.text, a, z)
			if innocent.MatchString(fd.text[ws:we]) {
				continue
			}
			if r.NotIf != nil && r.NotIf.MatchString(fd.text[ws:min(len(fd.text), we+notIfWindow)]) {
				continue
			}
			hits = append(hits, hit{a, z, ws, we, i})
		}
	}
	if hits == nil {
		return s, false
	}
	slices.SortFunc(hits, func(x, y hit) int { return cmp.Compare(x.a, y.a) })
	var b strings.Builder
	last := 0
	for _, h := range hits {
		oa, oz := fd.span(h.a, h.z)
		if oa < last {
			continue // overlaps an earlier rule's match, or a split ligature
		}
		sub := f.choose(f.rules[h.rule].Subs, h.ws < h.a || h.we > h.z)
		word := sub.One
		if h.we == h.z && plural(fd.text[h.z-1]) {
			word = sub.Many
		}
		b.WriteString(s[last:oa])
		b.WriteString(word)
		last = oz
	}
	b.WriteString(s[last:])
	return b.String(), true
}

// skeleton reduces text to what the rule keys are written in, appending to
// dst: its consonants in order, lowercase, with leet digits read as their
// letters, k read as c and ph as f, and a letter repeated with nothing but
// vowels or separators in between collapsed to one. "N1gg3r",
// "n . i . g . g . e . r", "nig@g6er" and "ni99er" all come out "ngr". It
// runs on folded text, so lookalikes are already Latin.
//
// Vowels and every symbol are dropped outright, even the ones a rule reads
// as a letter: those are all vowels except + and $, and keys avoid t and s
// for exactly that reason. Collapsing after dropping is what makes the
// skeleton the same whichever way a symbol was meant.
func skeleton(s string, dst []byte) []byte {
	var prev byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		case c >= 0x80:
			dst = append(dst, c) // another script's letter: kept, never a key
			prev = 0
			continue
		}
		switch c {
		case '9', '6':
			c = 'g'
		case '7':
			c = 't'
		case '5':
			c = 's'
		case 'k':
			c = 'c'
		case 'p':
			if i+1 < len(s) && (s[i+1] == 'h' || s[i+1] == 'H') {
				c = 'f'
				i++
			}
		case 'a', 'e', 'i', 'o', 'u', 'v', 'l':
			continue // vowels, and the letters the rules read as vowels
		}
		if c < 'b' || c > 'z' || c == prev {
			continue // digits, symbols, separators and repeats
		}
		prev = c
		dst = append(dst, c)
	}
	return dst
}

// trimRight drops separators a match ended on. A rule ending in an optional
// plural can match the separator before a plural that then matched nothing,
// which would swallow the space after the word and widen the word the
// innocent veto is tested against.
func trimRight(s string, a, z int) int {
	for z > a {
		r, size := utf8.DecodeLastRuneInString(s[a:z])
		if word(r) {
			break
		}
		z -= size
	}
	return z
}

// spansWords reports a match that crosses whitespace with more than one
// letter on some side of it, which is two words rather than one word spaced
// out.
func spansWords(m string) bool {
	pieces := strings.FieldsFunc(m, unicode.IsSpace)
	if len(pieces) < 2 {
		return false
	}
	for _, p := range pieces {
		letters := 0
		for _, r := range p {
			if word(r) {
				letters++
			}
		}
		if letters > 1 {
			return true
		}
	}
	return false
}

func plural(c byte) bool {
	switch c {
	case 's', 'S', 'z', 'Z', '5', '$':
		return true
	}
	return false
}

// choose draws a Sub, from the single-word ones when the match is glued into
// a longer word, so "spacenigger" comes back "spaceninja" and not
// "spacenight owl".
func (f *Filter) choose(subs []Sub, glued bool) Sub {
	if glued {
		var solo []Sub
		for _, s := range subs {
			if !strings.Contains(s.One, " ") && !strings.Contains(s.Many, " ") {
				solo = append(solo, s)
			}
		}
		if len(solo) > 0 {
			subs = solo
		}
	}
	return subs[f.pick(len(subs))]
}

// wordBounds widens a match to the whole word around it, by the same
// letters-and-digits rule the separator uses.
func wordBounds(s string, a, z int) (int, int) {
	for a > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:a])
		if !word(r) {
			break
		}
		a -= size
	}
	for z < len(s) {
		r, size := utf8.DecodeRuneInString(s[z:])
		if !word(r) {
			break
		}
		z += size
	}
	return a, z
}

func word(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
