package pixel

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestPalette(t *testing.T) {
	if len(Palette) > 256 {
		t.Fatalf("%d colours don't fit an indexed PNG", len(Palette))
	}
	seen := map[uint8]bool{}
	for _, i := range append([]uint8{Ink, Dim, Shadow, Bone, Gold, Green, Red, Ember}, Sky[:]...) {
		if seen[i] {
			t.Errorf("ink %d is used twice", i)
		}
		seen[i] = true
	}
	r0, _, _, _ := Palette[Sky[0]].RGBA()
	r5, _, _, _ := Palette[Sky[5]].RGBA()
	if r0 <= r5 {
		t.Error("the sky doesn't darken toward its foot")
	}
}

func TestWindow(t *testing.T) {
	c := New(40, 30)
	c.Window(image.Rect(2, 2, 38, 28))
	for _, want := range []struct {
		x, y int
		ink  uint8
		what string
	}{
		{2, 2, Sky[5], "a cut corner"},
		{3, 2, Shadow, "the outline"},
		{3, 3, Bone, "the border"},
		{5, 4, Sky[0], "the sky's top band"},
		{5, 25, Sky[5], "the sky's foot band"},
		{0, 0, Sky[5], "outside"},
	} {
		if got := c.ColorIndexAt(want.x, want.y); got != want.ink {
			t.Errorf("%s at %d,%d is ink %d; want %d", want.what, want.x, want.y, got, want.ink)
		}
	}
}

func TestText(t *testing.T) {
	c := New(80, 20)
	if end := c.Text(1, 1, "lv 7", Ink, 0); end != 1+4*Advance {
		t.Errorf("4 characters end at %d", end)
	}
	if end := c.Text(0, 0, "a much longer line", Ink, 8); end != 8*Advance {
		t.Errorf("a cut line ends at %d; want %d", end, 8*Advance)
	}
	var ink, shadow int
	for _, p := range c.Pix {
		switch p {
		case Ink:
			ink++
		case Shadow:
			shadow++
		}
	}
	if ink == 0 || shadow == 0 {
		t.Errorf("text drew %d ink and %d shadow", ink, shadow)
	}
	if Plain("héron\tthief  ok") != "h ron thief ok" {
		t.Errorf("Plain: %q", Plain("héron\tthief  ok"))
	}
}

func TestBar(t *testing.T) {
	c := New(20, 5)
	c.Bar(image.Rect(0, 0, 12, 3), 5, 10, Gold)
	// The inside is 10 wide: half is 5 gold cells.
	for x := 1; x < 11; x++ {
		want := Sky[5]
		if x < 6 {
			want = Gold
		}
		if got := c.ColorIndexAt(x, 1); got != want {
			t.Errorf("cell %d is %d; want %d", x, got, want)
		}
	}
	c.Bar(image.Rect(0, 0, 12, 3), 99, 10, Gold)
	if c.ColorIndexAt(10, 1) != Gold {
		t.Error("an overfull bar isn't full")
	}
	c.Bar(image.Rect(0, 0, 12, 3), 1, 0, Gold)
	if c.ColorIndexAt(1, 1) != Sky[5] {
		t.Error("a bar of nothing filled")
	}
}

func TestBirdAndCursor(t *testing.T) {
	c := New(140, 140)
	c.Bird(image.Pt(4, 4))
	c.Cursor(20, 10)
	var drawn int
	for _, p := range c.Pix {
		if p != Sky[5] {
			drawn++
		}
	}
	if drawn < 2000 {
		t.Errorf("only %d cells drawn", drawn)
	}
}

// Every scaled cell has to come out one flat colour, and the file indexed.
func TestPNGIsFlatCells(t *testing.T) {
	c := New(30, 20)
	c.Window(c.Rect)
	c.Text(4, 4, "pp", Gold, 0)
	data, err := c.PNG(3)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := img.(*image.Paletted)
	if !ok || p.Rect.Dx() != 90 || p.Rect.Dy() != 60 {
		t.Fatalf("got %T %v", img, img.Bounds())
	}
	for y := range 60 {
		for x := range 90 {
			if p.ColorIndexAt(x, y) != c.ColorIndexAt(x/3, y/3) {
				t.Fatalf("pixel %d,%d isn't its cell's colour", x, y)
			}
		}
	}
}

func BenchmarkScreen(b *testing.B) {
	for b.Loop() {
		c := New(384, 216)
		c.Window(image.Rect(4, 4, 380, 212))
		c.Bird(image.Pt(12, 12))
		for i := range 8 {
			c.Text(150, 16+i*16, "swan knight banneret lv 23", Ink, 30)
		}
		c.Bar(image.Rect(150, 160, 370, 168), 3, 7, Gold)
		data, err := c.PNG(3)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(len(data)), "bytes")
	}
}
