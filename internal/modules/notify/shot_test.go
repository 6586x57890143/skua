package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
)

// frames is a fake platform image host: a frame for each link it has,
// matched by prefix so a busted link still finds it.
type frames struct {
	mu    sync.Mutex
	have  map[string]image.Image
	asked []string
}

func (f *frames) fetch(_ context.Context, u string) (image.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, u)
	for k, img := range f.have {
		if strings.HasPrefix(u, k) {
			return img, nil
		}
	}
	return nil, errors.New("not made yet")
}

func (f *frames) add(u string, c color.Color) {
	f.mu.Lock()
	defer f.mu.Unlock()
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := range 360 {
		for x := range 640 {
			img.Set(x, y, c)
		}
	}
	f.have[u] = img
}

// decoded is the picture a card or edit uploads, decoded, and the colour
// near the top left of its frame, clear of the band and the bird.
func decoded(t *testing.T, files []*discord.File) (image.Image, color.RGBA) {
	t.Helper()
	if len(files) != 1 || files[0].Name != shotName {
		t.Fatalf("files: %+v", files)
	}
	b, _ := io.ReadAll(files[0].Reader)
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != shotW || img.Bounds().Dy() != shotH {
		t.Fatalf("size %v", img.Bounds())
	}
	r, g, bl, _ := img.At(shotW/8, shotH/8).RGBA()
	return img, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 255}
}

func near(a, b color.RGBA) bool {
	d := func(x, y uint8) bool { return x-y < 12 || y-x < 12 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B)
}

// A stream's card carries skua's picture: slate until the platform has a
// frame, the frame as soon as it has, a fresh one every restyle, and the
// last one kept, redrawn as over, when the stream ends.
func TestAStreamCarriesItsPicture(t *testing.T) {
	src := newFake()
	m := module(t, src)
	m.follows = []follow{{guild: 1, channel: 10, platform: "fake", account: "bird", name: "Bird"}}
	now := time.Date(2026, 10, 9, 19, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	f := &frames{have: map[string]image.Image{}}
	m.fetchFrame = f.fetch
	p := &poster{}
	ctx := context.Background()
	stream := item{ID: "live:1", Title: "private investigator munki", URL: "https://p/live1", Author: "munkiki", Image: "https://k/t.webp", Started: now, Viewers: 10}
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	red, blue := color.RGBA{200, 40, 40, 255}, color.RGBA{40, 40, 200, 255}

	src.show("bird")
	m.poll(ctx, p, "fake", src)
	src.show("bird", stream)
	m.poll(ctx, p, "fake", src)
	_, mid := decoded(t, p.sent[0].msg.Files)
	if !near(mid, color.RGBA{0x1E, 0x22, 0x28, 255}) {
		t.Fatalf("slate before the frame exists: %v", mid)
	}
	if got := js(p.sent[0].msg); !strings.Contains(got, "attachment://stream.jpg") || !strings.Contains(got, "10 watching") {
		t.Fatalf("the card shows its picture: %s", got)
	}

	// Nothing new: no edit. The frame arrives: one, on it.
	now = now.Add(time.Minute)
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 0 {
		t.Fatal("an edit with nothing new")
	}
	f.add("https://k/t.webp", red)
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 1 {
		t.Fatalf("the frame goes on as soon as it exists: %d", len(p.edits))
	}
	e := p.edits[0].msg
	if _, mid := decoded(t, e.Files); !near(mid, red) {
		t.Fatalf("on the frame: %v", mid)
	}
	if e.Attachments == nil || len(*e.Attachments) != 0 || e.AllowedMentions == nil || len(e.AllowedMentions.Roles) != 0 {
		t.Fatalf("the old picture goes, and no one is pinged: %+v", e)
	}
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 1 {
		t.Fatal("edited again with nothing new")
	}

	// A restyle fetches a fresh frame on a new link; a peak is kept.
	f.add("https://k/t.webp", blue)
	stream.Viewers = 1530
	src.show("bird", stream)
	m.poll(ctx, p, "fake", src)
	stream.Viewers = 900
	src.show("bird", stream)
	now = now.Add(restyle)
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 2 {
		t.Fatalf("a restyle: %d", len(p.edits))
	}
	if _, mid := decoded(t, p.edits[1].msg.Files); !near(mid, blue) {
		t.Fatalf("the fresh frame: %v", mid)
	}
	if !strings.Contains(strings.Join(f.asked, " "), "https://k/t.webp?t="+strconv.FormatInt(now.Unix(), 10)) {
		t.Fatalf("on a new link: %q", f.asked)
	}

	// Over: the frame stays, now ember, with how long and the peak.
	f.mu.Lock()
	f.have = map[string]image.Image{}
	f.mu.Unlock()
	now = now.Add(time.Hour)
	src.show("bird")
	m.poll(ctx, p, "fake", src)
	e = p.edits[2].msg
	if _, mid := decoded(t, e.Files); !near(mid, blue) {
		t.Fatalf("the last frame is kept: %v", mid)
	}
	got := js(e)
	for _, want := range []string{"was live on fake", "attachment://stream.jpg", "1h 11m", "peak 1.5k", `"accent_color":` + strconv.Itoa(brand.ColorEnded)} {
		if !strings.Contains(got, want) {
			t.Errorf("the ended card has no %q: %s", want, got)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.frames) != 0 {
		t.Fatal("an ended stream's frame is let go")
	}
}

// A new title goes on at once; a refused edit is logged; a server with
// notify off gets none.
func TestAStreamPictureFollowsItsTitle(t *testing.T) {
	src := newFake()
	m := module(t, src)
	m.follows = []follow{{guild: 1, channel: 10, platform: "fake", account: "bird", name: "Bird"}}
	p := &poster{}
	ctx := context.Background()
	stream := item{ID: "live:1", Title: "on air", URL: "https://p/1", Author: "bird"}
	src.show("bird")
	m.poll(ctx, p, "fake", src)
	src.show("bird", stream)
	m.poll(ctx, p, "fake", src)
	stream.Title = "still on air"
	src.show("bird", stream)
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 1 {
		t.Fatalf("a new title at once: %d", len(p.edits))
	}
	stream.Title = "off"
	src.show("bird", stream)
	m.on = func(snowflake.ID) bool { return false }
	m.poll(ctx, p, "fake", src)
	m.on = func(snowflake.ID) bool { return true }
	stream.Title = "refused"
	src.show("bird", stream)
	p.editErr = errors.New("gone")
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 1 {
		t.Fatalf("edits: %d", len(p.edits))
	}
}

func TestCompose(t *testing.T) {
	tall := image.NewRGBA(image.Rect(0, 0, 100, 400))
	b, err := compose(tall, strings.Repeat("a very long title ", 10)+"✨ ok", "who / live 5m")
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds() != image.Rect(0, 0, shotW, shotH) {
		t.Fatalf("%v %v", img.Bounds(), err)
	}
	if crop(image.Rect(0, 0, 1920, 1080)) != image.Rect(0, 0, 1920, 1080) ||
		crop(image.Rect(0, 0, 2000, 1080)) != image.Rect(40, 0, 1960, 1080) ||
		crop(image.Rect(0, 0, 1280, 1000)) != image.Rect(0, 140, 1280, 860) {
		t.Fatal("crop keeps the middle in shape")
	}
	if plain(" a✨\tb\n c ") != "a b c" || plain("日本") != "" {
		t.Fatal("plain")
	}
	if headline(item{Title: "日本", Author: "bird"}) != "bird" || headline(item{Title: "hi"}) != "hi" {
		t.Fatal("headline")
	}
	for _, c := range []struct {
		it   item
		over bool
		want string
	}{
		{item{Author: "munkiki", Viewers: 1200}, false, "munkiki / live 5m / 1.2k watching"},
		{item{Author: "munkiki", Viewers: 1530}, true, "munkiki / was live 5m / peak 1.5k"},
		{item{}, true, "was live 5m"},
	} {
		if got := foot(c.it, 5*time.Minute, c.over); got != c.want {
			t.Errorf("%q, want %q", got, c.want)
		}
	}
}

func TestFetchImage(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(buf.Bytes())
		case "/lies.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("not a png"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	if img, err := fetchImage(ctx, srv.Client(), srv.URL+"/ok.png"); err != nil || img.Bounds().Dx() != 4 {
		t.Fatalf("%v", err)
	}
	for _, u := range []string{srv.URL + "/lies.png", srv.URL + "/gone", "::nope", "http://[::1]:0/x"} {
		if _, err := fetchImage(ctx, srv.Client(), u); err == nil {
			t.Errorf("%s decoded", u)
		}
	}
	m := module(t, newFake())
	m.fetchFrame = func(_ context.Context, u string) (image.Image, error) {
		if strings.Contains(u, "hqdefault") {
			return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
		}
		return nil, errors.New("no")
	}
	if _, got := m.frame(ctx, "https://i/vi/a/maxresdefault_live.jpg"); got != "https://i/vi/a/hqdefault_live.jpg" {
		t.Fatalf("youtube's smaller frame: %q", got)
	}
	if img, _ := m.frame(ctx, "http://i/x.jpg"); img != nil {
		t.Fatal("a plain link")
	}
}
