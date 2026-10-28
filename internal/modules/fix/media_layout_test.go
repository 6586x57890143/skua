package fix

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand/v2"
	"net/url"
	"strings"
	"testing"
)

func TestOverBudgetGalleryRetainsNativeLayout(t *testing.T) {
	media := make([]Media, 4)
	for i := range media {
		media[i] = Media{Type: "photo", Width: 2400, Height: 3200}
	}
	if compositeGallery(media) {
		t.Fatal("known oversized collection must not receive a relay URL that will fail decoding")
	}
}

func TestGalleryLayoutGeometry(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for n := 2; n <= 10; n++ {
		for trial := 0; trial < 40; trial++ {
			media := make([]Media, n)
			for i := range media {
				media[i] = Media{Type: "photo", Width: 200 + r.IntN(1800), Height: 200 + r.IntN(1800)}
			}
			p, ok := planGallery(media)
			if !ok || len(p.Tiles) != n || p.Width > sheetWidth || p.Height > sheetHeight {
				t.Fatalf("invalid plan: %+v", p)
			}
			for i, tile := range p.Tiles {
				if tile.Empty() || !tile.In(image.Rect(0, 0, p.Width, p.Height)) {
					t.Fatal(tile, p)
				}
				srcAspect := float64(media[i].Width) / float64(media[i].Height)
				if math.Abs(float64(tile.Dx())-srcAspect*float64(tile.Dy())) > 1+srcAspect {
					t.Fatalf("stretched image %v for %v", tile, media[i])
				}
				for j := 0; j < i; j++ {
					if tile.Overlaps(p.Tiles[j]) {
						t.Fatal("overlapping tiles", p)
					}
				}
				if i > 0 && (tile.Min.Y < p.Tiles[i-1].Min.Y || tile.Min.Y == p.Tiles[i-1].Min.Y && tile.Min.X < p.Tiles[i-1].Min.X) {
					t.Fatal("image order changed", p)
				}
			}
			again, _ := planGallery(media)
			if again.Score != p.Score {
				t.Fatal("non-deterministic layout")
			}
		}
	}
}
func TestGalleryPreservesEntireFrames(t *testing.T) {
	sizes := []image.Point{{X: 500, Y: 800}, {X: 900, Y: 600}, {X: 400, Y: 1000}}
	imgs := make([]image.Image, len(sizes))
	media := make([]Media, len(sizes))
	for i, size := range sizes {
		img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				c := color.RGBA{G: 100, A: 255}
				if x < 30 || x >= size.X-30 || y < 30 || y >= size.Y-30 {
					c = color.RGBA{R: 255, A: 255}
				}
				img.SetRGBA(x, y, c)
			}
		}
		imgs[i] = img
		media[i] = Media{Width: size.X, Height: size.Y}
	}
	raw, err := composeGallery(imgs)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := planGallery(media)
	for _, tile := range plan.Tiles {
		points := []image.Point{{X: tile.Min.X + 2, Y: (tile.Min.Y + tile.Max.Y) / 2}, {X: tile.Max.X - 3, Y: (tile.Min.Y + tile.Max.Y) / 2}, {X: (tile.Min.X + tile.Max.X) / 2, Y: tile.Min.Y + 2}, {X: (tile.Min.X + tile.Max.X) / 2, Y: tile.Max.Y - 3}}
		for _, point := range points {
			red, _, _, alpha := img.At(point.X, point.Y).RGBA()
			if red < 50000 || alpha != 65535 {
				t.Fatalf("frame edge cropped at %v", point)
			}
		}
	}
}
func TestGalleryAcrossTrustedPlatforms(t *testing.T) {
	hosts := map[string]string{"twitter": "pbs.twimg.com", "bluesky": "cdn.bsky.app", "reddit": "i.redd.it", "instagram": "scontent.cdninstagram.com", "threads": "scontent.fbcdn.net", "pixiv": "i.pximg.net", "pinterest": "i.pinimg.com", "tumblr": "media.tumblr.com", "tiktok": "img.tiktokcdn.com", "bilibili": "i0.hdslb.com", "deviantart": "images.wixmp.com"}
	relay := newMediaRelay(&API{}, "https://hooks.example", "a-secret-long-enough-for-tests")
	for platform, host := range hosts {
		media := make([]Media, 6)
		for i := range media {
			media[i] = Media{URL: "https://" + host + "/photo" + strings.Repeat("x", i) + ".png?" + strings.Repeat("shared-query=123&", 30), Type: "photo", Width: 500, Height: 900}
		}
		link := relay.galleryLink(platform, media)
		if link == "" {
			t.Fatal("no composite for", platform)
		}
		u, _ := url.Parse(link)
		parts := strings.Split(strings.TrimPrefix(u.Path, "/fix/media/"), "/")
		payload, err := relay.openGallery(parts[1])
		if err != nil || len(payload.URLs) != 6 {
			t.Fatal(platform, payload, err)
		}
	}
}
