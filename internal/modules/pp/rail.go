package pp

import (
	"embed"
	"image"
	"math"
	"strconv"
	"sync"

	"github.com/6586x57890143/skua/internal/pixel"
)

// Calling out at the rail: two birds who want to settle something stand
// at the gannet's rail and stake the same. Each throws against the house
// as usual and the house pays each on their own throw; the higher takes
// the honour. Nothing passes between them.
//
// The rail is drawn from her deck looking out over the sump: the cliff
// houses and the gut climbing on the right with the dyers' runnel beside
// it, the low harbour wall running out to the left with a candle in every
// window, and the drowned town's chimneys beyond. Its lantern flickers,
// the runnel runs in three dyes, the water glints, dawn brings the fog,
// and the two birds stand on the deck with the bones over their heads.

// The rail painted in each light, finished art like the sump's: the same
// picture to the pixel, fitted to 384x128 at 40 colours. Replace one whole.
//
//go:embed rail*.png
var railFS embed.FS

// railArts are the rail's paintings by light, read once each.
var railArts [4]func() *sumpArt

func init() {
	for light := range railArts {
		railArts[light] = sync.OnceValue(func() *sumpArt {
			name := "rail" + lightNames[light]
			b, err := railFS.ReadFile(name + ".png")
			if err != nil {
				panic("pp: no painting " + name)
			}
			img := decodeArt(b, name)
			deep := hexOf(img.At(200, 72))
			return &sumpArt{
				img: img, shade: lightShade[light], mist: lightMist[light],
				water: [5]uint32{deep, blend(deep, 0xFFFFFF, 0.12), blend(deep, 0xFFFFFF, 0.3), blend(deep, 0xFFFFFF, 0.6), blend(deep, 0xFFFFFF, 0.85)},
			}
		})
	}
}

// The rail's own places.
var (
	// harbour is the strip of harbour water between the shingle and the
	// wall, where the glints are.
	harbour = image.Rect(20, 66, 300, 79)
	// runnel is the dyers' gutter beside the gut, from where it shows to
	// where it spills into the harbour, and the fan its dyes spread in.
	runnelTop, runnelFoot = image.Pt(282, 42), image.Pt(279, 74)
	runnelFan             = image.Rect(268, 74, 300, 80)
	// railBirds are where the two birds stand on the deck, the middle of
	// each, either side of the lantern's post.
	railBirds = [2]int{96, 262}
	// railLantern is the lantern on the rail's post, and railLight its
	// light on the harbour water under it.
	railLantern = image.Rect(156, 52, 174, 72)
	railLight   = image.Rect(120, 66, 210, 79)
	// railTown is the cliff houses' lit windows up the gut, and
	// railCustoms the customs house on the end of the wall.
	railTown    = image.Rect(214, 0, 362, 72)
	railCustoms = image.Rect(60, 20, 112, 62)
)

// lanternFlame is the lantern's candle at a step: as painted, guttering
// low, or flaring. By day it's out, and nothing changes.
func lanternFlame(c *pixel.Canvas, art *sumpArt, step int) {
	toward, by := [3]uint32{0, 0x5A2A0C, 0xFFF0B8}[step], [3]float64{0, 0.35, 0.3}[step]
	if by == 0 {
		return
	}
	for y := railLantern.Min.Y; y < railLantern.Max.Y; y++ {
		for x := railLantern.Min.X; x < railLantern.Max.X; x++ {
			if col := art.img.At(x, y); lit(col) {
				c.SetColorIndex(x, y, c.Dye(blend(hexOf(col), toward, by)))
			}
		}
	}
}

// The dyers' three colours, as they come down the runnel: tallow yellow,
// hide red and the blue the yard boils for the stair.
var runnelDyes = [3]uint32{0xD9A030, 0xA8322A, 0x2F5FA8}

// Rail is what a call-out's screen shows.
type Rail struct {
	Light        int
	Bell, Thrown bool
	// Seats are the two birds, the caller first, as many as have stood.
	Seats []TableSeat
	// Called is the name of the bird called out, for while they haven't
	// come to the rail.
	Called string
	Slate  string
}

// RailScreen draws a call-out at the rail, moving: see railFrames.
func RailScreen(r Rail) ([]byte, error) {
	frames, beats := railFrames(r)
	return pixel.Animate(frames, beats, screenScale)
}

// railFrames are the rail's frames and their beats: the sump moving
// beyond the rail with the birds on the deck; once thrown, the bones
// tumbling over each bird first, then landing.
func railFrames(r Rail) ([]*pixel.Canvas, []int) {
	art := railArts[r.Light]()
	base := pixel.New(screenW, screenH)
	base.Picture(art.img, image.Point{}, 1)
	for i, p := range r.Seats {
		if i >= len(railBirds) {
			break
		}
		fig := cutout(Portrait(p.Class))
		sitDownIn(base, fig, image.Pt(railBirds[i]-len(fig[0])/2, sumpSize.Y-len(fig)))
	}
	railPanel(base, r)

	// The still: the runnel's dyes where they run, the fog where it lies.
	runnelFlow(base, art, 0)
	if thick := fogThick[r.Light]; thick > 0 {
		fog(base, art, 0, thick)
	}
	var frames []*pixel.Canvas
	var beats []int
	if r.Thrown {
		for k, at := range tumble {
			c := base.Copy()
			for i, p := range r.Seats {
				n := (p.Throw*7 + k*31 + i*13) % 100
				tens, ones := faces(n)
				pair(c, railDice(i).Add(image.Pt(at.dx, at.dy)), tens, ones, k%2 == 1)
			}
			frames, beats = append(frames, c), append(beats, tumbleBeat)
		}
		for i, p := range r.Seats {
			tens, ones := faces(p.Throw)
			pair(base, railDice(i), tens, ones, false)
		}
	}
	// What's alive is the lantern on the rail and its light on the water
	// below it, taking turns.
	for _, c := range ambient(base, []mover{
		{railLantern, func(c *pixel.Canvas, step int) { lanternFlame(c, art, step) }},
		{railLight, func(c *pixel.Canvas, step int) { harbourGlints(c, art, step*3) }},
		{railTown, func(c *pixel.Canvas, step int) { windows(c, art.img, railTown, step) }},
		{railCustoms, func(c *pixel.Canvas, step int) { windows(c, art.img, railCustoms, step) }},
	}) {
		frames, beats = append(frames, c), append(beats, sumpBeat)
	}
	return frames, beats
}

// railDice is where bird i's dice come to rest, over their head.
func railDice(i int) image.Point {
	return image.Pt(railBirds[i]-pairSize.X/2, sumpSize.Y-48-6-pairSize.Y)
}

// harbourGlints are the lamps' and the sky's light on the harbour water,
// lit and gone again.
func harbourGlints(c *pixel.Canvas, art *sumpArt, f int) {
	crest, glint := c.Dye(art.water[2]), c.Dye(art.water[4])
	for y := harbour.Min.Y; y < harbour.Max.Y; y++ {
		for x := harbour.Min.X; x < harbour.Max.X-2; x++ {
			if h := hash(x, y); h%41 == 0 && int(h>>8)%sumpFrames == f && wet(art.img.At(x, y)) {
				c.SetColorIndex(x, y, crest)
				c.SetColorIndex(x+1, y, glint)
			}
		}
	}
}

// runnelFlow runs the dyers' three colours down the runnel a frame at a
// time, in bands that slide down it, and spreads them out over the
// harbour where it spills.
func runnelFlow(c *pixel.Canvas, art *sumpArt, f int) {
	// Each dye is seen through the gutter's own water and the light of the
	// hour: mixed into what's painted there, never laid on top of it.
	at := func(x, y, k int) uint8 {
		return c.Dye(blend(hexOf(art.img.At(x, y)), shaded(runnelDyes[k], art.shade), 0.5))
	}
	n := runnelFoot.Y - runnelTop.Y
	for k := 0; k <= n; k++ {
		y := runnelTop.Y + k
		x := runnelTop.X + (runnelFoot.X-runnelTop.X)*k/max(n, 1)
		band := ((k-f*2)/4%3 + 3) % 3
		c.SetColorIndex(x, y, at(x, y, band))
	}
	// On the water the dyes fan out in streaks that drift and thin.
	for y := runnelFan.Min.Y; y < runnelFan.Max.Y; y++ {
		spread := (y - runnelFan.Min.Y + 1) * 2
		for x := runnelFoot.X - spread; x <= runnelFoot.X+spread; x++ {
			if !image.Pt(x, y).In(runnelFan) {
				continue
			}
			w := math.Sin(float64(x)/3 + float64(f)*math.Pi/4 + float64(y))
			if w < 0.5 || hash(x, y+f)%2 == 0 {
				continue
			}
			c.SetColorIndex(x, y, at(x, y, (x/3+y)%3))
		}
	}
}

// railPanel is the purser's tally of the call-out under the rail: each
// bird's name, stake and what the bones paid, the slate in the corner and,
// once thrown, who takes the honour.
func railPanel(c *pixel.Canvas, r Rail) {
	box := image.Rect(4, sumpSize.Y+4, screenW-4, screenH-4)
	c.Window(box)
	tab := "duel at the rail"
	if r.Bell {
		tab = "duel in the drowning hour"
	}
	c.Tab(box, tab)
	if r.Slate != "" {
		word := "slate to the wall"
		if r.Thrown {
			word = "slate turned"
		}
		at := rightIn(c, pixel.Regular, box.Max.X-10, box.Min.Y+5, r.Slate[:min(len(r.Slate), 8)], pixel.Dim)
		rightIn(c, pixel.Light, at-labelGap, box.Min.Y+5, word, pixel.Dim)
	}
	mid := box.Min.X + box.Dx()/2
	c.Fill(image.Rect(mid, paneTop(box)-5, mid+1, box.Max.Y-6), pixel.Bone)
	best := -1
	if r.Thrown && len(r.Seats) == 2 {
		best = 0
		if r.Seats[1].Throw > r.Seats[0].Throw {
			best = 1
		}
	}
	y := paneTop(box) - 2
	for i := range 2 {
		left, right := box.Min.X+12, mid-10
		if i == 1 {
			left, right = mid+10, box.Max.X-12
		}
		if i >= len(r.Seats) {
			word := "waiting at the rail"
			if r.Called != "" {
				word = "waiting on " + r.Called
			}
			label(c, left, y+8, fitName(pixel.Light, word, right-left))
			continue
		}
		p := r.Seats[i]
		ink := pixel.Ink
		if i == best {
			ink = pixel.Gold
		}
		c.Print(pixel.Bold, left, y, fitName(pixel.Bold, pixel.Plain(p.Name), right-left), ink)
		c.Icon(stakeIcon(p.Stake), image.Pt(left, y+18-iconLift))
		line, lineInk := grouped(p.Stake/unit)+" pp", pixel.Gold
		if r.Thrown {
			line, lineInk = "threw "+strconv.Itoa(p.Throw)+", lost", pixel.Red
			if p.Won > 0 {
				line, lineInk = "threw "+strconv.Itoa(p.Throw)+", +"+grouped(p.Won/unit), pixel.Green
			}
		}
		rightIn(c, pixel.Regular, right, y+18, line, lineInk)
		if i == best {
			rightIn(c, pixel.Light, right, y+36, "takes the honour", pixel.Gold)
		} else if r.Thrown && p.Won == 0 {
			rightIn(c, pixel.Light, right, y+36, "the stake goes to the house", pixel.Dim)
		}
	}
}
