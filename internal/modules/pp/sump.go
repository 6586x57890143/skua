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

// The painted sump at each tide under each light, finished art: ChatGPT
// paintings of the same scene, averaged onto the scene's grid and
// flattened to 40 colours each, with no boats so they can be drawn where
// the tide puts them. The usual tide was painted in all four lights; low
// and high water were painted at dusk and carried into the others by the
// ratio between the usual tide's paintings, pixel by pixel. Every painting
// is the same picture to the pixel, so what's drawn over them sits in the
// same places. Replace one whole, framed the same.
//
//go:embed sump*.png
var sumpFS embed.FS

// The tides the sump is painted at: as usual, drawn far back off the
// steps before the bell, and up over the shingle when the bell rings or
// the hold is full to its line.
const (
	tideUsual = iota
	tideLow
	tideHigh
)

var (
	tideNames  = [3]string{"sump", "sump_low", "sump_high"}
	lightNames = [4]string{"", "_night", "_dawn", "_day"}
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
// the colours of its water and stones.
type sumpArt struct {
	img    image.Image
	water  [5]uint32 // deep, swell, crest, foam, glint
	stones [3]uint32 // wet stones, dark to light
	mist   uint32
	shade  float64 // how much light the sky gives what's drawn in it
	edge   []int   // the water's top edge in each column, from the tide
}

// The sky's light on what's drawn into it, and the mist's colour, by
// light.
var (
	lightShade = [4]float64{1, 0.45, 0.8, 0.95}
	lightMist  = [4]uint32{0x8A8494, 0x2A3348, 0x9C93A6, 0xA9AFB4}
)

// sumpArts are the paintings, read once each: [tide][light].
var sumpArts [3][4]func() *sumpArt

func init() {
	for tide := range sumpArts {
		for light := range sumpArts[tide] {
			sumpArts[tide][light] = sync.OnceValue(func() *sumpArt {
				name := tideNames[tide] + lightNames[light]
				b, err := sumpFS.ReadFile(name + ".png")
				if err != nil {
					panic("pp: no painting " + name)
				}
				a := readSump(b, name, lightShade[light], lightMist[light])
				a.edge = shores[tide]()
				return a
			})
		}
	}
}

// shores are the water's top edge at each tide, from its dusk painting:
// in each column, the top of the water reaching up from the bottom, or the
// bottom where the beach or the wall stop it.
var shores [3]func() []int

func init() {
	for tide := range shores {
		shores[tide] = sync.OnceValue(func() []int {
			b, err := sumpFS.ReadFile(tideNames[tide] + ".png")
			if err != nil {
				panic("pp: no painting " + tideNames[tide])
			}
			img := decodeArt(b, tideNames[tide])
			// Water is dark and blue; the lamps' light on it and the foam
			// break it up, so a few rows that aren't are passed over.
			water := func(x, y int) bool { return wet(img.At(x, y)) && lum(img.At(x, y)) < 95 }
			edge := make([]int, sumpSize.X)
			for x := range edge {
				edge[x] = sumpSize.Y
				gap := 0
				for y := sumpSize.Y - 1; y >= 60 && gap <= 4; y-- {
					switch {
					case water(x, y):
						edge[x], gap = y, 0
					case edge[x] < sumpSize.Y || y < sumpSize.Y-3:
						gap++
					}
				}
			}
			return edge
		})
	}
}

func readSump(b []byte, what string, shade float64, mist uint32) *sumpArt {
	img := decodeArt(b, what)
	a := &sumpArt{img: img, shade: shade, mist: mist}
	deep := hexOf(img.At(150, sumpSize.Y-1))
	a.water = [5]uint32{deep, blend(deep, 0xFFFFFF, 0.12), blend(deep, 0xFFFFFF, 0.3), blend(deep, 0xFFFFFF, 0.6), blend(deep, 0xFFFFFF, 0.85)}
	for i, p := range []image.Point{{150, 110}, {140, 108}, {160, 112}} {
		a.stones[i] = blend(hexOf(img.At(p.X, p.Y)), deep, 0.35)
	}
	return a
}

// sumpSize is the painting's size, the top of the slack tide's screen.
var sumpSize = image.Pt(384, 128)

// The paintings' own places.
var (
	// wallWicks are the tops of the flames in the harbour wall's niches.
	wallWicks = []image.Point{{265, 102}, {308, 102}, {351, 102}}
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
)

// The scene's frames: eight, so everything in it loops together.
const (
	sumpFrames = ambientFrames
	sumpBeat   = ambientBeat
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

// tide is which tide the sump is painted at: drawn far back from an hour
// before the bell, up over the shingle while it rings and whenever the
// hold is full to its line, and as usual otherwise.
func (s Sump) tide() int {
	switch {
	case s.Stage == bellDrawing || s.Stage == bellBoats:
		return tideLow
	case s.Stage == bellRinging || s.Line > 0 && s.Hold >= s.Line:
		return tideHigh
	}
	return tideUsual
}

// lift is how far above its painted edge the usual tide stands: a little
// down the shingle while the hold is low, a little up it as it fills.
// Low and high water are painted as they stand.
func (s Sump) lift() int {
	if s.tide() != tideUsual {
		return 0
	}
	full := min(max(s.Hold, 0), s.Line)
	return -2 + int(5*full/max(s.Line, 1))
}

// leaning is whether the wall's candles lean the same way with no wind to
// lean them: from a quarter of an hour before the bell until it stops.
func (s Sump) leaning() bool { return s.Stage == bellBoats || s.Stage == bellRinging }

// boatsUp is whether the ferrymen have pulled their boats up the shingle.
func (s Sump) boatsUp() bool { return s.Stage == bellBoats || s.Stage == bellRinging }

// handleMask is the painted handle's pixels, from the day's painting,
// where it stands out against the sky best.
var handleMask = sync.OnceValue(func() map[image.Point]bool {
	img := sumpArts[tideUsual][daylight]().img
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
	art := sumpArts[s.tide()][s.Light]()
	base.Picture(art.img, image.Point{}, 1)
	ink := func(rgb uint32) uint8 { return base.Dye(rgb) }
	lift := s.lift()
	edge := art.edge

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

	// The still: the sky, the town and her hull as painted, the fog lying
	// where it lies, the pump at rest and the boats on the shingle if the
	// ferrymen have pulled them up.
	if thick := fogThick[s.Light]; thick > 0 {
		fog(base, art, 0, thick)
	}
	if !s.Pumping {
		handle(base, pumpRest, base.Dye(shaded(0x22262E, art.shade)))
	}
	// What's alive is the shoreline in front of her: the water lapping the
	// shingle with the boats riding it, and the wall's candles. At the
	// turn the pump works, and in the bell rings go out over the water.
	movers := []mover{
		// The water the whole length of the shore, rolling through the
		// swell's whole cycle once round the loop, the boats riding it.
		func(c *pixel.Canvas, f int) {
			surf(c, art, lift, f)
			boats(c, s, art, f)
		},
		// The wall's candles, a new shape every other frame.
		func(c *pixel.Canvas, f int) {
			step := every(f, 2)
			for i, w := range wallWicks {
				shape := flames[(step+i)%3]
				if s.leaning() {
					shape = flames[1+2*((step+i)%2)]
				}
				grid(c, w.Sub(image.Pt(1, 0)), shape...)
			}
		},
		func(c *pixel.Canvas, f int) { windows(c, art.img, sumpWindows, f) },
	}
	// And the small things that make it a place: a gull hanging on the
	// wind over her mast while there's light, and her portholes, lit from
	// dusk, burning a little unevenly.
	if s.Light != night {
		movers = append(movers, func(c *pixel.Canvas, f int) { gull(c, art, every(f, 4)) })
	}
	if s.Light == dusk || s.Light == night {
		movers = append(movers, func(c *pixel.Canvas, f int) { gutter(c, art, every(f, 4)) })
	}
	if s.Pumping {
		movers = append(movers, func(c *pixel.Canvas, f int) { pump(c, art, f) })
	}
	if s.Stage == bellRinging {
		movers = append(movers, func(c *pixel.Canvas, f int) { rings(c, art, f, lift) })
	}
	return ambient(base, movers...)
}

// surf is the water's edge at frame f: a swell running along the shore,
// lit along its crest, with foam where it breaks on the stones and drawn
// back down them after.
func surf(c *pixel.Canvas, art *sumpArt, lift, f int) {
	edge := art.edge
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

// The fog, finished art: a ChatGPT painting of a band of sea mist on
// black, tiling side to side, fitted to the scene's grid; how bright it is
// is how thick. fogDrop sets the band low on the water, and fogThick is how
// much of it each light brings: a dawn mist, a thin one on the night sea,
// and none in the evening or the day.
var (
	fogPic   = sync.OnceValue(func() image.Image { return sprite("fog") })
	fogThick = [4]float64{0, 0.45, 0.85, 0}
)

const (
	fogDrop  = 22
	fogDrift = 2 // pixels a frame
)

// fogAt is how thick the fog is at x, y, 0 to 1, the band moved along by
// shift and wrapped round.
func fogAt(x, y, shift int) float64 {
	img := fogPic()
	b := img.Bounds()
	fy := y - fogDrop
	if fy < 0 || fy >= b.Dy() {
		return 0
	}
	fx := ((x+shift)%b.Dx() + b.Dx()) % b.Dx()
	return float64(lum(img.At(b.Min.X+fx, b.Min.Y+fy))) / 255
}

// fog rolls along the water at frame f. The band drifts east a few pixels
// a frame, and so that the eighth frame meets the first it's two bands a
// loop's drift apart, the one fading out as the other fades in.
func fog(c *pixel.Canvas, art *sumpArt, f int, thick float64) {
	t := float64(f) / sumpFrames
	cache := map[[2]uint8]uint8{}
	for y := 40; y < sumpSize.Y; y++ {
		for x := range sumpSize.X {
			d := (1-t)*fogAt(x, y, -f*fogDrift) + t*fogAt(x, y, (sumpFrames-f)*fogDrift)
			d *= thick
			var lv uint8
			switch {
			case d > 0.55:
				lv = 4
			case d > 0.4:
				lv = 3
			case d > 0.26:
				lv = 2
			case d > 0.14:
				lv = 1
			default:
				continue
			}
			i := c.ColorIndexAt(x, y)
			to, ok := cache[[2]uint8{i, lv}]
			if !ok {
				to = c.Dye(blend(hexOf(c.Palette[i]), art.mist, []float64{0, 0.15, 0.28, 0.42, 0.56}[lv]))
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
	edge := art.edge
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
	edge, lift := art.edge, s.lift()
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

// Where the gull hangs over her mast, and the row of her lit portholes.
var (
	gullAt    = image.Pt(330, 22)
	portholes = image.Rect(224, 72, 287, 81)
	// sumpWindows are the town's lit windows up the steps and her
	// deckhouse's.
	sumpWindows = image.Rect(36, 4, 196, 66)
)

// gullBeats are the gull's wings at each step, from its sprites (wings up,
// gliding, down, half up): a glide, a beat up and a beat down, drifting a
// pixel or two as the wind takes it.
var (
	gullBeats = [3]int{1, 0, 2}
	gullDrift = [3]image.Point{{0, 0}, {2, -1}, {1, 1}}
)

// gull draws the gull at a step, in the sky's light.
func gull(c *pixel.Canvas, art *sumpArt, step int) {
	img := sprite("gull_" + strconv.Itoa(gullBeats[step]))
	at := gullAt.Add(gullDrift[step]).Sub(img.Bounds().Size().Div(2))
	stamp(c, img, at, art.shade, sumpSize.Y)
}

// gutter is her portholes at a step: as painted, the first two guttering
// low, or the last flaring. Lamplight only; an unlit porthole stays dark.
func gutter(c *pixel.Canvas, art *sumpArt, step int) {
	if step == 0 {
		return
	}
	mid := (portholes.Min.X + portholes.Max.X) / 2
	for y := portholes.Min.Y; y < portholes.Max.Y; y++ {
		for x := portholes.Min.X; x < portholes.Max.X; x++ {
			col := art.img.At(x, y)
			if !lit(col) {
				continue
			}
			switch {
			case step == 1 && x < mid:
				c.SetColorIndex(x, y, c.Dye(blend(hexOf(col), 0x7A3A10, 0.16)))
			case step == 2 && x >= mid:
				c.SetColorIndex(x, y, c.Dye(blend(hexOf(col), 0xFFF0B8, 0.14)))
			}
		}
	}
}
