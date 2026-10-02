package main

import (
	"image"
	"image/color"
	"math"
)

// The profile picture and banner. High resolution pixel art, not high
// fidelity: flat colour fields, one flat halo, and texture built from clean
// 2px clusters and streak columns rather than per-pixel noise. Everything is
// computed (shapes, banded light from the upper left, feather patterns keyed
// to position, a fixed hash for which cluster gets which tone), so every run
// renders the same image and a palette change is one rerun.
//
// What makes it a skua rather than any brown bird: a short heavy hooked
// slate bill, golden hackle streaks down the neck, a streaked chest, and the
// white flash at the base of the primaries. The frame is cool grey; the bird
// carries the only warmth.

func rgb(h uint32) color.NRGBA {
	return color.NRGBA{uint8(h >> 16), uint8(h >> 8), uint8(h), 0xFF}
}

var (
	// Umber, darkest to lightest, and the skua's golden hackle tone.
	umberRamp = []color.NRGBA{rgb(0x2A211B), rgb(0x3E3128), rgb(0x564437), rgb(0x725C4A), rgb(0x947A60)}
	gold      = []color.NRGBA{rgb(0xA98A5E), rgb(0xC9A872)}
	slate     = []color.NRGBA{rgb(0x1D2127), rgb(0x343B45), rgb(0x55606C), rgb(0x7D8994)}
	bone      = []color.NRGBA{rgb(0xB9B4AA), rgb(0xE6E2DA)}
	lineInk   = rgb(0x1A1513)

	// The frame: flat cool grey, a lighter flat halo, pale speed lines.
	fieldInk  = rgb(0x3A3F49)
	haloInk   = rgb(0x596170)
	streakInk = []color.NRGBA{rgb(0x6A7382), rgb(0x8C95A3)}
)

func clampi(v, lo, hi int) int { return max(lo, min(hi, v)) }

// tone picks a ramp step from a light value in [0,1].
func tone(r []color.NRGBA, t float64) color.NRGBA {
	return r[clampi(int(t*float64(len(r))), 0, len(r)-1)]
}

// light is the upper-left key light, 0 in shadow and 1 fully lit.
func light(x, y int, cx, cy, r float64) float64 {
	v := 0.5 + ((cx-float64(x))*0.55+(cy-float64(y))*0.8)/(2*r)
	return math.Max(0, math.Min(0.999, v))
}

// cluster is the 2x2 block noise every texture is built from.
func cluster(x, y int) uint32 { return hash(x/2, y/2) % 8 }

// feathers shades one body pixel: three broad light bands, then a 2x2
// cluster one step lighter or darker.
func feathers(x, y int, t float64) color.NRGBA {
	i := clampi(int(t*3.0)+1, 0, len(umberRamp)-1)
	switch cluster(x, y) {
	case 0:
		i++
	case 1, 2:
		i--
	}
	return umberRamp[clampi(i, 0, len(umberRamp)-1)]
}

func flat(img *image.NRGBA, c color.NRGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
}

func fill(img *image.NRGBA, inside func(x, y float64) bool, paint func(x, y int) color.NRGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if inside(float64(x)+0.5, float64(y)+0.5) {
				img.SetNRGBA(x, y, paint(x, y))
			}
		}
	}
}

func solid(c color.NRGBA) func(int, int) color.NRGBA {
	return func(int, int) color.NRGBA { return c }
}

// wingSpec is one wing: shoulder and tip on the leading edge, its outline,
// and a light offset (negative for the far wing).
type wingSpec struct {
	sx, sy, tx, ty float64
	shape          func(x, y float64) bool
	shift          float64
}

// paintWing lays feathers out along the wing in bands: spotted coverts at
// the leading edge, barred secondaries behind them, the white flash across
// the base of the primaries, and long dark primaries with pale edges.
func paintWing(img *image.NRGBA, w wingSpec) {
	ax, ay := w.tx-w.sx, w.ty-w.sy
	l := math.Hypot(ax, ay)
	ux, uy := ax/l, ay/l
	fill(img, w.shape, func(x, y int) color.NRGBA {
		px, py := float64(x)+0.5-w.sx, float64(y)+0.5-w.sy
		u := (px*ux + py*uy) / l     // 0 at the shoulder, 1 at the tip
		v := math.Abs(px*uy - py*ux) // distance behind the leading edge
		along := int(u * l)
		base := 0.55 + w.shift
		switch {
		case u > 0.48 && u < 0.6 && v > 2:
			if along%4 == 0 {
				return bone[0]
			}
			return bone[1]
		case u >= 0.6:
			switch along % 4 {
			case 0:
				return umberRamp[0]
			case 1:
				return tone(umberRamp, base-0.05)
			}
			return tone(umberRamp, base-0.3)
		case v < 6:
			if int(v)%3 == 1 && (along+int(v))%4 < 2 {
				return gold[0]
			}
			return tone(umberRamp, base+0.1)
		default:
			if int(v)%4 == 0 {
				return tone(umberRamp, base-0.35)
			}
			if along%5 == 0 {
				return umberRamp[0]
			}
			return tone(umberRamp, base-0.1)
		}
	})
}

// banner is 320x110 logical pixels at 4x, 1280x440: the skua seen from
// above, gliding left across a flat halo disc, wings swept back and spread
// the full height, the two white primary flashes mirrored. That pair of
// flashes is how a skua is told apart at a distance, so it is the one thing
// the banner is built around. A short wake trails off the tail. The bird
// sits right of centre, clear of where Discord overlaps the avatar.
func banner() *image.NRGBA {
	const w, h = 320, 110
	const cy = 55.0 // the bird's axis; everything below mirrors across it
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	flat(img, fieldInk)
	fill(img, func(x, y float64) bool { return math.Hypot(x-228, y-cy) <= 50 }, solid(haloInk))

	// Wake: broken lines off the tail, brighter nearer the bird.
	for i, ln := range [][3]int{{258, 300, 49}, {262, 312, 53}, {256, 296, 57}, {264, 306, 61}} {
		for x := ln[0]; x < ln[1]; x++ {
			if (x+6*i)%15 > 10 {
				continue
			}
			c := streakInk[1]
			if x > (ln[0]+ln[1])/2 {
				c = streakInk[0]
			}
			img.SetNRGBA(x, ln[2], c)
		}
	}

	bird := image.NewNRGBA(image.Rect(0, 0, w, h))
	mirror := func(xy ...float64) func(x, y float64) bool {
		m := make([]float64, len(xy))
		for i := range xy {
			m[i] = xy[i]
			if i%2 == 1 {
				m[i] = 2*cy - xy[i]
			}
		}
		return poly(m...)
	}
	upper := []float64{203, 50, 210, 32, 222, 16, 236, 4, 243, 8, 239, 22, 233, 36, 227, 49}
	paintWing(bird, wingSpec{sx: 206, sy: 49, tx: 236, ty: 5, shift: 0.05, shape: poly(upper...)})
	paintWing(bird, wingSpec{sx: 206, sy: 2*cy - 49, tx: 236, ty: 2*cy - 5, shift: -0.1, shape: mirror(upper...)})

	// Tail, body and head along the axis, lit from the upper left.
	fill(bird, poly(232, 50, 251, 52, 253, 55, 251, 58, 232, 60), func(x, y int) color.NRGBA {
		if (x+y)%4 == 0 {
			return umberRamp[0]
		}
		return umberRamp[1]
	})
	fill(bird, func(x, y float64) bool { return inEllipse(x, y, 216, cy, 19, 8) }, func(x, y int) color.NRGBA {
		t := light(x, y, 214, cy, 16)
		// Golden hackles on the nape run along the body, seen from above.
		if x < 214 && y%3 == 1 && (x+int(hash(0, y)%5))%6 < 4 {
			return gold[clampi(int(t*2), 0, 1)]
		}
		if (x+(y/4)%2*2)%5 == 0 {
			return tone(umberRamp, t-0.25) // scalloped mantle
		}
		return feathers(x, y, t-0.1)
	})
	fill(bird, func(x, y float64) bool { return inEllipse(x, y, 196, cy, 8, 6.5) }, func(x, y int) color.NRGBA {
		return feathers(x, y, light(x, y, 196, cy, 8)-0.3)
	})
	fill(bird, poly(190, 52.5, 182, 54.5, 181, 55.5, 182, 56, 190, 57.5), func(x, y int) color.NRGBA {
		if y < 55 {
			return slate[3]
		}
		return slate[2]
	})
	bird.SetNRGBA(181, 55, slate[0]) // the hook's tip

	outlineBird(bird)
	bird.SetNRGBA(193, 51, rgb(0x0A0908)) // eyes, one each side
	bird.SetNRGBA(193, 58, rgb(0x0A0908))
	over(img, bird)
	return img
}

// outlineBird turns every bird pixel that touches empty space into the
// outline colour, so the silhouette reads at any size.
func outlineBird(b *image.NRGBA) {
	r := b.Bounds()
	var edge []image.Point
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if b.NRGBAAt(x, y).A == 0 {
				continue
			}
			for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				p := image.Pt(x+d.X, y+d.Y)
				if !p.In(r) || b.NRGBAAt(p.X, p.Y).A == 0 {
					edge = append(edge, image.Pt(x, y))
					break
				}
			}
		}
	}
	for _, p := range edge {
		b.SetNRGBA(p.X, p.Y, lineInk)
	}
}

func over(dst, src *image.NRGBA) {
	r := src.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if c := src.NRGBAAt(x, y); c.A != 0 {
				dst.SetNRGBA(x, y, c)
			}
		}
	}
}

func inEllipse(x, y, cx, cy, rx, ry float64) bool {
	dx, dy := (x-cx)/rx, (y-cy)/ry
	return dx*dx+dy*dy <= 1
}

// poly returns a point-in-polygon test (even-odd) for x0,y0,x1,y1,...
func poly(xy ...float64) func(x, y float64) bool {
	return func(x, y float64) bool {
		in := false
		n := len(xy) / 2
		for i, j := 0, n-1; i < n; j, i = i, i+1 {
			xi, yi, xj, yj := xy[2*i], xy[2*i+1], xy[2*j], xy[2*j+1]
			if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
				in = !in
			}
		}
		return in
	}
}

// hash is a fixed per-pixel noise, so the texture is the same on every run.
func hash(x, y int) uint32 {
	h := uint32(x)*374761393 + uint32(y)*668265263
	h = (h ^ (h >> 13)) * 1274126177
	return h ^ (h >> 16)
}
