package pixel

import "testing"

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
