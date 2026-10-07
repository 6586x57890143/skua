package notify

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
)

// In guild 3: @everyone (3), skua's role (5) at position 4, and the ping
// role (7) at position 2.
func guildRoles(skuaPerms discord.Permissions, pingPos int, managed bool) []discord.Role {
	return []discord.Role{
		{ID: 3, Position: 0},
		{ID: 5, Position: 4, Permissions: skuaPerms},
		{ID: 7, Position: pingPos, Managed: managed},
	}
}

func TestGrantable(t *testing.T) {
	for name, c := range map[string]struct {
		roles []discord.Role
		mine  []snowflake.ID
		role  snowflake.ID
		want  bool
	}{
		"manage roles, above":   {guildRoles(discord.PermissionManageRoles, 2, false), []snowflake.ID{5}, 7, true},
		"administrator, above":  {guildRoles(discord.PermissionAdministrator, 2, false), []snowflake.ID{5}, 7, true},
		"manage roles, level":   {guildRoles(discord.PermissionManageRoles, 4, false), []snowflake.ID{5}, 7, false},
		"manage roles, below":   {guildRoles(discord.PermissionManageRoles, 6, false), []snowflake.ID{5}, 7, false},
		"no permission":         {guildRoles(discord.PermissionSendMessages, 2, false), []snowflake.ID{5}, 7, false},
		"a bot's managed role":  {guildRoles(discord.PermissionManageRoles, 2, true), []snowflake.ID{5}, 7, false},
		"@everyone":             {guildRoles(discord.PermissionManageRoles, 2, false), []snowflake.ID{5}, 3, false},
		"a role that's gone":    {guildRoles(discord.PermissionManageRoles, 2, false), []snowflake.ID{5}, 9, false},
		"skua holds no role":    {guildRoles(discord.PermissionManageRoles, 2, false), nil, 7, false},
		"everyone may manage":   {[]discord.Role{{ID: 3, Permissions: discord.PermissionManageRoles}, {ID: 5, Position: 4}, {ID: 7, Position: 2}}, []snowflake.ID{5}, 7, true},
		"a role with any power": {[]discord.Role{{ID: 3}, {ID: 5, Position: 4, Permissions: discord.PermissionManageRoles}, {ID: 7, Position: 2, Permissions: discord.PermissionBanMembers}}, []snowflake.ID{5}, 7, false},
		"a role that's a label": {[]discord.Role{{ID: 3}, {ID: 5, Position: 4, Permissions: discord.PermissionAdministrator}, {ID: 7, Position: 2}}, []snowflake.ID{5}, 7, true},
	} {
		if got := grantable(c.roles, c.mine, 3, c.role); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestCanGrantAsksDiscord(t *testing.T) {
	m := module(t, newFake())
	p := &poster{roles: guildRoles(discord.PermissionManageRoles, 2, false), mine: []snowflake.ID{5}}
	if m.canGrant(p, 3, 7) {
		t.Fatal("before Ready skua doesn't know who she is")
	}
	m.self = 2
	if !m.canGrant(p, 3, 7) || m.canGrant(p, 3, 0) {
		t.Fatal("canGrant")
	}
	p.roleErr = errors.New("down")
	if m.canGrant(p, 3, 7) {
		t.Fatal("a failed read is a no")
	}
}

func TestPingMeButtonOnlyWhenItCanBeHonoured(t *testing.T) {
	src := newFake()
	m := module(t, src)
	m.self = 2
	m.follows = []follow{{guild: 3, channel: 10, platform: "fake", account: "bird", name: "Bird", role: 7}}
	p := &poster{roles: guildRoles(discord.PermissionManageRoles, 2, false), mine: []snowflake.ID{5}}
	m.announce(context.Background(), p, key{"fake", "bird"}, post("a"))
	got := js(p.sent[0].msg)
	if !strings.Contains(got, `"custom_id":"notify-role:7"`) || !strings.Contains(got, `"label":"ping me"`) || !strings.Contains(got, `"style":1`) {
		t.Fatalf("the card offers the role: %s", got)
	}
	p.mine = nil
	m.announce(context.Background(), p, key{"fake", "bird"}, post("b"))
	if strings.Contains(js(p.sent[1].msg), "notify-role") {
		t.Fatal("no button skua couldn't honour")
	}
	// A ping role with permissions of its own is never one press away.
	p.mine = []snowflake.ID{5}
	p.roles[2].Permissions = discord.PermissionManageMessages
	m.announce(context.Background(), p, key{"fake", "bird"}, post("d"))
	if strings.Contains(js(p.sent[2].msg), "notify-role") {
		t.Fatal("a button for a role with permissions")
	}
	if strings.Contains(js(alert("fake", post("c"), 0, true)), "notify-role") {
		t.Fatal("no role, no button")
	}
}

// roleRest records role changes, and fails them with err when set. Its
// roles are what a press re-reads before handing one out.
type roleRest struct {
	rest.Rest
	added, removed []snowflake.ID
	err            error
	roles          []discord.Role
}

func (r *roleRest) GetRoles(snowflake.ID, ...rest.RequestOpt) ([]discord.Role, error) {
	return r.roles, nil
}

func (r *roleRest) GetMember(_, user snowflake.ID, _ ...rest.RequestOpt) (*discord.Member, error) {
	return &discord.Member{User: discord.User{ID: user}, RoleIDs: []snowflake.ID{5}}, nil
}

func (r *roleRest) AddMemberRole(_, _, role snowflake.ID, _ ...rest.RequestOpt) error {
	if r.err != nil {
		return r.err
	}
	r.added = append(r.added, role)
	return nil
}

func (r *roleRest) RemoveMemberRole(_, _, role snowflake.ID, _ ...rest.RequestOpt) error {
	if r.err != nil {
		return r.err
	}
	r.removed = append(r.removed, role)
	return nil
}

func (r *roleRest) CreateFollowupMessage(snowflake.ID, string, discord.MessageCreate, ...rest.RequestOpt) (*discord.Message, error) {
	return &discord.Message{}, nil
}

// press is member 5 pressing a card's ping me for role, holding roles,
// through the router; it returns what they read back.
func press(t *testing.T, r *core.Router, rr *roleRest, role string, holding ...string) string {
	t.Helper()
	e, _ := coretest.Button(t, roleID+":"+role, func(p map[string]any) {
		roles := []any{}
		for _, h := range holding {
			roles = append(roles, h)
		}
		p["member"].(map[string]any)["roles"] = roles
	})
	var got []string
	e.Respond = record(&got)
	e.Client().Rest = rr
	r.OnComponent(e)
	return strings.Join(got, "\n")
}

func TestPressingPingMe(t *testing.T) {
	m := module(t, newFake())
	m.self = 2
	m.follows = []follow{{guild: 3, platform: "fake", account: "bird", role: 7}}
	r := core.NewRouter(0, func(snowflake.ID) (snowflake.ID, bool) { return 0, false }, quiet())
	if err := r.Add(m); err != nil {
		t.Fatal(err)
	}
	rr := &roleRest{roles: guildRoles(discord.PermissionManageRoles, 2, false)}

	if got := press(t, r, rr, "7"); !strings.Contains(got, "you'll be pinged with <@&7>") || len(rr.added) != 1 {
		t.Fatalf("take it: %s %v", got, rr.added)
	}
	if got := press(t, r, rr, "7", "7"); !strings.Contains(got, "no more pings from <@&7>") || len(rr.removed) != 1 {
		t.Fatalf("drop it: %s %v", got, rr.removed)
	}
	// A role this server's follows don't ping is never handed out.
	if got := press(t, r, rr, "8"); !strings.Contains(got, "doesn't hand out that role") || len(rr.added) != 1 {
		t.Fatalf("a forged role: %s", got)
	}
	if got := press(t, r, rr, "nope"); !strings.Contains(got, "only works in a server") {
		t.Fatal(got)
	}
	// The role gained a permission after the card went out: taking it is
	// refused, dropping it still works.
	rr.roles[2].Permissions = discord.PermissionAdministrator
	if got := press(t, r, rr, "7"); !strings.Contains(got, "can't be handed out from cards") || len(rr.added) != 1 {
		t.Fatalf("a role that gained permissions: %s %v", got, rr.added)
	}
	if got := press(t, r, rr, "7", "7"); !strings.Contains(got, "no more pings") || len(rr.removed) != 2 {
		t.Fatalf("dropping it: %s %v", got, rr.removed)
	}
	rr.roles[2].Permissions = 0
	rr.err = &rest.Error{Response: &http.Response{StatusCode: http.StatusForbidden}}
	if got := press(t, r, rr, "7"); !strings.Contains(got, "needs to give her manage roles above it") {
		t.Fatal(got)
	}
	rr.err = errors.New("boom")
	if got := press(t, r, rr, "7"); !strings.Contains(got, "something went wrong") {
		t.Fatal(got)
	}
	g := guard.New()
	for g.Allow(3, guard.MemberEdit) == nil {
	}
	m.guard = g
	if got := press(t, r, rr, "7"); !strings.Contains(got, "too many role changes") {
		t.Fatal(got)
	}
}
