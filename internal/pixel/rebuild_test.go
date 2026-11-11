package pixel

import (
	"image"
	"image/color"
	"testing"
)

// TestRebuild is someone else's art on the grid: by majority per cell,
// clear where most of a cell is clear, in no more inks than asked.
func TestRebuild(t *testing.T) {
	red, blue := color.NRGBA{0xC0, 0x20, 0x20, 0xFF}, color.NRGBA{0x20, 0x40, 0xC0, 0xFF}
	src := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	for y := 10; y < 40; y++ {
		for x := range 40 {
			c := red
			if x >= 20 {
				c = blue
			}
			src.SetNRGBA(x, y, c)
		}
	}
	// One speck of the other colour in a red cell loses the vote.
	src.SetNRGBA(1, 21, blue)
	out := Rebuild(src, src.Bounds(), 4, 4, 2)
	if out.NRGBAAt(0, 0).A != 0 {
		t.Fatal("a clear cell was filled")
	}
	if out.NRGBAAt(0, 2) != red || out.NRGBAAt(3, 3) != blue {
		t.Fatalf("cells: %v %v", out.NRGBAAt(0, 2), out.NRGBAAt(3, 3))
	}
	inks := map[color.NRGBA]bool{}
	for y := range 4 {
		for x := range 4 {
			if c := out.NRGBAAt(x, y); c.A != 0 {
				inks[c] = true
			}
		}
	}
	if len(inks) > 2 {
		t.Fatalf("%d inks, asked for 2", len(inks))
	}
	if empty := Rebuild(image.NewNRGBA(image.Rect(0, 0, 8, 8)), image.Rect(0, 0, 8, 8), 4, 4, 3); empty.NRGBAAt(1, 1).A != 0 {
		t.Fatal("a clear picture came back filled")
	}
	if small := Rebuild(src, image.Rect(0, 0, 2, 2), 4, 4, 3); small.Bounds().Dx() != 4 {
		t.Fatal("a region smaller than the grid")
	}
	if got := Quantise([]color.NRGBA{red}, 5); len(got) != 1 || got[0] != red {
		t.Fatalf("more inks than colours: %v", got)
	}
}

// TestSolidFitted is a figure found in its render, specks ignored, and
// grown to a grid's shape about its centre.
func TestSolidFitted(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 200, 200))
	for y := 50; y < 150; y++ {
		for x := 80; x < 120; x++ {
			img.SetNRGBA(x, y, color.NRGBA{1, 2, 3, 0xFF})
		}
	}
	img.SetNRGBA(2, 2, color.NRGBA{1, 2, 3, 0xFF}) // a speck
	r := Solid(img)
	if r != image.Rect(80, 50, 120, 150) {
		t.Fatalf("solid %v", r)
	}
	// To the nearest pixel: 40 wide by 100 tall becomes 66 by 100.
	if f := Fitted(r, 64, 96); f.Dx() != 66 || f.Dy() != 100 || f.Min.Y != 50 || (f.Min.X+f.Max.X)/2 != 100 {
		t.Fatalf("fitted tall %v", f)
	}
	if f := Fitted(image.Rect(0, 0, 100, 10), 64, 96); f.Dx() != 100 || f.Dy() != 150 {
		t.Fatalf("fitted wide %v", f)
	}
}

// TestDrawCut is a face set on a true colour picture, with its shadow, and
// a line cut to fit.
func TestDrawCut(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 60, 20))
	ink, shade := color.NRGBA{0xFF, 0, 0, 0xFF}, color.NRGBA{0, 0, 0xFF, 0xFF}
	end := Regular.Draw(img, 1, 1, "Hi", ink, shade)
	if end != 1+2*Advance {
		t.Fatalf("pen at %d", end)
	}
	var inked, shaded bool
	for y := range 20 {
		for x := range 60 {
			switch img.NRGBAAt(x, y) {
			case ink:
				inked = true
			case shade:
				shaded = true
			}
		}
	}
	if !inked || !shaded {
		t.Fatal("no ink or no shadow")
	}
	Light.Draw(img, -5, -5, "off the edge", ink, nil)
	if got := Light.Cut("short", 100); got != "short" {
		t.Fatal(got)
	}
	if got := Light.Cut("a much longer name than fits", 40); Light.Width(got) > 40 || got[len(got)-3:] != "..." {
		t.Fatalf("cut %q", got)
	}
	if Light.Cut("wide", 1) != "" {
		t.Fatal("nothing fits, something came back")
	}
}
