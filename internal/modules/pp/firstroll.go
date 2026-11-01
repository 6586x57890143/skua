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

// firstRollPlaces is how many places the first roll holds.
const firstRollPlaces = 10

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

// The ribbon's inks: the sea-green cloth the counting house pins, a bone
// stripe down each tail, and its rim the metal of the place.
const (
	rollCloth  = 0x2F5D58
	rollShade  = 0x1F3F3C
	rollStripe = 0xD9D2C4
	rollCentre = 0x1E2328
)

// rollRim is the rosette's rim for place: gold, silver and bronze for the
// first three, bone for the rest.
func rollRim(place int) (rim, light uint32) {
	switch place {
	case 1:
		return 0xC8962E, 0xF0CF6A
	case 2:
		return 0x9EA3AC, 0xDCE0E6
	case 3:
		return 0x9A5A2E, 0xD08A52
	}
	return 0xB9B0A0, 0xE8E2D6
}

// rollAt is where the ribbon is pinned: in the gutter right of the
// portrait, its rosette's middle.
var rollAt = image.Pt(125, 46)

// drawRoll pins the first roll's ribbon for place on c, its rosette's
// middle at p: two swallowtail tails, the scalloped rosette in the
// place's metal, and the place on its dark middle.
func drawRoll(c *pixel.Canvas, place int, p image.Point) {
	cloth, shade, stripe := c.Dye(rollCloth), c.Dye(rollShade), c.Dye(rollStripe)
	rimRGB, lightRGB := rollRim(place)
	rim, light, centre := c.Dye(rimRGB), c.Dye(lightRGB), c.Dye(rollCentre)

	// The tails, hanging apart from under the rosette, a stripe down each
	// and a swallowtail cut at the foot.
	const tailW, tailH = 7, 28
	for side, x0 := range []int{p.X - tailW, p.X} {
		for y := 0; y < tailH; y++ {
			slant := y / 10 // they part a little as they fall
			if side == 0 {
				slant = -slant
			}
			for x := 0; x < tailW; x++ {
				// The swallowtail: a notch cut up the middle of the foot,
				// widening to the hem.
				if k := y - (tailH - 4); k >= 0 && abs(2*x-(tailW-1)) <= k {
					continue
				}
				ink := cloth
				switch {
				case x == tailW/2:
					ink = stripe
				case side == 0 && x == 0, side == 1 && x == tailW-1:
					ink = shade // the fold on the outer edge
				}
				c.SetColorIndex(x0+x+slant, p.Y+6+y, ink)
			}
		}
	}

	// The rosette: a scalloped rim of twelve petals round a ring of cloth
	// and the dark middle the place is stamped on.
	const outer, ring, middle = 11.6, 8.6, 7.4
	for dy := -13; dy <= 13; dy++ {
		for dx := -13; dx <= 13; dx++ {
			d := math.Hypot(float64(dx), float64(dy))
			a := math.Atan2(float64(dy), float64(dx))
			petal := outer - 1.2*(1-math.Cos(12*a))/2
			var ink uint8
			switch {
			case d > petal+1:
				continue
			case d > petal:
				ink = shade // a dark edge, so a bone rim stands off the parchment
			case d > ring:
				ink = rim
				if dy < 0 && dx < 0 {
					ink = light // the light on the rim, from the top left
				}
			case d > middle:
				ink = cloth
			default:
				ink = centre
			}
			c.SetColorIndex(p.X+dx, p.Y+dy, ink)
		}
	}
	n := strconv.Itoa(place)
	c.Shaded(p.X-pixel.Regular.Width(n)/2, p.Y-pixel.LineHeight/2, n, light, centre)
}
