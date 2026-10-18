package pp

import (
	"fmt"
	"image"
	"strings"

	"github.com/6586x57890143/skua/internal/pixel"
)

// A portrait's frame on the status screen rises with the member's title:
// plain wood, then iron, bronze, silver, gilt and diamond.
// A frame is built in layers so more can be hung on it later: a base, the
// thin band of a material all round the portrait, and trims, each hung in
// one of a fixed set of slots. A slot is a place against the portrait's
// edge, outside the band, the same at every tier, so a trim can be larger
// than the band, and a later one (a season's laurel, a shop piece) hangs on
// any frame without redrawing it.

// Metal is a material as four IconInks letters, darkest first.
type Metal string

// The materials frames are made of.
const (
	Wood     Metal = "IJKl"
	Iron     Metal = "EFGH"
	Bronze   Metal = "AKBl"
	Silver   Metal = "FGHw"
	Gilt     Metal = "ABCD"
	Ruby     Metal = "LMNO"
	Sapphire Metal = "TUVW"
	Diamond  Metal = "GVWw"
)

// Slot is a place on a frame a trim hangs.
type Slot int

// The slots: the four corners, the middle of either side, the crest over
// the top and the plaque under the foot.
const (
	CornerTL Slot = iota
	CornerTR
	CornerBL
	CornerBR
	SideL
	SideR
	Crest
	Plaque
	slots
)

// Trim is one piece hung on a frame: a shape in a metal, in a slot. A
// trim hangs outside the band, against the portrait, unless it's Set into
// the frame: then it sits on the band, centred on it, and may reach a
// little over the portrait the way a stone set in a frame's corner does.
type Trim struct {
	Slot  Slot
	Shape string
	Metal Metal
	Set   bool
}

// Frame is a portrait's frame: the band's material, its surface, and the
// trims hung on it, drawn in order. Surface is a row of IconInks letters
// for each of the band's metal rows, outer first, repeated along the band:
// a grain or the facets of a cut stone. Without one, the band is lit on its
// top and left and shaded on its foot and right.
type Frame struct {
	Name    string
	Band    Metal
	Surface [frameBand - 1]string
	Trims   []Trim
}

// frameBand is how wide the band is: a dark rule, then the metal.
const frameBand = 3

// corners are the four corner slots, for a trim on each.
var corners = []Slot{CornerTL, CornerTR, CornerBL, CornerBR}

// sides are the two side slots.
var sides = []Slot{SideL, SideR}

// each is shape in metal on every one of in.
func each(in []Slot, shape string, metal Metal) []Trim {
	out := make([]Trim, len(in))
	for i, s := range in {
		out[i] = Trim{s, shape, metal, false}
	}
	return out
}

// set is trims set into the frame.
func set(trims ...[]Trim) []Trim {
	var out []Trim
	for _, ts := range trims {
		for _, t := range ts {
			t.Set = true
			out = append(out, t)
		}
	}
	return out
}

// Frames are the frame each title tier wears, by titleFrom's index.
var Frames = [len(titleFrom)]Frame{
	{Name: "a wooden frame", Band: Wood, Surface: [2]string{"lKKKKlKKKKKKlKK", "JJJIJJJJJIIJJJJJJ"}},
	{Name: "an iron frame", Band: Iron, Trims: each(corners, "rivet", Iron)},
	{Name: "a bronze frame", Band: Bronze, Trims: append(append(each(corners, "rivet", Bronze), each(sides, "stud", Bronze)...),
		Trim{Crest, "finial", Bronze, false})},
	{Name: "a silver frame", Band: Silver, Trims: set(each(corners, "boss", Silver), each(sides, "stud", Silver),
		[]Trim{{Crest, "fleuron", Silver, false}})},
	{Name: "a gilt frame", Band: Gilt, Trims: set(each(corners, "boss", Gilt), each(corners, "gem", Ruby), each(sides, "gem", Ruby),
		[]Trim{{Crest, "crown", Gilt, false}, {Crest, "jewel", Ruby, false}, {Plaque, "plaque", Gilt, false}})},
	{Name: "a diamond frame", Band: Diamond, Surface: [2]string{"wWWVwWVW", "VWGVWVGW"},
		Trims: set(each(corners, "brilliant", Diamond), each(sides, "cut", Diamond),
			[]Trim{{Crest, "great crown", Diamond, false}, {Crest, "jewel", Sapphire, false}, {Plaque, "plaque", Diamond, false}})},
}

// frameOf is the frame level l's title wears.
func frameOf(l int) Frame {
	f := Frames[0]
	for i, from := range titleFrom {
		if l >= from {
			f = Frames[i]
		}
	}
	return f
}

// Shapes are the trims' drawings: IconInks letters, but 1 to 4 stand for
// the trim's metal, darkest first, and a dot is left clear.
var Shapes = map[string][]string{
	"rivet": {
		".kkk.",
		"k432k",
		"k321k",
		"k211k",
		".kkk.",
	},
	"stud": {
		"..k..",
		".k4k.",
		"k432k",
		"k321k",
		".k1k.",
		"..k..",
	},
	"boss": bevel(7, 7),
	"gem":  stone(3),
	// The diamond's stones are drawn cut, a table ringed by its girdle and
	// facets catching the light from the top left, in their own inks.
	"brilliant": {
		"....kkkkk....",
		"..kkwWVUTkk..",
		".kwwWGVUTUTk.",
		".kwWGGGGGTUk.",
		"kWWGwwWWVGUTk",
		"kwWGwWWVVGTUk",
		"kVwGWWVVUGUWk",
		"kUVGWVVUUGWwk",
		"kTUGVVUUTGwWk",
		".kTUGGGGGwWk.",
		".kUTUVGWwwWk.",
		"..kkTUVWwkk..",
		"....kkkkk....",
	},
	"cut": {
		"...kkk...",
		".kkwVTkk.",
		".kwGGGUk.",
		"kWGwWVGTk",
		"kwGWVUGUk",
		"kUGVUTGwk",
		".kTGGGwk.",
		".kkUWwkk.",
		"...kkk...",
	},
	"jewel": stone(3),
	"finial": {
		"...kkk...",
		"..k443k..",
		"..k432k..",
		"...k2k...",
		".kk434kk.",
		"k4433322k",
		"kkkkkkkkk",
	},
	"fleuron": {
		"......kkk......",
		".....k443k.....",
		"..kk.k432k.kk..",
		".k43kk432kk43k.",
		".k432k432k432k.",
		"..k3kk432kk3k..",
		"...kk43332kk...",
		".kk444444433kk.",
		"k4433333333322k",
		"kkkkkkkkkkkkkkk",
	},
	"crown":       crown(33, 15),
	"great crown": crown(41, 20),
	"plaque":      bevel(25, 6),
}

// bevel is a plate w by h: lit along its top and left, shaded along its
// foot and right, inside a dark rule.
func bevel(w, h int) []string {
	rows := make([]string, h)
	for y := range h {
		var b strings.Builder
		for x := range w {
			switch {
			case x == 0 || y == 0 || x == w-1 || y == h-1:
				b.WriteByte('k')
			case x == 1 || y == 1:
				b.WriteByte('4')
			case x == w-2 || y == h-2:
				b.WriteByte('2')
			default:
				b.WriteByte('3')
			}
		}
		rows[y] = b.String()
	}
	return rows
}

// stone is a cut stone r cells from its centre to each point: a dark rule
// round a rhombus, its facets lit from the top left, a glint by the top.
func stone(r int) []string {
	rows := make([]string, 2*r+1)
	for y := range 2*r + 1 {
		var b strings.Builder
		for x := range 2*r + 1 {
			dx, dy := x-r, y-r
			d := abs(dx) + abs(dy)
			light := -dx - dy // from the top left
			switch {
			case d > r:
				b.WriteByte('.')
			case d == r:
				b.WriteByte('k')
			case dx == -1 && dy == -r/2 && r > 2:
				b.WriteByte('w')
			case dy == 0 || dx == 0:
				// The table's edges, a step down from what's either side.
				b.WriteByte("2233"[min(max(light+2, 0), 3)])
			case light > r/2:
				b.WriteByte('4')
			case light > 0:
				b.WriteByte('3')
			case light > -r/2:
				b.WriteByte('2')
			default:
				b.WriteByte('1')
			}
		}
		rows[y] = b.String()
	}
	return rows
}

// crown is a crown w by h: five points rising off a jewelled band, the
// middle point tallest, each tipped with a glint.
func crown(w, h int) []string {
	base := h - 6 // the band's top row
	points := []int{2, w / 4, w / 2, w - 1 - w/4, w - 3}
	top := func(x int) int {
		t := base
		for i, cx := range points {
			peak := 2
			if i == 2 {
				peak = 0
			}
			t = min(t, peak+2*abs(x-cx))
		}
		return t
	}
	in := func(x, y int) bool { return x >= 0 && x < w && y < h && y >= top(x) }
	rows := make([]string, h)
	for y := range h {
		var b strings.Builder
		for x := range w {
			nearest := points[0]
			for _, cx := range points {
				if abs(x-cx) < abs(x-nearest) {
					nearest = cx
				}
			}
			switch {
			case !in(x, y):
				b.WriteByte('.')
			case !in(x-1, y) || !in(x+1, y) || !in(x, y-1) || y == h-1:
				b.WriteByte('k')
			case y < base && x == nearest && y == top(x)+1:
				b.WriteByte('w')
			case y < base && x <= nearest:
				b.WriteByte('4')
			case y < base:
				b.WriteByte('3')
			case y == base:
				b.WriteByte('k')
			case y == base+1:
				b.WriteByte('4')
			case y == h-2:
				b.WriteByte('2')
			default:
				b.WriteByte('3')
			}
		}
		rows[y] = b.String()
	}
	return rows
}

func abs(n int) int { return max(n, -n) }

// cast is shape's rows with its metal put in for 1 to 4 and its dots
// cleared to nothing the grid draws.
func cast(shape []string, m Metal) []string {
	out := make([]string, len(shape))
	for i, row := range shape {
		out[i] = strings.NewReplacer("1", string(m[0]), "2", string(m[1]), "3", string(m[2]), "4", string(m[3]), ".", " ").Replace(row)
	}
	return out
}

// hang is where a trim w by h goes in slot s, against p, the portrait's
// own rule: outside it, the corners hugging its corners, the sides
// centred on its sides, the crest standing on its top and the plaque
// hanging under its foot. Larger trims reach further out, never in. A set
// trim is centred on the band instead, and a set crest or plaque stands
// on the band, its foot a little over the portrait's rule.
func hang(p image.Rectangle, s Slot, w, h int, set bool) image.Point {
	mid := p.Min.Add(p.Max).Div(2)
	if set {
		band := p.Inset(-frameBand / 2).Inset(-1) // the band's middle line
		far := band.Max.Sub(image.Pt(1, 1))
		switch s {
		case CornerTL:
			return band.Min.Sub(image.Pt(w/2, h/2))
		case CornerTR:
			return image.Pt(far.X-w/2, band.Min.Y-h/2)
		case CornerBL:
			return image.Pt(band.Min.X-w/2, far.Y-h/2)
		case CornerBR:
			return far.Sub(image.Pt(w/2, h/2))
		case SideL:
			return image.Pt(band.Min.X-w/2, mid.Y-h/2)
		case SideR:
			return image.Pt(far.X-w/2, mid.Y-h/2)
		case Crest:
			return image.Pt(mid.X-w/2, p.Min.Y+2-h)
		}
		return image.Pt(mid.X-w/2, p.Max.Y-2)
	}
	switch s {
	case CornerTL:
		return image.Pt(p.Min.X-w, p.Min.Y-h)
	case CornerTR:
		return image.Pt(p.Max.X, p.Min.Y-h)
	case CornerBL:
		return image.Pt(p.Min.X-w, p.Max.Y)
	case CornerBR:
		return p.Max
	case SideL:
		return image.Pt(p.Min.X-w, mid.Y-h/2)
	case SideR:
		return image.Pt(p.Max.X, mid.Y-h/2)
	case Crest:
		return image.Pt(mid.X-w/2, p.Min.Y-h)
	}
	return image.Pt(mid.X-w/2, p.Max.Y)
}

// drawBand draws f's band round a portrait whose own rule is p, before the
// portrait.
func drawBand(c *pixel.Canvas, f Frame, p image.Rectangle) {
	o := p.Inset(-frameBand)
	c.Ground(o, pixel.IconInk('k'))
	lit := string(f.Band[3]) + string(f.Band[2])
	shade := string(f.Band[1]) + string(f.Band[0])
	for k := 1; k < frameBand; k++ {
		ring := o.Inset(k)
		at := func(along int, side string) uint8 {
			if s := f.Surface[k-1]; s != "" {
				return pixel.IconInk(s[along%len(s)])
			}
			return pixel.IconInk(side[k-1])
		}
		for x := ring.Min.X; x < ring.Max.X; x++ {
			c.SetColorIndex(x, ring.Min.Y, at(x, lit))
			c.SetColorIndex(x, ring.Max.Y-1, at(x+3, shade))
		}
		for y := ring.Min.Y + 1; y < ring.Max.Y-1; y++ {
			c.SetColorIndex(ring.Min.X, y, at(y, lit))
			c.SetColorIndex(ring.Max.X-1, y, at(y+3, shade))
		}
	}
}

// drawTrims hangs f's trims round a portrait whose own rule is p, after
// the portrait, so a trim set into the frame lies over its edge.
func drawTrims(c *pixel.Canvas, f Frame, p image.Rectangle) {
	// Trims in one slot stack, a stone set on a crown, centred on the
	// largest, so a slot is one mark.
	var hung [slots]image.Rectangle
	var size [slots]image.Point
	var setIn [slots]bool
	for _, t := range f.Trims {
		setIn[t.Slot] = setIn[t.Slot] || t.Set
		g := Shapes[t.Shape]
		size[t.Slot] = image.Pt(max(size[t.Slot].X, len(g[0])), max(size[t.Slot].Y, len(g)))
	}
	for _, t := range f.Trims {
		g := cast(Shapes[t.Shape], t.Metal)
		box := image.Rectangle{hang(p, t.Slot, size[t.Slot].X, size[t.Slot].Y, setIn[t.Slot]), image.Point{}}
		box.Max = box.Min.Add(size[t.Slot])
		at := box.Min.Add(box.Size().Sub(image.Pt(len(g[0]), len(g))).Div(2))
		if t.Slot == Crest {
			// A stone on a crown sits in its band, not its middle.
			at.Y = box.Max.Y - len(g) - 1
		}
		hung[t.Slot] = hung[t.Slot].Union(c.Grid(g, at))
	}
	for s, r := range hung {
		if !r.Empty() {
			c.Marks = append(c.Marks, pixel.Mark{What: fmt.Sprintf("trim slot %d", s), R: r})
		}
	}
}
