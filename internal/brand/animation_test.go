package brand

import (
	"bytes"
	"context"
	"image/gif"
	"strings"
	"testing"

	"github.com/6586x57890143/skua/internal/guard"
)

func TestFixLoadingAnimationBudgetAndSync(t *testing.T) {
	unsynced(t)
	raw, err := emojiArt.ReadFile("emoji/fix_loading.gif")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= 10<<10 {
		t.Fatalf("loading emoji exceeds art budget: %d bytes", len(raw))
	}
	animation, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(animation.Image) != 3 || animation.Config.Width != 128 || animation.Config.Height != 128 || animation.LoopCount != 20 {
		t.Fatal("animation dimensions or loop changed")
	}
	for i, frame := range animation.Image {
		if frame.Bounds().Dx() != 128 || frame.Bounds().Dy() != 128 || animation.Delay[i] < 20 {
			t.Fatal("invalid frame or rapid flashing")
		}
	}
	if bytes.Equal(animation.Image[0].Pix, animation.Image[1].Pix) {
		t.Fatal("loading art is not animated")
	}
	fake := &fakeEmoji{}
	if err := Sync(context.Background(), fake, 9, guard.New()); err != nil {
		t.Fatal(err)
	}
	if mark := Mention("fix_loading"); !strings.HasPrefix(mark, "<a:fix_loading_") {
		t.Fatal(mark)
	}
	if mark := Mention("pf_x"); !strings.HasPrefix(mark, "<:pf_x_") {
		t.Fatal("static icons changed", mark)
	}
	if e := ComponentEmoji("fix_loading"); e == nil || !e.Animated {
		t.Fatal(e)
	}
}
