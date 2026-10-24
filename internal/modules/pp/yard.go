package pp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// The dyers' yard is the shop's second room: liveries leased by the week.
// It opens to everyone once an admin presses its one switch on /pp's help
// page, so the owner can announce it; until then only admins see it.
//
// A lease is paid up front and runs leaseDays from the day it's bought, or
// from the day the last one runs out when it's the same livery again, up to
// leaseCap days ahead. A livery not paid for fades fadeDays after its lease
// runs out, when the day turns, and its role comes off.

const (
	leaseDays = 7
	leaseCap  = 28
	fadeDays  = 3
	// Past the bell the dye holds: a lease runs seasonDays for the price
	// of seasonWeeks weeks.
	seasonDays, seasonWeeks = 28, 3
)

// pastTheBell is the first rung above the drowned bell.
const pastTheBell = 6

// term is a lease as a member on rung leases it: its days, the weeks it
// costs, and how far ahead leases may run.
func term(rung int64) (days, weeks, ahead int64) {
	if rung >= pastTheBell {
		return seasonDays, seasonWeeks, 2 * seasonDays
	}
	return leaseDays, 1, leaseCap
}

// termWord is a lease of days as a member reads it.
func termWord(days int64) string {
	if days == seasonDays {
		return "season"
	}
	return "week"
}

var (
	errYardShut  = core.Tell("the dyers' yard hasn't opened yet")
	errNoDye     = core.Tell("the yard doesn't sell that; open the shop again")
	errDyePerch  = core.Tell("the sumptuary roll doesn't let your perch wear that yet; climb the stair first")
	errDyeShort  = core.Tell("a lease of that costs more than you hold; keep earning and come back")
	errDyeAhead  = core.Tell("your lease already runs as far ahead as the yard takes; come back nearer its end")
	errYardOpens = core.Tell("only admins open the dyers' yard")
)

// sees is whether i may see the yard: it's open, or i is an admin.
func (m *Module) sees(guild snowflake.ID, i discord.Interaction) bool {
	s := m.st.server(guild)
	s.mu.Lock()
	open := s.yardOpen
	s.mu.Unlock()
	return open || m.Admin != nil && m.Admin(i)
}

// loadDyes reads each server's livery roles.
func (st *state) loadDyes(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select guild_id, key, role_id from pp_dye`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guild, role int64
		var key string
		if err := rows.Scan(&guild, &key, &role); err != nil {
			return err
		}
		s := st.server(snowflake.ID(guild))
		s.mu.Lock()
		s.setDye(key, snowflake.ID(role))
		s.mu.Unlock()
	}
	return rows.Err()
}

// setDye keeps key's role. s.mu held.
func (s *server) setDye(key string, role snowflake.ID) {
	if s.dyeRoles == nil {
		s.dyeRoles = map[string]snowflake.ID{}
	}
	s.dyeRoles[key] = role
}

// leasePrice is a week of d. st's pot day is the unit.
func (st *state) leasePrice(d dye) int64 { return st.pot() * d.price / 100 }

// yardMessage is page of the yard as mem sees it. s.mu held.
func (st *state) yardMessage(s *server, user snowflake.ID, mem *member, page int, today int64) (discord.MessageCreate, error) {
	title := "the sump's trades"
	if page == factionsPage {
		title = "the factions"
	}
	days, _, _ := term(mem.rung)
	var rows []YardRow
	var look []discord.StringSelectMenuOption
	for _, d := range dyes {
		if d.page() != page {
			continue
		}
		_, weeks, _ := term(mem.rung)
		row := YardRow{Name: d.name, Color: d.color, Price: st.leasePrice(d) * weeks, Worn: mem.livery == d.key}
		if row.Worn {
			row.Left = mem.liveryUntil - today
		}
		if mem.rung < d.rung {
			row.From, _ = perch(d.rung)
		}
		rows = append(rows, row)
		o := discord.NewStringSelectMenuOption(d.name, d.key)
		o.Description = grouped(row.Price/unit) + " pp a " + termWord(days)
		if row.From != "" {
			o.Description = "worn from the " + row.From
		}
		look = append(look, o)
	}
	wear := st.wearing(s, user, mem, today)
	pic, err := YardScreen(title, rows, mem.purse, wear, termWord(days))
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := "the dyers' yard, " + title + ": "
	for i, r := range rows {
		if i > 0 {
			alt += ", "
		}
		switch {
		case r.Worn:
			alt += r.Name + " (yours, " + leftWords(r.Left) + ")"
		case r.From != "":
			alt += r.Name + " (from the " + r.From + ")"
		default:
			alt += r.Name + " (" + grouped(r.Price/unit) + " pp a " + termWord(days) + ")"
		}
	}
	alt = clip(alt+". you hold "+grouped(mem.purse/unit)+" pp", altLimit)
	other, otherID := "the factions ▸", "pp:yard:1"
	if page == factionsPage {
		other, otherID = "◂ the sump's trades", "pp:yard:0"
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", "",
			discord.NewMediaGallery(screenItem(pic, alt)),
			discord.NewActionRow(discord.NewStringSelectMenu("pp:dye", "look closer at a livery", look...)),
			yardRow(discord.NewSecondaryButton("◂ the stair", "pp:back"), discord.NewSecondaryButton(other, otherID), mem.rung >= ownStyles[0].rung))},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// wearing is the livery mem wears, for a screen's corner. s.mu held.
func (st *state) wearing(s *server, user snowflake.ID, mem *member, today int64) Livery {
	if mem.livery == ownKey {
		return Livery{Name: "your own livery", Color: s.own[user].color, Left: mem.liveryUntil - today}
	}
	d, ok := dyeOf(mem.livery)
	if !ok {
		return Livery{}
	}
	return Livery{Name: d.name, Color: d.color, Left: mem.liveryUntil - today}
}

// dyeMessage is d's own card as mem sees it, with the way back to its page
// and the button to lease it. s.mu held.
func (st *state) dyeMessage(mem *member, d dye, today int64) (discord.MessageCreate, error) {
	days, weeks, ahead := term(mem.rung)
	price := st.leasePrice(d) * weeks
	worn := mem.livery == d.key
	page := "the sump's trades"
	if d.page() == factionsPage {
		page = "the factions"
	}
	effect := strings.Replace(d.effect, "for a week", "for a "+termWord(days), 1)
	it := Item{Kind: "livery", Name: d.name, What: d.what, Story: d.story, Effect: effect,
		Price: price, Short: mem.purse < price, Climbed: worn, You: or(mem.name, "you"), Dye: d.color, Garment: garments[d.key]}
	pic, err := ItemScreen(it)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := clip(fmt.Sprintf("%s, a livery of %s, %s pp a %s: %s. %s. effect: %s", d.name, page, grouped(price/unit), termWord(days), d.what, d.story, effect), altLimit)
	label := "lease a " + termWord(days) + " · " + grouped(price/unit) + " pp"
	if worn {
		label = "another " + termWord(days) + " · " + grouped(price/unit) + " pp"
	}
	lease := withIcon(discord.NewPrimaryButton(label, "pp:lease:"+d.key), "pot")
	switch {
	case mem.rung < d.rung:
		from, _ := perch(d.rung)
		lease = withIcon(discord.NewPrimaryButton("worn from the "+from, "pp:lease:"+d.key), "pot").AsDisabled()
	case mem.purse < price, worn && mem.liveryUntil+days > today+ahead:
		lease = lease.AsDisabled()
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", "",
			discord.NewMediaGallery(screenItem(pic, alt)),
			discord.NewActionRow(discord.NewSecondaryButton("◂ the yard", "pp:yard:"+strconv.Itoa(d.page())), lease))},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// yard is a press in the dyers' yard: a page, a livery's card, or a lease.
func (m *Module) yard(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if !m.sees(guild, e) {
		return errYardShut
	}
	id := e.Data.CustomID()
	user := e.User().ID
	switch {
	case id == "pp:ownwrite":
		return m.openOwn(guild, e)
	case id == "pp:own":
		msg, err := m.ownPage(ctx, guild, user)
		if err != nil {
			return err
		}
		return m.answer(guild, e, msg, "pp:shop")
	case id == "pp:ownsend", id == "pp:ownweek":
		// A send posts to the heralds and a week dresses the member: both
		// longer than an interaction may wait.
		return m.deferred(ctx, guild, e, func(ctx context.Context) error {
			if id == "pp:ownsend" {
				return m.sendPetition(ctx, guild, user)
			}
			return m.leaseOwn(ctx, guild, user)
		}, func(ctx context.Context) (discord.MessageCreate, error) { return m.ownPage(ctx, guild, user) })
	case strings.HasPrefix(id, "pp:lease:"):
		d, ok := dyeOf(strings.TrimPrefix(id, "pp:lease:"))
		if !ok {
			return errNoDye
		}
		// Making a role and dressing a member are a few calls to Discord,
		// longer than an interaction may wait.
		var msg discord.MessageCreate
		return m.deferred(ctx, guild, e, func(ctx context.Context) (err error) {
			msg, err = m.leased(ctx, guild, user, d)
			return err
		}, func(context.Context) (discord.MessageCreate, error) { return msg, nil })
	case id == "pp:dye":
		data, ok := e.Data.(discord.StringSelectMenuInteractionData)
		if !ok || len(data.Values) != 1 {
			return errNoDye
		}
		d, ok := dyeOf(data.Values[0])
		if !ok {
			return errNoDye
		}
		return m.dyeCard(guild, e, d)
	}
	page := tradesPage
	if id == "pp:yard:1" {
		page = factionsPage
	}
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	msg, err := m.st.yardMessage(s, user, m.st.member(s, guild, user, today), page, today)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return m.answer(guild, e, msg, "pp:shop")
}

// dyeCard shows d's card.
func (m *Module) dyeCard(guild snowflake.ID, e *events.ComponentInteractionCreate, d dye) error {
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	msg, err := m.st.dyeMessage(m.st.member(s, guild, e.User().ID, today), d, today)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return m.answer(guild, e, msg, "pp:shop")
}

// leased leases user a week of d, dressing them in its role and taking off
// the one they wore before, and gives back d's card as it reads now. The
// server's lease lock is held throughout, so a double press or a fade
// can't interleave with it, and if the purse can no longer pay once the
// member is dressed, the dressing is undone.
func (m *Module) leased(ctx context.Context, guild, user snowflake.ID, d dye) (discord.MessageCreate, error) {
	today := DayOf(m.now())
	s := m.st.server(guild)
	check := func(mem *member) (price, days int64, err error) {
		days, weeks, ahead := term(mem.rung)
		price = m.st.leasePrice(d) * weeks
		switch {
		case mem.rung < d.rung:
			return 0, 0, errDyePerch
		case mem.purse < price:
			return 0, 0, errDyeShort
		case mem.livery == d.key && mem.liveryUntil+days > today+ahead:
			return 0, 0, errDyeAhead
		}
		return price, days, nil
	}
	s.leasing.Lock()
	defer s.leasing.Unlock()
	s.mu.Lock()
	mem := m.st.member(s, guild, user, today)
	_, _, err := check(mem)
	old, was := mem.livery, wornRole(s, user, mem)
	s.mu.Unlock()
	if err != nil {
		return discord.MessageCreate{}, err
	}

	roles, at, role, err := m.dyeRole(ctx, guild, d)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	// Dressed even when it's the livery they lease already: the role may
	// have come off by hand, or with a leave and a return.
	if err := m.dressIn(ctx, guild, roles, at, user, role, true, "leased "+d.name); err != nil {
		return discord.MessageCreate{}, dyeErr(err)
	}
	swapped := old != d.key && was != 0
	if swapped {
		if err := m.dressIn(ctx, guild, roles, at, user, was, false, "changed livery"); err != nil && !errors.Is(err, errNotLivery) {
			m.undress(ctx, guild, roles, at, user, role)
			return discord.MessageCreate{}, err
		}
	}

	s.mu.Lock()
	mem = m.st.member(s, guild, user, today)
	price, days, err := check(mem)
	if err != nil {
		s.mu.Unlock()
		if old != d.key {
			m.undress(ctx, guild, roles, at, user, role)
			if swapped {
				_ = m.dressIn(ctx, guild, roles, at, user, was, true, "a livery change undone")
			}
		}
		return discord.MessageCreate{}, err
	}
	from := today
	if mem.livery == d.key {
		from = max(today, mem.liveryUntil)
	}
	mem.purse -= price
	mem.livery, mem.liveryUntil = d.key, from+days
	m.st.burn(s, user, today, reasonLease, price, []int64{int64(dyeIndex(d.key)), mem.liveryUntil})
	mem.dirty = true
	if old == ownKey {
		delete(s.own, user)
	}
	msg, err := m.st.dyeMessage(mem, d, today)
	s.mu.Unlock()
	// What was paid lands with its ledger row now, not at the minute.
	m.Save()
	// A livery of one's own given up for a trade's or a faction's goes as
	// it would have faded: its role and its petition with it.
	if old == ownKey && was != 0 {
		m.retireOwn(ctx, guild, was)
	}
	return msg, err
}

// retireOwn deletes a livery of one's own's role and lapses its petition.
func (m *Module) retireOwn(ctx context.Context, guild, role snowflake.ID) {
	if err := m.dropRole(ctx, guild, role, "a livery of their own given up"); err != nil && !errors.Is(err, errNotLivery) {
		m.log.Warn("pp: deleting a livery given up", "guild", guild, "err", err)
	}
	if _, err := m.db.Exec(ctx, `update pp_petition set state = 'lapsed' where guild_id = $1 and role_id = $2 and state = 'granted'`,
		int64(guild), int64(role)); err != nil {
		m.log.Warn("pp: lapsing a petition", "guild", guild, "err", err)
	}
}

// undress takes role back off user after a lease that didn't go through,
// saying so if even that fails.
func (m *Module) undress(ctx context.Context, guild snowflake.ID, roles []discord.Role, at int, user, role snowflake.ID) {
	if err := m.dressIn(ctx, guild, roles, at, user, role, false, "a lease that didn't go through"); err != nil {
		m.log.Warn("pp: undoing a livery", "guild", guild, "err", err)
	}
}

// errDyeMoved is a livery whose role an admin has since changed so that
// skua won't touch it.
var errDyeMoved = core.Tell("this livery's role was changed by hand: an admin has to move it back under the livery anchor, with no permissions")

// dyeErr is err as a member leasing reads it.
func dyeErr(err error) error {
	if errors.Is(err, errNotLivery) {
		return errDyeMoved
	}
	return err
}

// dyeIndex is key's place in dyes, the ledger's record of what was leased.
func dyeIndex(key string) int {
	for i, d := range dyes {
		if d.key == key {
			return i
		}
	}
	return -1
}

// dyeRole is d's role in guild with guild's roles and the anchor's
// position, read once for the whole lease. A role that was never made, or
// that an admin deleted, is made now. s.leasing held.
func (m *Module) dyeRole(ctx context.Context, guild snowflake.ID, d dye) ([]discord.Role, int, snowflake.ID, error) {
	roles, at, err := m.standing(ctx, guild)
	if err != nil {
		return nil, 0, 0, err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	role := s.dyeRoles[d.key]
	s.mu.Unlock()
	if role != 0 && slices.ContainsFunc(roles, func(r discord.Role) bool { return r.ID == role }) {
		return roles, at, role, nil
	}
	role, err = m.makeRole(ctx, guild, look{name: d.name, colors: discord.RoleColors{PrimaryColor: int(d.color)}}, d.name+" first leased")
	if role == 0 {
		return nil, 0, 0, err
	}
	// A role made but not moved under the anchor is still a livery.
	if err != nil {
		m.log.Warn("pp: moving a livery under the anchor", "guild", guild, "err", err)
	}
	// Kept in memory first, so a database that fails now doesn't make the
	// next lease make another.
	s.mu.Lock()
	s.setDye(d.key, role)
	s.mu.Unlock()
	if _, err := m.db.Exec(ctx, `insert into pp_dye (guild_id, key, role_id) values ($1, $2, $3)
		on conflict (guild_id, key) do update set role_id = excluded.role_id`, int64(guild), d.key, int64(role)); err != nil {
		m.log.Warn("pp: keeping a livery's role", "guild", guild, "livery", d.key, "err", err)
	}
	roles, at, err = m.standing(ctx, guild)
	return roles, at, role, err
}

// fade takes off every livery whose lease ran out more than fadeDays ago,
// on the day turning, reading each server's roles once. One that can't
// come off yet is tried again the next turn.
func (m *Module) fade(ctx context.Context, today int64) {
	for g, s := range m.st.servers() {
		s.mu.Lock()
		due := false
		for _, mem := range s.members {
			due = due || mem.livery != "" && today >= mem.liveryUntil+fadeDays
		}
		s.mu.Unlock()
		if due {
			m.fadeIn(ctx, g, s, today)
		}
	}
}

// fadeIn is fade in one server.
func (m *Module) fadeIn(ctx context.Context, guild snowflake.ID, s *server, today int64) {
	s.leasing.Lock()
	defer s.leasing.Unlock()
	roles, at, err := m.standing(ctx, guild)
	if err != nil {
		if _, isTell := errors.AsType[core.Tell](err); !isTell {
			m.log.Warn("pp: fading liveries", "guild", guild, "err", err)
		}
		return
	}
	type due struct {
		user, role snowflake.ID
		own        bool
	}
	var dues []due
	s.mu.Lock()
	for u, mem := range s.members {
		if mem.livery != "" && today >= mem.liveryUntil+fadeDays {
			d := due{u, s.dyeRoles[mem.livery], mem.livery == ownKey}
			if d.own {
				d.role = s.own[u].role
			}
			dues = append(dues, d)
		}
	}
	s.mu.Unlock()
	for _, d := range dues {
		// A role an admin since gave a permission, or deleted, is no
		// livery to take off; only a failure worth retrying keeps the
		// lease on the book.
		if d.role != 0 && slices.ContainsFunc(roles, func(r discord.Role) bool { return r.ID == d.role }) {
			err := m.dressIn(ctx, guild, roles, at, d.user, d.role, false, "a livery's lease ran out")
			if err != nil && !errors.Is(err, errNotLivery) {
				m.log.Warn("pp: fading a livery", "guild", guild, "err", err)
				continue
			}
		}
		// A livery of one's own is its member's alone: its role goes with
		// it, and its petition lapses.
		if d.own && d.role != 0 {
			if err := m.dropRole(ctx, guild, d.role, "a livery's lease ran out"); err != nil && !errors.Is(err, errNotLivery) {
				m.log.Warn("pp: deleting a faded livery", "guild", guild, "err", err)
				continue
			}
			if _, err := m.db.Exec(ctx, `update pp_petition set state = 'lapsed' where guild_id = $1 and role_id = $2 and state = 'granted'`,
				int64(guild), int64(d.role)); err != nil {
				m.log.Warn("pp: lapsing a petition", "guild", guild, "err", err)
				continue
			}
		}
		s.mu.Lock()
		if mem := s.members[d.user]; mem != nil && today >= mem.liveryUntil+fadeDays {
			mem.livery, mem.liveryUntil, mem.dirty = "", 0, true
			if d.own {
				delete(s.own, d.user)
			}
		}
		s.mu.Unlock()
	}
}

// yardControls is the switch that opens the yard, on /pp's help page until
// it's pressed.
func (m *Module) yardControls(guild snowflake.ID) []discord.ContainerSubComponent {
	s := m.st.server(guild)
	s.mu.Lock()
	open := s.yardOpen
	s.mu.Unlock()
	if open {
		return nil
	}
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay("### the dyers' yard\n-# liveries are for admins only until this is pressed. it opens the yard to everyone, once, and can't be undone"),
		discord.NewActionRow(discord.NewPrimaryButton("open the dyers' yard", "pp:openyard")),
	}
}

// openYard opens the yard to everyone, for good.
func (m *Module) openYard(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errYardOpens
	}
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, yard_open) values ($1, true)
		on conflict (guild_id) do update set yard_open = true`, int64(guild)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	s.yardOpen = true
	s.mu.Unlock()
	return e.CreateMessage(discord.MessageCreate{Content: "the dyers' yard is open: everyone can lease a livery from the shop now",
		Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}

// yardRow is the yard's buttons: back to the stair, the other page and,
// from the chitin perch, a livery of one's own.
func yardRow(back, other discord.ButtonComponent, own bool) discord.ActionRowComponent {
	row := discord.NewActionRow(back, other)
	if own {
		row = row.AddComponents(withIcon(discord.NewPrimaryButton("a livery of your own", "pp:own"), "quill"))
	}
	return row
}
