package pp

import (
	"fmt"
	"image"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The other screens, built like the status screen: windows with named
// tabs on the same 384 by 216, lines 17 to 19 apart, an icon beside what
// it names, values set flush right, and the hand on the member's own line.
// layout_test holds every one to its windows: nothing overlaps and nothing
// leaves the parchment.

// paneTop is where a tabbed window's first line sits.
func paneTop(r image.Rectangle) int { return r.Min.Y + pixel.TabDrop + 6 }

// midLine is the y a line of text and icon sits at in the middle of the
// window over r.
func midLine(r image.Rectangle) int { return r.Min.Y + (r.Dy()-pixel.IconCells)/2 + iconLift }

// BoardRow is one line of a board: the place, the member's class emblem,
// their name, their class and their score, and whether it's the member
// looking.
type BoardRow struct {
	Place       int
	Icon, Name  string
	Class       string // shown where the board is wide enough
	Value       string
	You         bool
	ValueIsGold bool
}

// boardPane draws one board in r: a place, an emblem, a name and a value a
// line, and on a board wide enough the class between, the top three's
// places in gold, the member's own line under the hand.
func boardPane(c *pixel.Canvas, r image.Rectangle, tab string, rows []BoardRow) {
	c.Window(r)
	c.Tab(r, tab)
	x, right0 := r.Min.X+20, r.Max.X-12
	y := paneTop(r)
	if len(rows) == 0 {
		c.Text(x, y, "nobody yet", pixel.Dim, 0)
		return
	}
	places := 2 // characters, as wide as the widest place
	for _, row := range rows {
		places = max(places, len(fmt.Sprint(row.Place)))
	}
	icon := x + places*pixel.Advance + 3
	nameX := icon + pixel.IconCells + 4
	// A wide board gives the class a column of its own, after the longest
	// name it lets through.
	classX := 0
	if r.Dx() >= 300 {
		classX = nameX + 20*pixel.Advance
	}
	for i, row := range rows {
		ly := y + i*18
		ink := pixel.Dim
		if row.Place <= 3 {
			ink = pixel.Gold
		}
		place := fmt.Sprint(row.Place)
		c.Text(x+(places-len(place))*pixel.Advance, ly, place, ink, 0)
		c.Icon(row.Icon, image.Pt(icon, ly-iconLift))
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
		end := right0 - (len(pixel.Plain(row.Value))+1)*pixel.Advance
		if classX > 0 {
			class, _ := fitPair(row.Class, "", end-classX)
			c.Fit(classX, ly, class, pixel.Dim, end-classX)
			end = classX - pixel.Advance
		}
		c.Fit(nameX, ly, row.Name, nameInk, end-nameX)
	}
}

// boardStrip is the line across the foot of the preseason's board: when the
// first season starts and what ranks until it does.
var boardStrip = image.Rect(4, 180, 380, 212)

// BoardScreen draws the levels board on the left and the season's on the
// right; or before the first season, the levels board across the screen
// and under it a line on when the first season starts.
func BoardScreen(levels, season []BoardRow, seasonTab, starts string) ([]byte, error) {
	return boardCanvas(levels, season, seasonTab, starts).Soft(screenScale)
}

func boardCanvas(levels, season []BoardRow, seasonTab, starts string) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	if seasonTab != "" {
		boardPane(c, image.Rect(4, 4, 190, 212), "levels", levels)
		boardPane(c, image.Rect(194, 4, 380, 212), seasonTab, season)
		return c
	}
	boardPane(c, image.Rect(4, 4, 380, boardStrip.Min.Y-4), "levels", levels)
	c.Window(boardStrip)
	y := midLine(boardStrip)
	end := iconText(c, "bell", 16, y, "season 1 starts", pixel.Ink)
	c.Text(end+pixel.Advance, y, starts, pixel.Gold, 0)
	right(c, 368, y, "until then, levels rank", pixel.Dim)
	return c
}

// Rung is one rung of the rookery stair as the shop shows it.
type Rung struct {
	N          int64
	Icon, Name string
	Price      int64 // pp hundredths
	Held, Next bool
}

// ShopScreen draws the shop's front: the rookery stair, a rung a line with
// its perch, its name and its price or that it's climbed, the hand on the
// next; and under it the purse and the perch the member sits on.
func ShopScreen(rungs []Rung, purse, nextPP int64, heldIcon, held string) ([]byte, error) {
	return shopCanvas(rungs, purse, nextPP, heldIcon, held).Soft(screenScale)
}

func shopCanvas(rungs []Rung, purse, nextPP int64, heldIcon, held string) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	stair := image.Rect(4, 4, 380, 148)
	c.Window(stair)
	c.Tab(stair, "the rookery stair")
	x, r := 40, 368
	for i, g := range rungs {
		y := paneTop(stair) + i*19
		n := fmt.Sprint(g.N)
		c.Text(x-len(n)*pixel.Advance, y, n, pixel.Dim, 0)
		ink := pixel.Dim
		if g.Held || g.Next {
			ink = pixel.Ink
		}
		iconText(c, g.Icon, x+4, y, g.Name, ink)
		switch {
		case g.Held:
			right(c, r, y, "climbed", pixel.Green)
		case g.Next:
			right(c, r, y, grouped(g.Price/unit)+" pp", pixel.Gold)
			c.Cursor(x-len(n)*pixel.Advance-3, y+6)
		default:
			right(c, r, y, grouped(g.Price/unit)+" pp", pixel.Dim)
		}
	}

	purseR := image.Rect(4, 152, 252, 212)
	c.Window(purseR)
	c.Tab(purseR, "purse")
	y := purseR.Min.Y + pixel.TabDrop + 3
	total := grouped(purse/unit) + " pp"
	right(c, 240, y, total, pixel.Gold)
	c.Icon("pot", image.Pt(240-len(total)*pixel.Advance-pixel.IconCells-4, y-iconLift))
	c.Text(16, y, "you hold", pixel.Dim, 0)
	c.Text(16, y+17, "the next rung", pixel.Dim, 0)
	right(c, 240, y+17, grouped(nextPP/unit)+" pp", pixel.Ink)

	you := image.Rect(256, 152, 380, 212)
	c.Window(you)
	c.Tab(you, "you sit on")
	perchLines(c, image.Rect(264, you.Min.Y+pixel.TabDrop, 372, you.Max.Y-4), heldIcon, held)
	return c
}

// perchLines sets a perch's icon and name in the middle of r, its name
// over two lines when one won't hold it, or the ground with no perch.
func perchLines(c *pixel.Canvas, r image.Rectangle, icon, name string) {
	mid := r.Min.Y + (r.Dy()-pixel.LineHeight)/2 - 1
	if icon == "" {
		c.Text(r.Min.X, mid, "the ground", pixel.Dim, 0)
		return
	}
	x := r.Min.X + pixel.IconCells + 4
	lines := pixel.Wrap(name, r.Max.X-x)
	lines = lines[:min(len(lines), 2)]
	c.Icon(icon, image.Pt(r.Min.X, r.Min.Y+(r.Dy()-pixel.IconCells)/2))
	for i, l := range lines {
		c.Text(x, mid-17*(len(lines)-1)/2+i*17, l, pixel.Ink, 0)
	}
}

// Item is one thing in the shop as its own card shows it: its picture, its
// name and its lore, what it costs and where the member stands with it.
type Item struct {
	Icon, Name, Lore string
	Kind             string // what it is in the shop, on its picture's tab
	Price, Purse     int64
	State            string // climbed, next or later
}

// ItemScreen draws one item's card: its icon large in a window of its own
// under its kind, beside it its name and lore, and under them its price
// against the purse and where the member stands.
func ItemScreen(it Item) ([]byte, error) { return itemCanvas(it).Soft(screenScale) }

func itemCanvas(it Item) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	pic := image.Rect(4, 4, 140, 148)
	c.Window(pic)
	c.Tab(pic, it.Kind)
	const scale = 5
	side := pixel.IconCells * scale
	top := pic.Min.Y + pixel.TabDrop
	c.BigIcon(it.Icon, image.Pt((pic.Min.X+pic.Max.X-side)/2, top+(pic.Max.Y-4-top-side)/2), scale)

	lore := image.Rect(144, 4, 380, 148)
	c.Window(lore)
	c.Tab(lore, it.Name)
	for i, l := range pixel.Wrap(it.Lore, 368-156) {
		c.Text(156, paneTop(lore)+i*17, l, pixel.Ink, 0)
	}

	price := image.Rect(4, 152, 252, 212)
	c.Window(price)
	c.Tab(price, "price")
	y := price.Min.Y + pixel.TabDrop + 3
	c.Icon("pot", image.Pt(16, y-iconLift))
	c.Text(16+pixel.IconCells+4, y, "it costs", pixel.Dim, 0)
	right(c, 240, y, grouped(it.Price/unit)+" pp", pixel.Gold)
	c.Text(16+pixel.IconCells+4, y+17, "you hold", pixel.Dim, 0)
	ink := pixel.Ink
	if it.Purse < it.Price && it.State != "climbed" {
		ink = pixel.Red
	}
	right(c, 240, y+17, grouped(it.Purse/unit)+" pp", ink)

	state := image.Rect(256, 152, 380, 212)
	c.Window(state)
	c.Tab(state, "for you")
	word, wink := "out of reach", pixel.Dim
	switch it.State {
	case "climbed":
		word, wink = "climbed", pixel.Green
	case "next":
		word, wink = "next up", pixel.Gold
	}
	r := image.Rect(266, state.Min.Y+pixel.TabDrop, 368, state.Max.Y-4)
	c.Text(266, r.Min.Y+(r.Dy()-pixel.LineHeight)/2-1, word, wink, 0)
	return c
}

// JournalRow is one line of a member's journal.
type JournalRow struct {
	Day, Icon, What string
	PP              int64
}

// JournalScreen draws a member's journal: what came in and went out, a
// line each, newest first, under the day still open.
func JournalScreen(name string, rows []JournalRow, open int64) ([]byte, error) {
	return journalCanvas(name, rows, open).Soft(screenScale)
}

func journalCanvas(name string, rows []JournalRow, open int64) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	r := image.Rect(4, 4, 380, 212)
	c.Window(r)
	c.Tab(r, "journal")
	x, rx := 16, 368
	c.Fit(rx-min(len(pixel.Plain(name)), 24)*pixel.Advance, tabLine(r), name, pixel.Dim, 24*pixel.Advance)
	y := paneTop(r)
	line := func(day, icon, what string, pp int64, ink uint8) {
		c.Text(x, y, day, pixel.Dim, 0)
		value := grouped(pp/unit) + " pp"
		if pp >= 0 {
			value = "+" + value
		}
		whatX := x + 11*pixel.Advance + pixel.IconCells + 4
		c.Icon(icon, image.Pt(x+11*pixel.Advance, y-iconLift))
		c.Fit(whatX, y, what, pixel.Ink, rx-(len(value)+1)*pixel.Advance-whatX)
		right(c, rx, y, value, ink)
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
	return c
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
	Median         int64 // pp hundredths
	Top            int64 // percent the top 1% hold
	Supply         int64
	Burned         int64 // percent of what the season minted
	Backstop       bool
	Rows           int64
	Head           string
}

// TreasuryScreen draws the treasury: what it holds and whether it's
// taking, what came in and went out this season, what members hold, and
// how the shop and the ledger stand.
func TreasuryScreen(t Treasury) ([]byte, error) { return treasuryCanvas(t).Soft(screenScale) }

func treasuryCanvas(t Treasury) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	row := func(r image.Rectangle, i int, icon, label, value string, ink uint8) {
		x, y := r.Min.X+12, paneTop(r)+i*19
		if icon != "" {
			iconText(c, icon, x, y, label, pixel.Dim)
		} else {
			c.Text(x+pixel.IconCells+4, y, label, pixel.Dim, 0)
		}
		right(c, r.Max.X-12, y, value, ink)
	}

	tr := image.Rect(4, 4, 190, 106)
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

	in := image.Rect(194, 4, 380, 106)
	c.Window(in)
	c.Tab(in, t.Period)
	row(in, 0, "bell", "levy in", "+"+grouped(t.Levy/unit), pixel.Green)
	row(in, 1, "laurel", "prizes", "-"+grouped(t.Prizes/unit), pixel.Red)
	row(in, 2, "envelope", "grants", "-"+grouped(t.Granted/unit), pixel.Red)
	row(in, 3, "compass", "quests paid", grouped(t.Quest/unit), pixel.Ink)

	sv := image.Rect(4, 110, 190, 212)
	c.Window(sv)
	c.Tab(sv, "savings")
	row(sv, 0, "", "active", fmt.Sprint(t.Active), pixel.Ink)
	row(sv, 1, "", "median", grouped(t.Median/unit)+" pp", pixel.Ink)
	row(sv, 2, "", "top 1% hold", fmt.Sprintf("%d%%", t.Top), pixel.Ink)
	row(sv, 3, "pot", "all purses", grouped(t.Supply/unit), pixel.Gold)

	sk := image.Rect(194, 110, 380, 212)
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
	return c
}
