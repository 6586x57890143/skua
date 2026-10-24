package pp

import "testing"

// Past the great nothing every rung has a name of its own for a cycle of
// the names, a shade of its own for a cycle of the shades, and its count.
func TestParagon(t *testing.T) {
	top := int64(len(perches))
	seen := map[string]bool{}
	for n := top; n <= top+int64(len(paragonNames)); n++ {
		p := perchOf(n)
		if seen[p.name] {
			t.Errorf("rung %d repeats %q", n, p.name)
		}
		seen[p.name] = true
		if p.what == "" || p.story == "" || p.effect == "" || p.icon == "" {
			t.Errorf("rung %d is missing its words", n)
		}
	}
	if perchOf(top+1).name != "hushed perch" || perchOf(top+1+int64(len(paragonNames))).name != "hushed perch" {
		t.Error("the names don't start or come round where they should")
	}
	if l := lookOf(top + 1); l.Count != top+1 || l.Tint == 0 || !l.Frame {
		t.Errorf("past the nothing: %+v", l)
	}
	if lookOf(top+1).Tint == lookOf(top+2).Tint {
		t.Error("two rungs in a row share a shade")
	}
	if l := lookOf(top); l.Count != 0 || l.Tint != 0 {
		t.Errorf("the great nothing itself: %+v", l)
	}
	if perchOf(0) != perches[0] {
		t.Error("rung 0 doesn't clamp to the first perch")
	}
}
