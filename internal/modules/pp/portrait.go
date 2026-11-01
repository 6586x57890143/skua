package pp

import (
	"bytes"
	"embed"
	"image"
	"image/png"
	"sync"
)

// portraitFiles are the classes' portraits, one a bird, 32 by 48 on black,
// finished art: replace a file whole rather than edit its pixels.
//
//go:embed portraits/*.png
var portraitFiles embed.FS

// portraits is every portrait by its bird, decoded once.
var portraits = sync.OnceValue(func() map[string]image.Image {
	m := map[string]image.Image{}
	for _, c := range Classes {
		data, err := portraitFiles.ReadFile("portraits/" + c.Bird + ".png")
		if err != nil {
			continue
		}
		if img, err := png.Decode(bytes.NewReader(data)); err == nil {
			m[c.Bird] = img
		}
	}
	return m
})

// Portrait is c's portrait, or nil for one not drawn, which shows skua.
func Portrait(c Class) image.Image { return portraits()[c.Bird] }

// figureFiles are the people who stand in the scenes at twice a portrait,
// finished art like the portraits.
//
//go:embed figures/*.png
var figureFiles embed.FS

// purserFigure is the purser standing, 64 by 96, cut out once.
var purserFigure = sync.OnceValue(func() figure {
	data, err := figureFiles.ReadFile("figures/purser.png")
	if err != nil {
		panic("pp: no purser figure")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic("pp: purser figure: " + err.Error())
	}
	return cutout(img)
})
