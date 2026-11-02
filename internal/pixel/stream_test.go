package pixel

import (
	"bytes"
	"image"
	"testing"

	"github.com/HugoSmits86/nativewebp"
)

// The old way: every part softened and encoded by nativewebp's EncodeAll.
func animateUncached(t *testing.T, frames []*Canvas, beats []int, scale int) []byte {
	t.Helper()
	full := image.Rect(0, 0, frames[0].Rect.Dx()*scale, frames[0].Rect.Dy()*scale)
	ani := nativewebp.Animation{}
	add := func(f *Canvas, box image.Rectangle, beat int) {
		ani.Images = append(ani.Images, f.softIn(scale, box))
		ani.Durations = append(ani.Durations, uint(beat))
		ani.Disposals = append(ani.Disposals, 0)
	}
	add(frames[0], full, beats[0])
	for i := 1; i < len(frames); i++ {
		d := changed(frames[i-1], frames[i]).Inset(-1)
		add(frames[i], image.Rect(d.Min.X*scale&^1, d.Min.Y*scale&^1, d.Max.X*scale, d.Max.Y*scale).Intersect(full), beats[i])
	}
	var buf bytes.Buffer
	if err := nativewebp.EncodeAll(&buf, &ani, &nativewebp.Options{CompressionLevel: nativewebp.BestSpeed}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// scene is a still ground with a member's own corner, and a light that
// moves along the top on its own.
func scene(own uint32) ([]*Canvas, []int) {
	base := New(96, 54)
	base.Window(image.Rect(2, 30, 94, 52))
	base.Text(8, 36, "purse", base.Dye(own), 0)
	var frames []*Canvas
	for f := range 4 {
		c := base.Copy()
		for x := 10 + f*8; x < 16+f*8; x++ {
			for y := 4; y < 9; y++ {
				c.SetColorIndex(x, y, c.Dye(0xF0CF6A))
			}
		}
		frames = append(frames, c)
	}
	return frames, []int{200, 200, 200, 200}
}

// Animate writes what nativewebp would, byte for byte, whether its parts
// were encoded now or found cached; and a second member's draw of the same
// moving scene encodes only their own first frame.
func TestAnimateCachesParts(t *testing.T) {
	was := Draft.Load()
	Draft.Store(false)
	defer Draft.Store(was)
	streams.Lock()
	streams.m, streams.size = map[[32]byte][]byte{}, 0
	streams.Unlock()

	frames, beats := scene(0xC8962E)
	want := animateUncached(t, frames, beats, 3)
	for range 2 {
		got, err := Animate(frames, beats, 3)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Animate wrote %d bytes, not nativewebp's %d", len(got), len(want))
		}
	}

	encoded := Encoded.Load()
	other, beats := scene(0x9EA3AC)
	got, err := Animate(other, beats, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, animateUncached(t, other, beats, 3)) {
		t.Fatal("a second member's screen isn't nativewebp's")
	}
	if n := Encoded.Load() - encoded; n != 1 {
		t.Errorf("a second member's screen encoded %d parts, want only their first frame", n)
	}
}
