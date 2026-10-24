package pixel

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"slices"
	"sync"

	"github.com/HugoSmits86/nativewebp"
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
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, c.SoftImage(scale), &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SoftImage is the canvas softened as Soft has it, before it's encoded.
func (c *Canvas) SoftImage(scale int) *image.RGBA {
	c = c.frame(0)
	return c.softIn(scale, image.Rect(0, 0, c.Rect.Dx()*scale, c.Rect.Dy()*scale))
}

// softIn is the canvas softened as SoftImage has it, but only the box out
// of the output, which is all a frame that moves a little needs.
func (c *Canvas) softIn(scale int, out image.Rectangle) *image.RGBA {
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
	// Bilinear: every output pixel samples between the cell centres, out
	// to where the softening below reads.
	W, H := w*scale, h*scale
	reach := out.Inset(-3).Intersect(image.Rect(0, 0, W, H))
	up := make([][3]float32, reach.Dx()*reach.Dy())
	for Y := reach.Min.Y; Y < reach.Max.Y; Y++ {
		fy := (float32(Y)+0.5)/float32(scale) - 0.5
		y0 := int(fy)
		if fy < 0 {
			y0 = -1
		}
		ty := fy - float32(y0)
		for X := reach.Min.X; X < reach.Max.X; X++ {
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
			up[(Y-reach.Min.Y)*reach.Dx()+X-reach.Min.X] = p
		}
	}
	// Softened across (a composite signal smears sideways) and less down,
	// with red read a pixel to the left so it lands a pixel right.
	pix := func(x, y, k int) float32 {
		return up[(min(max(y, 0), H-1)-reach.Min.Y)*reach.Dx()+min(max(x, 0), W-1)-reach.Min.X][k]
	}
	img := image.NewRGBA(out)
	for Y := out.Min.Y; Y < out.Max.Y; Y++ {
		for X := out.Min.X; X < out.Max.X; X++ {
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
			img.SetRGBA(X, Y, color.RGBA{v[0], v[1], v[2], 0xFF})
		}
	}
	return img
}

// bayer is the PlayStation's 4 by 4 ordered dither, thresholds of 16.
var bayer = [4][4]uint8{
	{0, 8, 2, 10},
	{12, 4, 14, 6},
	{3, 11, 1, 9},
	{15, 7, 13, 5},
}

// Screen is the canvas as a screen goes out: still, as Soft has it, or
// when a hand is on it, an animated WebP of the hand nudging Bob toward
// what it points at. The first frame goes whole; the rest are only the box
// round the hands, softened on their own, so a moving screen costs about
// one frame to make and to send.
func (c *Canvas) Screen(scale int) ([]byte, error) {
	if len(c.hands) == 0 {
		return c.Soft(scale)
	}
	var box image.Rectangle
	for _, at := range c.hands {
		box = box.Union(image.Rect(at.X-len(hand[0]), at.Y-len(hand)/2, at.X+slices.Max(Bob), at.Y-len(hand)/2+len(hand)))
	}
	// A cell round it for the smoothing, and a frame's place is stored
	// halved, so the box starts on even pixels.
	box = box.Inset(-1)
	out := image.Rect(box.Min.X*scale&^1, box.Min.Y*scale&^1, box.Max.X*scale, box.Max.Y*scale).Intersect(image.Rect(0, 0, c.Rect.Dx()*scale, c.Rect.Dy()*scale))
	soft := map[int]image.Image{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, b := range Bob {
		if _, ok := soft[b]; ok {
			continue
		}
		soft[b] = nil
		wg.Go(func() {
			var img image.Image
			if b == Bob[0] {
				img = c.frame(b).softIn(scale, image.Rect(0, 0, c.Rect.Dx()*scale, c.Rect.Dy()*scale))
			} else {
				img = c.frame(b).softIn(scale, out)
			}
			mu.Lock()
			soft[b] = img
			mu.Unlock()
		})
	}
	wg.Wait()
	ani := nativewebp.Animation{}
	for i, b := range Bob {
		img := soft[b]
		if i > 0 && b == Bob[0] {
			img = img.(*image.RGBA).SubImage(out)
		}
		ani.Images = append(ani.Images, img)
		ani.Durations = append(ani.Durations, Beat)
		ani.Disposals = append(ani.Disposals, 0)
	}
	var buf bytes.Buffer
	if err := nativewebp.EncodeAll(&buf, &ani, &nativewebp.Options{CompressionLevel: nativewebp.BestSpeed}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
