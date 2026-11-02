package pp

import (
	"image"
	"math"
	"strconv"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The first roll (LORE.md, the first roll): when the first season was
// called, the counting house closed its books on the preseason and pinned
// a ribbon on the birds who had poured the most into the pot, their place
// stamped on it. It's taken once, at the turn into season 1, and never
// again.

// firstRollPlaces is how many places the first roll holds. Its ribbons
// are worn through the first week of the first season; the places are
// kept after it, off the status screen.
const firstRollPlaces = 10

// wornRoll is whether the first roll's ribbon is worn in season's week.
func wornRoll(season, week int64) bool { return season == 1 && week == 1 }

// rollFirst takes the first roll of s: every member's place on the level
// ladder as the preseason left it, kept for the first firstRollPlaces. It
// runs at the turn into the first season, before that day's first earning.
// s.mu held.
func (s *server) rollFirst() {
	var users []*member
	var totals []int64
	for _, m := range s.members {
		users = append(users, m)
		totals = append(totals, m.total)
	}
	for i, place := range Places(totals) {
		if place > 0 && place <= firstRollPlaces {
			users[i].firstRoll, users[i].dirty = int64(place), true
		}
	}
	s.dirty = true
}

// The ribbon is a painting, sprites/roll.png: a pewter rosette with an
// empty dark middle over two sea-green tails, each with a bone stripe. It's
// drawn the way a sprite of the time was, every material on a hard ramp of
// a few inks under a dark outline: the rosette dyed the place's metal by
// how light each of its pixels is, the cloth and the stripe kept to their
// own ramps, the middle flat, and the place stamped on it.

// The ramps, darkest first.
var (
	rollCloth  = []uint32{0x1C3A36, 0x2F5D58, 0x4A7F78}
	rollStripe = []uint32{0xB5AC9A, 0xE3DCCB}
	rollMetals = [][]uint32{
		{0x5C3D12, 0x9A6B1F, 0xC8962E, 0xF0CF6A}, // gold
		{0x4A4E57, 0x7C818B, 0xAEB3BC, 0xE2E5EA}, // silver
		{0x4A2814, 0x7A4520, 0xA9672F, 0xD89A60}, // bronze
		{0x6B6458, 0x9C9483, 0xC9C0AE, 0xEDE7DB}, // bone, for the rest
	}
)

const (
	rollCentre  = 0x1E2328
	rollOutline = 0x16110D
	// rollTails is the painting's first row of tails, under the rosette.
	rollTails = 23
)

// rollMiddle is the middle of the rosette's dark disc in the painting, and
// rollDisc its radius.
var (
	rollMiddle = image.Pt(12, 11)
	rollDisc   = 6.3
)

// rollMetal is the rim's ramp for place: gold, silver and bronze for the
// first three, bone for the rest.
func rollMetal(place int) []uint32 { return rollMetals[min(max(place, 1), 4)-1] }

// rollAt is where the ribbon's painting is pinned: its top left, in the
// gutter right of the portrait, clear of the frame's corner.
var rollAt = image.Pt(113, 32)

// step is which of n inks a pixel of lightness l (0-255) takes, the edges
// between them at cut.
func step(l int, cut ...int) int {
	for i, c := range cut {
		if l < c {
			return i
		}
	}
	return len(cut)
}

// drawRoll pins the first roll's ribbon for place on c, its top left at p.
func drawRoll(c *pixel.Canvas, place int, p image.Point) {
	art := sprite("roll")
	b := art.Bounds()
	metal := rollMetal(place)
	// Each opaque pixel's ink, by material and step, then a pass that
	// gives a pixel alone among four of one ink that ink.
	inks := map[image.Point]uint32{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			col := art.At(x, y)
			if !opaque(col) {
				continue
			}
			rgb := hexOf(col)
			r, g := int(rgb>>16&0xFF), int(rgb>>8&0xFF)
			l := lum(col)
			q := image.Pt(x-b.Min.X, y-b.Min.Y)
			switch {
			case q.Y < rollTails && math.Hypot(float64(q.X-rollMiddle.X), float64(q.Y-rollMiddle.Y)) <= rollDisc:
				inks[q] = rollCentre
			case q.Y < rollTails && g <= r+15:
				inks[q] = metal[step(l, 85, 125, 170)]
			case g > r+15:
				inks[q] = rollCloth[step(l, 70, 105)]
			default:
				inks[q] = rollStripe[step(l, 175)]
			}
		}
	}
	for q, ink := range inks {
		var around []uint32
		for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			if n, ok := inks[q.Add(d)]; ok {
				around = append(around, n)
			}
		}
		if len(around) == 4 && around[0] != ink && around[0] == around[1] && around[1] == around[2] && around[2] == around[3] {
			inks[q] = around[0]
		}
	}
	outline := c.Dye(rollOutline)
	for q := range inks {
		for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			if _, ok := inks[q.Add(d)]; !ok {
				c.SetColorIndex(p.X+q.X+d.X, p.Y+q.Y+d.Y, outline)
			}
		}
	}
	for q, ink := range inks {
		c.SetColorIndex(p.X+q.X, p.Y+q.Y, c.Dye(ink))
	}
	n := strconv.Itoa(place)
	mid := p.Add(rollMiddle)
	c.Shaded(mid.X-pixel.Regular.Width(n)/2+1, mid.Y-pixel.LineHeight/2+1, n, c.Dye(metal[3]), c.Dye(rollCentre))
}
