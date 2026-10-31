package pp

import (
	"embed"
	"image"
	"image/color"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The sump in front of the slack tide: the bottom of the gut, the gannet
// grounded on her props, the fish market steps going down into the water,
// the shingle and the harbour wall with its candles. What the house does
// that the whole sump can watch happens out here. The tide comes up the
// shingle as the hold fills, the pump on her deck works at the turn, and
// the drowning hour is told in the water: drawn back off the steps, the
// boats pulled up, the candles leaning, and the sea coming back with rings
// going out from under her. The sky over it is the hour's: dawn with its
// mist, the grey day, the evening and the night with its stars.

// The painted sump under each light, finished art: ChatGPT paintings of
// the same scene, averaged onto the scene's grid and flattened to 40
// colours each, with no boats so they can be drawn where the tide puts
// them. Every painting is the same picture to the pixel, so what's drawn
// over them sits in the same places. Replace one whole, framed the same.
var (
	//go:embed sump.png
	sumpDusk []byte
	//go:embed sump_night.png
	sumpNight []byte
	//go:embed sump_dawn.png
	sumpDawn []byte
	//go:embed sump_day.png
	sumpDay []byte
)

// The light over the sump, from the hour in UTC: the evening the first
// painting is of (the europe bell's window), the night, dawn and the day.
const (
	dusk = iota
	night
	dawn
	daylight
)

// lightAt is the light over the sump at t.
func lightAt(t time.Time) int {
	switch h := t.UTC().Hour(); {
	case h >= 5 && h < 8:
		return dawn
	case h >= 8 && h < 17:
		return daylight
	case h >= 17 && h < 21:
		return dusk
	}
	return night
}

// sumpArt is each light's painting and what's worked out from it once:
// where its lamps are lit, its stars, and the colours of its water and
// stones.
type sumpArt struct {
	img    image.Image
	lamps  [][]image.Point // each lit window or porthole, its pixels
	stars  []image.Point
	water  [5]uint32 // deep, swell, crest, foam, glint
	stones [3]uint32 // wet stones, dark to light
	mist   uint32
	shade  float64 // how much light the sky gives what's drawn in it
}

var sumpArts = [4]func() *sumpArt{
	sync.OnceValue(func() *sumpArt { return readSump(sumpDusk, "dusk", 1, 0x8A8494) }),
	sync.OnceValue(func() *sumpArt { return readSump(sumpNight, "night", 0.45, 0x2A3348) }),
	sync.OnceValue(func() *sumpArt { return readSump(sumpDawn, "dawn", 0.8, 0x9C93A6) }),
	sync.OnceValue(func() *sumpArt { return readSump(sumpDay, "day", 0.95, 0xA9AFB4) }),
}

func readSump(b []byte, what string, shade float64, mist uint32) *sumpArt {
	img := decodeArt(b, "sump at "+what)
	a := &sumpArt{img: img, shade: shade, mist: mist}
	a.lamps = glows(img)
	// Stars: points of the night sky over the open sea that stand well out
	// of the dark around them. A day's or an evening's sky has none.
	for y := 1; y < 60 && what == "night"; y++ {
		for x := 200; x < sumpSize.X-1; x++ {
			around, sky := 0, true
			for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				around += lum(img.At(x+d.X, y+d.Y))
				// Night sky round it, not a rope's tarred brown.
				r, _, b := rgbOf(img.At(x+d.X, y+d.Y))
				sky = sky && b > r
			}
			if l := lum(img.At(x, y)); sky && l > 70 && l-around/4 > 35 && !lit(img.At(x, y)) {
				a.stars = append(a.stars, image.Pt(x, y))
			}
		}
	}
	// A star stands alone; a run of bright points is the moon's edge.
	alone := a.stars[:0:0]
	for _, p := range a.stars {
		n := 0
		for _, q := range a.stars {
			if d := p.Sub(q); d.X*d.X+d.Y*d.Y <= 2 {
				n++
			}
		}
		if n == 1 {
			alone = append(alone, p)
		}
	}
	a.stars = alone
	// The painting's faint stars were lost in fitting it, so a few more are
	// set in open sky: dark and blue, with dark blue sky all round.
	open := func(x, y int) bool {
		r, _, b := rgbOf(img.At(x, y))
		return b > r && lum(img.At(x, y)) < 50
	}
	for y := 2; y < 58 && what == "night"; y++ {
	cell:
		for x := 285; x < sumpSize.X-2; x++ {
			if hash(x, y)%61 != 0 {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					if !open(x+dx, y+dy) {
						continue cell
					}
				}
			}
			a.stars = append(a.stars, image.Pt(x, y))
		}
	}
	deep := hexOf(img.At(150, sumpSize.Y-1))
	a.water = [5]uint32{deep, blend(deep, 0xFFFFFF, 0.12), blend(deep, 0xFFFFFF, 0.3), blend(deep, 0xFFFFFF, 0.6), blend(deep, 0xFFFFFF, 0.85)}
	for i, p := range []image.Point{{150, 110}, {140, 108}, {160, 112}} {
		a.stones[i] = blend(hexOf(img.At(p.X, p.Y)), deep, 0.35)
	}
	return a
}

// glows are the lit glass in img, each window or porthole as its own
// pixels: warm, bright, and small; the evening sky is warm too but wide.
func glows(img image.Image) [][]image.Point {
	seen := map[image.Point]bool{}
	var out [][]image.Point
	for y := range 100 {
		for x := range sumpSize.X {
			p := image.Pt(x, y)
			if seen[p] || !lit(img.At(x, y)) {
				continue
			}
			var got []image.Point
			stack := []image.Point{p}
			for len(stack) > 0 {
				q := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if !q.In(image.Rect(0, 0, sumpSize.X, 100)) || seen[q] || !lit(img.At(q.X, q.Y)) {
					continue
				}
				seen[q] = true
				got = append(got, q)
				for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					stack = append(stack, q.Add(d))
				}
			}
			if len(got) >= 2 && len(got) <= 40 {
				out = append(out, got)
			}
		}
	}
	return out
}

// sumpSize is the painting's size, the top of the slack tide's screen.
var sumpSize = image.Pt(384, 128)

// The paintings' own places.
var (
	// wallWicks are the tops of the flames in the harbour wall's niches.
	wallWicks = []image.Point{{265, 102}, {308, 102}, {351, 102}}
	// lampGlint is the lamp by the steps, broken up on the water under it.
	lampGlint = image.Rect(57, 119, 69, 128)
	// The pump on her deck: the post's top, where the handle swings from,
	// the handle's end at rest, and the spout's mouth.
	pumpPivot = image.Pt(271, 37)
	pumpRest  = image.Pt(279, 47)
	pumpSpout = image.Pt(263, 47)
	// pumpHandle is the box the painted handle lies in, cleared so it can
	// be drawn swinging.
	pumpHandle = image.Rect(274, 36, 284, 49)
	// deckAt is where the pumped water lands, at the rail.
	deckAt = 54
	// shingle is the open beach, where the tide is drawn coming in and
	// going out; west of it posts and steps stand in the water.
	shingle = [2]int{70, 282}
	// farSea is the open sea past her bow.
	farSea = image.Rect(330, 78, 384, 92)
	// gullsAt is the middle of the gulls' wheel over her mast.
	gullsAt = image.Pt(330, 22)
)

// The scene's frames: eight, so everything in it loops together.
const (
	sumpFrames = 8
	sumpBeat   = 160
)

// Sump is what the scene outside shows.
type Sump struct {
	// Hold and Line are the hold against its waterline: the fuller the
	// hold, the further the tide comes up the shingle.
	Hold, Line int64
	// Stage is how far the drowning hour has come, bellNone when it isn't.
	Stage int
	// Pumping is the hour after the turn, when the pump works.
	Pumping bool
	// Light is the sky over it: dusk, night, dawn or daylight.
	Light int
}

// lift is how far above its painted edge the water stands: up the shingle
// when the tide's in, drawn right back before the bell, and high while it
// rings.
func (s Sump) lift() int {
	switch s.Stage {
	case bellDrawing, bellBoats:
		return -7
	case bellRinging:
		return 6
	}
	full := min(max(s.Hold, 0), s.Line)
	return -2 + int(6*full/max(s.Line, 1))
}

// leaning is whether the wall's candles lean the same way with no wind to
// lean them: from a quarter of an hour before the bell until it stops.
func (s Sump) leaning() bool { return s.Stage == bellBoats || s.Stage == bellRinging }

// boatsUp is whether the ferrymen have pulled their boats up the shingle.
func (s Sump) boatsUp() bool { return s.Stage == bellBoats || s.Stage == bellRinging }

// shore is the painted water's top edge in each column, the bottom row
// where the beach and the wall stop it, the same in every painting.
var shore = sync.OnceValue(func() []int {
	img := sumpArts[dusk]().img
	edge := make([]int, sumpSize.X)
	for x := range edge {
		edge[x] = sumpSize.Y
		for y := sumpSize.Y - 1; y >= sumpSize.Y-20; y-- {
			if !wet(img.At(x, y)) {
				break
			}
			edge[x] = y
		}
	}
	return edge
})

// handleMask is the painted handle's pixels, from the day's painting,
// where it stands out against the sky best.
var handleMask = sync.OnceValue(func() map[image.Point]bool {
	img := sumpArts[daylight]().img
	m := map[image.Point]bool{}
	for y := pumpHandle.Min.Y; y < pumpHandle.Max.Y; y++ {
		for x := pumpHandle.Min.X; x < pumpHandle.Max.X; x++ {
			if lum(img.At(x, y)) < 70 {
				m[image.Pt(x, y)] = true
			}
		}
	}
	return m
})

// sumpScene draws the scene over the top of base, and returns it a frame
// at a time: the painting, then everything that moves on it.
func sumpScene(base *pixel.Canvas, s Sump) []*pixel.Canvas {
	art := sumpArts[s.Light]()
	base.Picture(art.img, image.Point{}, 1)
	ink := func(rgb uint32) uint8 { return base.Dye(rgb) }
	lift := s.lift()
	edge := shore()

	// The painted handle, taken out: each of its pixels the sky beside it.
	for p := range handleMask() {
		for nx := p.X + 1; nx < pumpHandle.Max.X+6; nx++ {
			if !handleMask()[image.Pt(nx, p.Y)] {
				base.SetColorIndex(p.X, p.Y, base.ColorIndexAt(nx, p.Y))
				break
			}
		}
	}
	// The water's still body for this tide: up the shingle, or drawn off
	// it and the stones it covered left wet.
	for x := range sumpSize.X {
		top := edge[x]
		if top >= sumpSize.Y {
			continue
		}
		switch {
		case lift < 0:
			for y := top; y < min(top-lift, sumpSize.Y); y++ {
				base.SetColorIndex(x, y, ink(art.stones[hash(x, y)%3]))
			}
		case lift > 0 && x >= shingle[0] && x < shingle[1]:
			for y := top - lift; y < top; y++ {
				base.SetColorIndex(x, y, ink(art.water[0]))
			}
		}
	}

	var frames []*pixel.Canvas
	for f := range sumpFrames {
		c := base.Copy()
		surf(c, art, lift, f)
		boats(c, s, art, f)
		glints(c, art, f)
		flicker(c, art, f)
		for i, w := range wallWicks {
			shape := flames[(f+i)%3]
			if s.leaning() {
				shape = flames[1+2*((f+i)%2)]
			}
			grid(c, w.Sub(image.Pt(1, 0)), shape...)
		}
		switch s.Light {
		case night:
			stars(c, art, f)
		default:
			gulls(c, art, f)
		}
		if s.Pumping {
			pump(c, art, f)
		} else {
			handle(c, pumpRest, ink(shaded(0x22262E, art.shade)))
		}
		if s.Stage == bellRinging {
			rings(c, art, f, lift)
		}
		if s.Light == dawn {
			mist(c, art, f)
		}
		frames = append(frames, c)
	}
	return frames
}

// surf is the water's edge at frame f: a swell running along the shore,
// lit along its crest, with foam where it breaks on the stones and drawn
// back down them after.
func surf(c *pixel.Canvas, art *sumpArt, lift, f int) {
	edge := shore()
	deep, swell, crest, foam := c.Dye(art.water[0]), c.Dye(art.water[1]), c.Dye(art.water[2]), c.Dye(art.water[3])
	for x := range sumpSize.X {
		top := edge[x]
		if top >= sumpSize.Y {
			continue
		}
		if lift < 0 || x >= shingle[0] && x < shingle[1] {
			top -= lift
		}
		phase := 2 * math.Pi * (float64(x)/24 - float64(f)/sumpFrames)
		wave := math.Sin(phase) + 0.4*math.Sin(2*math.Pi*(float64(x)/9+2*float64(f)/sumpFrames))
		reach := int(math.Round(wave))
		for y := top - 2; y <= top+3 && y < sumpSize.Y; y++ {
			if y < 0 {
				continue
			}
			switch d := y - (top - reach); {
			case d == 0 && wave > 0.9:
				c.SetColorIndex(x, y, foam)
			case d == 0:
				c.SetColorIndex(x, y, crest)
			case d == 1:
				c.SetColorIndex(x, y, swell)
			case d > 1:
				c.SetColorIndex(x, y, deep)
			case d == -1 && wave < -0.6 && hash(x, f)%3 == 0:
				// The wash left on the stones as the swell draws back.
				c.SetColorIndex(x, y, swell)
			}
		}
	}
}

// glints are the sky on the water: points lit and gone again across the
// shallows and the far sea, and the lamp by the steps broken up under it.
func glints(c *pixel.Canvas, art *sumpArt, f int) {
	crest, glint := c.Dye(art.water[2]), c.Dye(art.water[4])
	for y := sumpSize.Y - 10; y < sumpSize.Y; y++ {
		for x := range sumpSize.X - 2 {
			if h := hash(x, y); h%53 == 0 && int(h>>8)%sumpFrames == f && wet(c.At(x, y)) {
				c.SetColorIndex(x, y, crest)
				c.SetColorIndex(x+1, y, glint)
				c.SetColorIndex(x+2, y, crest)
			}
		}
	}
	for y := farSea.Min.Y; y < farSea.Max.Y; y++ {
		for x := farSea.Min.X; x < farSea.Max.X; x++ {
			if h := hash(x, y); h%19 == 0 && int(h>>8)%sumpFrames == f && wet(c.At(x, y)) {
				c.SetColorIndex(x, y, crest)
			}
		}
	}
	src := c.Copy()
	for y := lampGlint.Min.Y; y < lampGlint.Max.Y; y++ {
		dx := int(math.Round(1.4 * math.Sin(2*math.Pi*(float64(y)/3+float64(f)/sumpFrames))))
		for x := lampGlint.Min.X; x < lampGlint.Max.X; x++ {
			c.SetColorIndex(x, y, src.ColorIndexAt(x-dx, y))
		}
	}
}

// flicker is every lit window and porthole at frame f: mostly as painted,
// now and then a candle behind one gutters low or flares.
func flicker(c *pixel.Canvas, art *sumpArt, f int) {
	for i, lamp := range art.lamps {
		var toward uint32
		var by float64
		switch (hash(i, f) >> 4) % 7 {
		case 0:
			toward, by = 0x401A08, 0.45
		case 1:
			toward, by = 0xFFF0B0, 0.4
		case 2:
			toward, by = 0x7A3A10, 0.2
		default:
			continue
		}
		for _, p := range lamp {
			c.SetColorIndex(p.X, p.Y, c.Dye(blend(hexOf(art.img.At(p.X, p.Y)), toward, by)))
		}
	}
}

// stars twinkle: each goes dim on its own frames and comes back.
func stars(c *pixel.Canvas, art *sumpArt, f int) {
	for i, p := range art.stars {
		switch (hash(i, 7) + uint32(f)) % 5 {
		case 0:
			c.SetColorIndex(p.X, p.Y, c.Dye(0x3E4866))
		case 1, 3:
			c.SetColorIndex(p.X, p.Y, c.Dye(0x8C96B4))
		case 4:
			c.SetColorIndex(p.X, p.Y, c.Dye(0x66708E))
		case 2:
			c.SetColorIndex(p.X, p.Y, c.Dye(0xFFFFFF))
			// A bright one throws a cross.
			if hash(i, 3)%3 == 0 {
				for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					q := p.Add(d)
					c.SetColorIndex(q.X, q.Y, c.Dye(blend(hexOf(art.img.At(q.X, q.Y)), 0xC6D2EE, 0.5)))
				}
			}
		}
	}
}

// A far gull is a few strokes: w its white, g its grey wings.
var gullFar = [][]string{{"g...g", ".gwg."}, {".....", "ggwgg"}}

// gullBeats are the near gull's wingbeats a frame at a time, from its
// sprites (wings up, gliding, down, half up): it glides, and once round
// beats its wings to hold its place.
var gullBeats = [sumpFrames]int{1, 1, 1, 3, 0, 3, 2, 1}

// gulls hang on the wind over her mast, the way they do over every
// harbour: one near, drifting a little and beating its wings now and then
// to hold its place, and one far off past her bow, smaller, on its own
// beat.
func gulls(c *pixel.Canvas, art *sumpArt, f int) {
	inks := map[rune]uint8{
		'w': c.Dye(shaded(0xF4F2EC, art.shade)),
		'g': c.Dye(shaded(0x5E646C, art.shade)),
	}
	a := 2 * math.Pi * float64(f) / sumpFrames
	near := gullsAt.Add(image.Pt(int(math.Round(3*math.Cos(a))), int(math.Round(2*math.Sin(2*a)))))
	img := sprite("gull_" + strconv.Itoa(gullBeats[f]))
	stamp(c, img, near.Sub(img.Bounds().Size().Div(2)), art.shade, sumpSize.Y)
	far := image.Pt(362+int(math.Round(2*math.Sin(a+1))), 40+int(math.Round(math.Cos(a))))
	gull(c, far, gullFar[(f/2)%2], inks)
}

// gull draws a gull's rows centred on p.
func gull(c *pixel.Canvas, p image.Point, rows []string, inks map[rune]uint8) {
	for dy, row := range rows {
		for dx, r := range row {
			if i, ok := inks[r]; ok {
				c.SetColorIndex(p.X-len(row)/2+dx, p.Y-len(rows)/2+dy, i)
			}
		}
	}
}

// mist lies on the water at dawn and rolls along it, thick in some places
// and thin in others, more of it out past her bow.
func mist(c *pixel.Canvas, art *sumpArt, f int) {
	t := 2 * math.Pi * float64(f) / sumpFrames
	cache := map[[2]uint8]uint8{}
	for y := 62; y < sumpSize.Y; y++ {
		for x := range sumpSize.X {
			// How thick: lying low, heavier to the east and over the water,
			// with two drifts of it rolling through at their own speeds.
			low := float64(y-62) / float64(sumpSize.Y-62)
			east := float64(x) / float64(sumpSize.X)
			n := 0.5*math.Sin(float64(x)/23+t+math.Sin(float64(y)/7)) + 0.3*math.Sin(float64(x)/9-2*t+float64(y)/4)
			d := 0.32*low*low + 0.32*east*east*(0.4+0.6*low) + 0.2*n
			if wet(c.At(x, y)) {
				d += 0.1
			}
			var lv uint8
			switch {
			case d > 0.42:
				lv = 3
			case d > 0.3:
				lv = 2
			case d > 0.2:
				lv = 1
			default:
				continue
			}
			i := c.ColorIndexAt(x, y)
			to, ok := cache[[2]uint8{i, lv}]
			if !ok {
				to = c.Dye(blend(hexOf(c.Palette[i]), art.mist, []float64{0, 0.18, 0.32, 0.48}[lv]))
				cache[[2]uint8{i, lv}] = to
			}
			c.SetColorIndex(x, y, to)
		}
	}
}

// handle draws the pump's handle from its pivot to end.
func handle(c *pixel.Canvas, end image.Point, i uint8) {
	d := end.Sub(pumpPivot)
	n := max(abs(d.X), abs(d.Y), 1)
	for k := 0; k <= n; k++ {
		c.SetColorIndex(pumpPivot.X+d.X*k/n, pumpPivot.Y+d.Y*k/n, i)
	}
}

// pump is the pump working at frame f: the handle down and up, and what's
// over the line gushing out of the spout with each stroke and over the
// side.
func pump(c *pixel.Canvas, art *sumpArt, f int) {
	ends := []image.Point{{281, 39}, {282, 43}, pumpRest, {282, 43}}
	handle(c, ends[f%4], c.Dye(shaded(0x22262E, art.shade)))
	crest, foam, glint := c.Dye(art.water[2]), c.Dye(art.water[3]), c.Dye(art.water[4])
	// Strongest on the down stroke: the stream thick and arcing out, then
	// thinning to drops.
	gush := []int{1, 3, 2, 0}[f%4]
	for y := pumpSpout.Y; y < deckAt; y++ {
		x := pumpSpout.X - (y-pumpSpout.Y)*gush/6
		if gush == 0 && (y+f)%3 != 0 {
			continue
		}
		ink := crest
		if (y-f)%3 == 0 {
			ink = foam
		}
		c.SetColorIndex(x, y, ink)
		if gush >= 2 {
			c.SetColorIndex(x+1, y, crest)
		}
	}
	if gush > 0 {
		for _, d := range []image.Point{{-3, -1}, {-1, -2}, {1, -1}, {2, -2}}[:gush+1] {
			p := image.Pt(pumpSpout.X-gush, deckAt).Add(d)
			c.SetColorIndex(p.X, p.Y, glint)
		}
	}
}

// rings go out over the water from under the steps while the bell rings,
// two at a time, each wider than the last, fading as they go.
func rings(c *pixel.Canvas, art *sumpArt, f, lift int) {
	edge := shore()
	from := image.Pt(36, sumpSize.Y+2)
	for k := range 2 {
		step := (f + k*sumpFrames/2) % sumpFrames
		r := float64(step)*5 + 4
		ink := c.Dye(art.water[3])
		if step > sumpFrames/2 {
			ink = c.Dye(art.water[2])
		}
		for t := 0.0; t < math.Pi; t += 0.015 {
			x := from.X + int(math.Round(r*2.2*math.Cos(t)))
			y := from.Y - int(math.Round(r*math.Sin(t)/2.2))
			if x < 0 || x >= sumpSize.X || y >= sumpSize.Y || y <= edge[x]-lift+1 {
				continue
			}
			c.SetColorIndex(x, y, ink)
		}
	}
}

// The sprites drawn into the scenes, finished art: ChatGPT paintings keyed
// off magenta and reduced to the scenes' grid, each to its own few colours.
// The ferrymen's two boats afloat and pulled up on the shingle, the gull's
// four wingbeats, and the chapel bell that hangs in the taproom while it
// rings. Replace one whole.
//
//go:embed sprites/*.png
var spriteFS embed.FS

var sprites sync.Map // name -> image.Image

// sprite is the sprite name, decoded once.
func sprite(name string) image.Image {
	if img, ok := sprites.Load(name); ok {
		return img.(image.Image)
	}
	b, err := spriteFS.ReadFile("sprites/" + name + ".png")
	if err != nil {
		panic("pp: no sprite " + name)
	}
	img := decodeArt(b, name)
	sprites.Store(name, img)
	return img
}

// stamp draws img with its top left at p in the sky's light, leaving what's
// clear and anything at or below the row under (the water it floats in).
func stamp(c *pixel.Canvas, img image.Image, p image.Point, shade float64, under int) {
	b := img.Bounds()
	inks := map[color.Color]uint8{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			col := img.At(x, y)
			if _, _, _, a := col.RGBA(); a < 0x8000 {
				continue
			}
			q := p.Add(image.Pt(x-b.Min.X, y-b.Min.Y))
			if !q.In(c.Rect) || q.Y >= under {
				continue
			}
			i, ok := inks[col]
			if !ok {
				i = c.Dye(shaded(hexOf(col), shade))
				inks[col] = i
			}
			c.SetColorIndex(q.X, q.Y, i)
		}
	}
}

// The boats' places: afloat, where the shore is under each, and pulled up,
// where their keels rest on the shingle.
var (
	afloatAt  = [2]int{98, 160}
	beachedAt = [2]image.Point{{104, 112}, {160, 115}}
)

// boats draws the ferrymen's boats: afloat in the shallows, riding the
// swell at frame f, sat on the stones when the water's drawn back, or
// pulled up the shingle for the bell.
func boats(c *pixel.Canvas, s Sump, art *sumpArt, f int) {
	names := [2]string{"boat_brown", "boat_green"}
	if s.boatsUp() {
		for i, at := range beachedAt {
			img := sprite(names[i] + "_beached")
			stamp(c, img, at.Sub(image.Pt(0, img.Bounds().Dy())), art.shade, sumpSize.Y)
		}
		return
	}
	edge, lift := shore(), s.lift()
	for i, x := range afloatAt {
		img := sprite(names[i])
		w := img.Bounds().Dx()
		water := edge[x+w/2] - max(lift, 0)
		// Half a hull under, rising and falling a pixel with the swell,
		// the two out of step; on the stones, the keel on them.
		bob, under := 0, water+2
		if lift >= 0 {
			if (f/2+i)%2 == 1 {
				bob = 1
			}
		} else {
			water, under = edge[x+w/2]-lift, sumpSize.Y
		}
		stamp(c, img, image.Pt(x, water-img.Bounds().Dy()+5+bob), art.shade, under)
	}
}

// wet is whether a colour of the paintings is water: blue over red.
func wet(col color.Color) bool {
	r, g, b := rgbOf(col)
	return b > r+8 && b >= g
}

// lit is whether a colour is lamplight: warm and bright.
func lit(col color.Color) bool {
	r, g, b := rgbOf(col)
	return r > 190 && g > 110 && b < 140 && r > b+80
}

func rgbOf(col color.Color) (r, g, b uint32) {
	r, g, b, _ = col.RGBA()
	return r >> 8, g >> 8, b >> 8
}

func lum(col color.Color) int {
	r, g, b := rgbOf(col)
	return int(r*3+g*6+b) / 10
}

func hexOf(col color.Color) uint32 {
	r, g, b := rgbOf(col)
	return r<<16 | g<<8 | b
}

// blend is a moved by toward b, 0 to 1.
func blend(a, b uint32, by float64) uint32 {
	ch := func(s uint) uint32 {
		x, y := float64(a>>s&0xFF), float64(b>>s&0xFF)
		return uint32(math.Round(x+(y-x)*by)) << s
	}
	return ch(16) | ch(8) | ch(0)
}

// shaded is rgb in the sky's light: darker and bluer as it falls.
func shaded(rgb uint32, by float64) uint32 {
	if by >= 1 {
		return rgb
	}
	return blend(rgb, 0x0C1222, 1-by)
}

// hash is a fixed scatter, for stones, glints and lamps.
func hash(x, y int) uint32 {
	h := uint32(x)*374761393 + uint32(y)*668265263
	h = (h ^ h>>13) * 1274126177
	return h ^ h>>16
}
