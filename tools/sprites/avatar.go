package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// The avatar is drawn, not computed: art/source/skua_avatar_source.png is the
// original, on a navy field with a lavender halo. This lifts the bird out
// and recolours it into skua's scheme, so the source stays the one thing a
// person edits and the palette stays the one thing this file owns:
//
//   - the frame (anything clearly blue-shifted) is dropped; the profile
//     picture redraws it as a flat field and a computed halo disc, centred
//     on the grid, which the source's hand-stepped halo is not quite;
//   - the bird's warm-neutral feathers map by brightness onto the umber
//     ramp, topping out in the golden hackle tone;
//   - cool greys (the bill, the darker wing feathers) map onto slate;
//   - the near-black outline and the eye's glint snap to the outline ink
//     and bone.

const avatarSource = "art/source/skua_avatar_source.png"

// lumStep is the brightness quantum the recolour snaps to.
const lumStep = 12.0

// frameAt reports whether a source pixel is the navy field or the lavender
// halo rather than the bird: the frame is blue-shifted by about 35, the bird
// and its outline by under 15.
func frameAt(c color.NRGBA) bool {
	return int(c.B)-int(c.R) >= 22 && c.B >= 80
}

func lum(c color.NRGBA) float64 {
	return 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
}

// stop is one point on a brightness-to-colour gradient.
type stop struct {
	at float64
	c  color.NRGBA
}

func gradient(stops []stop, l float64) color.NRGBA {
	if l <= stops[0].at {
		return stops[0].c
	}
	for i := 1; i < len(stops); i++ {
		if l <= stops[i].at {
			a, b := stops[i-1], stops[i]
			return mix(a.c, b.c, (l-a.at)/(b.at-a.at))
		}
	}
	return stops[len(stops)-1].c
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = math.Max(0, math.Min(1, t))
	f := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.NRGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 0xFF}
}

var (
	featherStops = []stop{
		{8, lineInk}, {32, umberRamp[0]}, {52, umberRamp[1]}, {76, umberRamp[2]},
		{102, umberRamp[3]}, {130, umberRamp[4]}, {165, gold[1]},
	}
	slateStops = []stop{
		{8, lineInk}, {35, slate[0]}, {60, slate[1]}, {95, slate[2]}, {150, slate[3]},
	}
)

// recolor maps one source pixel into skua's scheme, or reports it as frame.
func recolor(c color.NRGBA) (color.NRGBA, bool) {
	l := lum(c)
	// Brightness is snapped to steps before any mapping, so each ramp yields
	// a few dozen fixed tones rather than a smooth gradient: that is what
	// keeps the result pixel art, and keeps it under 256 colours.
	q := math.Round(l/lumStep) * lumStep
	switch {
	case frameAt(c):
		return color.NRGBA{}, true
	case l < 14:
		return lineInk, false // outline
	case l > 190:
		return bone[1], false // the eye's glint
	case int(c.B)-int(c.R) > 1:
		return gradient(slateStops, q), false
	default:
		return gradient(featherStops, q), false
	}
}

// grid is the avatar's logical resolution, in cells across. Every avatar
// output is a whole multiple of it: the profile picture is 128x8 = 1024 and
// the mood icons 128x2 = 256, so a cell is always a hard square of one size.
const grid = 128

// haloR is the halo disc's radius in cells, the source halo's 1041px across
// 1254 at 128 cells. Centred on the grid, it leaves 11 cells of field on
// every side.
const haloR = 53

// bird returns the source's bird, recoloured, on transparency at grid
// resolution.
func bird() (*image.NRGBA, error) {
	f, err := os.Open(avatarSource)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	src, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			if c, frame := recolor(c); !frame {
				out.SetNRGBA(x, y, c)
			}
		}
	}
	return cells(out, grid), nil
}

// cells resamples src onto an n by n grid. A cell is opaque when most of its
// source pixels are, and then takes the colour most of those have. Nearest
// neighbour would sample one pixel per cell, and the source's own cells are
// about 13.5px, not a whole number, so its output mixed cells 1px to 5px
// wide. This keeps every cell the same size and adds no colour.
func cells(src *image.NRGBA, n int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	for cy := range n {
		for cx := range n {
			count := map[color.NRGBA]int{}
			var best color.NRGBA
			top, opaque, all := 0, 0, 0
			for y := cy * b.Dy() / n; y < (cy+1)*b.Dy()/n; y++ {
				for x := cx * b.Dx() / n; x < (cx+1)*b.Dx()/n; x++ {
					all++
					c := src.NRGBAAt(b.Min.X+x, b.Min.Y+y)
					if c.A == 0 {
						continue
					}
					opaque++
					count[c]++
					if count[c] > top {
						best, top = c, count[c]
					}
				}
			}
			if 2*opaque > all {
				dst.SetNRGBA(cx, cy, best)
			}
		}
	}
	return dst
}

// pfp is the profile picture at grid resolution: the bird over a halo disc
// centred on the field.
func pfp(b *image.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(b.Bounds())
	flat(img, fieldInk)
	fill(img, func(x, y float64) bool { return math.Hypot(x-grid/2, y-grid/2) <= haloR }, solid(haloInk))
	over(img, b)
	return img
}

// centred moves the bird so its bounding box sits in the middle of the grid,
// to the nearest whole cell.
func centred(b *image.NRGBA) *image.NRGBA {
	box := image.Rectangle{}
	r := b.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if b.NRGBAAt(x, y).A != 0 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	d := image.Pt((r.Dx()-box.Dx())/2-box.Min.X, (r.Dy()-box.Dy())/2-box.Min.Y)
	out := image.NewNRGBA(r)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			out.SetNRGBA(x+d.X, y+d.Y, b.NRGBAAt(x, y))
		}
	}
	return out
}
