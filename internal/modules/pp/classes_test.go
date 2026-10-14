package pp

import (
	"strings"
	"testing"
	"unicode"
)

func TestClasses(t *testing.T) {
	if Classes[0].Key != "wanderer" {
		t.Fatal("everyone starts a wanderer, so it comes first")
	}
	keys, birds := map[string]bool{}, map[string]bool{}
	for _, c := range Classes {
		if keys[c.Key] || birds[c.Bird] {
			t.Errorf("%s: a class or bird is used twice", c.Name())
		}
		keys[c.Key], birds[c.Bird] = true, true
		for _, s := range append([]string{c.Key, c.Bird}, c.Titles[:]...) {
			if s == "" || strings.IndexFunc(s, unicode.IsUpper) >= 0 {
				t.Errorf("%s: %q should be lowercase and there", c.Name(), s)
			}
			// The status screen gives a name and a title one line each.
			if len(c.Name()) > 20 || len(s) > 16 {
				t.Errorf("%s: %q is too long for the status screen", c.Name(), s)
			}
		}
	}
}

func TestClassTitles(t *testing.T) {
	thief := ClassOf("thief")
	if thief.Name() != "skua thief" {
		t.Errorf("the thief reads %q", thief.Name())
	}
	for l, want := range map[int]string{0: "footpad", 4: "footpad", 5: "cutpurse", 29: "sharper", 50: "master thief", 99: "master thief"} {
		if got := thief.Title(l); got != want {
			t.Errorf("a level %d thief is a %s; want %s", l, got, want)
		}
	}
	if ClassOf("necromancer").Key != "wanderer" {
		t.Error("an unknown class isn't the wanderer")
	}
}
