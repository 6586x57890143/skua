package pp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
)

// roleRest is a server's roles as Discord holds them, recording every
// write skua makes.
type roleRest struct {
	rest.Rest
	roles   []discord.Role
	mine    []snowflake.ID
	writes  []string
	created discord.RoleCreate
	updated discord.RoleUpdate
	moved   []discord.RolePositionUpdate
	missing bool // a delete or a removal finds nothing there
}

func (f *roleRest) GetRoles(snowflake.ID, ...rest.RequestOpt) ([]discord.Role, error) {
	return f.roles, nil
}

func (f *roleRest) GetMember(_, user snowflake.ID, _ ...rest.RequestOpt) (*discord.Member, error) {
	return &discord.Member{User: discord.User{ID: user}, RoleIDs: f.mine}, nil
}

func (f *roleRest) CreateRole(_ snowflake.ID, c discord.RoleCreate, _ ...rest.RequestOpt) (*discord.Role, error) {
	f.writes, f.created = append(f.writes, "create"), c
	f.roles = append(f.roles, discord.Role{ID: 99, Position: 1})
	return &discord.Role{ID: 99}, nil
}

func (f *roleRest) UpdateRolePositions(_ snowflake.ID, u []discord.RolePositionUpdate, _ ...rest.RequestOpt) ([]discord.Role, error) {
	f.writes, f.moved = append(f.writes, "move"), u
	return nil, nil
}

func (f *roleRest) UpdateRole(_, role snowflake.ID, u discord.RoleUpdate, _ ...rest.RequestOpt) (*discord.Role, error) {
	f.writes, f.updated = append(f.writes, "edit "+role.String()), u
	return &discord.Role{ID: role}, nil
}

func (f *roleRest) DeleteRole(_, role snowflake.ID, _ ...rest.RequestOpt) error {
	f.writes = append(f.writes, "delete "+role.String())
	return f.notFound()
}

func (f *roleRest) AddMemberRole(_, user, role snowflake.ID, _ ...rest.RequestOpt) error {
	f.writes = append(f.writes, "add "+role.String())
	return nil
}

func (f *roleRest) RemoveMemberRole(_, user, role snowflake.ID, _ ...rest.RequestOpt) error {
	f.writes = append(f.writes, "remove "+role.String())
	return f.notFound()
}

func (f *roleRest) notFound() error {
	if f.missing {
		return &rest.Error{Response: &http.Response{StatusCode: http.StatusNotFound}}
	}
	return nil
}

// A server where skua (77, holding role 20 with Manage Roles) sits above
// the anchor 10: 30 is a livery, 31 carries a permission, 32 sits above
// the anchor and 33 is an integration's.
func liveryServer() (*Module, *roleRest) {
	m := testModule(&fakeDB{})
	r := &roleRest{
		roles: []discord.Role{
			{ID: 3, Position: 0, Permissions: discord.PermissionViewChannel},
			{ID: 20, Position: 9, Permissions: discord.PermissionManageRoles},
			{ID: 10, Position: 5},
			{ID: 30, Position: 2},
			{ID: 31, Position: 3, Permissions: discord.PermissionAdministrator},
			{ID: 32, Position: 7},
			{ID: 33, Position: 1, Managed: true},
		},
		mine: []snowflake.ID{20},
	}
	m.rest, m.Guard, m.self = r, guard.New(), 77
	m.st.server(3).anchor = 10
	return m, r
}

func TestLiveryStanding(t *testing.T) {
	ctx := context.Background()
	m, r := liveryServer()
	if _, at, err := m.standing(ctx, 3); err != nil || at != 5 {
		t.Fatalf("a good server: at %d, %v", at, err)
	}
	for name, c := range map[string]struct {
		set  func()
		want error
	}{
		"no anchor":        {func() { m.st.server(3).anchor = 0 }, errNoAnchor},
		"anchor deleted":   {func() { m.st.server(3).anchor = 404 }, errNoAnchor},
		"not up yet":       {func() { m.self = 0 }, errNotStarted},
		"no manage roles":  {func() { r.roles[1].Permissions = 0 }, errNoManage},
		"skua under it":    {func() { r.roles[1].Position = 4 }, errUnderSkua},
		"skua level to it": {func() { r.roles[1].Position = 5 }, errUnderSkua},
	} {
		m, r = liveryServer()
		c.set()
		if _, _, err := m.standing(ctx, 3); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if len(r.writes) != 0 {
			t.Errorf("%s: wrote %v", name, r.writes)
		}
	}
	// Administrator stands in for Manage Roles.
	m, r = liveryServer()
	r.roles[1].Permissions = discord.PermissionAdministrator
	if _, _, err := m.standing(ctx, 3); err != nil {
		t.Errorf("an administrator skua: %v", err)
	}
}

// A role skua makes carries no permissions at all, wears the look asked
// for and goes directly under the anchor.
func TestMakeRole(t *testing.T) {
	m, r := liveryServer()
	second := 0x6286c3
	l := look{name: "the heron's own", colors: discord.RoleColors{PrimaryColor: 0xc66e34, SecondaryColor: &second}, icon: []byte("png")}
	id, err := m.makeRole(context.Background(), 3, l, "granted to tester")
	if err != nil || id != 99 {
		t.Fatalf("made %d, %v", id, err)
	}
	c := r.created
	if c.Permissions == nil || *c.Permissions != 0 || c.Hoist || c.Mentionable {
		t.Errorf("the role isn't bare: %+v", c)
	}
	if !strings.Contains(js(c), `"permissions":"0"`) {
		t.Errorf("a role made without permissions takes @everyone's: %s", js(c))
	}
	if c.Name != l.name || c.Colors.PrimaryColor != 0xc66e34 || *c.Colors.SecondaryColor != second || c.Icon == nil {
		t.Errorf("the look didn't carry: %+v", c)
	}
	if len(r.moved) != 1 || r.moved[0].ID != 99 || *r.moved[0].Position != 5 {
		t.Errorf("not moved under the anchor: %+v", r.moved)
	}

	// Without an anchor nothing is made.
	m, r = liveryServer()
	m.st.server(3).anchor = 0
	if _, err := m.makeRole(context.Background(), 3, l, "x"); !errors.Is(err, errNoAnchor) || len(r.writes) != 0 {
		t.Errorf("made a role with no anchor: %v %v", err, r.writes)
	}
}

// Only a bare role under the anchor is ever edited, deleted or handed out.
func TestLiveryFence(t *testing.T) {
	ctx := context.Background()
	for _, role := range []snowflake.ID{3, 31, 32, 33, 404} {
		m, r := liveryServer()
		if err := m.editRole(ctx, 3, role, look{name: "x"}, "x"); !errors.Is(err, errNotLivery) {
			t.Errorf("edited %d: %v", role, err)
		}
		if err := m.dress(ctx, 3, 5, role, true, "x"); !errors.Is(err, errNotLivery) {
			t.Errorf("handed out %d: %v", role, err)
		}
		if err := m.dress(ctx, 3, 5, role, false, "x"); !errors.Is(err, errNotLivery) {
			t.Errorf("took back %d: %v", role, err)
		}
		if role != 404 {
			if err := m.dropRole(ctx, 3, role, "x"); !errors.Is(err, errNotLivery) {
				t.Errorf("deleted %d: %v", role, err)
			}
		}
		if len(r.writes) != 0 {
			t.Errorf("role %d: wrote %v", role, r.writes)
		}
	}

	m, r := liveryServer()
	if err := m.editRole(ctx, 3, 30, look{name: "renamed", colors: discord.RoleColors{PrimaryColor: 7}}, "x"); err != nil {
		t.Fatal(err)
	}
	if *r.updated.Name != "renamed" || !strings.Contains(js(r.updated), `"primary_color":7`) || strings.Contains(js(r.updated), "icon") {
		t.Errorf("the edit: %s", js(r.updated))
	}
	if err := m.dress(ctx, 3, 5, 30, true, "x"); err != nil {
		t.Fatal(err)
	}
	if err := m.dress(ctx, 3, 5, 30, false, "x"); err != nil {
		t.Fatal(err)
	}
	if err := m.dropRole(ctx, 3, 30, "x"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.writes, ", "); got != "edit 30, add 30, remove 30, delete 30" {
		t.Errorf("the writes: %s", got)
	}

	// A role already gone, or a member already out of it, is done.
	m, r = liveryServer()
	r.missing = true
	if err := m.dropRole(ctx, 3, 404, "x"); err != nil || len(r.writes) != 0 {
		t.Errorf("a role not there: %v %v", err, r.writes)
	}
	if err := m.dropRole(ctx, 3, 30, "x"); err != nil {
		t.Errorf("a delete that found nothing: %v", err)
	}
	if err := m.dress(ctx, 3, 5, 30, false, "x"); err != nil {
		t.Errorf("a removal that found nothing: %v", err)
	}
}

// Writes go through the guard: past its hour's cap, none go out.
func TestLiveryGuard(t *testing.T) {
	m, r := liveryServer()
	n := 0
	for ; n < 1000; n++ {
		if err := m.dress(context.Background(), 3, 5, 30, true, "x"); err != nil {
			if !errors.Is(err, guard.ErrRateLimited) {
				t.Fatal(err)
			}
			break
		}
	}
	if n == 1000 || len(r.writes) != n {
		t.Errorf("the guard let %d through and %d went out", n, len(r.writes))
	}
}

// Only admins pick the anchor and the heralds' channel, on the help page.
func TestLiveryControls(t *testing.T) {
	m := testModule(&fakeDB{})
	got := js(m.Report(3).Controls)
	if !strings.Contains(got, "pp:anchor") || !strings.Contains(got, "pp:heralds") {
		t.Fatalf("the help page has no livery settings: %s", got)
	}
	role := func(id string) (*events.ComponentInteractionCreate, *[]discord.MessageCreate) {
		return coretest.Button(t, "pp:anchor", func(p map[string]any) {
			p["data"] = map[string]any{"custom_id": "pp:anchor", "component_type": 6, "values": []any{id},
				"resolved": map[string]any{"roles": map[string]any{id: map[string]any{"id": id, "name": "anchor", "permissions": "0"}}}}
		})
	}
	channel := func(id string) (*events.ComponentInteractionCreate, *[]discord.MessageCreate) {
		return coretest.Button(t, "pp:heralds", func(p map[string]any) {
			p["data"] = map[string]any{"custom_id": "pp:heralds", "component_type": 8, "values": []any{id},
				"resolved": map[string]any{"channels": map[string]any{id: map[string]any{"id": id, "type": 0, "name": "heralds", "permissions": "0"}}}}
		})
	}
	admin := false
	m.Admin = func(discord.Interaction) bool { return admin }
	if err := m.press(context.Background(), firstOf(role("10"))); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member set the anchor: %v", err)
	}
	admin = true
	e, sent := role("10")
	if err := m.press(context.Background(), e); err != nil || m.st.server(3).anchor != 10 || !strings.Contains((*sent)[0].Content, "<@&10>") {
		t.Fatalf("the anchor: %v %d %s", err, m.st.server(3).anchor, js(*sent))
	}
	e, sent = channel("88")
	if err := m.press(context.Background(), e); err != nil || m.st.server(3).heralds != 88 || !strings.Contains((*sent)[0].Content, "<#88>") {
		t.Fatalf("the heralds' channel: %v %d %s", err, m.st.server(3).heralds, js(*sent))
	}
	if got := js(m.Report(3).Controls); !strings.Contains(got, `"id":"10"`) || !strings.Contains(got, `"id":"88"`) {
		t.Errorf("the help page doesn't show what's picked: %s", got)
	}
	// A select under the other's ID is refused.
	e, _ = coretest.Button(t, "pp:heralds", func(p map[string]any) {
		p["data"] = map[string]any{"custom_id": "pp:heralds", "component_type": 6, "values": []any{"10"},
			"resolved": map[string]any{"roles": map[string]any{"10": map[string]any{"id": "10", "name": "x", "permissions": "0"}}}}
	})
	if err := m.press(context.Background(), e); !errors.Is(err, errNotAdmin) {
		t.Errorf("a role select posing as the channel picker: %v", err)
	}
}
