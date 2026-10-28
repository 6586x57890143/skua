package fix

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestComposeGalleryMakesFourColumnContactSheet(t *testing.T) {
	colors := []color.RGBA{
		{R: 220, A: 255},
		{G: 220, A: 255},
		{B: 220, A: 255},
		{R: 220, G: 220, A: 255},
	}
	images := make([]image.Image, len(colors))
	for i, c := range colors {
		img := image.NewRGBA(image.Rect(0, 0, 200, 610))
		for y := 0; y < img.Bounds().Dy(); y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				img.SetRGBA(x, y, c)
			}
		}
		images[i] = img
	}
	raw, err := composeGallery(images)
	if err != nil {
		t.Fatal(err)
	}
	got, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds().Dx() != 1198 || got.Bounds().Dy() != sheetHeight {
		t.Fatalf("got %v, want 1198x%d with full frames", got.Bounds(), sheetHeight)
	}
	_, _, _, gapAlpha := got.At((sheetWidth-3*sheetGap)/4+sheetGap/2, sheetHeight/2).RGBA()
	if gapAlpha != 0 {
		t.Fatalf("gallery gutter alpha = %d, want transparent", gapAlpha)
	}
	for i, want := range colors {
		tileWidth := (sheetWidth - 3*sheetGap) / 4
		x := i*(tileWidth+sheetGap) + tileWidth/2
		r, g, b, _ := got.At(x, sheetHeight/2).RGBA()
		if abs(int(uint8(r>>8))-int(want.R)) > 20 || abs(int(uint8(g>>8))-int(want.G)) > 20 || abs(int(uint8(b>>8))-int(want.B)) > 20 {
			t.Fatalf("column %d color = #%02x%02x%02x, want near #%02x%02x%02x", i+1, uint8(r>>8), uint8(g>>8), uint8(b>>8), want.R, want.G, want.B)
		}
	}
}

func TestGalleryLinkEncryptsOnlyTrustedPortraitPhotos(t *testing.T) {
	api := &API{}
	relay := newMediaRelay(api, "https://hooks.example", "a-secret-long-enough-for-tests")
	media := make([]Media, 4)
	for i := range media {
		media[i] = Media{URL: "https://pbs.twimg.com/media/pic" + strconv.Itoa(i+1) + ".jpg?format=jpg&name=large", Type: "photo", Width: 720, Height: 1280}
	}
	link := relay.galleryLink("twitter", media)
	if link == "" || strings.Contains(link, "pbs.twimg.com") {
		t.Fatalf("expected an opaque contact sheet URL, got %q", link)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/fix/media/"), "/")
	if len(parts) != 3 || parts[0] != "gallery" {
		t.Fatalf("unexpected gallery path %q", u.Path)
	}
	payload, err := relay.openGallery(parts[1])
	if err != nil || payload.Platform != "twitter" || len(payload.URLs) != 4 {
		t.Fatalf("openGallery = %+v, %v", payload, err)
	}
	media[2].URL = "https://attacker.example/pic.jpg"
	if got := relay.galleryLink("twitter", media); got != "" {
		t.Fatalf("untrusted media URL got a sheet link: %s", got)
	}
	media[2].URL = "https://pbs.twimg.com/media/pic3.jpg"
	for i := range media {
		media[i].Height = media[i].Width / 2
	}
	if got := relay.galleryLink("twitter", media); got != "" {
		t.Fatalf("landscape gallery got a sheet link: %s", got)
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func TestMixedPortraitGalleryUsesShallowStrip(t *testing.T) {
	dimensions := []image.Point{{X: 1428, Y: 1331}, {X: 1277, Y: 1920}, {X: 500, Y: 699}, {X: 1181, Y: 1754}}
	media := make([]Media, len(dimensions))
	images := make([]image.Image, len(dimensions))
	for i, size := range dimensions {
		media[i] = Media{Type: "photo", Width: size.X, Height: size.Y}
		images[i] = image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
	}
	if !compositeGallery(media) {
		t.Fatal("one near-square landscape should not reject three portraits")
	}
	raw, err := composeGallery(images)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if height := img.Bounds().Dy(); height < 370 || height > 385 || height != gallerySheetHeight(media) {
		t.Fatalf("mixed gallery should match the shallow native strip, got height %d", height)
	}
	media[1].Type = "video"
	if compositeGallery(media) {
		t.Fatal("video gallery must retain native playback")
	}
	media[1].Type = "photo"
	media[1].Width = 0
	if compositeGallery(media) {
		t.Fatal("unknown dimensions must retain native layout")
	}
}
