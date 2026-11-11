package forever

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// square is a size by size picture in col, clear around a solid middle,
// as Blizzard's renders and icons are.
func square(size int, col color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := size / 8; y < size*7/8; y++ {
		for x := size / 4; x < size*3/4; x++ {
			img.SetNRGBA(x, y, col)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// gear serves a-b's equipment, its items' media and every picture, and
// counts the equipment reads.
func gear(a *armory) *atomic.Int32 {
	reads := &atomic.Int32{}
	a.routes["/profile/wow/character/a-b/equipment"] = func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"equipped_items": []any{
			map[string]any{"slot": map[string]any{"type": "HEAD"}, "name": "Taurajo Headband", "quality": map[string]any{"type": "RARE"}, "level": map[string]any{"value": 23}, "media": map[string]any{"id": 1}},
			map[string]any{"slot": map[string]any{"type": "FINGER_1"}, "name": "Snake Hoop", "quality": map[string]any{"type": "UNCOMMON"}, "media": map[string]any{"id": 2}},
			map[string]any{"slot": map[string]any{"type": "MAIN_HAND"}, "name": "Spine of the Devourer", "quality": map[string]any{"type": "EPIC"}, "media": map[string]any{"id": 3},
				"transmog": map[string]any{"item": map[string]any{"name": "Arcanite Reaper"}}},
			map[string]any{"slot": map[string]any{"type": "OFF_HAND"}, "name": "Tome", "quality": map[string]any{"type": "ODD"}, "media": map[string]any{"id": 4}},
			map[string]any{"slot": map[string]any{"type": "RANGED"}, "name": "Plaguerot Sprig", "quality": map[string]any{"type": "COMMON"}, "media": map[string]any{"id": 999}},
			map[string]any{"slot": map[string]any{"type": "TABARD_OF_SOMETHING_NEW"}, "name": "?", "media": map[string]any{"id": 5}},
		}})
	}
	a.routes["/data/wow/media/item/"] = func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/data/wow/media/item/")
		if id == "999" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprintf(w, `{"assets":[{"key":"icon","value":"%s/img/%s.png"}]}`, base, id)
	}
	a.routes["/img/"] = func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "render") {
			_, _ = w.Write(square(256, color.NRGBA{0x80, 0x30, 0x20, 0xFF}))
			return
		}
		_, _ = w.Write(square(56, color.NRGBA{0x30, 0x60, 0xA0, 0xFF}))
	}
	return reads
}

func TestArmoryScreen(t *testing.T) {
	m, a, _ := newTest(t)
	reads := gear(a)
	a.set("a-b", prof(1, "A", "B", "Warlock", 39), "x")
	if _, err := m.list(context.Background(), 3, 5, "eu", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	c := *m.chars[key{"eu", 1}]
	c.render = base + "/img/render.png"
	b, err := m.screenOf(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != screenW*scale || img.Bounds().Dy() != screenH*scale || len(b) > 256<<10 {
		t.Fatalf("screen: %v %v, %d KB", err, img.Bounds(), len(b)>>10)
	}
	// The head's well is framed in rare blue, its icon inside, and the
	// figure stands on the stage.
	at := func(x, y int) color.NRGBA { return color.NRGBAModel.Convert(img.At(x*scale, y*scale)).(color.NRGBA) }
	if got := at(18, 34); got != quality["RARE"] {
		t.Fatalf("head well's frame is %v", got)
	}
	if got := at(18+8, 34+8); got != (color.NRGBA{0x30, 0x60, 0xA0, 0xFF}) {
		t.Fatalf("head icon is %v", got)
	}
	if got := at(figureAt.X+32, figureAt.Y+48); got != (color.NRGBA{0x80, 0x30, 0x20, 0xFF}) {
		t.Fatalf("figure is %v", got)
	}
	// The ranged piece's icon 404s, so that screen wasn't whole and isn't
	// kept: the next look draws it again.
	if _, err := m.screenOf(context.Background(), c); err != nil || reads.Load() != 2 {
		t.Fatalf("a screen short of an icon was kept: %v, %d reads", err, reads.Load())
	}
	// With every picture there, it's drawn once per profile.
	delete(a.routes, "/data/wow/media/item/")
	a.routes["/data/wow/media/item/"] = func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"assets":[{"key":"icon","value":"%s/img/x.png"}]}`, base)
	}
	m.icons.have = map[int]image.Image{}
	if _, err := m.screenOf(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if again, err := m.screenOf(context.Background(), c); err != nil || reads.Load() != 3 || !bytes.Equal(again, b) && len(again) == 0 {
		t.Fatalf("drawn again: %v, %d reads", err, reads.Load())
	}
	// A picture anywhere but Blizzard's render host is refused.
	if _, err := m.picture(context.Background(), "https://evil.example/x.png"); err == nil {
		t.Fatal("fetched a picture off the render host")
	}
	if outline("FINGER_1") != "slot_finger" || outline("MAIN_HAND") != "slot_main_hand" || slotAt("NOPE") != image.Pt(-1, -1) {
		t.Fatal("slots")
	}
}

func TestArmoryNoGear(t *testing.T) {
	m, a, _ := newTest(t)
	a.set("a-b", prof(1, "A", "B", "Mage", 12), "x")
	if _, err := m.list(context.Background(), 3, 5, "eu", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	c := *m.chars[key{"eu", 1}]
	c.surname = "gone" // the equipment path 404s
	if _, _, err := m.armory(context.Background(), c); err != errNoGear {
		t.Fatalf("want errNoGear, got %v", err)
	}
}

// sent records what a share posts.
type sent struct {
	fake
	posted []discord.MessageCreate
}

func (s *sent) CreateMessage(_ snowflake.ID, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	s.posted = append(s.posted, m)
	return &discord.Message{}, nil
}

func TestArmoryButtons(t *testing.T) {
	m, a, _ := newTest(t)
	gear(a)
	a.set("a-b", prof(1, "A", "B", "Warlock", 39), "x")
	if _, err := m.list(context.Background(), 3, 5, "eu", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	pick := func(v string) func(p map[string]any) {
		return func(p map[string]any) {
			p["data"] = map[string]any{"custom_id": "forever:armory", "component_type": 3, "values": []any{v}}
		}
	}
	r := &sent{}
	b, _ := coretest.Button(t, "forever:armory", pick("eu.1"))
	b.Client().Rest = r
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if len(r.updates) != 1 || len(r.updates[0].Files) != 1 || !strings.Contains(text((*r.updates[0].Components)[0].(discord.ContainerComponent)), "A B · lv 39") {
		t.Fatalf("armory reply: %+v", r.updates)
	}
	for _, v := range []string{"eu.2", "mars.1", "eu", "eu.x"} {
		b, _ := coretest.Button(t, "forever:armory", pick(v))
		if err := m.click(context.Background(), b); err == nil {
			t.Fatalf("armory for %q passed", v)
		}
	}
	b, _ = coretest.Button(t, "forever:share:eu.1", nil)
	b.Client().Rest = r
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if len(r.posted) != 1 || len(r.posted[0].Files) != 1 || r.posted[0].Flags.Has(discord.MessageFlagEphemeral) {
		t.Fatalf("share: %+v", r.posted)
	}
	// The card the member pressed share on is now the shared one: no
	// button left to post it twice.
	shared := (*r.updates[1].Components)[0].(discord.ContainerComponent)
	if hasButton(shared, "forever:share:eu.1") || len(r.updates[1].Files) != 1 {
		t.Fatal("the shared card can be shared again")
	}
	for _, id := range []string{"forever:share", "forever:share:eu.7"} {
		b, _ := coretest.Button(t, id, nil)
		if err := m.click(context.Background(), b); err == nil {
			t.Fatalf("%s passed", id)
		}
	}
	// A character with no gear says so in place.
	a.routes["/profile/wow/character/a-b/equipment"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }
	m.screens.have = map[key]drawn{}
	b, _ = coretest.Button(t, "forever:armory", pick("eu.1"))
	b.Client().Rest = r
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text((*r.updates[2].Components)[0].(discord.ContainerComponent)), "doesn't show A B's gear yet") {
		t.Fatal("no gear not said")
	}
}

// TestShareUndrawn is a share whose screen can't be drawn: it says why, in
// the error colour, and posts nothing.
func TestShareUndrawn(t *testing.T) {
	m, a, _ := newTest(t)
	a.set("a-b", prof(1, "A", "B", "Mage", 12), "x")
	a.routes["/profile/wow/character/a-b/equipment"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }
	if _, err := m.list(context.Background(), 3, 5, "eu", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	r := &sent{}
	b, _ := coretest.Button(t, "forever:share:eu.1", nil)
	b.Client().Rest = r
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	card := r.followups[0].Components[0].(discord.ContainerComponent)
	if len(r.posted) != 0 || !strings.Contains(text(card), "doesn't show A B's gear yet") || card.AccentColor == 0x6E9A6A {
		t.Fatalf("undrawn share: %d posted, %q", len(r.posted), text(card))
	}
	worn := piece{Name: "Tome"}
	if worn.label() != "Tome" {
		t.Fatal("label")
	}
	worn.Transmog = &struct {
		Item named `json:"item"`
	}{Item: named{Name: "Mightstone Hammer"}}
	if worn.label() != "Mightstone Hammer *" {
		t.Fatal("transmog label")
	}
}
