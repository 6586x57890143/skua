package pixel

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"
	"strings"
)

// Rebuild is the part r of src as w by h cells, the way skua redraws
// someone else's art on her grid: its colours quantised to inks, each cell
// the ink most of its pixels have (UX.md: by majority, never one sample),
// and a cell most of whose pixels are clear left clear.
func Rebuild(src image.Image, r image.Rectangle, w, h, inks int) *image.NRGBA {
	var px []color.NRGBA
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA); c.A >= 0x80 {
				px = append(px, c)
			}
		}
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	if len(px) == 0 || r.Dx() < w || r.Dy() < h {
		return out
	}
	// The inks come from a sample: a few thousand pixels find the same ones
	// as every pixel of a full size render, at a sliver of the cost.
	if step := len(px) / 4096; step > 1 {
		sample := make([]color.NRGBA, 0, len(px)/step+1)
		for i := 0; i < len(px); i += step {
			sample = append(sample, px[i])
		}
		px = sample
	}
	pal := Quantise(px, inks)
	votes := make([]int, len(pal)+1) // the last is clear
	for cy := range h {
		for cx := range w {
			clear(votes)
			x0, x1 := r.Min.X+cx*r.Dx()/w, r.Min.X+(cx+1)*r.Dx()/w
			y0, y1 := r.Min.Y+cy*r.Dy()/h, r.Min.Y+(cy+1)*r.Dy()/h
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
					if c.A < 0x80 {
						votes[len(pal)]++
						continue
					}
					votes[nearest(pal, c)]++
				}
			}
			best := 0
			for i, v := range votes {
				if v > votes[best] {
					best = i
				}
			}
			if best < len(pal) {
				out.SetNRGBA(cx, cy, pal[best])
			}
		}
	}
	return out
}

// Quantise is k colours for px by k-means, seeded evenly along brightness
// so the same picture always gives the same inks.
func Quantise(px []color.NRGBA, k int) []color.NRGBA {
	k = min(k, len(px))
	sorted := slices.Clone(px)
	slices.SortFunc(sorted, func(a, b color.NRGBA) int {
		return (int(a.R) + int(a.G) + int(a.B)) - (int(b.R) + int(b.G) + int(b.B))
	})
	cent := make([]color.NRGBA, k)
	for i := range k {
		cent[i] = sorted[(2*i+1)*len(sorted)/(2*k)]
	}
	sum := make([][3]float64, k)
	n := make([]float64, k)
	for range 12 {
		clear(sum)
		clear(n)
		for _, p := range px {
			i := nearest(cent, p)
			sum[i][0] += float64(p.R)
			sum[i][1] += float64(p.G)
			sum[i][2] += float64(p.B)
			n[i]++
		}
		for i := range cent {
			if n[i] > 0 {
				cent[i] = color.NRGBA{uint8(sum[i][0] / n[i]), uint8(sum[i][1] / n[i]), uint8(sum[i][2] / n[i]), 0xFF}
			}
		}
	}
	return cent
}

func nearest(pal []color.NRGBA, c color.NRGBA) int {
	best, bd := 0, math.MaxFloat64
	for i, p := range pal {
		dr, dg, db := float64(p.R)-float64(c.R), float64(p.G)-float64(c.G), float64(p.B)-float64(c.B)
		if d := dr*dr + dg*dg + db*db; d < bd {
			best, bd = i, d
		}
	}
	return best
}

// Solid is the part of a render that is the figure: the rows and columns
// at least 1% solid, so a stray speck doesn't stretch it.
func Solid(img image.Image) image.Rectangle {
	b := img.Bounds()
	cols, rows := make([]int, b.Dx()), make([]int, b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0xC000 {
				cols[x-b.Min.X]++
				rows[y-b.Min.Y]++
			}
		}
	}
	span := func(v []int, need int) (int, int) {
		lo, hi := 0, len(v)-1
		for lo < hi && v[lo] < max(need, 1) {
			lo++
		}
		for hi > lo && v[hi] < max(need, 1) {
			hi--
		}
		return lo, hi + 1
	}
	x0, x1 := span(cols, b.Dy()/100)
	y0, y1 := span(rows, b.Dx()/100)
	return image.Rect(b.Min.X+x0, b.Min.Y+y0, b.Min.X+x1, b.Min.Y+y1)
}

// Fitted is r grown about its centre to the aspect w:h, so a figure keeps
// its proportions on a grid of that shape.
func Fitted(r image.Rectangle, w, h int) image.Rectangle {
	if r.Dx()*h > r.Dy()*w {
		nh := r.Dx() * h / w
		c := (r.Min.Y + r.Max.Y) / 2
		return image.Rect(r.Min.X, c-nh/2, r.Max.X, c-nh/2+nh)
	}
	nw := r.Dy() * w / h
	c := (r.Min.X + r.Max.X) / 2
	return image.Rect(c-nw/2, r.Min.Y, c-nw/2+nw, r.Max.Y)
}

// Draw sets s in f on any image, in col over a one pixel shadow in shadow
// (nil for none), with its top left at x, y, and returns where the pen
// ends: Canvas.Print for a picture with more colours than a palette holds.
func (f *Face) Draw(dst draw.Image, x, y int, s string, col, shadow color.Color) int {
	b := dst.Bounds()
	set := func(c color.Color) func(image.Point) {
		return func(p image.Point) {
			if p.In(b) {
				dst.Set(p.X, p.Y, c)
			}
		}
	}
	var shade func(image.Point)
	if shadow != nil {
		shade = set(shadow)
	}
	return f.walk(x, y, Plain(s), 0, set(col), shade)
}

// Cut is s cut with "..." to fit width set in f, or s whole when it fits.
func (f *Face) Cut(s string, width int) string {
	s = Plain(s)
	if f.Width(s) <= width {
		return s
	}
	for k := len(s) - 1; k > 0; k-- {
		if cut := strings.TrimSpace(s[:k]) + "..."; f.Width(cut) <= width {
			return cut
		}
	}
	return ""
}
