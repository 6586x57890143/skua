package pp

import (
	"fmt"
	"os"
	"testing"
)

// Every pane draws: full boards with a long name and the hand, empty ones,
// the stair from the ground and from a rung, a journal full and empty, and
// the treasury with its backstop on. SKUA_DUMP_PANES=dir writes them out to
// look at.
func TestPanes(t *testing.T) {
	dir := os.Getenv("SKUA_DUMP_PANES")
	save := func(name string, b []byte, err error) {
		t.Helper()
		decodes(t, b, err)
		if dir != "" {
			_ = os.WriteFile(dir+"/"+name, b, 0o644)
		}
	}
	names := []string{"heavywinds", "steve", "tester", "a very long display name indeed", "moth", "gloamwick", "reed", "ash", "tallowmere", "brine"}
	icons := []string{"sword", "bow", "orb", "pouch", "lute", "beads", "shield", "lantern", "sword", "orb"}
	var rows, srows []BoardRow
	for i := range 10 {
		rows = append(rows, BoardRow{Place: i + 1, Icon: icons[i], Name: names[i], Value: fmt.Sprintf("lv %d", 92-i*10)})
		srows = append(srows, BoardRow{Place: i + 1, Icon: icons[i], Name: names[i], Value: grouped(int64(9000-i*731)) + " pp", ValueIsGold: true})
	}
	rows[9] = BoardRow{Place: 37, Icon: "lantern", Name: "you", Value: "lv 3", You: true}
	b, err := BoardScreen(rows, nil, "", "2 nov", "you're 37th, level 3")
	save("board_pre.jpg", b, err)
	b, err = BoardScreen(rows, srows, "season 1", "", "")
	save("board.jpg", b, err)
	b, err = BoardScreen(nil, nil, "season 1", "", "")
	save("board_empty.jpg", b, err)

	rungs := []Rung{
		{N: 1, Icon: "perch_tallow", Name: "tallow perch", PotDays: 2, Held: true},
		{N: 2, Icon: "perch_shale", Name: "shale perch", PotDays: 4, Held: true},
		{N: 3, Icon: "perch_chitin", Name: "chitin perch", PotDays: 8, Next: true},
		{N: 4, Icon: "perch_glass", Name: "pale-glass perch", PotDays: 16},
		{N: 5, Icon: "perch_bell", Name: "drowned-bell perch", PotDays: 32},
		{N: 6, Icon: "perch_nameless", Name: "a perch nobody built", PotDays: 64},
	}
	b, err = ShopScreen(rungs, 512345*unit/100, 3424*unit, "perch_shale", "shale perch")
	save("shop.jpg", b, err)
	b, err = ShopScreen(rungs, 0, 856*unit, "", "")
	save("shop_ground.jpg", b, err)

	b, err = JournalScreen("heavywinds", []JournalRow{
		{"2026-10-16", "pot", "a day's earning", 612 * unit}, {"2026-10-16", "compass", "great quest", 392 * unit},
		{"2026-10-15", "perch_tallow", "tallow perch", -856 * unit}, {"2026-10-13", "laurel", "a weekly prize", 1500 * unit},
		{"2026-10-12", "envelope", "a grant", 428 * unit}, {"2026-10-12", "shield", "a new path", -250 * unit},
	}, 213*unit, 12044)
	save("journal.jpg", b, err)
	b, err = JournalScreen("steve", nil, 0, 0)
	save("journal_empty.jpg", b, err)

	b, err = TreasuryScreen(Treasury{Balance: 81234 * unit, Taking: true, Grants: "open", GrantRoom: 20300 * unit, Period: "preseason",
		Levy: 40210 * unit, Prizes: 16000 * unit, Granted: 2140 * unit, Quest: 88400 * unit, Active: 231, Median: 3.4, Top: 11,
		Supply: 1830400 * unit, Burned: 7, Rows: 12044, Head: "3fa9c1d2", Backstop: true})
	save("treasury.jpg", b, err)
}
