package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// The avatar is drawn, not computed: art/source/skua_avatar_source.png is the
// original, on a navy field with a lavender halo. This recolours it into
// skua's scheme, so the source stays the one thing a person edits and the
// palette stays the one thing this file owns:
//
//   - the frame (anything clearly blue-shifted) becomes the flat field grey
//     and the halo grey, edge pixels blended between the two by brightness
//     so the stair-stepped halo keeps its shape;
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

// recolor maps one source pixel into skua's scheme. It also reports whether
// the pixel was frame, so the bird can be cut out for the mood icons.
func recolor(c color.NRGBA) (color.NRGBA, bool) {
	l := lum(c)
	// Brightness is snapped to steps before any mapping, so each ramp yields
	// a few dozen fixed tones rather than a smooth gradient: that is what
	// keeps the result pixel art, and keeps it under 256 colours.
	q := math.Round(l/lumStep) * lumStep
	switch {
	case frameAt(c):
		// Field is about 66 bright, halo about 140; the blend between them
		// is only there for the halo's edge pixels, so four steps do.
		return mix(fieldInk, haloInk, math.Round((l-70)/65*3)/3), true
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

// avatar returns the recoloured avatar at source resolution, and the bird
// alone on transparency at the same size.
func avatar() (full, bird *image.NRGBA, err error) {
	f, err := os.Open(avatarSource)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	src, err := png.Decode(f)
	if err != nil {
		return nil, nil, err
	}
	b := src.Bounds()
	full = image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	bird = image.NewNRGBA(full.Bounds())
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			out, frame := recolor(c)
			full.SetNRGBA(x, y, out)
			if !frame {
				bird.SetNRGBA(x, y, out)
			}
		}
	}
	return full, bird, nil
}

// shrink resamples to n square by nearest neighbour, which keeps hard pixel
// edges and adds no colours that are not already in the palette.
func shrink(src *image.NRGBA, n int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			sx := b.Min.X + (2*x+1)*b.Dx()/(2*n)
			sy := b.Min.Y + (2*y+1)*b.Dy()/(2*n)
			dst.SetNRGBA(x, y, src.NRGBAAt(sx, sy))
		}
	}
	return dst
}
