package roles

import (
	"context"
	"errors"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

type reader struct {
	roles    []discord.Role
	mine     []snowflake.ID
	channels []discord.GuildChannel
	err      error
}

func (r reader) GetRoles(snowflake.ID, ...rest.RequestOpt) ([]discord.Role, error) {
	return r.roles, r.err
}

func (r reader) GetMember(_, user snowflake.ID, _ ...rest.RequestOpt) (*discord.Member, error) {
	return &discord.Member{User: discord.User{ID: user}, RoleIDs: r.mine}, r.err
}

func (r reader) GetGuildChannels(snowflake.ID, ...rest.RequestOpt) ([]discord.GuildChannel, error) {
	return r.channels, r.err
}

func TestGrant(t *testing.T) {
	const guild, role, mine = 3, 7, 9
	base := []discord.Role{{ID: guild}, {ID: role, Position: 1}, {ID: mine, Position: 5, Permissions: discord.PermissionManageRoles}}
	ok := reader{roles: base, mine: []snowflake.ID{mine}}
	if !CanGrant(WithCtx{R: ok, Ctx: context.Background()}, guild, 1, role) || !CanDrop(WithCtx{R: ok, Ctx: context.Background()}, guild, role) {
		t.Fatal("a plain role below skua's")
	}
	for name, r := range map[string]reader{
		"privileged": {roles: []discord.Role{{ID: guild}, {ID: role, Position: 1, Permissions: discord.PermissionKickMembers}, base[2]}, mine: []snowflake.ID{mine}},
		"above":      {roles: []discord.Role{{ID: guild}, {ID: role, Position: 9}, base[2]}, mine: []snowflake.ID{mine}},
		"no manage":  {roles: []discord.Role{{ID: guild}, {ID: role, Position: 1}, {ID: mine, Position: 5}}, mine: []snowflake.ID{mine}},
		"failing":    {err: errors.New("down")},
	} {
		if CanGrant(r, guild, 1, role) {
			t.Errorf("%s: granted", name)
		}
	}
	if CanGrant(ok, guild, 0, role) || CanGrant(ok, guild, 1, 0) || CanDrop(reader{err: errors.New("down")}, guild, role) {
		t.Fatal("unset or failing")
	}
	admin := []discord.Role{{ID: guild}, {ID: role, Position: 1}, {ID: mine, Position: 5, Permissions: discord.PermissionAdministrator}}
	if !Grantable(admin, []snowflake.ID{mine}, guild, role) || Grantable(admin, nil, guild, guild) {
		t.Fatal("administrator, or the everyone role")
	}
	ch := discord.GuildTextChannel{}
	if _, _, valid := ChannelPerms([]discord.GuildChannel{ch, nil}, guild, role); valid {
		t.Fatal("a malformed channel list passed")
	}
}
