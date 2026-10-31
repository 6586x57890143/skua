package pixel

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestPalette(t *testing.T) {
	if len(Palette) > 256 {
		t.Fatalf("%d colours don't fit an indexed PNG", len(Palette))
	}
	seen := map[uint8]bool{}
	for _, i := range append([]uint8{Ink, Dim, Shadow, Bone, Gold, Green, Red, Ember, Line, Glove, Slate, Glass, Bronze, Gilt, Yellow, Blue, Crimson, lit[Yellow], lit[Blue], lit[Crimson], shade[Yellow], shade[Blue], shade[Crimson]}, Sky[:]...) {
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
		{2, 2, Line, "a cut corner"},
		{3, 2, Shadow, "the outline"},
		{3, 3, Bone, "the frame"},
		{4, 4, Bone, "the frame's second row"},
		{5, 5, Line, "the rule inside the frame"},
		{7, 6, Sky[0], "the parchment's top band"},
		{7, 23, Sky[5], "the parchment's foot band"},
		{0, 0, Line, "the backdrop"},
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
		case Bone:
			shadow++
		}
	}
	if ink == 0 || shadow == 0 {
		t.Errorf("text drew %d ink and %d shadow", ink, shadow)
	}
	for in, want := range map[string]string{
		"héron\tthief  ok": "heron thief ok",
		"𝓫𝓸𝓫 ＦＵＬＬ 𝐁𝐎𝐋𝐃":    "bob FULL BOLD",
		"🦆 duck": "duck",
	} {
		if got := Plain(in); got != want {
			t.Errorf("Plain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBar(t *testing.T) {
	c := New(20, 5)
	c.Bar(image.Rect(0, 0, 12, 3), 5, 10, Gold)
	// The inside is 10 wide: half is 5 gold cells.
	for x := 1; x < 11; x++ {
		want := Line
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
	if c.ColorIndexAt(1, 1) != Line {
		t.Error("a bar of nothing filled")
	}
	// A gauge is lit along its top row and shaded along its foot.
	c.Bar(image.Rect(0, 0, 12, 5), 10, 10, Blue)
	if c.ColorIndexAt(3, 1) != lit[Blue] || c.ColorIndexAt(3, 2) != Blue || c.ColorIndexAt(3, 3) != shade[Blue] {
		t.Error("a gauge isn't lit and shaded")
	}
}

func TestBirdAndCursor(t *testing.T) {
	c := New(140, 140)
	c.Bird(image.Pt(4, 4))
	c.Cursor(20, 10)
	var drawn int
	for _, p := range c.Pix {
		if p != Line {
			drawn++
		}
	}
	if drawn < 2000 {
		t.Errorf("only %d cells drawn", drawn)
	}
}

func TestFit(t *testing.T) {
	c := New(200, 20)
	if end := c.Fit(0, 0, "short", Ink, 70); end != 5*Advance {
		t.Errorf("a name that fits ends at %d", end)
	}
	if end := c.Fit(0, 0, "eleven char", Ink, 70); end != 11*(Advance-1) {
		t.Errorf("a name a cell too wide ends at %d; want it tighter", end)
	}
	if end := c.Fit(0, 0, "a name far too long for its place", Ink, 70); end > 70 {
		t.Errorf("a long name runs to %d past 70", end)
	}
}

func TestTab(t *testing.T) {
	c := New(80, 40)
	w := image.Rect(2, 2, 78, 38)
	c.Window(w)
	c.Tab(w, "buffs")
	// The frame stays whole over the tab, and the tab fills the corner
	// inside it down to TabDrop.
	if c.ColorIndexAt(8, 2) != Shadow || c.ColorIndexAt(8, 3) != Bone || c.ColorIndexAt(3, 8) != Bone {
		t.Error("the tab breaks the window's frame")
	}
	if c.ColorIndexAt(6, 6) != Line || c.ColorIndexAt(6, 2+TabDrop-1) != Line || c.ColorIndexAt(6, 2+TabDrop) == Line {
		t.Error("the tab doesn't fill the corner inside the frame down to TabDrop")
	}
	var glove int
	for _, p := range c.Pix {
		if p == Glove {
			glove++
		}
	}
	if glove == 0 {
		t.Error("no letters on the tab")
	}
}

// A portrait keeps its own colours: they join the canvas's palette, and the
// shared one is left alone.
func TestPortrait(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 48))
	odd := color.NRGBA{0x12, 0x34, 0x56, 0xFF}
	for y := range 48 {
		for x := range 32 {
			img.Set(x, y, odd)
		}
	}
	shared := len(Palette)
	c := New(80, 110)
	c.Portrait(img, image.Pt(8, 6), 2)
	if got := c.Palette[c.ColorIndexAt(8, 6)]; !same(got, odd) {
		t.Errorf("the portrait's colour came out %v", got)
	}
	if c.ColorIndexAt(5, 3) != Shadow || c.ColorIndexAt(6, 4) != Bone {
		t.Error("the portrait has no frame")
	}
	if len(Palette) != shared {
		t.Error("a portrait grew the shared palette")
	}
	if _, err := c.PNG(2); err != nil {
		t.Fatal(err)
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

// Soft scales up smoothly: a hard edge between two inks comes out with a
// step between them, not a jump.
func TestSoft(t *testing.T) {
	c := New(8, 4)
	c.Fill(image.Rect(4, 0, 8, 4), Glove)
	data, err := c.Soft(3)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 24 || img.Bounds().Dy() != 12 {
		t.Fatalf("%v %v", err, img)
	}
	edge, _, _, _ := img.At(12, 4).RGBA()
	dark, _, _, _ := img.At(1, 4).RGBA()
	light, _, _, _ := img.At(22, 4).RGBA()
	if dark >= edge || edge >= light {
		t.Errorf("the edge isn't softened: %d %d %d", dark>>8, edge>>8, light>>8)
	}
}

func TestWrap(t *testing.T) {
	got := Wrap("the bronze of a bell pulled from the sunk chapel", 14*Advance)
	want := []string{"the bronze of", "a bell pulled", "from the sunk", "chapel"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q; want %q", got, want)
	}
	if got := Wrap("abcdefghij", 4*Advance); strings.Join(got, "|") != "abcd|efgh|ij" {
		t.Errorf("a long word cuts to %q", got)
	}
	if got := Wrap("  ", 70); len(got) != 0 {
		t.Errorf("blank wraps to %q", got)
	}
}

// A dye joins the canvas's own palette once, and leaves the shared one be.
func TestDye(t *testing.T) {
	c, shared := New(4, 4), len(Palette)
	i := c.Dye(0xa2803e)
	if int(i) != shared || c.Dye(0xa2803e) != i || len(Palette) != shared {
		t.Fatalf("dye %d, palette %d, shared %d of %d", i, len(c.Palette), len(Palette), shared)
	}
	if r, g, b, _ := c.Palette[i].RGBA(); r>>8 != 0xa2 || g>>8 != 0x80 || b>>8 != 0x3e {
		t.Errorf("the dye is %v", c.Palette[i])
	}
	if c.Dye(0x333329) != Ink {
		t.Error("an ink already there was added again")
	}
}

// A screen with a hand on it is an animated WebP, its later frames only
// the box round the hand, on even pixels; one without is a JPEG.
func TestScreenMoves(t *testing.T) {
	still, err := New(64, 64).Screen(3)
	if err != nil || bytes.HasPrefix(still, []byte("RIFF")) {
		t.Fatalf("a screen with no hand: %v", err)
	}
	c := New(64, 64)
	c.Cursor(40, 30)
	out, err := c.Screen(3)
	if err != nil || !bytes.HasPrefix(out, []byte("RIFF")) || bytes.Count(out, []byte("ANMF")) != len(Bob) {
		t.Fatalf("a screen with a hand: %v", err)
	}
	second := bytes.Index(out[bytes.Index(out, []byte("ANMF"))+4:], []byte("ANMF")) + bytes.Index(out, []byte("ANMF")) + 4
	f := out[second+8:]
	u24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
	if x, w := u24(f)*2, u24(f[6:])+1; w >= 64*3/2 || x == 0 {
		t.Errorf("the second frame is %d wide at x %d", w, x)
	}
	// The hand is drawn, and drawn over what came after it.
	c.Fill(image.Rect(0, 0, 64, 64), Line)
	if f := c.frame(0); f.ColorIndexAt(39, 30) == Line {
		t.Error("the hand isn't drawn over what was set after it")
	}
}

// A box softened alone is the same pixels as the whole canvas softened, so
// a later frame shows no seam.
func TestSoftInMatches(t *testing.T) {
	c := New(40, 30)
	c.Bird(image.Pt(2, 2))
	whole := c.SoftImage(3)
	box := image.Rect(31, 20, 77, 58)
	part := c.softIn(3, box)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			if whole.RGBAAt(x, y) != part.RGBAAt(x, y) {
				t.Fatalf("(%d,%d) is %v alone and %v whole", x, y, part.RGBAAt(x, y), whole.RGBAAt(x, y))
			}
		}
	}
}

// Frames that differ go out moving; frames that never change go out
// still, since Discord breaks on a one-frame WebP.
func TestAnimate(t *testing.T) {
	a := New(40, 30)
	b := a.Copy()
	b.Fill(image.Rect(10, 10, 14, 14), Gold)
	moving, err := Animate([]*Canvas{a, b, a.Copy()}, []int{100, 100, 100}, 2)
	if err != nil || !bytes.HasPrefix(moving, []byte("RIFF")) {
		t.Fatalf("differing frames: %v", err)
	}
	still, err := Animate([]*Canvas{a, a.Copy()}, []int{100, 100}, 2)
	if err != nil || bytes.HasPrefix(still, []byte("RIFF")) {
		t.Fatalf("still frames went out moving: %v", err)
	}
	if got := changed(a, b); got != image.Rect(10, 10, 14, 14) {
		t.Errorf("changed %v", got)
	}
}
