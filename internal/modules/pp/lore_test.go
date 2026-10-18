package pp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// Every class has a character, its story on at most two pages that stay
// inside the card, and every page set where it can be read.
func TestLoreCards(t *testing.T) {
	for _, cl := range Classes {
		l, ok := ClassLore[cl.Key]
		if !ok || l.Epithet == "" || len(l.Story) == 0 || l.Quote == "" {
			t.Errorf("%s has no character", cl.Key)
			continue
		}
		pages := lorePages(l)
		if len(pages) > 2 {
			t.Errorf("%s's story runs to %d pages", cl.Key, len(pages))
		}
		for p, lines := range pages {
			if h := loreHeight(lines); h > loreText.Dy() {
				t.Errorf("%s page %d runs %dpx, the card holds %d", cl.Key, p+1, h, loreText.Dy())
			}
			checkLayout(t, cl.Key, loreCanvas(cl, p))
		}
		if last := pages[len(pages)-1]; !last[len(last)-1].quote {
			t.Errorf("%s doesn't end on the quote", cl.Key)
		}
	}
}

// A lore press pages through the story and the classes, and refuses a
// class or page that isn't there.
func TestLorePress(t *testing.T) {
	m := testModule(&fakeDB{})
	e, sent := coretest.Button(t, loreID(3, 1), nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	got := js((*sent)[0])
	for _, want := range []string{"raven sorcerer", "from the top", loreID(3, 0), loreID(2, 0), loreID(4, 0), "pp:pick:sorcerer", "archmagus"} {
		if !strings.Contains(got, want) {
			t.Errorf("the sorcerer's second page has no %q: %s", want, got)
		}
	}
	for _, bad := range []string{"pp:lore:8:0", "pp:lore:0:9", "pp:lore:-1:0", "pp:lore:x", "pp:lore:0:0:0"} {
		if err := m.press(context.Background(), firstOf(coretest.Button(t, bad, nil))); !errors.Is(err, errNotAClass) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// A member's own class can't be walked again.
	if err := m.press(context.Background(), firstOf(pick(t, "monk"))); err != nil {
		t.Fatal(err)
	}
	e, sent = coretest.Button(t, "pp:class", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := js((*sent)[0]); !strings.Contains(got, "heron monk") || !strings.Contains(got, "your path") || !strings.Contains(got, `"label":"lore"`) {
		t.Errorf("the lore button for a monk: %s", got)
	}
}

// Every class's card goes out with a picture description Discord takes:
// past altLimit it refuses the whole message.
func TestLoreAltFits(t *testing.T) {
	for i, cl := range Classes {
		msg, err := loreMessage(i, 0, "", 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if d := msg.Files[0].Description; len(d) > altLimit || !strings.HasSuffix(d, ".") && !strings.HasSuffix(d, "\"") {
			t.Errorf("%s's description runs %d: %q", cl.Key, len(d), d)
		}
	}
	if got := clip("one. two. three", 9); got != "one. two." {
		t.Errorf("clip: %q", got)
	}
	if got := clip("abcdefgh", 4); got != "abcd" {
		t.Errorf("clip with no sentence: %q", got)
	}
}
