package notify

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	rolecheck "github.com/6586x57890143/skua/internal/roles"
)

// A card with a ping role carries a button that hands the role to whoever
// presses it, or takes it back from someone who has it, so members opt in
// from the card itself. It is only there when skua can grant the role.

// roleID is the button's custom ID: "notify-role:<role>".
const roleID = "notify-role"

// grantable is rolecheck.Grantable: the role carries no permissions and skua
// sits above it with Manage Roles.
func grantable(rs []discord.Role, mine []snowflake.ID, guild, role snowflake.ID) bool {
	return rolecheck.Grantable(rs, mine, guild, role)
}

// roleReader is the REST canGrant reads with: the poller's client, or an
// interaction's.
type roleReader = rolecheck.Reader

// canMention checks skua's permissions where the cards will be posted,
// rather than in the channel where an admin opened the panel.
func canMention(ctx context.Context, p rest.Rest, guild, channel, self snowflake.ID) (bool, error) {
	roles, err := p.GetRoles(guild, rest.WithCtx(ctx))
	if err != nil {
		return false, err
	}
	me, err := p.GetMember(guild, self, rest.WithCtx(ctx))
	if err != nil {
		return false, err
	}
	var perms discord.Permissions
	for _, r := range roles {
		if r.ID == guild || slices.Contains(me.RoleIDs, r.ID) {
			perms |= r.Permissions
		}
	}
	if perms.Has(discord.PermissionAdministrator) {
		return true, nil
	}
	ch, err := p.GetChannel(channel, rest.WithCtx(ctx))
	if err != nil {
		return false, err
	}
	gc, ok := ch.(discord.GuildChannel)
	if !ok || gc.GuildID() != guild {
		return false, errors.New("notify: destination isn't a channel in this server")
	}
	ow := gc.PermissionOverwrites()
	if everyone, ok := ow.Role(guild); ok {
		perms = perms.Remove(everyone.Deny).Add(everyone.Allow)
	}
	var allow, deny discord.Permissions
	for _, role := range me.RoleIDs {
		if overwrite, ok := ow.Role(role); ok {
			allow |= overwrite.Allow
			deny |= overwrite.Deny
		}
	}
	perms = perms.Remove(deny).Add(allow)
	if member, ok := ow.Member(self); ok {
		perms = perms.Remove(member.Deny).Add(member.Allow)
	}
	return perms.Has(discord.PermissionMentionEveryone), nil
}

// canGrant asks Discord whether role may be handed out in guild, as it is
// now. Any failure is a no: the card goes out without the button, and a
// press is refused.
func (m *Module) canGrant(p roleReader, guild, role snowflake.ID) bool {
	m.mu.Lock()
	self := m.self
	m.mu.Unlock()
	return rolecheck.CanGrant(p, guild, self, role)
}

// canDrop is rolecheck.CanDrop: a role with channel denials can't be dropped
// from a card, since dropping it could open channels.
func (m *Module) canDrop(p roleReader, guild, role snowflake.ID) bool {
	return rolecheck.CanDrop(p, guild, role)
}

// roleButton is the card's opt-in, blurple so it stands out from the grey
// link beside it: Discord offers four styles, never a custom colour.
func roleButton(role snowflake.ID) discord.ButtonComponent {
	b := discord.NewPrimaryButton("ping me", roleID+":"+role.String())
	// The bell alone, not on its tile: a slate square on blurple is a hole.
	b.Emoji = brand.ComponentEmoji("btn_notify")
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
	// Answered at once, then worked: reading the roles and changing one can
	// take Discord longer than the three seconds a press gets, which on
	// flightless showed as "this interaction failed" on a drop.
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	has := slices.Contains(member.RoleIDs, role)
	r := e.Client().Rest
	// Asked again at the press: the role may have gained permissions, or
	// skua lost hers, since the card went out. Removing a role with channel
	// denials can also grant access, so that direction is checked separately.
	if has && !m.canDrop(withCtx{R: r, Ctx: ctx}, *guild, role) {
		return core.Tell("that role can't be removed from cards; an admin needs to check its channel restrictions")
	}
	if !has && !m.canGrant(withCtx{R: r, Ctx: ctx}, *guild, role) {
		return core.Tell("that role can't be handed out from cards; an admin can pick a role with no permissions or channel overrides of its own as the ping role")
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
	_, err = r.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.MessageUpdate{
		Components:      &[]discord.LayoutComponent{brand.Card(brand.ColorOK, "notify", "", text)},
		Flags:           new(discord.MessageFlagIsComponentsV2),
		AllowedMentions: core.NoPings(),
	}, rest.WithCtx(ctx))
	return err
}

// withCtx is a roleReader whose reads end with ctx.
type withCtx = rolecheck.WithCtx
