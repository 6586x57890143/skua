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
	Buffs        []string
	Season, Week int64
	Place        int // on the season's board, 0 for none yet
	Held         int // quests held
	RerollReady  bool
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
	c.Text(x, 28, s.Class.Name(), pixel.Dim, 0)
	right(c, r, 28, s.Class.Title(s.Level), pixel.Ink)

	c.Text(x, 50, "lv", pixel.Dim, 0)
	c.Text(x+28, 50, strconv.Itoa(s.Level), pixel.Ink, 0)
	right(c, r, 50, grouped((s.Need-s.Into)/unit)+" to next", pixel.Dim)

	c.Text(x, 72, "pp", pixel.Gold, 0)
	c.Bar(image.Rect(x+28, 75, r, 83), s.Into, s.Need, pixel.Gold)
	right(c, r, 88, grouped(s.Into/unit)+" / "+grouped(s.Need/unit), pixel.Dim)

	c.Text(x, 110, "vigor", pixel.Dim, 0)
	c.Bar(image.Rect(x+42, 113, r-56, 121), s.Vigor, 100, pixel.Green)
	right(c, r, 110, s.Phase, pixel.Ink)

	c.Window(image.Rect(4, 144, 380, 212))
	x, r = 16, 368
	c.Text(x, 152, "buffs", pixel.Dim, 0)
	bx := x + 56
	if len(s.Buffs) == 0 {
		c.Text(bx, 152, "none worn", pixel.Dim, 0)
	}
	for _, b := range s.Buffs {
		bx = c.Text(bx, 152, b, pixel.Gold, 0) + pixel.Advance
	}

	if s.Season > 0 {
		c.Text(c.Text(x, 170, fmt.Sprintf("season %d", s.Season), pixel.Dim, 0)+2*pixel.Advance, 170, fmt.Sprintf("week %d", s.Week), pixel.Dim, 0)
	} else {
		c.Text(x, 170, "preseason", pixel.Dim, 0)
	}
	place := "unplaced"
	if s.Place > 0 {
		place = ordinal(s.Place)
	}
	right(c, r, 170, place, pixel.Ink)

	c.Cursor(x+12, 194)
	end := c.Text(x+16, 188, fmt.Sprintf("quests %d held", s.Held), pixel.Ink, 0)
	if s.RerollReady {
		c.Text(end+2*pixel.Advance, 188, "reroll ready", pixel.Green, 0)
	}
	right(c, r, 188, grouped(s.Total/unit)+" pp", pixel.Gold)

	return c.PNG(screenScale)
}

// QuestLine is one held quest as the quest screen shows it.
type QuestLine struct {
	What           string
	Progress, Goal int64
	Tier           int
	Reward         int64
}

// QuestScreen draws the quests held as an item list, the cursor on the
// first, each with how far along it is and what it pays.
func QuestScreen(name string, quests []QuestLine, rerollReady bool) ([]byte, error) {
	c := pixel.New(screenW, screenH)
	c.Window(image.Rect(4, 4, 380, 36))
	c.Text(16, 13, "quests", pixel.Ink, 0)
	right(c, 368, 13, name, pixel.Dim)

	c.Window(image.Rect(4, 40, 380, 176))
	tier := [3]uint8{pixel.Ink, pixel.Green, pixel.Gold}
	for i, q := range quests {
		y := 50 + i*42
		if i == 0 {
			c.Cursor(24, y+6)
		}
		c.Text(28, y, q.What, pixel.Ink, 30)
		reward := grouped(q.Reward/unit) + " pp"
		right(c, 368, y, reward, tier[q.Tier])
		if q.Tier > 0 {
			right(c, 368-(len(reward)+1)*pixel.Advance, y, QuestTiers[q.Tier], tier[q.Tier])
		}
		c.Bar(image.Rect(28, y+18, 280, y+26), q.Progress, q.Goal, pixel.Green)
		right(c, 368, y+16, fmt.Sprintf("%d / %d", min(q.Progress, q.Goal), q.Goal), pixel.Dim)
	}
	if len(quests) == 0 {
		c.Text(28, 100, "nothing held; a new one comes at 00:00 utc", pixel.Dim, 0)
	}

	c.Window(image.Rect(4, 180, 380, 212))
	end := c.Text(16, 189, "one reroll a day", pixel.Dim, 0) + 2*pixel.Advance
	if rerollReady {
		c.Text(end, 189, "ready", pixel.Green, 0)
	} else {
		c.Text(end, 189, "used", pixel.Dim, 0)
	}
	right(c, 368, 189, fmt.Sprintf("%d / %d held", len(quests), Default.QuestHold), pixel.Dim)
	return c.PNG(screenScale)
}
