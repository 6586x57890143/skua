package pp

import (
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/6586x57890143/skua/internal/core"
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
// their name in their look, their class and their score, and whether it's
// the member looking.
type BoardRow struct {
	Place       int
	Icon, Name  string
	Rung        int64
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
		label(c, x, y, "nobody yet")
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
		at := right(c, right0, ly, row.Value, valueInk)
		if row.You {
			c.Cursor(x-3, ly+6)
		}
		end := at - pixel.Advance
		if classX > 0 {
			class, _ := fitPair(row.Class, pixel.Light, "", pixel.Light, end-classX)
			c.FitIn(pixel.Light, classX, ly, class, pixel.Dim, end-classX)
			end = classX - pixel.Advance
		}
		look := lookOf(row.Rung)
		if row.You && row.Rung == 0 {
			look.Ink = pixel.Gold
		}
		setName(c, look, nameX, ly, row.Name, end-nameX, image.Rect(nameX-3, ly-2, end+2, ly+15))
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
	c.Print(pixel.Bold, end+pixel.Advance, y, starts, pixel.Gold)
	rightIn(c, pixel.Light, 368, y, "until then, levels rank", pixel.Dim)
	return c
}

// Rung is one rung of the rookery stair as the shop's front shows it.
type Rung struct {
	N          int64
	Icon, Name string
	Price      int64 // pp hundredths
	Held, Next bool
}

// ShopScreen draws the shop's front: the rookery stair, a rung a line with
// its perch and its name set in the look it buys, and its price or that
// it's climbed, the hand on the next; and under it the purse and the
// perch the member sits on.
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
		c.Text(x-pixel.Regular.Width(n), y, n, pixel.Dim, 0)
		c.Icon(g.Icon, image.Pt(x+4, y-iconLift))
		var at int
		switch {
		case g.Held:
			at = right(c, r, y, "climbed", pixel.Green)
		case g.Next:
			at = right(c, r, y, grouped(g.Price/unit)+" pp", pixel.Gold)
			c.Cursor(x-pixel.Regular.Width(n)-3, y+6)
		default:
			at = right(c, r, y, grouped(g.Price/unit)+" pp", pixel.Dim)
		}
		// Each rung's name is set in the look it buys, its perch already
		// beside it, every plate as long as the longest price allows.
		look := lookOf(g.N)
		look.Perch, look.Count = "", 0
		nameX := x + 4 + pixel.IconCells + 4
		end := min(at, r-pixel.Regular.Width("999,999 pp")) - pixel.Advance
		setName(c, look, nameX, y, g.Name, end-nameX, image.Rect(nameX-3, y-2, end+2, y+15))
	}

	purseR := image.Rect(4, 152, 252, 212)
	c.Window(purseR)
	c.Tab(purseR, "purse")
	y := purseR.Min.Y + pixel.TabDrop + 3
	total := grouped(purse/unit) + " pp"
	at := right(c, 240, y, total, pixel.Gold)
	c.Icon("pot", image.Pt(at-pixel.IconCells-4, y-iconLift))
	label(c, 16, y, "you hold")
	label(c, 16, y+17, "the next rung")
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
		label(c, r.Min.X, mid, "the ground")
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

// Item is one thing in the shop as its own card shows it, the way an old
// game's item screen does: its picture under its kind, its name, a plain
// line on what it is, the story it tells and what it does in the game,
// what it costs, and how the member's name will look with it.
type Item struct {
	Icon, Kind, Name    string
	What, Story, Effect string
	Price               int64
	Short               bool // the member can't pay for it yet
	Climbed             bool // the member has it already
	You                 string
	Look                *Look // how the member's name looks with it, or nil
}

// ItemScreen draws an item's card: on the left its picture, the member's
// name as it will look and the price; on the right its name and its text.
func ItemScreen(it Item) ([]byte, error) { return itemCanvas(it).Soft(screenScale) }

func itemCanvas(it Item) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	pic := image.Rect(4, 4, 112, 112)
	c.Window(pic)
	c.Tab(pic, it.Kind)
	const scale = 4
	side := pixel.IconCells * scale
	top := pic.Min.Y + pixel.TabDrop
	c.BigIcon(it.Icon, image.Pt((pic.Min.X+pic.Max.X-side)/2, top+(pic.Max.Y-4-top-side)/2), scale)

	if it.Look != nil {
		you := image.Rect(4, 116, 112, 164)
		c.Window(you)
		c.Tab(you, "you")
		y := you.Min.Y + pixel.TabDrop + 6
		setName(c, *it.Look, 14, y, it.You, 104-14, image.Rect(10, y-3, 106, y+15))
	}

	price := image.Rect(4, 168, 112, 212)
	c.Window(price)
	c.Tab(price, "price")
	y := price.Min.Y + pixel.TabDrop + 5
	ink := pixel.Gold
	if it.Short {
		ink = pixel.Red
	}
	c.Icon("pot", image.Pt(14, y-iconLift))
	if it.Climbed {
		c.Text(14+pixel.IconCells+4, y, "climbed", pixel.Green, 0)
	} else {
		c.Text(14+pixel.IconCells+4, y, grouped(it.Price/unit)+" pp", ink, 0)
	}

	text := image.Rect(116, 4, 380, 212)
	c.Window(text)
	x, width := 128, 368-128
	y = paneTop(text)
	// A name too long for the tab heads the text instead, in bold, and
	// says enough on its own that the plain line gives way to it.
	what := it.What
	if pixel.Bold.Width(it.Name) <= width-pixel.IconCells {
		c.Tab(text, it.Name)
	} else {
		c.Tab(text, "item")
		what = it.Name
	}
	face, ink := pixel.Regular, uint8(pixel.Ink)
	if what == it.Name {
		face, ink = pixel.Bold, pixel.Gold
	}
	for _, l := range face.Wrap(what, width) {
		c.Print(face, x, y, l, ink)
		y += 15
	}
	y += 4
	for _, l := range pixel.Serif.Wrap(it.Story, width) {
		c.Print(pixel.Serif, x, y, l, pixel.Ink)
		y += 14
	}
	y += 5
	// The effect leads with its word in bold gold; the wrap holds a place
	// for it as wide as the bold word and a space.
	const lead = "effect"
	hold := strings.Repeat("x", pixel.Bold.Width(lead)/pixel.Advance+1)
	for i, l := range pixel.Wrap(hold+" "+it.Effect, width) {
		at := x
		if i == 0 {
			c.Print(pixel.Bold, x, y, lead, pixel.Gold)
			at, l = x+pixel.Regular.Width(hold+" "), l[len(hold)+1:]
		}
		c.Text(at, y, l, pixel.Ink, 0)
		y += 14
	}
	return c
}

// BuffInfo is one buff as the buffs screen explains it: what it is, what
// it adds, how a member comes by it, and whether they have it now, and
// for a buff that runs out, for how long.
type BuffInfo struct {
	Icon, Name, Effect, How string
	On                      bool
	Off                     string        // the word while it's not on; "not worn" when empty
	Left                    time.Duration // for a buff that runs out; 0 for one that lasts while it's worn
}

// BuffScreen draws every buff as a pair of lines: its icon, its name lit
// gold while worn and what it adds; under them how it's come by and
// whether it's worn now, or how long it has left.
func BuffScreen(name string, buffs []BuffInfo) ([]byte, error) {
	return buffCanvas(name, buffs).Soft(screenScale)
}

func buffCanvas(name string, buffs []BuffInfo) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	r := image.Rect(4, 4, 380, 212)
	c.Window(r)
	c.Tab(r, "buffs")
	tabName(c, r, name)
	x, rx := 16, 368
	step := min(44, (r.Max.Y-8-paneTop(r))/max(len(buffs), 1))
	for i, b := range buffs {
		y := paneTop(r) + i*step
		c.Icon(b.Icon, image.Pt(x, y-iconLift))
		ink := pixel.Dim
		if b.On {
			ink = pixel.Gold
		}
		c.Print(pixel.Bold, x+pixel.IconCells+4, y, b.Name, ink)
		right(c, rx, y, b.Effect, ink)
		var at int
		switch {
		case b.On && b.Left > 0:
			at = right(c, rx, y+16, "ends in "+core.Duration(b.Left), pixel.Ember)
		case b.On:
			at = right(c, rx, y+16, "worn", pixel.Green)
		default:
			at = rightIn(c, pixel.Light, rx, y+16, or(b.Off, "not worn"), pixel.Dim)
		}
		hx := x + pixel.IconCells + 4
		c.FitIn(pixel.Light, hx, y+16, b.How, pixel.Dim, at-pixel.Advance-hx)
	}
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
	tabName(c, r, name)
	y := paneTop(r)
	whatX := x + pixel.Light.Width("2026-10-16") + pixel.Advance
	line := func(day, icon, what string, pp int64, ink uint8) {
		label(c, x, y, day)
		value := grouped(pp/unit) + " pp"
		if pp >= 0 {
			value = "+" + value
		}
		at := right(c, rx, y, value, ink)
		c.Icon(icon, image.Pt(whatX, y-iconLift))
		c.Fit(whatX+pixel.IconCells+4, y, what, pixel.Ink, at-pixel.Advance-whatX-pixel.IconCells-4)
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
		label(c, x, y, "nothing yet: a day goes in when it turns")
	}
	return c
}

// Treasury is the server's money as admins see it.
type Treasury struct {
	Balance        int64
	Taking         bool
	Fund           int64  // the part of Balance only grants spend
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
	row := func(r image.Rectangle, i int, icon, name, value string, ink uint8) {
		x, y := r.Min.X+12, paneTop(r)+i*19
		if icon != "" {
			c.Icon(icon, image.Pt(x, y-iconLift))
		}
		label(c, x+pixel.IconCells+4, y, name)
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
	row(tr, 1, "bell", "levy "+Rules.LevyPct(), taking, ink)
	row(tr, 2, "envelope", "contest fund", grouped(t.Fund/unit)+" pp", pixel.Ink)
	row(tr, 3, "laurel", "for prizes", grouped((t.Balance-t.Fund)/unit)+" pp", pixel.Ink)

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
