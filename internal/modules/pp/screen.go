package pp

import (
	"fmt"
	"image"
	"strconv"

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
	Total        int64
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

// classIcons is each class's emblem until the classes have portraits.
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

// StatusScreen draws s as Tactics Ogre's status screen: the portrait in
// its frame, who they are and how far along with the gauges under it, and
// two named panels, what they wear and where they stand. It returns the
// PNG.
func StatusScreen(s Sheet) ([]byte, error) {
	c := pixel.New(screenW, screenH)

	c.Window(image.Rect(4, 4, 140, 152))
	if s.Portrait != nil {
		c.Portrait(s.Portrait, image.Pt(40, 30))
	} else {
		c.Bird(image.Pt(8, 14))
	}

	c.Window(image.Rect(144, 4, 380, 152))
	x, r := 156, 368
	c.Text(x, 12, s.Name, pixel.Ink, 24)
	if s.Laurels > 0 {
		lx := r - len(fmt.Sprintf("x%d", s.Laurels))*pixel.Advance
		right(c, r, 12, fmt.Sprintf("x%d", s.Laurels), pixel.Green)
		c.Icon("laurel", image.Pt(lx-pixel.IconCells-2, 12-iconLift))
	}
	iconText(c, classIcons[s.Class.Key], x, 32, s.Class.Name(), pixel.Dim)
	right(c, r, 32, s.Class.Title(s.Level), pixel.Ink)

	c.Text(x, 52, "lv", pixel.Dim, 0)
	c.Text(x+20, 52, strconv.Itoa(s.Level), pixel.Ink, 0)
	right(c, r, 52, grouped((s.Need-s.Into)/unit)+" to next", pixel.Dim)

	end := c.Badge(x+2, 72, "pp", pixel.Glove)
	c.Bar(image.Rect(end+4, 78, r, 83), s.Into, s.Need, pixel.Blue)
	right(c, r, 88, grouped(s.Into/unit)+" / "+grouped(s.Need/unit), pixel.Dim)

	end = c.Badge(x+2, 106, "vigor", pixel.Glove)
	c.Bar(image.Rect(end+4, 112, r-56, 117), s.Vigor, 100, pixel.Crimson)
	right(c, r, 106, s.Phase, pixel.Ink)

	c.Cursor(x+10, 134)
	c.Text(x+14, 128, fmt.Sprintf("quests %d held", s.Held), pixel.Ink, 0)
	total := grouped(s.Total / unit)
	right(c, r, 128, total, pixel.Gold)
	c.Icon("pot", image.Pt(r-len(total)*pixel.Advance-pixel.IconCells-4, 128-iconLift))

	buffs := image.Rect(4, 164, 252, 212)
	c.Window(buffs)
	c.Tab(buffs, "buffs")
	bx := 16
	if len(s.Buffs) == 0 {
		c.Text(bx, 182, "none worn", pixel.Dim, 0)
	}
	for _, b := range s.Buffs {
		bx = iconText(c, b.Icon, bx, 182, b.Label, pixel.Gold) + pixel.Advance
	}

	season := image.Rect(256, 164, 380, 212)
	c.Window(season)
	c.Tab(season, "season")
	x, r = 266, 370
	if s.Season > 0 {
		c.Text(x, 174, fmt.Sprintf("season %d", s.Season), pixel.Dim, 0)
		right(c, r, 174, fmt.Sprintf("wk %d", s.Week), pixel.Dim)
	} else {
		c.Text(x, 174, "preseason", pixel.Dim, 0)
	}
	place := "unplaced"
	if s.Place > 0 {
		place = ordinal(s.Place)
	}
	c.Text(x, 192, "place", pixel.Dim, 0)
	right(c, r, 192, place, pixel.Ink)

	return c.PNG(screenScale)
}

// QuestLine is one held quest as the quest screen shows it.
type QuestLine struct {
	Icon, What     string
	Progress, Goal int64
	Tier           int
	Reward         int64
}

// QuestScreen draws the quests held as an item list in a named panel, the
// cursor on the first, each with its icon, a gauge of how far along it is
// and what it pays.
func QuestScreen(name string, quests []QuestLine, rerollReady bool) ([]byte, error) {
	c := pixel.New(screenW, screenH)
	list := image.Rect(4, 12, 380, 170)
	c.Window(list)
	c.Tab(list, "quests")
	right(c, 368, 20, name, pixel.Dim)

	tier := [3]uint8{pixel.Ink, pixel.Green, pixel.Gold}
	x, r := 34, 368
	for i, q := range quests {
		y := 40 + i*42
		if i == 0 {
			c.Cursor(x-2, y+6)
		}
		iconText(c, q.Icon, x, y, q.What, pixel.Ink)
		reward := grouped(q.Reward/unit) + " pp"
		right(c, r, y, reward, tier[q.Tier])
		if q.Tier > 0 {
			right(c, r-(len(reward)+1)*pixel.Advance, y, QuestTiers[q.Tier], tier[q.Tier])
		}
		bar := x + pixel.IconCells + 4
		c.Bar(image.Rect(bar, y+20, 280, y+25), q.Progress, q.Goal, pixel.Blue)
		right(c, r, y+16, fmt.Sprintf("%d / %d", min(q.Progress, q.Goal), q.Goal), pixel.Dim)
	}
	if len(quests) == 0 {
		c.Text(28, 90, "nothing held; a new one comes at 00:00 utc", pixel.Dim, 0)
	}

	foot := image.Rect(4, 180, 380, 212)
	c.Window(foot)
	end := iconText(c, "reroll", 16, 190, "one reroll a day", pixel.Dim) + 2*pixel.Advance
	if rerollReady {
		c.Text(end, 190, "ready", pixel.Green, 0)
	} else {
		c.Text(end, 190, "used", pixel.Dim, 0)
	}
	right(c, 368, 190, fmt.Sprintf("%d / %d held", len(quests), Default.QuestHold), pixel.Dim)
	return c.PNG(screenScale)
}
