package pixel

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"slices"
	"sync"
	"sync/atomic"

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
	if Draft.Load() {
		return draft, nil
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, c.SoftImage(scale), &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Draft makes Soft and Screen skip the softening and the encoding and
// return a fixed placeholder. Packages whose tests only press buttons set it
// in TestMain: under -race a real screen costs seconds, and they never look
// at the picture.
var Draft atomic.Bool

var draft = []byte("draft screen")

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
	if Draft.Load() {
		return draft, nil
	}
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
	// Each nudge is softened once, side by side: the first whole, the rest
	// only in the box.
	var nudges []int
	for _, b := range Bob {
		if !slices.Contains(nudges, b) {
			nudges = append(nudges, b)
		}
	}
	soft := make([]*image.RGBA, len(nudges))
	var wg sync.WaitGroup
	for k, b := range nudges {
		wg.Go(func() {
			if k == 0 {
				soft[k] = c.frame(b).softIn(scale, image.Rect(0, 0, c.Rect.Dx()*scale, c.Rect.Dy()*scale))
			} else {
				soft[k] = c.frame(b).softIn(scale, out)
			}
		})
	}
	wg.Wait()
	ani := nativewebp.Animation{}
	for i, b := range Bob {
		var img image.Image = soft[slices.Index(nudges, b)]
		if i > 0 && b == Bob[0] {
			img = soft[0].SubImage(out)
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

// Animate is frames as one moving screen, each shown for its beat in
// milliseconds: the first whole, and each after it only the box where it
// differs from the one before, softened on its own, so a scene that moves
// in a few places costs little more than a still one. Frames are the same
// size. A run of frames that never changes is no animation, and goes out
// still: Discord breaks on a one-frame WebP.
func Animate(frames []*Canvas, beats []int, scale int) ([]byte, error) {
	if Draft.Load() {
		return draft, nil
	}
	full := image.Rect(0, 0, frames[0].Rect.Dx()*scale, frames[0].Rect.Dy()*scale)
	type part struct {
		f    *Canvas
		box  image.Rectangle
		beat int
	}
	parts := []part{{frames[0], full, beats[0]}}
	for i := 1; i < len(frames); i++ {
		d := changed(frames[i-1], frames[i])
		if d.Empty() {
			parts[len(parts)-1].beat += beats[i]
			continue
		}
		d = d.Inset(-1)
		box := image.Rect(d.Min.X*scale&^1, d.Min.Y*scale&^1, d.Max.X*scale, d.Max.Y*scale).Intersect(full)
		parts = append(parts, part{frames[i], box, beats[i]})
	}
	if len(parts) == 1 {
		return frames[0].Soft(scale)
	}
	imgs := make([]*image.RGBA, len(parts))
	var wg sync.WaitGroup
	for k, p := range parts {
		wg.Go(func() { imgs[k] = p.f.softIn(scale, p.box) })
	}
	wg.Wait()
	ani := nativewebp.Animation{}
	for k, p := range parts {
		ani.Images = append(ani.Images, imgs[k])
		ani.Durations = append(ani.Durations, uint(p.beat))
		ani.Disposals = append(ani.Disposals, 0)
	}
	var buf bytes.Buffer
	if err := nativewebp.EncodeAll(&buf, &ani, &nativewebp.Options{CompressionLevel: nativewebp.BestSpeed}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// changed is the box of cells where b differs from a.
func changed(a, b *Canvas) image.Rectangle {
	var r image.Rectangle
	w := a.Rect.Dx()
	for i := range a.Pix {
		if a.Palette[a.Pix[i]] != b.Palette[b.Pix[i]] {
			p := image.Pt(i%w, i/w)
			r = r.Union(image.Rectangle{p, p.Add(image.Pt(1, 1))})
		}
	}
	return r
}

// Copy is a copy of c to draw the next frame on.
func (c *Canvas) Copy() *Canvas {
	return &Canvas{Paletted: &image.Paletted{Pix: slices.Clone(c.Pix), Stride: c.Stride, Rect: c.Rect, Palette: slices.Clone(c.Palette)}}
}
