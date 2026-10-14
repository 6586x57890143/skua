package pp

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"sync"

	"github.com/6586x57890143/skua/internal/pixel"
)

// titleArt is the title, 384 by 106: "melting pot" in a blackletter shrunk
// to pixels (from UnifrakturMaguntia, SIL Open Font License; only the
// picture ships), cream shading to teal, over a rust cauldron on a low
// fire. Finished art: replace it whole rather than edit its pixels.
//
//go:embed title.png
var titleArt []byte

var title = sync.OnceValue(func() image.Image {
	img, err := png.Decode(bytes.NewReader(titleArt))
	if err != nil {
		panic("pp: the title won't decode: " + err.Error())
	}
	return img
})

// TitleScreen is what a member sees before they pick a class, as an old
// game's title: the name over its emblem, what it is, every class's bird in
// a row, and a menu with the cursor on picking one. It returns the PNG.
func TitleScreen() ([]byte, error) {
	c := pixel.New(screenW, screenH)
	c.Fill(c.Rect, pixel.Shadow)
	c.Picture(title(), image.Pt(0, 2), 1)

	sub := "a pot point journey"
	c.Shaded((screenW-len(sub)*pixel.Advance)/2, 106, sub, pixel.Glove, pixel.Line)

	// The birds in a row, each in a frame, a gap between.
	const frame, gap = 32 + 6, 6
	x := (screenW-len(Classes)*frame-(len(Classes)-1)*gap)/2 + 3
	for _, cl := range Classes {
		if p := Portrait(cl); p != nil {
			c.Portrait(p, image.Pt(x, 126), 1)
		}
		x += frame + gap
	}

	menu := image.Rect(104, 180, 280, 212)
	c.Window(menu)
	c.Cursor(menu.Min.X+18, menu.Min.Y+15)
	c.Text(menu.Min.X+22, menu.Min.Y+9, "pick a class", pixel.Ink, 0)
	right(c, menu.Max.X-12, menu.Min.Y+9, "beta", pixel.Crimson)
	return c.PNG(screenScale)
}
