package pixel

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
)

// Soft is the canvas scale times larger as the PlayStation put it on a
// television: its colour cut to 15 bits under the console's 4 by 4
// ordered dither, then scaled up smoothly rather than by whole pixels and
// softened a little more across than down, as a composite signal smeared,
// red drifting a pixel right of the rest, all of it mixed half and half
// with the crisp picture so it stays a hint. It is a JPEG, whose own faint
// artefacts belong to the look, and the screens use it where crisp art
// would read too clean for the 90s.
func (c *Canvas) Soft(scale int) ([]byte, error) {
	w, h := c.Rect.Dx(), c.Rect.Dy()
	// The canvas's colours as floats, a cell each, cut to 5 bits a channel
	// under the ordered dither: its grain is the console's.
	src := make([][3]float32, w*h)
	crisp := make([][3]float32, w*h)
	for y := range h {
		for x := range w {
			r, g, b, _ := c.Palette[c.Pix[c.PixOffset(x, y)]].RGBA()
			nudge := (float32(bayer[y%4][x%4])/16 - 0.5) * 8
			cut := func(v uint32) float32 {
				return float32(min(max(int((float32(v>>8)+nudge)/8)*8+4, 0), 255))
			}
			src[y*w+x] = [3]float32{cut(r), cut(g), cut(b)}
			crisp[y*w+x] = [3]float32{float32(r >> 8), float32(g >> 8), float32(b >> 8)}
		}
	}
	at := func(x, y int) [3]float32 {
		return src[min(max(y, 0), h-1)*w+min(max(x, 0), w-1)]
	}
	// Bilinear: every output pixel samples between the cell centres.
	W, H := w*scale, h*scale
	up := make([][3]float32, W*H)
	for Y := range H {
		fy := (float32(Y)+0.5)/float32(scale) - 0.5
		y0 := int(fy)
		if fy < 0 {
			y0 = -1
		}
		ty := fy - float32(y0)
		for X := range W {
			fx := (float32(X)+0.5)/float32(scale) - 0.5
			x0 := int(fx)
			if fx < 0 {
				x0 = -1
			}
			tx := fx - float32(x0)
			a, b, cc, d := at(x0, y0), at(x0+1, y0), at(x0, y0+1), at(x0+1, y0+1)
			var p [3]float32
			for k := range 3 {
				top := a[k] + (b[k]-a[k])*tx
				bot := cc[k] + (d[k]-cc[k])*tx
				p[k] = top + (bot-top)*ty
			}
			up[Y*W+X] = p
		}
	}
	// Softened across (a composite signal smears sideways) and less down,
	// with red read a pixel to the left so it lands a pixel right.
	pix := func(x, y, k int) float32 {
		return up[min(max(y, 0), H-1)*W+min(max(x, 0), W-1)][k]
	}
	out := image.NewRGBA(image.Rect(0, 0, W, H))
	for Y := range H {
		for X := range W {
			var v [3]uint8
			for k := range 3 {
				x := X
				if k == 0 {
					x--
				}
				across := func(y int) float32 {
					return 0.1*pix(x-2, y, k) + 0.22*pix(x-1, y, k) + 0.36*pix(x, y, k) + 0.22*pix(x+1, y, k) + 0.1*pix(x+2, y, k)
				}
				s := (across(Y-1) + 3*across(Y) + across(Y+1)) / 5
				// Half the effect: an even mix with the crisp cell.
				s = (s + crisp[(Y/scale)*w+X/scale][k]) / 2
				v[k] = uint8(min(max(s+0.5, 0), 255))
			}
			out.SetRGBA(X, Y, color.RGBA{v[0], v[1], v[2], 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// bayer is the PlayStation's 4 by 4 ordered dither, thresholds of 16.
var bayer = [4][4]uint8{
	{0, 8, 2, 10},
	{12, 4, 14, 6},
	{3, 11, 1, 9},
	{15, 7, 13, 5},
}
