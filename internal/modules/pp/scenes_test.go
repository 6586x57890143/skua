package pp

import (
	"bytes"
	"testing"

	"github.com/6586x57890143/skua/internal/pixel"
)

// Both places are alive in every light: a full loop, and the frames apart
// from one another, so the steam and the fires are seen to move.
func TestPlacesMove(t *testing.T) {
	for light := range lightNames {
		for name, scene := range map[string]func(*pixel.Canvas, int) []*pixel.Canvas{"stair": potScene, "yard": yardScene} {
			frames := scene(pixel.New(screenW, screenH), light)
			if len(frames) != ambientFrames {
				t.Fatalf("%s %d: %d frames", name, light, len(frames))
			}
			if bytes.Equal(frames[0].Pix, frames[ambientFrames/2].Pix) {
				t.Errorf("%s in light %d stands still", name, light)
			}
		}
	}
}

// The fronts as they ship: moving pictures small enough to send.
func TestFrontsSend(t *testing.T) {
	realScreens(t)
	stair, err := StairScreen(night, 1234*unit, 2500*unit, "pot", "copper perch")
	if err != nil {
		t.Fatal(err)
	}
	yard, err := YardFront(dawn, 1234*unit, Livery{Name: "tallow", Color: 0xD9A030, Left: 4}, "week")
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"stair": stair, "yard": yard} {
		t.Logf("%s: %d KB", name, len(b)/1024)
		if len(b) > 6<<20 {
			t.Errorf("%s is %d bytes", name, len(b))
		}
	}
}
