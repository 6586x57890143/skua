package pp

import (
	"image"
	"image/png"
	"os"
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

// The purser, the slack tide's magpie, is finished art at both sizes: his
// portrait at a class portrait's 32 by 48 in at most 16 colours, and his
// figure, three times that, in at most 24.
func TestPurserArt(t *testing.T) {
	for _, a := range []struct {
		file          string
		w, h, colours int
	}{{"portraits/purser.png", 32, 48, 16}, {"figures/purser.png", 96, 144, 24}} {
		f, err := os.Open(a.file)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(a.file, err)
		}
		p, ok := img.(*image.Paletted)
		if !ok || p.Bounds().Dx() != a.w || p.Bounds().Dy() != a.h || len(p.Palette) > a.colours {
			t.Errorf("%s: %T %v, %d colours", a.file, img, img.Bounds().Size(), len(p.Palette))
		}
	}
}
