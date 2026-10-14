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
