package pp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
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
)

var (
	errYardShut  = core.Tell("the dyers' yard hasn't opened yet")
	errNoDye     = core.Tell("the yard doesn't sell that; open the shop again")
	errDyePerch  = core.Tell("the sumptuary roll doesn't let your perch wear that yet; climb the stair first")
	errDyeShort  = core.Tell("a week of that costs more than you hold; keep earning and come back")
	errDyeAhead  = core.Tell("your lease already runs four weeks ahead; the yard won't take more")
	errYardOpens = core.Tell("only admins open the dyers' yard")
)

// dyeing makes sure two first leases of the same livery make one role.
var dyeing sync.Mutex

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
func (st *state) yardMessage(mem *member, page int, today int64) (discord.MessageCreate, error) {
	title := "the sump's trades"
	if page == factionsPage {
		title = "the factions"
	}
	var rows []YardRow
	var look []discord.StringSelectMenuOption
	for _, d := range dyes {
		if d.page() != page {
			continue
		}
		row := YardRow{Name: d.name, Color: d.color, Price: st.leasePrice(d), Worn: mem.livery == d.key}
		if row.Worn {
			row.Left = mem.liveryUntil - today
		}
		if mem.rung < d.rung {
			row.From, _ = perch(d.rung)
		}
		rows = append(rows, row)
		o := discord.NewStringSelectMenuOption(d.name, d.key)
		o.Description = grouped(row.Price/unit) + " pp a week"
		if row.From != "" {
			o.Description = "worn from the " + row.From
		}
		look = append(look, o)
	}
	wear := st.wearing(mem, today)
	pic, err := YardScreen(title, rows, mem.purse, wear)
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
			alt += r.Name + " (" + grouped(r.Price/unit) + " pp a week)"
		}
	}
	alt = clip(alt+". you hold "+grouped(mem.purse/unit)+" pp", altLimit)
	other, otherID := "the factions ▸", "pp:yard:1"
	if page == factionsPage {
		other, otherID = "◂ the sump's trades", "pp:yard:0"
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", "",
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}),
			discord.NewActionRow(discord.NewStringSelectMenu("pp:dye", "look closer at a livery", look...)),
			discord.NewActionRow(discord.NewSecondaryButton("◂ the stair", "pp:shop"), discord.NewSecondaryButton(other, otherID)))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// wearing is the livery mem wears, for a screen's corner. s.mu held.
func (st *state) wearing(mem *member, today int64) Livery {
	d, ok := dyeOf(mem.livery)
	if !ok {
		return Livery{}
	}
	return Livery{Name: d.name, Color: d.color, Left: mem.liveryUntil - today}
}

// dyeMessage is d's own card as mem sees it, with the way back to its page
// and the button to lease it. s.mu held.
func (st *state) dyeMessage(mem *member, d dye, today int64) (discord.MessageCreate, error) {
	price := st.leasePrice(d)
	worn := mem.livery == d.key
	page := "the sump's trades"
	if d.page() == factionsPage {
		page = "the factions"
	}
	it := Item{Kind: "livery", Name: d.name, What: d.what, Story: d.story, Effect: d.effect,
		Price: price, Short: mem.purse < price, Climbed: worn, You: or(mem.name, "you"), Dye: d.color}
	pic, err := ItemScreen(it)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := clip(fmt.Sprintf("%s, a livery of %s, %s pp a week: %s. %s. effect: %s", d.name, page, grouped(price/unit), d.what, d.story, d.effect), altLimit)
	label := "lease a week · " + grouped(price/unit) + " pp"
	if worn {
		label = "another week · " + grouped(price/unit) + " pp"
	}
	lease := withIcon(discord.NewPrimaryButton(label, "pp:lease:"+d.key), "pot")
	switch {
	case mem.rung < d.rung:
		from, _ := perch(d.rung)
		lease = withIcon(discord.NewPrimaryButton("worn from the "+from, "pp:lease:"+d.key), "pot").AsDisabled()
	case mem.purse < price, worn && mem.liveryUntil+leaseDays > today+leaseCap:
		lease = lease.AsDisabled()
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", "",
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}),
			discord.NewActionRow(discord.NewSecondaryButton("◂ the yard", "pp:yard:"+strconv.Itoa(d.page())), lease))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
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
	case strings.HasPrefix(id, "pp:lease:"):
		d, ok := dyeOf(strings.TrimPrefix(id, "pp:lease:"))
		if !ok {
			return errNoDye
		}
		// Making a role and dressing a member are a few calls to Discord,
		// longer than an interaction may wait.
		if err := e.DeferUpdateMessage(); err != nil {
			return err
		}
		msg, err := m.leased(ctx, guild, user, d)
		if err != nil {
			if _, isTell := errors.AsType[core.Tell](err); !isTell {
				m.log.Warn("pp: a lease", "guild", guild, "livery", d.key, "err", err)
				err = core.Tell("the dyers couldn't dress you just now; try again in a while")
			}
			_, ferr := e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(), discord.MessageCreate{
				Content: err.Error(), Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx))
			return ferr
		}
		msg = m.withMenu(guild, user, m.Admin != nil && m.Admin(e), msg, "pp:shop")
		_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(),
			core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...), rest.WithCtx(ctx))
		return err
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
	msg, err := m.st.yardMessage(m.st.member(s, guild, user, today), page, today)
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
// the one they wore before, and gives back d's card as it reads now.
func (m *Module) leased(ctx context.Context, guild, user snowflake.ID, d dye) (discord.MessageCreate, error) {
	today := DayOf(m.now())
	s := m.st.server(guild)
	price := m.st.leasePrice(d)
	check := func(mem *member) error {
		switch {
		case mem.rung < d.rung:
			return errDyePerch
		case mem.purse < price:
			return errDyeShort
		case mem.livery == d.key && mem.liveryUntil+leaseDays > today+leaseCap:
			return errDyeAhead
		}
		return nil
	}
	s.mu.Lock()
	mem := m.st.member(s, guild, user, today)
	err := check(mem)
	old := mem.livery
	s.mu.Unlock()
	if err != nil {
		return discord.MessageCreate{}, err
	}

	role, err := m.dyeRole(ctx, guild, d)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	if old != d.key {
		if err := m.dress(ctx, guild, user, role, true, "leased "+d.name); err != nil {
			return discord.MessageCreate{}, err
		}
		s.mu.Lock()
		was := s.dyeRoles[old]
		s.mu.Unlock()
		if was != 0 {
			if err := m.dress(ctx, guild, user, was, false, "changed livery"); err != nil {
				m.log.Warn("pp: taking off a livery", "guild", guild, "err", err)
			}
		}
	}

	s.mu.Lock()
	mem = m.st.member(s, guild, user, today)
	if err := check(mem); err != nil {
		s.mu.Unlock()
		return discord.MessageCreate{}, err
	}
	from := today
	if mem.livery == d.key {
		from = max(today, mem.liveryUntil)
	}
	mem.purse -= price
	mem.livery, mem.liveryUntil = d.key, from+leaseDays
	m.st.burn(s, user, today, reasonShop, price, []int64{int64(dyeIndex(d.key)), mem.liveryUntil})
	mem.dirty = true
	msg, err := m.st.dyeMessage(mem, d, today)
	s.mu.Unlock()
	// What was paid lands with its ledger row now, not at the minute.
	m.Save()
	return msg, err
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

// dyeRole is d's role in guild, made the first time anyone leases it.
func (m *Module) dyeRole(ctx context.Context, guild snowflake.ID, d dye) (snowflake.ID, error) {
	dyeing.Lock()
	defer dyeing.Unlock()
	s := m.st.server(guild)
	s.mu.Lock()
	role := s.dyeRoles[d.key]
	s.mu.Unlock()
	if role != 0 {
		return role, nil
	}
	role, err := m.makeRole(ctx, guild, look{name: d.name, colors: discord.RoleColors{PrimaryColor: int(d.color)}}, d.name+" first leased")
	if role == 0 {
		return 0, err
	}
	// A role made but not moved under the anchor is still a livery, and
	// kept so a retry doesn't make another.
	if err != nil {
		m.log.Warn("pp: moving a livery under the anchor", "guild", guild, "err", err)
	}
	if _, err := m.db.Exec(ctx, `insert into pp_dye (guild_id, key, role_id) values ($1, $2, $3)
		on conflict (guild_id, key) do update set role_id = excluded.role_id`, int64(guild), d.key, int64(role)); err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.setDye(d.key, role)
	s.mu.Unlock()
	return role, nil
}

// fade takes off every livery whose lease ran out more than fadeDays ago,
// on the day turning. One that can't come off yet is tried again the next.
func (m *Module) fade(ctx context.Context, today int64) {
	type due struct {
		guild, user, role snowflake.ID
	}
	var dues []due
	for g, s := range m.st.servers() {
		s.mu.Lock()
		for u, mem := range s.members {
			if mem.livery != "" && today >= mem.liveryUntil+fadeDays {
				dues = append(dues, due{g, u, s.dyeRoles[mem.livery]})
			}
		}
		s.mu.Unlock()
	}
	for _, d := range dues {
		if d.role != 0 {
			err := m.dress(ctx, d.guild, d.user, d.role, false, "a livery's lease ran out")
			// A role an admin since gave a permission is no livery to take
			// off; only a failure worth retrying keeps the lease on the book.
			if err != nil && !errors.Is(err, errNotLivery) {
				if _, isTell := errors.AsType[core.Tell](err); !isTell {
					m.log.Warn("pp: fading a livery", "guild", d.guild, "err", err)
				}
				continue
			}
		}
		s := m.st.server(d.guild)
		s.mu.Lock()
		if mem := s.members[d.user]; mem != nil && today >= mem.liveryUntil+fadeDays {
			mem.livery, mem.liveryUntil, mem.dirty = "", 0, true
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
