package pp

import (
	"image"
	"strconv"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The bones are two d10s of whale ivory, one marked in tens (00 to 90) and
// one in ones (0 to 9), read together as 1 to 100; 00 and 0 is a hundred.
// On the table they're drawn twice size: face on, with their numbers
// inked in red, or tipped on an edge as they roll.

// dieFace is a d10 seen face on, its kite face lit and wide enough to take
// two digits; dieScale how many pixels a cell.
var dieFace = []string{
	"....k....",
	"...kwk...",
	"..kwwrk..",
	".kqwwrqk.",
	"kqwwwrrqk",
	"kqwwwrrqk",
	".kqwwrqk.",
	"..kqwqk..",
	"...kqk...",
	"....k....",
}

const dieScale = 2

// dieTipped is the same die rolled onto an edge: its face turned away.
var dieTipped = sideways(dieFace)

// sideways is g turned a quarter.
func sideways(g []string) []string {
	out := make([]string, len(g[0]))
	for x := range out {
		row := make([]byte, len(g))
		for y := range g {
			row[len(g)-1-y] = g[y][x]
		}
		out[x] = string(row)
	}
	return out
}

// digits are a die's numbers, 3 by 5.
var digits = [10][5]string{
	{"###", "#.#", "#.#", "#.#", "###"},
	{".#.", "##.", ".#.", ".#.", "###"},
	{"###", "..#", "###", "#..", "###"},
	{"###", "..#", ".##", "..#", "###"},
	{"#.#", "#.#", "###", "..#", "..#"},
	{"###", "#..", "###", "..#", "###"},
	{"###", "#..", "###", "#.#", "###"},
	{"###", "..#", ".#.", ".#.", ".#."},
	{"###", "#.#", "###", "#.#", "###"},
	{"###", "#.#", "###", "..#", "###"},
}

// inkNumber writes s in the dice's digits, centred on x, its top at y.
func inkNumber(c *pixel.Canvas, x, y int, s string) {
	ink := ink('L')
	x -= (len(s)*4 - 1) / 2
	for _, r := range s {
		for dy, row := range digits[r-'0'] {
			for dx := range len(row) {
				if row[dx] == '#' {
					c.SetColorIndex(x+dx, y+dy, ink)
				}
			}
		}
		x += 4
	}
}

// die draws one die with its top left at p: face on showing label, or
// tipped and showing none.
func die(c *pixel.Canvas, p image.Point, label string, tipped bool) {
	if tipped {
		c.BigGrid(dieTipped, p, dieScale)
		return
	}
	c.BigGrid(dieFace, p, dieScale)
	// The face's middle, where it's widest.
	inkNumber(c, p.X+len(dieFace[0])*dieScale/2, p.Y+4*dieScale-1, label)
}

// pairOffset is where the ones die sits from the tens: beside it and a
// touch lower, so the two read left to right like the number and stay
// clear of the beam over them; pairSize is the box the two take.
var (
	pairOffset = image.Pt(8*dieScale, 1*dieScale)
	pairSize   = image.Pt(len(dieFace[0])*dieScale, len(dieFace)*dieScale).Add(pairOffset)
)

// pair draws the two dice with the tens die's top left at p.
func pair(c *pixel.Canvas, p image.Point, tens, ones string, tipped bool) {
	die(c, p, tens, tipped)
	die(c, p.Add(pairOffset), ones, tipped)
}

// faces are the two dice's numbers for a throw: 73 is 70 and 3, 5 is 00
// and 5, and a hundred is 00 and 0.
func faces(throw int) (tens, ones string) {
	t := throw / 10 % 10
	tens = strconv.Itoa(t) + "0"
	return tens, strconv.Itoa(throw % 10)
}
