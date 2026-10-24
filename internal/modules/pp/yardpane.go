package pp

import (
	"fmt"
	"image"

	"github.com/6586x57890143/skua/internal/pixel"
)

// YardRow is one livery on a page of the dyers' yard.
type YardRow struct {
	Name  string
	Color uint32
	Price int64  // a week's lease, pp hundredths
	From  string // the perch it's worn from, when the member sits lower
	Left  int64  // days left on it when they wear it, else 0
	Worn  bool
}

// Livery is the livery a member wears, for the yard's corner: its name and
// colour and the days left on its lease, or nothing.
type Livery struct {
	Name  string
	Color uint32
	Left  int64
}

// YardScreen draws a page of the dyers' yard: each livery on it with a
// swatch of its cloth, its name in its own colour and its weekly price,
// whether it's worn or which perch it waits on; under it the purse and
// what the member wears now.
func YardScreen(title string, rows []YardRow, purse int64, wear Livery, lease string) ([]byte, error) {
	return yardCanvas(title, rows, purse, wear, lease).Screen(screenScale)
}

func yardCanvas(title string, rows []YardRow, purse int64, wear Livery, lease string) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	yard := image.Rect(4, 4, 380, 148)
	c.Window(yard)
	c.Tab(yard, title)
	step := 19
	if len(rows) > 0 {
		step = min(step, (yard.Max.Y-6-paneTop(yard))/len(rows))
	}
	x, r := 34, 368
	point := -1
	for i, row := range rows {
		if row.Worn {
			point = i
		}
	}
	for i, row := range rows {
		y := paneTop(yard) + i*step
		if i == point {
			c.Cursor(x-4, y+6)
		}
		swatch(c, image.Rect(x, y, x+14, y+11), row.Color)
		var at int
		switch {
		case row.Worn:
			at = right(c, r, y, leftWords(row.Left), pixel.Green)
		case row.From != "":
			at = rightIn(c, pixel.Light, r, y, "from the "+row.From, pixel.Dim)
		default:
			at = right(c, r, y, grouped(row.Price/unit)+" pp a "+or(lease, "week"), pixel.Gold)
		}
		c.FitIn(pixel.Bold, x+22, y, row.Name, c.Dye(row.Color), at-pixel.Advance-(x+22))
	}

	purseR := image.Rect(4, 152, 192, 212)
	c.Window(purseR)
	c.Tab(purseR, "purse")
	y := purseR.Min.Y + pixel.TabDrop + 3
	at := right(c, 180, y, grouped(purse/unit)+" pp", pixel.Gold)
	c.Icon("pot", image.Pt(at-pixel.IconCells-4, y-iconLift))
	label(c, 16, y, "you hold")
	label(c, 16, y+17, "a lease runs")
	right(c, 180, y+17, "a "+or(lease, "week"), pixel.Ink)

	you := image.Rect(196, 152, 380, 212)
	c.Window(you)
	c.Tab(you, "you wear")
	y = you.Min.Y + pixel.TabDrop + 3
	if wear.Name == "" {
		label(c, 208, y+8, "your own feathers")
		return c
	}
	swatch(c, image.Rect(208, y, 222, y+11), wear.Color)
	c.FitIn(pixel.Bold, 230, y, wear.Name, c.Dye(wear.Color), 368-230)
	ink := pixel.Ink
	if wear.Left <= 0 {
		ink = pixel.Red
	}
	c.Text(230, y+17, leftWords(wear.Left), ink, 0)
	return c
}

// leftWords is a lease's days left as a member reads them; a lease run out
// still fades for fadeDays.
func leftWords(left int64) string {
	switch {
	case left > 1:
		return fmt.Sprintf("%d days left", left)
	case left == 1:
		return "a day left"
	case left > -fadeDays:
		return "fading"
	}
	return "faded"
}

// swatch draws cloth in rgb over r: the dye with a lit edge along the top
// and its shade along the foot, a dark rule round it and, when it's big
// enough to show them, pleats hanging down it with the lit side of each
// fold to the left.
func swatch(c *pixel.Canvas, r image.Rectangle, rgb uint32) {
	lit, shade := c.Dye(pixel.Tint(rgb, 0xffffff, 30)), c.Dye(pixel.Tint(rgb, 0x000000, 28))
	c.Fill(r, c.Dye(rgb))
	if r.Dy() >= 24 {
		for x := r.Min.X + 6; x < r.Max.X-4; x += 12 {
			c.Fill(image.Rect(x, r.Min.Y, x+2, r.Max.Y), lit)
			c.Fill(image.Rect(x+6, r.Min.Y, x+9, r.Max.Y), shade)
		}
	}
	c.Fill(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+max(1, r.Dy()/8)), lit)
	c.Fill(image.Rect(r.Min.X, r.Max.Y-max(2, r.Dy()/6), r.Max.X, r.Max.Y), shade)
	c.Outline(r.Inset(-1), pixel.Line)
}
