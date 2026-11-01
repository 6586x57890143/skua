package pp

import (
	"image"

	"github.com/6586x57890143/skua/internal/pixel"
)

// A scene is a still painting with a few chosen things alive in it: the
// water, the flames, the lit windows, a gull. Every frame each of them
// moves a small step at its own pace, so the motion is smooth, and
// nothing else changes, so nothing sparkles at random.

// The scenes' loop: ambientFrames frames of ambientBeat milliseconds, so
// the slowest thing in it (the swell) takes the whole loop round.
const (
	ambientFrames = 12
	ambientBeat   = 200
)

// mover draws one living thing onto a scene at frame f of the loop.
type mover func(c *pixel.Canvas, f int)

// ambient is base with every mover drawn on it at each frame of the loop.
func ambient(base *pixel.Canvas, movers ...mover) []*pixel.Canvas {
	frames := make([]*pixel.Canvas, ambientFrames)
	for f := range frames {
		c := base.Copy()
		for _, m := range movers {
			m(c, f)
		}
		frames[f] = c
	}
	return frames
}

// every is f slowed to change once every n frames of the loop, for a thing
// that moves more slowly than the water: a candle every two frames, a
// window every four.
func every(f, n int) int { return f / n }

// windows flickers the lit glass inside box: each window (a patch of
// lamplight, told apart by a coarse grid over the painting) on its own
// slow beat dipping, brightening, or as painted. Lit windows stay lit;
// nothing unlit changes, so the day's painting stays still.
func windows(c *pixel.Canvas, img image.Image, box image.Rectangle, f int) {
	step := every(f, 4)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			col := img.At(x, y)
			if !lit(col) {
				continue
			}
			var toward uint32
			var by float64
			switch (hash(x/5, y/5) + uint32(step)) % 5 {
			case 0:
				toward, by = 0x7A3A10, 0.16
			case 2:
				toward, by = 0xFFF0B8, 0.14
			default:
				continue
			}
			c.SetColorIndex(x, y, c.Dye(blend(hexOf(col), toward, by)))
		}
	}
}
