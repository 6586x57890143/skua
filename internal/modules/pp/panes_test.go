package pp

import (
	"fmt"
	"os"
	"testing"

	"github.com/6586x57890143/skua/internal/pixel"
)

// Every pane lays out clean and draws: full boards with a long name, a
// long class and the hand, empty ones, the stair from the ground and from
// a rung, every rung's own card, a journal full and empty, and the
// treasury with its backstop on. SKUA_DUMP_PANES=dir writes them out to
// look at.
func TestPanes(t *testing.T) {
	dir := os.Getenv("SKUA_DUMP_PANES")
	save := func(name string, c *pixel.Canvas) {
		t.Helper()
		checkLayout(t, name, c)
		b, err := c.Soft(screenScale)
		decodes(t, b, err)
		if dir != "" {
			_ = os.WriteFile(dir+"/"+name, b, 0o644)
		}
	}
	names := []string{"heavywinds", "steve", "tester", "a very long display name indeed", "moth", "gloamwick", "reed", "ash", "tallowmere", "brine"}
	icons := []string{"sword", "bow", "orb", "pouch", "lute", "beads", "shield", "lantern", "sword", "orb"}
	var rows, srows []BoardRow
	for i := range 10 {
		cl := Classes[i%len(Classes)].Name()
		rows = append(rows, BoardRow{Place: i + 1, Icon: icons[i], Name: names[i], Class: cl, Value: fmt.Sprintf("lv %d", 92-i*10)})
		srows = append(srows, BoardRow{Place: i + 1, Icon: icons[i], Name: names[i], Class: cl, Value: grouped(int64(99000 - i*731)), ValueIsGold: true})
	}
	rows[9] = BoardRow{Place: 137, Icon: "lantern", Name: "you", Class: "albatross wanderer", Value: "lv 3", You: true}
	srows[9] = BoardRow{Place: 137, Icon: "lantern", Name: "you", Class: "albatross wanderer", Value: "12", You: true, ValueIsGold: true}
	save("board_pre.jpg", boardCanvas(append(rows[:preseasonRows-1:preseasonRows-1], rows[9]), nil, "", "2 nov"))
	save("board.jpg", boardCanvas(rows, srows, "season 1", ""))
	save("board_empty.jpg", boardCanvas(nil, nil, "season 1", ""))
	save("board_pre_empty.jpg", boardCanvas(nil, nil, "", "2 nov"))

	pd := Default.PotDay(Rules)
	var rungs []Rung
	for n := int64(1); n <= shopRungs; n++ {
		name, icon := perch(n)
		rungs = append(rungs, Rung{N: n, Icon: icon, Name: name, Price: rungPrice(pd, n-1), Held: n <= 2, Next: n == 3})
	}
	save("shop.jpg", shopCanvas(rungs, 512345*unit/100, rungPrice(pd, 2), "perch_shale", "shale perch"))
	save("shop_bell.jpg", shopCanvas(rungs, 0, rungPrice(pd, 5), "perch_bell", "drowned-bell perch"))
	save("shop_ground.jpg", shopCanvas(rungs, 0, rungPrice(pd, 0), "", ""))
	high := rungs[:0:0]
	for n := int64(19); n < 19+shopRungs; n++ {
		name, icon := perch(n)
		high = append(high, Rung{N: n, Icon: icon, Name: name, Price: rungPrice(pd, n-1), Held: n < 20, Next: n == 20})
	}
	save("shop_high.jpg", shopCanvas(high, 9999999999*unit, rungPrice(pd, 19), "perch_nameless", "a perch nobody built"))
	for n := int64(1); n <= int64(len(perchLore)); n++ {
		name, icon := perch(n)
		state := []string{"climbed", "next", ""}[min(n-1, 2)]
		save(fmt.Sprintf("item_%d.jpg", n), itemCanvas(Item{Icon: icon, Name: name, Lore: perchLore[n-1], Kind: fmt.Sprintf("rung %d", n),
			Price: rungPrice(pd, n-1), Purse: 2000 * unit, State: state}))
	}

	save("journal.jpg", journalCanvas("heavywinds", []JournalRow{
		{"2026-10-16", "pot", "a day's earning", 612 * unit}, {"2026-10-16", "compass", "great quest", 392 * unit},
		{"2026-10-15", "perch_bell", "drowned-bell perch", -109568 * unit}, {"2026-10-13", "laurel", "a weekly prize", 1500 * unit},
		{"2026-10-12", "envelope", "a grant", 428 * unit}, {"2026-10-12", "shield", "a new path", -250 * unit},
		{"2026-10-11", "lantern", "purse carried over", 1234567 * unit}, {"2026-10-11", "perch_tallow", "a perch nobody built", -9999999 * unit},
	}, 213*unit))
	save("journal_empty.jpg", journalCanvas("steve", nil, 0))

	save("treasury.jpg", treasuryCanvas(Treasury{Balance: 81234 * unit, Taking: true, Grants: "open", GrantRoom: 20300 * unit, Period: "preseason",
		Levy: 40210 * unit, Prizes: 16000 * unit, Granted: 2140 * unit, Quest: 88400 * unit, Active: 231, Median: 1455 * unit, Top: 11,
		Supply: 1830400 * unit, Burned: 7, Rows: 12044, Head: "3fa9c1d2", Backstop: true}))
}

// Every rung's lore fits its card.
func TestPerchLore(t *testing.T) {
	if len(perchLore) != len(perchNames) {
		t.Fatalf("%d lores for %d perches", len(perchLore), len(perchNames))
	}
	for i, l := range perchLore {
		if n := len(pixel.Wrap(l, 368-156)); n > 6 {
			t.Errorf("%s's lore runs to %d lines", perchNames[i], n)
		}
	}
}
