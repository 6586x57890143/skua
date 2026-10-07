package notify

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// A card with a ping role carries a button that hands the role to whoever
// presses it, or takes it back from someone who has it, so members opt in
// from the card itself. It is only there when skua can grant the role.

// roleID is the button's custom ID: "notify-role:<role>".
const roleID = "notify-role"

// grantable is whether role may be handed to anyone who presses for it,
// and skua can: the role carries no permissions at all, so taking it is a
// label and never a privilege (a "mods" role picked as the ping role must
// not be one press away for everyone), and she holds Manage Roles, or
// Administrator, with her highest role above it.
func grantable(roles []discord.Role, mine []snowflake.ID, guild, role snowflake.ID) bool {
	var perms discord.Permissions
	top, target := -1, -1
	for _, r := range roles {
		switch {
		case r.ID == role:
			if r.Managed || r.ID == guild || r.Permissions != 0 {
				return false
			}
			target = r.Position
		case r.ID == guild:
			perms |= r.Permissions
		case slices.Contains(mine, r.ID):
			perms |= r.Permissions
			top = max(top, r.Position)
		}
	}
	if target < 0 || !perms.Has(discord.PermissionManageRoles) && !perms.Has(discord.PermissionAdministrator) {
		return false
	}
	return top > target
}

// roleReader is the REST canGrant reads with: the poller's client, or an
// interaction's.
type roleReader interface {
	GetRoles(guild snowflake.ID, opts ...rest.RequestOpt) ([]discord.Role, error)
	GetMember(guild, user snowflake.ID, opts ...rest.RequestOpt) (*discord.Member, error)
}

// canGrant asks Discord whether role may be handed out in guild, as it is
// now. Any failure is a no: the card goes out without the button, and a
// press is refused.
func (m *Module) canGrant(p roleReader, guild, role snowflake.ID) bool {
	m.mu.Lock()
	self := m.self
	m.mu.Unlock()
	if role == 0 || self == 0 {
		return false
	}
	roles, err := p.GetRoles(guild)
	if err != nil {
		return false
	}
	me, err := p.GetMember(guild, self)
	if err != nil {
		return false
	}
	return grantable(roles, me.RoleIDs, guild, role)
}

// roleButton is the card's opt-in, blurple so it stands out from the grey
// link beside it: Discord offers four styles, never a custom colour.
func roleButton(role snowflake.ID) discord.ButtonComponent {
	b := discord.NewPrimaryButton("ping me", roleID+":"+role.String())
	b.Emoji = brand.ComponentEmoji("mod_notify")
	return b
}

// grab gives the presser the card's role, or takes it back if they have it.
// The role comes from their client, so it must be one this server's follows
// ping; anything else is refused before Discord is asked.
func (m *Module) grab(ctx context.Context, e *events.ComponentInteractionCreate) error {
	guild, member := e.GuildID(), e.Member()
	role, err := snowflake.Parse(strings.TrimPrefix(e.Data.CustomID(), roleID+":"))
	if guild == nil || member == nil || err != nil {
		return core.Tell("that button only works in a server")
	}
	m.mu.Lock()
	ours := slices.ContainsFunc(m.follows, func(f follow) bool { return f.guild == *guild && f.role == role })
	m.mu.Unlock()
	if !ours {
		return core.Tell("this server doesn't hand out that role from cards any more")
	}
	if err := m.guard.Allow(*guild, guard.MemberEdit); err != nil {
		return core.Tell("too many role changes here this hour; try again in a bit")
	}
	has := slices.Contains(member.RoleIDs, role)
	r := e.Client().Rest
	// Asked again at the press: the role may have gained permissions, or
	// skua lost hers, since the card went out. Dropping a role never
	// grants anything, so only taking one is checked.
	if !has && !m.canGrant(r, *guild, role) {
		return core.Tell("that role can't be handed out from cards; an admin can pick a role with no permissions of its own as the ping role")
	}
	opts := []rest.RequestOpt{rest.WithCtx(ctx), rest.WithReason("notify: the member pressed ping me")}
	if has {
		err = r.RemoveMemberRole(*guild, member.User.ID, role, opts...)
	} else {
		err = r.AddMemberRole(*guild, member.User.ID, role, opts...)
	}
	m.guard.Report(*guild, struggling(err))
	if re, ok := errors.AsType[*rest.Error](err); ok && re.Response != nil && re.Response.StatusCode == http.StatusForbidden {
		return core.Tell("skua can't hand out that role right now; an admin needs to give her manage roles above it")
	}
	if err != nil {
		return err
	}
	text := "✓ you'll be pinged with <@&" + role.String() + ">"
	if has {
		text = "✓ no more pings from <@&" + role.String() + ">"
	}
	return e.CreateMessage(discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorOK, "notify", "", text)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
}
