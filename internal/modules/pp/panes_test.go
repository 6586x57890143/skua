package pp

import (
	"fmt"
	"os"
	"testing"
	"time"

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
	// Every rung's card, its name as it will look on the longest name a
	// member can have, and the counted rungs past the last perch.
	for n := int64(1); n <= int64(len(perches))+3; n++ {
		p, look := perchOf(n), lookOf(n)
		save(fmt.Sprintf("item_%d.jpg", n), itemCanvas(Item{Icon: p.icon, Kind: fmt.Sprintf("rung %d", n), Name: p.name,
			What: p.what, Story: p.story, Effect: p.effect, Price: rungPrice(pd, n-1), Short: n > 3, You: names[n%3*3], Look: &look}))
	}
	// A livery's card, its name on Discord's dark and light themes.
	for _, d := range []dye{dyes[0], dyes[len(dyes)-1]} {
		save("livery_"+d.key+".jpg", itemCanvas(Item{Kind: "livery", Name: d.name, What: d.what, Story: d.story, Effect: d.effect, Price: 407 * unit,
			You: names[d.color%3*3], Dye: d.color, Garment: garments[d.key]}))
	}
	// The notices: the board with nothing held, a chirograph half signed
	// and a round robin under way.
	board := []Posted{{Ring: true, By: "steve", Places: 2}, {By: "tester", Places: 1}, {Ring: true, By: names[3], Places: 1}}
	save("notices_board.jpg", noticesCanvas("heavywinds", nil, board))
	save("notices_empty.jpg", noticesCanvas("heavywinds", nil, nil))
	sig := func(i int, min int64) NoticeSigner {
		return NoticeSigner{Name: names[i%len(names)], Portrait: Portrait(Classes[i%len(Classes)]), Minutes: min}
	}
	save("notices_pair.jpg", noticesCanvas("heavywinds", &Notice{Signers: []NoticeSigner{sig(0, 0)}, Places: 2, Goal: 30, Pays: "60 pp each"}, board))
	save("notices_ring.jpg", noticesCanvas("heavywinds", &Notice{Ring: true, Signers: []NoticeSigner{sig(1, 17), sig(3, 17), sig(6, 12)}, Places: 4, Together: 17, Goal: 30, Pays: "90 / 120 pp each"}, nil))
	var named []BoardRow
	for n := range int64(10) {
		named = append(named, BoardRow{Place: int(n) + 1, Icon: icons[n], Name: names[n], Rung: n, Class: Classes[n%8].Name(), Value: "lv 9", You: n == 4})
	}
	save("board_looks.jpg", boardCanvas(named[:preseasonRows], nil, "", "2 nov"))
	save("board_looks_season.jpg", boardCanvas(named, named, "season 1", ""))

	save("buffs.jpg", buffCanvas("heavywinds", []BuffInfo{
		{Icon: "crest", Name: "crest", Effect: "+10%", How: "wear this server's tag as yours", On: true},
		{Icon: "banner", Name: "banner", Effect: "+10%", How: "put .gg/melted in your custom status"},
		{Icon: "plume", Name: "plume", Effect: "+5%", How: "boost the server; it lasts while you boost", On: true, Left: 50 * time.Hour},
		{Icon: "hearth", Name: "hearth", Effect: "+5 to 15%", How: "lit for all when the server beats its usual day", Off: "unlit"},
	}))

	save("journal.jpg", journalCanvas("heavywinds", []JournalRow{
		{"2026-10-16", "pot", "a day's earning", 612 * unit}, {"2026-10-16", "compass", "great quest", 392 * unit},
		{"2026-10-15", "perch_bell", "drowned-bell perch", -109568 * unit}, {"2026-10-13", "laurel", "a weekly prize", 1500 * unit},
		{"2026-10-12", "envelope", "a grant", 428 * unit}, {"2026-10-12", "shield", "a new path", -250 * unit},
		{"2026-10-11", "lantern", "purse carried over", 1234567 * unit}, {"2026-10-11", "perch_tallow", "a perch nobody built", -9999999 * unit},
	}, 213*unit))
	save("journal_empty.jpg", journalCanvas("steve", nil, 0))

	save("treasury.jpg", treasuryCanvas(Treasury{Balance: 81234 * unit, Taking: true, Fund: 11250 * unit, Period: "preseason",
		Levy: 40210 * unit, Prizes: 16000 * unit, Granted: 2140 * unit, Quest: 88400 * unit, Active: 231, Median: 1455 * unit, Top: 11,
		Supply: 1830400 * unit, Burned: 7, Rows: 12044, Head: "3fa9c1d2", Backstop: true}))
}

// Every rung has its words, and each says what it does.
func TestPerches(t *testing.T) {
	for _, p := range perches {
		if p.what == "" || p.story == "" || p.effect == "" || p.icon == "" {
			t.Errorf("%s is missing its words", p.name)
		}
	}
	if perchOf(0) != perches[0] || perchOf(int64(len(perches))) != perches[len(perches)-1] {
		t.Error("rungs off the stair don't clamp to it")
	}
}
