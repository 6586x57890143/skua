package brand

import (
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
