package pp

import (
	"image"
	"testing"
)

// Every class shows its own bird, at the size the status screen frames.
func TestEveryClassHasItsPortrait(t *testing.T) {
	seen := map[image.Image]string{}
	for _, c := range Classes {
		p := Portrait(c)
		if p == nil {
			t.Errorf("%s has no portrait: draw portraits/%s.png", c.Name(), c.Bird)
			continue
		}
		if p.Bounds().Dx() != 32 || p.Bounds().Dy() != 48 {
			t.Errorf("%s's portrait is %v; want 32 by 48", c.Name(), p.Bounds().Size())
		}
		if other, ok := seen[p]; ok {
			t.Errorf("%s and %s share a portrait", c.Name(), other)
		}
		seen[p] = c.Name()
	}
	if Portrait(Class{Bird: "dodo"}) != nil {
		t.Error("a bird with no portrait got one")
	}
}
