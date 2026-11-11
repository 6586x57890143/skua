package main

import (
	"image"
	"image/color"
	"log"
	"path/filepath"
)

// inkGrid is a multi-ink icon: each cell an index into ink, '.' empty.
type inkGrid struct {
	ink  []color.NRGBA
	grid []string
}

// foreverGold is forever's colour: its role and its crest.
var foreverGold = color.NRGBA{0xE0, 0xA6, 0x4B, 0xFF}

// foreverGlyphs are forever's buttons and its role, drawn on the grid, every
// one centred on it to the nearest cell (centred refuses one that isn't).
var foreverGlyphs = map[string][]string{
	"ladder": doubleSocialGlyph([]string{ // a podium
		"............",
		"............",
		".....ww.....",
		".....ww.....",
		".ww..ww.....",
		".ww..ww.....",
		".ww..ww..ww.",
		".ww..ww..ww.",
		".ww..ww..ww.",
		".ww..ww..ww.",
		"............",
		"............",
	}),
	"role": doubleSocialGlyph([]string{ // a crest with forever's chevron
		"............",
		"..wwwwwwww..",
		"..w......w..",
		"..w..ww..w..",
		"..w.wwww.w..",
		"..www..www..",
		"..w......w..",
		"..w......w..",
		"...w....w...",
		"....w..w....",
		".....ww.....",
		"............",
	}),
	"next": doubleSocialGlyph([]string{
		"............",
		"............",
		"....w.......",
		"....ww......",
		"....www.....",
		"....wwww....",
		"....wwww....",
		"....www.....",
		"....ww......",
		"....w.......",
		"............",
		"............",
	}),
	"prev": doubleSocialGlyph([]string{
		"............",
		"............",
		".......w....",
		"......ww....",
		".....www....",
		"....wwww....",
		"....wwww....",
		".....www....",
		"......ww....",
		".......w....",
		"............",
		"............",
	}),
}

// foreverIcons writes forever's emoji: each class's own icon, rebuilt from
// Blizzard's (forever_icons.go), the role's crest in foreverGold, the module's
// buttons, and the add and who buttons borrowed from remind's set.
func foreverIcons(out string) {
	for name, ig := range foreverClassIcons {
		img := image.NewNRGBA(image.Rect(0, 0, emojiSize, emojiSize))
		off := (emojiSize - glyphCells*emojiCell) / 2
		inks := map[byte]color.NRGBA{}
		for i, c := range ig.ink {
			inks[byte('0'+i)] = c
		}
		drawScaled(img, ig.grid, off, off, inks, emojiCell)
		mustWrite(filepath.Join(out, "fv_"+name+".png"), img)
	}
	for name, g := range foreverGlyphs {
		glyphCentred("fv_"+name, g)
	}
	tiles(out, "fv_", map[string][]string{"role": foreverGlyphs["role"]}, map[string]color.NRGBA{"role": foreverGold})
	for _, name := range []string{"ladder", "next", "prev"} {
		bare(out, "fv_"+name, foreverGlyphs[name])
	}
	for name, from := range map[string]string{"add": "new", "who": "team"} {
		glyphCentred("fv_"+name, buttonGlyphs[from])
		bare(out, "fv_"+name, buttonGlyphs[from])
	}
}

// glyphCentred refuses a glyph whose marks sit more than one cell of its 12 off
// the middle, either way: a button reads as broken when its icon leans.
func glyphCentred(name string, g []string) {
	top, bottom, left, right := len(g), -1, len(g[0]), -1
	for y, row := range g {
		for x := range row {
			if row[x] != '.' {
				top, bottom = min(top, y), max(bottom, y)
				left, right = min(left, x), max(right, x)
			}
		}
	}
	if bottom < 0 {
		log.Fatalf("glyph %s is empty", name)
	}
	dx := left - (len(g[0]) - 1 - right)
	dy := top - (len(g) - 1 - bottom)
	if dx < -2 || dx > 2 || dy < -2 || dy > 2 {
		log.Fatalf("glyph %s is off centre by %d across and %d down (24 cell grid); move it", name, dx, dy)
	}
}
