package pp

import (
	"image"
	"math"
	"strconv"

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

// flames are a tallow flame's three shapes as the draught takes it.
var flames = [][]string{
	{".O.", "ODO", "ODO", "NON", ".N."},
	{"O..", ".OO", "ODO", "NON", ".N."},
	{"..O", "OO.", "ODO", "NON", ".N."},
}

// candle draws a tallow candle in a sconce on a rib, its flame at shape f,
// with the warm light it throws on the planks round it.
func candle(c *pixel.Canvas, x, y, f int, glow uint8) {
	r := 9 + f%2
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				px, py := x+dx, y-2+dy
				if image.Pt(px, py).In(c.Rect) && c.ColorIndexAt(px, py) == ink('I') {
					c.SetColorIndex(px, py, glow)
				}
			}
		}
	}
	grid(c, image.Pt(x-1, y-5), flames[f%len(flames)]...)
	grid(c, image.Pt(x-2, y), "krrk", "krqk", "kwqk", "krqk", "krpk")
	grid(c, image.Pt(x-4, y+5), "kGGGGGGk", ".kFFFFk.", "...kk...", "...kk...")
}

// scraps are the colours nailed to the deckhead over the benches: where
// each is and its ink.
var scraps = []struct {
	x, y int
	ink  byte
}{
	{14, 6, 'N'}, {41, 11, 'R'}, {70, 4, 'V'}, {103, 9, 'Z'}, {131, 5, 'C'}, {159, 12, 'T'},
	{222, 6, 'M'}, {248, 11, 'Y'}, {276, 4, 'Q'}, {303, 10, 'O'}, {334, 6, 'U'}, {361, 11, 'X'},
}

// taproom draws the room behind a table: the hull's planks and ribs, the
// deckhead with its scraps and, in the drowning hour, the bell hung under
// it. Candles go on after, a frame at a time.
func taproom(c *pixel.Canvas, bell bool) {
	w := c.Rect.Dx()
	seam := c.Dye(0x2E1C10)
	c.Fill(image.Rect(0, 22, w, 152), ink('I'))
	for y, row := 28, 0; y < 152; y, row = y+8, row+1 {
		c.Fill(image.Rect(0, y, w, y+1), seam)
		for x := (row % 2) * 37; x < w; x += 74 {
			c.Fill(image.Rect(x, y-7, x+1, y), seam)
		}
	}
	// The deckhead, its beams and its scraps.
	c.Fill(image.Rect(0, 0, w, 22), ink('J'))
	for x := 0; x < w; x += 48 {
		c.Fill(image.Rect(x, 0, x+3, 22), ink('K'))
		c.Fill(image.Rect(x+3, 0, x+4, 22), ink('I'))
	}
	c.Fill(image.Rect(0, 19, w, 22), ink('I'))
	c.Fill(image.Rect(0, 22, w, 23), ink('k'))
	for _, s := range scraps {
		c.Fill(image.Rect(s.x, s.y, s.x+7, s.y+5), ink(s.ink))
		c.Fill(image.Rect(s.x, s.y+5, s.x+7, s.y+6), ink('k'))
		c.SetColorIndex(s.x+3, s.y+1, ink('k'))
	}
	// The ribs, between the benches.
	for x := 64; x < w; x += 64 {
		c.Fill(image.Rect(x-3, 23, x+3, 152), ink('J'))
		c.Fill(image.Rect(x-3, 23, x-2, 152), ink('K'))
		c.Fill(image.Rect(x+2, 23, x+3, 152), ink('k'))
	}
	if bell {
		// The chapel bell's pieces, hung from the deckhead, and the sea
		// come up green between the planks.
		sea := c.Dye(0x1F3A42)
		for y := 28; y < 152; y += 8 {
			c.Fill(image.Rect(0, y, w, y+1), sea)
		}
		c.Fill(image.Rect(w/2-1, 23, w/2+1, 30), ink('k'))
		c.BigIcon("bell", image.Pt(w/2-16, 28), 2)
	}
}

// tableTopAt draws the purser's table across the room, its top at y.
func tableTopAt(c *pixel.Canvas, y int) {
	w := c.Rect.Dx()
	c.Fill(image.Rect(6, y, w-6, y+1), ink('l'))
	c.Fill(image.Rect(6, y+1, w-6, y+8), ink('K'))
	c.Fill(image.Rect(6, y+8, w-6, y+15), ink('J'))
	c.Fill(image.Rect(6, y+15, w-6, y+16), ink('k'))
	for _, x := range []int{14, w - 22} {
		c.Fill(image.Rect(x, y+16, x+8, 152), ink('I'))
		c.Fill(image.Rect(x, y+16, x+1, 152), ink('J'))
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

// glass draws the purser's sand glass hung on the wall: full and still
// before anyone sits, running at frame f once someone has.
func glass(c *pixel.Canvas, x, y int, running bool, f int) {
	top, foot := "kkkkkkkkk", "kJJJJJJJk"
	rows := []string{
		top, foot,
		".kWCCCWk.",
		".kWCCCWk.",
		"..kWCWk..",
		"...kCk...",
		"...kWk...",
		"..kWWWk..",
		".kWWWWWk.",
		".kWWWWWk.",
		foot, top,
	}
	if running {
		rows[2], rows[3] = ".kWWWWWk.", ".kWCCCWk."
		stream := []string{"...kCk...", "...kWk..."}
		rows[6] = stream[f%2]
		rows[8], rows[9] = ".kWWCWWk.", ".kCCCCCk."
	}
	grid(c, image.Pt(x, y), rows...)
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
	if t.Thrown && len(t.Seats) > 0 {
		chalk(base, t.Seats[t.best()].Name)
	}

	glow := base.Dye(0x5A3A1E)
	draw := func(c *pixel.Canvas, f int) {
		candle(c, 128, 40, f, glow)
		candle(c, 256, 40, f+1, glow)
		if !t.Thrown {
			glass(c, 362, 30, len(t.Seats) > 0, f)
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
		x := benchX(i) - pixel.Bold.Width(s)/2
		c.PrintOn(pixel.Bold, x, benchTop-20, s, ink, pixel.Shadow)
	}
}

// chalk writes the beam's new name on the deckhead.
func chalk(c *pixel.Canvas, name string) {
	name = pixel.Plain(name)
	w := pixel.Regular.Width(name)
	x := c.Rect.Dx()/2 - w/2
	c.Fill(image.Rect(x-4, 3, x+w+4, 17), ink('I'))
	c.Text(x, 4, name, pixel.Glove, 0)
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
