package pixel

import "strings"

// The screens are typeset in four faces of one family, each with a job:
//
//   - Regular, the hand-drawn slab serif, for values and body text.
//   - Bold, the same letters with their strokes doubled across, for names,
//     headings and tabs.
//   - Light, a hairline sans with no shadow, set proportionally, for
//     labels and the quieter words beside a value.
//   - Serif, a book hand set proportionally as a book is, with a taller
//     x-height, a two-storey g and old-style figures, for the story an
//     item tells.
//
// Regular and Bold are monospaced, so figures line up in a column. Light
// and Serif give each letter its own width and one clear column after it.
//
// Every face sets its baseline on the regular's, row 10 of the cell, so
// faces mix on one line. Light and Serif draw lowercase, figures and the
// punctuation prose needs, and borrow the rest from Regular.

// Face is one face of the family: its glyphs, how far apart they sit,
// how tall a line of it is and whether it casts the khaki shadow.
type Face struct {
	glyphs   map[rune]glyph
	Advance  int // a cell's width, the clear column included
	Height   int // a line's height, shadow included
	shadow   bool
	fallback *Face
	// proportional faces move the pen by each glyph's own width and set a
	// space space wide.
	proportional bool
	space        int
}

var (
	Regular = &Face{glyphs: face, Advance: Advance, Height: LineHeight, shadow: true}
	Bold    = &Face{glyphs: bolden(face), Advance: Advance + 1, Height: LineHeight, shadow: true}
	Light   = &Face{glyphs: lightGlyphs, Advance: Advance - 1, Height: LineHeight, fallback: Regular, proportional: true, space: 4}
	Serif   = &Face{glyphs: serifGlyphs, Advance: Advance, Height: LineHeight + 2, shadow: true, fallback: Regular, proportional: true, space: 4}
)

// glyph is r's glyph in f, or its fallback's.
func (f *Face) glyph(r rune) (glyph, bool) {
	if g, ok := f.glyphs[r]; ok {
		return g, true
	}
	if f.fallback != nil {
		return f.fallback.glyph(r)
	}
	return glyph{}, false
}

// ink reports whether f's glyph for r has ink at gx, gy of its cell.
func (f *Face) ink(r rune, gx, gy int) bool {
	g, ok := f.glyph(r)
	if !ok || gy < g.top || gy >= g.top+len(g.rows) {
		return false
	}
	row := g.rows[gy-g.top]
	return gx >= 0 && gx < len(row) && row[gx] == '#'
}

// span is the column r's ink starts at in its cell and how far the pen
// moves past r.
func (f *Face) span(r rune) (left, adv int) {
	if !f.proportional {
		return 0, f.Advance
	}
	g, _ := f.glyph(r)
	lo, hi := f.Advance, -1
	for _, row := range g.rows {
		for x := range len(row) {
			if row[x] == '#' {
				lo, hi = min(lo, x), max(hi, x)
			}
		}
	}
	if hi < 0 {
		return 0, f.space
	}
	return lo, hi - lo + 2
}

// width is how wide s is set in f, each character tight columns closer.
func (f *Face) width(s string, tight int) int {
	w := 0
	for _, r := range Plain(s) {
		_, adv := f.span(r)
		w += adv - tight
	}
	return w
}

// Width is how wide s is set in f.
func (f *Face) Width(s string) int { return f.width(s, 0) }

// Wrap breaks s into lines at most width wide set in f, between words; a
// word longer than a line is cut.
func (f *Face) Wrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(Plain(s)) {
		switch {
		case line == "":
			line = w
		case f.Width(line+" "+w) <= width:
			line += " " + w
			continue
		default:
			lines = append(lines, line)
			line = w
		}
		for f.Width(line) > width && len(line) > 1 {
			k := len(line) - 1
			for k > 1 && f.Width(line[:k]) > width {
				k--
			}
			lines = append(lines, line[:k])
			line = line[k:]
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// bolden is every glyph with its strokes doubled one column right, the
// way old consoles made a bold, a column wider; boldFixes redraws the
// letters whose counters that would close.
func bolden(regular map[rune]glyph) map[rune]glyph {
	out := make(map[rune]glyph, len(regular))
	for r, g := range regular {
		rows := make([]string, len(g.rows))
		for y, row := range g.rows {
			b := []byte(strings.Repeat(".", len(row)+1))
			for x := range len(row) {
				if row[x] == '#' {
					b[x], b[x+1] = '#', '#'
				}
			}
			rows[y] = string(b)
		}
		out[r] = glyph{g.top, rows}
	}
	for r, g := range boldFixes {
		out[r] = g
	}
	return out
}

// boldFixes are the bold letters drawn by hand: those with stems a column
// apart, where doubling would fill the counters in.
var boldFixes = map[rune]glyph{
	'm': {xTop, []string{"######.", "##.##.#", "##.##.#", "##.##.#", "##.##.#", "##.##.#"}},
	'w': {xTop, []string{"##...##", "##...##", "##.#.##", "##.#.##", "##.#.##", ".##.##."}},
	'M': {capTop, []string{"##...##", "###.###", "##.#.##", "##.#.##", "##...##", "##...##", "##...##", "##...##", "##...##"}},
	'W': {capTop, []string{"##...##", "##...##", "##...##", "##...##", "##.#.##", "##.#.##", "##.#.##", "###.###", ".#...#."}},
	'%': {capTop, []string{"##...##", "##..##.", "...##..", "...##..", "..##...", "..##...", ".##..##", "##...##"}},
	'#': {capTop + 1, []string{".##.##.", ".##.##.", "#######", ".##.##.", "#######", ".##.##.", ".##.##."}},
	'@': {capTop, []string{".#####.", "##...##", "##..###", "##.#.##", "##.#.##", "##..###", "##.....", "##...##", ".#####."}},
	'&': {capTop, []string{".###...", "##.##..", "##.##..", ".###...", ".###.##", "##.###.", "##..##.", "##.##.#", ".###..#"}},
	'"': {capTop, []string{".##.##.", ".##.##.", "......."}},
	'~': {capTop + 4, []string{".###.##", "##.###."}},
}

// lightGlyphs is the hairline sans: one pixel strokes, no serifs, five
// columns wide.
var lightGlyphs = map[rune]glyph{
	'a': {xTop, []string{".###.", "....#", ".####", "#...#", "#...#", ".####"}},
	'b': {capTop, []string{"#....", "#....", "#....", "#.##.", "##..#", "#...#", "#...#", "#...#", "####."}},
	'c': {xTop, []string{".###.", "#...#", "#....", "#....", "#...#", ".###."}},
	'd': {capTop, []string{"....#", "....#", "....#", ".####", "#...#", "#...#", "#...#", "#...#", ".####"}},
	'e': {xTop, []string{".###.", "#...#", "#####", "#....", "#...#", ".###."}},
	'f': {capTop, []string{"..##.", ".#...", ".#...", "####.", ".#...", ".#...", ".#...", ".#...", ".#..."}},
	'g': {xTop, []string{".####", "#...#", "#...#", "#...#", ".####", "....#", "....#", ".###."}},
	'h': {capTop, []string{"#....", "#....", "#....", "#.##.", "##..#", "#...#", "#...#", "#...#", "#...#"}},
	'i': {capTop + 1, []string{"..#", "...", "..#", "..#", "..#", "..#", "..#", "..#"}},
	'j': {capTop + 1, []string{"...#.", ".....", "...#.", "...#.", "...#.", "...#.", "...#.", "...#.", "#..#.", ".##.."}},
	'k': {capTop, []string{"#....", "#....", "#....", "#..#.", "#.#..", "##...", "#.#..", "#..#.", "#...#"}},
	'l': {capTop, []string{".#...", ".#...", ".#...", ".#...", ".#...", ".#...", ".#...", ".#...", "..##."}},
	'm': {xTop, []string{"##.#.", "#.#.#", "#.#.#", "#.#.#", "#.#.#", "#.#.#"}},
	'n': {xTop, []string{"#.##.", "##..#", "#...#", "#...#", "#...#", "#...#"}},
	'o': {xTop, []string{".###.", "#...#", "#...#", "#...#", "#...#", ".###."}},
	'p': {xTop, []string{"#.##.", "##..#", "#...#", "#...#", "##..#", "#.##.", "#....", "#...."}},
	'q': {xTop, []string{".##.#", "#..##", "#...#", "#...#", "#..##", ".##.#", "....#", "....#"}},
	'r': {xTop, []string{"#.##.", "##..#", "#....", "#....", "#....", "#...."}},
	's': {xTop, []string{".####", "#....", ".###.", "....#", "....#", "####."}},
	't': {capTop + 1, []string{".#...", ".#...", "####.", ".#...", ".#...", ".#...", ".#...", "..##."}},
	'u': {xTop, []string{"#...#", "#...#", "#...#", "#...#", "#...#", ".####"}},
	'v': {xTop, []string{"#...#", "#...#", "#...#", ".#.#.", ".#.#.", "..#.."}},
	'w': {xTop, []string{"#...#", "#...#", "#.#.#", "#.#.#", "#.#.#", ".#.#."}},
	'x': {xTop, []string{"#...#", ".#.#.", "..#..", "..#..", ".#.#.", "#...#"}},
	'y': {xTop, []string{"#...#", "#...#", "#...#", "#...#", ".####", "....#", "....#", ".###."}},
	'z': {xTop, []string{"#####", "...#.", "..#..", ".#...", "#....", "#####"}},

	'0': {capTop, []string{".###.", "#...#", "#...#", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."}},
	'1': {capTop, []string{"..#..", ".##..", "..#..", "..#..", "..#..", "..#..", "..#..", "..#..", "..#.."}},
	'2': {capTop, []string{".###.", "#...#", "....#", "....#", "...#.", "..#..", ".#...", "#....", "#####"}},
	'3': {capTop, []string{".###.", "#...#", "....#", "....#", "..##.", "....#", "....#", "#...#", ".###."}},
	'4': {capTop, []string{"...#.", "..##.", ".#.#.", "#..#.", "#..#.", "#####", "...#.", "...#.", "...#."}},
	'5': {capTop, []string{"#####", "#....", "#....", "####.", "....#", "....#", "....#", "#...#", ".###."}},
	'6': {capTop, []string{"..##.", ".#...", "#....", "####.", "#...#", "#...#", "#...#", "#...#", ".###."}},
	'7': {capTop, []string{"#####", "....#", "....#", "...#.", "...#.", "..#..", "..#..", "..#..", "..#.."}},
	'8': {capTop, []string{".###.", "#...#", "#...#", "#...#", ".###.", "#...#", "#...#", "#...#", ".###."}},
	'9': {capTop, []string{".###.", "#...#", "#...#", "#...#", "#...#", ".####", "....#", "...#.", ".##.."}},

	'.':  {capTop + 8, []string{"..#"}},
	',':  {capTop + 8, []string{"..#", ".#."}},
	':':  {xTop + 1, []string{"..#", "...", "...", "...", "..#"}},
	'\'': {capTop, []string{"..#", "..#"}},
	'-':  {capTop + 5, []string{"####"}},
	'+':  {capTop + 3, []string{"..#..", "..#..", "#####", "..#..", "..#.."}},
	'/':  {capTop, []string{"....#", "....#", "...#.", "...#.", "..#..", ".#...", ".#...", "#....", "#...."}},
	'%':  {capTop + 1, []string{"##..#", "##.#.", "...#.", "..#..", ".#...", ".#.##", "#..##"}},
	'(':  {capTop, []string{"..#", ".#.", "#..", "#..", "#..", "#..", "#..", ".#.", "..#"}},
	')':  {capTop, []string{"#..", ".#.", "..#", "..#", "..#", "..#", "..#", ".#.", "#.."}},
	'?':  {capTop, []string{".###.", "#...#", "....#", "...#.", "..#..", "..#..", ".....", ".....", "..#.."}},
	'!':  {capTop, []string{"..#", "..#", "..#", "..#", "..#", "..#", "..#", "...", "..#"}},
}

// The serif's x-height starts a row above the regular's, so its
// lowercase stands taller and its descenders reach a row further.
const serifX = xTop - 1

// serifGlyphs is the book hand: the regular's slab serifs on a taller
// x-height, a two-storey g, and figures that rise and fall as old-style
// figures do.
var serifGlyphs = map[rune]glyph{
	'a': {serifX, []string{".###..", "#...#.", "....#.", ".####.", "#...#.", "#..##.", ".##.##"}},
	'b': {capTop - 1, []string{"##....", ".#....", ".#....", ".#.##.", ".##..#", ".#...#", ".#...#", ".#...#", ".##..#", "#.###."}},
	'c': {serifX, []string{".####.", "#...##", "#.....", "#.....", "#.....", "#....#", ".####."}},
	'd': {capTop - 1, []string{"...##.", "....#.", "....#.", ".##.#.", "#..##.", "#...#.", "#...#.", "#...#.", "#..##.", ".##.##"}},
	'e': {serifX, []string{".###..", "#...#.", "#...#.", "#####.", "#.....", "#...#.", ".###.."}},
	'f': {capTop - 1, []string{"..###.", ".#..##", ".#....", ".#....", "####..", ".#....", ".#....", ".#....", ".#....", "###..."}},
	'g': {serifX, []string{".###.#", "#...#.", "#...#.", "#...#.", ".###..", "#.....", ".####.", "#....#", "#....#", ".####."}},
	'h': {capTop - 1, []string{"##....", ".#....", ".#....", ".#.##.", ".##..#", ".#...#", ".#...#", ".#...#", ".#...#", "##..##"}},
	'i': {capTop, []string{"..#...", "......", ".##...", "..#...", "..#...", "..#...", "..#...", "..#...", ".###.."}},
	'j': {capTop, []string{"....#.", "......", "...##.", "....#.", "....#.", "....#.", "....#.", "....#.", "....#.", "#...#.", ".###.."}},
	'k': {capTop - 1, []string{"##....", ".#....", ".#....", ".#..##", ".#..#.", ".#.#..", ".##...", ".#.#..", ".#..#.", "##..##"}},
	'l': {capTop - 1, []string{".##...", "..#...", "..#...", "..#...", "..#...", "..#...", "..#...", "..#...", "..#...", ".###.."}},
	'm': {serifX, []string{"##.#..", ".##.#.", ".#.#.#", ".#.#.#", ".#.#.#", ".#.#.#", "##.#.#"}},
	'n': {serifX, []string{"##.##.", ".##..#", ".#...#", ".#...#", ".#...#", ".#...#", "##..##"}},
	'o': {serifX, []string{".###..", "#...#.", "#...#.", "#...#.", "#...#.", "#...#.", ".###.."}},
	'p': {serifX, []string{"##.##.", ".##..#", ".#...#", ".#...#", ".#...#", ".##..#", ".#.##.", ".#....", ".#....", "###..."}},
	'q': {serifX, []string{".##.##", "#..##.", "#...#.", "#...#.", "#...#.", "#..##.", ".##.#.", "....#.", "....#.", "...###"}},
	'r': {serifX, []string{"##.##.", ".##..#", ".#....", ".#....", ".#....", ".#....", "###..."}},
	's': {serifX, []string{".####.", "#....#", "#.....", ".####.", ".....#", "#....#", ".####."}},
	't': {capTop, []string{".#....", ".#....", "####..", ".#....", ".#....", ".#....", ".#....", ".#..#.", "..##.."}},
	'u': {serifX, []string{"##..##", ".#..#.", ".#..#.", ".#..#.", ".#..#.", ".#.##.", "..#.##"}},
	'v': {serifX, []string{"##..##", "#....#", ".#..#.", ".#..#.", ".#..#.", "..##..", "..##.."}},
	'w': {serifX, []string{"##..##", "#....#", "#....#", "#.##.#", "#.##.#", "#.##.#", ".#..#."}},
	'x': {serifX, []string{"##..##", ".#..#.", "..##..", "..##..", "..##..", ".#..#.", "##..##"}},
	'y': {serifX, []string{"##..##", ".#..#.", ".#..#.", ".#..#.", ".#..#.", "..###.", "....#.", "....#.", "#...#.", ".###.."}},
	'z': {serifX, []string{"######", "#...#.", "...#..", "..#...", ".#....", "#....#", "######"}},

	'0': {serifX, []string{".###..", "#...#.", "#...#.", "#...#.", "#...#.", "#...#.", ".###.."}},
	'1': {serifX, []string{"..#...", ".##...", "..#...", "..#...", "..#...", "..#...", ".###.."}},
	'2': {serifX, []string{".###..", "#...#.", "....#.", "...#..", "..#...", ".#....", "#####."}},
	'3': {serifX, []string{".###..", "#...#.", "....#.", "..##..", "....#.", "....#.", "....#.", "#...#.", ".###.."}},
	'4': {serifX, []string{"...#..", "..##..", ".#.#..", "#..#..", "#####.", "...#..", "...#..", "...#..", "..###."}},
	'5': {serifX, []string{"#####.", "#.....", "####..", "....#.", "....#.", "....#.", "....#.", "#...#.", ".###.."}},
	'6': {capTop - 1, []string{"...##.", "..#...", ".#....", "#.....", "####..", "#...#.", "#...#.", "#...#.", "#...#.", ".###.."}},
	'7': {serifX, []string{"#####.", "#...#.", "....#.", "...#..", "...#..", "..#...", "..#...", "..#...", "..#..."}},
	'8': {capTop - 1, []string{".###..", "#...#.", "#...#.", "#...#.", ".###..", "#...#.", "#...#.", "#...#.", "#...#.", ".###.."}},
	'9': {serifX, []string{".###..", "#...#.", "#...#.", "#...#.", ".####.", "....#.", "....#.", "...#..", ".##..."}},

	'.':  {capTop + 7, []string{".##...", ".##..."}},
	',':  {capTop + 7, []string{".##...", ".##...", "..#...", ".#...."}},
	':':  {serifX + 1, []string{".##...", ".##...", "......", "......", ".##...", ".##..."}},
	';':  {serifX + 1, []string{".##...", ".##...", "......", "......", ".##...", ".##...", "..#...", ".#...."}},
	'\'': {capTop - 1, []string{"..#...", "..#...", ".#...."}},
	'-':  {capTop + 4, []string{"#####."}},
	'!':  {capTop - 1, []string{"..#...", "..#...", "..#...", "..#...", "..#...", "..#...", "..#...", "..#...", "......", "..#..."}},
	'?':  {capTop - 1, []string{".###..", "#...#.", "....#.", "....#.", "...#..", "..#...", "..#...", "......", "......", "..#..."}},
}
