package forever

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // Blizzard's icons
	"image/png"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/6586x57890143/skua/internal/pixel"
	xdraw "golang.org/x/image/draw"
)

// The armory screen: a character the way the game's character pane shows
// it, at skua's resolution. The figure is Blizzard's render of them as they
// stand, transmog and all, rebuilt to 64 by 96 cells; the gear is each
// piece's own icon rebuilt to 14 in a well framed in its quality; the names
// are set in skua's faces. The frame, wells and slot outlines are original
// art (art/), made for skua.

//go:embed art/*.png
var artFS embed.FS

var (
	artOnce sync.Once
	arts    map[string]image.Image
)

// art is an embedded picture by name, decoded once.
func art(name string) image.Image {
	artOnce.Do(func() {
		arts = map[string]image.Image{}
		entries, _ := artFS.ReadDir("art")
		for _, e := range entries {
			b, _ := artFS.ReadFile("art/" + e.Name())
			if img, err := png.Decode(bytes.NewReader(b)); err == nil {
				arts[strings.TrimSuffix(e.Name(), ".png")] = img
			}
		}
	})
	return arts[name]
}

// The pane: the canvas and where everything sits on it (art/ was drawn to
// these).
const (
	screenW, screenH = 384, 216
	slotSize         = 18
	stageW, stageH   = 64, 96
	scale            = 3 // what Discord shows: 1152 by 648
)

var (
	figureAt = image.Pt(160, 62)              // the stage, stageW by stageH at 1x
	barRect  = image.Rect(152, 168, 232, 170) // under the stage: the level to 60
	left     = []string{"HEAD", "NECK", "SHOULDER", "BACK", "CHEST", "SHIRT", "TABARD", "WRIST"}
	right    = []string{"HANDS", "WAIST", "LEGS", "FEET", "FINGER_1", "FINGER_2", "TRINKET_1", "TRINKET_2"}
	bottom   = []string{"MAIN_HAND", "OFF_HAND", "RANGED"}
)

func slotAt(slot string) image.Point {
	for i, s := range left {
		if s == slot {
			return image.Pt(18, 34+20*i)
		}
	}
	for i, s := range right {
		if s == slot {
			return image.Pt(348, 34+20*i)
		}
	}
	for i, s := range bottom {
		if s == slot {
			return image.Pt(160+23*i, 190)
		}
	}
	return image.Point{-1, -1}
}

// outline is a slot's empty silhouette's name: FINGER_1 is finger.
func outline(slot string) string {
	s := strings.ToLower(slot)
	if i := strings.LastIndex(s, "_"); i > 0 && s[i+1] >= '0' && s[i+1] <= '9' {
		s = s[:i]
	}
	return "slot_" + s
}

// quality is the game's colour for each item quality.
var quality = map[string]color.NRGBA{
	"POOR": {0x9D, 0x9D, 0x9D, 0xFF}, "COMMON": {0xFF, 0xFF, 0xFF, 0xFF}, "UNCOMMON": {0x1E, 0xFF, 0x00, 0xFF},
	"RARE": {0x00, 0x70, 0xDD, 0xFF}, "EPIC": {0xA3, 0x35, 0xEE, 0xFF}, "LEGENDARY": {0xFF, 0x80, 0x00, 0xFF},
}

// equipment is the slice of a character's equipment the screen shows.
type equipment struct {
	Items []piece `json:"equipped_items"`
}

type piece struct {
	Slot struct {
		Type string `json:"type"`
	} `json:"slot"`
	Name    string `json:"name"`
	Quality struct {
		Type string `json:"type"`
	} `json:"quality"`
	Level struct {
		Value int `json:"value"`
	} `json:"level"`
	Media struct {
		ID int `json:"id"`
	} `json:"media"`
	Transmog *struct {
		Item named `json:"item"`
	} `json:"transmog"`
}

// picture fetches one of Blizzard's renders.
func (m *Module) picture(ctx context.Context, url string) (image.Image, error) {
	if !strings.HasPrefix(url, m.api.render) {
		return nil, fmt.Errorf("forever: not a render: %q", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := m.api.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("forever: render %s", res.Status)
	}
	img, _, err := image.Decode(io.LimitReader(res.Body, 4<<20))
	return img, err
}

// icons keeps every item icon skua has rebuilt, by its media id: an icon
// never changes, so it is fetched once.
// ponytail: unbounded; each is 14x14, so even every item in the game is a
// few MB. An LRU if the armory ever reads more than that.
type icons struct {
	mu   sync.Mutex
	have map[int]image.Image
}

// icon is item media's icon on the 14 cell grid, nil when Blizzard has
// none.
func (m *Module) icon(ctx context.Context, region string, media int) image.Image {
	m.icons.mu.Lock()
	img, ok := m.icons.have[media]
	m.icons.mu.Unlock()
	if ok {
		return img
	}
	var md struct {
		Assets []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"assets"`
	}
	if _, _, err := m.api.get(ctx, region, "static", fmt.Sprintf("/data/wow/media/item/%d", media), "", &md); err != nil {
		return nil
	}
	for _, a := range md.Assets {
		if a.Key != "icon" {
			continue
		}
		src, err := m.picture(ctx, a.Value)
		if err != nil {
			return nil
		}
		// Blizzard's bevel off, then the inside on the grid.
		img = pixel.Rebuild(src, src.Bounds().Inset(src.Bounds().Dx()/14), 14, 14, 8)
		m.icons.mu.Lock()
		m.icons.have[media] = img
		m.icons.mu.Unlock()
		return img
	}
	return nil
}

// figure is c's render as Blizzard draws it, cut to the figure and scaled
// smoothly to fill the stage at the size Discord shows it, nil when
// Blizzard has none yet. The character is the one thing on the pane not
// in pixels: it's who the screen is about, and the game's own render of
// them reads better whole.
func (m *Module) figure(ctx context.Context, c char) *image.NRGBA {
	if c.render == "" {
		return nil
	}
	src, err := m.picture(ctx, c.render)
	if err != nil {
		return nil
	}
	out := image.NewNRGBA(image.Rect(0, 0, stageW*scale, stageH*scale))
	xdraw.CatmullRom.Scale(out, out.Rect, src, pixel.Fitted(pixel.Solid(src), stageW, stageH), xdraw.Src, nil)
	return out
}

// pane is an armory screen in its two parts: the pixel pane at 1x and the
// figure at the size it's shown, laid on the stage only when it's sent.
type pane struct {
	screen *image.NRGBA
	fig    *image.NRGBA
}

// errNoGear is a character Blizzard has no equipment for yet.
var errNoGear = errors.New("forever: no equipment")

// armory is c's armory screen, and whether every picture on it came: one
// drawn short of an icon or the figure isn't worth keeping.
func (m *Module) armory(ctx context.Context, c char) (pane, bool, error) {
	var eq equipment
	if _, _, err := m.api.get(ctx, c.region, "profile", charPath(c.slug())+"/equipment", "", &eq); err != nil {
		if errors.Is(err, errGone) {
			return pane{}, false, errNoGear
		}
		return pane{}, false, err
	}
	pics := make([]image.Image, len(eq.Items))
	var wg sync.WaitGroup
	for i, p := range eq.Items {
		wg.Go(func() { pics[i] = m.icon(ctx, c.region, p.Media.ID) })
	}
	var fig *image.NRGBA
	wg.Go(func() { fig = m.figure(ctx, c) })
	wg.Wait()
	whole := fig != nil || c.render == ""
	for _, p := range pics {
		whole = whole && p != nil
	}
	return pane{screen: screen(c, eq.Items, pics), fig: fig}, whole, nil
}

// screen draws the pixel pane: the frame, the title, every slot filled or
// empty, the names beside them. The stage is left for the figure.
func screen(c char, items []piece, pics []image.Image) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, screenW, screenH))
	// The stage is clear in the frame: the figure stands on the pane's
	// darkest slate.
	draw.Draw(img, img.Rect, &image.Uniform{color.NRGBA{0x17, 0x1B, 0x24, 0xFF}}, image.Point{}, draw.Src)
	if frame := art("armory_frame"); frame != nil {
		draw.Draw(img, img.Rect, frame, image.Point{}, draw.Over)
	}
	shadow := color.NRGBA{0x0E, 0x0D, 0x12, 0xFF}
	dim := color.NRGBA{0x9A, 0xA4, 0xB2, 0xFF}
	cc := classColor[c.class]
	name := color.NRGBA{uint8(cc >> 16), uint8(cc >> 8), uint8(cc), 0xFF}
	if cc == 0 {
		name = color.NRGBA{0xDB, 0xDE, 0xE1, 0xFF}
	}
	x := pixel.Bold.Draw(img, 16, 9, fullName(c), name, shadow)
	// The faces are ASCII: facts part with a wide space, not a dot.
	about := fmt.Sprintf("lv %d %s %s   %s %s", c.level, c.race, c.class, c.region, c.ruleset)
	if c.ilvl > 0 {
		about += fmt.Sprintf("   ilvl %d", c.ilvl)
	}
	pixel.Light.Draw(img, x+8, 9, pixel.Light.Cut(about, 364-x-8), dim, nil)

	gold := color.NRGBA{0xE0, 0xA6, 0x4B, 0xFF}
	fill := barRect
	fill.Max.X = fill.Min.X + fill.Dx()*min(c.level, Cap)/Cap
	draw.Draw(img, fill, &image.Uniform{gold}, image.Point{}, draw.Src)

	worn := map[string]int{}
	for i, p := range items {
		worn[p.Slot.Type] = i
	}
	for _, slot := range append(append(append([]string{}, left...), right...), bottom...) {
		at := slotAt(slot)
		i, ok := worn[slot]
		if !ok {
			if o := art(outline(slot)); o != nil {
				draw.Draw(img, image.Rectangle{at.Add(image.Pt(1, 1)), at.Add(image.Pt(17, 17))}, o, image.Point{}, draw.Over)
			}
			continue
		}
		p := items[i]
		if pics[i] != nil {
			draw.Draw(img, image.Rectangle{at.Add(image.Pt(2, 2)), at.Add(image.Pt(16, 16))}, pics[i], image.Point{}, draw.Over)
		}
		if q := art("quality_" + strings.ToLower(p.Quality.Type)); q != nil {
			draw.Draw(img, image.Rectangle{at, at.Add(image.Pt(slotSize, slotSize))}, q, image.Point{}, draw.Over)
		}
		col, ok := quality[p.Quality.Type]
		if !ok {
			col = quality["COMMON"]
		}
		label := p.label()
		switch {
		case slices.Contains(left, slot):
			pixel.Light.Draw(img, at.X+slotSize+4, at.Y+3, pixel.Light.Cut(label, 100), col, shadow)
		case slices.Contains(right, slot):
			s := pixel.Light.Cut(label, 100)
			pixel.Light.Draw(img, at.X-4-pixel.Light.Width(s), at.Y+3, s, col, shadow)
		case slot == "MAIN_HAND":
			s := pixel.Light.Cut(label, 110)
			pixel.Light.Draw(img, 156-pixel.Light.Width(s), 199, s, col, shadow)
		default: // off hand and ranged share the line right of the weapons
			_, offHand := worn["OFF_HAND"]
			_, ranged := worn["RANGED"]
			nx, room := 232, 110
			if offHand && ranged {
				room = 52
				if slot == "RANGED" {
					nx = 232 + pixel.Light.Width(pixel.Light.Cut(items[worn["OFF_HAND"]].label(), room)) + 6
				}
			}
			pixel.Light.Draw(img, nx, 199, pixel.Light.Cut(label, room), col, shadow)
		}
	}
	return img
}

// label is the name a piece shows: its transmog's, marked, when it wears
// one.
func (p piece) label() string {
	if p.Transmog != nil && p.Transmog.Item.Name != "" {
		return p.Transmog.Item.Name + " *"
	}
	return p.Name
}

// encode is the pane as Discord shows it: the pixels scale times larger,
// cell for cell, and the figure laid on the stage at its own size, as a
// PNG.
func encode(screen, fig image.Image) ([]byte, error) {
	big := image.NewNRGBA(image.Rect(0, 0, screen.Bounds().Dx()*scale, screen.Bounds().Dy()*scale))
	for y := range big.Rect.Dy() {
		for x := range big.Rect.Dx() {
			big.Set(x, y, screen.At(x/scale, y/scale))
		}
	}
	if fig != nil {
		at := figureAt.Mul(scale)
		draw.Draw(big, image.Rectangle{at, at.Add(fig.Bounds().Size())}, fig, image.Point{}, draw.Over)
	}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&b, big); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
