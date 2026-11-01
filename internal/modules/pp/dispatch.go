package pp

import (
	_ "embed"
	"fmt"
	"image"
	"strings"
	"sync"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/pixel"
)

// The morning sheet is the first /pp a member opens each day: a screen of
// its own, before the status screen, the way an old game opens on the
// day's news. On the left the broadsheet sung at the rim before the turn,
// the world's news (the same for everyone that day) and a rumour drawn from
// the season's seed, so nobody can reroll it. On the right their own day:
// the quests dealt them and notes on what's true of them. Everything is on
// the picture; the card holds only the way on to the pot. The lines are
// LORE.md's "The broadsheet".

// slotDispatch is the roll a day's rumour draws from, apart from quests'.
const slotDispatch = 1 << 42

// Morning is one member's morning sheet.
type Morning struct {
	Date         string
	News, Rumour string
	Quests       []QuestLine
	Notes        []string
}

// morning is user's sheet for today, and false once they've had it.
func (m *Module) morning(guild, user snowflake.ID) (Morning, bool) {
	today := DayOf(m.now())
	seed := m.st.seedOf(today)
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := m.st.member(s, guild, user, today)
	// A bird who hasn't picked a class yet starts on the title screen.
	if mem.dispatchDay == today || mem.classSeason == -1 || !s.released(today) {
		return Morning{}, false
	}
	mem.dispatchDay = today
	r := Roll(seed, uint64(guild), uint64(user), today, slotDispatch)
	b := m.st.bal()
	sh := Morning{
		Date:   sheetDate(b, today),
		News:   worldLine(b, today),
		Rumour: rumours[r%uint64(len(rumours))],
		Notes:  m.notes(s, mem, today),
	}
	for _, q := range mem.quests {
		if q.aside() {
			continue
		}
		l := QuestLine{Icon: b.QuestKinds[q.tier][q.kind].Icon, Tier: q.tier, Reward: b.QuestPP[q.tier]}
		for j, t := range q.tasks(b) {
			l.Tasks = append(l.Tasks, TaskLine{What: sheetTask(t), Progress: min(q.prog[j], t.Goal), Goal: t.Goal})
		}
		sh.Quests = append(sh.Quests, l)
	}
	return sh, true
}

// sheetTask is a task as the sheet's one line holds it: short, its goal
// in it, each kind its own word.
func sheetTask(t Task) string {
	switch t.Metric {
	case taskVoice:
		return fmt.Sprintf("%dm voice", t.Goal)
	case taskTalk:
		return fmt.Sprintf("%d lines", t.Goal)
	case taskHearth:
		return fmt.Sprintf("%dm hearth", t.Goal)
	case taskSelfie:
		return "a selfie"
	case taskCrowd:
		return fmt.Sprintf("%dm crowd", t.Goal)
	case taskReply:
		return fmt.Sprintf("%d replies", t.Goal)
	case taskStream:
		return fmt.Sprintf("%dm stream", t.Goal)
	case taskCamera:
		return fmt.Sprintf("%dm camera", t.Goal)
	}
	return t.Metric
}

// sheetTaskRoom is how wide a quest's task may be on the sheet, beside the
// widest reward.
const sheetTaskRoom = 72

// sheetDate is how the sheet dates today: by the season's day.
func sheetDate(b *Balance, today int64) string {
	season, _ := b.Season(today)
	if season <= 0 {
		return "before the season"
	}
	return fmt.Sprintf("season %d, day %d", season, today-b.SeasonStart-(season-1)*7*b.SeasonWeeks+1)
}

// worldLine is the world's news on today, "" past what's written.
func worldLine(b *Balance, today int64) string {
	season, _ := b.Season(today)
	lines := worldLines[season]
	if d := today - b.SeasonStart - (season-1)*7*b.SeasonWeeks; season > 0 && d < int64(len(lines)) {
		return lines[d]
	}
	return ""
}

// notes are what's true of mem today worth a line on their sheet. s.mu
// held.
func (m *Module) notes(s *server, mem *member, today int64) []string {
	var out []string
	// A standing order is paid at the turn, before anyone reads a sheet, so
	// a renewing livery still ending today is one the dyer's boy couldn't
	// collect for.
	switch {
	case mem.livery != "" && mem.liveryUntil == today && mem.renew:
		out = append(out, "the dyer's boy found your purse short, so your livery fades tonight")
	case mem.livery != "" && mem.liveryUntil == today:
		out = append(out, "your livery fades tonight unless the yard is paid")
	}
	if l, into, need := m.st.bal().Level(mem.total); need-into <= need/10 {
		out = append(out, fmt.Sprintf("a raven has you %s pp off level %d", grouped((need-into+unit-1)/unit), l+1))
	}
	if s.hearth() > 0 {
		out = append(out, "the hearth is lit, and your work goes further today")
	}
	return out
}

// noNews is the sheet on a day with no news written for it.
const noNews = "the presses had nothing new to set, so the singers sang yesterday's sheet again and nobody minded"

// The pressroom high in the bellward where the sheet is set, at dawn:
// finished art, a ChatGPT painting fitted to the screen's grid at 40
// colours. Replace it whole, framed the same. The sheet's two panes sit
// over its quiet wall and bench as parchment the room shows through, and
// leave the lantern, the press and the window clear.
//
//go:embed press.png
var pressPNG []byte

var pressArt = sync.OnceValue(func() *sumpArt {
	return &sumpArt{img: decodeArt(pressPNG, "press"), shade: 1, mist: lightMist[dawn]}
})

// The pressroom's own places: the sheets pegged up to dry, each from its
// line, and the lantern by the press.
var (
	drying = []image.Rectangle{
		image.Rect(0, 0, 15, 35), image.Rect(22, 0, 38, 55), image.Rect(35, 2, 72, 58), image.Rect(82, 20, 124, 76),
		image.Rect(130, 0, 186, 33), image.Rect(139, 40, 166, 86), image.Rect(190, 10, 216, 62), image.Rect(222, 24, 246, 62),
	}
	pressLantern = image.Rect(262, 40, 284, 78)
)

// The panes: the sheet under the drying sheets' tops, and the bird's day
// under the window, how much of the parchment is parchment, and how tall
// a quest's line is on the day.
const (
	questRow = pixel.IconCells + 2
	sheetTop = 12
	dayTop   = 76
	paper    = 0.85
)

// MorningScreen is the sheet as a screen, moving: the pressroom at dawn,
// the sheets drying and the lantern burning, under the sheet and the day.
func MorningScreen(s Morning) ([]byte, error) {
	frames := morningFrames(s)
	return pixel.Animate(frames, beatsOf(len(frames)), screenScale)
}

// morningFrames are the pressroom's loop with the panes over each frame,
// the room showing through their parchment.
func morningFrames(s Morning) []*pixel.Canvas {
	art := pressArt()
	base := pixel.New(screenW, screenH)
	base.Picture(art.img, image.Point{}, 1)
	room := ambient(base,
		func(c *pixel.Canvas, f int) {
			for i, r := range drying {
				sway(c, art, r, f, i, false)
			}
		},
		func(c *pixel.Canvas, f int) { glow(c, art, pressLantern, f) },
	)
	out := make([]*pixel.Canvas, len(room))
	for i, r := range room {
		c := r.Copy()
		morningPanes(c, s)
		seeThrough(c, r, paper)
		out[i] = c
	}
	return out
}

// seeThrough lets room show through the parchment of c's panes: each
// pixel of parchment mixed with the room's under it, by parchment's share.
// Text, icons and the panes' frames stay as drawn.
func seeThrough(c, room *pixel.Canvas, by float64) {
	parchment := map[uint8]bool{}
	for _, i := range pixel.Sky {
		parchment[i] = true
	}
	// Mixed against one parchment, its bands being all but the same, so
	// the room's colours each take one ink and the palette holds.
	sheet := hexOf(c.Palette[pixel.Sky[len(pixel.Sky)/2]])
	inks := map[uint32]uint8{}
	for _, pane := range c.Panes {
		for y := pane.Min.Y; y < pane.Max.Y; y++ {
			for x := pane.Min.X; x < pane.Max.X; x++ {
				i := c.ColorIndexAt(x, y)
				if !parchment[i] {
					continue
				}
				under := hexOf(room.At(x, y))
				to, ok := inks[under]
				if !ok {
					to = c.Dye(blend(under, sheet, by))
					inks[under] = to
				}
				c.SetColorIndex(x, y, to)
			}
		}
	}
}

// morningCanvas is the panes alone, for checking the layout.
func morningCanvas(s Morning) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	morningPanes(c, s)
	return c
}

// morningPanes draws the sheet and the bird's day onto c.
func morningPanes(c *pixel.Canvas, s Morning) {
	// The broadsheet: the news in the serif a press sets, the rumour light,
	// as a sheet sets its column filler.
	sheet := image.Rect(4, sheetTop, 228, 212)
	c.Window(sheet)
	c.Tab(sheet, "the morning sheet")
	x, w := 14, 204
	y := sheet.Min.Y + 22
	label(c, x, y, s.Date)
	y += pixel.LineHeight
	news := s.News
	if news == "" {
		news = noNews
	}
	for _, l := range pixel.Serif.Wrap(news, w) {
		c.Print(pixel.Serif, x, y, l, pixel.Ink)
		y += pixel.Serif.Height
	}
	y += 4
	label(c, x, y, "overheard at mother tern's")
	y += pixel.LineHeight
	for _, l := range pixel.Light.Wrap(s.Rumour, w) {
		c.Print(pixel.Light, x, y, l, pixel.Ink)
		y += pixel.LineHeight
	}

	// Their own day: the quests dealt them, then notes.
	day := image.Rect(232, dayTop, 380, 212)
	c.Window(day)
	c.Tab(day, "your day")
	x, r := day.Min.X+20, day.Max.X-12
	y = day.Min.Y + 22
	tier := [3]uint8{pixel.Ink, pixel.Green, pixel.Gold}
	for i, q := range s.Quests {
		if i == 0 {
			c.Cursor(x-2, y+6)
		}
		// One line a quest, so the notes have room: its icon, what it asks
		// and, at the right, the reward in its tier's ink, as on the quests
		// screen. The pp are in the alt text.
		c.Icon(q.Icon, image.Pt(x, y-iconLift))
		at := rightIn(c, pixel.Regular, r, y, grouped(q.Reward/unit), tier[q.Tier])
		if len(q.Tasks) > 0 {
			t := q.Tasks[0]
			if len(q.Tasks) > 1 {
				t.What += "+"
			}
			c.FitIn(pixel.Light, x+pixel.IconCells+3, y, t.What, pixel.Ink, at-4-(x+pixel.IconCells+3))
		}
		y += questRow
	}
	if len(s.Quests) == 0 {
		label(c, x, y, "no notices yet")
		y += pixel.LineHeight
	}
	y += 4
	// Notes as far as the page goes: the quests come first.
	// Notes start at the pane's edge: the hand never points at them.
	x = day.Min.X + 10
	for _, n := range s.Notes {
		lines := pixel.Light.Wrap(n, r-x)
		if y+len(lines)*pixel.LineHeight > day.Max.Y-6 {
			break
		}
		for _, l := range lines {
			c.Print(pixel.Light, x, y, l, pixel.Ink)
			y += pixel.LineHeight
		}
		y += 3
	}
}

// morningMessage is the sheet as a message, with the way on to the pot.
func morningMessage(s Morning) (discord.MessageCreate, error) {
	pic, err := MorningScreen(s)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	news := s.News
	if news == "" {
		news = noNews
	}
	alt := []string{"the morning sheet, " + s.Date, "the news: " + news, "overheard: " + s.Rumour}
	for _, q := range s.Quests {
		alt = append(alt, fmt.Sprintf("a %s quest, %s pp", QuestTiers[q.Tier], grouped(q.Reward/unit)))
	}
	alt = append(alt, s.Notes...)
	desc := strings.Join(alt, ". ")
	if len(desc) > 1024 {
		desc = desc[:1024]
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "the morning sheet", "",
			discord.NewMediaGallery(screenItem(pic, desc)),
			discord.NewActionRow(withIcon(discord.NewPrimaryButton("to the pot", "pp:status"), "pot")))},
		Files:           []*discord.File{screenAttachment(pic, desc)},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}
