package pp

import (
	"image"

	"github.com/6586x57890143/skua/internal/pixel"
)

// A scene is a still painting with one place in it alive: a few movers
// close together, taking turns. An animated WebP frame can only redraw one
// box, so a frame that moves one small thing costs little, and one that
// moves things all over the picture costs the whole picture again. Each
// frame here moves one mover a step, so every frame's box is that mover's.

// mover is one moving thing in a scene: the box it moves inside, and how
// to draw it at a step, onto the whole scene (what falls outside its box
// is never shown).
type mover struct {
	box  image.Rectangle
	draw func(c *pixel.Canvas, step int)
}

// ambientSteps is how many steps each mover takes round the loop.
const ambientSteps = 3

// ambient is base with its movers taking turns: every mover drawn at its
// first step, then one mover a frame moving on a step, round and round
// until each is back where it started.
func ambient(base *pixel.Canvas, movers []mover) []*pixel.Canvas {
	at := func(m mover, step int) *pixel.Canvas {
		c := base.Copy()
		m.draw(c, step)
		return c
	}
	cur := base.Copy()
	// A mover's copy may have dyed colours of its own, so its pixels come
	// over by colour, not by index.
	paste := func(from *pixel.Canvas, r image.Rectangle) {
		r = r.Intersect(cur.Rect)
		inks := map[uint8]uint8{}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				i := from.ColorIndexAt(x, y)
				to, ok := inks[i]
				if !ok {
					to = cur.Dye(hexOf(from.Palette[i]))
					inks[i] = to
				}
				cur.SetColorIndex(x, y, to)
			}
		}
	}
	for _, m := range movers {
		paste(at(m, 0), m.box)
	}
	frames := []*pixel.Canvas{cur.Copy()}
	if len(movers) == 0 {
		return frames
	}
	steps := make([]int, len(movers))
	for k := 1; k < len(movers)*ambientSteps; k++ {
		i := k % len(movers)
		steps[i] = (steps[i] + 1) % ambientSteps
		paste(at(movers[i], steps[i]), movers[i].box)
		frames = append(frames, cur.Copy())
	}
	return frames
}

// windows flickers the lit glass inside box at a step: each window (a
// patch of lamplight, told apart by a coarse grid over the painting) on
// its own beat guttering low, flaring, or as painted. Nothing unlit
// changes, so the day's painting stays still.
func windows(c *pixel.Canvas, img image.Image, box image.Rectangle, step int) {
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
				toward, by = 0x4A200A, 0.45
			case 2:
				toward, by = 0xFFF0B8, 0.35
			default:
				continue
			}
			c.SetColorIndex(x, y, c.Dye(blend(hexOf(col), toward, by)))
		}
	}
}
