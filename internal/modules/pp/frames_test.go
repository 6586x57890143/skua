package pp

import (
	"strings"
	"testing"

	"github.com/6586x57890143/skua/internal/pixel"
)

// Every shape is a clean grid of inks, every trim names a shape, and each
// tier's frame comes in at its title's level. TestStatusLayout draws each
// tier and holds every slot off the portrait and on the parchment.
func TestFrames(t *testing.T) {
	for name, rows := range Shapes {
		for _, row := range rows {
			if len(row) != len(rows[0]) {
				t.Errorf("%s's rows differ in width", name)
			}
			for _, r := range strings.Trim(row, ".1234") {
				if _, ok := pixel.IconInks[byte(r)]; !ok && !strings.ContainsRune(".1234", r) {
					t.Errorf("%s draws %q, which isn't an ink", name, r)
				}
			}
		}
	}
	for i, f := range Frames {
		if len(f.Band) != 4 {
			t.Errorf("%s's band isn't a four ink ramp", f.Name)
		}
		for _, tr := range f.Trims {
			if _, ok := Shapes[tr.Shape]; !ok || tr.Slot < 0 || tr.Slot >= slots || len(tr.Metal) != 4 {
				t.Errorf("%s hangs %+v", f.Name, tr)
			}
		}
		if got := frameOf(titleFrom[i]); got.Name != f.Name {
			t.Errorf("level %d wears %s, not %s", titleFrom[i], got.Name, f.Name)
		}
	}
	if frameOf(titleFrom[1]-1).Name != Frames[0].Name {
		t.Error("a level under the first title past the start isn't in wood")
	}
}
