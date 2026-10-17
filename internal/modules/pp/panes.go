package pp

import (
	"fmt"
	"image"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The other screens, built like the status screen: windows with named
// tabs on the same 384 by 216, lines 18 or 19 apart, an icon beside what
// it names, values set flush right, and the hand on the member's own line.

// paneTop is where a tabbed window's first line sits.
func paneTop(r image.Rectangle) int { return r.Min.Y + pixel.TabDrop + 6 }

// BoardRow is one line of a board: the place, the member's class emblem,
// their name and their score, and whether it's the member looking.
type BoardRow struct {
	Place       int
	Icon, Name  string
	Value       string
	You         bool
	ValueIsGold bool
}

// boardPane draws one board in r: a place, an emblem, a name and a value a
// line, the top three's places in gold, the member's own line under the
// hand.
func boardPane(c *pixel.Canvas, r image.Rectangle, tab string, rows []BoardRow) {
	c.Window(r)
	c.Tab(r, tab)
	x, right0 := r.Min.X+18, r.Max.X-8
	y := paneTop(r)
	if len(rows) == 0 {
		c.Text(x, y, "nobody yet", pixel.Dim, 0)
		return
	}
	for i, row := range rows {
		ly := y + i*18
		ink := pixel.Dim
		if row.Place <= 3 {
			ink = pixel.Gold
		}
		place := fmt.Sprint(row.Place)
		c.Text(x+2*pixel.Advance-len(place)*pixel.Advance, ly, place, ink, 0)
		c.Icon(row.Icon, image.Pt(x+2*pixel.Advance+3, ly-iconLift))
		nameX := x + 2*pixel.Advance + 3 + pixel.IconCells + 4
		valueInk := pixel.Ink
		if row.ValueIsGold {
			valueInk = pixel.Gold
		}
		right(c, right0, ly, row.Value, valueInk)
		nameInk := pixel.Ink
		if row.You {
			nameInk = pixel.Gold
			c.Cursor(x-3, ly+6)
		}
		c.Fit(nameX, ly, row.Name, nameInk, right0-len(pixel.Plain(row.Value))*pixel.Advance-6-nameX)
	}
}

// BoardScreen draws the levels board on the left and the season's on the
// right, or before the first season, when it starts.
func BoardScreen(levels, season []BoardRow, seasonTab, starts, you string) ([]byte, error) {
	c := pixel.New(screenW, screenH)
	boardPane(c, image.Rect(4, 4, 190, 212), "levels", levels)
	if seasonTab != "" {
		boardPane(c, image.Rect(194, 4, 380, 212), seasonTab, season)
		return c.Soft(screenScale)
	}
	r := image.Rect(194, 4, 380, 212)
	c.Window(r)
	c.Tab(r, "preseason")
	x, y := r.Min.X+12, paneTop(r)
	iconText(c, "bell", x, y, "the first season", pixel.Ink)
	c.Text(x+pixel.IconCells+4, y+19, "starts "+starts, pixel.Gold, 0)
	iconText(c, "laurel", x, y+57, "until then the", pixel.Ink)
	c.Text(x+pixel.IconCells+4, y+76, "levels are the", pixel.Ink, 0)
	c.Text(x+pixel.IconCells+4, y+95, "board", pixel.Ink, 0)
	if you != "" {
		iconText(c, "pot", x, y+152, you, pixel.Gold)
	}
	return c.Soft(screenScale)
}

// Rung is one rung of the rookery stair as the shop shows it.
type Rung struct {
	N          int64
	Icon, Name string
	PotDays    int64
	Held, Next bool
}

// ShopScreen draws the rookery stair, a rung a line with its perch, its
// name and its price or that it's held, the hand on the next; and under
// it the purse and what the next rung costs.
func ShopScreen(rungs []Rung, purse, nextPP int64, heldIcon, held string) ([]byte, error) {
	c := pixel.New(screenW, screenH)
	stair := image.Rect(4, 4, 380, 152)
	c.Window(stair)
	c.Tab(stair, "the rookery stair")
	x, r := 40, 368
	for i, g := range rungs {
		y := paneTop(stair) + i*20
		c.Text(x-2*pixel.Advance, y, fmt.Sprint(g.N), pixel.Dim, 0)
		ink := pixel.Dim
		if g.Held || g.Next {
			ink = pixel.Ink
		}
		iconText(c, g.Icon, x+4, y, g.Name, ink)
		switch {
		case g.Held:
			right(c, r, y, "held", pixel.Green)
		case g.Next:
			right(c, r, y, fmt.Sprintf("%d pot days", g.PotDays), pixel.Gold)
			c.Cursor(x-2*pixel.Advance-3, y+6)
		default:
			right(c, r, y, fmt.Sprintf("%d pot days", g.PotDays), pixel.Dim)
		}
	}

	purseR := image.Rect(4, 156, 252, 212)
	c.Window(purseR)
	c.Tab(purseR, "purse")
	y := paneTop(purseR)
	total := grouped(purse / unit)
	right(c, 240, y, total+" pp", pixel.Gold)
	c.Icon("pot", image.Pt(240-len(total+" pp")*pixel.Advance-pixel.IconCells-4, y-iconLift))
	c.Text(16, y, "you hold", pixel.Dim, 0)
	c.Text(16, y+17, "the next rung", pixel.Dim, 0)
	right(c, 240, y+17, grouped(nextPP/unit)+" pp", pixel.Ink)

	you := image.Rect(256, 156, 380, 212)
	c.Window(you)
	c.Tab(you, "you sit on")
	y = paneTop(you) + 8
	if heldIcon == "" {
		c.Text(268, y, "the ground", pixel.Dim, 0)
	} else {
		c.Icon(heldIcon, image.Pt(268, y-iconLift))
		c.Fit(268+pixel.IconCells+4, y, held, pixel.Ink, 100-pixel.IconCells-4)
	}
	return c.Soft(screenScale)
}

// JournalRow is one line of a member's journal.
type JournalRow struct {
	Day, Icon, What string
	PP              int64
}

// JournalScreen draws a member's journal: what came in and went out, a
// line each, newest first, and under it how long the server's ledger is.
func JournalScreen(name string, rows []JournalRow, open int64, ledgerRows int64) ([]byte, error) {
	c := pixel.New(screenW, screenH)
	r := image.Rect(4, 4, 380, 212)
	c.Window(r)
	c.Tab(r, "journal")
	right(c, 368, 10, name, pixel.Dim)
	x, rx := 16, 368
	y := paneTop(r)
	line := func(day, icon, what string, pp int64, ink uint8) {
		c.Text(x, y, day, pixel.Dim, 0)
		iconText(c, icon, x+11*pixel.Advance, y, what, pixel.Ink)
		sign := "+"
		if pp < 0 {
			sign = "-"
		}
		right(c, rx, y, sign+grouped(abs(pp)/unit)+" pp", ink)
		y += 18
	}
	if open != 0 {
		line("today", "pot", "earned so far", open, pixel.Dim)
	}
	for _, row := range rows {
		ink := pixel.Green
		if row.PP < 0 {
			ink = pixel.Red
		}
		line(row.Day, row.Icon, row.What, row.PP, ink)
	}
	if open == 0 && len(rows) == 0 {
		c.Text(x, y, "nothing yet: a day goes in when it turns", pixel.Dim, 0)
	}
	iconText(c, "quill", x, 192, fmt.Sprintf("in the server's ledger, %s lines long", grouped(ledgerRows)), pixel.Dim)
	return c.Soft(screenScale)
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// Treasury is the server's money as admins see it.
type Treasury struct {
	Balance        int64
	Taking         bool
	Grants         string // open or when it opens
	GrantRoom      int64
	Period         string // the season, or preseason
	Levy, Prizes   int64  // this period: levy in, prizes out
	Granted, Quest int64  // grants out, quests paid as labour
	Active         int
	Median         float64 // pot days
	Top            int64   // percent the top 1% hold
	Supply         int64
	Burned         int64 // percent of what the season minted
	Backstop       bool
	Rows           int64
	Head           string
}

// TreasuryScreen draws the treasury: what it holds and whether it's
// taking, what came in and went out this season, what members hold, and
// how the shop and the ledger stand.
func TreasuryScreen(t Treasury) ([]byte, error) {
	c := pixel.New(screenW, screenH)
	row := func(r image.Rectangle, i int, icon, label, value string, ink uint8) {
		x, y := r.Min.X+12, paneTop(r)+i*19
		if icon != "" {
			iconText(c, icon, x, y, label, pixel.Dim)
		} else {
			c.Text(x, y, label, pixel.Dim, 0)
		}
		right(c, r.Max.X-10, y, value, ink)
	}

	tr := image.Rect(4, 4, 190, 104)
	c.Window(tr)
	c.Tab(tr, "treasury")
	row(tr, 0, "pot", "holds", grouped(t.Balance/unit)+" pp", pixel.Gold)
	taking, ink := "full", pixel.Dim
	if t.Taking {
		taking, ink = "taking", pixel.Green
	}
	row(tr, 1, "bell", fmt.Sprintf("levy %d%%", Rules.Levy/100), taking, ink)
	row(tr, 2, "envelope", "grants", t.Grants, pixel.Ink)
	row(tr, 3, "", "this week", grouped(t.GrantRoom/unit)+" pp", pixel.Ink)

	in := image.Rect(194, 4, 380, 104)
	c.Window(in)
	c.Tab(in, t.Period)
	row(in, 0, "bell", "levy in", "+"+grouped(t.Levy/unit), pixel.Green)
	row(in, 1, "laurel", "prizes", "-"+grouped(t.Prizes/unit), pixel.Red)
	row(in, 2, "envelope", "grants", "-"+grouped(t.Granted/unit), pixel.Red)
	row(in, 3, "compass", "quests paid", grouped(t.Quest/unit), pixel.Ink)

	sv := image.Rect(4, 108, 190, 212)
	c.Window(sv)
	c.Tab(sv, "savings")
	row(sv, 0, "", "active", fmt.Sprint(t.Active), pixel.Ink)
	row(sv, 1, "", "median", fmt.Sprintf("%.1f pot days", t.Median), pixel.Ink)
	row(sv, 2, "", "top 1% hold", fmt.Sprintf("%d%%", t.Top), pixel.Ink)
	row(sv, 3, "pot", "all purses", grouped(t.Supply/unit), pixel.Gold)

	sk := image.Rect(194, 108, 380, 212)
	c.Window(sk)
	c.Tab(sk, "sinks")
	row(sk, 0, "perch", "burned", fmt.Sprintf("%d%%", t.Burned), pixel.Ink)
	back, ink := "off", pixel.Green
	if t.Backstop {
		back, ink = "on", pixel.Red
	}
	row(sk, 1, "", "backstop", back, ink)
	row(sk, 2, "quill", "ledger", grouped(t.Rows), pixel.Ink)
	row(sk, 3, "", "head", t.Head, pixel.Dim)
	return c.Soft(screenScale)
}
