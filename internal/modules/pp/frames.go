package pp

import (
	"fmt"
	"image"
	"strings"

	"github.com/6586x57890143/skua/internal/pixel"
)

// A portrait's frame on the status screen rises with the member's title:
// plain wood, then iron, bronze, silver, gilt and gilt set with rubies.
// A frame is built in layers so more can be hung on it later: a base, the
// band of a material all round the portrait, and trims, each drawn in one
// of a fixed set of slots. The band and the slots are the same size and in
// the same place at every tier, so a later trim (a season's laurel, a shop
// piece) drops into a slot on any frame without redrawing it.

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

// Trim is one piece hung on a frame: a shape in a metal, in a slot.
type Trim struct {
	Slot  Slot
	Shape string
	Metal Metal
}

// Frame is a portrait's frame: the band's material, whether it shows a
// grain, and the trims hung on it, drawn in order.
type Frame struct {
	Name  string
	Band  Metal
	Grain bool
	Trims []Trim
}

// frameBand is how wide the band is, outside the portrait's own rule.
const frameBand = 8

// corners are the four corner slots, for a trim on each.
var corners = []Slot{CornerTL, CornerTR, CornerBL, CornerBR}

// each is shape in metal on every one of in.
func each(in []Slot, shape string, metal Metal) []Trim {
	out := make([]Trim, len(in))
	for i, s := range in {
		out[i] = Trim{s, shape, metal}
	}
	return out
}

// Frames are the frame each title tier wears, by titleFrom's index.
var Frames = [len(titleFrom)]Frame{
	{Name: "a wooden frame", Band: Wood, Grain: true},
	{Name: "an iron frame", Band: Iron, Trims: each(corners, "rivet", Iron)},
	{Name: "a bronze frame", Band: Bronze, Trims: append(each(corners, "rivet", Bronze), each([]Slot{SideL, SideR}, "stud", Bronze)...)},
	{Name: "a silver frame", Band: Silver, Trims: append(each(corners, "boss", Silver), each([]Slot{SideL, SideR}, "stud", Silver)...)},
	{Name: "a gilt frame", Band: Gilt, Trims: append(append(each(corners, "boss", Gilt), each([]Slot{SideL, SideR}, "stud", Gilt)...),
		Trim{Crest, "crown", Gilt})},
	{Name: "a jewelled frame", Band: Gilt, Trims: append(append(append(append(each(corners, "boss", Gilt), each(corners, "gem", Ruby)...),
		each([]Slot{SideL, SideR}, "gem", Sapphire)...), Trim{Crest, "crown", Gilt}, Trim{Crest, "jewel", Ruby}), Trim{Plaque, "plaque", Gilt})},
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
	"boss": bevel(8, 8),
	"gem": {
		"...k...",
		"..k4k..",
		".k432k.",
		"k43321k",
		".k321k.",
		"..k1k..",
		"...k...",
	},
	"crown": {
		"..k.....k.....k..",
		".k4k...k4k...k4k.",
		".k3k..k443k..k3k.",
		"k432kk43332kk432k",
		"k433343333334332k",
		"k444444444444443k",
		"k433333333333332k",
		"k322222222222221k",
		"kkkkkkkkkkkkkkkkk",
	},
	"jewel": {
		".k.",
		"k4k",
		"k2k",
		".k.",
	},
	"plaque": bevel(23, 7),
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

// cast is shape's rows with its metal put in for 1 to 4 and its dots
// cleared to nothing the grid draws.
func cast(shape []string, m Metal) []string {
	out := make([]string, len(shape))
	for i, row := range shape {
		out[i] = strings.NewReplacer("1", string(m[0]), "2", string(m[1]), "3", string(m[2]), "4", string(m[3]), ".", " ").Replace(row)
	}
	return out
}

// slotAt is where slot s's centre falls on a frame whose band's outer
// edge is o.
func slotAt(o image.Rectangle, s Slot) image.Point {
	mid := o.Min.Add(o.Max).Div(2)
	half := frameBand / 2
	switch s {
	case CornerTL:
		return image.Pt(o.Min.X+half, o.Min.Y+half)
	case CornerTR:
		return image.Pt(o.Max.X-half, o.Min.Y+half)
	case CornerBL:
		return image.Pt(o.Min.X+half, o.Max.Y-half)
	case CornerBR:
		return image.Pt(o.Max.X-half, o.Max.Y-half)
	case SideL:
		return image.Pt(o.Min.X+half, mid.Y)
	case SideR:
		return image.Pt(o.Max.X-half, mid.Y)
	case Crest:
		return image.Pt(mid.X, o.Min.Y+1)
	}
	return image.Pt(mid.X, o.Max.Y-half)
}

// drawFrame draws f round a portrait whose own rule is r: the band, lit on
// its top and left and shaded on its foot and right, any grain, then every
// trim centred on its slot.
func drawFrame(c *pixel.Canvas, f Frame, r image.Rectangle) {
	o := r.Inset(-frameBand)
	c.Ground(o, pixel.IconInk('k'))
	// Across the band from its outer edge in: the rule, the metal rising to
	// its light and back down, and the rule against the portrait's.
	lit := "k" + string(f.Band[1]) + string(f.Band[2]) + string(f.Band[3]) + string(f.Band[2]) + string(f.Band[1]) + string(f.Band[0]) + "k"
	shade := "k" + string(f.Band[0]) + string(f.Band[1]) + string(f.Band[2]) + string(f.Band[1]) + string(f.Band[0]) + string(f.Band[0]) + "k"
	for k := range frameBand {
		ring := o.Inset(k)
		top, foot := pixel.IconInk(lit[k]), pixel.IconInk(shade[k])
		c.Fill(image.Rect(ring.Min.X, ring.Min.Y, ring.Max.X, ring.Min.Y+1), top)
		c.Fill(image.Rect(ring.Min.X, ring.Min.Y, ring.Min.X+1, ring.Max.Y), top)
		c.Fill(image.Rect(ring.Min.X+1, ring.Max.Y-1, ring.Max.X, ring.Max.Y), foot)
		c.Fill(image.Rect(ring.Max.X-1, ring.Min.Y+1, ring.Max.X, ring.Max.Y), foot)
	}
	if f.Grain {
		// Wood's grain: short dark runs along the middle of the band, at
		// steps that don't repeat evenly.
		dark := pixel.IconInk(f.Band[0])
		for x := o.Min.X + 6; x < o.Max.X-8; x += 9 + x%4 {
			c.Fill(image.Rect(x, o.Min.Y+3, x+4, o.Min.Y+4), dark)
			c.Fill(image.Rect(x+2, o.Max.Y-4, x+6, o.Max.Y-3), dark)
		}
		for y := o.Min.Y + 7; y < o.Max.Y-8; y += 10 + y%3 {
			c.Fill(image.Rect(o.Min.X+3, y, o.Min.X+4, y+4), dark)
			c.Fill(image.Rect(o.Max.X-4, y+3, o.Max.X-3, y+7), dark)
		}
	}
	// Trims in one slot stack, a gem set on a boss, so a slot is one mark.
	var hung [slots]image.Rectangle
	for _, t := range f.Trims {
		g := cast(Shapes[t.Shape], t.Metal)
		at := slotAt(o, t.Slot).Sub(image.Pt(len(g[0])/2, len(g)/2))
		hung[t.Slot] = hung[t.Slot].Union(c.Grid(g, at))
	}
	for s, r := range hung {
		if !r.Empty() {
			c.Marks = append(c.Marks, pixel.Mark{What: fmt.Sprintf("trim slot %d", s), R: r})
		}
	}
}
