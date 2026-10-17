package pixel

import (
	"image"
	"image/color"
	"slices"
	"strings"
)

// Icons are 16 by 16 cell pictures in the style of an old RPG's item
// icons, after merlin's art: a dark outline and each material lit from the
// top left in three or four tones. Each is a text grid, one character an
// ink from IconInks and '.' clear, so a change is reviewable as text. The
// screens draw them one pixel a cell, and tools/sprites writes each as an
// application emoji (pp_<name>) at eight, so a button and a screen show
// the same picture. icons_test holds every one to its grid and centred.
//
// Materials, darkest first: gold ABCD, iron EFGH, wood IJKl, ember LMNO,
// leaf PQRS, sky gem TUVW, boost pink XYZz, parchment pqr, and k the
// outline, w the brightest white.
//
// Icons is every icon, by name.
var Icons = map[string][]string{
	"pot": {
		"................",
		".....kkkkkk.....",
		"...kkDCCCCBkk...",
		"..kDCCDCCBBBAk..",
		".kkkkkkkkkkkkkk.",
		"kHGGGGGGGGFFFFEk",
		"kkkkkkkkkkkkkkkk",
		".kGHGFFFFFFFEEk.",
		"kGHGFFFFFFFFEEEk",
		"kGGFFFFFFFFFEEEk",
		"kFFFFFFFFFFEEEEk",
		".kFFFFFFFFEEEEk.",
		"..kkFFFFFEEEkk..",
		"...kkkkkkkkkk...",
		"...kk......kk...",
		"................",
	},
	"scales": {
		"................",
		".......kk.......",
		"......kDCk......",
		".kkkkkkCBkkkkkk.",
		"kDCCCCCCBBBBBBAk",
		".kkkkkkCBkkkkkk.",
		"..G...kCBk...G..",
		".G.G..kCBk..G.G.",
		"G...G.kCBk.G...G",
		"kDCBk.kCBk.kDCBk",
		".kBk..kCBk..kBk.",
		"......kCBk......",
		"......kCBk......",
		"....kkCCBBkk....",
		"...kDCCCCBBAk...",
		"...kkkkkkkkkk...",
	},
	"heart": {
		"................",
		"..kkkk....kkkk..",
		".kNOONk..kNNNMk.",
		"kNOOONNkkNNNNMMk",
		"kNOONNNNNNNNMMMk",
		"kNONNNNNNNNNMMMk",
		"kNNNNNNNNNNMMMLk",
		".kNNNNNNNNMMMLk.",
		"..kNNNNNNMMMLk..",
		"...kNNNNMMMLk...",
		"....kNNMMMLk....",
		".....kNMMLk.....",
		"......kMLk......",
		".......kk.......",
		"................",
		"................",
	},
	"crest": {
		"................",
		"...kkk....kkk...",
		"...kVUk..kUTk...",
		"...kVUUkkUTTk...",
		"....kVUkkUTk....",
		".....kkkkkk.....",
		"....kCDDCCBk....",
		"...kCDCCCCBAk...",
		"..kCDCCDDCCBAk..",
		"..kCCCDDDDCBAk..",
		"..kCCCCDDCCBAk..",
		"..kBCCCCCCBBAk..",
		"...kBBBBBBBAk...",
		"....kkAAAAkk....",
		"......kkkk......",
		"................",
	},
	"banner": {
		"................",
		"..kk............",
		"..kDk...........",
		"..kJkkkkkkkkkk..",
		"..kJkNOOOONNMk..",
		"..kJkNONNNNMMk..",
		"..kJkNNNNNNMk...",
		"..kJkNNNNNMk....",
		"..kJkMNNNNMMk...",
		"..kJkMMMMMMMMk..",
		"..kJkkkkkkkkkk..",
		"..kJk...........",
		"..kJk...........",
		"..kIk...........",
		"..kkk...........",
		"................",
	},
	"plume": {
		"................",
		"...........kkk..",
		"..........kzZk..",
		".........kzZZYk.",
		"........kzZZZYk.",
		".......kzZZZYk..",
		"........kZZYYk..",
		".....kkzZZZYk...",
		"....kZZzZYYk....",
		"....kZzZYYk.....",
		"...kZzYYXk......",
		"..kkzYXkk.......",
		"..kJkkk.........",
		".kJk............",
		".kk.............",
		"................",
	},
	"hearth": {
		"................",
		".......kk.......",
		"......kOk.......",
		"......kOOk..k...",
		"..k..kNOOk.kOk..",
		".kOk.kNOOOkkOk..",
		".kOkkNNOOONkNk..",
		"..kNNNOOOONNNk..",
		"..kMNNNOONNNMk..",
		"..kMMNNNNNNMMk..",
		".kkkkkkkkkkkkkk.",
		".kHGGGGGGGFFFEk.",
		"..kkFFFFFFFEkk..",
		"....kFk..kEk....",
		"...kkkk..kkkk...",
		"................",
	},
	"laurel": {
		"................",
		"...kk......kk...",
		"..kRSk....kSRk..",
		"..kQRk....kRQk..",
		".kRSk......kSRk.",
		".kQRk......kRQk.",
		".kRSk......kSRk.",
		".kQRk......kRQk.",
		"..kRSk....kSRk..",
		"..kQRRk..kRRQk..",
		"...kQRRkkRRQk...",
		"....kkkCCkkk....",
		"......kBBk......",
		".....kB..Bk.....",
		".....kk..kk.....",
		"................",
	},
	"reroll": {
		"................",
		"................",
		"...kkkkkkkkkk...",
		"..kwwwwwwwwwHk..",
		"..kwkkwwwwwwHk..",
		"..kwkkwwwwwwHk..",
		"..kwwwwkkwwwHk..",
		"..kwwwwkkwwwHk..",
		"..kwwwwwwkkwHk..",
		"..kwwwwwwkkwHk..",
		"..kHHHHHHHHHGk..",
		"..kGGGGGGGGGGk..",
		"...kkkkkkkkkk...",
		"................",
		"................",
		"................",
	},
	"quill": {
		"................",
		"............kkk.",
		"...........kwwk.",
		"..kkkkkkkkkwGk..",
		".krrrrrrrkwGkk..",
		".krkkkkrkwGkrk..",
		".krrrrrkwGkrrk..",
		".krkkkkkGkkkrk..",
		".krrrrkJkrrrrk..",
		".krkkkkIkkkkrk..",
		".krrrrrrrrrrrk..",
		".krkkkkkkkrrrk..",
		".krrrrrrrrrrrk..",
		".kqqqqqqqqqqqk..",
		"..kkkkkkkkkkk...",
		"................",
	},
	"drum": {
		"................",
		"..kk........kk..",
		"...kJk....kJk...",
		"....kJk..kJk....",
		"...kkkkkkkkkk...",
		"..krrrrrrrrrqk..",
		"..kqrrrrrrrqpk..",
		"..kkkkkkkkkkkk..",
		"..kCBMMMMMMBAk..",
		"..kMNMkMMkMMLk..",
		"..kMNkMkkMkMLk..",
		"..kMkNMMMMkMLk..",
		"..kCBBBBBBBBAk..",
		"...kkkkkkkkkk...",
		"................",
		"................",
	},
	"bell": {
		"................",
		".......kk.......",
		"......kCCk......",
		".....kkkkkk.....",
		"....kCDCCCBk....",
		"....kDCCCCBk....",
		"...kCDCCCCBAk...",
		"...kCDCCCCBAk...",
		"..kCDCCCCCCBAk..",
		"..kCCCCCCCCBAk..",
		".kkkkkkkkkkkkkk.",
		".kBCCCCCCCCBBAk.",
		".kkkkkkkkkkkkkk.",
		"......kAAk......",
		".......kk.......",
		"................",
	},
	"compass": {
		"................",
		".....kkkkkk.....",
		"...kkGHHHHGkk...",
		"..kGHwwkkwwHGk..",
		"..kHwwwNMwwwHk..",
		".kGwwwkNMkwwwGk.",
		".kHwwwkNMkwwwFk.",
		".kHkGGHNMFFEkFk.",
		".kHkGGHEFFFEkFk.",
		".kGwwwkEFkwwwFk.",
		"..kGwwwEFwwwFk..",
		"..kGGwwkkwwFFk..",
		"...kkGFFFFFkk...",
		".....kkkkkk.....",
		"................",
		"................",
	},
	"envelope": {
		"................",
		"................",
		".kkkkkkkkkkkkkk.",
		".krrrrrrrrrrrrk.",
		".kqkrrrrrrrrkqk.",
		".kqqkrrrrrrkqqk.",
		".kqqqkrrrrkqqqk.",
		".kqqqqkMMkqqqqk.",
		".kqqqqMNOMqqqqk.",
		".kqqqkMMNMkqqqk.",
		".kqqkqqMMqqkqqk.",
		".kqkqqqqqqqqkqk.",
		".kkpppppppppppk.",
		".kkkkkkkkkkkkkk.",
		"................",
		"................",
	},
	"campfire": {
		"................",
		".......kk.......",
		"......kOk.......",
		"......kOOk......",
		".....kNOOk......",
		"....kNNOONk.....",
		"....kNOOONNk....",
		"...kMNOOONNk....",
		"...kMNNOONNMk...",
		"...kkMNNNNMkk...",
		"..kJKkkkkkkKJk..",
		".kJKKJkkkkJKKJk.",
		"..kkJKJkkJKJkk..",
		"....kkk..kkk....",
		"................",
		"................",
	},
	"mirror": {
		"................",
		".....kkkkkk.....",
		"....kCCCCCBk....",
		"...kCWWVVVUBk...",
		"...kCWVVVVUBk...",
		"...kCVVVVUUBk...",
		"...kCVVVUUTBk...",
		"...kBVVUUUTAk...",
		"....kBUUTTAk....",
		".....kkBBkk.....",
		"......kJKk......",
		"......kJKk......",
		"......kJKk......",
		"......kIJk......",
		".......kk.......",
		"................",
	},
	"lantern": {
		"................",
		".......kk.......",
		"......kGGk......",
		"......k..k......",
		".....kkkkkk.....",
		"....kGHHHHFk....",
		"....kkkkkkkk....",
		"....kGOOONFk....",
		"....kGODDOFk....",
		"....kGNOONFk....",
		"....kGNNNMFk....",
		"....kkkkkkkk....",
		".....kFFFEk.....",
		"......kkkk......",
		"................",
		"................",
	},
	"sword": {
		"................",
		"............kkk.",
		"...........kHwk.",
		"..........kHGk..",
		".........kHGk...",
		"........kHGk....",
		"...kk..kHGk.....",
		"...kCkkHGk......",
		"....kCkGk.......",
		".....kCCk.......",
		"....kJkkCk......",
		"...kJk...kk.....",
		"..kIk...........",
		"..kk............",
		"................",
		"................",
	},
	"bow": {
		"................",
		"...kkk..........",
		"..kKlKk.........",
		"..kkJKk.........",
		".kGk.kKk........",
		".kGk..kKk.......",
		".kGkkkkkkkkkkk..",
		".kGkHHHHHHHHGHk.",
		".kGkkkkkkkkkkk..",
		".kGk..kKk.......",
		".kGk.kKk........",
		"..kkJKk.........",
		"..kKlKk.........",
		"...kkk..........",
		"................",
		"................",
	},
	"orb": {
		"................",
		"........kkkk....",
		".......kWVVUk...",
		"......kWVVVUTk..",
		"......kVVVUUTk..",
		"......kVUUUTTk..",
		".......kUTTTk...",
		".......kkBBkk...",
		"......kKlk......",
		".....kKlk.......",
		"....kJKk........",
		"...kJKk.........",
		"..kJKk..........",
		".kIJk...........",
		".kkk............",
		"................",
	},
	"pouch": {
		"................",
		".....kk..kk.....",
		"....kKlkklKk....",
		".....kKllKk.....",
		"......kCCk......",
		".....kKllKk.....",
		"....kKllllKk....",
		"...kKlllllKJk...",
		"..kKlllllllKJk..",
		"..kKllllllKKJk..",
		"..kKlllllKKJIk..",
		"..kJKKKKKKJJIk..",
		"...kJJJJJJJIk...",
		"....kkkkkkkk....",
		"................",
		"................",
	},
	"lute": {
		"................",
		".............kk.",
		"............kJk.",
		"...........kJk..",
		"..........kJk...",
		".........kJk....",
		"....kkkkkJk.....",
		"...kKlllkk......",
		"..kKlkklKk......",
		"..kKlkklKk......",
		"..kKllllKk......",
		"..kJKlllKk......",
		"...kJKKKk.......",
		"....kkkk........",
		"................",
		"................",
	},
	"beads": {
		"................",
		".....kk..kk.....",
		"....kKlkkKlk....",
		"...kKk....kKk...",
		"..kKk......kKk..",
		"..kJk......kJk..",
		"..kKk......kKk..",
		"..kJk......kJk..",
		"...kKk....kKk...",
		"....kJkkkkJk....",
		".....kkCCkk.....",
		"......kBBk......",
		"......kMMk......",
		"......kMLk......",
		".......kk.......",
		"................",
	},
	"shield": {
		"................",
		"..kkkkkkkkkkkk..",
		"..kHGGGGGGGGFk..",
		"..kGUUUUUUUUFk..",
		"..kGUwUUUUwUFk..",
		"..kGUUwUUwUUFk..",
		"..kGUUUwwUUUFk..",
		"..kGUUUUUUUTFk..",
		"...kGUUUUUTFk...",
		"...kGUUUUTTFk...",
		"....kGUUTTFk....",
		".....kGTTFk.....",
		"......kGFk......",
		".......kk.......",
		"................",
		"................",
	},
}

// perchGrid is the perch every rung of the rookery stair is drawn from, in
// wood.
var perchGrid = []string{
	"................",
	"................",
	"................",
	".kkkkkkkkkkkkkk.",
	"kllKKKKKKKKKJJIk",
	"kKJJJJJJJJJJJIIk",
	".kkkkkkKJkkkkkk.",
	"......kKJk......",
	"......kKJk......",
	"......kKJk......",
	"......kKJk......",
	"....kkKKJIkk....",
	"...kKKKJJJIIk...",
	"...kkkkkkkkkk...",
	"................",
	"................",
}

// screenIcons are drawn on screens like Icons but kept apart from them:
// screen art only, never application emoji, which have an upload budget.
var screenIcons = map[string][]string{}

// Perches are the rookery stair's rungs in order, the perch in each one's
// material: tallow, shale, chitin, pale glass, a drowned bell's bronze,
// and past them one nobody made.
var Perches = []string{"perch_tallow", "perch_shale", "perch_chitin", "perch_glass", "perch_bell", "perch_nameless"}

// perchInks is the wood each perch's material takes the place of.
var perchInks = map[string]string{
	"perch_tallow": "pqrw", "perch_shale": "EFGH", "perch_chitin": "LMNO",
	"perch_glass": "TUVW", "perch_bell": "PQRS", "perch_nameless": "ABCD",
}

func init() {
	for name, inks := range perchInks {
		g := make([]string, len(perchGrid))
		for y, row := range perchGrid {
			b := []byte(row)
			for x, r := range b {
				if i := strings.IndexByte("IJKl", r); i >= 0 {
					b[x] = inks[i]
				}
			}
			g[y] = string(b)
		}
		screenIcons[name] = g
	}
	screenIcons["perch"] = perchGrid
}

// IconCells is an icon's side.
const IconCells = 16

// IconInks is every ink an icon may use.
var IconInks = map[byte]color.NRGBA{
	'k': rgb(0x1A1513),
	'A': rgb(0x7A4A12), 'B': rgb(0xC98A2E), 'C': rgb(0xF2C14E), 'D': rgb(0xFFF1A8),
	'E': rgb(0x2E343E), 'F': rgb(0x5A6472), 'G': rgb(0x8E99A8), 'H': rgb(0xD5DDE6),
	'I': rgb(0x3E2616), 'J': rgb(0x6E4527), 'K': rgb(0xA06A3A), 'l': rgb(0xC9965C),
	'L': rgb(0x5E1A16), 'M': rgb(0xA8322A), 'N': rgb(0xE0583A), 'O': rgb(0xFFA65A),
	'P': rgb(0x1C3F24), 'Q': rgb(0x357540), 'R': rgb(0x6FAF52), 'S': rgb(0xC3EB8C),
	'T': rgb(0x1A2F5C), 'U': rgb(0x2F5FA8), 'V': rgb(0x62A0E6), 'W': rgb(0xC6E2FF),
	'X': rgb(0x5C1A4E), 'Y': rgb(0xA8357F), 'Z': rgb(0xEA74C2), 'z': rgb(0xFFCDEE),
	'p': rgb(0x7D6446), 'q': rgb(0xBFA67A), 'r': rgb(0xEDDFBA),
	'w': rgb(0xF7F4EC),
}

// iconInk is each ink's index in Palette, filled in at init.
var iconInk = map[byte]uint8{}

// addIconInks puts the icons' inks on the palette, in a fixed order so a
// screen comes out the same byte for byte.
func addIconInks() {
	keys := make([]byte, 0, len(IconInks))
	for r := range IconInks {
		keys = append(keys, r)
	}
	slices.Sort(keys)
	for _, r := range keys {
		iconInk[r] = uint8(len(Palette))
		Palette = append(Palette, IconInks[r])
	}
}

// Icon draws the named icon with its top left at p; an unknown name draws
// nothing.
func (c *Canvas) Icon(name string, p image.Point) { c.BigIcon(name, p, 1) }

// BigIcon draws the named icon scale times its size, scale pixels a cell,
// with its top left at p.
func (c *Canvas) BigIcon(name string, p image.Point, scale int) {
	g, ok := Icons[name]
	if !ok {
		g = screenIcons[name]
	}
	var box image.Rectangle
	for y, row := range g {
		for x := range len(row) {
			if i, ok := iconInk[row[x]]; ok {
				cell := image.Rect(p.X+x*scale, p.Y+y*scale, p.X+(x+1)*scale, p.Y+(y+1)*scale)
				c.Fill(cell, i)
				box = box.Union(cell.Intersect(c.Rect))
			}
		}
	}
	c.mark("icon "+name, box)
}
