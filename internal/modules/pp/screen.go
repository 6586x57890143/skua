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
		c.Portrait(s.Portrait, image.Pt(40, 22))
	} else {
		c.Bird(image.Pt(8, 6))
	}
	// laurels sit under the portrait, as an emblem would
	if s.Laurels > 0 {
		n := fmt.Sprintf("x%d", s.Laurels)
		lx := 72 - (pixel.IconCells+2+len(n)*pixel.Advance)/2
		iconText(c, "laurel", lx, 134, n, pixel.Green)
	}

	c.Window(image.Rect(144, 4, 380, 152))
	x, r := 156, 368
	c.Fit(x, 12, s.Name, pixel.Ink, r-x)
	iconText(c, classIcons[s.Class.Key], x, 32, s.Class.Name(), pixel.Dim)
	right(c, r, 32, s.Class.Title(s.Level), pixel.Ink)

	c.Text(x, 52, "lv", pixel.Dim, 0)
	c.Text(x+20, 52, strconv.Itoa(s.Level), pixel.Ink, 0)
	right(c, r, 52, grouped((s.Need-s.Into)/unit)+" to next", pixel.Dim)

	gauge := x + 6*pixel.Advance
	c.Text(x, 72, "pp", pixel.Dim, 0)
	c.Bar(image.Rect(gauge, 78, r, 83), s.Into, s.Need, pixel.Yellow)
	right(c, r, 88, grouped(s.Into/unit)+" / "+grouped(s.Need/unit), pixel.Dim)

	c.Text(x, 106, "vigor", pixel.Dim, 0)
	c.Bar(image.Rect(gauge, 112, r-56, 117), s.Vigor, 100, pixel.Crimson)
	right(c, r, 106, s.Phase, pixel.Ink)

	c.Cursor(x+10, 134)
	c.Text(x+14, 128, fmt.Sprintf("quests %d held", s.Held), pixel.Ink, 0)
	total := grouped(s.Total / unit)
	right(c, r, 128, total, pixel.Gold)
	c.Icon("pot", image.Pt(r-len(total)*pixel.Advance-pixel.IconCells-4, 128-iconLift))

	buffs := image.Rect(4, 156, 252, 212)
	c.Window(buffs)
	c.Tab(buffs, "buffs")
	drawBuffs(c, s.Buffs, image.Rect(16, buffs.Min.Y+pixel.TabDrop, buffs.Max.X-12, buffs.Max.Y-4))

	season := image.Rect(256, 156, 380, 212)
	c.Window(season)
	c.Tab(season, "season")
	x, r = 266, 370
	y := season.Min.Y + pixel.TabDrop + 6
	if s.Season > 0 {
		c.Text(x, y, fmt.Sprintf("season %d", s.Season), pixel.Dim, 0)
		right(c, r, y, fmt.Sprintf("wk %d", s.Week), pixel.Dim)
	} else {
		c.Text(x, y, "preseason", pixel.Dim, 0)
	}
	place := "unplaced"
	if s.Place > 0 {
		place = ordinal(s.Place)
	}
	c.Text(x, y+16, "place", pixel.Dim, 0)
	right(c, r, y+16, place, pixel.Ink)

	return c.PNG(screenScale)
}

// drawBuffs sets the buffs worn in r: on one line while they fit, and in
// two columns over two lines past that.
func drawBuffs(c *pixel.Canvas, buffs []Buff, r image.Rectangle) {
	if len(buffs) == 0 {
		c.Text(r.Min.X, r.Min.Y+13, "none worn", pixel.Dim, 0)
		return
	}
	width := 0
	for _, b := range buffs {
		width += pixel.IconCells + 4 + len(b.Label)*pixel.Advance + pixel.Advance
	}
	if width-pixel.Advance <= r.Dx() {
		x := r.Min.X
		for _, b := range buffs {
			x = iconText(c, b.Icon, x, r.Min.Y+13, b.Label, pixel.Gold) + pixel.Advance
		}
		return
	}
	col := r.Dx() / 2
	for i, b := range buffs {
		x, y := r.Min.X+i%2*col, r.Min.Y+4+i/2*17
		c.Icon(b.Icon, image.Pt(x, y-iconLift))
		c.Fit(x+pixel.IconCells+4, y, b.Label, pixel.Gold, col-pixel.IconCells-8)
	}
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
	list := image.Rect(4, 4, 380, 170)
	c.Window(list)
	c.Tab(list, "quests")
	right(c, 368, 10, name, pixel.Dim)

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
