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

// streaked is the shared plumage: dark cap above capY, golden hackles in
// staggered columns down the neck between capY and chestY, pale streaks on
// a darker chest below.
func streaked(x, y int, t float64, capY, chestY int) color.NRGBA {
	switch {
	case y < capY:
		return feathers(x, y, t-0.3)
	case y < capY+13:
		return feathers(x, y, t)
	case y < chestY:
		if x%3 == 1 && (y+int(hash(x, 0)%5))%6 < 4 {
			return gold[clampi(int(t*2), 0, 1)]
		}
		return feathers(x, y, t-0.1)
	default:
		if x%3 == 0 && (y+int(hash(x, 1)%4))%5 < 3 {
			return tone(umberRamp, t+0.35)
		}
		return feathers(x, y, t-0.2)
	}
}

// pfp is 64 logical pixels at 16x: the bust facing left on a flat halo.
func pfp() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, bustSize, bustSize))
	flat(img, fieldInk)
	fill(img, func(x, y float64) bool { return math.Hypot(x-33, y-29) <= 27 }, solid(haloInk))
	over(img, bust())
	return img
}

const bustSize = 64

// bust is the bird alone on a transparent 64px canvas: the profile picture
// puts it on the halo, and every mood icon wears it with a badge.
func bust() *image.NRGBA {
	const n = bustSize
	bird := image.NewNRGBA(image.Rect(0, 0, n, n))
	head := func(x, y float64) bool { return inEllipse(x, y, 34, 24, 13, 12) }
	body := poly(25, 29, 18, 37, 12, 45, 9, 52, 12, 57, 22, 60, 44, 60, 54, 57, 57, 50, 53, 40, 46, 31)
	fill(bird, func(x, y float64) bool { return head(x, y) || body(x, y) }, func(x, y int) color.NRGBA {
		return streaked(x, y, light(x, y, 32, 34, 28), 18, 44)
	})
	// Short, heavy, hooked slate bill: lit culmen, dark gape, a nostril.
	fill(bird, poly(23, 19, 17, 20, 13, 22, 11, 25, 11, 29, 13, 28, 15, 26, 23, 26), func(x, y int) color.NRGBA {
		if y <= 21 {
			return slate[3]
		}
		return slate[2]
	})
	fill(bird, poly(23, 26, 16, 26, 14, 28, 17, 29, 23, 29), solid(slate[1]))
	for x := 15; x <= 23; x++ {
		bird.SetNRGBA(x, 26, lineInk)
	}
	bird.SetNRGBA(17, 22, slate[0])
	bird.SetNRGBA(18, 22, slate[0])

	outlineBird(bird)
	eye(bird, 25, 18)
	return bird
}

// eye is 5x5: a dark ring, a black eye, a bright glint, and a pale crescent
// under it, the one light mark on a dark face.
func eye(img *image.NRGBA, x0, y0 int) {
	for dy := -1; dy <= 5; dy++ {
		for dx := -1; dx <= 5; dx++ {
			if (dx == -1 || dx == 5) && (dy == -1 || dy == 5) {
				continue
			}
			img.SetNRGBA(x0+dx, y0+dy, lineInk)
		}
	}
	for dy := range 5 {
		for dx := range 5 {
			if (dx == 0 || dx == 4) && (dy == 0 || dy == 4) {
				continue
			}
			img.SetNRGBA(x0+dx, y0+dy, rgb(0x0A0908))
		}
	}
	img.SetNRGBA(x0+1, y0+1, bone[1])
	img.SetNRGBA(x0+2, y0+1, bone[1])
	img.SetNRGBA(x0+1, y0+2, bone[0])
	for dx := range 5 {
		img.SetNRGBA(x0+dx, y0+6, umberRamp[4])
	}
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

// banner is 320x110 logical pixels at 4x, 1280x440, on a flat field: the
// skua flying left, far wing raised behind its head, near wing raised wide,
// tail down, feet hanging, a few speed lines trailing below.
func banner() *image.NRGBA {
	const w, h = 320, 110
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	flat(img, fieldInk)
	for i, ln := range [][3]int{{118, 168, 64}, {126, 182, 69}, {112, 160, 74}, {130, 176, 79}, {122, 156, 84}} {
		for x := ln[0]; x < ln[1]; x++ {
			if (x+7*i)%19 > 14 {
				continue
			}
			c := streakInk[0]
			if x > (ln[0]+ln[1])/2 {
				c = streakInk[1]
			}
			img.SetNRGBA(x, ln[2]-(x-ln[0])/12, c)
		}
	}

	bird := image.NewNRGBA(image.Rect(0, 0, w, h))
	paintWing(bird, wingSpec{sx: 186, sy: 44, tx: 146, ty: 6, shift: -0.15,
		shape: poly(194, 50, 180, 32, 164, 18, 145, 5, 147, 16, 151, 26, 159, 37, 171, 47, 182, 54)})

	// Tail first so the body overlaps it, then body, then head.
	fill(bird, poly(232, 74, 253, 86, 251, 93, 238, 94, 224, 86), func(x, y int) color.NRGBA {
		if (x+y)%4 == 0 {
			return umberRamp[0]
		}
		return umberRamp[1]
	})
	fill(bird, poly(180, 40, 200, 43, 222, 54, 238, 72, 236, 84, 222, 87, 196, 75, 176, 60, 170, 51), func(x, y int) color.NRGBA {
		return streaked(x, y, light(x, y, 200, 58, 30), 0, 66)
	})
	fill(bird, func(x, y float64) bool { return inEllipse(x, y, 177, 48, 11, 10) }, func(x, y int) color.NRGBA {
		return streaked(x, y, light(x, y, 175, 48, 11), 44, 200)
	})
	fill(bird, poly(168, 45, 162, 46, 158, 49, 157, 53, 159, 52, 162, 50, 168, 51), func(x, y int) color.NRGBA {
		if y <= 46 {
			return slate[3]
		}
		return slate[2]
	})
	fill(bird, poly(168, 51, 162, 51, 161, 53, 168, 54), solid(slate[1]))
	fill(bird, poly(212, 82, 216, 82, 218, 92, 214, 93), solid(slate[1]))
	fill(bird, poly(219, 84, 222, 84, 224, 93, 220, 94), solid(slate[2]))

	paintWing(bird, wingSpec{sx: 196, sy: 46, tx: 312, ty: 4, shift: 0.05,
		shape: poly(190, 46, 210, 34, 232, 21, 270, 8, 312, 3, 306, 12, 286, 22, 262, 34, 240, 46, 222, 58, 204, 60)})

	outlineBird(bird)
	eye(bird, 168, 43)
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
