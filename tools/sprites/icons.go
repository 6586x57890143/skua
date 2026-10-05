package main

import (
	"image"
	"image/color"
	"log"
	"path/filepath"
)

// moduleGlyphs are each module's icon on a 24 cell grid, rasterised once
// from pixelarticons 2.4.1 (MIT, Gerrit Halfmann; art/LICENSE-pixelarticons)
// and kept here as text so a change is reviewable. The source icon is
// named beside each.
var moduleGlyphs = map[string][]string{
	"status": { // analytics
		"........................",
		"........................",
		"....wwwwwwwwwwwwwwww....",
		"....wwwwwwwwwwwwwwww....",
		"..ww................ww..",
		"..ww................ww..",
		"..ww...........ww...ww..",
		"..ww...........ww...ww..",
		"..ww...........ww...ww..",
		"..ww...........ww...ww..",
		"..ww...........ww...ww..",
		"..ww...........ww...ww..",
		"..ww.......ww..ww...ww..",
		"..ww.......ww..ww...ww..",
		"..ww...ww..ww..ww...ww..",
		"..ww...ww..ww..ww...ww..",
		"..ww...ww..ww..ww...ww..",
		"..ww...ww..ww..ww...ww..",
		"..ww................ww..",
		"..ww................ww..",
		"....wwwwwwwwwwwwwwww....",
		"....wwwwwwwwwwwwwwww....",
		"........................",
		"........................",
	},
	"whisper": { // message-text
		"........................",
		"........................",
		"....wwwwwwwwwwwwwwww....",
		"....wwwwwwwwwwwwwwww....",
		"..ww................ww..",
		"..ww................ww..",
		"..ww................ww..",
		"..ww................ww..",
		"..ww..wwwwwwww......ww..",
		"..ww..wwwwwwww......ww..",
		"..ww................ww..",
		"..ww................ww..",
		"..ww..wwww..........ww..",
		"..ww..wwww..........ww..",
		"..ww................ww..",
		"..ww................ww..",
		"..ww..wwwwwwwwwwwwww....",
		"..ww..wwwwwwwwwwwwww....",
		"..wwww..................",
		"..wwww..................",
		"..ww....................",
		"..ww....................",
		"........................",
		"........................",
	},
	"bird": { // music
		"........................",
		"........................",
		"........................",
		"........................",
		"..........wwwwwwww......",
		"..........wwwwwwww......",
		"........ww........ww....",
		"........ww........ww....",
		"........ww........ww....",
		"........ww........ww....",
		"........ww........ww....",
		"........ww........ww....",
		"....wwwwww....wwwwww....",
		"....wwwwww....wwwwww....",
		"..ww....ww..ww....ww....",
		"..ww....ww..ww....ww....",
		"..ww....ww..ww....ww....",
		"..ww....ww..ww....ww....",
		"....wwww......wwww......",
		"....wwww......wwww......",
		"........................",
		"........................",
		"........................",
		"........................",
	},
	"purge": { // trash
		"........................",
		"........................",
		".........wwwwww.........",
		".........wwwwww.........",
		".......ww......ww.......",
		".......ww......ww.......",
		"..wwwwwwwwwwwwwwwwwwww..",
		"..wwwwwwwwwwwwwwwwwwww..",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"....ww............ww....",
		"......wwwwwwwwwwww......",
		"......wwwwwwwwwwww......",
		"........................",
		"........................",
	},
	"help": { // book-open
		"........................",
		"........................",
		"........................",
		"..wwwwwwwww..wwwwwwwww..",
		"..wwwwwwwww..wwwwwwwww..",
		"ww.........ww.........ww",
		"ww.........ww.........ww",
		"ww.........ww..wwwww..ww",
		"ww.........ww..wwwww..ww",
		"ww.........ww.........ww",
		"ww.........ww.........ww",
		"ww.........ww..wwwww..ww",
		"ww.........ww..wwwww..ww",
		"ww.........ww.........ww",
		"ww.........ww.........ww",
		"ww.........ww..ww.....ww",
		"ww.........ww..ww.....ww",
		"ww.........ww.........ww",
		"ww.........ww.........ww",
		"wwwwwwwwwwwwwwwwwwwwwwww",
		"wwwwwwwwwwwwwwwwwwwwwwww",
		"...........ww...........",
		"...........ww...........",
		"........................",
	},
	"preen": { // feather
		"........................",
		"........................",
		"............wwwwww......",
		"............wwwwww......",
		"..........ww......ww....",
		"..........ww......ww....",
		"........ww..........ww..",
		"........ww..........ww..",
		"......ww......ww....ww..",
		"......ww......ww....ww..",
		"....ww......ww......ww..",
		"....ww......ww......ww..",
		"....ww....ww......ww....",
		"....ww....ww......ww....",
		"....ww..ww......ww......",
		"....ww..ww......ww......",
		"......ww......ww........",
		"......ww......ww........",
		"....ww..wwwwww..........",
		"....ww..wwwwww..........",
		"..ww....................",
		"..ww....................",
		"........................",
		"........................",
	},
	"perf": { // speed-fast
		"........................",
		"........................",
		"........................",
		"........................",
		"........................",
		".........wwwwww.........",
		".........wwwwww.........",
		".....wwww.........ww....",
		".....wwww.........ww....",
		"...ww...........ww......",
		"...ww...........ww......",
		".ww...........ww.....ww.",
		".ww...........ww.....ww.",
		".ww.......wwww.......ww.",
		".ww.......wwww.......ww.",
		".ww.......wwww.......ww.",
		".ww.......wwww.......ww.",
		"...ww..............ww...",
		"...ww..............ww...",
		"........................",
		"........................",
		"........................",
		"........................",
		"........................",
	},
}

// Emoji are 128px, the size Discord keeps an application emoji at. The
// mood emoji are the bird at 1px cells, which is its own 128 cell grid,
// wearing its badge at 4px cells; a module emoji is its glyph at 4px cells
// on a slate tile of 32 cells.
const (
	emojiSize  = 128
	emojiCell  = 4
	tileCells  = emojiSize / emojiCell
	glyphCells = 24
)

// tile is the module emoji's ground: slate, a line ink frame one cell
// wide, its corners cut two cells so it reads as a tile and not a box.
func tile() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, tileCells, tileCells))
	last := tileCells - 1
	for y := range tileCells {
		for x := range tileCells {
			dx, dy := min(x, last-x), min(y, last-y)
			switch {
			case dx+dy < 2: // the cut corner
			case dx == 0 || dy == 0 || dx+dy == 2:
				img.SetNRGBA(x, y, lineInk)
			default:
				img.SetNRGBA(x, y, slate[1])
			}
		}
	}
	return img
}

// writeEmoji writes every application emoji: a mood per badge, the bird
// alone and a tile per module.
func writeEmoji(b *image.NRGBA) {
	out := filepath.Join("internal", "brand", "emoji")
	mustMkdir(out)
	bird := centred(b)
	// The badge's 10 cells of 4px sit one cell in from the left and bottom.
	const k, bx, by = emojiCell, emojiCell, emojiSize - emojiCell - 10*emojiCell
	for mood, g := range glyphs {
		img := scale(bird, 1)
		drawScaled(img, badge, bx, by, map[byte]color.NRGBA{'k': lineInk, 'x': moodInk[mood]}, k)
		drawScaled(img, g, bx+2*k, by+2*k, map[byte]color.NRGBA{'w': bone[1]}, k)
		mustWrite(filepath.Join(out, "skua_"+mood+".png"), img)
	}
	mustWrite(filepath.Join(out, "skua_avatar.png"), bird)
	for name, g := range moduleGlyphs {
		if len(g) != glyphCells {
			log.Fatalf("module glyph %s has %d rows, want %d", name, len(g), glyphCells)
		}
		img := scale(tile(), emojiCell)
		off := (tileCells - glyphCells) / 2 * emojiCell
		drawScaled(img, g, off, off, map[byte]color.NRGBA{'w': bone[1]}, emojiCell)
		mustWrite(filepath.Join(out, "mod_"+name+".png"), img)
	}
}
