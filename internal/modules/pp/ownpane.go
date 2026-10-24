package pp

import (
	"fmt"
	"image"
	"strings"

	"github.com/6586x57890143/skua/internal/pixel"
)

// Own is a petition for a livery of one's own as its screen shows it.
type Own struct {
	Name   string // "" before one is written
	Style  string
	Colors []uint32 // one, or the gradient's stops
	Icon   image.Image
	Week   int64 // pp hundredths
	Fee    int64
	Purse  int64
	// Letter is the heralds' last word, "" for none; Wearing is whether the
	// member wears a livery of their own now, with Left days on its lease.
	Letter  string
	Wearing bool
	Left    int64
	// Filled and Slots are how full the heralds' roll is, and Term how
	// long a lease runs, "week" or "season".
	Filled, Slots int64
	Term          string
}

// The two themes a name is shown on: Discord's dark member list and its
// light one.
const darkTheme, lightTheme = 0x2b2d31, 0xf2f3f5

// OwnScreen draws a petition: the name as Discord will show it, on the
// dark theme and the light, with its device; what it's woven as; and what
// it costs.
func OwnScreen(o Own) ([]byte, error) { return ownCanvas(o).Screen(screenScale) }

func ownCanvas(o Own) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	show := image.Rect(4, 4, 380, 120)
	c.Window(show)
	c.Tab(show, "a livery of your own")
	if o.Name == "" {
		mid := paneTop(show) + 30
		label(c, 18, mid, "no petition written yet. write one,")
		label(c, 18, mid+15, "and the dyers will show it to you here")
	} else {
		for i, theme := range []uint32{darkTheme, lightTheme} {
			plate := image.Rect(14, paneTop(show)+i*40-4, 370, paneTop(show)+i*40+30)
			c.Ground(plate, c.Dye(theme))
			x := 22
			if o.Icon != nil {
				x += 20
			}
			dyed(c, x, plate.Min.Y+10, o.Name, o.Colors, plate.Max.X-8-x)
		}
		// The device last: a photo can fill the palette, and the name's
		// inks and the plates are drawn true before it does.
		if o.Icon != nil {
			for i, theme := range []uint32{darkTheme, lightTheme} {
				c.Picture(shrink(o.Icon, 16, theme), image.Pt(22, paneTop(show)+i*40+5), 1)
			}
		}
	}
	// Under the plates: the heralds' word and the lease on what's worn.
	foot := show.Max.Y - 19
	if o.Letter != "" {
		ink := pixel.Dim
		if strings.HasPrefix(o.Letter, "refused") {
			ink = pixel.Red
		}
		c.FitIn(pixel.Light, 18, foot, "the heralds: "+o.Letter, ink, 230)
	} else if o.Slots > 0 {
		label(c, 18, foot, fmt.Sprintf("the heralds' roll: %d of %d names", o.Filled, o.Slots))
	}
	if o.Wearing {
		right(c, 368, foot, "worn · "+leftWords(o.Left), pixel.Green)
	}

	weave := image.Rect(4, 124, 192, 212)
	c.Window(weave)
	c.Tab(weave, "the petition")
	y := paneTop(weave)
	label(c, 16, y, "weave")
	right(c, 180, y, or(o.Style, "none yet"), pixel.Ink)
	label(c, 16, y+17, "colours")
	for i, col := range o.Colors {
		at := 180 - (len(o.Colors)-i)*18 + 4
		swatch(c, image.Rect(at, y+17, at+14, y+28), col)
	}
	label(c, 16, y+34, "device")
	dev := "none"
	if o.Icon != nil {
		dev = "sewn on"
	}
	right(c, 180, y+34, dev, pixel.Ink)

	price := image.Rect(196, 124, 380, 212)
	c.Window(price)
	c.Tab(price, "price")
	y = paneTop(price)
	label(c, 208, y, "a "+or(o.Term, "week"))
	week := "-"
	if o.Week > 0 {
		week = grouped(o.Week/unit) + " pp"
	}
	right(c, 368, y, week, pixel.Gold)
	label(c, 208, y+17, "the heralds' fee")
	right(c, 368, y+17, grouped(o.Fee/unit)+" pp", pixel.Gold)
	label(c, 208, y+34, "you hold")
	right(c, 368, y+34, grouped(o.Purse/unit)+" pp", pixel.Ink)
	return c
}

// dyed sets s in Bold at x, y, its letters shading from one of colors to
// the next across its width, as Discord draws a gradient role's name, and
// cut to width.
func dyed(c *pixel.Canvas, x, y int, s string, colors []uint32, width int) {
	if len(colors) == 0 {
		return
	}
	stand := c.Dye(0x010203)
	end := c.FitIn(pixel.Bold, x, y, s, stand, width)
	span := max(1, end-x)
	// A step a few columns wide, so a long gradient costs a few inks.
	const step = 4
	for cx := x; cx < end; cx++ {
		at := float64((cx-x)/step*step) / float64(span)
		ink := c.Dye(along(colors, at))
		for cy := y - 2; cy < y+pixel.LineHeight+2; cy++ {
			if c.ColorIndexAt(cx, cy) == stand {
				c.SetColorIndex(cx, cy, ink)
			}
		}
	}
}

// along is the colour a share t of the way through colors.
func along(colors []uint32, t float64) uint32 {
	if len(colors) == 1 {
		return colors[0]
	}
	t = min(max(t, 0), 1) * float64(len(colors)-1)
	i := min(int(t), len(colors)-2)
	return pixel.Tint(colors[i], colors[i+1], uint32((t-float64(i))*100))
}

// shrink is img at most side square over bg, a pixel's alpha blended into
// it: the canvas has no alpha.
func shrink(img image.Image, side int, bg uint32) image.Image {
	out := resize(img, side)
	br, bgg, bb := bg>>16&0xff, bg>>8&0xff, bg&0xff
	for i := 0; i < len(out.Pix); i += 4 {
		a := uint32(out.Pix[i+3])
		mixed := func(v, under uint32) uint8 { return uint8((v*a + under*(255-a)) / 255) }
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = mixed(uint32(out.Pix[i]), br), mixed(uint32(out.Pix[i+1]), bgg), mixed(uint32(out.Pix[i+2]), bb), 255
	}
	return out
}
