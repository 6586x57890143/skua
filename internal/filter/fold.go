package filter

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// folded is text rewritten into the form the patterns are matched against,
// with the byte span in the original that every byte of it came from. A
// match in folded text maps back through start and end to exactly the
// characters the member typed, which is what gets replaced.
//
// ASCII text is never folded: the patterns are case-insensitive, so the
// original is its own folded form, and the common case pays nothing.
type folded struct {
	text       string
	start, end []int // nil when text is the original
}

func (f folded) span(a, b int) (int, int) {
	if f.start == nil {
		return a, b
	}
	return f.start[a], f.end[b-1]
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// fold undoes the evasions that live below the spelling. Per character:
//
//   - Format characters (zero-width spaces and joiners, soft hyphens, bidi
//     marks) and combining marks (accents left over from decomposition, and
//     Zalgo stacks) are dropped, so they neither separate letters nor count
//     as one.
//   - NFKD compatibility decomposition turns fullwidth, mathematical,
//     circled, superscript and accented letters into their base letters.
//   - lookalikes maps the letters NFKD leaves alone (Cyrillic, Greek,
//     Armenian, small capitals) onto the Latin letter they impersonate.
//   - Everything is lowercased.
func fold(s string) folded {
	if isASCII(s) {
		return folded{text: s}
	}
	var b strings.Builder
	b.Grow(len(s))
	start := make([]int, 0, len(s))
	end := make([]int, 0, len(s))
	emit := func(r rune, from, to int) {
		if drop(r) {
			return
		}
		if l, ok := lookalikes[r]; ok {
			r = l
		}
		r = unicode.ToLower(r)
		n, _ := b.WriteRune(r)
		for range n {
			start = append(start, from)
			end = append(end, to)
		}
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r < utf8.RuneSelf {
			emit(r, i, i+size)
			i += size
			continue
		}
		// Decomposition returns nil when the rune is already decomposed,
		// and never allocates either way.
		if d := norm.NFKD.PropertiesString(s[i:]).Decomposition(); d != nil {
			for _, c := range string(d) {
				emit(c, i, i+size)
			}
		} else {
			emit(r, i, i+size)
		}
		i += size
	}
	return folded{text: b.String(), start: start, end: end}
}

// drop reports runes that render as nothing, or as decoration on the letter
// before them.
func drop(r rune) bool {
	switch r {
	// Hangul fillers are letters by category and invisible on screen, which
	// is the whole reason they are used to split words.
	case 0x115F, 0x1160, 0x3164, 0xFFA0:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf)
}

// lookalikes maps letters from other scripts onto the Latin letter they are
// drawn like, for every letter the rules spell with. Hex keys on purpose: a
// table of confusables is unreadable written as the glyphs themselves.
//
// ponytail: curated by hand for the letters the rules use. Generating it from
// Unicode's confusables.txt is the upgrade if a new rule needs letters this
// does not cover, or evasions arrive from scripts it does not list.
var lookalikes = map[rune]rune{
	// Cyrillic
	0x0430: 'a', 0x0410: 'a', 0x0435: 'e', 0x0415: 'e', 0x043E: 'o', 0x041E: 'o',
	0x0440: 'p', 0x0420: 'p', 0x0441: 'c', 0x0421: 'c', 0x0443: 'y', 0x0423: 'y',
	0x0445: 'x', 0x0425: 'x', 0x0456: 'i', 0x0406: 'i', 0x0458: 'j', 0x0408: 'j',
	0x043A: 'k', 0x041A: 'k', 0x0442: 't', 0x0422: 't', 0x043F: 'n', 0x0433: 'r',
	0x0455: 's', 0x0405: 's', 0x04CF: 'i', 0x0501: 'd', 0x051B: 'q', 0x051D: 'w',
	0x04BB: 'h', 0x0432: 'b', 0x043C: 'm',
	// Greek
	0x03B1: 'a', 0x0391: 'a', 0x03BF: 'o', 0x039F: 'o', 0x03B9: 'i', 0x0399: 'i',
	0x03BA: 'k', 0x039A: 'k', 0x03B7: 'n', 0x039D: 'n', 0x03C4: 't', 0x03A4: 't',
	0x03B3: 'y', 0x03C5: 'u', 0x03C1: 'p', 0x03B5: 'e', 0x0395: 'e', 0x0396: 'z',
	0x03BD: 'v',
	// Armenian
	0x0578: 'n', 0x0585: 'o', 0x057D: 'u', 0x0581: 'g',
	// Latin letters that are not decompositions of their lookalike
	0x0131: 'i', 0x0251: 'a', 0x0261: 'g', 0x0142: 'l', 0x00F8: 'o', 0x0111: 'd',
	// Small capitals
	0x1D00: 'a', 0x0299: 'b', 0x1D04: 'c', 0x1D05: 'd', 0x1D07: 'e', 0xA730: 'f',
	0x0262: 'g', 0x029C: 'h', 0x026A: 'i', 0x1D0A: 'j', 0x1D0B: 'k', 0x029F: 'l',
	0x1D0D: 'm', 0x0274: 'n', 0x1D0F: 'o', 0x1D18: 'p', 0x0280: 'r', 0xA731: 's',
	0x1D1B: 't', 0x1D1C: 'u', 0x1D20: 'v', 0x1D21: 'w', 0x028F: 'y', 0x1D22: 'z',
}
