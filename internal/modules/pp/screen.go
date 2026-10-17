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

// Sheet is what the status screen shows of one member.
type Sheet struct {
	Portrait     image.Image // the class's portrait, 32 by 48; nil shows skua
	Name         string
	Class        Class
	Level        int
	Into, Need   int64 // pp into this level and what it costs
	Total        int64 // everything earned, which sets the level
	Purse        int64 // what is left to spend
	Vigor        int64 // percent of today's pool left
	Phase        string
	Buffs        []Buff
	Season, Week int64
	Place        int // on the season's board, 0 for none yet
	Laurels      int // checkpoints placed at this season
	Held         int // quests held
	RerollReady  bool
}

// Buff is one buff as a screen shows it: its icon and its word.
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

// right sets s so that it ends at x.
func right(c *pixel.Canvas, x, y int, s string, ink uint8) {
	c.Text(x-len(pixel.Plain(s))*pixel.Advance, y, s, ink, 0)
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

// fitPair is left and right shortened, a word at a time and the same word
// in both, until they fit width with two characters between them. With
// right empty it fits left alone.
func fitPair(left, right string, width int) (string, string) {
	gap := 0
	if right != "" {
		gap = 2
	}
	for _, f := range shortForms {
		if (len(left)+gap+len(right))*pixel.Advance <= width {
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
	c.Fit(x, 12, s.Name, pixel.Ink, r-x)
	class, title := fitPair(s.Class.Name(), s.Class.Title(s.Level), r-x-pixel.IconCells-4)
	iconText(c, classIcons[s.Class.Key], x, 31, class, pixel.Dim)
	right(c, r, 31, title, pixel.Ink)

	c.Text(x, 50, "lv", pixel.Dim, 0)
	c.Text(x+3*pixel.Advance, 50, strconv.Itoa(s.Level), pixel.Ink, 0)
	right(c, r, 50, grouped((s.Need-s.Into)/unit)+" to next", pixel.Dim)

	gauge := x + 6*pixel.Advance
	c.Text(x, 69, "pp", pixel.Dim, 0)
	c.Bar(image.Rect(gauge, 74, r, 79), s.Into, s.Need, pixel.Yellow)
	right(c, r, 85, grouped(s.Into/unit)+" / "+grouped(s.Need/unit), pixel.Dim)

	c.Text(x, 104, "vigor", pixel.Dim, 0)
	c.Bar(image.Rect(gauge, 109, r-8*pixel.Advance, 114), s.Vigor, 100, pixel.Crimson)
	right(c, r, 104, s.Phase, pixel.Ink)

	c.Cursor(x+10, 129)
	end := c.Text(x+14, 123, "quests", pixel.Dim, 0)
	c.Text(end+pixel.Advance, 123, fmt.Sprintf("%d / %d", s.Held, Default.QuestHold), pixel.Ink, 0)
	total := grouped(s.Purse / unit)
	right(c, r, 123, total, pixel.Gold)
	c.Icon("pot", image.Pt(r-len(total)*pixel.Advance-pixel.IconCells-4, 123-iconLift))

	buffs := image.Rect(4, 152, 252, 212)
	c.Window(buffs)
	c.Tab(buffs, "buffs")
	drawBuffs(c, s.Buffs, image.Rect(16, buffs.Min.Y+pixel.TabDrop, buffs.Max.X-12, buffs.Max.Y-4))

	// The tab names the season and week, or the preseason; under it,
	// label and value, the place on the board and the laurels won.
	season := image.Rect(256, 152, 380, 212)
	c.Window(season)
	tab := "preseason"
	if s.Season > 0 {
		tab = fmt.Sprintf("season %d week %d", s.Season, s.Week)
	}
	c.Tab(season, tab)
	x, r = 266, 368
	y := season.Min.Y + pixel.TabDrop + 3
	place := "unplaced"
	if s.Place > 0 {
		place = ordinal(s.Place)
	}
	c.Text(x, y, "board", pixel.Dim, 0)
	right(c, r, y, place, pixel.Ink)
	c.Text(x, y+17, "laurels", pixel.Dim, 0)
	if s.Laurels > 0 {
		n := fmt.Sprintf("x%d", s.Laurels)
		right(c, r, y+17, n, pixel.Green)
		c.Icon("laurel", image.Pt(r-len(n)*pixel.Advance-pixel.IconCells-2, y+17-iconLift))
	} else {
		right(c, r, y+17, "none", pixel.Dim)
	}
	return c
}

// drawBuffs sets the buffs worn in r: on one line in its middle while they
// fit, and in two columns over two lines past that.
func drawBuffs(c *pixel.Canvas, buffs []Buff, r image.Rectangle) {
	mid := r.Min.Y + (r.Dy()-pixel.IconCells)/2 + iconLift
	if len(buffs) == 0 {
		c.Text(r.Min.X, mid, "none worn", pixel.Dim, 0)
		return
	}
	width := 0
	for _, b := range buffs {
		width += pixel.IconCells + 4 + len(b.Label)*pixel.Advance + pixel.Advance
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
// and what it pays.
func QuestScreen(name string, quests []QuestLine, rerollReady bool) ([]byte, error) {
	return questCanvas(name, quests, rerollReady).Soft(screenScale)
}

func questCanvas(name string, quests []QuestLine, rerollReady bool) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	list := image.Rect(4, 4, 380, 172)
	c.Window(list)
	c.Tab(list, "quests")
	x, r := 34, 368
	c.Fit(r-min(len(pixel.Plain(name)), 24)*pixel.Advance, tabLine(list), name, pixel.Dim, 24*pixel.Advance)

	tier := [3]uint8{pixel.Ink, pixel.Green, pixel.Gold}
	for i, q := range quests {
		y := 32 + i*44
		if i == 0 {
			c.Cursor(x-4, y+6)
		}
		reward := grouped(q.Reward/unit) + " pp"
		if q.Tier > 0 {
			reward = QuestTiers[q.Tier] + " " + reward
		}
		right(c, r, y, reward, tier[q.Tier])
		c.Icon(q.Icon, image.Pt(x, y-iconLift))
		c.Fit(x+pixel.IconCells+4, y, q.What, pixel.Ink, r-(len(reward)+1)*pixel.Advance-x-pixel.IconCells-4)
		bar := x + pixel.IconCells + 4
		c.Bar(image.Rect(bar, y+22, 280, y+27), q.Progress, q.Goal, pixel.Blue)
		right(c, r, y+18, fmt.Sprintf("%d / %d", min(q.Progress, q.Goal), q.Goal), pixel.Dim)
	}
	if len(quests) == 0 {
		c.Text(x, 32, "no quests open; a new one comes at 00:00 utc", pixel.Dim, 0)
	}

	foot := image.Rect(4, 176, 380, 212)
	c.Window(foot)
	y := foot.Min.Y + (foot.Dy()-pixel.IconCells)/2 + iconLift
	end := iconText(c, "reroll", 16, y, "one reroll a day", pixel.Dim) + 2*pixel.Advance
	if rerollReady {
		c.Text(end, y, "ready", pixel.Green, 0)
	} else {
		c.Text(end, y, "used", pixel.Dim, 0)
	}
	right(c, r, y, fmt.Sprintf("%d / %d open", len(quests), Default.QuestHold), pixel.Dim)
	return c
}

// tabLine is the y a line beside r's tab is set at, to share its text's
// line.
func tabLine(r image.Rectangle) int { return r.Min.Y + 3 }
