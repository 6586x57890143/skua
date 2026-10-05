// Command sprites writes all of skua's art: the profile picture and mood
// icons from the drawn avatar (avatar.go), the banner from shapes
// (profile.go), and each mood's badge from the text grids below, so the
// badges are reviewable as text and a palette change is one rerun:
//
//	go run ./tools/sprites
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

// Each mood is a bone glyph on a rounded badge of the mood colour, lower
// left. A glyph is drawn on a 6 by 6 grid, centred in it, and the grid sits
// one cell inside the badge's 8 by 8 interior, so every glyph is centred in
// its badge. balanced enforces it.
var glyphs = map[string][]string{
	"ok":     {".....w", "....ww", "...ww.", "w.ww..", "wwww..", ".ww..."},
	"error":  {"ww..ww", ".wwww.", "..ww..", "..ww..", ".wwww.", "ww..ww"},
	"warn":   {"..ww..", "..ww..", "..ww..", "..ww..", "......", "..ww.."},
	"info":   {"..ww..", "......", ".www..", "..ww..", "..ww..", ".wwww."},
	"notice": {"..ww..", ".wwww.", ".wwww.", "wwwwww", "......", "..ww.."},
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

// The badge sits 8px (one badge cell) in from the icon's left and bottom
// edges; its 10 cells of 8px end at 88 and 248 of 256.
const cell, badgeX, badgeY = 8, 8, 168

func main() {
	out := filepath.Join("internal", "brand", "assets")
	if err := os.MkdirAll(out, 0o755); err != nil {
		log.Fatal(err)
	}
	b, err := bird()
	if err != nil {
		log.Fatal(err)
	}
	// Every mood is the bird alone, centred, at 2x, wearing its badge.
	icon := scale(centred(b), 256/grid)
	for mood, g := range glyphs {
		if err := balanced(g); err != nil {
			log.Fatalf("glyph %s: %v", mood, err)
		}
	}
	for mood, g := range glyphs {
		img := scale(icon, 1)
		drawScaled(img, badge, badgeX, badgeY, map[byte]color.NRGBA{'k': lineInk, 'x': moodInk[mood]}, cell)
		drawScaled(img, g, badgeX+2*cell, badgeY+2*cell, map[byte]color.NRGBA{'w': bone[1]}, cell)
		if err := write(filepath.Join(out, "skua_"+mood+".png"), img); err != nil {
			log.Fatal(err)
		}
	}
	if err := write(filepath.Join(out, "skua_avatar.png"), icon); err != nil {
		log.Fatal(err)
	}
	// Profile art lives outside the embedded assets: the binary never sends
	// it, only tools/setup uploads it and the README shows it.
	if err := os.MkdirAll("art", 0o755); err != nil {
		log.Fatal(err)
	}
	if err := write(filepath.Join("art", "skua_pfp.png"), scale(pfp(b), 1024/grid)); err != nil {
		log.Fatal(err)
	}
	if err := write(filepath.Join("art", "skua_banner.png"), scale(banner(), 4)); err != nil {
		log.Fatal(err)
	}
}

// balanced reports whether a glyph is 6 by 6 with equal empty space on
// opposite sides, which is what centres it in the badge.
func balanced(g []string) error {
	if len(g) != 6 {
		return fmt.Errorf("%d rows, want 6", len(g))
	}
	x0, x1, y0, y1 := 6, -1, 6, -1
	for y, row := range g {
		if len(row) != 6 {
			return fmt.Errorf("row %q is not 6 wide", row)
		}
		for x := range 6 {
			if row[x] == 'w' {
				x0, x1, y0, y1 = min(x0, x), max(x1, x), min(y0, y), max(y1, y)
			}
		}
	}
	if x0 != 5-x1 || y0 != 5-y1 {
		return fmt.Errorf("margins left %d right %d top %d bottom %d; it would sit off centre", x0, 5-x1, y0, 5-y1)
	}
	return nil
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

// paletted returns img as an indexed image, which pixel art always fits: an
// indexed PNG is a fraction of the size of a true colour one. Over 256
// colours is a generator bug, so it fails loudly rather than quietly
// shipping a large file.
func paletted(img *image.NRGBA) image.Image {
	index := map[color.NRGBA]uint8{}
	var pal color.Palette
	b := img.Bounds()
	out := image.NewPaletted(b, nil)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.NRGBAAt(x, y)
			i, ok := index[c]
			if !ok {
				if len(pal) == 256 {
					log.Fatal("more than 256 colours in one image; quantise before writing")
				}
				i = uint8(len(pal))
				index[c] = i
				pal = append(pal, c)
			}
			out.SetColorIndex(x, y, i)
		}
	}
	out.Palette = pal
	return out
}

// scale upscales nearest-neighbour by k, so every logical pixel stays a
// hard square.
func scale(src *image.NRGBA, k int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx()*k, b.Dy()*k))
	for y := range b.Dy() * k {
		for x := range b.Dx() * k {
			dst.SetNRGBA(x, y, src.NRGBAAt(x/k, y/k))
		}
	}
	return dst
}

func write(path string, img *image.NRGBA) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, paletted(img)); err != nil {
		_ = f.Close()
		return err
	}
	fmt.Println(strings.ReplaceAll(path, "\\", "/"))
	return f.Close()
}
