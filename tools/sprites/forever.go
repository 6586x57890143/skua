package main

import "image/color"

// classInk is each class's colour, Blizzard's own, so a class reads as
// itself in skua's art too.
var classInk = map[string]color.NRGBA{
	"warrior": {0xC6, 0x9B, 0x6D, 0xFF},
	"paladin": {0xF4, 0x8C, 0xBA, 0xFF},
	"hunter":  {0xAA, 0xD3, 0x72, 0xFF},
	"rogue":   {0xFF, 0xF4, 0x68, 0xFF},
	"priest":  {0xFF, 0xFF, 0xFF, 0xFF},
	"shaman":  {0x00, 0x70, 0xDD, 0xFF},
	"mage":    {0x3F, 0xC7, 0xEB, 0xFF},
	"warlock": {0x87, 0x88, 0xEE, 0xFF},
	"druid":   {0xFF, 0x7C, 0x0A, 0xFF},
	"role":    {0xE0, 0xA6, 0x4B, 0xFF}, // forever's gold, the role's colour
}

// classGlyphs are drawn on the grid, each class by its classic emblem.
// ponytail: drawn stand-ins; swap for icons rebuilt from Blizzard's class
// media once skua's battle.net app exists.
var classGlyphs = map[string][]string{
	"warrior": doubleSocialGlyph([]string{ // a sword
		"............",
		".........ww.",
		"........www.",
		".......www..",
		"......www...",
		"..w..www....",
		"...wwww.....",
		"....ww......",
		"...wwww.....",
		"..ww..w.....",
		".ww.........",
		"............",
	}),
	"paladin": doubleSocialGlyph([]string{ // a hammer
		"............",
		"..wwwwwwww..",
		"..wwwwwwww..",
		"..wwwwwwww..",
		".....ww.....",
		".....ww.....",
		".....ww.....",
		".....ww.....",
		".....ww.....",
		"....wwww....",
		"............",
		"............",
	}),
	"hunter": doubleSocialGlyph([]string{ // an arrow
		"............",
		".......wwww.",
		".........ww.",
		"........w.w.",
		".......w..w.",
		"......w.....",
		".....w......",
		"....w.......",
		".w.w........",
		"..w.........",
		".w.w........",
		"............",
	}),
	"rogue": doubleSocialGlyph([]string{ // a dagger
		"............",
		".....ww.....",
		"....wwww....",
		"....wwww....",
		"....wwww....",
		"....wwww....",
		"..wwwwwwww..",
		".....ww.....",
		".....ww.....",
		"....wwww....",
		"............",
		"............",
	}),
	"priest": doubleSocialGlyph([]string{ // a cross
		"............",
		".....ww.....",
		".....ww.....",
		"..wwwwwwww..",
		"..wwwwwwww..",
		".....ww.....",
		".....ww.....",
		".....ww.....",
		".....ww.....",
		".....ww.....",
		"............",
		"............",
	}),
	"shaman": doubleSocialGlyph([]string{ // a bolt
		"............",
		"......www...",
		".....www....",
		"....www.....",
		"...wwwwww...",
		".....www....",
		"....www.....",
		"...ww.......",
		"..w.........",
		"............",
		"............",
		"............",
	}),
	"mage": doubleSocialGlyph([]string{ // a star
		"............",
		".....ww.....",
		".....ww.....",
		"....wwww....",
		".wwwwwwwwww.",
		"...wwwwww...",
		"....wwww....",
		"...ww..ww...",
		"..w......w..",
		"............",
		"............",
		"............",
	}),
	"warlock": doubleSocialGlyph([]string{ // a skull
		"............",
		"...wwwwww...",
		"..wwwwwwww..",
		"..w..ww..w..",
		"..w..ww..w..",
		"..wwwwwwww..",
		"...wwwwww...",
		"...w.ww.w...",
		"...wwwwww...",
		"............",
		"............",
		"............",
	}),
	"druid": doubleSocialGlyph([]string{ // a paw
		"............",
		"..w..ww..w..",
		"..w..ww..w..",
		".ww......ww.",
		"............",
		"...wwwwww...",
		"..wwwwwwww..",
		"..wwwwwwww..",
		"...wwwwww...",
		"............",
		"............",
		"............",
	}),
	"role": doubleSocialGlyph([]string{ // a crest with forever's chevron
		"............",
		"..wwwwwwww..",
		"..w......w..",
		"..w..ww..w..",
		"..w.wwww.w..",
		"..ww....ww..",
		"..w......w..",
		"...w....w...",
		"....w..w....",
		".....ww.....",
		"............",
		"............",
	}),
}

// ladderGlyph is the ladder button's podium.
var ladderGlyph = doubleSocialGlyph([]string{
	"............",
	".....ww.....",
	".....ww.....",
	".....ww.....",
	".ww..ww.....",
	".ww..ww..ww.",
	".ww..ww..ww.",
	".ww..ww..ww.",
	".ww..ww..ww.",
	"wwwwwwwwwwww",
	"............",
	"............",
})

// foreverIcons writes forever's emoji: a tile per class in its colour,
// the role's crest in gold and the hub's buttons.
func foreverIcons(out string) {
	tiles(out, "fv_", classGlyphs, classInk)
	bare(out, "fv_add", moduleGlyphs["new"])
	bare(out, "fv_who", moduleGlyphs["team"])
	bare(out, "fv_ladder", ladderGlyph)
}
