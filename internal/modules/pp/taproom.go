package pp

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"math"
	"strconv"
	"sync"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The slack tide drawn: the taproom below the gannet's deck, where the
// purser's table runs down the middle under the deckhead's scraps of
// colour, tallow candles lean in the sconces on her ribs and the sand
// glass runs; and the hold, cut away, with the water against the painted
// waterline and the eels a finger under it. Each is a few frames: the
// candles flicker, the sand runs, the bones tumble and the eels move.

// The frames' beats, in milliseconds.
const (
	flickerBeat = 180
	tumbleBeat  = 110
)

// ink is an icon ink's index, for painting scenery in the icons' colours.
func ink(r byte) uint8 { return pixel.IconInk(r) }

// grid draws rows of icon inks at p, '.' clear: a small sprite.
func grid(c *pixel.Canvas, p image.Point, rows ...string) { c.Grid(rows, p) }

// flames are a tallow flame's shapes as the draught takes it; in the
// drowning hour every candle leans the same way with no wind to lean it,
// between the last two.
var flames = [][]string{
	{".O.", "ODO", "ODO", "NON", ".N."},
	{"O..", ".OO", "ODO", "NON", ".N."},
	{"..O", "OO.", "ODO", "NON", ".N."},
	{"O..", "OO.", "ODO", "NON", ".N."},
}

// The painted taproom's own places: where its two candles' wicks are and
// its sand glass's neck.
var (
	wicks    = []image.Point{{88, 53}, {219, 53}}
	glassAt  = image.Pt(358, 56)
	roomSize = image.Pt(384, 128)
)

//go:embed taproom.png
var taproomArt []byte

// taproomPic is the painted taproom, finished art: a ChatGPT painting of
// the deckhead, the ribs, the sconces and the glass, averaged onto the
// screen's grid and flattened to 40 colours. Replace it whole; don't edit
// its pixels.
var taproomPic = sync.OnceValue(func() image.Image {
	img, err := png.Decode(bytes.NewReader(taproomArt))
	if err != nil {
		panic("pp: the taproom won't decode: " + err.Error())
	}
	return img
})

// taproom draws the room behind a table, and in the drowning hour the
// chapel bell hung from the main beam between the middle benches. The
// flames and the sand go on after, a frame at a time.
func taproom(c *pixel.Canvas, bell bool) {
	c.Picture(taproomPic(), image.Point{}, 1)
	floor := c.ColorIndexAt(0, roomSize.Y-1)
	c.Fill(image.Rect(0, roomSize.Y, c.Rect.Dx(), 152), floor)
	if bell {
		x := c.Rect.Dx() / 2
		c.Fill(image.Rect(x-1, 33, x+1, 58), ink('p'))
		c.Fill(image.Rect(x, 33, x+1, 58), ink('q'))
		c.BigIcon("bell", image.Pt(x-16, 56), 2)
	}
}

// candles draws the taproom's flames at frame f: flickering, or in the
// drowning hour leaning.
func candles(c *pixel.Canvas, f int, bell bool) {
	for i, w := range wicks {
		shape := flames[(f+i)%3]
		if bell {
			shape = flames[1+2*((f+i)%2)]
		}
		grid(c, w.Sub(image.Pt(1, 0)), shape...)
	}
}

// sand runs the glass at frame f: a grain falling through its neck.
func sand(c *pixel.Canvas, f int) {
	grain := ink('r')
	for y := glassAt.Y + f%2; y < glassAt.Y+5; y += 2 {
		c.SetColorIndex(glassAt.X, y, grain)
	}
}

// tableTopAt draws the purser's table across the room, its top at y: an
// oak top lit along its edge, with the grain running its length, and a
// front of planks.
func tableTopAt(c *pixel.Canvas, y int) {
	w := c.Rect.Dx()
	c.Fill(image.Rect(6, y, w-6, y+1), ink('l'))
	c.Fill(image.Rect(6, y+1, w-6, y+8), ink('K'))
	// The grain: long dark streaks, staggered so no two line up.
	for i, x := 0, 10; x < w-30; i, x = i+1, x+37+(i*13)%23 {
		gy := y + 2 + (i*3)%5
		c.Fill(image.Rect(x, gy, x+18+(i*7)%15, gy+1), ink('J'))
	}
	c.Fill(image.Rect(6, y+7, w-6, y+8), ink('J'))
	c.Fill(image.Rect(6, y+8, w-6, y+15), ink('I'))
	for x := 6 + 47; x < w-6; x += 47 {
		c.Fill(image.Rect(x, y+8, x+1, y+15), ink('k'))
		c.SetColorIndex(x+2, y+11, ink('G'))
	}
	c.Fill(image.Rect(6, y+8, w-6, y+9), ink('J'))
	c.Fill(image.Rect(6, y+15, w-6, y+16), ink('k'))
	for _, x := range []int{14, w - 22} {
		c.Fill(image.Rect(x, y+16, x+8, 152), ink('I'))
		c.Fill(image.Rect(x, y+16, x+1, 152), ink('J'))
		c.Fill(image.Rect(x+7, y+16, x+8, 152), ink('k'))
	}
	c.Fill(image.Rect(0, 150, w, 152), ink('k'))
}

// chair draws an empty bench's ladder-back behind the table at cx.
func chair(c *pixel.Canvas, cx, top, bottom int) {
	for _, x := range []int{cx - 11, cx + 9} {
		c.Fill(image.Rect(x, top, x+3, bottom), ink('J'))
		c.Fill(image.Rect(x, top, x+1, bottom), ink('K'))
	}
	for y := top + 4; y < bottom-4; y += 8 {
		c.Fill(image.Rect(cx-8, y, cx+9, y+3), ink('K'))
		c.Fill(image.Rect(cx-8, y+2, cx+9, y+3), ink('I'))
	}
}

// bones are the two bones as they tumble, a frame each: where they are
// over a bird's head and which way up.
var tumble = []struct{ dx, dy int }{{-6, 8}, {-2, -6}, {3, -10}, {6, -2}}

// turned is g turned over.
func turned(g []string) []string {
	out := make([]string, len(g))
	for i, row := range g {
		b := []byte(row)
		for l, r := 0, len(b)-1; l < r; l, r = l+1, r-1 {
			b[l], b[r] = b[r], b[l]
		}
		out[i] = string(b)
	}
	return out
}

// Where a table's six benches stand: the middle of each, the portraits'
// tops and the table's top.
const (
	benchTop = 80
	tableTop = 120
)

func benchX(i int) int { return 32 + i*64 }

// stakeIcons are the stakes' icons, chalked over the bar.
var stakeIcons = []string{"wick", "candle", "lamp", "lantern"}

// stakeIcon is the icon for a stake of pp.
func stakeIcon(pp int64) string {
	for i, s := range stakes {
		if s.pp == pp {
			return stakeIcons[i]
		}
	}
	return "pot"
}

// tableFrames are a table's frames and their beats: the room with its
// birds and their stakes, the candles flickering and the glass running;
// once thrown, the bones tumbling over each bird first, then landing.
func tableFrames(t Table) ([]*pixel.Canvas, []int) {
	base := pixel.New(screenW, screenH)
	taproom(base, t.Bell)
	for i := range tableSeats {
		if i >= len(t.Seats) {
			chair(base, benchX(i), benchTop+12, tableTop)
			continue
		}
		base.Portrait(Portrait(t.Seats[i].Class), image.Pt(benchX(i)-16, benchTop), 1)
	}
	tableTopAt(base, tableTop)
	pool := base.Dye(0xD9A24E)
	for i, p := range t.Seats {
		x := benchX(i)
		if p.Stake == stakes[len(stakes)-1].pp {
			// A lantern lit on the table in front of them.
			base.Fill(image.Rect(x-12, tableTop+1, x+12, tableTop+7), pool)
		}
		base.Icon(stakeIcon(p.Stake), image.Pt(x-8, tableTop-9))
	}
	panel(base, t)

	draw := func(c *pixel.Canvas, f int) {
		candles(c, f, t.Bell)
		if !t.Thrown && len(t.Seats) > 0 {
			sand(c, f)
		}
	}
	var frames []*pixel.Canvas
	var beats []int
	if t.Thrown {
		for k, at := range tumble {
			c := base.Copy()
			draw(c, k)
			for i := range t.Seats {
				g := pixel.Icons["bones"]
				if k%2 == 1 {
					g = turned(g)
				}
				c.Grid(g, image.Pt(benchX(i)-8+at.dx, benchTop-24+at.dy))
			}
			frames, beats = append(frames, c), append(beats, tumbleBeat)
		}
		landed(base, t)
		chalk(base, t.Seats[t.best()].Name)
	}
	for f := range 6 {
		c := base.Copy()
		draw(c, f)
		frames, beats = append(frames, c), append(beats, flickerBeat)
	}
	return frames, beats
}

// landed draws each bird's throw over their head once the bones land:
// gilt for a win, bone for a loss.
func landed(c *pixel.Canvas, t Table) {
	for i, p := range t.Seats {
		s := strconv.Itoa(p.Throw)
		ink := pixel.Bone
		if p.Won > 0 {
			ink = pixel.Gilt
		}
		w := pixel.Bold.Width(s)
		x := benchX(i) - w/2
		// A slate, so the number reads over the candles behind it.
		plate := image.Rect(x-4, benchTop-19, x+w+4, benchTop-2)
		c.Fill(plate, pixel.Line)
		c.Outline(plate, pixel.Shadow)
		c.Print(pixel.Bold, x, benchTop-17, s, ink)
	}
}

// chalk writes the beam's new name in chalk on the main beam.
func chalk(c *pixel.Canvas, name string) {
	name = pixel.Plain(name)
	for pixel.Regular.Width(name) > 160 && len(name) > 1 {
		name = name[:len(name)-1]
	}
	c.Text(c.Rect.Dx()/2-pixel.Regular.Width(name)/2, 17, name, pixel.Glove, 0)
}

// panel is the parchment under the table: each bench's bird, their stake
// and, once thrown, what the bones paid.
func panel(c *pixel.Canvas, t Table) {
	r := image.Rect(4, 152, screenW-4, screenH-4)
	c.Window(r)
	tab := "the purser's table"
	if t.Bell {
		tab = "the drowning hour"
	}
	c.Tab(r, tab)
	y := paneTop(r) - 2
	for i := range tableSeats {
		x := benchX(i)
		if i >= len(t.Seats) {
			s := "empty"
			c.Print(pixel.Light, x-pixel.Light.Width(s)/2, y+7, s, pixel.Dim)
			continue
		}
		p := t.Seats[i]
		name := []rune(pixel.Plain(p.Name))
		for pixel.Bold.Width(string(name)) > 56 && len(name) > 1 {
			name = name[:len(name)-1]
		}
		at := max(x-pixel.Bold.Width(string(name))/2, r.Min.X+8)
		c.Print(pixel.Bold, at, y, string(name), pixel.Ink)
		line, ink := grouped(p.Stake/unit)+" pp", pixel.Gold
		if t.Thrown {
			line, ink = "lost", pixel.Red
			if p.Won > 0 {
				line, ink = "+"+grouped(p.Won/unit), pixel.Green
			}
		}
		c.Print(pixel.Regular, x-pixel.Regular.Width(line)/2, y+14, line, ink)
	}
}

// holdFrames are the slack tide's screen: the hold cut away on the left,
// its water against the line and the eels under it, rippling a frame at a
// time; the hold's figures, the stakes and the bird's day on the right.
func holdFrames(h House) ([]*pixel.Canvas, []int) {
	base := pixel.New(screenW, screenH)
	pic := image.Rect(4, 4, 190, 212)
	base.Fill(pic, pixel.Shadow)
	base.Fill(pic.Inset(1), pixel.Bone)
	base.Fill(pic.Inset(3), pixel.Line)
	in := pic.Inset(4)
	night := base.Dye(0x141A22)
	base.Fill(in, night)

	// The hull, cut across: a bowl of planks on its ribs.
	cx, rx, top, bottom := (in.Min.X+in.Max.X)/2, 74, in.Min.Y+34, in.Max.Y-10
	floor := func(x int) int {
		d := float64(x-cx) / float64(rx)
		if d*d >= 1 {
			return top
		}
		return top + int(float64(bottom-top)*math.Sqrt(1-d*d))
	}
	inside := base.Dye(0x24160C)
	for x := cx - rx; x <= cx+rx; x++ {
		f := floor(x)
		base.Fill(image.Rect(x, top, x+1, f), inside)
		base.Fill(image.Rect(x, f, x+1, f+6), ink('J'))
		base.Fill(image.Rect(x, f+6, x+1, f+7), ink('k'))
	}
	for x := cx - rx + 18; x < cx+rx; x += 18 {
		base.Fill(image.Rect(x, top, x+2, floor(x)), ink('I'))
	}
	// The deck across her top, the pumps on it.
	base.Fill(image.Rect(cx-rx-4, top-6, cx+rx+5, top), ink('K'))
	base.Fill(image.Rect(cx-rx-4, top-1, cx+rx+5, top), ink('k'))
	grid(base, image.Pt(cx+rx-26, top-22),
		"....kk....",
		"..kkGGkk..",
		"..kHGGFk..",
		"...kGFk...",
		"...kGFk...",
		"...kGFk...",
		"..kGGFFk..",
		"..kGGFFk..",
		".kGGGFFFk.",
		".kkkkkkkk.",
	)

	// The water, as high as the hold is full: the painted line is full.
	depth := bottom - top - 8
	line := bottom - depth
	level := bottom - int(int64(depth)*min(h.Hold, h.Line)/max(h.Line, 1))
	sea, crest := ink('T'), ink('U')
	for x := cx - rx; x <= cx+rx; x++ {
		if f := floor(x); f > level {
			base.Fill(image.Rect(x, level, x+1, f), sea)
		}
	}
	red := ink('M')
	for x := cx - rx + 6; x < cx+rx-6; x += 6 {
		if floor(x) > line+1 && floor(x+3) > line+1 {
			base.Fill(image.Rect(x, line, x+4, line+1), red)
		}
	}
	base.Print(pixel.Light, cx-rx+8, line+10, "the line", pixel.Bone)

	base.Print(pixel.Regular, in.Min.X+6, in.Min.Y+4, tideWord(h), pixel.Glove)
	if h.Bell {
		base.Print(pixel.Light, in.Min.X+6, in.Min.Y+18, "the bell rings", pixel.Gilt)
	}

	figures(base, h)

	var frames []*pixel.Canvas
	var beats []int
	for f := range 4 {
		c := base.Copy()
		// The surface catches the light a little further along each frame.
		for x := cx - rx; x <= cx+rx; x++ {
			if floor(x) > level+1 && (x+f*2)%8 < 3 {
				c.SetColorIndex(x, level, crest)
			}
		}
		// The eels keep a finger under the line, wherever the water is.
		for i, ex := range []int{cx - 40, cx + 6, cx + 44} {
			ey := line + 5 + (i % 2)
			if ey <= level || floor(ex) <= ey+2 {
				continue
			}
			wave := []string{"w.w.w.", ".r.r.r"}
			if (f+i)%2 == 1 {
				wave = []string{".w.w.w", "r.r.r."}
			}
			for dy, row := range wave {
				for dx := range len(row) {
					if row[dx] != '.' {
						c.SetColorIndex(ex+dx, ey+dy, ink(row[dx]))
					}
				}
			}
		}
		frames, beats = append(frames, c), append(beats, flickerBeat*2)
	}
	return frames, beats
}

// figures are the slack tide screen's windows: the hold, the stakes and
// the bird's day at the tables.
func figures(c *pixel.Canvas, h House) {
	hold := image.Rect(194, 4, 380, 86)
	c.Window(hold)
	c.Tab(hold, "the hold")
	y := paneTop(hold)
	label(c, hold.Min.X+12, y, "water")
	right(c, hold.Max.X-12, y, grouped(h.Hold/unit)+" pp", pixel.Gold)
	label(c, hold.Min.X+12, y+19, "the line")
	right(c, hold.Max.X-12, y+19, grouped(h.Line/unit)+" pp", pixel.Ink)
	c.Bar(image.Rect(hold.Min.X+12, y+38, hold.Max.X-12, y+44), min(h.Hold, h.Line), h.Line, pixel.Blue)

	st := image.Rect(194, 90, 380, 212)
	c.Window(st)
	c.Tab(st, "the stakes")
	y = paneTop(st)
	for i, s := range stakes {
		yy := y + i*17
		ink, word := pixel.Ink, grouped(s.pp/unit)+" pp"
		if s.pp > h.MaxStake {
			ink, word = pixel.Dim, "tide's out"
		}
		c.Icon(stakeIcons[i], image.Pt(st.Min.X+10, yy-iconLift))
		label(c, st.Min.X+10+pixel.IconCells+4, yy, s.name)
		right(c, st.Max.X-12, yy, word, ink)
	}
	yy := y + 4*17 + 2
	label(c, st.Min.X+12, yy, "staked today")
	right(c, st.Max.X-12, yy, grouped(h.Staked/unit)+" of "+grouped(h.Cap/unit), pixel.Ink)
}
