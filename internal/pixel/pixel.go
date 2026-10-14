// Package pixel draws skua's game screens the way a 90s RPG drew its menus:
// at a small native resolution in one fixed palette, then scaled up by
// whole pixels, so every cell is flat and the PNG stays small. The look is
// Tactics Ogre's: a window is flat parchment inside a khaki frame with a
// dark rule, text is dark in a hand-drawn slab serif over a khaki shadow,
// gauges are thin blue and red bars, and labels sit on dark tabs.
package pixel

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strings"

	"github.com/6586x57890143/skua/internal/brand"
)

// The UI's inks, as indexes into Palette.
var (
	Ink, Dim, Shadow, Bone, Gold, Green, Red, Ember uint8
	// Line is the dark rule inside a window's frame, the backdrop behind
	// the windows and a tab's ground; Glove is the cursor's white and a
	// tab's letters.
	Line, Glove uint8
	// Yellow, Blue and Crimson are the gauges, after Tactics Ogre's, each
	// lit along its top row and shaded along its foot.
	Yellow, Blue, Crimson uint8
	// lit and shade are each gauge ink's top and foot rows.
	lit, shade = map[uint8]uint8{}, map[uint8]uint8{}
	// Sky is a window's parchment, top band first: all but flat.
	Sky [6]uint8
)

// Palette is the bird's own colours and the UI's after them.
var Palette color.Palette

// bird is skua's avatar, 128 px at one px a cell.
var bird *image.Paletted

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xFF}
}

func init() {
	img, err := png.Decode(bytes.NewReader(brand.Art("skua_avatar")))
	if err != nil {
		panic("pixel: the avatar won't decode: " + err.Error())
	}
	bird = img.(*image.Paletted)
	Palette = append(color.Palette{}, bird.Palette...)
	add := func(c color.NRGBA) uint8 {
		Palette = append(Palette, c)
		return uint8(len(Palette) - 1)
	}
	Ink = add(rgb(0x333329))    // the text, dark on the parchment
	Dim = add(rgb(0x7A735C))    // the quieter text
	Shadow = add(rgb(0x1A1A12)) // the outline
	Bone = add(rgb(0xACA68A))   // khaki: the trim and the text's shadow
	Gold = add(rgb(0xA8701E))   // pp, amber dark enough for parchment
	Green = add(rgb(0x5E6E2A))  // olive
	Red = add(rgb(0x9A3A26))    // rust
	Ember = add(rgb(0xA8744E))  // brand.ColorEnded
	Line = add(rgb(0x4D4B3C))
	Glove = add(rgb(0xF2EEE2))
	gauge := func(top, mid, foot uint32) uint8 {
		i := add(rgb(mid))
		lit[i], shade[i] = add(rgb(top)), add(rgb(foot))
		return i
	}
	Yellow = gauge(0xF2D26A, 0xD8A83E, 0xA0742A)
	Blue = gauge(0x8E9DE5, 0x838ABE, 0x6C7496)
	Crimson = gauge(0xE6684C, 0xC36147, 0x9E533A)
	// Parchment, all but flat: Tactics Ogre's windows have no gradient.
	top, foot := rgb(0xDAD2B2), rgb(0xD0C8A7)
	for i := range Sky {
		mix := func(a, b uint8) uint8 { return uint8((int(a)*(len(Sky)-1-i) + int(b)*i) / (len(Sky) - 1)) }
		Sky[i] = add(color.NRGBA{mix(top.R, foot.R), mix(top.G, foot.G), mix(top.B, foot.B), 0xFF})
	}
	addIconInks()
}

// Canvas is a picture at native resolution, one pixel a cell.
type Canvas struct{ *image.Paletted }

// New is a w by h canvas over the dark backdrop.
func New(w, h int) *Canvas {
	c := &Canvas{image.NewPaletted(image.Rect(0, 0, w, h), Palette)}
	c.Fill(c.Rect, Line)
	return c
}

// Fill paints r in ink i.
func (c *Canvas) Fill(r image.Rectangle, i uint8) {
	r = r.Intersect(c.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := c.Pix[c.PixOffset(r.Min.X, y):c.PixOffset(r.Max.X, y)]
		for x := range row {
			row[x] = i
		}
	}
}

// Window draws a menu window over r: an outline, a khaki frame inside it
// and a dark rule inside that, the parchment, and its corners cut round.
func (c *Canvas) Window(r image.Rectangle) {
	c.Fill(r, Shadow)
	c.Fill(r.Inset(1), Bone)
	c.Fill(r.Inset(3), Line)
	in := r.Inset(4)
	for i := range Sky {
		band := in
		band.Min.Y = in.Min.Y + in.Dy()*i/len(Sky)
		band.Max.Y = in.Min.Y + in.Dy()*(i+1)/len(Sky)
		c.Fill(band, Sky[i])
	}
	for _, p := range []image.Point{r.Min, {r.Max.X - 1, r.Min.Y}, {r.Min.X, r.Max.Y - 1}, r.Max.Sub(image.Pt(1, 1))} {
		c.SetColorIndex(p.X, p.Y, Line)
	}
}

// Advance is how wide one character of text is.
const Advance = 7

// LineHeight is how far apart lines of text sit.
const LineHeight = 13

// ink reports whether the face's glyph for r has ink at gx, gy of its cell.
func ink(r rune, gx, gy int) bool {
	g, ok := face[r]
	if !ok || gy < g.top || gy >= g.top+len(g.rows) {
		return false
	}
	row := g.rows[gy-g.top]
	return gx < len(row) && row[gx] == '#'
}

// Text sets s with its top left at x, y in ink i over a drop shadow, and
// returns where it ends. Past max characters it is cut with "..."; max 0
// means no limit.
func (c *Canvas) Text(x, y int, s string, i uint8, max int) int {
	return c.text(x, y, s, i, Bone, max, Advance)
}

// Shaded sets s like Text, over a shadow in ink shadow: for text on a
// dark ground, where the khaki shadow would glare.
func (c *Canvas) Shaded(x, y int, s string, i, shadow uint8) int {
	return c.text(x, y, s, i, shadow, 0, Advance)
}

// Fit sets s like Text within width: at full spacing if it fits, a cell
// tighter if that fits, and cut with "..." at the tighter spacing only
// past that.
func (c *Canvas) Fit(x, y int, s string, i uint8, width int) int {
	n := len(Plain(s))
	if n*Advance <= width {
		return c.Text(x, y, s, i, 0)
	}
	return c.text(x, y, s, i, Bone, width/(Advance-1), Advance-1)
}

// text sets s in ink i over a shadow in ink shadow, or none if shadow is
// the same as i, adv cells a character.
func (c *Canvas) text(x, y int, s string, i, shadow uint8, max, adv int) int {
	s = Plain(s)
	if max > 0 && len(s) > max {
		s = strings.TrimSpace(s[:max-3]) + "..."
	}
	for n, r := range s {
		at := x + n*adv
		for gy := range LineHeight {
			for gx := range Advance {
				if !ink(r, gx, gy) {
					continue
				}
				if p := image.Pt(at+gx+1, y+gy+1); shadow != i && p.In(c.Rect) && !ink(r, gx+1, gy+1) {
					c.SetColorIndex(p.X, p.Y, shadow)
				}
				if p := image.Pt(at+gx, y+gy); p.In(c.Rect) {
					c.SetColorIndex(p.X, p.Y, i)
				}
			}
		}
	}
	return x + len(s)*adv
}

// Plain is s in what the face has, printable ASCII, on one line.
func Plain(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7E {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// Bar draws a gauge over r filled n of d in ink i: an outline, the fill,
// and the dark behind what's left. A gauge ink's fill is lit along its top
// row and shaded along its foot, as Tactics Ogre's are; 5 rows tall is one.
func (c *Canvas) Bar(r image.Rectangle, n, d int64, i uint8) {
	c.Fill(r, Shadow)
	in := r.Inset(1)
	c.Fill(in, Line)
	if d <= 0 {
		return
	}
	n = min(max(n, 0), d)
	full := in
	full.Max.X = in.Min.X + int(int64(in.Dx())*n/d)
	c.Fill(full, i)
	if l, ok := lit[i]; ok {
		top, foot := full, full
		top.Max.Y, foot.Min.Y = full.Min.Y+1, full.Max.Y-1
		c.Fill(top, l)
		c.Fill(foot, shade[i])
	}
}

// Tab sets s on a dark tab inside the top left of the window over r, its
// frame running into the window's, as Tactics Ogre names its panels. What
// the window holds starts TabDrop below its top.
func (c *Canvas) Tab(r image.Rectangle, s string) {
	w := len(Plain(s))*Advance + 5
	tab := image.Rect(r.Min.X+4, r.Min.Y, r.Min.X+4+w, r.Min.Y+TabDrop)
	c.Fill(tab, Shadow)
	c.Fill(tab.Inset(1), Line)
	c.text(tab.Min.X+3, tab.Min.Y+1, s, Glove, Glove, 0, Advance)
}

// TabDrop is how far a tab reaches down into its window.
const TabDrop = 15

// Portrait draws img scale times its size with its top left at p, in a
// frame like a window's around it. Its colours join this canvas's palette,
// so a portrait keeps its own sixteen.
func (c *Canvas) Portrait(img image.Image, p image.Point, scale int) {
	frame := image.Rectangle{p, p.Add(img.Bounds().Size().Mul(scale))}.Inset(-3)
	c.Fill(frame, Shadow)
	c.Fill(frame.Inset(1), Bone)
	c.Fill(frame.Inset(2), Line)
	c.Picture(img, p, scale)
}

// Picture draws img scale times its size with its top left at p, keeping
// its own colours: each joins this canvas's palette while there's room,
// and past that takes the nearest ink there.
func (c *Canvas) Picture(img image.Image, p image.Point, scale int) {
	b := img.Bounds()
	c.Palette = slices.Clip(c.Palette)
	known := map[[4]uint32]uint8{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			col := img.At(x, y)
			r, g, bl, a := col.RGBA()
			i, ok := known[[4]uint32{r, g, bl, a}]
			if !ok {
				at := slices.IndexFunc(c.Palette, func(have color.Color) bool { return same(have, col) })
				if at < 0 && len(c.Palette) < 256 {
					c.Palette = append(c.Palette, col)
					at = len(c.Palette) - 1
				}
				if at < 0 {
					at = c.Palette.Index(col)
				}
				i = uint8(at)
				known[[4]uint32{r, g, bl, a}] = i
			}
			q := p.Add(image.Pt(x-b.Min.X, y-b.Min.Y).Mul(scale))
			c.Fill(image.Rectangle{q, q.Add(image.Pt(scale, scale))}, i)
		}
	}
}

// same reports whether two colours are the same colour.
func same(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// Bird draws skua's avatar with its top left at p.
func (c *Canvas) Bird(p image.Point) {
	c.Sprite(bird, p)
}

// Sprite draws img with its top left at p, mapping each colour to the
// nearest ink and leaving what's clear.
func (c *Canvas) Sprite(img image.Image, p image.Point) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			col := img.At(x, y)
			if _, _, _, a := col.RGBA(); a < 0x8000 {
				continue
			}
			if q := image.Pt(p.X+x-b.Min.X, p.Y+y-b.Min.Y); q.In(c.Rect) {
				c.SetColorIndex(q.X, q.Y, uint8(Palette.Index(col)))
			}
		}
	}
}

// hand is the menu cursor, a gloved hand pointing right: w is the glove,
// # its outline.
var hand = []string{
	".#####.....",
	"#wwwww#####",
	"#wwwwwwwww#",
	"#wwww######",
	"#wwwww#....",
	"#wwww#.....",
	".####......",
}

// Cursor draws the hand with its tip's row at y, pointing at x.
func (c *Canvas) Cursor(x, y int) {
	x -= len(hand[0])
	y -= len(hand) / 2
	for dy, row := range hand {
		for dx, ch := range row {
			switch ch {
			case 'w':
				c.SetColorIndex(x+dx, y+dy, Glove)
			case '#':
				c.SetColorIndex(x+dx, y+dy, Shadow)
			}
		}
	}
}

// PNG is the canvas scale times larger, written indexed. Default
// compression, not best: a status screen is drawn per request, and best
// took 11 ms a screen to save 2 KB where default takes under 2.
func (c *Canvas) PNG(scale int) ([]byte, error) {
	w, h := c.Rect.Dx(), c.Rect.Dy()
	big := image.NewPaletted(image.Rect(0, 0, w*scale, h*scale), c.Palette)
	for y := range h {
		src := c.Pix[c.PixOffset(0, y):c.PixOffset(w, y)]
		row := big.Pix[big.PixOffset(0, y*scale):big.PixOffset(w*scale, y*scale)]
		for x, i := range src {
			for k := range scale {
				row[x*scale+k] = i
			}
		}
		for k := 1; k < scale; k++ {
			copy(big.Pix[big.PixOffset(0, y*scale+k):], row)
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, big); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
