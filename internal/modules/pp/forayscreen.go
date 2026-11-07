package pp

import (
	"context"
	"embed"
	"fmt"
	"image"
	"strconv"
	"strings"
	"sync"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/pixel"
)

// The north gate and the places beyond it, as screens: the gate where a
// bird is written out, and the mire and the last wood, where its day is
// read. Each is a ChatGPT painting fitted to the whole screen at 40
// colours, in the four lights the other places have, the same picture to
// the pixel, its lamps, kilns and fires alive (windows). Text sits on
// parchment over the painting's quiet part: the gate's foreground, the
// places' left third. Replace a painting whole, framed the same.
//
//go:embed gate*.png mire*.png wood*.png
var forayFS embed.FS

// forayArts are each scene's paintings by light, read once each.
var forayArts = func() map[string][4]func() *sumpArt {
	out := map[string][4]func() *sumpArt{}
	for _, name := range []string{"gate", "mire", "wood"} {
		var arts [4]func() *sumpArt
		for light := range arts {
			arts[light] = sync.OnceValue(func() *sumpArt {
				file := name + lightNames[light]
				b, err := forayFS.ReadFile(file + ".png")
				if err != nil {
					panic("pp: no painting " + file)
				}
				return &sumpArt{img: decodeArt(b, file), shade: lightShade[light], mist: lightMist[light]}
			})
		}
		out[name] = arts
	}
	return out
}()

// forayPaper is how much of a foray pane is parchment and how much the
// painting under it: less than the morning sheet's, so the place reads
// through the page while its text stays clear.
const forayPaper = 0.62

// The panes: the gate's over its foreground and the left fork, a place's
// down its quiet left third.
var (
	gatePane  = image.Rect(4, 100, 236, 212)
	placePane = image.Rect(4, 4, 176, 212)
)

// ForayView is one foray screen: a scene in a light, the pane's tab, its
// lines already set to the pane's width, and a foot line under them.
type ForayView struct {
	Scene string
	Light int
	Tab   string
	Lines []string
	Foot  string
}

// ForayScreen draws v, moving.
func ForayScreen(v ForayView) ([]byte, error) {
	frames := forayFrames(v)
	return pixel.Animate(frames, beatsOf(len(frames)), screenScale)
}

// forayFrames are the scene's loop with the pane over each frame, the
// scene showing through its parchment.
func forayFrames(v ForayView) []*pixel.Canvas {
	art := forayArts[v.Scene][v.Light]()
	base := pixel.New(screenW, screenH)
	base.Picture(art.img, image.Point{}, 1)
	scene := ambient(base, func(c *pixel.Canvas, f int) { windows(c, art.img, base.Rect, f) })
	out := make([]*pixel.Canvas, len(scene))
	for i, r := range scene {
		c := r.Copy()
		forayPane(c, v)
		seeThrough(c, r, forayPaper)
		out[i] = c
	}
	return out
}

// forayCanvas is the pane alone, for checking the layout.
func forayCanvas(v ForayView) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	forayPane(c, v)
	return c
}

// paneOf is the pane a scene's text sits in.
func paneOf(scene string) image.Rectangle {
	if scene == "gate" {
		return gatePane
	}
	return placePane
}

// forayPane draws v's pane onto c.
func forayPane(c *pixel.Canvas, v ForayView) {
	r := paneOf(v.Scene)
	c.Window(r)
	c.Tab(r, v.Tab)
	x, y := r.Min.X+10, r.Min.Y+pixel.TabDrop+3
	for _, l := range v.Lines {
		c.Print(pixel.Light, x, y, l, pixel.Ink)
		y += pixel.LineHeight
	}
	if v.Foot != "" {
		label(c, x, footY(r), v.Foot)
	}
}

// footY is where a pane's foot line sits, clear of its frame.
func footY(r image.Rectangle) int { return r.Max.Y - pixel.LineHeight - 6 }

// paneRows is how many lines of text a scene's pane holds above its foot.
func paneRows(scene string) int {
	r := paneOf(scene)
	return (footY(r) - 2 - (r.Min.Y + pixel.TabDrop + 3)) / pixel.LineHeight
}

// setLines is text set to scene's pane: wrapped, a blank line between
// paragraphs, cut into the pages the pane holds.
func setLines(text, scene string) [][]string {
	w := paneOf(scene).Dx() - 20
	var lines []string
	for i, para := range strings.Split(text, "\n\n") {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, pixel.Light.Wrap(para, w)...)
	}
	rows := paneRows(scene)
	var pages [][]string
	for len(lines) > 0 {
		n := min(rows, len(lines))
		page := lines[:n]
		lines = lines[n:]
		for len(lines) > 0 && lines[0] == "" {
			lines = lines[1:]
		}
		pages = append(pages, page)
	}
	if len(pages) == 0 {
		pages = [][]string{nil}
	}
	return pages
}

var (
	errGateShut = core.Tell("the north gate isn't open here yet")
)

// gateSees is whether a press may use the gate: once an admin has opened
// it, everyone, and admins before.
func (m *Module) gateSees(s *server, e *events.ComponentInteractionCreate) bool {
	return s.gateOpen || m.Admin != nil && m.Admin(e)
}

// gate answers every press at the north gate: the gate itself, choosing
// where to go, leaning and setting out, reading the day, the out pages and
// putting peat on the fire. The screens move, so it's answered at once and
// drawn after.
func (m *Module) gate(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	id := pressed(e)
	user := e.User().ID
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	if !m.gateSees(s, e) {
		s.mu.Unlock()
		return errGateShut
	}
	where, far, view, page := -1, false, "gate", 0
	var err error
	switch {
	case id == "pp:fwhere":
		data, ok := e.Data.(discord.StringSelectMenuInteractionData)
		if !ok || len(data.Values) != 1 {
			err = errForayWhere
			break
		}
		where, far, err = parseWhere(data.Values[0])
	case strings.HasPrefix(id, "pp:fout:"):
		var lean int
		where, far, lean, err = parseOut(id)
		if err == nil {
			err = m.st.setOut(s, user, where, far, Wind(lean), today)
		}
		where = -1
	case id == "pp:peat":
		err = m.st.feedPeat(s, user, s.forayOf(user).peat, today)
	case strings.HasPrefix(id, "pp:fread:"):
		view = "read"
		page, _ = strconv.Atoi(strings.TrimPrefix(id, "pp:fread:"))
	case strings.HasPrefix(id, "pp:book:"):
		view = "book"
		page, _ = strconv.Atoi(strings.TrimPrefix(id, "pp:book:"))
	}
	if err != nil {
		s.mu.Unlock()
		return err
	}
	build := m.gateView(s, user, view, page, where, far, today)
	s.mu.Unlock()
	return m.answerSlow(guild, e, "pp:gate", build)
}

// parseWhere reads a choice of road, place:far.
func parseWhere(v string) (place int, far bool, err error) {
	p, f, ok := strings.Cut(v, ":")
	place, err1 := strconv.Atoi(p)
	if !ok || err1 != nil || place < 0 || place >= len(places) || (f != "0" && f != "1") {
		return 0, false, errForayWhere
	}
	return place, f == "1", nil
}

// parseOut reads a press to set out, pp:fout:<place>:<far>:<lean>.
func parseOut(id string) (place int, far bool, lean int, err error) {
	parts := strings.Split(strings.TrimPrefix(id, "pp:fout:"), ":")
	if len(parts) != 3 {
		return 0, false, 0, errForayWhere
	}
	place, far, err = parseWhere(parts[0] + ":" + parts[1])
	lean, err2 := strconv.Atoi(parts[2])
	if err == nil && (err2 != nil || lean < int(Writ) || lean > int(Fold)) {
		err = errForayWhere
	}
	return place, far, lean, err
}

// reachName is a reach as the gate says it.
func reachName(far bool) string {
	if far {
		return "the far reaches"
	}
	return "the near reaches"
}

// storyOf is user's day out o as told so far, as paragraphs: the road out,
// each step told with its throw, and once the bird is back the road home,
// the ending and what it carried. s.mu held.
func (st *state) storyOf(s *server, user snowflake.ID, o *outing) string {
	d := walk(st.walker(s, user, o))
	n, back := toldOf(len(d.Steps), o.quiet, o.need)
	reach := 0
	if o.far {
		reach = 1
	}
	paras := []string{d.Place.Out[reach]}
	for _, step := range d.Steps[:n] {
		text := step.Event.Text
		if line := step.Event.Class[o.class]; line != "" {
			text += " " + line
		}
		paras = append(paras, text)
		try := "you " + step.Option.Try
		if step.Option.Need > 0 {
			word := "and it goes your way"
			if !step.Passed {
				word = "and it doesn't"
			}
			try = fmt.Sprintf("%s. the bones say %d against %d, %s", try, step.Roll+step.Bonus, step.Option.Need, word)
		}
		paras = append(paras, try+".", step.Out.Text)
	}
	if !back {
		paras = append(paras, "the rest of the day is still on the road.")
	} else {
		paras = append(paras, d.Place.In[reach], d.Ending.Title+".", d.Ending.Text)
		if d.Finds > 0 {
			paras = append(paras, fmt.Sprintf("you carried back %d %s.", d.Finds, d.Place.Finds))
		}
	}
	return strings.ReplaceAll(strings.Join(paras, "\n\n"), "{name}", o.partner)
}

// leanLines are the five winds a bird can lean into, as the screen for
// choosing one sets them over scene: the question, then a wind a line or
// two, all on the one screen (TestLeanFits).
func leanLines(scene string) []string {
	w := paneOf(scene).Dx() - 20
	lines := append(pixel.Light.Wrap("which way does your bird lean?", w), "")
	for _, wd := range winds {
		lines = append(lines, pixel.Light.Wrap(wd.Key+": "+wd.Line, w)...)
	}
	return lines
}

// leaf is one screen's worth of the book: a scene and the lines on it.
type leaf struct {
	scene, tab string
	lines      []string
}

// bookOf is user's out pages, every page they've found in the order the
// places and their decks keep them, set into screens. s.mu held.
func bookOf(f *forays) []leaf {
	var out []leaf
	for _, p := range places {
		for _, e := range p.Events {
			if !f.pages[p.Key+"/"+e.Key] {
				continue
			}
			for _, ls := range setLines(pages[p.Key][e.Key], p.Key) {
				out = append(out, leaf{p.Key, p.Name, ls})
			}
		}
	}
	return out
}

// gateView is what a press at the gate shows: the gate with where the bird
// stands, a place to lean into on the way out, the day read a screen at a
// time, or the out pages, counted as gate_view's a of 0, 1, 2 and 3 in the
// order they're told here. Gathered under s.mu and drawn after. s.mu held.
func (m *Module) gateView(s *server, user snowflake.ID, view string, page, where int, far bool, today int64) func() (discord.MessageCreate, error) {
	f := s.forayOf(user)
	light := lightAt(m.now())
	v := ForayView{Scene: "gate", Light: light, Tab: "the north gate"}
	var rows []discord.ContainerSubComponent
	turn := func(id func(int) string, i, n int) {
		if n < 2 {
			return
		}
		rows = append(rows, discord.NewActionRow(
			discord.NewSecondaryButton("◂", id(max(i-1, 0))).WithDisabled(i == 0),
			discord.NewSecondaryButton(fmt.Sprintf("%d / %d", i+1, n), "pp:fpage").AsDisabled(),
			discord.NewSecondaryButton("▸", id(min(i+1, n-1))).WithDisabled(i == n-1)))
	}
	back := discord.NewActionRow(discord.NewSecondaryButton("◂ the gate", "pp:gate:back"))
	alt := "the north gate"
	seen := 0
	switch {
	case view == "read" && f.out != nil:
		seen = 2
		o := f.out
		p := places[o.place]
		leaves := setLines(m.st.storyOf(s, user, o), p.Key)
		page = min(max(page, 0), len(leaves)-1)
		v = ForayView{Scene: p.Key, Light: light, Tab: p.Name, Lines: leaves[page], Foot: reachName(o.far)}
		turn(func(i int) string { return "pp:fread:" + strconv.Itoa(i) }, page, len(leaves))
		rows = append(rows, back)
		alt = "your bird's day out at " + p.Name
	case view == "book" && len(f.pages) > 0:
		seen = 3
		leaves := bookOf(f)
		page = min(max(page, 0), len(leaves)-1)
		l := leaves[page]
		v = ForayView{Scene: l.scene, Light: light, Tab: "the out pages", Lines: l.lines, Foot: l.tab}
		turn(func(i int) string { return "pp:book:" + strconv.Itoa(i) }, page, len(leaves))
		rows = append(rows, back)
		alt = "the out pages, at " + l.tab
	case where >= 0:
		seen = 1
		p := places[where]
		v = ForayView{Scene: p.Key, Light: light, Tab: p.Name, Lines: leanLines(p.Key), Foot: reachName(far)}
		var leans []discord.InteractiveComponent
		for w := Writ; w <= Fold; w++ {
			leans = append(leans, withIcon(discord.NewSecondaryButton(w.String(), fmt.Sprintf("pp:fout:%d:%s:%d", where, map[bool]string{false: "0", true: "1"}[far], w)), "wind_"+w.String()))
		}
		rows = append(rows, discord.NewActionRow(leans[:3]...), discord.NewActionRow(leans[3:]...), back)
		alt = "leaning into a wind for " + p.Name
	default:
		v.Lines = setLines(m.st.gateWords(s, user, today), "gate")[0]
		home := f.out == nil || f.out.back
		if home && f.lastOut != today {
			var opts []discord.StringSelectMenuOption
			for i, p := range places {
				for _, fr := range []bool{false, true} {
					opts = append(opts, discord.NewStringSelectMenuOption(p.Name+", "+reachName(fr), fmt.Sprintf("%d:%s", i, map[bool]string{false: "0", true: "1"}[fr])))
				}
			}
			rows = append(rows, discord.NewActionRow(discord.NewStringSelectMenu("pp:fwhere", "where your bird goes today", opts...)))
		}
		var act []discord.InteractiveComponent
		if f.out != nil {
			label := "the day so far"
			if f.out.back {
				label = "read the day"
			}
			act = append(act, discord.NewPrimaryButton(label, "pp:fread:0"))
		}
		if f.peat > 0 {
			act = append(act, withIcon(discord.NewSecondaryButton(fmt.Sprintf("put %d peat on the fire", f.peat), "pp:peat"), "peat"))
		}
		if len(f.pages) > 0 {
			act = append(act, discord.NewSecondaryButton("the out pages", "pp:book:0"))
		}
		if len(act) > 0 {
			rows = append(rows, discord.NewActionRow(act...))
		}
		// Only an admin reaches a shut gate: the switch that opens it to
		// everyone is here, where they'd look for it.
		if !s.gateOpen {
			rows = append(rows, discord.NewActionRow(withIcon(discord.NewPrimaryButton("open the gate to everyone", "pp:opengate"), "gate")))
		}
	}
	s.count(today, statGateView, seen, page)
	return func() (discord.MessageCreate, error) {
		pic, err := ForayScreen(v)
		if err != nil {
			return discord.MessageCreate{}, err
		}
		extra := append([]discord.ContainerSubComponent{discord.NewMediaGallery(screenItem(pic, alt))}, rows...)
		return discord.MessageCreate{
			Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "the north gate", "", extra...)},
			Files:           []*discord.File{screenAttachment(pic, alt)},
			Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
			AllowedMentions: core.NoPings(),
		}, nil
	}
}

// gateWords is what the warden tells user at the gate: where their bird
// is, what they carry and how far their book has come. s.mu held.
func (st *state) gateWords(s *server, user snowflake.ID, today int64) string {
	f := s.forayOf(user)
	var paras []string
	switch o := f.out; {
	case o != nil && !o.back:
		way := "it has only just set out"
		switch {
		case o.quiet*3 >= o.need*2:
			way = "it's two thirds of the way"
		case o.quiet*3 >= o.need:
			way = "it's a third of the way"
		}
		paras = append(paras, fmt.Sprintf("your bird is out in %s, %s, and %s. it walks only while the pot isn't paying you.", places[o.place].Name, reachName(o.far), way))
	case o != nil && f.lastOut == today:
		paras = append(paras, fmt.Sprintf("your bird is back from %s. the warden will write you out again after the turn.", places[o.place].Name))
	default:
		paras = append(paras, "the warden asks where you're going, how far and which way you lean, and writes it in the out pages.")
	}
	if f.peat > 0 || f.amber > 0 {
		paras = append(paras, fmt.Sprintf("you carry %d peat and %d beads of amber.", f.peat, f.amber))
	}
	total, ends := 0, 0
	for _, p := range places {
		total += len(p.Events)
		ends += len(p.Endings)
	}
	paras = append(paras, fmt.Sprintf("your out pages: %d of %d, and %d of %d endings.", len(f.pages), total, len(f.endings), ends))
	return strings.Join(paras, " ")
}

// openGate opens the gate to everyone, for good.
func (m *Module) openGate(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errGateShut
	}
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, gate_open) values ($1, true)
		on conflict (guild_id) do update set gate_open = true`, int64(guild)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	s.gateOpen = true
	s.mu.Unlock()
	return e.CreateMessage(discord.MessageCreate{Content: "the north gate is open: every bird can go out from /pp now",
		Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}

// forayNote is the morning sheet's line on user's bird, "" for none.
// s.mu held.
func forayNote(s *server, user snowflake.ID) string {
	f, ok := s.forays[user]
	if !ok || f.out == nil || f.out.back {
		return ""
	}
	return "your bird is out in " + places[f.out.place].Name
}
