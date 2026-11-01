package pp

import (
	"fmt"
	"image"
	"strings"

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
	if mem.dispatchDay == today || mem.classSeason == -1 {
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
			l.Tasks = append(l.Tasks, TaskLine{What: shortTask(t, false), Progress: min(q.prog[j], t.Goal), Goal: t.Goal})
		}
		sh.Quests = append(sh.Quests, l)
	}
	return sh, true
}

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
	if mem.livery != "" && mem.liveryUntil == today {
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

// sheetBand is the piece of the pot square over the bird's day: the rim
// where the singers sing the sheet, the board on it and the steam going
// up, at dawn. sheetPic is where it's set, framed, at the top of the day's
// column.
var (
	sheetBand = image.Rect(124, 50, 284, 86)
	sheetPic  = image.Rectangle{image.Pt(220, 4), image.Pt(220, 4).Add(sheetBand.Size())}
)

// MorningScreen is the sheet as a screen, moving: the pot square at dawn
// steaming over the bird's day, beside the sheet.
func MorningScreen(s Morning) ([]byte, error) {
	frames := morningFrames(s)
	return pixel.Animate(frames, beatsOf(len(frames)), screenScale)
}

// morningFrames are the sheet with each frame of the pot square's loop
// set in its frame.
func morningFrames(s Morning) []*pixel.Canvas {
	c := morningCanvas(s)
	scene := potScene(pixel.New(sumpSize.X, sumpSize.Y), dawn)
	out := make([]*pixel.Canvas, len(scene))
	for i, f := range scene {
		out[i] = c.Copy()
		out[i].Picture(f.SubImage(sheetBand), sheetPic.Min, 1)
	}
	return out
}

func morningCanvas(s Morning) *pixel.Canvas {
	c := pixel.New(screenW, screenH)

	// The broadsheet: the news in the serif a press sets, the rumour light,
	// as a sheet sets its column filler.
	sheet := image.Rect(4, 4, 216, 212)
	c.Window(sheet)
	c.Tab(sheet, "the morning sheet")
	x, w := 14, 192
	y := 26
	label(c, x, y, s.Date)
	y += pixel.LineHeight + 2
	news := s.News
	if news == "" {
		news = noNews
	}
	for _, l := range pixel.Serif.Wrap(news, w) {
		c.Print(pixel.Serif, x, y, l, pixel.Ink)
		y += pixel.Serif.Height
	}
	y += 6
	label(c, x, y, "overheard at mother tern's")
	y += pixel.LineHeight
	for _, l := range pixel.Light.Wrap(s.Rumour, w) {
		c.Print(pixel.Light, x, y, l, pixel.Ink)
		y += pixel.LineHeight
	}

	// Their own day: the quests dealt them, then notes.
	c.Outline(sheetPic.Inset(-1), pixel.Line)
	day := image.Rect(220, sheetPic.Max.Y+4, 380, 212)
	c.Window(day)
	c.Tab(day, "your day")
	x, r := 240, 368
	y = day.Min.Y + 22
	tier := [3]uint8{pixel.Ink, pixel.Green, pixel.Gold}
	for i, q := range s.Quests {
		if i == 0 {
			c.Cursor(x-2, y+6)
		}
		c.Icon(q.Icon, image.Pt(x, y-iconLift))
		// The tier is the reward's ink, as on the quests screen.
		c.Print(pixel.Bold, x+pixel.IconCells+3, y, grouped(q.Reward/unit)+" pp", tier[q.Tier])
		y += questHead
		if len(q.Tasks) > 0 {
			t := q.Tasks[0]
			if len(q.Tasks) > 1 {
				t.What += fmt.Sprintf(" +%d", len(q.Tasks)-1)
			}
			c.FitIn(pixel.Light, x+pixel.IconCells+3, y, t.What, pixel.Dim, r-x-pixel.IconCells-3)
			y += questTask
		}
		y += questGap
	}
	if len(s.Quests) == 0 {
		label(c, x, y, "no notices yet")
		y += pixel.LineHeight
	}
	y += 4
	// Notes as far as the page goes: the quests come first.
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
	return c
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
