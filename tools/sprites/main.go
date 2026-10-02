// Command sprites draws skua's mood icons from hand-authored pixel grids and
// writes them to internal/brand/assets. Throwaway tooling, kept so the art
// is reviewable as text and a palette change is one rerun:
//
//	go run ./tools/sprites
//
// A drawn sprite sheet can replace these later; brand only cares about the
// file names.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ink is the palette: a dark, cold skua, near-black outline, mottled umber
// body, slate hooked bill, bone wing flash.
var ink = map[byte]color.NRGBA{
	'k': {0x0B, 0x0D, 0x10, 0xFF}, // outline
	'd': {0x2B, 0x25, 0x21, 0xFF}, // dark umber
	'm': {0x46, 0x3C, 0x34, 0xFF}, // mottle
	'l': {0x6F, 0x63, 0x58, 0xFF}, // speckle
	'b': {0x33, 0x39, 0x41, 0xFF}, // bill
	'B': {0x5A, 0x64, 0x6F, 0xFF}, // bill highlight
	'e': {0x02, 0x02, 0x03, 0xFF}, // eye
	'w': {0xE6, 0xE1, 0xD6, 0xFF}, // glint
	'n': {0x8C, 0x80, 0x72, 0xFF}, // pale streak
	'h': {0x4A, 0x52, 0x5C, 0xFF}, // hook
}

// Each mood is a bone glyph on a rounded badge of the mood colour, lower
// left.
var glyphs = map[string][]string{
	"ok":     {".....w", "....ww", "w..ww.", "wwww..", ".ww...", ""},
	"error":  {"ww..ww", ".wwww.", "..ww..", ".wwww.", "ww..ww", ""},
	"warn":   {"..ww..", "..ww..", "..ww..", "", "..ww..", ""},
	"info":   {"..ww..", "", ".www..", "..ww..", "..ww..", ".wwww."},
	"notice": {"..ww..", ".wwww.", ".wwww.", "wwwwww", "", "..ww.."},
	"idle":   {"wwww..", "..w...", ".w....", "wwww..", "....ww", "....ww"},
}

var badge = []string{
	"..kkkkkk..",
	".kxxxxxxk.",
	"kxxxxxxxxk",
	"kxxxxxxxxk",
	"kxxxxxxxxk",
	"kxxxxxxxxk",
	"kxxxxxxxxk",
	"kxxxxxxxxk",
	".kxxxxxxk.",
	"..kkkkkk..",
}

// moodInk matches brand's palette so the glyph reads as the embed's colour.
var moodInk = map[string]color.NRGBA{
	"ok":     {0x6E, 0x9A, 0x6A, 0xFF},
	"error":  {0xC0, 0x4A, 0x3E, 0xFF},
	"warn":   {0xD8, 0x9B, 0x45, 0xFF},
	"info":   {0x8A, 0x9C, 0xB2, 0xFF},
	"notice": {0x4F, 0x7C, 0x86, 0xFF},
	"idle":   {0x6A, 0x70, 0x7A, 0xFF},
}

func main() {
	out := filepath.Join("internal", "brand", "assets")
	if err := os.MkdirAll(out, 0o755); err != nil {
		log.Fatal(err)
	}
	// Every mood is the profile bust with its badge at lower left, drawn at
	// 2x so the badge's pixels match the bust's.
	for mood, g := range glyphs {
		img := bust()
		drawScaled(img, badge, 2, 42, map[byte]color.NRGBA{'k': ink['k'], 'x': moodInk[mood]}, 2)
		drawScaled(img, g, 6, 46, ink, 2)
		if err := writeScaled(filepath.Join(out, "skua_"+mood+".png"), img, 4); err != nil {
			log.Fatal(err)
		}
	}
	if err := writeScaled(filepath.Join(out, "skua_avatar.png"), bust(), 4); err != nil {
		log.Fatal(err)
	}
	// Profile art lives outside the embedded assets: the binary never sends
	// it, only tools/setup uploads it and the README shows it.
	if err := os.MkdirAll("art", 0o755); err != nil {
		log.Fatal(err)
	}
	if err := writeScaled(filepath.Join("art", "skua_pfp.png"), pfp(), 16); err != nil {
		log.Fatal(err)
	}
	if err := writeScaled(filepath.Join("art", "skua_banner.png"), banner(), 4); err != nil {
		log.Fatal(err)
	}
}

// drawScaled paints a text grid with each cell k pixels square. '.' is
// transparent; any other character must be in pal.
func drawScaled(img *image.NRGBA, grid []string, ox, oy int, pal map[byte]color.NRGBA, k int) {
	for y, row := range grid {
		if ox+len(row)*k > img.Bounds().Dx() {
			log.Fatalf("row %d is %d wide: %q", y, len(row), row)
		}
		for x := range len(row) {
			c, ok := pal[row[x]]
			if !ok {
				if row[x] != '.' {
					log.Fatalf("unknown ink %q at %d,%d", row[x], x, y)
				}
				continue
			}
			for dy := range k {
				for dx := range k {
					img.SetNRGBA(ox+x*k+dx, oy+y*k+dy, c)
				}
			}
		}
	}
}

// writeScaled upscales nearest-neighbour by k, so every logical pixel stays
// a hard square.
func writeScaled(path string, src *image.NRGBA, k int) error {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx()*k, b.Dy()*k))
	for y := range b.Dy() * k {
		for x := range b.Dx() * k {
			dst.SetNRGBA(x, y, src.NRGBAAt(x/k, y/k))
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, dst); err != nil {
		_ = f.Close()
		return err
	}
	fmt.Println(strings.ReplaceAll(path, "\\", "/"))
	return f.Close()
}
