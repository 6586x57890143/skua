package main

import (
	"image"
	"image/color"
	"math"
)

// The profile picture and banner for the bot's Discord profile and the
// README. Same pixel language as the mood icons, laid out like merlin's: a
// bust in a halo, and the bird in flight with speed lines. Greys first, the
// skua itself carrying the only warmth (umber, cinnamon streaks) and its one
// signature mark, the white flash at the base of the primaries.

var (
	bgDeep  = color.NRGBA{0x1C, 0x1F, 0x24, 0xFF}
	bgHalo  = color.NRGBA{0x4A, 0x50, 0x59, 0xFF}
	bgLine  = color.NRGBA{0x52, 0x59, 0x63, 0xFF}
	wingFar = color.NRGBA{0x22, 0x1D, 0x1A, 0xFF}
	umber   = color.NRGBA{0x3A, 0x31, 0x2B, 0xFF}
	umberLo = color.NRGBA{0x2A, 0x23, 0x1F, 0xFF}
	streak  = color.NRGBA{0x7E, 0x6C, 0x5C, 0xFF}
	flash   = color.NRGBA{0xE4, 0xE0, 0xD8, 0xFF}
)

// pfp is the bust from the mood icons on a halo, 40 logical pixels square,
// with the body rounded off by the halo's circle rather than cut flat.
func pfp() *image.NRGBA {
	const n = 40
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	cx, cy, r := 21.0, 19.0, 16.0
	for y := range n {
		for x := range n {
			c := bgDeep
			if inCircle(x, y, cx, cy, r) {
				c = bgHalo
			}
			img.SetNRGBA(x, y, c)
		}
	}
	layer := image.NewNRGBA(image.Rect(0, 0, n, n))
	draw(layer, bird, 6, 4, ink)
	// Round the body off below the head, so the bust sits in the halo.
	for y := range n {
		for x := range n {
			if y > 26 && !inCircle(x, y, cx, cy+2, r+3) {
				layer.SetNRGBA(x, y, color.NRGBA{})
			}
		}
	}
	outlineBird(layer)
	over(img, layer)
	return img
}

// banner is 256x88 logical pixels, which scales by 4 to 1024x352: the bird
// in flight on the right, speed lines trailing left.
func banner() *image.NRGBA {
	const w, h = 256, 88
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, bgDeep)
		}
	}
	// Speed lines: a few broken strokes behind the bird, pale grey.
	for i, y := range []int{38, 44, 50, 56, 61} {
		start := 92 + 9*(i%3)
		for x := start; x < 138-4*(i%2); x++ {
			if (x+3*i)%13 < 9 {
				img.SetNRGBA(x, y, bgLine)
			}
		}
	}

	layer := image.NewNRGBA(image.Rect(0, 0, w, h))
	paint := func(inside func(x, y float64) bool, fill func(x, y int) color.NRGBA) {
		for y := range h {
			for x := range w {
				if inside(float64(x)+0.5, float64(y)+0.5) {
					layer.SetNRGBA(x, y, fill(x, y))
				}
			}
		}
	}
	mottle := func(base, lo, hi color.NRGBA, pHi int) func(x, y int) color.NRGBA {
		return func(x, y int) color.NRGBA {
			switch v := int(hash(x, y) % 100); {
			case v < pHi:
				return hi
			case v < pHi+30:
				return lo
			default:
				return base
			}
		}
	}

	// Back to front: far wing, tail, body, head, bill, near wing.
	farWing := poly(166, 44, 140, 26, 116, 10, 122, 20, 136, 32, 152, 44, 170, 52)
	paint(farWing, mottle(wingFar, umberLo, umber, 8))
	paint(poly(124, 14, 131, 19, 129, 25, 122, 20), func(x, y int) color.NRGBA { return dim(flash) })

	paint(poly(192, 47, 216, 50, 218, 57, 194, 60), mottle(umberLo, wingFar, umber, 10))
	paint(func(x, y float64) bool { return inEllipse(x, y, 176, 53, 22, 11) }, mottle(umber, umberLo, streak, 6))
	paint(func(x, y float64) bool { return inEllipse(x, y, 152, 47, 11, 10) }, mottle(umber, umberLo, streak, 8))
	// Hooked bill, slate, pointing left.
	paint(poly(143, 44, 130, 46, 126, 49, 127, 53, 130, 50, 143, 51), func(x, y int) color.NRGBA {
		if y <= 47 {
			return ink['B']
		}
		return ink['b']
	})
	// Feet tucked under the tail.
	paint(poly(186, 61, 194, 61, 194, 63, 186, 63), func(int, int) color.NRGBA { return ink['b'] })

	nearWing := poly(168, 46, 182, 40, 208, 22, 240, 6, 238, 15, 226, 26, 212, 38, 196, 50, 182, 54)
	paint(nearWing, func(x, y int) color.NRGBA {
		// Primaries get long feather strokes rather than speckle.
		if x > 222 && (x-y)%4 == 0 {
			return umberLo
		}
		return mottle(umber, umberLo, streak, 6)(x, y)
	})
	// The skua mark: a white flash at the base of the primaries.
	paint(poly(212, 21, 224, 13, 230, 19, 218, 29), func(int, int) color.NRGBA { return flash })

	outlineBird(layer)
	// Eye, after the outline so it is never mistaken for an edge.
	layer.SetNRGBA(147, 44, ink['e'])
	layer.SetNRGBA(148, 44, ink['e'])
	layer.SetNRGBA(147, 45, ink['e'])
	layer.SetNRGBA(148, 45, ink['e'])
	layer.SetNRGBA(147, 44, ink['w'])
	over(img, layer)
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
		b.SetNRGBA(p.X, p.Y, ink['k'])
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

func inCircle(x, y int, cx, cy, r float64) bool {
	dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
	return dx*dx+dy*dy <= r*r
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

// hash is a fixed per-pixel noise, so the mottling is the same on every run.
func hash(x, y int) uint32 {
	h := uint32(x)*374761393 + uint32(y)*668265263
	h = (h ^ (h >> 13)) * 1274126177
	return h ^ (h >> 16)
}

func dim(c color.NRGBA) color.NRGBA {
	f := func(v uint8) uint8 { return uint8(math.Round(float64(v) * 0.6)) }
	return color.NRGBA{f(c.R), f(c.G), f(c.B), c.A}
}
