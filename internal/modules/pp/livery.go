package pp

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// Liveries are roles: a trade's or a faction's colours, or a livery of a
// member's own, leased from the shop. skua makes them and hands them out
// herself, so she holds Manage Roles, and everything here is fenced so
// that power never reaches further than a coloured name:
//
//   - every role she makes carries no permissions, isn't hoisted or
//     mentionable, and sits directly under the anchor role an admin picked;
//   - she touches only a role that is still bare (no permissions, not
//     managed, not @everyone) and below the anchor, checked again from
//     Discord before every write, so a role an admin later gave a
//     permission is left alone;
//   - she writes nothing unless her own highest role is above the anchor;
//   - every write goes through the guard and names itself in the audit log.

var (
	errNoAnchor   = core.Tell("liveries need an anchor role first: an admin picks it on /pp's help page")
	errNoManage   = core.Tell("skua needs manage roles to hand out liveries here")
	errUnderSkua  = core.Tell("skua's highest role has to sit above the livery anchor; move her up in the server's roles")
	errNotLivery  = core.Tell("that role isn't a livery skua may touch")
	errNotStarted = core.Tell("skua is still waking up; try again in a moment")
)

// look is how a livery's role looks: its name, its colours (a second
// makes a gradient, a third holographic) and its icon as a PNG, or none.
type look struct {
	name   string
	colors discord.RoleColors
	icon   []byte
}

// standing reads guild's roles and checks that skua may dress members
// there: she has Manage Roles and her highest role is above the anchor.
// It gives the roles and the anchor's position.
func (m *Module) standing(ctx context.Context, guild snowflake.ID) ([]discord.Role, int, error) {
	s := m.st.server(guild)
	s.mu.Lock()
	anchor := s.anchor
	s.mu.Unlock()
	if anchor == 0 {
		return nil, 0, errNoAnchor
	}
	if m.rest == nil || m.self == 0 {
		return nil, 0, errNotStarted
	}
	roles, err := m.rest.GetRoles(guild, rest.WithCtx(ctx))
	if err != nil {
		return nil, 0, err
	}
	me, err := m.rest.GetMember(guild, m.self, rest.WithCtx(ctx))
	if err != nil {
		return nil, 0, err
	}
	var perms discord.Permissions
	top, at := -1, -1
	for _, r := range roles {
		switch {
		case r.ID == guild:
			perms |= r.Permissions
		case slices.Contains(me.RoleIDs, r.ID):
			perms |= r.Permissions
			top = max(top, r.Position)
		}
		if r.ID == anchor {
			at = r.Position
		}
	}
	switch {
	case at < 0:
		return nil, 0, errNoAnchor
	case !perms.Has(discord.PermissionManageRoles) && !perms.Has(discord.PermissionAdministrator):
		return nil, 0, errNoManage
	case top <= at:
		return nil, 0, errUnderSkua
	}
	return roles, at, nil
}

// bare is whether role is one skua may touch: it exists, it is no
// integration's and not @everyone, it carries no permissions, and it sits
// under the anchor at position at.
func bare(roles []discord.Role, guild, role snowflake.ID, at int) bool {
	i := slices.IndexFunc(roles, func(r discord.Role) bool { return r.ID == role })
	if i < 0 {
		return false
	}
	r := roles[i]
	return r.ID != guild && !r.Managed && r.Permissions == 0 && r.Position < at
}

// write spends one guard op on guild and does it, telling the guard how
// Discord took it.
func (m *Module) write(guild snowflake.ID, op guard.Op, do func() error) error {
	if err := m.Guard.Allow(guild, op); err != nil {
		return err
	}
	err := do()
	m.Guard.Report(guild, struggling(err))
	return err
}

// makeRole makes a livery's role in guild and moves it directly under the
// anchor. A role made but not moved is still bare, so its ID comes back
// with the error and the caller keeps it.
func (m *Module) makeRole(ctx context.Context, guild snowflake.ID, l look, why string) (snowflake.ID, error) {
	_, at, err := m.standing(ctx, guild)
	if err != nil {
		return 0, err
	}
	none := discord.Permissions(0)
	create := discord.RoleCreate{Name: l.name, Permissions: &none, Colors: l.colors}
	if l.icon != nil {
		create.Icon = discord.NewIconRaw(discord.IconTypePNG, l.icon)
	}
	var made *discord.Role
	if err := m.write(guild, guard.RoleWrite, func() (err error) {
		made, err = m.rest.CreateRole(guild, create, rest.WithCtx(ctx), rest.WithReason("pp: a livery, "+why))
		return err
	}); err != nil {
		return 0, err
	}
	// The anchor's own position: the new role takes it and the anchor
	// moves up one, so the livery sits directly under it.
	return made.ID, m.write(guild, guard.RoleWrite, func() error {
		_, err := m.rest.UpdateRolePositions(guild, []discord.RolePositionUpdate{{ID: made.ID, Position: &at}},
			rest.WithCtx(ctx), rest.WithReason("pp: under the livery anchor"))
		return err
	})
}

// editRole gives a livery's role a new look.
func (m *Module) editRole(ctx context.Context, guild, role snowflake.ID, l look, why string) error {
	roles, at, err := m.standing(ctx, guild)
	if err != nil {
		return err
	}
	if !bare(roles, guild, role, at) {
		return errNotLivery
	}
	update := discord.RoleUpdate{Name: &l.name, Colors: omit.NewPtr(l.colors)}
	if l.icon != nil {
		update.Icon = omit.New(discord.NewIconRaw(discord.IconTypePNG, l.icon))
	}
	return m.write(guild, guard.RoleWrite, func() error {
		_, err := m.rest.UpdateRole(guild, role, update, rest.WithCtx(ctx), rest.WithReason("pp: a livery, "+why))
		return err
	})
}

// dropRole deletes a livery's role. One already gone is dropped.
func (m *Module) dropRole(ctx context.Context, guild, role snowflake.ID, why string) error {
	roles, at, err := m.standing(ctx, guild)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(roles, func(r discord.Role) bool { return r.ID == role }) {
		return nil
	}
	if !bare(roles, guild, role, at) {
		return errNotLivery
	}
	return m.write(guild, guard.RoleWrite, func() error {
		err := m.rest.DeleteRole(guild, role, rest.WithCtx(ctx), rest.WithReason("pp: "+why))
		if gone(err) {
			return nil
		}
		return err
	})
}

// dress puts user in a livery's role, or takes them out of it.
func (m *Module) dress(ctx context.Context, guild, user, role snowflake.ID, on bool, why string) error {
	roles, at, err := m.standing(ctx, guild)
	if err != nil {
		return err
	}
	return m.dressIn(ctx, guild, roles, at, user, role, on, why)
}

// dressIn is dress with guild's roles and the anchor's position already
// read by standing, for a caller dressing several members at once.
func (m *Module) dressIn(ctx context.Context, guild snowflake.ID, roles []discord.Role, at int, user, role snowflake.ID, on bool, why string) error {
	if !bare(roles, guild, role, at) {
		return errNotLivery
	}
	opts := []rest.RequestOpt{rest.WithCtx(ctx), rest.WithReason("pp: " + why)}
	return m.write(guild, guard.MemberRole, func() error {
		if on {
			return m.rest.AddMemberRole(guild, user, role, opts...)
		}
		err := m.rest.RemoveMemberRole(guild, user, role, opts...)
		if gone(err) {
			return nil
		}
		return err
	})
}

// gone is whether Discord said what was asked about no longer exists.
func gone(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	return ok && re.Response != nil && re.Response.StatusCode == http.StatusNotFound
}

// liveryControls is the way from /pp's help page to the livery settings.
// They have a panel of their own: a help page holds at most 40
// components, and /pp's other settings already take most of them.
func (m *Module) liveryControls() []discord.ContainerSubComponent {
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay("### liveries\n-# the role liveries sit under, where petitions are reviewed, and the switch that opens the dyers' yard"),
		discord.NewActionRow(withIcon(discord.NewSecondaryButton("livery settings", "pp:liveries"), "scales")),
	}
}

// liveryPanel is the livery settings, for an admin only.
func (m *Module) liveryPanel(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	return e.CreateMessage(discord.MessageCreate{
		Components:      []discord.LayoutComponent{discord.NewContainer(m.liverySettings(guild)...).WithAccentColor(brand.ColorNotice)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
}

// liverySettings are the anchor role, the heralds' channel and, until it's
// pressed, the yard's switch.
func (m *Module) liverySettings(guild snowflake.ID) []discord.ContainerSubComponent {
	s := m.st.server(guild)
	s.mu.Lock()
	anchor, heralds := s.anchor, s.heralds
	s.mu.Unlock()
	role := discord.NewRoleSelectMenu("pp:anchor", "pick the role liveries sit under").WithMinValues(0)
	if anchor != 0 {
		role = role.AddDefaultValue(anchor)
	}
	ch := discord.ChannelSelectMenuComponent{CustomID: "pp:heralds", Placeholder: "pick where petitions are reviewed", ChannelTypes: logChannels, MinValues: new(0), MaxValues: 1}
	if heralds != 0 {
		ch.DefaultValues = []discord.SelectMenuDefaultValue{discord.NewSelectMenuDefaultChannel(heralds)}
	}
	return append([]discord.ContainerSubComponent{
		discord.NewTextDisplay("### liveries\n-# roles bought in the shop sit directly under the anchor, so put it above the server's other colour roles and below staff; skua's own role must be above it. petitions for a livery of one's own go to the heralds' channel for a moderator to grant or refuse"),
		discord.NewActionRow(role),
		discord.NewActionRow(ch),
	}, m.yardControls(guild)...)
}

// setLivery keeps the anchor or the heralds' channel an admin picked, or
// none when they cleared it, and says so.
func (m *Module) setLivery(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	var picked snowflake.ID
	column, said := "livery_anchor", "no livery anchor now: the shop sells no liveries here"
	switch data := e.Data.(type) {
	case discord.RoleSelectMenuInteractionData:
		if e.Data.CustomID() != "pp:anchor" {
			return errNotAdmin
		}
		if len(data.Values) > 0 {
			picked = data.Values[0]
			said = "liveries sit directly under <@&" + picked.String() + "> now"
		}
	case discord.ChannelSelectMenuInteractionData:
		if e.Data.CustomID() != "pp:heralds" {
			return errNotAdmin
		}
		column, said = "heralds_channel", "no heralds' channel now: petitions for a livery of one's own wait unread"
		if len(data.Values) > 0 {
			picked = data.Values[0]
			said = "petitions for a livery of one's own go to <#" + picked.String() + "> now"
		}
	default:
		return errNotAdmin
	}
	// column is one of two constants, never input.
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, `+column+`) values ($1, $2)
		on conflict (guild_id) do update set `+column+` = excluded.`+column, int64(guild), int64(picked)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	if column == "livery_anchor" {
		s.anchor = picked
	} else {
		s.heralds = picked
	}
	s.mu.Unlock()
	return e.CreateMessage(discord.MessageCreate{Content: said, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}
