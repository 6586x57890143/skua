package brand

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// nameMethod is a module's Name, which is what /help looks its icon up by:
// "mod_" + Name().
var nameMethod = regexp.MustCompile(`func \(\*?Module\) Name\(\) string(?: \{ return "([a-z]+)" \})?`)

// Every module under internal/modules wears an icon in /help (UX.md), so a
// new one ships with its glyph in tools/sprites. The name checked is the
// one its Name method returns, read from source; a directory with no Module
// Name is not a module and is skipped.
func TestEveryModuleHasAnIcon(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "modules", "*", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range nameMethod.FindAllSubmatch(src, -1) {
			name := string(m[1])
			if name == "" {
				t.Errorf("%s: Name doesn't return a plain literal, so this test can't check its icon", f)
				continue
			}
			found++
			if _, err := emojiArt.Open("emoji/mod_" + name + ".png"); err != nil {
				t.Errorf("module %s has no icon: add its glyph to tools/sprites/icons.go and run go run ./tools/sprites", name)
			}
		}
	}
	if found == 0 {
		t.Fatal("found no modules to check")
	}
}
