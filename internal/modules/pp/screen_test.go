package pp

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"

	"github.com/6586x57890143/skua/internal/pixel"
)

func decodes(t *testing.T, data []byte, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != screenW*screenScale || img.Bounds().Dy() != screenH*screenScale {
		t.Fatalf("got a %T of %v", img, img.Bounds())
	}
	if len(data) > 256<<10 {
		t.Errorf("a screen is %d KB; keep it small", len(data)>>10)
	}
}

func TestStatusScreen(t *testing.T) {
	l, into, need := Default.Level(9400 * unit)
	for _, s := range []Sheet{
		{Name: "tester", Class: ClassOf("wanderer"), Need: Default.Need(0), Vigor: 100, Phase: "fresh", Held: 1, RerollReady: true},
		{Portrait: image.NewNRGBA(image.Rect(0, 0, 32, 48)), Name: "steve", Class: ClassOf("knight"), Need: Default.Need(0), Phase: "spent"},
		{Name: "heavywinds", Class: ClassOf("thief"), Level: l, Into: into, Need: need, Total: 9400 * unit, Vigor: 38, Phase: "tired",
			Buffs: []Buff{{"crest", "crest"}, {"plume", "plume"}}, Season: 1, Week: 2, Place: 14, Laurels: 2, Held: 2},
	} {
		data, err := StatusScreen(s)
		decodes(t, data, err)
	}
}

func TestQuestScreen(t *testing.T) {
	data, err := QuestScreen("heavywinds", []QuestLine{
		{Icon: "quill", What: "send 40 messages", Progress: 23, Goal: 40, Reward: Default.QuestPP[0]},
		{Icon: "mirror", What: "post a selfie", Progress: 0, Goal: 1, Tier: 2, Reward: Default.QuestPP[2]},
	}, 2)
	decodes(t, data, err)
	data, err = QuestScreen("steve", nil, 0)
	decodes(t, data, err)
}

// Every class has an emblem drawn, until it has a portrait.
func TestEveryClassHasAnEmblem(t *testing.T) {
	for _, c := range Classes {
		if _, ok := pixel.Icons[classIcons[c.Key]]; !ok {
			t.Errorf("%s has no emblem", c.Name())
		}
	}
}

func TestNumbers(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 61000: "61,000", 1234567: "1,234,567", -1500: "-1,500"} {
		if got := grouped(n); got != want {
			t.Errorf("grouped(%d) = %s; want %s", n, got, want)
		}
	}
	for n, want := range map[int]string{1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 11: "11th", 12: "12th", 13: "13th", 21: "21st", 112: "112th"} {
		if got := ordinal(n); got != want {
			t.Errorf("ordinal(%d) = %s; want %s", n, got, want)
		}
	}
}

func BenchmarkStatusScreen(b *testing.B) {
	s := Sheet{Name: "heavywinds", Class: ClassOf("knight"), Level: 16, Into: 7200 * unit, Need: 9100 * unit, Vigor: 40, Phase: "warm",
		Buffs: []Buff{{"crest", "crest"}, {"banner", "banner"}, {"plume", "plume"}, {"hearth", "hearth iii"}}, Season: 3, Week: 4, Place: 1, Laurels: 3, Held: 3, RerollReady: true}
	for b.Loop() {
		if _, err := StatusScreen(s); err != nil {
			b.Fatal(err)
		}
	}
}

func TestTitleScreen(t *testing.T) {
	data, err := TitleScreen()
	decodes(t, data, err)
}
