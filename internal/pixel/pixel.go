// Package pixel draws skua's game screens the way a 90s RPG drew its menus:
// at a small native resolution in one fixed palette, then scaled up by
// whole pixels, so every cell is flat and the PNG stays small. A window is
// the classic one, an outline, a bone border and a sky gradient in bands,
// and text is the 7 by 13 face with a drop shadow.
package pixel

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/6586x57890143/skua/internal/brand"
)

// The UI's inks, as indexes into Palette.
var (
	Ink, Dim, Shadow, Bone, Gold, Green, Red, Ember uint8
	// Sky is a window's gradient, top band first.
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
	Ink = add(rgb(0xE6E2DA))    // bone, the text
	Dim = add(rgb(0x8A9CB2))    // brand.ColorInfo, the quieter text
	Shadow = add(rgb(0x1A1513)) // the line ink, outlines and drop shadows
	Bone = add(rgb(0xB9B4AA))   // the window's border
	Gold = add(rgb(0xD89B45))   // brand.ColorWarn: pp, the one warm ink
	Green = add(rgb(0x6E9A6A))  // brand.ColorOK
	Red = add(rgb(0xC04A3E))    // brand.ColorError
	Ember = add(rgb(0xA8744E))  // brand.ColorEnded
	// brand.ColorNotice down to brand.ColorPrimary, in even bands.
	top, foot := rgb(0x4F7C86), rgb(0x1E2228)
	for i := range Sky {
		mix := func(a, b uint8) uint8 { return uint8((int(a)*(len(Sky)-1-i) + int(b)*i) / (len(Sky) - 1)) }
		Sky[i] = add(color.NRGBA{mix(top.R, foot.R), mix(top.G, foot.G), mix(top.B, foot.B), 0xFF})
	}
	addIconInks()
}

// Canvas is a picture at native resolution, one pixel a cell.
type Canvas struct{ *image.Paletted }

// New is a w by h canvas, filled with the darkest sky.
func New(w, h int) *Canvas {
	c := &Canvas{image.NewPaletted(image.Rect(0, 0, w, h), Palette)}
	c.Fill(c.Rect, Sky[len(Sky)-1])
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

// Window draws the classic menu window over r: an outline, a bone border
// inside it, the sky in bands, and its corners cut round.
func (c *Canvas) Window(r image.Rectangle) {
	c.Fill(r, Shadow)
	c.Fill(r.Inset(1), Bone)
	in := r.Inset(2)
	for i := range Sky {
		band := in
		band.Min.Y = in.Min.Y + in.Dy()*i/len(Sky)
		band.Max.Y = in.Min.Y + in.Dy()*(i+1)/len(Sky)
		c.Fill(band, Sky[i])
	}
	for _, p := range []image.Point{r.Min, {r.Max.X - 1, r.Min.Y}, {r.Min.X, r.Max.Y - 1}, r.Max.Sub(image.Pt(1, 1))} {
		c.SetColorIndex(p.X, p.Y, Sky[len(Sky)-1])
	}
}

// face is the 7 by 13 pixel face.
var face = basicfont.Face7x13

// Advance is how wide one character of text is.
const Advance = 7

// LineHeight is how far apart lines of text sit.
const LineHeight = 13

// glyphs is printable ASCII in the face, one alpha mask a character.
var glyphs = sync.OnceValue(func() map[rune]*image.Alpha {
	m := map[rune]*image.Alpha{}
	for r := rune(0x21); r <= 0x7E; r++ {
		a := image.NewAlpha(image.Rect(0, 0, Advance, LineHeight))
		(&font.Drawer{Dst: a, Src: image.Opaque, Face: face, Dot: fixed.P(0, face.Ascent)}).DrawString(string(r))
		m[r] = a
	}
	return m
})

// Text sets s with its top left at x, y in ink i over a drop shadow, and
// returns where it ends. Past max characters it is cut with "..."; max 0
// means no limit.
func (c *Canvas) Text(x, y int, s string, i uint8, max int) int {
	s = Plain(s)
	if max > 0 && len(s) > max {
		s = strings.TrimSpace(s[:max-3]) + "..."
	}
	for n, r := range s {
		g := glyphs()[r]
		if g == nil {
			continue
		}
		at := x + n*Advance
		for gy := range LineHeight {
			for gx := range Advance {
				if g.AlphaAt(gx, gy).A < 0x80 {
					continue
				}
				if p := image.Pt(at+gx+1, y+gy+1); p.In(c.Rect) {
					c.SetColorIndex(p.X, p.Y, Shadow)
				}
				if p := image.Pt(at+gx, y+gy); p.In(c.Rect) {
					c.SetColorIndex(p.X, p.Y, i)
				}
			}
		}
	}
	return x + len(s)*Advance
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
// and the dark of the sky behind what's left.
func (c *Canvas) Bar(r image.Rectangle, n, d int64, i uint8) {
	c.Fill(r, Shadow)
	in := r.Inset(1)
	c.Fill(in, Sky[len(Sky)-1])
	if d <= 0 {
		return
	}
	n = min(max(n, 0), d)
	full := in
	full.Max.X = in.Min.X + int(int64(in.Dx())*n/d)
	c.Fill(full, i)
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
				c.SetColorIndex(x+dx, y+dy, Ink)
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
	big := image.NewPaletted(image.Rect(0, 0, w*scale, h*scale), Palette)
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
