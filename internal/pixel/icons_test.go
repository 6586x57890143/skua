package pixel

import (
	"image"
	"testing"
)

// Every icon is on its grid: 16 by 16, only known inks, outlined, and
// centred to the nearest cell, so icons line up wherever they are set.
func TestIconsSitOnTheirGrid(t *testing.T) {
	if len(Icons) == 0 {
		t.Fatal("no icons")
	}
	for name, g := range Icons {
		if len(g) != IconCells {
			t.Errorf("%s: %d rows", name, len(g))
			continue
		}
		x0, x1, y0, y1 := IconCells, -1, IconCells, -1
		outlined := false
		for y, row := range g {
			if len(row) != IconCells {
				t.Errorf("%s: row %d is %d wide", name, y, len(row))
				continue
			}
			for x := range IconCells {
				r := row[x]
				if r == '.' {
					continue
				}
				if _, ok := IconInks[r]; !ok {
					t.Errorf("%s: unknown ink %q at %d,%d", name, r, x, y)
				}
				outlined = outlined || r == 'k'
				x0, x1, y0, y1 = min(x0, x), max(x1, x), min(y0, y), max(y1, y)
			}
		}
		if !outlined {
			t.Errorf("%s has no outline", name)
		}
		left, right, top, bottom := x0, IconCells-1-x1, y0, IconCells-1-y1
		if d := left - right; d < -1 || d > 1 {
			t.Errorf("%s sits off centre across: %d left, %d right", name, left, right)
		}
		if d := top - bottom; d < -1 || d > 1 {
			t.Errorf("%s sits off centre down: %d above, %d below", name, top, bottom)
		}
	}
}

func TestIconDraws(t *testing.T) {
	c := New(20, 20)
	c.Icon("pot", image.Pt(2, 2))
	c.Icon("nothing", image.Pt(0, 0))
	if got := c.ColorIndexAt(2+7, 2+1); got != iconInk['k'] {
		t.Errorf("the pot's rim is ink %d; want the outline", got)
	}
	if len(Palette) > 256 {
		t.Fatalf("%d colours", len(Palette))
	}
}
