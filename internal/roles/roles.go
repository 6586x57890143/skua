// Package roles is whether skua may hand a role to anyone who presses for
// it: a self-pick role is a label, never a privilege. notify's ping me and
// forever's role both ask here.
package roles

import (
	"context"
	"slices"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// Grantable is whether role may be handed to anyone who presses for it,
// and skua can: the role carries no permissions at all, so taking it is a
// label and never a privilege (a "mods" role picked must not be one press
// away for everyone), and she holds Manage Roles, or Administrator, with
// her highest role above it. CanGrant also checks the role's channel
// overwrites.
func Grantable(roles []discord.Role, mine []snowflake.ID, guild, role snowflake.ID) bool {
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

// Reader is the REST the checks read with.
type Reader interface {
	GetRoles(guild snowflake.ID, opts ...rest.RequestOpt) ([]discord.Role, error)
	GetMember(guild, user snowflake.ID, opts ...rest.RequestOpt) (*discord.Member, error)
	GetGuildChannels(guild snowflake.ID, opts ...rest.RequestOpt) ([]discord.GuildChannel, error)
}

// ChannelPerms collects role's overwrites across all channels, including
// categories. Threads inherit their parent's, so the guild channel list
// covers them. A malformed channel list is never a safe role check.
func ChannelPerms(channels []discord.GuildChannel, guild, role snowflake.ID) (allow, deny discord.Permissions, valid bool) {
	for _, ch := range channels {
		if ch == nil || ch.GuildID() != guild {
			return 0, 0, false
		}
		if overwrite, ok := ch.PermissionOverwrites().Role(role); ok {
			allow |= overwrite.Allow
			deny |= overwrite.Deny
		}
	}
	return allow, deny, true
}

// CanGrant asks Discord whether role may be handed out in guild by self,
// as it is now. Any failure is a no.
func CanGrant(p Reader, guild, self, role snowflake.ID) bool {
	if role == 0 || self == 0 {
		return false
	}
	roles, err := p.GetRoles(guild)
	if err != nil {
		return false
	}
	me, err := p.GetMember(guild, self)
	if err != nil || me == nil || !Grantable(roles, me.RoleIDs, guild, role) {
		return false
	}
	channels, err := p.GetGuildChannels(guild)
	if err != nil {
		return false
	}
	allow, deny, valid := ChannelPerms(channels, guild, role)
	return valid && allow == 0 && deny == 0
}

// CanDrop checks the other direction: removing a role with channel denials
// can grant access. Allow-only overwrites don't stop someone opting out.
func CanDrop(p Reader, guild, role snowflake.ID) bool {
	channels, err := p.GetGuildChannels(guild)
	if err != nil {
		return false
	}
	_, deny, valid := ChannelPerms(channels, guild, role)
	return valid && deny == 0
}

// WithCtx is a Reader whose reads end with Ctx.
type WithCtx struct {
	R   Reader
	Ctx context.Context
}

func (w WithCtx) GetRoles(guild snowflake.ID, opts ...rest.RequestOpt) ([]discord.Role, error) {
	return w.R.GetRoles(guild, append(opts, rest.WithCtx(w.Ctx))...)
}

func (w WithCtx) GetMember(guild, user snowflake.ID, opts ...rest.RequestOpt) (*discord.Member, error) {
	return w.R.GetMember(guild, user, append(opts, rest.WithCtx(w.Ctx))...)
}

func (w WithCtx) GetGuildChannels(guild snowflake.ID, opts ...rest.RequestOpt) ([]discord.GuildChannel, error) {
	return w.R.GetGuildChannels(guild, append(opts, rest.WithCtx(w.Ctx))...)
}
