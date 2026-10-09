package notify

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // previews come as any of these
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp" // kick's previews

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// A stream's card carries skua's own picture of it, uploaded with the
// card: the platform's frame with a slate band along its foot holding the
// bird, the stream's title, who, and how long it has been on. It is drawn
// again each restyle and once more when the stream ends, so the picture
// outlives the platform's link. Until the platform has a frame, the band
// sits on a plain slate field.

// shotName is the picture's file name on its card.
const shotName = "stream.jpg"

const (
	shotW, shotH = 1280, 720
	band         = 136     // the watermark band's height
	maxImage     = 8 << 20 // the most of a frame read
)

var (
	slate = color.NRGBA{0x1E, 0x22, 0x28, 0xFF} // brand.ColorPrimary
	veil  = color.NRGBA{0x1E, 0x22, 0x28, 0xD8}
	ink   = color.NRGBA{0xE4, 0xE8, 0xEE, 0xFF}
	steel = color.NRGBA{0x8A, 0x9C, 0xB2, 0xFF} // brand.ColorInfo
)

// bird is skua's avatar, 128 px, decoded once.
var bird = sync.OnceValue(func() image.Image {
	img, _, err := image.Decode(bytes.NewReader(brand.Art("skua_avatar")))
	if err != nil {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}
	return img
})

// fetchImage gets u and decodes it.
func fetchImage(ctx context.Context, c *http.Client, u string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
		return nil, fmt.Errorf("notify: %s answered %d %s", req.URL.Host, res.StatusCode, res.Header.Get("Content-Type"))
	}
	img, _, err := image.Decode(io.LimitReader(res.Body, maxImage))
	return img, err
}

// frame is the first of u and its smaller stand ins that decodes, and the
// link it came from; nil and "" when none does.
func (m *Module) frame(ctx context.Context, u string) (image.Image, string) {
	if !strings.HasPrefix(u, "https://") {
		return nil, ""
	}
	tries := []string{u}
	if strings.Contains(u, "/maxresdefault") {
		tries = append(tries, strings.Replace(u, "/maxresdefault", "/hqdefault", 1))
	}
	// A slow host costs the card its frame, not its timing.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, t := range tries {
		if img, err := m.fetchFrame(ctx, t); err == nil {
			return img, t
		}
	}
	return nil, ""
}

// compose draws a stream's picture: base, or slate without one, under the
// band with head in large type and foot below it.
func compose(base image.Image, head, foot string) ([]byte, error) {
	out := image.NewRGBA(image.Rect(0, 0, shotW, shotH))
	if base == nil {
		// No frame yet: slate, and the bird at two px a cell in the middle
		// of what the band leaves.
		draw.Draw(out, out.Bounds(), &image.Uniform{slate}, image.Point{}, draw.Src)
		b := bird().Bounds()
		big := image.Rect(0, 0, 2*b.Dx(), 2*b.Dy())
		big = big.Add(image.Pt((shotW-big.Dx())/2, (shotH-band-big.Dy())/2))
		draw.NearestNeighbor.Scale(out, big, bird(), b, draw.Over, nil)
	} else {
		draw.ApproxBiLinear.Scale(out, out.Bounds(), base, crop(base.Bounds()), draw.Src, nil)
	}
	draw.Draw(out, image.Rect(0, shotH-band, shotW, shotH), &image.Uniform{veil}, image.Point{}, draw.Over)
	b := bird()
	at := image.Pt(16, shotH-band+(band-b.Bounds().Dy())/2)
	draw.Draw(out, b.Bounds().Sub(b.Bounds().Min).Add(at), b, b.Bounds().Min, draw.Over)
	x, width := at.X+b.Bounds().Dx()+16, shotW-(at.X+b.Bounds().Dx()+16)-24
	write(out, head, x, shotH-band+20, 3, ink, width)
	write(out, foot, x, shotH-band+20+3*13+14, 2, steel, width)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// crop is the middle of r in the picture's shape, so a frame of another
// shape fills it without stretching.
func crop(r image.Rectangle) image.Rectangle {
	w, h := r.Dx(), r.Dy()
	if w*shotH > h*shotW {
		cw := h * shotW / shotH
		return image.Rect(r.Min.X+(w-cw)/2, r.Min.Y, r.Min.X+(w-cw)/2+cw, r.Max.Y)
	}
	ch := w * shotH / shotW
	return image.Rect(r.Min.X, r.Min.Y+(h-ch)/2, r.Max.X, r.Min.Y+(h-ch)/2+ch)
}

// write sets s in the 7 by 13 pixel face at scale px a cell, its top left
// at x, y, cut to width with "..." when it runs over.
func write(dst *image.RGBA, s string, x, y, scale int, c color.Color, width int) {
	s = plain(s)
	if s == "" {
		return
	}
	if fit := width / (7 * scale); len(s) > fit {
		s = strings.TrimSpace(s[:max(fit-3, 0)]) + "..."
	}
	face := basicfont.Face7x13
	mask := image.NewAlpha(image.Rect(0, 0, 7*len(s), 13))
	(&font.Drawer{Dst: mask, Src: image.Opaque, Face: face, Dot: fixed.P(0, face.Ascent)}).DrawString(s)
	big := image.NewAlpha(image.Rect(0, 0, mask.Rect.Dx()*scale, 13*scale))
	draw.NearestNeighbor.Scale(big, big.Bounds(), mask, mask.Bounds(), draw.Src, nil)
	draw.DrawMask(dst, big.Bounds().Add(image.Pt(x, y)), &image.Uniform{c}, image.Point{}, big, image.Point{}, draw.Over)
}

// plain is s in what the face has, printable ASCII, on one line.
func plain(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7E {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// shot is a stream's picture as its card is sent: the file, and the item
// pointing at it.
func shot(it item, pic []byte) (item, *discord.File) {
	it.Image = "attachment://" + shotName
	alt := line(first(it.Title, it.Author), 120)
	return it, discord.NewFile(shotName, alt, bytes.NewReader(pic))
}

// headline is a stream's picture's large line: its title, or who.
func headline(it item) string {
	if t := plain(it.Title); t != "" {
		return t
	}
	return it.Author
}

// foot is a stream's picture's small line: who, how long, and the
// audience: live, now; over, its peak.
func foot(it item, length time.Duration, over bool) string {
	parts := []string{plain(it.Author), "live " + core.Duration(length)}
	if over {
		parts[1] = "was " + parts[1]
	}
	switch {
	case it.Viewers > 0 && over:
		parts = append(parts, "peak "+count(it.Viewers))
	case it.Viewers > 0:
		parts = append(parts, count(it.Viewers)+" watching")
	}
	if parts[0] == "" {
		parts = parts[1:]
	}
	return strings.Join(parts, " / ")
}
