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

// StatusScreen draws s as the classic status menu: the bird's portrait,
// who they are and how far along, and a strip of what they wear and where
// they stand. It returns the PNG.
func StatusScreen(s Sheet) ([]byte, error) {
	c := pixel.New(screenW, screenH)

	c.Window(image.Rect(4, 4, 140, 140))
	c.Bird(image.Pt(8, 8))

	c.Window(image.Rect(144, 4, 380, 140))
	x, r := 156, 368
	c.Text(x, 12, s.Name, pixel.Ink, 30)
	iconText(c, classIcons[s.Class.Key], x, 32, s.Class.Name(), pixel.Dim)
	right(c, r, 32, s.Class.Title(s.Level), pixel.Ink)

	c.Text(x, 56, "lv", pixel.Dim, 0)
	c.Text(x+20, 56, strconv.Itoa(s.Level), pixel.Ink, 0)
	right(c, r, 56, grouped((s.Need-s.Into)/unit)+" to next", pixel.Dim)

	bar := x + pixel.IconCells + 4
	c.Icon("pot", image.Pt(x, 80-iconLift))
	c.Bar(image.Rect(bar, 82, r, 90), s.Into, s.Need, pixel.Gold)
	right(c, r, 96, grouped(s.Into/unit)+" / "+grouped(s.Need/unit), pixel.Dim)

	c.Icon("heart", image.Pt(x, 118-iconLift))
	c.Bar(image.Rect(bar, 120, r-56, 128), s.Vigor, 100, pixel.Green)
	right(c, r, 118, s.Phase, pixel.Ink)

	c.Window(image.Rect(4, 144, 380, 212))
	x, r = 16, 368
	c.Text(x, 152, "buffs", pixel.Dim, 0)
	bx := x + 6*pixel.Advance
	if len(s.Buffs) == 0 {
		c.Text(bx, 152, "none worn", pixel.Dim, 0)
	}
	for _, b := range s.Buffs {
		bx = iconText(c, b.Icon, bx, 152, b.Label, pixel.Gold) + pixel.Advance
	}

	if s.Season > 0 {
		end := c.Text(x, 172, fmt.Sprintf("season %d", s.Season), pixel.Dim, 0)
		end = c.Text(end+2*pixel.Advance, 172, fmt.Sprintf("week %d", s.Week), pixel.Dim, 0)
		if s.Laurels > 0 {
			iconText(c, "laurel", end+2*pixel.Advance, 172, fmt.Sprintf("x%d", s.Laurels), pixel.Green)
		}
	} else {
		c.Text(x, 172, "preseason", pixel.Dim, 0)
	}
	place := "unplaced"
	if s.Place > 0 {
		place = ordinal(s.Place)
	}
	right(c, r, 172, place, pixel.Ink)

	c.Cursor(x+12, 198)
	end := c.Text(x+16, 192, fmt.Sprintf("quests %d held", s.Held), pixel.Ink, 0)
	if s.RerollReady {
		iconText(c, "reroll", end+2*pixel.Advance, 192, "ready", pixel.Green)
	}
	total := grouped(s.Total / unit)
	right(c, r, 192, total, pixel.Gold)
	c.Icon("pot", image.Pt(r-len(total)*pixel.Advance-pixel.IconCells-4, 192-iconLift))

	return c.PNG(screenScale)
}

// QuestLine is one held quest as the quest screen shows it.
type QuestLine struct {
	Icon, What     string
	Progress, Goal int64
	Tier           int
	Reward         int64
}

// QuestScreen draws the quests held as an item list, the cursor on the
// first, each with its icon, how far along it is and what it pays.
func QuestScreen(name string, quests []QuestLine, rerollReady bool) ([]byte, error) {
	c := pixel.New(screenW, screenH)
	c.Window(image.Rect(4, 4, 380, 36))
	c.Text(16, 13, "quests", pixel.Ink, 0)
	right(c, 368, 13, name, pixel.Dim)

	c.Window(image.Rect(4, 40, 380, 176))
	tier := [3]uint8{pixel.Ink, pixel.Green, pixel.Gold}
	x, r := 34, 368
	for i, q := range quests {
		y := 52 + i*42
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
		c.Bar(image.Rect(bar, y+20, 280, y+28), q.Progress, q.Goal, pixel.Green)
		right(c, r, y+18, fmt.Sprintf("%d / %d", min(q.Progress, q.Goal), q.Goal), pixel.Dim)
	}
	if len(quests) == 0 {
		c.Text(28, 100, "nothing held; a new one comes at 00:00 utc", pixel.Dim, 0)
	}

	c.Window(image.Rect(4, 180, 380, 212))
	end := iconText(c, "reroll", 16, 191, "one reroll a day", pixel.Dim) + 2*pixel.Advance
	if rerollReady {
		c.Text(end, 191, "ready", pixel.Green, 0)
	} else {
		c.Text(end, 191, "used", pixel.Dim, 0)
	}
	right(c, 368, 191, fmt.Sprintf("%d / %d held", len(quests), Default.QuestHold), pixel.Dim)
	return c.PNG(screenScale)
}
