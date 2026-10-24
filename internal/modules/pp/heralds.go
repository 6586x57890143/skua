package pp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// The heralds' desk. A petition sent holds its price from the member's
// purse (a hold row in the ledger) and is posted in the heralds' channel
// for a moderator to grant or refuse. A grant makes the member's role, or
// changes the one they wear, and the hold is spent; a refusal gives it
// back (a release row) with the heralds' reason. A first livery holds the
// fee and a week; a change to one already worn holds only the fee.
//
// A petition's row is claimed before it's acted on, so two moderators
// pressing at once settle it once.

const (
	heraldForm = "ppherald"
	// ownKey is a member's livery when it's one of their own.
	ownKey = "own"
)

var (
	errNoHeralds  = core.Tell("the heralds have no desk here yet: an admin picks their channel in the livery settings")
	errOwnAsked   = core.Tell("your petition is with the heralds already; wait for their answer")
	errOwnShort   = core.Tell("the fee and a first week cost more than you hold; keep earning and come back")
	errHeraldGone = core.Tell("that petition was already settled")
	errHeraldSelf = core.Tell("nobody settles their own petition")
	errHeraldWho  = core.Tell("only moderators who can manage roles settle petitions")
	errOwnDraft   = core.Tell("there's no petition written yet; write one first")
	errOwnNone    = core.Tell("you wear no livery of your own to keep")
)

// petition is a petition as the desk keeps it.
type petition struct {
	id     int64
	user   snowflake.ID
	name   string
	style  ownStyle
	colors []uint32
	icon   []byte
	held   int64
	change bool         // to a livery already worn
	role   snowflake.ID // made by a grant that didn't finish, else 0
}

// roleColors is how p's colours go to Discord: one, a gradient's two, or
// nacre's three.
func (p *petition) roleColors() discord.RoleColors {
	cs := p.colors
	if p.style.key == "nacre" {
		cs = nacreColors
	}
	var rc discord.RoleColors
	if len(cs) > 0 {
		rc.PrimaryColor = int(cs[0])
	}
	if len(cs) > 1 {
		second := int(cs[1])
		rc.SecondaryColor = &second
	}
	if len(cs) > 2 {
		third := int(cs[2])
		rc.TertiaryColor = &third
	}
	return rc
}

// ownWeek is a week of a livery of one's own woven as style, with a
// device or not, in hundredths of a pot day.
func ownWeek(style ownStyle, device bool) int64 {
	if device {
		return style.price + devicePrice
	}
	return style.price
}

// loadPetitions reads the petitions still with the heralds and the
// liveries of one's own worn now.
func (st *state) loadPetitions(ctx context.Context, db DB) error {
	// A grant a crash cut short never reached its record, so it waits
	// again; the role it made is on the row for the next grant to reuse.
	if _, err := db.Exec(ctx, `update pp_petition set state = 'asked', decided_by = 0 where state = 'granting'`); err != nil {
		return err
	}
	rows, err := db.Query(ctx, `select guild_id, user_id, id, state, role_id, style, icon is not null, colors
		from pp_petition where state in ('asked', 'granted')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guild, user, id, role int64
		var state, style string
		var device bool
		var colors []int64
		if err := rows.Scan(&guild, &user, &id, &state, &role, &style, &device, &colors); err != nil {
			return err
		}
		s := st.server(snowflake.ID(guild))
		s.mu.Lock()
		if state == "granted" {
			sty, _ := ownStyleOf(style)
			p := petition{style: sty}
			for _, c := range colors {
				p.colors = append(p.colors, uint32(c))
			}
			s.wearOwn(snowflake.ID(user), snowflake.ID(role), ownWeek(sty, device), uint32(p.roleColors().PrimaryColor))
		} else {
			s.ask(snowflake.ID(user), id)
		}
		s.mu.Unlock()
	}
	return rows.Err()
}

// wearOwn keeps user's own livery's role and its weekly price. s.mu held.
func (s *server) wearOwn(user, role snowflake.ID, week int64, color uint32) {
	if s.own == nil {
		s.own = map[snowflake.ID]ownWorn{}
	}
	s.own[user] = ownWorn{role: role, week: week, color: color}
}

// ask notes user's petition id as with the heralds, 0 for none. s.mu held.
func (s *server) ask(user snowflake.ID, id int64) {
	if s.asked == nil {
		s.asked = map[snowflake.ID]int64{}
	}
	if id == 0 {
		delete(s.asked, user)
		return
	}
	s.asked[user] = id
}

// ownWorn is a livery of one's own being worn: its role and what a week
// of it costs, in hundredths of a pot day.
type ownWorn struct {
	role  snowflake.ID
	week  int64
	color uint32 // its first colour, for the yard's corner
}

// sendPetition sends user's draft to the heralds, holding its price.
func (m *Module) sendPetition(ctx context.Context, guild, user snowflake.ID) error {
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.leasing.Lock()
	defer s.leasing.Unlock()
	s.mu.Lock()
	mem := m.st.member(s, guild, user, today)
	d, heralds := s.drafts[user], s.heralds
	worn := s.own[user]
	p := petition{user: user, change: worn.role != 0 && mem.livery == ownKey}
	var err error
	switch {
	case d == nil:
		err = errOwnDraft
	case heralds == 0:
		err = errNoHeralds
	case s.asked[user] != 0:
		err = errOwnAsked
	case mem.rung < d.style.rung || d.icon != nil && mem.rung < deviceRung:
		err = errOwnStyle
	}
	if err == nil {
		p.name, p.style, p.colors, p.icon = d.name, d.style, d.colors, d.icon
		p.held = m.st.pot() * heraldsFee / 100
		if !p.change {
			p.held += m.st.weekly(d) * s.rollFactor() / 100
		}
		if mem.purse < p.held {
			err = errOwnShort
		}
	}
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	// Another member's petition with the heralds, or livery granted, holds
	// its name.
	var taken int64
	if err := queryOne(ctx, m.db, `select count(*) from pp_petition where guild_id = $1 and user_id <> $2
		and lower(name) = lower($3) and state in ('asked', 'granting', 'granted')`,
		[]any{int64(guild), int64(user), p.name}, &taken); err != nil {
		return err
	}
	if taken > 0 {
		return errOwnTaken
	}
	s.mu.Lock()
	mem = m.st.member(s, guild, user, today)
	if mem.purse < p.held || s.asked[user] != 0 {
		s.mu.Unlock()
		return errOwnShort
	}
	mem.purse -= p.held
	mem.dirty = true
	s.append(user, today, reasonHold, -p.held, nil)
	s.ask(user, -1)
	s.mu.Unlock()
	m.Save()

	colors := make([]int64, len(p.colors))
	for i, c := range p.colors {
		colors[i] = int64(c)
	}
	err = queryOne(ctx, m.db, `insert into pp_petition (guild_id, user_id, name, style, colors, icon, state, held, change, asked)
		values ($1, $2, $3, $4, $5, $6, 'asked', $7, $8, $9) returning id`,
		[]any{int64(guild), int64(user), p.name, p.style.key, colors, p.icon, p.held, p.change, today}, &p.id)
	if err != nil {
		m.release(guild, user, p.held, 0)
		return err
	}
	s.mu.Lock()
	s.ask(user, p.id)
	s.mu.Unlock()
	if err := m.postPetition(ctx, guild, heralds, p); err != nil {
		// It's sent and held; the livery settings list it for the heralds
		// to settle all the same.
		m.log.Warn("pp: posting a petition", "guild", guild, "err", err)
	}
	return nil
}

// release gives user back held after a petition that didn't go through,
// and clears it from the desk. id is the petition, 0 if it never was one.
func (m *Module) release(guild, user snowflake.ID, held, id int64) {
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, user, today)
	mem.purse += held
	mem.dirty = true
	s.append(user, today, reasonRelease, held, []int64{id})
	s.ask(user, 0)
	s.mu.Unlock()
	m.Save()
}

// petitionCard is p as the heralds read it, with what was decided or the
// buttons to decide it.
func petitionCard(p petition, note string) discord.ContainerComponent {
	hex := make([]string, len(p.colors))
	for i, c := range p.colors {
		hex[i] = fmt.Sprintf("`#%06x`", c)
	}
	if p.style.key == "nacre" {
		hex = []string{"holographic"}
	}
	what := "a new livery"
	if p.change {
		what = "a change to their livery"
	}
	device := "none"
	if p.icon != nil {
		device = "the picture above, beside the name"
	}
	body := core.Readout([][2]string{
		{"from", fmt.Sprintf("<@%s>", p.user)},
		{"asks for", what},
		{"name", p.name},
		{"weave", p.style.name + " · " + strings.Join(hex, " ")},
		{"device", device},
		{"held", grouped(p.held/unit) + " pp, returned if refused"},
	})
	var extra []discord.ContainerSubComponent
	if note == "" {
		id := strconv.FormatInt(p.id, 10)
		extra = append(extra, discord.NewActionRow(
			discord.NewSuccessButton("grant", "pp:herald:yes:"+id),
			discord.NewDangerButton("refuse", "pp:herald:no:"+id)))
	} else {
		body += "\n" + note
	}
	color := brand.ColorWarn
	if strings.HasPrefix(note, "✓") {
		color = brand.ColorOK
	} else if note != "" {
		color = brand.ColorIdle
	}
	return brand.Card(color, Name, "a petition to the heralds", body, extra...)
}

// postPetition posts p in the heralds' channel, the device shown so a
// moderator sees what they grant.
func (m *Module) postPetition(ctx context.Context, guild, ch snowflake.ID, p petition) error {
	if m.rest == nil {
		return nil
	}
	msg := discord.MessageCreate{
		Components:      []discord.LayoutComponent{petitionCard(p, "")},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}
	if p.icon != nil {
		card := msg.Components[0].(discord.ContainerComponent)
		card.Components = append([]discord.ContainerSubComponent{discord.NewMediaGallery(discord.MediaGalleryItem{
			Media: discord.UnfurledMediaItem{URL: "attachment://device.png"}, Description: "the device " + p.name + " asks for"})}, card.Components...)
		msg.Components[0] = card
		msg.Files = []*discord.File{discord.NewFile("device.png", "", bytes.NewReader(p.icon))}
	}
	var posted *discord.Message
	err := m.write(guild, guard.MessageSend, func() (err error) {
		posted, err = m.rest.CreateMessage(ch, msg, rest.WithCtx(ctx))
		return err
	})
	if err != nil {
		return err
	}
	_, err = m.db.Exec(ctx, `update pp_petition set post = $2 where id = $1`, p.id, int64(posted.ID))
	return err
}

// mayDecide is whether e comes from someone who settles petitions: an
// admin, or a member who can manage roles.
func (m *Module) mayDecide(e discord.Interaction) bool {
	if m.Admin != nil && m.Admin(e) {
		return true
	}
	mem := e.Member()
	return mem != nil && (mem.Permissions.Has(discord.PermissionManageRoles) || mem.Permissions.Has(discord.PermissionAdministrator))
}

// herald is a press on a petition: grant settles it now, refuse asks why.
func (m *Module) herald(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if !m.mayDecide(e) {
		return errHeraldWho
	}
	id := e.Data.CustomID()
	if id == "pp:herald:repost" {
		return m.repost(ctx, guild, e)
	}
	n, err := strconv.ParseInt(id[strings.LastIndex(id, ":")+1:], 10, 64)
	if err != nil {
		return errHeraldGone
	}
	if strings.HasPrefix(id, "pp:herald:no:") {
		return e.Modal(discord.ModalCreate{
			CustomID: heraldForm + ":" + strconv.FormatInt(n, 10),
			Title:    "refuse the petition",
			Components: []discord.LayoutComponent{discord.LabelComponent{Label: "the heralds' reason", Description: "the member reads it on their petition",
				Component: discord.TextInputComponent{CustomID: "reason", Style: discord.TextInputStyleShort, Required: true, MaxLength: 200}}},
		})
	}
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	note, err := m.grantPetition(ctx, guild, n, e.User().ID)
	if err != nil {
		if _, isTell := errors.AsType[core.Tell](err); !isTell {
			m.log.Warn("pp: granting a petition", "guild", guild, "err", err)
			err = core.Tell("skua couldn't make the livery just now; the petition still waits")
		}
		_, ferr := e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(), said(err.Error()), rest.WithCtx(ctx))
		return ferr
	}
	_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(),
		core.Edit(discord.NewMessageUpdateV2(petitionCard(note.p, note.line))), rest.WithCtx(ctx))
	return err
}

// settled is a petition and the line that says how it was settled.
type settled struct {
	p    petition
	line string
}

// petitionCols are the columns scanPetition reads, in its order.
const petitionCols = `id, user_id, name, style, colors, icon, held, change, role_id`

// scanPetition reads one row of petitionCols.
func scanPetition(scan func(dest ...any) error) (petition, error) {
	var p petition
	var user, role int64
	var style string
	var colors []int64
	if err := scan(&p.id, &user, &p.name, &style, &colors, &p.icon, &p.held, &p.change, &role); err != nil {
		return p, err
	}
	p.user, p.role = snowflake.ID(user), snowflake.ID(role)
	p.style, _ = ownStyleOf(style)
	for _, c := range colors {
		p.colors = append(p.colors, uint32(c))
	}
	return p, nil
}

// claim reads petition n of guild, still asked, and marks it state with
// reason, so nobody else acts on it.
func (m *Module) claim(ctx context.Context, guild snowflake.ID, n int64, by snowflake.ID, state, reason string) (petition, error) {
	rows, err := m.db.Query(ctx, `select `+petitionCols+` from pp_petition where id = $1 and guild_id = $2 and state = 'asked'`, n, int64(guild))
	if err != nil {
		return petition{}, err
	}
	var p petition
	found := rows.Next()
	if found {
		p, err = scanPetition(rows.Scan)
	}
	rows.Close()
	switch {
	case err != nil:
		return p, err
	case !found:
		return p, errHeraldGone
	case p.user == by:
		return p, errHeraldSelf
	}
	tag, err := m.db.Exec(ctx, `update pp_petition set state = $3, decided_by = $4, reason = $5 where id = $1 and guild_id = $2 and state = 'asked'`,
		n, int64(guild), state, int64(by), reason)
	if err != nil {
		return p, err
	}
	if tag.RowsAffected() != 1 {
		return p, errHeraldGone
	}
	return p, nil
}

// grantPetition grants petition n: the member's role made or changed and
// put on them, the hold spent. A change to a livery that has faded since
// it was sent can't be granted: its fee goes back.
func (m *Module) grantPetition(ctx context.Context, guild snowflake.ID, n int64, by snowflake.ID) (settled, error) {
	p, err := m.claim(ctx, guild, n, by, "granting", "")
	if err != nil {
		return settled{}, err
	}
	s := m.st.server(guild)
	s.leasing.Lock()
	defer s.leasing.Unlock()
	today := DayOf(m.now())
	s.mu.Lock()
	mem := m.st.member(s, guild, p.user, today)
	worn := s.own[p.user].role != 0 && mem.livery == ownKey
	before := wornRole(s, p.user, mem)
	s.mu.Unlock()
	if p.change && !worn {
		const why = "the livery it changes has faded since"
		if _, err := m.db.Exec(ctx, `update pp_petition set state = 'refused', reason = $2 where id = $1`, n, why); err != nil {
			return settled{}, err
		}
		m.release(guild, p.user, p.held, n)
		return settled{p, "✗ not granted: " + why + "; the fee went back"}, nil
	}
	role, err := m.dressOwn(ctx, guild, p)
	if err != nil {
		// Not granted, so it waits again, keeping a role already made.
		_, _ = m.db.Exec(ctx, `update pp_petition set state = 'asked', decided_by = 0, role_id = $2 where id = $1 and state = 'granting'`, n, int64(role))
		return settled{}, dyeErr(err)
	}
	// The record first: once it says granted, a boot finds the livery worn.
	if _, err := m.db.Exec(ctx, `update pp_petition set state = 'granted', role_id = $2 where id = $1`, n, int64(role)); err != nil {
		_, _ = m.db.Exec(ctx, `update pp_petition set state = 'asked', decided_by = 0, role_id = $2 where id = $1 and state = 'granting'`, n, int64(role))
		return settled{}, err
	}
	// A livery changed retires the petition it was granted from.
	if _, err := m.db.Exec(ctx, `update pp_petition set state = 'changed' where guild_id = $1 and user_id = $2 and state = 'granted' and id <> $3`,
		int64(guild), int64(p.user), n); err != nil {
		m.log.Warn("pp: retiring a changed petition", "guild", guild, "err", err)
	}
	// A trade or faction livery worn before comes off.
	if before != 0 && before != role {
		if err := m.dress(ctx, guild, p.user, before, false, "a livery of their own granted"); err != nil && !errors.Is(err, errNotLivery) {
			m.log.Warn("pp: taking off a livery", "guild", guild, "err", err)
		}
	}
	s.mu.Lock()
	mem = m.st.member(s, guild, p.user, today)
	// The hold was taken from the purse when it was sent; granted, it's
	// spent.
	s.seasonBurn += p.held
	s.dirty = true
	if !p.change {
		mem.livery, mem.liveryUntil = ownKey, today+leaseDays
	}
	s.wearOwn(p.user, role, ownWeek(p.style, p.icon != nil), uint32(p.roleColors().PrimaryColor))
	s.ask(p.user, 0)
	delete(s.drafts, p.user)
	mem.dirty = true
	s.mu.Unlock()
	// ponytail: a crash between the record and this save leaves the member
	// with the role granted but no lease to fade it; a boot that squares
	// granted petitions with members would close it.
	m.Save()
	return settled{p, fmt.Sprintf("✓ granted by <@%s>", by)}, nil
}

// wornRole is the role of the livery mem wears, 0 for none. s.mu held.
func wornRole(s *server, user snowflake.ID, mem *member) snowflake.ID {
	if mem.livery == ownKey {
		return s.own[user].role
	}
	return s.dyeRoles[mem.livery]
}

// dressOwn makes p's role, or changes the one its member wears or a
// grant that failed already made, and puts it on them; one deleted by
// hand is made again. It gives the role even with an error, so a role
// made is never lost. s.leasing held.
func (m *Module) dressOwn(ctx context.Context, guild snowflake.ID, p petition) (snowflake.ID, error) {
	roles, _, err := m.standing(ctx, guild)
	if err != nil {
		return p.role, err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	role := s.own[p.user].role
	s.mu.Unlock()
	if role == 0 {
		role = p.role
	}
	if role != 0 && !slices.ContainsFunc(roles, func(r discord.Role) bool { return r.ID == role }) {
		role = 0
	}
	l := look{name: p.name, colors: p.roleColors(), icon: p.icon}
	why := "a livery granted by the heralds"
	if role != 0 {
		if err := m.editRole(ctx, guild, role, l, why); err != nil {
			return role, err
		}
	} else {
		made, err := m.makeRole(ctx, guild, l, why)
		if made == 0 {
			return 0, err
		}
		if err != nil {
			m.log.Warn("pp: moving a livery under the anchor", "guild", guild, "err", err)
		}
		role = made
		// Kept on the petition at once, so a grant that fails from here
		// reuses it rather than making another.
		if _, err := m.db.Exec(ctx, `update pp_petition set role_id = $2 where id = $1`, p.id, int64(role)); err != nil {
			m.log.Warn("pp: keeping a livery's role", "guild", guild, "err", err)
		}
	}
	return role, m.dress(ctx, guild, p.user, role, true, why)
}

// refusePetition is the heralds' refusal form sent: the petition refused
// with its reason and the hold returned.
func (m *Module) refusePetition(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil || !m.mayDecide(e) {
		return errHeraldWho
	}
	_, after, _ := strings.Cut(e.Data.CustomID, ":")
	n, err := strconv.ParseInt(after, 10, 64)
	if err != nil {
		return errHeraldGone
	}
	reason := line(e.Data.Text("reason"))
	p, err := m.claim(ctx, *guild, n, e.User().ID, "refused", reason)
	if err != nil {
		return err
	}
	m.release(*guild, p.user, p.held, n)
	return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(petitionCard(p, fmt.Sprintf("✗ refused by <@%s>: %s", e.User().ID, reason)))))
}

// letter is the heralds' last word on user's petitions: with them, or
// refused and why, "" when there's nothing to say.
func (m *Module) letter(ctx context.Context, guild, user snowflake.ID) string {
	s := m.st.server(guild)
	s.mu.Lock()
	asked := s.asked[user] != 0
	s.mu.Unlock()
	if asked {
		return "with the heralds"
	}
	var state, reason string
	err := queryOne(ctx, m.db, `select state, reason from pp_petition where guild_id = $1 and user_id = $2 order by id desc limit 1`,
		[]any{int64(guild), int64(user)}, &state, &reason)
	if err != nil || state != "refused" {
		return ""
	}
	return "refused: " + reason
}

// deferred answers e after act, which may take longer than an interaction
// waits: the press is deferred, act runs, and the screen page draws comes
// back as an edit, or act's refusal as a note.
func (m *Module) deferred(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate, act func() error, page func() (discord.MessageCreate, error)) error {
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	err := act()
	var msg discord.MessageCreate
	if err == nil {
		msg, err = page()
	}
	if err != nil {
		if _, isTell := errors.AsType[core.Tell](err); !isTell {
			m.log.Warn("pp: a livery", "guild", guild, "err", err)
			err = core.Tell("the dyers couldn't do that just now; try again in a while")
		}
		_, ferr := e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(), said(err.Error()), rest.WithCtx(ctx))
		return ferr
	}
	msg = m.withMenu(guild, e.User().ID, m.Admin != nil && m.Admin(e), msg, "pp:shop")
	_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(),
		core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...), rest.WithCtx(ctx))
	return err
}

// leaseOwn leases user another week of the livery of their own they wear,
// dressing them again in case its role came off.
func (m *Module) leaseOwn(ctx context.Context, guild, user snowflake.ID) error {
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.leasing.Lock()
	defer s.leasing.Unlock()
	var days int64
	check := func(mem *member, worn ownWorn) (int64, error) {
		var weeks, ahead int64
		days, weeks, ahead = term(mem.rung)
		price := m.st.pot() * worn.week / 100 * s.rollFactor() / 100 * weeks
		switch {
		case worn.role == 0 || mem.livery != ownKey:
			return 0, errOwnNone
		case mem.purse < price:
			return 0, errDyeShort
		case mem.liveryUntil+days > today+ahead:
			return 0, errDyeAhead
		}
		return price, nil
	}
	s.mu.Lock()
	worn := s.own[user]
	_, err := check(m.st.member(s, guild, user, today), worn)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := m.dress(ctx, guild, user, worn.role, true, "another week of a livery"); err != nil {
		return dyeErr(err)
	}
	s.mu.Lock()
	mem := m.st.member(s, guild, user, today)
	price, err := check(mem, s.own[user])
	if err != nil {
		s.mu.Unlock()
		return err
	}
	mem.purse -= price
	mem.liveryUntil = max(today, mem.liveryUntil) + days
	m.st.burn(s, user, today, reasonLease, price, []int64{-1, mem.liveryUntil})
	mem.dirty = true
	s.mu.Unlock()
	m.Save()
	return nil
}

// waiting is the livery settings' word on petitions with the heralds, and
// the way to post them again, for one whose post went astray.
func (m *Module) waiting(guild snowflake.ID) []discord.ContainerSubComponent {
	s := m.st.server(guild)
	s.mu.Lock()
	n := len(s.asked)
	s.mu.Unlock()
	if n == 0 {
		return nil
	}
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay(fmt.Sprintf("-# %d petitions wait for the heralds", n)),
		discord.NewActionRow(discord.NewSecondaryButton("post them to the heralds again", "pp:herald:repost")),
	}
}

// repost posts every petition still with the heralds to their channel
// again, going on past one that fails.
func (m *Module) repost(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	s := m.st.server(guild)
	s.mu.Lock()
	ch := s.heralds
	s.mu.Unlock()
	if ch == 0 {
		return errNoHeralds
	}
	// Each post may upload a device: more than an interaction may wait.
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	rows, err := m.db.Query(ctx, `select `+petitionCols+` from pp_petition where guild_id = $1 and state = 'asked' order by id`, int64(guild))
	if err != nil {
		return err
	}
	var ps []petition
	for rows.Next() {
		p, err := scanPetition(rows.Scan)
		if err != nil {
			rows.Close()
			return err
		}
		ps = append(ps, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	posted := 0
	for _, p := range ps {
		if err := m.postPetition(ctx, guild, ch, p); err != nil {
			m.log.Warn("pp: posting a petition again", "guild", guild, "err", err)
			continue
		}
		posted++
	}
	_, err = e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(),
		said(fmt.Sprintf("%d of %d petitions posted to <#%s> again", posted, len(ps), ch)), rest.WithCtx(ctx))
	return err
}

// rollSlots is the roll's room until an admin sets it.
const rollSlots = 40

// rolled is how many names the heralds' roll holds: liveries of one's own
// worn and petitions waiting, a member once. s.mu held.
func (s *server) rolled() int64 {
	n := int64(len(s.own))
	for u := range s.asked {
		if _, worn := s.own[u]; !worn {
			n++
		}
	}
	return n
}

// rollFactor is a livery of one's own's price as the roll fills, in
// hundredths: Rules.RollRise percent dearer at full, with the square of
// how full, and dearer still past it. s.mu held.
func (s *server) rollFactor() int64 {
	slots := s.slots
	if slots <= 0 {
		slots = rollSlots
	}
	filled := s.rolled()
	return 100 + Rules.RollRise*filled*filled/(slots*slots)
}

// rollChoices are the room an admin may give the roll.
var rollChoices = []int64{10, 20, 30, 40, 60, 80, 120, 160}
