package pp

import (
	"bytes"
	_ "embed"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"sync"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The slack tide drawn: the taproom below the gannet's deck, where the
// purser's table runs down the middle under the deckhead's scraps of
// colour, tallow candles lean in the sconces on her ribs and the sand
// glass runs. Each is a few frames: the candles flicker, the sand runs and
// the bones tumble. The sump outside is in sump.go.

// The frames' beats, in milliseconds.
const (
	flickerBeat = 360
	tumbleBeat  = 170
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

// The purser's table and a bench's high back, painted the same way and
// mapped onto the taproom's own colours, clear round their edges.
var (
	//go:embed board.png
	boardArt []byte
	//go:embed settle.png
	settleArt []byte
)

var (
	boardPic  = sync.OnceValue(func() image.Image { return decodeArt(boardArt, "table") })
	settlePic = sync.OnceValue(func() image.Image { return decodeArt(settleArt, "bench") })
)

func decodeArt(b []byte, what string) image.Image {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		panic("pp: the " + what + " won't decode: " + err.Error())
	}
	return img
}

// paste draws img at p in its own colours, leaving what's clear.
func paste(c *pixel.Canvas, img image.Image, p image.Point) {
	b := img.Bounds()
	inks := map[color.Color]uint8{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			col := img.At(x, y)
			r, g, bl, a := col.RGBA()
			if a < 0x8000 {
				continue
			}
			i, ok := inks[col]
			if !ok {
				i = c.Dye(r>>8<<16 | g>>8<<8 | bl>>8)
				inks[col] = i
			}
			if q := p.Add(image.Pt(x-b.Min.X, y-b.Min.Y)); q.In(c.Rect) {
				c.SetColorIndex(q.X, q.Y, i)
			}
		}
	}
}

// taproomPic is the painted taproom, finished art: a ChatGPT painting of
// the deckhead, the ribs, the sconces and the glass, averaged onto the
// screen's grid and flattened to 40 colours. Replace it whole; don't edit
// its pixels.
var taproomPic = sync.OnceValue(func() image.Image { return decodeArt(taproomArt, "taproom") })

// taproom draws the room behind a table, and in the drowning hour the
// chapel bell hung from the main beam between the middle benches. The
// flames and the sand go on after, a frame at a time.
func taproom(c *pixel.Canvas, bell bool) {
	c.Picture(taproomPic(), image.Point{}, 1)
	floor := c.ColorIndexAt(0, roomSize.Y-1)
	c.Fill(image.Rect(0, roomSize.Y, c.Rect.Dx(), 152), floor)
}

// bellSway is how far the bell's lip swings each frame, its rope's knot
// fixed to the beam.
var bellSway = []int{0, 1, 2, 1, 0, -1, -2, -1}

// hangBell draws the chapel bell hung from the main beam between the
// middle benches at frame f, swinging on its rope while it rings: each row
// leans further than the one above it.
func hangBell(c *pixel.Canvas, f int) {
	img := sprite("bell")
	b := img.Bounds()
	top := image.Pt(c.Rect.Dx()/2-b.Dx()/2, 26)
	sway := bellSway[f%len(bellSway)]
	inks := map[color.Color]uint8{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		dx := int(math.Round(float64(sway) * float64(y-b.Min.Y) / float64(b.Dy())))
		for x := b.Min.X; x < b.Max.X; x++ {
			col := img.At(x, y)
			if _, _, _, a := col.RGBA(); a < 0x8000 {
				continue
			}
			i, ok := inks[col]
			if !ok {
				i = c.Dye(hexOf(col))
				inks[col] = i
			}
			c.SetColorIndex(top.X+x-b.Min.X+dx, top.Y+y-b.Min.Y, i)
		}
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

// tumble is the bones in the air, a frame each, from where they land over
// a bird's head: thrown up, over and down. They roll face on and tipped on
// an edge in turn, showing other numbers on the way.
var tumble = []struct{ dx, dy int }{{-8, 14}, {-3, -8}, {4, -14}, {6, -4}}

// diceAt is where bench i's dice come to rest, over its slate.
func diceAt(i int) image.Point { return image.Pt(benchX(i)-pairSize.X/2, benchTop-20-pairSize.Y) }

// Where a table's six benches stand: the middle of each, the portraits'
// tops and the table's top.
const (
	benchTop = 70
	tableTop = 110
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

// markerFor is the house's marker as it lies in front of who: tilted
// somewhere within a quarter turn either way of upright or over, and maybe
// mirrored, so no two lie quite the same, and each the same way every
// frame. Each pixel is the nearest of the marker's own, so it stays its
// palette.
func markerFor(who string) image.Image {
	h := fnv.New32a()
	h.Write([]byte(who))
	n := h.Sum32()
	src := sprite("marker_small")
	b := src.Bounds()
	angle := float64(int(n%4)*90+int(n>>2%41)-20) * math.Pi / 180
	mirror := n>>8&1 == 1
	sin, cos := math.Sin(angle), math.Cos(angle)
	cx, cy := float64(b.Dx())/2, float64(b.Dy())/2
	side := int(math.Ceil(math.Hypot(float64(b.Dx()), float64(b.Dy()))))
	out := image.NewNRGBA(image.Rect(0, 0, side, side))
	half := float64(side) / 2
	for y := range side {
		for x := range side {
			// Back from the turned picture to the marker.
			px, py := float64(x)+0.5-half, float64(y)+0.5-half
			sx := int(math.Floor(cos*px + sin*py + cx))
			sy := int(math.Floor(-sin*px + cos*py + cy))
			if sx < 0 || sy < 0 || sx >= b.Dx() || sy >= b.Dy() {
				continue
			}
			if mirror {
				sx = b.Dx() - 1 - sx
			}
			out.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return out
}

// tableFrames are a table's frames and their beats: the room with its
// birds and their stakes, the candles flickering and the glass running;
// once thrown, the bones tumbling over each bird first, then landing.
func tableFrames(t Table) ([]*pixel.Canvas, []int) {
	base := pixel.New(screenW, screenH)
	taproom(base, t.Bell)
	for i := range tableSeats {
		paste(base, settlePic(), image.Pt(benchX(i)-24, tableTop+6-settlePic().Bounds().Dy()))
		if i >= len(t.Seats) {
			continue
		}
		sitDownIn(base, cutout(Portrait(t.Seats[i].Class)), image.Pt(benchX(i)-16, benchTop))
	}
	paste(base, boardPic(), image.Pt((screenW-boardPic().Bounds().Dx())/2, tableTop))
	pool := base.Dye(0xD9A24E)
	for i, p := range t.Seats {
		x := benchX(i)
		if p.Stake == stakes[len(stakes)-1].pp {
			// A lantern lit on the table in front of them.
			base.Fill(image.Rect(x-12, tableTop+1, x+12, tableTop+7), pool)
		}
		if p.Marker {
			mk := markerFor(p.Name)
			paste(base, mk, image.Pt(x, tableTop-2).Sub(mk.Bounds().Size().Div(2)))
		} else {
			base.Icon(stakeIcon(p.Stake), image.Pt(x-8, tableTop-9))
		}
	}
	panel(base, t)

	draw := func(c *pixel.Canvas, f int) {
		candles(c, f, t.Bell)
		hangScraps(c, t.Scraps, f)
		if t.Bell {
			hangBell(c, f)
		}
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
			for i, p := range t.Seats {
				// Faces on the way down that aren't the throw's.
				n := (p.Throw*7 + k*31 + i*13) % 100
				tens, ones := faces(n)
				pair(c, diceAt(i).Add(image.Pt(at.dx, at.dy)), tens, ones, k%2 == 1)
			}
			frames, beats = append(frames, c), append(beats, tumbleBeat)
		}
		chalk(base, t.Seats[t.best()].Name)
	}
	for f := range 6 {
		c := base.Copy()
		draw(c, f)
		// The slates go over the flames behind them, the dice at rest over
		// them showing what was thrown.
		if t.Thrown {
			landed(c, t)
			for i, p := range t.Seats {
				tens, ones := faces(p.Throw)
				pair(c, diceAt(i), tens, ones, false)
			}
		}
		frames, beats = append(frames, c), append(beats, flickerBeat)
	}
	return frames, beats
}

// slate is where the throw w wide over bench i is chalked.
func slate(i, w int) image.Rectangle {
	x := benchX(i) - w/2
	return image.Rect(x-4, benchTop-19, x+w+4, benchTop-2)
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
		plate := slate(i, w)
		c.Fill(plate, pixel.Line)
		c.Outline(plate, pixel.Shadow)
		c.Print(pixel.Bold, x, benchTop-17, s, ink)
	}
}

// chalk writes the beam's new name in chalk on the main beam.
func chalk(c *pixel.Canvas, name string) {
	name = fitName(pixel.Regular, pixel.Plain(name), 160)
	c.Text(c.Rect.Dx()/2-pixel.Regular.Width(name)/2, 17, name, pixel.Glove, 0)
}

// panel is the purser's ledger under the table: a column ruled for each
// bench, riveted to the frame like the hull's planks, with the bird's
// name and their stake, or once thrown what the bones paid; the slate in
// the corner, its hash while it's turned to the wall and its seed once
// it's turned round; and the beam's new name in gold.
func panel(c *pixel.Canvas, t Table) {
	r := image.Rect(4, 152, screenW-4, screenH-4)
	c.Window(r)
	tab := "the purser's ledger"
	if t.Bell {
		tab = "the drowning hour"
	}
	c.Tab(r, tab)
	if t.Slate != "" {
		word := "slate to the wall"
		if t.Thrown {
			word = "slate turned"
		}
		at := rightIn(c, pixel.Regular, r.Max.X-10, r.Min.Y+5, t.Slate[:min(len(t.Slate), 8)], pixel.Dim)
		rightIn(c, pixel.Light, at-labelGap, r.Min.Y+5, word, pixel.Dim)
	}
	iron, hi := ink('F'), ink('H')
	rivet := func(x, y int) {
		c.Fill(image.Rect(x, y, x+2, y+2), iron)
		c.SetColorIndex(x, y, hi)
	}
	for _, p := range []image.Point{{r.Min.X + 1, r.Min.Y + 1}, {r.Max.X - 3, r.Min.Y + 1}, {r.Min.X + 1, r.Max.Y - 3}, {r.Max.X - 3, r.Max.Y - 3}} {
		rivet(p.X, p.Y)
	}
	top, foot := paneTop(r)-5, r.Max.Y-6
	for i := 1; i < tableSeats; i++ {
		x := benchX(i) - 32
		c.Fill(image.Rect(x, top, x+1, foot), pixel.Bone)
		rivet(x, r.Max.Y-3)
	}
	best := -1
	if t.Thrown && len(t.Seats) > 0 {
		best = t.best()
	}
	y := paneTop(r) - 2
	for i := range tableSeats {
		// A column runs between the rules either side of its bench, inside
		// the parchment at the ends.
		left := max(benchX(i)-29, r.Min.X+8)
		right := min(benchX(i)+29, r.Max.X-8)
		x := (left + right) / 2
		if i >= len(t.Seats) {
			s := "free"
			c.Print(pixel.Light, x-pixel.Light.Width(s)/2, y+8, s, pixel.Dim)
			continue
		}
		p := t.Seats[i]
		name := fitName(pixel.Bold, pixel.Plain(p.Name), right-left)
		ink := pixel.Ink
		if i == best {
			ink = pixel.Gold
		}
		c.Print(pixel.Bold, x-pixel.Bold.Width(name)/2, y, name, ink)
		line, lineInk := grouped(p.Stake/unit), pixel.Gold
		if p.Marker {
			// The marker is pinned at the foot of the column, in the middle,
			// over the ledger's lower edge where the column is clear; it says
			// itself, so only the throw's word goes in the row.
			mk := markerFor(p.Name)
			paste(c, mk, image.Pt(x, foot+3).Sub(mk.Bounds().Size().Div(2)))
			line = ""
		} else {
			c.Icon(stakeIcon(p.Stake), image.Pt(left, y+16-iconLift))
		}
		if t.Thrown {
			line, lineInk = "lost", pixel.Red
			if p.Marker {
				line, lineInk = "nil", pixel.Dim
			}
			if p.Won > 0 {
				line, lineInk = "+"+grouped(p.Won/unit), pixel.Green
			}
		}
		rightIn(c, pixel.Regular, right, y+16, line, lineInk)
	}
}

// houseFrames are the slack tide's screen: the sump in front of her, the
// tide on the shingle and whatever the hour brings moving on it, and
// under it the hold's figures, the stakes and the bird's day.
func houseFrames(h House) ([]*pixel.Canvas, []int) {
	base := pixel.New(screenW, screenH)
	figures(base, h)
	frames := sumpScene(base, h.Sump)
	beats := make([]int, len(frames))
	for i := range beats {
		beats[i] = sumpBeat
	}
	return frames, beats
}

// figures are the windows under the sump: the hold against its line, and
// the stakes with the bird's day at the tables.
func figures(c *pixel.Canvas, h House) {
	top := sumpSize.Y + 4
	hold := image.Rect(4, top, 190, 212)
	c.Window(hold)
	c.Tab(hold, tideWord(h))
	y := paneTop(hold) - 4
	label(c, hold.Min.X+12, y, "the hold")
	right(c, hold.Max.X-12, y, grouped(h.Hold/unit)+" pp", pixel.Gold)
	c.Bar(image.Rect(hold.Min.X+12, y+15, hold.Max.X-12, y+19), min(h.Hold, h.Line), h.Line, pixel.Blue)
	// The beam and the deckhead, from the bar.
	label(c, hold.Min.X+12, y+23, "the beam")
	beam, ink := "wiped", pixel.Dim
	if h.Beam != "" {
		beam, ink = fitName(pixel.Regular, pixel.Plain(h.Beam), 90)+" "+strconv.Itoa(h.BeamThrow), pixel.Ink
	}
	right(c, hold.Max.X-12, y+23, beam, ink)
	label(c, hold.Min.X+12, y+40, "the deckhead")
	right(c, hold.Max.X-12, y+40, fmt.Sprintf("%d of %d", h.Scraps, len(feats)), pixel.Ink)

	st := image.Rect(194, top, 380, 212)
	c.Window(st)
	tab := "the stakes"
	if h.Bell {
		tab = "the bell: 100 pays 15x"
	}
	c.Tab(st, tab)
	y = paneTop(st)
	for i, s := range stakes {
		x, yy := st.Min.X+10+(i%2)*88, y+(i/2)*17
		ink, word := pixel.Ink, grouped(s.pp/unit)
		if s.pp > h.MaxStake {
			ink, word = pixel.Dim, "out"
		}
		c.Icon(stakeIcons[i], image.Pt(x, yy-iconLift))
		end := label(c, x+pixel.IconCells+3, yy, s.name)
		c.Print(pixel.Regular, end+4, yy, word, ink)
	}
	yy := y + 35
	label(c, st.Min.X+12, yy, "staked today")
	right(c, st.Max.X-12, yy, grouped(h.Staked/unit)+" pp", pixel.Ink)
}

// figure is a portrait cut out of its black field, so the bird sits in the
// room rather than hanging on its wall: every pixel of the field's colour
// reached from the edge is clear, and black inside the bird (a raven's
// feathers) stays. nil is clear.
type figure [][]color.Color

var cutouts sync.Map // image.Image -> figure

// cutout is img's figure, worked out once per portrait.
func cutout(img image.Image) figure {
	if f, ok := cutouts.Load(img); ok {
		return f.(figure)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	// The field is the colour most of the edge is: a hood or a crest may
	// reach a corner.
	key := func(x, y int) [3]uint32 {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return [3]uint32{r, g, bl}
	}
	edge := map[[3]uint32]int{}
	for x := range w {
		edge[key(x, 0)]++
		edge[key(x, h-1)]++
	}
	for y := range h {
		edge[key(0, y)]++
		edge[key(w-1, y)]++
	}
	var field [3]uint32
	for k, n := range edge {
		if n > edge[field] || n == edge[field] && k[0]+k[1]+k[2] < field[0]+field[1]+field[2] {
			field = k
		}
	}
	isField := func(x, y int) bool { return key(x, y) == field }
	clear := make([]bool, w*h)
	var stack []image.Point
	for x := range w {
		stack = append(stack, image.Pt(x, 0), image.Pt(x, h-1))
	}
	for y := range h {
		stack = append(stack, image.Pt(0, y), image.Pt(w-1, y))
	}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if p.X < 0 || p.Y < 0 || p.X >= w || p.Y >= h || clear[p.Y*w+p.X] || !isField(p.X, p.Y) {
			continue
		}
		clear[p.Y*w+p.X] = true
		stack = append(stack, p.Add(image.Pt(1, 0)), p.Add(image.Pt(-1, 0)), p.Add(image.Pt(0, 1)), p.Add(image.Pt(0, -1)))
	}
	f := make(figure, h)
	for y := range h {
		f[y] = make([]color.Color, w)
		for x := range w {
			if !clear[y*w+x] {
				f[y][x] = img.At(b.Min.X+x, b.Min.Y+y)
			}
		}
	}
	cutouts.Store(img, f)
	return f
}

// sitDownIn draws f with its top left at p, outlined in shadow so the bird
// stands off the wall behind it.
func sitDownIn(c *pixel.Canvas, f figure, p image.Point) {
	at := func(x, y int) bool { return y >= 0 && y < len(f) && x >= 0 && x < len(f[y]) && f[y][x] != nil }
	for y := -1; y <= len(f); y++ {
		for x := -1; x <= len(f[0]); x++ {
			if !at(x, y) && (at(x-1, y) || at(x+1, y) || at(x, y-1) || at(x, y+1)) {
				c.SetColorIndex(p.X+x, p.Y+y, pixel.Shadow)
			}
		}
	}
	inks := map[color.Color]uint8{}
	for y, row := range f {
		for x, col := range row {
			if col == nil {
				continue
			}
			i, ok := inks[col]
			if !ok {
				r, g, b, _ := col.RGBA()
				i = c.Dye(r>>8<<16 | g>>8<<8 | b>>8)
				inks[col] = i
			}
			c.SetColorIndex(p.X+x, p.Y+y, i)
		}
	}
}

// fitName is s in f cut to width, a letter at a time, with a dot to say it
// was cut.
func fitName(f *pixel.Face, s string, width int) string {
	if f.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && f.Width(string(r)+".") > width {
		r = r[:len(r)-1]
	}
	return string(r) + "."
}
