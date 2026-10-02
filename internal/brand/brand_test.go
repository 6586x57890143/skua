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
