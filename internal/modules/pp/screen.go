package pp

import (
	"fmt"
	"image"
	"strconv"
	"strings"

	"github.com/6586x57890143/skua/internal/pixel"
)

// The screens are drawn at an old console's size and sent three times
// larger, so a phone and a desktop both show them crisp.
const (
	screenW, screenH = 384, 216
	screenScale      = 3
)

// The screens are typeset in pixel's four faces, each with one job: a tab
// or heading in Bold, a member's name in the look their rung bought, a
// label in Light and dim, a value or a line of text in Regular, and the
// story an item tells in Serif.

// Sheet is what the status screen shows of one member.
type Sheet struct {
	Portrait     image.Image // the class's portrait, 32 by 48; nil shows skua
	Name         string
	Rung         int64 // on the rookery stair, which sets their look
	Class        Class
	Level        int
	Into, Need   int64 // pp into this level and what it costs
	Total        int64 // everything earned, which sets the level
	Purse        int64 // what is left to spend
	Vigor        int64 // percent of today's pool left
	Phase        string
	Buffs        []Buff
	Season, Week int64
	Place        int // on the board the season box names, 0 for none yet
	Laurels      int // checkpoints placed at this season
	Held         int // quests open
	RerollReady  bool
}

// Buff is one buff as the status screen shows it: its icon and its word.
type Buff struct{ Icon, Label string }

// classIcons is each class's emblem, beside its name and on its buttons.
var classIcons = map[string]string{
	"wanderer": "lantern", "fighter": "sword", "ranger": "bow", "sorcerer": "orb",
	"thief": "pouch", "bard": "lute", "monk": "beads", "knight": "shield",
}

// An icon is 16 cells and a line of text 13, so an icon set beside text
// starts iconLift above it and the two share a middle.
const iconLift = 2

// iconText sets an icon at x on the text line y with s beside it, and
// returns where s ends.
func iconText(c *pixel.Canvas, icon string, x, y int, s string, ink uint8) int {
	c.Icon(icon, image.Pt(x, y-iconLift))
	return c.Text(x+pixel.IconCells+4, y, s, ink, 0)
}

// right sets s in Regular so that it ends at x.
func right(c *pixel.Canvas, x, y int, s string, ink uint8) int {
	return rightIn(c, pixel.Regular, x, y, s, ink)
}

// rightIn sets s in f so that it ends at x, and returns where it starts.
func rightIn(c *pixel.Canvas, f *pixel.Face, x, y int, s string, ink uint8) int {
	at := x - f.Width(s)
	c.Print(f, at, y, s, ink)
	return at
}

// label sets a label: Light, dim.
func label(c *pixel.Canvas, x, y int, s string) int { return c.Print(pixel.Light, x, y, s, pixel.Dim) }

// labelGap is the space between a label and its value.
const labelGap = 5

// rightLabelled sets a value in Regular with a label after it in Light,
// the pair ending at x, as "321 to next"; it returns where the pair starts.
func rightLabelled(c *pixel.Canvas, x, y int, value string, ink uint8, after string) int {
	at := rightIn(c, pixel.Light, x, y, after, pixel.Dim)
	return rightIn(c, pixel.Regular, at-labelGap, y, value, ink)
}

// grouped writes n with a comma every three digits.
func grouped(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// ordinal is 1st, 2nd, 3rd, 4th and so on.
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// shortForms are the words a line gives up first when it runs out of
// room, in order: an old game's abbreviations, so "raven sorcerer" over
// "hedge sorcerer" reads "raven sorc" over "hedge sorc".
var shortForms = [][2]string{
	{"sorcerer", "sorc"}, {"master", "mstr"}, {"commander", "cmdr"}, {"wanderer", "wndr"},
	{"nightingale", "n'gale"}, {"albatross", "albtrs"},
}

// fitPair is left set in lf and right in rf, shortened a word at a time
// and the same word in both, until they fit width with two characters of
// lf between them. With right empty it fits left alone.
func fitPair(left string, lf *pixel.Face, right string, rf *pixel.Face, width int) (string, string) {
	gap := 0
	if right != "" {
		gap = 2 * lf.Advance
	}
	for _, f := range shortForms {
		if lf.Width(left)+gap+rf.Width(right) <= width {
			break
		}
		left, right = strings.ReplaceAll(left, f[0], f[1]), strings.ReplaceAll(right, f[0], f[1])
	}
	return left, right
}

// StatusScreen draws s as Tactics Ogre's status screen: the portrait in
// its frame, who they are and how far along with the gauges under it, and
// two named panels, what they wear and where they stand. It returns the
// picture.
func StatusScreen(s Sheet) ([]byte, error) { return statusCanvas(s).Soft(screenScale) }

func statusCanvas(s Sheet) *pixel.Canvas {
	c := pixel.New(screenW, screenH)

	who := image.Rect(4, 4, 140, 148)
	c.Window(who)
	if s.Portrait != nil {
		c.Portrait(s.Portrait, image.Pt(40, 28), 2)
	} else {
		c.Bird(image.Pt(8, 12))
	}

	// The lines sit 19 apart, a gauge centred on its label's x-height.
	c.Window(image.Rect(144, 4, 380, 148))
	x, r := 156, 368
	setName(c, lookOf(s.Rung), x, 12, s.Name, r-x, image.Rect(x-5, 10, r+5, 28))
	class, title := fitPair(s.Class.Name(), pixel.Regular, s.Class.Title(s.Level), pixel.Serif, r-x-pixel.IconCells-4)
	iconText(c, classIcons[s.Class.Key], x, 32, class, pixel.Ink)
	rightIn(c, pixel.Serif, r, 32, title, pixel.Ink)

	label(c, x, 51, "lv")
	c.Text(x+pixel.Light.Width("lv")+labelGap, 51, strconv.Itoa(s.Level), pixel.Ink, 0)
	rightLabelled(c, r, 51, grouped((s.Need-s.Into)/unit), pixel.Ink, "to next")

	gauge := x + 6*pixel.Advance
	label(c, x, 69, "pp")
	c.Bar(image.Rect(gauge, 74, r, 79), s.Into, s.Need, pixel.Yellow)
	right(c, r, 85, grouped(s.Into/unit)+" / "+grouped(s.Need/unit), pixel.Dim)

	label(c, x, 104, "vigor")
	c.Bar(image.Rect(gauge, 109, r-8*pixel.Advance, 114), s.Vigor, 100, pixel.Crimson)
	right(c, r, 104, s.Phase, pixel.Ink)

	c.Cursor(x+10, 129)
	end := label(c, x+14, 123, "quests")
	c.Text(end+labelGap, 123, fmt.Sprintf("%d / %d", s.Held, Default.QuestHold), pixel.Ink, 0)
	total := grouped(s.Purse / unit)
	right(c, r, 123, total, pixel.Gold)
	c.Icon("pot", image.Pt(r-pixel.Regular.Width(total)-pixel.IconCells-4, 123-iconLift))

	buffs := image.Rect(4, 152, 236, 212)
	c.Window(buffs)
	c.Tab(buffs, "buffs")
	drawBuffs(c, s.Buffs, image.Rect(16, buffs.Min.Y+pixel.TabDrop, buffs.Max.X-12, buffs.Max.Y-4))

	// The tab names the season and week, or the preseason; under it,
	// label and value, the place on the board and the laurels won.
	season := image.Rect(240, 152, 380, 212)
	c.Window(season)
	tab := "preseason"
	if s.Season > 0 {
		tab = fmt.Sprintf("season %d week %d", s.Season, s.Week)
	}
	c.Tab(season, tab)
	x, r = 250, 368
	y := season.Min.Y + pixel.TabDrop + 3
	place := "unplaced"
	if s.Place > 0 {
		place = ordinal(s.Place)
	}
	label(c, x, y, "board")
	right(c, r, y, place, pixel.Ink)
	label(c, x, y+17, "laurels")
	if s.Laurels > 0 {
		n := fmt.Sprintf("x%d", s.Laurels)
		right(c, r, y+17, n, pixel.Green)
		c.Icon("laurel", image.Pt(r-pixel.Regular.Width(n)-pixel.IconCells-2, y+17-iconLift))
	} else {
		rightIn(c, pixel.Light, r, y+17, "none", pixel.Dim)
	}
	return c
}

// drawBuffs sets the buffs worn in r: on one line in its middle while they
// fit, and in two columns over two lines past that.
func drawBuffs(c *pixel.Canvas, buffs []Buff, r image.Rectangle) {
	mid := r.Min.Y + (r.Dy()-pixel.IconCells)/2 + iconLift
	if len(buffs) == 0 {
		label(c, r.Min.X, mid, "none worn; the buffs button says how")
		return
	}
	width := 0
	for _, b := range buffs {
		width += pixel.IconCells + 4 + pixel.Regular.Width(b.Label) + pixel.Advance
	}
	if width-pixel.Advance <= r.Dx() {
		x := r.Min.X
		for _, b := range buffs {
			x = iconText(c, b.Icon, x, mid, b.Label, pixel.Gold) + pixel.Advance
		}
		return
	}
	col := r.Dx() / 2
	for i, b := range buffs {
		x, y := r.Min.X+i%2*col, r.Min.Y+3+i/2*17
		c.Icon(b.Icon, image.Pt(x, y-iconLift))
		c.Fit(x+pixel.IconCells+4, y, b.Label, pixel.Gold, col-pixel.IconCells-8)
	}
}

// QuestLine is one open quest as the quest screen shows it.
type QuestLine struct {
	Icon, What     string
	Progress, Goal int64
	Tier           int
	Reward         int64
}

// QuestScreen draws the open quests as an item list in a named panel, the
// cursor on the first, each with its icon, a gauge of how far along it is
// and what it pays, over how many of their rerolls are left today.
func QuestScreen(name string, quests []QuestLine, rerolls int) ([]byte, error) {
	return questCanvas(name, quests, rerolls).Soft(screenScale)
}

func questCanvas(name string, quests []QuestLine, rerolls int) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	list := image.Rect(4, 4, 380, 172)
	c.Window(list)
	c.Tab(list, "quests")
	x, r := 34, 368
	tabName(c, list, name)

	tier := [3]uint8{pixel.Ink, pixel.Green, pixel.Gold}
	for i, q := range quests {
		y := 32 + i*44
		if i == 0 {
			c.Cursor(x-4, y+6)
		}
		at := right(c, r, y, grouped(q.Reward/unit)+" pp", tier[q.Tier])
		if q.Tier > 0 {
			at = rightIn(c, pixel.Bold, at-pixel.Advance, y, QuestTiers[q.Tier], tier[q.Tier])
		}
		c.Icon(q.Icon, image.Pt(x, y-iconLift))
		c.Fit(x+pixel.IconCells+4, y, q.What, pixel.Ink, at-pixel.Advance-x-pixel.IconCells-4)
		bar := x + pixel.IconCells + 4
		c.Bar(image.Rect(bar, y+22, 280, y+27), q.Progress, q.Goal, pixel.Blue)
		right(c, r, y+18, fmt.Sprintf("%d / %d", min(q.Progress, q.Goal), q.Goal), pixel.Dim)
	}
	if len(quests) == 0 {
		label(c, x, 32, "no quests open; a new one comes at 00:00 utc")
	}

	foot := image.Rect(4, 176, 380, 212)
	c.Window(foot)
	y := midLine(foot)
	c.Icon("reroll", image.Pt(16, y-iconLift))
	end := label(c, 16+pixel.IconCells+4, y, "a reroll a day each") + pixel.Advance
	if rerolls > 0 {
		c.Text(end, y, fmt.Sprintf("%d ready", rerolls), pixel.Green, 0)
	} else {
		label(c, end, y, "used")
	}
	rightLabelled(c, r, y, fmt.Sprintf("%d / %d", len(quests), Default.QuestHold), pixel.Ink, "open")
	return c
}

// tabName sets a member's name dim at the right of r's tab line, sharing
// the tab's line.
func tabName(c *pixel.Canvas, r image.Rectangle, name string) {
	const most = 160
	w := min(pixel.Light.Width(name), most)
	c.FitIn(pixel.Light, r.Max.X-12-w, r.Min.Y+3, name, pixel.Dim, w)
}
