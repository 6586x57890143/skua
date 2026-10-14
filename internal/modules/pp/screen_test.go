package pp

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func decodes(t *testing.T, data []byte, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := img.(*image.Paletted); !ok || img.Bounds().Dx() != screenW*screenScale || img.Bounds().Dy() != screenH*screenScale {
		t.Fatalf("got a %T of %v", img, img.Bounds())
	}
	if len(data) > 32<<10 {
		t.Errorf("a screen is %d KB; keep it small", len(data)>>10)
	}
}

func TestStatusScreen(t *testing.T) {
	l, into, need := Default.Level(9400 * unit)
	for _, s := range []Sheet{
		{Name: "tester", Class: ClassOf("wanderer"), Need: Default.Need(0), Vigor: 100, Phase: "fresh", Held: 1, RerollReady: true},
		{Name: "heavywinds", Class: ClassOf("thief"), Level: l, Into: into, Need: need, Total: 9400 * unit, Vigor: 38, Phase: "tired",
			Buffs: []string{"crest", "plume"}, Season: 1, Week: 2, Place: 14, Held: 2},
	} {
		data, err := StatusScreen(s)
		decodes(t, data, err)
	}
}

func TestQuestScreen(t *testing.T) {
	data, err := QuestScreen("heavywinds", []QuestLine{
		{What: "send 40 messages", Progress: 23, Goal: 40, Reward: Default.QuestPP[0]},
		{What: "post a selfie", Progress: 0, Goal: 1, Tier: 2, Reward: Default.QuestPP[2]},
	}, true)
	decodes(t, data, err)
	data, err = QuestScreen("steve", nil, false)
	decodes(t, data, err)
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
		Buffs: []string{"crest", "banner", "plume", "hearth iii"}, Season: 3, Week: 4, Place: 1, Held: 3, RerollReady: true}
	for b.Loop() {
		if _, err := StatusScreen(s); err != nil {
			b.Fatal(err)
		}
	}
}
