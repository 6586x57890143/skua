package main

import (
	"image"
	"image/draw"
	"image/gif"
	"log"
	"os"
	"path/filepath"
)

// A quiet two-pixel bob of skua's own source art. Three frames, no flashing,
// no raster resampling. Discord animates this emoji; the bot sends it once.
func writeFixLoading(out string, bird *image.NRGBA) {
	animation := &gif.GIF{LoopCount: 20}
	for _, dy := range []int{0, -2, 0} {
		frame := image.NewNRGBA(bird.Bounds())
		draw.Draw(frame, frame.Bounds(), bird, image.Pt(0, -dy), draw.Src)
		animation.Image = append(animation.Image, paletted(frame).(*image.Paletted))
		animation.Delay = append(animation.Delay, 24)
		animation.Disposal = append(animation.Disposal, gif.DisposalBackground)
	}
	f, err := os.Create(filepath.Join(out, "fix_loading.gif"))
	if err != nil {
		log.Fatal(err)
	}
	if err = gif.EncodeAll(f, animation); err != nil {
		_ = f.Close()
		log.Fatal(err)
	}
	if err = f.Close(); err != nil {
		log.Fatal(err)
	}
}
