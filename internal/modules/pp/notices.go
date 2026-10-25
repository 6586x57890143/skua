package pp

import (
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/pixel"
)

// classMarks is how each class signs in company (LORE.md).
var classMarks = map[string]string{
	"wanderer": "a navigator's fix", "fighter": "a thumb of pale clay", "ranger": "a number",
	"sorcerer": "the brand on a quill hand", "thief": "a ring from the low town", "bard": "a bar of music",
	"monk": "a ring of hearth ash", "knight": "a name and a rung in order blue",
}

// pactName is what a pact is called.
func pactName(ring bool) string {
	if ring {
		return "round robin"
	}
	return "chirograph"
}

// Notice is a pact as the notices screen shows it: its signers' portraits
// and places, how far the time together has come and what it pays.
type Notice struct {
	Ring           bool
	Started        bool
	Signers        []NoticeSigner
	Places         int // how many may sign
	Together, Goal int64
	Pays           string
}

// NoticeSigner is one signature: who, their class's portrait and mark, and
// their minutes in the time together.
type NoticeSigner struct {
	Name     string
	Portrait image.Image
	Mark     string
	Minutes  int64
}

// Posted is a notice up on the board, waiting for signatures.
type Posted struct {
	Ring   bool
	By     string
	Places int // still open
}

// NoticesScreen draws the notices screen: the notice held, its signers
// lined up like a party, or the board when none is held, and the board's
// count along the foot, the hand at the open place or the first notice up.
func NoticesScreen(name string, held *Notice, board []Posted) ([]byte, error) {
	return noticesCanvas(name, held, board).Screen(screenScale)
}

// noticeSlot is one signer's column on the party window.
const noticeSlot = 92

func noticesCanvas(name string, held *Notice, board []Posted) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	top := image.Rect(4, 4, 380, 128)
	c.Window(top)
	if held == nil {
		c.Tab(top, "the board")
		tabName(c, top, name)
		postedRows(c, top, board)
	} else {
		c.Tab(top, pactName(held.Ring))
		tabName(c, top, name)
		party(c, top, held)
	}

	mid := image.Rect(4, 132, 380, 172)
	c.Window(mid)
	y := midLine(mid)
	if held != nil && !held.Started {
		// Before the work: what it's waiting for.
		c.Icon("hearth", image.Pt(16, y-iconLift))
		wait := "waiting for names"
		if held.Ring && len(held.Signers) >= 3 {
			wait = "three signed: its poster starts it, or a fourth fills it"
		}
		label(c, 16+pixel.IconCells+4, y, wait)
	} else if held != nil {
		c.Icon("hearth", image.Pt(16, y-iconLift))
		x := label(c, 16+pixel.IconCells+4, y, "together") + pixel.Advance
		c.Bar(image.Rect(x, y+4, x+80, y+9), held.Together, held.Goal, pixel.Blue)
		label(c, x+86, y, fmt.Sprintf("%d / %d min", min(held.Together, held.Goal), held.Goal))
		right(c, 368, y, held.Pays, pixel.Gold)
	} else {
		c.Icon("envelope", image.Pt(16, y-iconLift))
		label(c, 16+pixel.IconCells+4, y, "post one below, or sign one that's up")
	}

	foot := image.Rect(4, 176, 380, 212)
	c.Window(foot)
	y = midLine(foot)
	c.Icon("beads", image.Pt(16, y-iconLift))
	open := "nothing posted on the board"
	switch n := len(board); {
	case board == nil:
		// A channel post: what to do with it, not a board it doesn't show.
		open = "sign below to take a place; /pp shows the whole board"
	case n > 0:
		open = fmt.Sprintf("%d up on the board, waiting for names", n)
	}
	label(c, 16+pixel.IconCells+4, y, open)
	return c
}

// party lines up a notice's places: a framed portrait each, the name and
// minutes under it, an empty frame for a place still open, the hand at the
// first of those.
func party(c *pixel.Canvas, r image.Rectangle, n *Notice) {
	x0 := r.Min.X + (r.Dx()-n.Places*noticeSlot)/2
	for i := range n.Places {
		x := x0 + i*noticeSlot
		at := image.Pt(x+(noticeSlot-32)/2, r.Min.Y+pixel.TabDrop+10)
		box := image.Rectangle{at, at.Add(image.Pt(32, 48))}
		under := box.Max.Y + 6
		if i >= len(n.Signers) {
			frame := box.Inset(-3)
			c.Fill(frame, pixel.Shadow)
			c.Fill(frame.Inset(1), pixel.Bone)
			c.Fill(frame.Inset(2), pixel.Line)
			centred(c, x, under, "a place", pixel.Dim)
			if i == len(n.Signers) {
				c.Cursor(frame.Min.X-2, frame.Min.Y+frame.Dy()/2)
			}
			continue
		}
		s := n.Signers[i]
		if s.Portrait != nil {
			c.Portrait(s.Portrait, at, 1)
		}
		centred(c, x, under, s.Name, pixel.Ink)
		centred(c, x, under+13, fmt.Sprintf("%d min", s.Minutes), pixel.Dim)
	}
}

// centred sets s in the light face centred on a party slot from x.
func centred(c *pixel.Canvas, x, y int, s string, ink uint8) {
	s = fitted(pixel.Light, s, noticeSlot-6)
	c.Print(pixel.Light, x+(noticeSlot-pixel.Light.Width(s))/2, y, s, ink)
}

// postedRows lists what's up on the board: its kind, who posted it and the
// places still open.
func postedRows(c *pixel.Canvas, r image.Rectangle, board []Posted) {
	x, y := 34, paneTop(r)+4
	if len(board) == 0 {
		label(c, x, y, "a chirograph is two birds, a round robin three or four")
		label(c, x, y+16, "the work is time together in voice")
		return
	}
	for i, p := range board[:min(len(board), 5)] {
		icon := "envelope"
		if p.Ring {
			icon = "beads"
		}
		c.Icon(icon, image.Pt(x, y-iconLift))
		end := c.FitIn(pixel.Bold, x+pixel.IconCells+4, y, p.By, pixel.Ink, 150)
		label(c, end+pixel.Advance, y, pactName(p.Ring))
		places := "1 place"
		if p.Places != 1 {
			places = fmt.Sprintf("%d places", p.Places)
		}
		right(c, 368, y, places, pixel.Green)
		if i == 0 {
			c.Cursor(x-4, y+6)
		}
		y += 18
	}
}

// noticeOf is p as the screen shows it. s.mu held.
func (m *Module) noticeOf(s *server, p *pact) *Notice {
	b := m.st.bal()
	n := &Notice{Ring: p.ring, Started: p.started, Places: p.size(), Together: p.minutes, Goal: b.PactGoal,
		Pays: grouped(b.Joinder[0]/unit) + " pp each"}
	if p.ring {
		n.Pays = fmt.Sprintf("%s / %s pp each", grouped(b.Joinder[1]/unit), grouped(b.Joinder[2]/unit))
	}
	for _, g := range p.signers {
		sig := NoticeSigner{Name: "a bird", Mark: classMarks["wanderer"], Minutes: g.minutes, Portrait: Portrait(ClassOf("wanderer"))}
		if mem, ok := s.members[g.user]; ok {
			sig.Portrait, sig.Mark = Portrait(ClassOf(mem.class)), classMarks[ClassOf(mem.class).Key]
			if mem.name != "" {
				sig.Name = mem.name
			}
		}
		n.Signers = append(n.Signers, sig)
	}
	return n
}

// posted is every pact on s's board with a place open and its work not
// started, but the one user holds. s.mu held.
func posted(s *server, user snowflake.ID) (out []*pact) {
	for u, mem := range s.members {
		p := mem.pact
		if p != nil && p.opener == u && len(p.signers) < p.size() && !p.started && !p.signed(user) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b *pact) int { return int(a.opener - b.opener) })
	return out
}

// noticesMessage is user's notices screen with what they can do from it:
// sign one up on the board, post one, or put theirs up in the channel.
func (m *Module) noticesMessage(guild snowflake.ID, who *discord.ResolvedMember) (discord.MessageCreate, error) {
	user := who.User.ID
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, user, DayOf(m.now()))
	var held *Notice
	if mem.pact != nil {
		held = m.noticeOf(s, mem.pact)
	}
	up := posted(s, user)
	board := make([]Posted, len(up))
	opts := make([]discord.StringSelectMenuOption, 0, len(up))
	for i, p := range up {
		by := "a bird"
		if o := s.members[p.opener]; o.name != "" {
			by = o.name
		}
		board[i] = Posted{Ring: p.ring, By: by, Places: p.size() - len(p.signers)}
		if i < 25 {
			opts = append(opts, discord.NewStringSelectMenuOption(clip(by+"'s "+pactName(p.ring), 100), p.key()))
		}
	}
	canPost := m.st.mayPost(mem, user, DayOf(m.now())) == nil
	canSign := m.st.mayPact(mem, user, DayOf(m.now())) == nil
	startable := mem.pact != nil && mem.pact.opener == user && mem.pact.startable()
	alt := m.noticesAlt(held, board)
	s.mu.Unlock()

	pic, err := NoticesScreen(pixelName(who), held, board)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	extra := []discord.ContainerSubComponent{
		discord.NewMediaGallery(screenItem(pic, clip(alt, altLimit))),
	}
	if len(opts) > 0 && canSign {
		extra = append(extra, discord.NewActionRow(discord.NewStringSelectMenu("pp:signpick", "sign a notice on the board", opts...)))
	}
	post := func(label, id, icon string) discord.InteractiveComponent {
		b := withIcon(discord.NewSecondaryButton(label, id), icon)
		if !canPost {
			b = b.AsDisabled()
		}
		return b
	}
	row := discord.NewActionRow(post("post a chirograph", "pp:pact:2", "envelope"), post("post a round robin", "pp:pact:4", "beads"))
	if held != nil && len(held.Signers) < held.Places && !held.Started {
		row = row.AddComponents(withIcon(discord.NewSecondaryButton("put it up here", "pp:rally"), "bell"))
	}
	if startable {
		row = row.AddComponents(withIcon(discord.NewSuccessButton("start the ring", "pp:start"), "beads"))
	}
	if held != nil {
		// Withdrawing as the city does it: a chirograph's half is torn up,
		// a name is struck from a ring.
		leave := "tear up your half"
		if held.Ring {
			leave = "strike your name"
		}
		row = row.AddComponents(discord.NewDangerButton(leave, "pp:withdraw"))
	}
	extra = append(extra, row)
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "notices", "", extra...)},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// noticesAlt is the notices screen as text, for a reader that can't see
// it.
func (m *Module) noticesAlt(held *Notice, board []Posted) string {
	var parts []string
	if held != nil {
		var names []string
		for _, g := range held.Signers {
			names = append(names, g.Name+" with "+g.Mark)
		}
		state := fmt.Sprintf("%d of %d minutes together", min(held.Together, held.Goal), held.Goal)
		if !held.Started {
			state = fmt.Sprintf("waiting to start, %d of %d places signed", len(held.Signers), held.Places)
			if held.Ring && len(held.Signers) >= 3 {
				state += ", for its poster to start it or a fourth to fill it"
			}
		}
		parts = append(parts, fmt.Sprintf("your %s, signed by %s, %s, pays %s",
			pactName(held.Ring), andList(names), state, held.Pays))
	}
	if board == nil {
		// A channel post carries no board.
		return strings.Join(parts, ". ")
	}
	var up []string
	for _, p := range board {
		up = append(up, fmt.Sprintf("%s's %s with %d open", p.By, pactName(p.Ring), p.Places))
	}
	if len(up) > 0 {
		parts = append(parts, "on the board: "+strings.Join(up, ", "))
	} else {
		parts = append(parts, "nothing on the board")
	}
	return strings.Join(parts, ". ")
}

// postView is what a pact's channel post shows, taken under the lock so the
// picture can be drawn after it.
type postView struct {
	notice              *Notice
	name                string
	key                 string
	signable, startable bool
}

// viewPost is p as its post shows it. s.mu held.
func (m *Module) viewPost(s *server, p *pact) postView {
	v := postView{notice: m.noticeOf(s, p), name: "a bird", key: p.key(), signable: len(p.signers) < p.size() && !p.started, startable: p.startable()}
	if o := s.members[p.opener]; o != nil && o.name != "" {
		v.name = o.name
	}
	return v
}

// postMessage is a pact's own screen posted for the channel, with a sign
// button while it has a place open and its work hasn't started. It draws
// the picture, so it runs outside the lock.
func (m *Module) postMessage(v postView) (discord.MessageCreate, error) {
	alt := m.noticesAlt(v.notice, nil)
	pic, err := NoticesScreen(v.name, v.notice, nil)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	sign := withIcon(discord.NewPrimaryButton("sign", "pp:sign:"+v.key), "quill")
	if !v.signable {
		sign = sign.AsDisabled()
	}
	row := discord.NewActionRow(sign)
	if v.startable {
		row = row.AddComponents(withIcon(discord.NewSuccessButton("start the ring", "pp:start:"+v.key), "beads"))
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", pactName(v.notice.Ring), "",
			discord.NewMediaGallery(screenItem(pic, clip(alt, altLimit))),
			row)},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// postPact posts a new chirograph, or a round robin if ring, and puts it up
// in the channel for others to sign.
func (m *Module) postPact(guild snowflake.ID, e *events.ComponentInteractionCreate, ring bool) error {
	s := m.st.server(guild)
	s.mu.Lock()
	p, err := m.st.post(s, guild, e.User().ID, ring, DayOf(m.now()))
	if err != nil {
		s.mu.Unlock()
		return err
	}
	p.rallied, p.channel = m.now(), e.Channel().ID()
	v := m.viewPost(s, p)
	s.mu.Unlock()
	msg, err := m.postMessage(v)
	if err != nil {
		return err
	}
	return e.CreateMessage(msg)
}

// rallyWait is how long a notice waits before it can be put up again.
const rallyWait = 10 * time.Minute

// rally puts the pact user holds up in the channel again, while it has a
// place open, once every rallyWait.
func (m *Module) rally(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	s := m.st.server(guild)
	s.mu.Lock()
	mem, ok := s.members[e.User().ID]
	if !ok || mem.pact == nil || len(mem.pact.signers) >= mem.pact.size() || mem.pact.started {
		s.mu.Unlock()
		return errPactGone
	}
	now := m.now()
	if now.Sub(mem.pact.rallied) < rallyWait {
		s.mu.Unlock()
		return errPactRally
	}
	mem.pact.rallied, mem.pact.channel = now, e.Channel().ID()
	m.st.touchPact(s, mem.pact)
	v := m.viewPost(s, mem.pact)
	s.mu.Unlock()
	msg, err := m.postMessage(v)
	if err != nil {
		return err
	}
	return e.CreateMessage(msg)
}

// signPact signs the pact on the post pressed, or the one picked from the
// board, and shows it again where it was pressed. A post pressed after its
// notice filled or started, from the board or elsewhere, is redrawn as it
// stands now.
func (m *Module) signPact(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	id := strings.TrimPrefix(e.Data.CustomID(), "pp:sign:")
	picked := e.Data.CustomID() == "pp:signpick"
	if picked {
		data, ok := e.Data.(discord.StringSelectMenuInteractionData)
		if !ok || len(data.Values) == 0 {
			return errPactGone
		}
		id = data.Values[0]
	}
	k, ok := parseKey(id)
	if !ok {
		return errPactGone
	}
	s := m.st.server(guild)
	s.mu.Lock()
	p, err := m.st.sign(s, guild, k, e.User().ID, DayOf(m.now()))
	if !picked && (errors.Is(err, errPactFull) || errors.Is(err, errPactActive)) {
		p, err = pactOf(s, k), nil
	}
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if picked {
		s.mu.Unlock()
		msg, err := m.noticesMessage(guild, e.Member())
		if err != nil {
			return err
		}
		return m.answer(guild, e, msg, "pp:notices")
	}
	v := m.viewPost(s, p)
	s.mu.Unlock()
	msg, err := m.postMessage(v)
	if err != nil {
		return err
	}
	return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...))
}

// withdrawPact takes the member's name off the notice they hold and shows
// their notices again.
func (m *Module) withdrawPact(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	s := m.st.server(guild)
	s.mu.Lock()
	err := m.st.withdraw(s, e.User().ID, DayOf(m.now()))
	s.mu.Unlock()
	if err != nil {
		return err
	}
	msg, err := m.noticesMessage(guild, e.Member())
	if err != nil {
		return err
	}
	return m.answer(guild, e, msg, "pp:notices")
}

// startPact starts the round robin its poster pressed start on: from their
// notices, shown again there, or from its post, redrawn.
func (m *Module) startPact(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	s := m.st.server(guild)
	s.mu.Lock()
	onPost := e.Data.CustomID() != "pp:start"
	var p *pact
	if onPost {
		// The post names its notice: one gone is gone, and one already
		// started is only redrawn.
		k, ok := parseKey(strings.TrimPrefix(e.Data.CustomID(), "pp:start:"))
		if p = pactOf(s, k); !ok || p == nil {
			s.mu.Unlock()
			return errPactGone
		}
		if p.started {
			v := m.viewPost(s, p)
			s.mu.Unlock()
			msg, err := m.postMessage(v)
			if err != nil {
				return err
			}
			return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...))
		}
		if p.opener != e.User().ID {
			s.mu.Unlock()
			return errPactStart
		}
	}
	p, err := m.st.start(s, e.User().ID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if !onPost {
		s.mu.Unlock()
		msg, err := m.noticesMessage(guild, e.Member())
		if err != nil {
			return err
		}
		return m.answer(guild, e, msg, "pp:notices")
	}
	v := m.viewPost(s, p)
	s.mu.Unlock()
	msg, err := m.postMessage(v)
	if err != nil {
		return err
	}
	return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...))
}
