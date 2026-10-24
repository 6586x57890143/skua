package pp

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

func TestOwnName(t *testing.T) {
	for raw, want := range map[string]string{
		"  the   lamplighter ": "the lamplighter",
		"heron":                "heron",
	} {
		if got, err := ownName(raw); err != nil || got != want {
			t.Errorf("%q: %q %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "   ", strings.Repeat("a", nameMost+1), "@everyone", "<@175928847299117063>", "https://x.y", "discord staff", "a#b"} {
		if _, err := ownName(raw); !errors.Is(err, errOwnName) {
			t.Errorf("%q passed: %v", raw, err)
		}
	}
}

// Colours come as hex or a livery's name, and each is brought into the
// band that reads on both themes, its hue kept.
func TestOwnColors(t *testing.T) {
	got, err := ownColors("#c66e34, ember stair", 2)
	if err != nil || len(got) != 2 || got[0] != 0xc66e34 || got[1] != 0xc66e34 {
		t.Fatalf("hex then a livery: %06x %v", got, err)
	}
	if got, _ := ownColors("stair", 1); got[0] != 0x6286c3 {
		t.Errorf("a livery's name: %06x", got[0])
	}
	for _, raw := range []string{"", "#12345", "velvet", "#c66e34"} {
		if _, err := ownColors(raw, 2); !errors.Is(err, errOwnColors) {
			t.Errorf("%q: %v", raw, err)
		}
	}
	if got, err := ownColors("", 0); err != nil || len(got) != 0 {
		t.Errorf("nacre takes none: %v %v", got, err)
	}
	for _, c := range []uint32{0x000000, 0xffffff, 0x0000ff, 0xffff00, 0x101820, 0xc66e34} {
		b := inBand(c)
		if l := luminance(b); l < bandLow-0.01 || l > bandHigh+0.01 {
			t.Errorf("%06x came to %06x, luminance %.3f", c, b, l)
		}
		if h1, s1, _ := toHSL(c); s1 > 0.2 {
			if h2, _, _ := toHSL(b); h1-h2 > 3 || h2-h1 > 3 {
				t.Errorf("%06x lost its hue: %.0f to %.0f", c, h1, h2)
			}
		}
	}
	if inBand(0xc66e34) != 0xc66e34 {
		t.Error("a colour already in the band was moved")
	}
}

// A device is drawn again as a PNG at most iconSide square; anything not
// a picture, or from anywhere but Discord, is refused.
func TestDevice(t *testing.T) {
	big := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	big.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	var raw bytes.Buffer
	_ = png.Encode(&raw, big)
	out, err := redrawIcon(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil || img.Bounds().Dx() != iconSide || img.Bounds().Dy() != iconSide/2 {
		t.Fatalf("redrawn as %v, %v", img.Bounds(), err)
	}
	if _, err := redrawIcon([]byte("GIF89a not really")); !errors.Is(err, errOwnIcon) {
		t.Errorf("garbage: %v", err)
	}
	png := "image/png"
	for _, a := range []discord.Attachment{
		{URL: "https://example.com/a.png", Size: 10, ContentType: &png},
		{URL: "http://cdn.discordapp.com/a.png", Size: 10, ContentType: &png},
		{URL: "https://cdn.discordapp.com.evil.test/a.png", Size: 10, ContentType: &png},
		{URL: "https://cdn.discordapp.com/a.png", Size: iconMost + 1, ContentType: &png},
	} {
		if _, err := fetchIcon(context.Background(), a); !errors.Is(err, errOwnIcon) {
			t.Errorf("%s (%d): %v", a.URL, a.Size, err)
		}
	}
	text := "text/plain"
	if _, err := fetchIcon(context.Background(), discord.Attachment{URL: "https://cdn.discordapp.com/a.txt", Size: 5, ContentType: &text}); !errors.Is(err, errOwnIcon) {
		t.Errorf("a text file: %v", err)
	}
}

// The form offers only what the perch may wear, and the device only from
// the drowned bell.
func TestOwnModal(t *testing.T) {
	chitin := js(ownModal(&member{rung: 3}, nil))
	if !strings.Contains(chitin, `"value":"plain"`) || strings.Contains(chitin, `"value":"silk"`) || strings.Contains(chitin, `"device"`) {
		t.Errorf("chitin: %s", chitin)
	}
	bell := js(ownModal(&member{rung: 5}, &draft{name: "x", style: ownStyles[1], colors: []uint32{0xc66e34, 0x6286c3}}))
	for _, want := range []string{`"value":"nacre"`, `"device"`, `"value":"x"`, "#c66e34 #6286c3", `"default":true`} {
		if !strings.Contains(bell, want) {
			t.Errorf("the bell's form has no %s: %s", want, bell)
		}
	}
}

// A petition read from the form is kept as the member's draft and shown;
// the perch, the weave and a name already taken are refused.
func TestOwnPetition(t *testing.T) {
	m, y := yardServer(4, 10000)
	m.st.server(3).yardOpen = true
	y.roles = append(y.roles, discord.Role{ID: 70, Name: "Moderators"})
	submit := func(name, style, colors string) error {
		e, _ := coretest.Modal(t, ownForm, map[string]string{"name": name, "colors": colors}, func(p map[string]any) {
			rows := p["data"].(map[string]any)["components"].([]any)
			p["data"].(map[string]any)["components"] = append(rows, map[string]any{"type": 18, "id": 9,
				"component": map[string]any{"type": 3, "custom_id": "style", "values": []any{style}}})
		})
		e.Client().Rest = y
		return m.submitOwn(context.Background(), e)
	}
	if err := submit("the lamplighter", "silk", "#c66e34 stair"); err != nil {
		t.Fatal(err)
	}
	d := m.st.server(3).drafts[5]
	if d == nil || d.name != "the lamplighter" || d.style.key != "silk" || len(d.colors) != 2 || len(y.edits) != 1 {
		t.Fatalf("the draft: %+v, edits %d", d, len(y.edits))
	}
	if !strings.Contains(y.edits[0], "change the petition") {
		t.Errorf("the screen: %s", y.edits[0])
	}
	for _, c := range []struct {
		name, style, colors string
		want                error
	}{
		{"moderators", "plain", "ember", errOwnTaken},
		{"x", "nacre", "", errOwnStyle},
		{"x", "plain", "velvet", errOwnColors},
		{"@here", "plain", "ember", errOwnName},
	} {
		y.followups = nil
		if err := submit(c.name, c.style, c.colors); err != nil || len(y.followups) != 1 || y.followups[0] != c.want.Error() {
			t.Errorf("%+v: %v %v", c, err, y.followups)
		}
	}
	// Below chitin there's no petition to write.
	m.st.server(3).members[5].rung = 2
	press := func(id string) error {
		e, _ := coretest.Button(t, id, nil)
		return m.press(context.Background(), e)
	}
	for _, id := range []string{"pp:own", "pp:ownwrite"} {
		if err := press(id); !errors.Is(err, errOwnPerch) {
			t.Errorf("%s from shale: %v", id, err)
		}
	}
}

// The yard offers a livery of one's own from the chitin perch, and its
// screen shows the draft, or that none is written.
func TestOwnFromTheYard(t *testing.T) {
	m, _ := yardServer(3, 100)
	m.st.server(3).yardOpen = true
	open := func(id string) string {
		e, sent := coretest.Button(t, id, nil)
		if err := m.press(context.Background(), e); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return js(*sent)
	}
	if got := open("pp:yard"); !strings.Contains(got, "pp:own") {
		t.Error("no way to a livery of one's own from chitin")
	}
	if got := open("pp:own"); !strings.Contains(got, "write a petition") || !strings.Contains(got, "no petition written yet") {
		t.Errorf("the empty petition: %s", got)
	}
	e, _ := coretest.Button(t, "pp:ownwrite", nil)
	modal := false
	e.Respond = func(kind discord.InteractionResponseType, _ discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		modal = kind == discord.InteractionResponseTypeModal
		return nil
	}
	if err := m.press(context.Background(), e); err != nil || !modal {
		t.Errorf("the form didn't open: %v", err)
	}
	m.st.server(3).members[5].rung = 2
	if got := open("pp:yard"); strings.Contains(got, `"pp:own"`) {
		t.Error("shale is offered a livery of its own")
	}
}
