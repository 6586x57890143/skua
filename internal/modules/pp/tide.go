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

// setHouse keeps the channel an admin picked for the slack tide's tables
// (pp:house) or its news (pp:tidenews).
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
	news := e.Data.CustomID() == "pp:tidenews"
	// column is one of two constants, never input.
	column := "house_channel"
	if news {
		column = "tide_news"
	}
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, `+column+`) values ($1, $2)
		on conflict (guild_id) do update set `+column+` = excluded.`+column, int64(guild), int64(ch)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	if news {
		s.newsCh = ch
	} else {
		s.houseCh = ch
	}
	s.mu.Unlock()
	said := "the slack tide's shut: no tables, no bell"
	switch {
	case news && ch == 0:
		said = "the slack tide's news goes with its tables"
	case news:
		said = fmt.Sprintf("the slack tide tells its bell, all hands and duels in <#%s>, with a way to the table", ch)
	case ch != 0:
		said = fmt.Sprintf("the slack tide posts its tables in <#%s>", ch)
	}
	return e.CreateMessage(discord.NewMessageCreate().WithContent(said).WithEphemeral(true).WithAllowedMentions(core.NoPings()))
}

// news is where s's slack tide tells its news: its news channel, or beside
// its tables. s.mu held.
func (s *server) news() snowflake.ID {
	if s.newsCh != 0 {
		return s.newsCh
	}
	return s.houseCh
}

// duelPing is how long a bird called out at the rail goes before another
// call-out pings them again: a call-out after that is told without one.
const duelPing = time.Hour

// pingDuel is whether a call-out of user now may ping them, and marks it.
// s.mu held.
func (s *server) pingDuel(user snowflake.ID, now time.Time) bool {
	if last, ok := s.duelPinged[user]; ok && now.Sub(last) < duelPing {
		return false
	}
	if s.duelPinged == nil {
		s.duelPinged = map[snowflake.ID]time.Time{}
	}
	s.duelPinged[user] = now
	return true
}

// tableLink is a way to t's message, or to its channel before it's posted.
func tableLink(guild snowflake.ID, t *table) discord.ButtonComponent {
	url := fmt.Sprintf("https://discord.com/channels/%s/%s", guild, t.channel)
	if t.msg != 0 {
		url += "/" + t.msg.String()
	}
	return withIcon(discord.NewLinkButton("go to the table", url), "bones")
}

// duelWord is a duel at the rail as the news tells it: called out, or how
// it went once the bones are thrown. s.mu held.
func duelWord(t *table, caller string) string {
	switch {
	case !t.settled:
		return fmt.Sprintf("**a duel at the rail.** %s has called %s out, and the purser's holding the glass", caller, t.called)
	case len(t.seats) < 2:
		return fmt.Sprintf("**a duel at the rail.** %s called %s out, and nobody came to the rail", caller, t.called)
	}
	a, b := t.seats[0], t.seats[1]
	if b.throw > a.throw {
		a, b = b, a
	}
	if a.throw == b.throw {
		return fmt.Sprintf("**a duel at the rail.** %s and %s both threw %d, and the honour stays at the rail", a.name, b.name, a.throw)
	}
	return fmt.Sprintf("**a duel at the rail.** %s takes the honour from %s, %d to %d", a.name, b.name, a.throw, b.throw)
}

// newsCard is a line of the slack tide's news with its way to the table.
func newsCard(text string, link discord.ButtonComponent) discord.MessageCreate {
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "the slack tide", text, discord.NewActionRow(link))},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}
}

// House is what the slack tide's screen shows: the hold against its
// line, the stakes the purser takes, and the bird's own day at the tables.
type House struct {
	Hold, Line      int64
	MaxStake        int64
	Staked          int64
	Bell            bool
	Open            bool // a table's open now
	Shut            bool // no house channel
	Beam            string
	BeamThrow       int
	Throws, Throwrs int64
	// Sump is the scene outside, the screen's top.
	Sump Sump
	// Scraps is how many of the deckhead's scraps are nailed up.
	Scraps int
}

// tideMessage is the slack tide's screen for user.
func (m *Module) tideMessage(guild, user snowflake.ID) (discord.MessageCreate, error) {
	now := m.now()
	_, bell := m.st.ringing(now)
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, user, DayOf(now))
	h := House{Hold: s.hold, Line: houseLine(), MaxStake: s.maxStake(), Staked: mem.staked,
		Bell: bell, Open: s.table != nil && !s.table.settled, Shut: s.houseCh == 0}
	if !s.noted {
		h.Hold, h.MaxStake = Rules.HouseNote*unit, Rules.HouseNote*unit*Rules.HouseStake/10000
	}
	ch := s.houseCh
	h.Beam, h.BeamThrow = s.onBeam(DayOf(now))
	h.Scraps = nailed(m.st.scraps(s, DayOf(now)))
	s.mu.Unlock()
	h.Sump = m.sumpAt(h.Hold, h.Line, now)
	pic, err := HouseScreen(h)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := fmt.Sprintf("the slack tide: the hold at %s of its %s pp line, %s; %d of %d scraps on the deckhead", grouped(h.Hold/unit), grouped(h.Line/unit), tideWord(h), h.Scraps, len(feats))
	if h.Beam != "" {
		alt += fmt.Sprintf("; %s on the beam with %d", h.Beam, h.BeamThrow)
	}
	// Everything a bird reads is on the screen; the card says only what
	// an admin has to do.
	var lines []string
	if h.Shut {
		lines = append(lines, "-# shut until an admin picks its channel on /help")
	}
	btn := withIcon(discord.NewPrimaryButton("open a table", "pp:table"), "bones")
	if h.Shut {
		btn = btn.AsDisabled()
	} else if h.Open {
		btn = withIcon(discord.NewLinkButton("go to the table", fmt.Sprintf("https://discord.com/channels/%s/%s", guild, ch)), "bones")
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "the slack tide", strings.Join(lines, "\n"),
			discord.NewMediaGallery(screenItem(pic, alt)), discord.NewActionRow(btn), discord.NewActionRow(railPick(h.Shut || h.Open)))},
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

// railPick is the slack tide's picker for calling a bird out at the rail,
// shut while a table is open or the house is.
func railPick(shut bool) discord.UserSelectMenuComponent {
	pick := discord.UserSelectMenuComponent{CustomID: "pp:rail", Placeholder: "duel a bird at the rail", MaxValues: 1}
	if shut {
		pick = pick.AsDisabled()
	}
	return pick
}

// openTable opens a table in the house channel and says where: an open
// one from the slack tide's button, or from its picker a call-out at the
// rail, which pings the bird called out and nobody else.
func (m *Module) openTable(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	now := m.now()
	at, bell := m.st.ringing(now)
	s := m.st.server(guild)
	s.mu.Lock()
	var t *table
	var err error
	if pick, ok := e.Data.(discord.UserSelectMenuInteractionData); ok {
		if len(pick.Values) == 0 {
			s.mu.Unlock()
			return errRailWho
		}
		called := pick.Values[0]
		name := called.String()
		if rm, ok := pick.Resolved.Members[called]; ok {
			name = rm.EffectiveName()
		} else if u, ok := pick.Resolved.Users[called]; ok {
			name = u.EffectiveName()
		}
		t, err = m.st.callOut(s, s.houseCh, now, bell, e.User().ID, called, name)
	} else {
		t, err = m.st.open(s, s.houseCh, now, bell)
	}
	var build func() (discord.MessageCreate, error)
	// A call-out pings the bird called once, where it's told: in the news
	// when the news has a channel of its own, on the table when it hasn't,
	// and not at all if a call-out pinged them within the hour.
	var news snowflake.ID
	ping := false
	if err == nil {
		t.bellAt = at
		if t.rail != 0 {
			t.callerName = e.Member().EffectiveName()
			if ch := s.news(); ch != t.channel {
				news = ch
			}
			ping = s.pingDuel(t.rail, now)
		}
		build = m.tableView(s, t)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if m.rest == nil {
		return e.CreateMessage(discord.NewMessageCreate().WithContent("the table's set").WithEphemeral(true))
	}
	said := fmt.Sprintf("the purser's set a table in <#%s>: sit down before the glass runs out", t.channel)
	// The table moves, so drawing it takes longer than a press waits: the
	// press is answered at once and the table posted after.
	if e.Client().Rest == nil {
		if err := m.postTable(ctx, guild, t, build, news, ping); err != nil {
			return err
		}
		return e.CreateMessage(discord.NewMessageCreate().WithContent(said).WithEphemeral(true).WithAllowedMentions(core.NoPings()))
	}
	if err := e.DeferCreateMessage(true); err != nil {
		s.mu.Lock()
		if s.table == t && len(t.seats) == 0 {
			s.table = nil
		}
		s.mu.Unlock()
		return err
	}
	m.later(ctx, func(ctx context.Context) {
		if err := m.postTable(ctx, guild, t, build, news, ping); err != nil {
			m.answerLater(ctx, guild, e, e.Client(), discord.MessageCreate{}, err, "a table", "the purser couldn't set the table just now; try again")
			return
		}
		if _, err := e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(),
			discord.MessageUpdate{Content: &said, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx)); err != nil {
			m.log.Warn("pp: saying where a table is", "guild", guild, "err", err)
		}
	})
	return nil
}

// postTable draws t and posts it in its channel, and a call-out's news, or
// takes an empty table down again if it can't. build is t's view.
func (m *Module) postTable(ctx context.Context, guild snowflake.ID, t *table, build func() (discord.MessageCreate, error), news snowflake.ID, ping bool) error {
	s := m.st.server(guild)
	var posted *discord.Message
	msg, err := build()
	if ping && news == 0 {
		msg.AllowedMentions = &discord.AllowedMentions{Users: []snowflake.ID{t.rail}}
	}
	if err == nil {
		err = m.write(guild, guard.MessageSend, func() (err error) {
			posted, err = m.rest.CreateMessage(t.channel, msg, rest.WithCtx(ctx))
			return err
		})
	}
	if err != nil {
		s.mu.Lock()
		if s.table == t && len(t.seats) == 0 {
			s.table = nil
		}
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	t.msg = posted.ID
	word := duelWord(t, t.callerName)
	s.mu.Unlock()
	if news != 0 {
		card := newsCard(fmt.Sprintf("<@%s>, %s", t.rail, word), tableLink(guild, t))
		if ping {
			card.AllowedMentions = &discord.AllowedMentions{Users: []snowflake.ID{t.rail}}
		}
		if err := m.write(guild, guard.MessageSend, func() error {
			told, err := m.rest.CreateMessage(news, card, rest.WithCtx(ctx))
			if err == nil {
				s.mu.Lock()
				t.news, t.newsIn = told.ID, news
				s.mu.Unlock()
			}
			return err
		}); err != nil {
			m.log.Warn("pp: telling a duel", "guild", guild, "err", err)
		}
	}
	return nil
}

// sitAt is a press on a table's stake: it seats the bird and redraws the
// table, and throws the bones once its benches are full.
func (m *Module) sitAt(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	// pp:sit:<table>:<stake>, or pp:sit:<table>:marker
	parts := strings.Split(e.Data.CustomID(), ":")
	if len(parts) != 4 {
		return errTableShut
	}
	id, err1 := strconv.ParseInt(parts[2], 10, 64)
	marker, amber := parts[3] == "marker", parts[3] == "amber"
	k, err2 := strconv.Atoi(parts[3])
	if err1 != nil || !marker && !amber && (err2 != nil || k < 0 || k >= len(stakes)) {
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
	sit := func() error {
		return m.st.sitDown(s, t, guild, e.User().ID, e.Member().EffectiveName(), stakes[k].pp, now)
	}
	if marker {
		sit = func() error { return m.st.sitOnMarker(s, t, guild, e.User().ID, e.Member().EffectiveName(), now) }
	}
	if amber {
		sit = func() error { return m.st.sitOnAmber(s, t, guild, e.User().ID, e.Member().EffectiveName(), now) }
	}
	if err := sit(); err != nil {
		s.mu.Unlock()
		return err
	}
	full := len(t.seats) >= t.benches()
	var build func() (discord.MessageCreate, error)
	if m.rest == nil {
		build = m.tableView(s, t)
	}
	s.mu.Unlock()
	m.glass(guild, t, first, full)
	if build != nil {
		// No REST to answer through later: draw it now.
		msg, err := build()
		if err != nil {
			return err
		}
		return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...))
	}
	// The stake is taken; the picture takes longer than a press waits when
	// a table fills at once, so the press is answered now and the table
	// redrawn after. A full table is redrawn by its throw.
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	if !full {
		m.later(context.Background(), func(ctx context.Context) {
			if err := m.redraw(guild, t, func(msg discord.MessageCreate) error {
				_, err := m.rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(),
					core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...), rest.WithCtx(ctx))
				return err
			}); err != nil {
				m.log.Warn("pp: redrawing a table", "guild", guild, "err", err)
			}
		})
	}
	return nil
}

// glass starts what a seat at t starts: the throw, once its benches are
// full, or the glass, at its first seat.
func (m *Module) glass(guild snowflake.ID, t *table, first, full bool) {
	switch {
	case full:
		m.later(context.Background(), func(ctx context.Context) { m.settleTable(ctx, guild, t) })
	case first && !m.inline:
		time.AfterFunc(tableGlass, func() {
			m.later(context.Background(), func(ctx context.Context) { m.settleTable(ctx, guild, t) })
		})
	}
}

// gateCandle stakes a candle's worth of amber from the gate: on the table
// standing at the slack tide, or on one set for it, so a bird with amber
// needn't wait for somebody else to open a table.
func (m *Module) gateCandle(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	now := m.now()
	at, bell := m.st.ringing(now)
	user, name := e.User().ID, e.Member().EffectiveName()
	s := m.st.server(guild)
	s.mu.Lock()
	if !m.gateSees(s, e) {
		s.mu.Unlock()
		return errGateShut
	}
	// Told now, before a table goes up for a bird who can't sit at it.
	f := s.forayOf(user)
	switch {
	case f.amberDay == DayOf(now):
		s.mu.Unlock()
		return errAmberDone
	case f.amber < amberCandle:
		s.mu.Unlock()
		return errNoAmber
	}
	t := s.table
	var set func() (discord.MessageCreate, error)
	if t == nil || t.settled {
		var err error
		if t, err = m.st.open(s, s.houseCh, now, bell); err != nil {
			s.mu.Unlock()
			return err
		}
		t.bellAt = at
		set = m.tableView(s, t)
	}
	said := fmt.Sprintf("your candle's on the marker in <#%s>: the bones go when the glass runs out", t.channel)
	s.mu.Unlock()

	sit := func(ctx context.Context) error {
		if set != nil && m.rest != nil {
			if err := m.postTable(ctx, guild, t, set, 0, false); err != nil {
				return err
			}
		}
		s.mu.Lock()
		first := t.first.IsZero()
		err := m.st.sitOnAmber(s, t, guild, user, name, m.now())
		full := len(t.seats) >= t.benches()
		posted := t.msg != 0
		s.mu.Unlock()
		if err != nil {
			return err
		}
		m.glass(guild, t, first, full)
		if m.rest == nil || full || !posted {
			return nil
		}
		if err := m.redraw(guild, t, func(msg discord.MessageCreate) error {
			return m.write(guild, guard.MessageSend, func() error {
				_, err := m.rest.UpdateMessage(t.channel, t.msg, core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...), rest.WithCtx(ctx))
				return err
			})
		}); err != nil {
			m.log.Warn("pp: redrawing a table", "guild", guild, "err", err)
		}
		return nil
	}
	if e.Client().Rest == nil {
		if err := sit(ctx); err != nil {
			return err
		}
		return e.CreateMessage(discord.NewMessageCreate().WithContent(said).WithEphemeral(true).WithAllowedMentions(core.NoPings()))
	}
	if err := e.DeferCreateMessage(true); err != nil {
		s.mu.Lock()
		if s.table == t && len(t.seats) == 0 {
			s.table = nil
		}
		s.mu.Unlock()
		return err
	}
	m.later(ctx, func(ctx context.Context) {
		if err := sit(ctx); err != nil {
			m.answerLater(ctx, guild, e, e.Client(), discord.MessageCreate{}, err, "a candle", "the purser couldn't set the table just now; try again")
			return
		}
		if _, err := e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(),
			discord.MessageUpdate{Content: &said, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx)); err != nil {
			m.log.Warn("pp: saying where a candle is", "guild", guild, "err", err)
		}
	})
	return nil
}

// redraw draws t as it stands now, outside s.mu, and sends it: one redraw
// of a table at a time, so each lands after the one before.
func (m *Module) redraw(guild snowflake.ID, t *table, send func(discord.MessageCreate) error) error {
	t.draw.Lock()
	defer t.draw.Unlock()
	s := m.st.server(guild)
	s.mu.Lock()
	build := m.tableView(s, t)
	s.mu.Unlock()
	msg, err := build()
	if err != nil {
		return err
	}
	return send(msg)
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
	var hands []seated
	for _, p := range t.seats {
		if p.throw == 100 && t.bell {
			hands = append(hands, p)
		}
	}
	posted := t.msg != 0
	news, told, toldIn, word := s.news(), t.news, t.newsIn, duelWord(t, t.callerName)
	s.mu.Unlock()
	m.Save()
	if m.rest == nil || !posted {
		return
	}
	if err := m.redraw(guild, t, func(msg discord.MessageCreate) error {
		return m.write(guild, guard.MessageSend, func() error {
			_, err := m.rest.UpdateMessage(t.channel, t.msg, core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...), rest.WithCtx(ctx))
			return err
		})
	}); err != nil {
		m.log.Warn("pp: showing a table's throw", "guild", guild, "err", err)
	}
	if told != 0 {
		card := newsCard(word, tableLink(guild, t))
		if err := m.write(guild, guard.MessageSend, func() error {
			_, err := m.rest.UpdateMessage(toldIn, told, core.Edit(discord.NewMessageUpdateV2(card.Components...)), rest.WithCtx(ctx))
			return err
		}); err != nil {
			m.log.Warn("pp: telling how a duel went", "guild", guild, "err", err)
		}
	}
	for _, p := range hands {
		line := fmt.Sprintf("**all hands.** %s threw a hundred in the drowning hour and the purser paid %s pp", p.name, grouped(p.won/unit))
		if err := m.write(guild, guard.MessageSend, func() error {
			_, err := m.rest.CreateMessage(news, newsCard(line, tableLink(guild, t)), rest.WithCtx(ctx))
			return err
		}); err != nil {
			m.log.Warn("pp: calling all hands", "guild", guild, "err", err)
		}
	}
}

// staleTables throws every table whose glass ran out without its timer
// firing, and clears every table nobody sat at, from the minute's work.
func (m *Module) staleTables(ctx context.Context) {
	now := m.now()
	for g, s := range m.st.servers() {
		s.mu.Lock()
		t := s.table
		due := t != nil && !t.settled && len(t.seats) > 0 && now.Sub(t.first) >= tableStale
		idle := t != nil && !t.settled && len(t.seats) == 0 && now.Sub(t.opened) >= tableIdle
		if idle {
			// Nothing staked, so nothing to throw or give back: the table
			// just goes, and a press on its old buttons finds it shut.
			t.settled, s.table = true, nil
		}
		s.mu.Unlock()
		switch {
		case due:
			m.settleTable(ctx, g, t)
		case idle:
			m.clearTable(ctx, g, t, "nobody sat, so the purser cleared the table")
		}
	}
}

// clearTable edits a table's message to say why the purser cleared it,
// its buttons gone.
func (m *Module) clearTable(ctx context.Context, guild snowflake.ID, t *table, why string) {
	if m.rest == nil || t.msg == 0 {
		return
	}
	card := []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "a table at the slack tide",
		why+"\n-# open another from /pp")}
	if err := m.write(guild, guard.MessageSend, func() error {
		_, err := m.rest.UpdateMessage(t.channel, t.msg, discord.MessageUpdate{Components: &card, Attachments: &[]discord.AttachmentUpdate{}}, rest.WithCtx(ctx))
		return err
	}); err != nil {
		m.log.Warn("pp: clearing a table", "guild", guild, "err", err)
	}
}

// Shut is skua going down. A table still open goes with the process and
// its bones are never thrown, so every stake at it goes back to its purse
// now and its message says so, rather than standing open after the
// restart. (A crash skips this; the next boot refunds the stakes all the
// same, and only the message is left as it was.) Run it before the last
// Save.
func (m *Module) Shut(ctx context.Context) {
	today := DayOf(m.now())
	for g, s := range m.st.servers() {
		s.mu.Lock()
		t := s.table
		open := t != nil && !t.settled
		seated := open && len(t.seats) > 0
		if open {
			t.settled, s.table = true, nil
			s.refundAmber(t)
			s.refundSeats(today)
		}
		s.mu.Unlock()
		switch {
		case seated:
			m.clearTable(ctx, g, t, "skua went down before the bones were thrown, so the purser shut the table and every stake went back to its purse")
		case open:
			m.clearTable(ctx, g, t, "nobody sat, so the purser cleared the table")
		}
	}
}

// tableMessage is t as its message, drawn in place. s.mu held.
func (m *Module) tableMessage(s *server, t *table) (discord.MessageCreate, error) {
	return m.tableView(s, t)()
}

// tableView is t as its message: the screen, the slate's hash (or the
// seed once thrown), and a button for each stake while it's open. What it
// shows is gathered while s.mu is held and drawn when the func it returns
// runs, which needs no lock: a table's picture takes a good part of a
// second.
func (m *Module) tableView(s *server, t *table) func() (discord.MessageCreate, error) {
	tb := Table{Bell: t.bell, Thrown: t.settled, Closes: t.closes(), Slate: t.slate(), Scraps: m.st.scraps(s, DayOf(m.now()))}
	if t.settled {
		tb.Slate = fmt.Sprintf("%x", t.seed)
	}
	for _, p := range t.seats {
		mem := s.members[p.user]
		class := Classes[0]
		if mem != nil {
			class = ClassOf(mem.class)
		}
		tb.Seats = append(tb.Seats, TableSeat{Name: p.name, Class: class, Stake: p.stake, Throw: p.throw, Won: p.won, Marker: p.marker})
	}
	light, rail, called := lightAt(m.now()), t.rail != 0, t.called
	var lines []string
	var extra []discord.ContainerSubComponent
	// The screen tells the table; the card keeps only what a picture can't:
	// the ping, the glass counting down, the whole slate to check the
	// bones by, and the scraps a throw nailed up.
	if t.settled {
		if t.rail != 0 && len(tb.Seats) == 1 {
			lines = append(lines, "nobody came to the rail")
		}
		for _, e := range t.earned {
			lines = append(lines, nailedLine(e))
		}
		lines = append(lines, "-# slate `"+fmt.Sprintf("%x", t.seed)+"` · sha256(slate, table, bird) is each throw")
	} else {
		if t.rail != 0 && t.first.IsZero() {
			lines = append(lines, fmt.Sprintf("<@%s>, you're called out: match the stake", t.rail))
		}
		glass := ""
		if !t.first.IsZero() {
			glass = fmt.Sprintf("glass out <t:%d:R> · ", t.closes().Unix())
		}
		lines = append(lines, "-# "+glass+"slate `"+t.slate()+"`")
		var row []discord.InteractiveComponent
		for k, st := range stakes {
			b := discord.NewSecondaryButton(fmt.Sprintf("%s · %s", st.name, grouped(st.pp/unit)), fmt.Sprintf("pp:sit:%d:%d", t.id, k))
			if t.rail != 0 && len(t.seats) == 1 && st.pp != t.seats[0].stake {
				b = b.AsDisabled()
			}
			if st.pp > s.maxStake() {
				b = b.AsDisabled()
			}
			row = append(row, b)
		}
		// The newcomers' marker, open to anyone to press: the purser asks
		// who they are when they do.
		if t.rail == 0 {
			b := withIcon(discord.NewSecondaryButton("on the marker", fmt.Sprintf("pp:sit:%d:marker", t.id)), "wick")
			if stakes[0].pp > s.maxStake() {
				b = b.AsDisabled()
			}
			row = append(row, b)
		}
		extra = append(extra, discord.NewActionRow(row...))
		// A candle on the marker for amber from the last wood, on a row
		// of its own: the stakes fill theirs.
		if t.rail == 0 && s.gateOpen {
			b := withIcon(discord.NewSecondaryButton(fmt.Sprintf("a candle for %d amber", amberCandle), fmt.Sprintf("pp:sit:%d:amber", t.id)), "amber")
			if stakes[1].pp > s.maxStake() {
				b = b.AsDisabled()
			}
			extra = append(extra, discord.NewActionRow(b))
		}
	}
	title := "a table at the slack tide"
	switch {
	case t.rail != 0:
		title = "a duel at the rail"
	case t.bell:
		title = "a table in the drowning hour"
	}
	return func() (discord.MessageCreate, error) {
		var pic []byte
		var err error
		if rail {
			pic, err = RailScreen(Rail{Light: light, Bell: tb.Bell, Thrown: tb.Thrown, Seats: tb.Seats, Called: called, Slate: tb.Slate})
		} else {
			pic, err = TableScreen(tb)
		}
		if err != nil {
			return discord.MessageCreate{}, err
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
}

// Table is what a table's screen shows.
type Table struct {
	Bell, Thrown bool
	Closes       time.Time
	Seats        []TableSeat
	// Slate is the slate's hash while it's turned to the wall, its seed
	// once the bones are thrown, in hex.
	Slate string
	// Scraps are the deckhead overhead.
	Scraps []Scrap
}

// TableSeat is one bench.
type TableSeat struct {
	Name       string
	Class      Class
	Stake, Won int64
	Throw      int
	// Marker is a seat on the house's marker.
	Marker bool
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
			if p.Marker {
				parts = append(parts, p.Name+" on the house's marker")
			} else {
				parts = append(parts, fmt.Sprintf("%s staked %s pp", p.Name, grouped(p.Stake/unit)))
			}
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

// HouseScreen draws the slack tide's screen, moving: see houseFrames.
func HouseScreen(h House) ([]byte, error) {
	frames, beats := houseFrames(h)
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
	at, ok := m.nextBell(m.now())
	if !ok || m.rest == nil {
		return
	}
	m.bellAt(ctx, at)
}

// bellAt moves every house's message for the bell starting at at to the
// stage it's in now.
func (m *Module) bellAt(ctx context.Context, at time.Time) {
	stage := bellStage(at, m.now())
	for g, s := range m.st.servers() {
		if !m.enabled(g) {
			continue
		}
		s.mu.Lock()
		if s.houseCh == 0 || !s.released(DayOf(m.now())) || (s.bell.at.Equal(at) && s.bell.stage >= stage) {
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
		ch, house, msg, text := s.news(), s.houseCh, s.bell.msg, s.bell.bellText(stage)
		s.mu.Unlock()
		card := discord.MessageCreate{
			Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "the drowning hour", text)},
			Flags:           discord.MessageFlagIsComponentsV2,
			AllowedMentions: core.NoPings(),
		}
		if stage == bellRinging {
			row := discord.NewActionRow(withIcon(discord.NewPrimaryButton("open a table", "pp:table"), "bones"))
			if ch != house {
				row = row.AddComponents(withIcon(discord.NewLinkButton("go to the tables", fmt.Sprintf("https://discord.com/channels/%s/%s", g, house)), "bones"))
			}
			card.Components[0] = brand.Card(brand.ColorNotice, "pp", "the drowning hour", text, row)
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

// bellLine is the drowning hour as the status screen tells it, the same
// words as the house channel's message, for a server whose house is open:
// nothing before the water draws back or after the bell stops.
func (m *Module) bellLine(guild snowflake.ID) string {
	s := m.st.server(guild)
	s.mu.Lock()
	open := s.houseCh != 0 && s.released(DayOf(m.now()))
	s.mu.Unlock()
	if !open {
		return ""
	}
	at, ok := m.nextBell(m.now())
	if !ok {
		return ""
	}
	switch stage := bellStage(at, m.now()); stage {
	case bellDrawing, bellBoats, bellRinging:
		return "\n-# " + (&bellPost{}).bellText(stage)
	}
	return ""
}

// sumpAt is the scene outside at now, for a hold and its line: the hour's
// light, how far the bell has come and whether the pumps are working.
func (m *Module) sumpAt(hold, line int64, now time.Time) Sump {
	s := Sump{Hold: hold, Line: line, Light: lightAt(now), Pumping: now.Sub(dayStart(DayOf(now))) < time.Hour}
	if at, ok := m.nextBell(now); ok {
		s.Stage = bellStage(at, now)
	}
	return s
}
