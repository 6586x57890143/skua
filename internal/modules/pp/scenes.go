package pp

import (
	"embed"
	"image"
	"math"
	"sync"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The stair and the dyers' yard open on painted places, the way the slack
// tide opens on the sump. The stair is the pot square at the spire's foot:
// the pot sunk in the cobbles with the notice board on its rim, steam off
// it and its fire glowing up through it, the stair winding up the spire
// behind with lit windows all the way. The yard is against the cliff: its
// vats boiling over their fires, the seconds hung to dry swaying and
// dripping, and the runnel taking the three dyes off it to the harbour.

// The pot square and the yard painted in each light, finished art like the
// sump's: the same picture to the pixel, fitted to 384x128 at 40 colours.
// Replace one whole.
//
//go:embed pot*.png yard*.png
var placeFS embed.FS

// placeArts are a place's paintings by light, read once each.
func placeArts(name string) (arts [4]func() *sumpArt) {
	for light := range arts {
		arts[light] = sync.OnceValue(func() *sumpArt {
			file := name + lightNames[light]
			b, err := placeFS.ReadFile(file + ".png")
			if err != nil {
				panic("pp: no painting " + file)
			}
			return &sumpArt{img: decodeArt(b, file), shade: lightShade[light], mist: lightMist[light]}
		})
	}
	return arts
}

var (
	potArts  = placeArts("pot")
	yardArts = placeArts("yard")
)

// The pot square's own places.
var (
	// potRim is the pot's mouth, where the steam comes off it, and potFire
	// the fire showing through it.
	potRim  = image.Rect(142, 78, 228, 82)
	potFire = image.Rect(140, 70, 232, 92)
	// potTown is everything up the spire and round the square with a lit
	// window in it.
	potTown = image.Rect(0, 0, sumpSize.X, 100)
)

// The yard's own places.
var (
	// vats are the mouths of the yard's vats, each with its steam, and
	// vatFires the fires under them.
	vats     = []image.Rectangle{image.Rect(98, 62, 142, 65), image.Rect(148, 65, 178, 68), image.Rect(194, 63, 212, 66), image.Rect(226, 63, 252, 66), image.Rect(260, 64, 284, 67)}
	vatFires = []image.Rectangle{image.Rect(116, 86, 142, 102), image.Rect(158, 84, 178, 100), image.Rect(192, 78, 212, 96), image.Rect(228, 78, 248, 92), image.Rect(268, 76, 284, 92)}
	// seconds are the cloths hung to dry, each from its top edge.
	seconds = []image.Rectangle{image.Rect(24, 0, 58, 36), image.Rect(80, 20, 108, 54), image.Rect(148, 14, 190, 56), image.Rect(232, 26, 300, 56)}
	// yardTown is the windows in the yard's sheds and down the shore.
	yardTown = image.Rect(0, 0, sumpSize.X, 90)
	// The yard's gutter, from where it leaves the yard to where it spills
	// down the stones, and the fan it spreads in at the bottom.
	gutterTop, gutterFoot = image.Pt(304, 94), image.Pt(279, 118)
	gutterFan             = image.Rect(266, 118, 294, 126)
)

// potScene draws the pot square over the top of base in light, a frame
// at a time.
func potScene(base *pixel.Canvas, light int) []*pixel.Canvas {
	art := potArts[light]()
	base.Picture(art.img, image.Point{}, 1)
	return ambient(base,
		func(c *pixel.Canvas, f int) { windows(c, art.img, potTown, f) },
		func(c *pixel.Canvas, f int) { glow(c, art, potFire, f) },
		func(c *pixel.Canvas, f int) { steam(c, art, potRim, 40, f) },
	)
}

// yardScene draws the dyers' yard over the top of base in light, a frame
// at a time.
func yardScene(base *pixel.Canvas, light int) []*pixel.Canvas {
	art := yardArts[light]()
	base.Picture(art.img, image.Point{}, 1)
	return ambient(base,
		func(c *pixel.Canvas, f int) { windows(c, art.img, yardTown, f) },
		func(c *pixel.Canvas, f int) {
			for i, r := range seconds {
				sway(c, art, r, f, i)
			}
		},
		func(c *pixel.Canvas, f int) {
			for i, r := range vatFires {
				fire(c, art, r, f, i)
			}
		},
		func(c *pixel.Canvas, f int) {
			for _, r := range vats {
				steam(c, art, r, 26, f)
			}
		},
		func(c *pixel.Canvas, f int) { runnelFlow(c, art, f, gutterTop, gutterFoot, gutterFan) },
	)
}

// steam rises off the mouth at frame f: puffs set along it, each coming
// up from the mouth, swelling and thinning as it climbs rise pixels, the
// next one starting as it goes, so the loop's last frame meets its first.
func steam(c *pixel.Canvas, art *sumpArt, mouth image.Rectangle, rise, f int) {
	n := max(mouth.Dx()/9, 1)
	type puff struct{ x, y, r, s float64 }
	puffs := make([]puff, n)
	for i := range puffs {
		p := float64((f+i*5)%ambientFrames) / ambientFrames
		x := float64(mouth.Min.X) + (float64(i)+0.5)*float64(mouth.Dx())/float64(n)
		puffs[i] = puff{
			x: x + 2*math.Sin(p*math.Pi+float64(i)),
			y: float64(mouth.Min.Y) - p*float64(rise),
			r: 3 + 6*p,
			s: 0.7 * math.Sin(p*math.Pi),
		}
	}
	tint := shaded(0xE4E0E8, art.shade)
	cache := map[[2]uint8]uint8{}
	box := image.Rect(mouth.Min.X-10, mouth.Min.Y-rise-10, mouth.Max.X+10, mouth.Max.Y).Intersect(c.Rect)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			d := 0.0
			for _, p := range puffs {
				dx, dy := float64(x)-p.x, float64(y)-p.y
				if q := 1 - (dx*dx+dy*dy)/(p.r*p.r); q > 0 {
					d += p.s * q
				}
			}
			var lv uint8
			switch {
			case d > 0.5:
				lv = 3
			case d > 0.3:
				lv = 2
			case d > 0.14:
				lv = 1
			default:
				continue
			}
			i := c.ColorIndexAt(x, y)
			to, ok := cache[[2]uint8{i, lv}]
			if !ok {
				to = c.Dye(blend(hexOf(c.Palette[i]), tint, []float64{0, 0.16, 0.3, 0.44}[lv]))
				cache[[2]uint8{i, lv}] = to
			}
			c.SetColorIndex(x, y, to)
		}
	}
}

// glow is the fire under the pot at frame f, showing through it: the lit
// pixels in box swelling brighter and dying back once round the loop.
func glow(c *pixel.Canvas, art *sumpArt, box image.Rectangle, f int) {
	v := math.Sin(2 * math.Pi * float64(f) / ambientFrames)
	toward, by := uint32(0xFFF0B8), 0.16*v
	if v < 0 {
		toward, by = 0x7A3A10, -0.14*v
	}
	if by < 0.04 {
		return
	}
	by = math.Round(by*20) / 20
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			if col := art.img.At(x, y); lit(col) {
				c.SetColorIndex(x, y, c.Dye(blend(hexOf(col), toward, by)))
			}
		}
	}
}

// fire is a fire under a vat at frame f: its lit pixels flaring and
// sinking in small tongues, each on its own beat, twice round the loop.
func fire(c *pixel.Canvas, art *sumpArt, box image.Rectangle, f, seed int) {
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			col := art.img.At(x, y)
			if !lit(col) {
				continue
			}
			phase := float64(hash(x/2+seed*31, y/3)%6) / 6
			v := math.Sin(2 * math.Pi * (2*float64(f)/ambientFrames + phase))
			switch {
			case v > 0.6:
				c.SetColorIndex(x, y, c.Dye(blend(hexOf(col), 0xFFE08A, 0.22)))
			case v < -0.6:
				c.SetColorIndex(x, y, c.Dye(blend(hexOf(col), 0x7A2A08, 0.2)))
			}
		}
	}
}

// sway is a cloth hung to dry at frame f: hanging from its top edge, its
// lower half swung a pixel one way and then the other once round the
// loop, and a drop gathering at its hem and falling.
func sway(c *pixel.Canvas, art *sumpArt, r image.Rectangle, f, seed int) {
	swing := math.Sin(2*math.Pi*float64(f)/ambientFrames + float64(seed))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		dx := int(math.Round(swing * 1.6 * float64(y-r.Min.Y) / float64(r.Dy())))
		if dx == 0 {
			continue
		}
		for x := r.Min.X; x < r.Max.X; x++ {
			c.SetColorIndex(x, y, c.Dye(hexOf(art.img.At(x-dx, y))))
		}
	}
	// The drop: off the hem a third of the way along, falling over the
	// second half of the loop, a pixel and then two a frame.
	k := (f + seed*3) % ambientFrames
	if k < ambientFrames/2 {
		return
	}
	x := r.Min.X + r.Dx()/3 + seed
	y := r.Max.Y + (k-ambientFrames/2)*(k-ambientFrames/2+1)/2
	if image.Pt(x, y).In(c.Rect) {
		c.SetColorIndex(x, y, c.Dye(shaded(0xB8C4D8, art.shade)))
	}
}
