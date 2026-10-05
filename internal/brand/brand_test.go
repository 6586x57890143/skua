package brand

import (
	"bytes"
	"image/png"
	"io"
	"io/fs"
	"strings"
	"testing"
)

// Every colour must resolve to an icon that is actually embedded, and the
// thumbnail URL must name the file that is uploaded with it.
func TestEveryMoodHasItsFile(t *testing.T) {
	for color := range moods {
		e, files := Embed(color, "t", "d")
		if len(files) != 1 {
			t.Fatalf("%#06x: %d files before any sync, want the icon", color, len(files))
		}
		f := files[0]
		if e.Thumbnail == nil || e.Thumbnail.URL != "attachment://"+f.Name {
			t.Fatalf("%#06x: thumbnail %v does not match file %q", color, e.Thumbnail, f.Name)
		}
		b, _ := io.ReadAll(f.Reader)
		if len(b) == 0 {
			t.Fatalf("%#06x: %s is empty or missing", color, f.Name)
		}
	}
	if _, f := Embed(0x123456, "", ""); !strings.Contains(f[0].Name, "info") {
		t.Fatalf("unknown colour got %s, want the info icon", f[0].Name)
	}
}

// Every embedded icon is compiled into the binary and either uploaded with
// a reply or as an emoji, so its size is a budget, not a detail.
// tools/sprites writes them as indexed PNGs; a change that regresses to
// true colour fails here.
func TestEmbeddedAssetsStaySmall(t *testing.T) {
	const budget = 10 << 10
	for _, a := range art(t) {
		if len(a.data) >= budget {
			t.Errorf("%s is %d bytes, over the %d byte budget", a.path, len(a.data), budget)
		}
	}
}

// Every icon is on its grid (UX.md): the attachments are 256px of 2px
// cells, the bird's 128 cells scaled by 2. Emoji are 128px: a mood is the
// bird at 1px cells, a module tile 4px cells. A cell that is not one flat
// colour is a resampling bug.
func TestEmbeddedAssetsAreOnTheirGrid(t *testing.T) {
	for _, a := range art(t) {
		img, err := png.Decode(bytes.NewReader(a.data))
		if err != nil {
			t.Fatalf("%s: %v", a.path, err)
		}
		size, k := 256, 2
		if strings.HasPrefix(a.path, "emoji/") {
			size, k = 128, 1
			if strings.HasPrefix(a.path, "emoji/mod_") {
				k = 4
			}
		}
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Errorf("%s is %dx%d, want %dx%d", a.path, b.Dx(), b.Dy(), size, size)
			continue
		}
	cells:
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if img.At(x, y) != img.At(x-x%k, y-y%k) {
					t.Errorf("%s: pixel %d,%d breaks its %dpx cell", a.path, x, y, k)
					break cells
				}
			}
		}
	}
}

type file struct {
	path string
	data []byte
}

// art is every embedded PNG, attachments and emoji.
func art(t *testing.T) []file {
	t.Helper()
	var out []file
	for _, fsys := range []fs.FS{assets, emojiArt} {
		paths, err := fs.Glob(fsys, "*/*.png")
		if err != nil || len(paths) == 0 {
			t.Fatalf("no art: %v", err)
		}
		for _, p := range paths {
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, file{p, data})
		}
	}
	return out
}
