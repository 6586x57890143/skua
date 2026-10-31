package pp

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/pixel"
)

// The slack tide as members see it: its screen in /pp, which opens a
// table in the house channel; the table, one message edited as birds sit
// and again when the bones are thrown; and the bell's one message, edited
// through the drowning hour.

// setHouse keeps the house channel an admin picked.
func (m *Module) setHouse(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	data, ok := e.Data.(discord.ChannelSelectMenuInteractionData)
	if !ok {
		return errNotAdmin
	}
	var ch snowflake.ID
	if len(data.Values) > 0 {
		ch = data.Values[0]
	}
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, house_channel) values ($1, $2)
		on conflict (guild_id) do update set house_channel = excluded.house_channel`, int64(guild), int64(ch)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	s.houseCh = ch
	s.mu.Unlock()
	said := "the slack tide's shut: no tables, no bell"
	if ch != 0 {
		said = fmt.Sprintf("the slack tide posts its tables and its bell in <#%s>", ch)
	}
	return e.CreateMessage(discord.NewMessageCreate().WithContent(said).WithEphemeral(true).WithAllowedMentions(core.NoPings()))
}

// House is what the slack tide's screen shows: the hold against its
// line, the stakes the purser takes, and the bird's own day at the tables.
type House struct {
	Hold, Line      int64
	MaxStake        int64
	Staked, Cap     int64
	Bell            bool
	Open            bool // a table's open now
	Shut            bool // no house channel
	Beam            string
	BeamThrow       int
	Throws, Throwrs int64
}

// tideMessage is the slack tide's screen for user.
func (m *Module) tideMessage(guild, user snowflake.ID) (discord.MessageCreate, error) {
	now := m.now()
	_, bell := m.st.ringing(now)
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, user, DayOf(now))
	h := House{Hold: s.hold, Line: houseLine(), MaxStake: s.maxStake(), Staked: mem.staked, Cap: m.st.stakeCap(),
		Bell: bell, Open: s.table != nil && !s.table.settled, Shut: s.houseCh == 0}
	if !s.noted {
		h.Hold, h.MaxStake = Rules.HouseNote*unit, Rules.HouseNote*unit*Rules.HouseStake/10000
	}
	ch := s.houseCh
	s.mu.Unlock()
	pic, err := HouseScreen(h)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := fmt.Sprintf("the slack tide: the hold at %s of its %s pp line, %s", grouped(h.Hold/unit), grouped(h.Line/unit), tideWord(h))
	lines := []string{"-# throw the bones against the purser's hold. a table seats six and every bird throws against the house, never each other"}
	switch {
	case h.Shut:
		lines = append(lines, "-# the house is shut here until an admin picks its channel on /help")
	case h.Bell:
		lines = append(lines, "-# the bell's ringing under the gannet: a hundred pays fifteen times")
	}
	btn := withIcon(discord.NewPrimaryButton("open a table", "pp:table"), "bell")
	if h.Shut {
		btn = btn.AsDisabled()
	} else if h.Open {
		btn = withIcon(discord.NewLinkButton("go to the table", fmt.Sprintf("https://discord.com/channels/%s/%s", guild, ch)), "bell")
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "the slack tide", strings.Join(lines, "\n"),
			discord.NewMediaGallery(screenItem(pic, alt)), discord.NewActionRow(btn))},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

func tideWord(h House) string {
	if h.Hold >= h.Line {
		return "the tide's in"
	}
	return "the tide's out"
}

// openTable opens a table in the house channel and says where.
func (m *Module) openTable(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	now := m.now()
	_, bell := m.st.ringing(now)
	s := m.st.server(guild)
	s.mu.Lock()
	t, err := m.st.open(s, s.houseCh, now, bell)
	var msg discord.MessageCreate
	if err == nil {
		msg, err = m.tableMessage(s, t)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if m.rest == nil {
		return e.CreateMessage(discord.NewMessageCreate().WithContent("the table's set").WithEphemeral(true))
	}
	var posted *discord.Message
	if err := m.write(guild, guard.MessageSend, func() (err error) {
		posted, err = m.rest.CreateMessage(t.channel, msg, rest.WithCtx(ctx))
		return err
	}); err != nil {
		s.mu.Lock()
		if s.table == t && len(t.seats) == 0 {
			s.table = nil
		}
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	t.msg = posted.ID
	s.mu.Unlock()
	return e.CreateMessage(discord.NewMessageCreate().
		WithContent(fmt.Sprintf("the purser's set a table in <#%s>: sit down before the glass runs out", t.channel)).
		WithEphemeral(true).WithAllowedMentions(core.NoPings()))
}

// sitAt is a press on a table's stake: it seats the bird and redraws the
// table, and throws the bones once its benches are full.
func (m *Module) sitAt(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	// pp:sit:<table>:<stake>
	parts := strings.Split(e.Data.CustomID(), ":")
	if len(parts) != 4 {
		return errTableShut
	}
	id, err1 := strconv.ParseInt(parts[2], 10, 64)
	k, err2 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || k < 0 || k >= len(stakes) {
		return errTableShut
	}
	now := m.now()
	s := m.st.server(guild)
	s.mu.Lock()
	t := s.table
	if t == nil || t.id != id {
		s.mu.Unlock()
		return errTableShut
	}
	first := t.first.IsZero()
	if err := m.st.sitDown(s, t, guild, e.User().ID, e.Member().EffectiveName(), stakes[k].pp, now); err != nil {
		s.mu.Unlock()
		return err
	}
	full := len(t.seats) >= tableSeats
	msg, err := m.tableMessage(s, t)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	switch {
	case full:
		m.later(context.Background(), func(ctx context.Context) { m.settleTable(ctx, guild, t) })
	case first && !m.inline:
		time.AfterFunc(tableGlass, func() {
			m.later(context.Background(), func(ctx context.Context) { m.settleTable(ctx, guild, t) })
		})
	}
	return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...))
}

// settleTable throws t's bones and shows them on its message, and calls
// all hands for every hundred in the drowning hour.
func (m *Module) settleTable(ctx context.Context, guild snowflake.ID, t *table) {
	s := m.st.server(guild)
	s.mu.Lock()
	if !m.st.throw(s, t, m.now()) {
		s.mu.Unlock()
		return
	}
	if t.bell {
		s.bell.tally(t)
	}
	msg, err := m.tableMessage(s, t)
	var hands []seated
	for _, p := range t.seats {
		if p.throw == 100 && t.bell {
			hands = append(hands, p)
		}
	}
	s.mu.Unlock()
	if err != nil || m.rest == nil || t.msg == 0 {
		return
	}
	m.Save()
	if err := m.write(guild, guard.MessageSend, func() error {
		_, err := m.rest.UpdateMessage(t.channel, t.msg, core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...), rest.WithCtx(ctx))
		return err
	}); err != nil {
		m.log.Warn("pp: showing a table's throw", "guild", guild, "err", err)
	}
	for _, p := range hands {
		line := fmt.Sprintf("**all hands.** %s threw a hundred in the drowning hour and the purser paid %s pp", p.name, grouped(p.won/unit))
		if err := m.write(guild, guard.MessageSend, func() error {
			_, err := m.rest.CreateMessage(t.channel, discord.MessageCreate{Content: line, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx))
			return err
		}); err != nil {
			m.log.Warn("pp: calling all hands", "guild", guild, "err", err)
		}
	}
}

// staleTables throws every table whose glass ran out without its timer
// firing, from the minute's work.
func (m *Module) staleTables(ctx context.Context) {
	now := m.now()
	for g, s := range m.st.servers() {
		s.mu.Lock()
		t := s.table
		due := t != nil && !t.settled && len(t.seats) > 0 && now.Sub(t.first) >= tableStale
		s.mu.Unlock()
		if due {
			m.settleTable(ctx, g, t)
		}
	}
}

// tableMessage is t as its message: the screen, the slate's hash (or the
// seed once thrown), and a button for each stake while it's open. s.mu
// held.
func (m *Module) tableMessage(s *server, t *table) (discord.MessageCreate, error) {
	tb := Table{Bell: t.bell, Thrown: t.settled, Closes: t.closes(), Slate: t.slate()}
	if t.settled {
		tb.Slate = fmt.Sprintf("%x", t.seed)
	}
	for _, p := range t.seats {
		mem := s.members[p.user]
		class := Classes[0]
		if mem != nil {
			class = ClassOf(mem.class)
		}
		tb.Seats = append(tb.Seats, TableSeat{Name: p.name, Class: class, Stake: p.stake, Throw: p.throw, Won: p.won})
	}
	pic, err := TableScreen(tb)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	var lines []string
	var extra []discord.ContainerSubComponent
	if t.settled {
		best := tb.best()
		lines = append(lines, fmt.Sprintf("the bones are thrown. **%s** throws highest and goes on the beam", tb.Seats[best].Name),
			"-# the slate, turned round: `"+fmt.Sprintf("%x", t.seed)+"`. sha256(slate, table, bird) says every throw")
	} else {
		lines = append(lines, "pick a stake to sit down. every bird throws against the house, never each other")
		if !t.first.IsZero() {
			lines = append(lines, fmt.Sprintf("-# the glass runs out <t:%d:R>", t.closes().Unix()))
		}
		lines = append(lines, "-# the slate, turned to the wall: `"+t.slate()+"`")
		var row []discord.InteractiveComponent
		for k, st := range stakes {
			b := discord.NewSecondaryButton(fmt.Sprintf("%s · %s", st.name, grouped(st.pp/unit)), fmt.Sprintf("pp:sit:%d:%d", t.id, k))
			if st.pp > s.maxStake() {
				b = b.AsDisabled()
			}
			row = append(row, b)
		}
		extra = append(extra, discord.NewActionRow(row...))
	}
	title := "a table at the slack tide"
	if t.bell {
		title = "a table in the drowning hour"
	}
	alt := tb.alt()
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", title, strings.Join(lines, "\n"),
			append([]discord.ContainerSubComponent{discord.NewMediaGallery(screenItem(pic, alt))}, extra...)...)},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// Table is what a table's screen shows.
type Table struct {
	Bell, Thrown bool
	Closes       time.Time
	Seats        []TableSeat
	// Slate is the slate's hash while it's turned to the wall, its seed
	// once the bones are thrown, in hex.
	Slate string
}

// TableSeat is one bench.
type TableSeat struct {
	Name       string
	Class      Class
	Stake, Won int64
	Throw      int
}

// best is the seat that threw highest, the first to throw it on a tie.
func (t Table) best() int {
	b := 0
	for i, p := range t.Seats {
		if p.Throw > t.Seats[b].Throw {
			b = i
		}
	}
	return b
}

func (t Table) alt() string {
	var parts []string
	for _, p := range t.Seats {
		if t.Thrown {
			parts = append(parts, fmt.Sprintf("%s threw %d for %s pp", p.Name, p.Throw, grouped(p.Won/unit)))
		} else {
			parts = append(parts, fmt.Sprintf("%s staked %s pp", p.Name, grouped(p.Stake/unit)))
		}
	}
	if len(parts) == 0 {
		return "an empty table at the slack tide, six benches"
	}
	return "a table at the slack tide: " + strings.Join(parts, ", ")
}

// TableScreen draws a table in the taproom, moving: see tableFrames.
func TableScreen(t Table) ([]byte, error) {
	frames, beats := tableFrames(t)
	return pixel.Animate(frames, beats, screenScale)
}

// HouseScreen draws the slack tide's screen, moving: see holdFrames.
func HouseScreen(h House) ([]byte, error) {
	frames, beats := holdFrames(h)
	return pixel.Animate(frames, beats, screenScale)
}

// bellPost is the bell's one message in the house channel: when the bell
// it tells of starts, the message, how far through the hour it has been
// edited, and the hour's tally.
type bellPost struct {
	at       time.Time
	msg      snowflake.ID
	stage    int
	throwers map[snowflake.ID]bool
	hands    int
	best     string
	bestOf   int
}

// The bell's stages, and when each is told.
const (
	bellNone = iota
	bellDrawing
	bellBoats
	bellRinging
	bellStopped
)

// bellStage is the stage of the bell starting at at that now is in.
func bellStage(at, now time.Time) int {
	switch d := now.Sub(at); {
	case d < -time.Hour:
		return bellNone
	case d < -15*time.Minute:
		return bellDrawing
	case d < 0:
		return bellBoats
	case d < bellHour:
		return bellRinging
	case d < bellHour+30*time.Minute:
		return bellStopped
	}
	return bellNone
}

// tally counts a thrown table into the hour. s.mu held.
func (b *bellPost) tally(t *table) {
	if b.throwers == nil {
		b.throwers = map[snowflake.ID]bool{}
	}
	for _, p := range t.seats {
		b.throwers[p.user] = true
		if p.throw == 100 {
			b.hands++
		}
		if p.throw > b.bestOf {
			b.best, b.bestOf = p.name, p.throw
		}
	}
}

// bellText is what the bell's message says at stage.
func (b *bellPost) bellText(stage int) string {
	switch stage {
	case bellDrawing:
		return "the water's drawing back off the fish market steps"
	case bellBoats:
		return "the ferrymen are pulling their boats up the shingle"
	case bellRinging:
		return "the bell's ringing under the gannet. for the hour a hundred on the bones pays fifteen times, and the purser calls all hands"
	}
	tally := fmt.Sprintf("the bell's stopped and the tide's coming back.\n-# %d threw in the hour · all hands called %d times", len(b.throwers), b.hands)
	if b.best != "" {
		tally += fmt.Sprintf(" · %s threw %d and goes on the beam", b.best, b.bestOf)
	}
	return tally
}

// nextBell is the bell whose stages cover now, if any. Tomorrow's is only
// looked at inside this season: reading a season's seed fixes it, and the
// next season's mustn't be fixed before its beacon is drawn.
func (m *Module) nextBell(now time.Time) (time.Time, bool) {
	day := DayOf(now)
	days := []int64{day - 1, day}
	this, _ := m.st.bal().Season(day)
	if next, _ := m.st.bal().Season(day + 1); next == this {
		days = append(days, day+1)
	}
	for _, d := range days {
		for _, at := range Bells(m.st.seedOf(d), d) {
			if bellStage(at, now) != bellNone {
				return at, true
			}
		}
	}
	return time.Time{}, false
}

// bells moves every house's bell message along, from the minute's work.
func (m *Module) bells(ctx context.Context) {
	now := m.now()
	at, ok := m.nextBell(now)
	if !ok || m.rest == nil {
		return
	}
	stage := bellStage(at, now)
	for g, s := range m.st.servers() {
		if !m.enabled(g) {
			continue
		}
		s.mu.Lock()
		if s.houseCh == 0 || (s.bell.at.Equal(at) && s.bell.stage >= stage) {
			s.mu.Unlock()
			continue
		}
		if !s.bell.at.Equal(at) {
			// ponytail: in memory, so a restart mid-bell posts a fresh message
			// for the stage it's in; persist it if deploys land in bells often.
			if stage == bellStopped {
				s.mu.Unlock()
				continue
			}
			s.bell = bellPost{at: at}
			if stage == bellRinging {
				s.count(DayOf(at), statBell, 0, 0)
			}
		} else if stage == bellRinging {
			s.count(DayOf(at), statBell, 0, 0)
		}
		s.bell.stage = stage
		ch, msg, text := s.houseCh, s.bell.msg, s.bell.bellText(stage)
		s.mu.Unlock()
		card := discord.MessageCreate{
			Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "the drowning hour", text)},
			Flags:           discord.MessageFlagIsComponentsV2,
			AllowedMentions: core.NoPings(),
		}
		if stage == bellRinging {
			card.Components[0] = brand.Card(brand.ColorNotice, "pp", "the drowning hour", text,
				discord.NewActionRow(withIcon(discord.NewPrimaryButton("open a table", "pp:table"), "bell")))
		}
		var err error
		if msg == 0 {
			err = m.write(g, guard.MessageSend, func() error {
				posted, err := m.rest.CreateMessage(ch, card, rest.WithCtx(ctx))
				if err == nil {
					s.mu.Lock()
					s.bell.msg = posted.ID
					s.mu.Unlock()
				}
				return err
			})
		} else {
			err = m.write(g, guard.MessageSend, func() error {
				_, err := m.rest.UpdateMessage(ch, msg, core.Edit(discord.NewMessageUpdateV2(card.Components...)), rest.WithCtx(ctx))
				return err
			})
		}
		if err != nil {
			m.log.Warn("pp: the bell", "guild", g, "err", err)
		}
	}
}
