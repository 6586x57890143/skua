package pp

import (
	"fmt"
	"image"
	"image/color"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The beam and the deckhead. Whoever throws highest at a table has their
// name chalked on the beam over the bar, and the beam is wiped at every
// new tide. Luck the slack tide has seen only a few times since the
// drowning puts a scrap of the bird's own colour on the deckhead, and
// nobody wipes the deckhead. Both are standing, never pp: no feat pays,
// so luck adds no reason to stake more than the bones already give.

// feats are the deckhead's scraps, in the order the regulars tell them,
// each a bit of member.feats.
var feats = []struct{ key, name, how string }{
	{"bell", "rang the bell", "a hundred in the drowning hour"},
	{"gannet", "the gannet's luck", "two hundreds in one day"},
	{"bottom", "back off the bottom", "a one, and a hundred on the very next throw"},
	{"nineties", "a run of nineties", "three throws in a row in the nineties"},
	{"bench", "a dry bench", "a full table of six, every bird on it winning on the same throw"},
	{"bells", "both bells", "a hundred in each drowning hour on a day the bell rings twice"},
	{"nod", "the dry one's nod", "two birds called out at the rail, and both threw a hundred"},
}

const (
	featBell = iota
	featGannet
	featBottom
	featNineties
	featBench
	featBells
	featNod
)

// luck is what the bones have done for a bird lately.
type luck struct {
	last     int     // their last throw, 0 for none yet
	nineties int     // throws in a row in the nineties
	day      int64   // the pp day hundreds and bells count
	hundreds int     // hundreds thrown on day
	bells    []int64 // the starts of day's bells they threw a hundred in
}

// earned is a scrap nailed up at a throw.
type earned struct {
	name string
	feat int
}

// beamMark is the beam over the bar: the day's highest throw and whose.
type beamMark struct {
	day   int64
	name  string
	throw int
}

// chalk puts t's highest throw on the beam if it beats today's, and
// wipes yesterday's first. s.mu held.
func (s *server) chalk(t *table, today int64) {
	if len(t.seats) == 0 {
		return
	}
	best := t.seats[0]
	for _, p := range t.seats[1:] {
		if p.throw > best.throw {
			best = p
		}
	}
	if s.beam.day != today || best.throw > s.beam.throw {
		s.beam = beamMark{day: today, name: best.name, throw: best.throw}
	}
}

// onBeam is today's name on the beam, "" when it's been wiped. s.mu
// held.
func (s *server) onBeam(today int64) (string, int) {
	if s.beam.day != today {
		return "", 0
	}
	return s.beam.name, s.beam.throw
}

// deckhead reads t's thrown bones for luck and nails up a scrap for every
// feat a bird hadn't yet, counting each. s.mu held.
func (st *state) deckhead(s *server, t *table, today int64) []earned {
	var got []earned
	nail := func(user snowflake.ID, name string, f int) {
		m := s.members[user]
		if m == nil || m.feats&(1<<f) != 0 {
			return
		}
		m.feats |= 1 << f
		m.dirty, s.dirty = true, true
		s.count(today, "feat_"+feats[f].key, 0, 0)
		got = append(got, earned{name: name, feat: f})
	}
	for _, p := range t.seats {
		m := s.members[p.user]
		if m == nil {
			continue
		}
		l := &m.luck
		if l.day != today {
			l.day, l.hundreds, l.bells = today, 0, nil
		}
		if p.throw == 100 {
			l.hundreds++
			if l.hundreds >= 2 {
				nail(p.user, p.name, featGannet)
			}
			if l.last == 1 {
				nail(p.user, p.name, featBottom)
			}
			if t.bell {
				nail(p.user, p.name, featBell)
				if at := t.bellAt.Unix(); !t.bellAt.IsZero() && !has(l.bells, at) {
					l.bells = append(l.bells, at)
				}
				if len(l.bells) >= 2 {
					nail(p.user, p.name, featBells)
				}
			}
		}
		if p.throw >= 90 && p.throw <= 99 {
			l.nineties++
			if l.nineties >= 3 {
				nail(p.user, p.name, featNineties)
			}
		} else {
			l.nineties = 0
		}
		l.last = p.throw
	}
	if n := len(t.seats); n == tableSeats && t.seats[0].won > 0 {
		same := true
		for _, p := range t.seats[1:] {
			same = same && p.throw == t.seats[0].throw
		}
		if same {
			for _, p := range t.seats {
				nail(p.user, p.name, featBench)
			}
		}
	}
	if t.rail != 0 && len(t.seats) == 2 && t.seats[0].throw == 100 && t.seats[1].throw == 100 {
		for _, p := range t.seats {
			nail(p.user, p.name, featNod)
		}
	}
	return got
}

func has(xs []int64, x int64) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// Scrap is one feat's place on the deckhead: whether anyone's nailed it
// up and in what colour, 0 for undyed wool.
type Scrap struct {
	Up    bool
	Color uint32
}

// scraps are the deckhead as s stands, a scrap per feat: the colour of
// the bird who first nailed each, by the order they came aboard, or
// blank where nobody has yet. s.mu held.
func (st *state) scraps(s *server, today int64) []Scrap {
	out := make([]Scrap, len(feats))
	first := make([]snowflake.ID, len(feats))
	for user, m := range s.members {
		for f := range feats {
			if m.feats&(1<<f) == 0 || (first[f] != 0 && user > first[f]) {
				continue
			}
			first[f] = user
			out[f] = Scrap{Up: true}
			if l := st.wearing(s, user, m, today); l.Name != "" && l.Left >= 0 {
				out[f].Color = l.Color
			}
		}
	}
	return out
}

// nailed is how many of the deckhead's scraps are up.
func nailed(sc []Scrap) int {
	n := 0
	for _, s := range sc {
		if s.Up {
			n++
		}
	}
	return n
}

// deckheadLine is the slack tide's word on the beam and the deckhead.
func deckheadLine(beam string, throw int, sc []Scrap) string {
	line := "-# the beam's wiped for the new tide"
	if beam != "" {
		line = fmt.Sprintf("-# on the beam today: **%s**, %d", beam, throw)
	}
	return line + fmt.Sprintf(" · %d of %d scraps on the deckhead", nailed(sc), len(feats))
}

// nailedLine is a settle's word on a scrap nailed up.
func nailedLine(e earned) string {
	return fmt.Sprintf("**%s** nails a scrap to the deckhead: *%s*. %s", e.name, feats[e.feat].name, feats[e.feat].how)
}

// The scraps' places along the taproom's deckhead: where each hangs from,
// spread over the benches between the bunting, and the carved timber
// behind the first.
var scrapAt = []image.Point{{40, 3}, {92, 5}, {146, 2}, {238, 4}, {290, 2}, {330, 5}, {364, 3}}

// hangScraps nails the deckhead's scraps up in the taproom, a sway a
// frame: each dyed its bird's colour, undyed wool for a bird with none,
// and a feat nobody has yet only a dark shape against the beams.
func hangScraps(c *pixel.Canvas, sc []Scrap, f int) {
	carved := sprite("carved")
	stamp(c, carved, scrapAt[0].Sub(image.Pt(carved.Bounds().Dx()/2, 0)), 0.7, c.Rect.Dy())
	for i, s := range sc {
		if i >= len(scrapAt) {
			break
		}
		img := sprite(fmt.Sprintf("scrap_%d", i))
		at := scrapAt[i].Sub(image.Pt(img.Bounds().Dx()/2, 0))
		// A draught along the deckhead: the cloth's foot moves a pixel.
		sway := []int{0, 0, 1, 1, 0, 0, -1, -1}[(f+i*3)%8]
		dyeScrap(c, img, at, s, sway)
	}
}

// dyeScrap draws the undyed scrap img at p, its greys taken as how light the
// cloth is and dyed s's colour; a scrap nobody's nailed up is drawn dark.
func dyeScrap(c *pixel.Canvas, img image.Image, p image.Point, s Scrap, sway int) {
	b := img.Bounds()
	// Where the cloth falls this frame, its foot swaying.
	cloth := map[image.Point]color.Color{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		dx := sway * (y - b.Min.Y) / max(b.Dy()-1, 1)
		for x := b.Min.X; x < b.Max.X; x++ {
			if col := img.At(x, y); opaque(col) {
				cloth[image.Pt(p.X+x-b.Min.X+dx, p.Y+y-b.Min.Y)] = col
			}
		}
	}
	// Each scrap stands off the painted bunting behind it: a nailed one
	// in shadow round its edge, a blank one only its edge, chalked.
	edge := c.Dye(0x120C08)
	if !s.Up {
		edge = c.Dye(0x8A8478)
	}
	for q := range cloth {
		for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			if n := q.Add(d); cloth[n] == nil && n.In(c.Rect) {
				c.SetColorIndex(n.X, n.Y, edge)
			}
		}
	}
	inks := map[color.Color]uint8{}
	for q, col := range cloth {
		i, ok := inks[col]
		if !ok {
			i = c.Dye(inDye(hexOf(col), s))
			inks[col] = i
		}
		c.SetColorIndex(q.X, q.Y, i)
	}
}

func opaque(col color.Color) bool { _, _, _, a := col.RGBA(); return a >= 0x8000 }

// inDye is the grey wool rgb in s's dye: its lightness carried over the
// colour, or for no dye the wool itself, or for a scrap not yet nailed a
// shape in the deckhead's shadow.
func inDye(rgb uint32, s Scrap) uint32 {
	switch {
	case !s.Up:
		return blend(rgb, 0x1A1410, 0.82)
	case s.Color == 0:
		return rgb
	}
	l := float64(lum(color.RGBA{uint8(rgb >> 16), uint8(rgb >> 8), uint8(rgb), 255})) / 255
	ch := func(sh uint) uint32 { return uint32(min(255, float64(s.Color>>sh&0xFF)*(0.35+0.9*l))) << sh }
	return ch(16) | ch(8) | ch(0)
}
