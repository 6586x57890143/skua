package pixel

import (
	"strings"
	"testing"
)

// Every printable character is drawn, inside its cell with a column clear
// to the next, and nothing else is.
func TestFace(t *testing.T) {
	for r := rune(0x21); r <= 0x7E; r++ {
		g, ok := face[r]
		if !ok {
			t.Errorf("%q isn't drawn", r)
			continue
		}
		if g.top < 0 || g.top+len(g.rows) > LineHeight {
			t.Errorf("%q runs out of its cell: rows %d to %d", r, g.top, g.top+len(g.rows))
		}
		inked := false
		for _, row := range g.rows {
			if len(row) != Advance-1 {
				t.Errorf("%q has a row %d wide: %q", r, len(row), row)
			}
			for _, ch := range row {
				if ch != '#' && ch != '.' {
					t.Errorf("%q has a %q", r, ch)
				}
				inked = inked || ch == '#'
			}
		}
		if !inked {
			t.Errorf("%q is blank", r)
		}
	}
	if len(face) != 0x7E-0x21+1 {
		t.Errorf("the face has %d glyphs", len(face))
	}
}

// Every face's glyphs sit inside its cell with a column clear to the next
// and its rows; Regular and Bold draw every
// printable character, Light and Serif at least the lowercase and the
// figures, and every face sets its baseline on row 10.
func TestFaces(t *testing.T) {
	for name, f := range map[string]*Face{"regular": Regular, "bold": Bold, "light": Light, "serif": Serif} {
		for r, g := range f.glyphs {
			if g.top < 0 || g.top+len(g.rows) > f.Height {
				t.Errorf("%s %q runs out of its cell: rows %d to %d", name, r, g.top, g.top+len(g.rows))
			}
			inked := false
			for _, row := range g.rows {
				if len(row) > f.Advance-1 {
					t.Errorf("%s %q has a row %d wide", name, r, len(row))
				}
				inked = inked || strings.Contains(row, "#")
				if strings.Trim(row, "#.") != "" {
					t.Errorf("%s %q has a %q", name, r, row)
				}
			}
			if !inked {
				t.Errorf("%s %q is blank", name, r)
			}
		}
		for _, r := range "abcdefghijklmnopqrstuvwxyz0123456789.,'" {
			if _, ok := f.glyphs[r]; !ok {
				t.Errorf("%s doesn't draw %q", name, r)
			}
		}
		// x sits on the baseline in every face.
		if g := f.glyphs['x']; g.top+len(g.rows) != 11 {
			t.Errorf("%s's x stands on row %d", name, g.top+len(g.rows)-1)
		}
	}
	if len(Bold.glyphs) != len(face) {
		t.Errorf("bold draws %d glyphs; regular %d", len(Bold.glyphs), len(face))
	}
}

// Faces wrap and measure at their own advance, and borrow what they don't
// draw.
func TestFaceMetrics(t *testing.T) {
	if Bold.Width("abc") != 24 || Light.Width("abc") != 18 {
		t.Errorf("bold abc is %d wide, light %d", Bold.Width("abc"), Light.Width("abc"))
	}
	if !Light.ink('A', 2, capTop) {
		t.Error("light doesn't borrow capitals from regular")
	}
	if got := Light.Wrap("one two three", 7*Light.Advance); strings.Join(got, "|") != "one two|three" {
		t.Errorf("light wraps to %q", got)
	}
}
