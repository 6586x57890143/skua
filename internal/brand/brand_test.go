package brand

import (
	"image/png"
	"io"
	"strings"
	"testing"
)

// Every colour must resolve to an icon that is actually embedded, and the
// thumbnail URL must name the file that is uploaded with it.
func TestEveryMoodHasItsFile(t *testing.T) {
	for color := range moods {
		e, f := Embed(color, "t", "d")
		if e.Thumbnail == nil || e.Thumbnail.URL != "attachment://"+f.Name {
			t.Fatalf("%#06x: thumbnail %v does not match file %q", color, e.Thumbnail, f.Name)
		}
		b, _ := io.ReadAll(f.Reader)
		if len(b) == 0 {
			t.Fatalf("%#06x: %s is empty or missing", color, f.Name)
		}
	}
	if _, f := Embed(0x123456, "", ""); !strings.Contains(f.Name, "info") {
		t.Fatalf("unknown colour got %s, want the info icon", f.Name)
	}
}

// Every embedded icon is uploaded with a reply and compiled into the
// binary, so its size is a budget, not a detail. tools/sprites writes them
// as indexed PNGs; a change that regresses to true colour fails here.
func TestEmbeddedAssetsStaySmall(t *testing.T) {
	const budget = 10 << 10
	entries, err := assets.ReadDir("assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() >= budget {
			t.Errorf("%s is %d bytes, over the %d byte budget", e.Name(), info.Size(), budget)
		}
	}
}

// Every embedded icon is 256px of 2px cells: tools/sprites draws the avatar
// on a 128 cell grid and scales it by 2, and the badge's 8px cells sit on
// the same grid. The banner is 1280 by 440 of 4px cells. A cell that is not
// one flat colour is a resampling bug.
func TestEmbeddedAssetsAreOnTheirGrid(t *testing.T) {
	entries, err := assets.ReadDir("assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		f, err := assets.Open("assets/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		w, h, k := 256, 256, 2
		if e.Name() == banner {
			w, h, k = 1280, 440, 4
		}
		if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
			t.Errorf("%s is %dx%d, want %dx%d", e.Name(), b.Dx(), b.Dy(), w, h)
			continue
		}
	cells:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if img.At(x, y) != img.At(x-x%k, y-y%k) {
					t.Errorf("%s: pixel %d,%d breaks its %dpx cell", e.Name(), x, y, k)
					break cells
				}
			}
		}
	}
}
