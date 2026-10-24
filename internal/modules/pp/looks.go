package pp

import (
	"image"
	"strconv"

	"github.com/6586x57890143/skua/internal/pixel"
)

// A rung of the rookery stair buys a look: how the member's name is set
// wherever others read it, the boards and their status. Each rung keeps
// what the one under it gave and adds to it, so the stair reads as a
// climb from across the room.

// Look is how a member's name is set.
type Look struct {
	Face  *pixel.Face
	Ink   uint8
	Plate uint8  // the ground behind their line, 0 for none
	Frame bool   // a gilt rule round the plate
	Perch string // their perch's icon after the name, "" for none
	Count int64  // the rung, shown past the last perch
	// Tint is the plate's own shade past the great nothing, 0 for Plate's.
	Tint uint32
}

// paragonTints are the plates past the great nothing, a dark shade a rung
// in turn, each near enough to black for the gilt to read on.
var paragonTints = []uint32{0x2a2433, 0x1f2d2e, 0x2e2620, 0x24262e, 0x2b2028, 0x232b22}

// lookOf is the look rung buys: bold with the perch beside it, so the
// first rung already shows, then slate, then chitin red, then a pale glass
// plate, then a bronze plate under the book hand, then bone white on a
// cold sea, and at the top gilt on a dark plate in a gilt frame.
func lookOf(rung int64) Look {
	l := Look{Face: pixel.Regular, Ink: pixel.Ink}
	if rung >= 1 {
		l.Face, l.Perch = pixel.Bold, perchOf(rung).icon
	}
	if rung >= 2 {
		l.Ink = pixel.Slate
	}
	if rung >= 3 {
		l.Ink = pixel.Red
	}
	if rung >= 4 {
		l.Plate = pixel.Glass
	}
	if rung >= 5 {
		l.Plate, l.Face, l.Ink = pixel.Bronze, pixel.Serif, pixel.Ink
	}
	if rung >= 6 {
		l.Plate, l.Ink = pixel.Sea, pixel.Glove
	}
	if rung >= 7 {
		l.Plate, l.Ink, l.Frame = pixel.Line, pixel.Gilt, true
	}
	if rung > int64(len(perches)) {
		l.Count = rung
		l.Tint = paragonTints[(rung-int64(len(perches))-1)%int64(len(paragonTints))]
	}
	return l
}

// setName sets name in look l at x on the line y, within width, its plate
// behind row. It returns where the name, perch and count end.
func setName(c *pixel.Canvas, l Look, x, y int, name string, width int, row image.Rectangle) int {
	if l.Plate != 0 {
		ground := l.Plate
		if l.Tint != 0 {
			ground = c.Dye(l.Tint)
		}
		c.Ground(row, ground)
		if l.Frame {
			c.Outline(row, pixel.Gold)
		}
	}
	tail := 0
	count := ""
	if l.Perch != "" {
		tail = pixel.IconCells + 4
		if l.Count > 0 {
			count = strconv.FormatInt(l.Count, 10)
			tail += pixel.Light.Width(count) + 1
		}
	}
	end := 0
	dark := l.Plate == pixel.Line || l.Plate == pixel.Sea
	if dark {
		// On a dark plate the khaki shadow would glare; the letters cast a
		// black one.
		end = c.PrintOn(l.Face, x, y, fitted(l.Face, name, width-tail), l.Ink, pixel.Shadow)
	} else {
		end = c.FitIn(l.Face, x, y, name, l.Ink, width-tail)
	}
	if l.Perch != "" {
		c.Icon(l.Perch, image.Pt(end+3, y-iconLift))
		end += 3 + pixel.IconCells
		if count != "" {
			ink := pixel.Dim
			if l.Plate == pixel.Line {
				ink = pixel.Gilt
			}
			end = c.Print(pixel.Light, end+1, y, count, ink)
		}
	}
	return end
}

// fitted is s cut with "..." to fit width in f, for text set without
// FitIn's tightening.
func fitted(f *pixel.Face, s string, width int) string {
	s = pixel.Plain(s)
	for k := len(s); k > 0; k-- {
		cut := s
		if k < len(s) {
			cut = s[:k] + "..."
		}
		if f.Width(cut) <= width {
			return cut
		}
	}
	return ""
}
