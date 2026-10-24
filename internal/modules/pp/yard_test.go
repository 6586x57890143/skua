package pp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/pixel"
)

// yardRest is a server's roles that also takes an interaction's edits and
// follow ups.
type yardRest struct {
	roleRest
	edits, followups []string
}

func (f *yardRest) UpdateInteractionResponse(_ snowflake.ID, _ string, u discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.edits = append(f.edits, js(u))
	return &discord.Message{}, nil
}

func (f *yardRest) CreateFollowupMessage(_ snowflake.ID, _ string, c discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.followups = append(f.followups, c.Content)
	return &discord.Message{}, nil
}

// yardServer is liveryServer with member 5 on rung r holding purse pp.
func yardServer(r int64, purse int64) (*Module, *yardRest) {
	m, roles := liveryServer()
	y := &yardRest{roleRest: *roles}
	m.rest = y
	s := m.st.server(3)
	mem := m.st.member(s, 3, 5, DayOf(m.now()))
	mem.rung, mem.purse = r, purse*unit
	return m, y
}

// Until an admin opens it, only admins see the yard: the shop has no way
// in and its pages refuse. Opening it is once, for good.
func TestYardGate(t *testing.T) {
	m := testModule(&fakeDB{})
	admin := false
	m.Admin = func(discord.Interaction) bool { return admin }
	m.st.server(3).beta = 1
	member := func(id string) (*events.ComponentInteractionCreate, *[]discord.MessageCreate) {
		return coretest.Button(t, id, func(p map[string]any) {
			p["member"].(map[string]any)["roles"] = []any{"1"}
		})
	}
	got := func(id string) (string, error) {
		e, sent := member(id)
		err := m.press(context.Background(), e)
		return js(*sent), err
	}
	if s, err := got("pp:shop"); err != nil || strings.Contains(s, "pp:yard") {
		t.Fatalf("a closed yard shows in the shop: %v %s", err, s)
	}
	if _, err := got("pp:yard"); !errors.Is(err, errYardShut) {
		t.Fatalf("a member walked into a closed yard: %v", err)
	}
	if _, err := got("pp:openyard"); !errors.Is(err, errYardOpens) {
		t.Fatalf("a member opened the yard: %v", err)
	}

	admin = true
	if s, err := got("pp:shop"); err != nil || !strings.Contains(s, "pp:yard") {
		t.Fatalf("an admin can't see the closed yard from the shop: %v %s", err, s)
	}
	if !strings.Contains(js(m.Report(3).Controls), "pp:openyard") {
		t.Fatal("the help page has no switch to open the yard")
	}
	e, sent := member("pp:openyard")
	if err := m.press(context.Background(), e); err != nil || !m.st.server(3).yardOpen || !strings.Contains((*sent)[0].Content, "open") {
		t.Fatalf("opening the yard: %v %v", err, *sent)
	}
	if strings.Contains(js(m.Report(3).Controls), "pp:openyard") {
		t.Error("the switch is still there once the yard is open")
	}

	admin = false
	if s, err := got("pp:shop"); err != nil || !strings.Contains(s, "pp:yard") {
		t.Fatalf("an open yard isn't in the shop: %v %s", err, s)
	}
	for _, id := range []string{"pp:yard", "pp:yard:1"} {
		if s, err := got(id); err != nil || !strings.Contains(s, "pp:dye") {
			t.Errorf("%s: %v %s", id, err, s)
		}
	}
}

// The pages list each livery once, on its own page, with what the member
// may wear priced and the rest waiting on their perch.
func TestYardPages(t *testing.T) {
	m, _ := yardServer(1, 0)
	s := m.st.server(3)
	today := DayOf(m.now())
	mem := m.st.member(s, 3, 5, today)
	mem.livery, mem.liveryUntil = "hide", today+5
	seen := map[string]bool{}
	for _, page := range []int{tradesPage, factionsPage} {
		msg, err := m.st.yardMessage(mem, page, today)
		if err != nil {
			t.Fatal(err)
		}
		got := js(msg)
		for _, d := range dyes {
			if strings.Contains(got, `"value":"`+d.key+`"`) {
				if seen[d.key] || d.page() != page {
					t.Errorf("%s shows on page %d", d.key, page)
				}
				seen[d.key] = true
			}
		}
		if page == factionsPage && !strings.Contains(got, "worn from the shale perch") {
			t.Error("a tallow perch sees no wait on the factions")
		}
		if page == tradesPage && !strings.Contains(got, "tanners' hide (yours, 5 days left)") {
			t.Errorf("the page doesn't say what they wear: %s", got)
		}
	}
	if len(seen) != len(dyes) {
		t.Errorf("%d of %d liveries listed", len(seen), len(dyes))
	}
	for _, d := range dyes {
		if d.price <= 0 || d.color == 0 || d.what == "" || d.story == "" || !strings.Contains(d.effect, "a week") || len(d.name) > 32 {
			t.Errorf("%s isn't a whole livery", d.key)
		}
	}
}

// A lease dresses the member, makes the role once, burns a week's price
// and runs a week; another week of the same runs on from the last.
func TestLease(t *testing.T) {
	ctx := context.Background()
	m, y := yardServer(1, 10000)
	db := m.db.(*fakeDB)
	tallow, _ := dyeOf("tallow")
	today := DayOf(m.now())
	s := m.st.server(3)
	price := m.st.leasePrice(tallow)
	if price != m.st.pot()/2 {
		t.Fatalf("a trade's week is %d, not half a pot day", price)
	}

	if _, err := m.leased(ctx, 3, 5, tallow); err != nil {
		t.Fatal(err)
	}
	mem := s.members[5]
	if mem.livery != "tallow" || mem.liveryUntil != today+leaseDays || mem.purse != 10000*unit-price {
		t.Fatalf("after a lease: %q until %d purse %d", mem.livery, mem.liveryUntil, mem.purse)
	}
	if got := strings.Join(y.writes, ", "); got != "create, move, add 99" {
		t.Errorf("the writes: %s", got)
	}
	if y.created.Name != "chandlers' tallow" || y.created.Colors.PrimaryColor != 0xa2803e || *y.created.Permissions != 0 {
		t.Errorf("the role made: %+v", y.created)
	}
	if !strings.Contains(strings.Join(db.execs, ";"), "pp_dye") || s.dyeRoles["tallow"] != 99 {
		t.Error("the livery's role isn't kept")
	}
	// The lease is saved with its ledger row straight away.
	if len(s.pending) != 0 || mem.dirty || !strings.Contains(strings.Join(db.execs, ";"), "insert into pp_ledger") {
		t.Errorf("the lease wasn't saved: %d pending, dirty %v", len(s.pending), mem.dirty)
	}

	// Another week runs on from the last, makes no new role and dresses
	// them again, in case the role came off by hand.
	y.writes = nil
	if _, err := m.leased(ctx, 3, 5, tallow); err != nil {
		t.Fatal(err)
	}
	if mem.liveryUntil != today+2*leaseDays || strings.Join(y.writes, ", ") != "add 99" {
		t.Errorf("a second week: until %d, writes %v", mem.liveryUntil, y.writes)
	}
	// Up to four weeks ahead.
	for range 2 {
		if _, err := m.leased(ctx, 3, 5, tallow); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.leased(ctx, 3, 5, tallow); !errors.Is(err, errDyeAhead) {
		t.Errorf("a fifth week ahead: %v", err)
	}

	// A different livery starts its own week and takes the old one off.
	y.roles = append(y.roles, discord.Role{ID: 98, Position: 4})
	s.setDye("hide", 98)
	hide, _ := dyeOf("hide")
	y.writes = nil
	if _, err := m.leased(ctx, 3, 5, hide); err != nil {
		t.Fatal(err)
	}
	if mem.livery != "hide" || mem.liveryUntil != today+leaseDays || strings.Join(y.writes, ", ") != "add 98, remove 99" {
		t.Errorf("a change of livery: %q until %d, writes %v", mem.livery, mem.liveryUntil, y.writes)
	}

	// A faction waits on the shale perch, and a short purse buys nothing.
	ink, _ := dyeOf("ink")
	if _, err := m.leased(ctx, 3, 5, ink); !errors.Is(err, errDyePerch) {
		t.Errorf("a faction from tallow: %v", err)
	}
	mem.rung, mem.purse = 2, 10
	if _, err := m.leased(ctx, 3, 5, ink); !errors.Is(err, errDyeShort) {
		t.Errorf("a lease with no money: %v", err)
	}
}

// Through the button: the press is deferred, the card comes back as an
// edit, and a refusal comes as a note.
func TestLeaseButton(t *testing.T) {
	m, y := yardServer(1, 10000)
	m.st.server(3).yardOpen = true
	press := func(id string) error {
		e, _ := coretest.Button(t, id, nil)
		e.Client().Rest = y
		return m.press(context.Background(), e)
	}
	if err := press("pp:lease:tallow"); err != nil || len(y.edits) != 1 || !strings.Contains(y.edits[0], "another week") {
		t.Fatalf("a lease: %v %v", err, y.edits)
	}
	if err := press("pp:lease:ink"); err != nil || len(y.followups) != 1 || y.followups[0] != string(errDyePerch) {
		t.Fatalf("a lease refused: %v %v", err, y.followups)
	}
	m.st.server(3).anchor = 0
	m.st.server(3).dyeRoles = nil
	if err := press("pp:lease:hide"); err != nil || y.followups[1] != string(errNoAnchor) {
		t.Fatalf("a lease with no anchor: %v %v", err, y.followups)
	}
	if err := press("pp:lease:velvet"); !errors.Is(err, errNoDye) {
		t.Errorf("a livery the yard doesn't sell: %v", err)
	}
	e, sent := coretest.Button(t, "pp:dye", func(p map[string]any) {
		p["data"] = map[string]any{"custom_id": "pp:dye", "component_type": 3, "values": []any{"ember"}}
	})
	if err := m.press(context.Background(), e); err != nil || !strings.Contains(js(*sent), "worn from the shale perch") {
		t.Errorf("the ember card from tallow: %v %s", err, js(*sent))
	}
}

// A lease run out fades fadeDays later, at the day's turn, and its role
// comes off; one Discord won't take off yet stays for the next turn.
func TestFade(t *testing.T) {
	ctx := context.Background()
	m, y := yardServer(1, 0)
	s := m.st.server(3)
	today := DayOf(m.now())
	s.setDye("tallow", 30)
	mem := s.members[5]
	mem.livery, mem.liveryUntil = "tallow", today-fadeDays+1
	m.fade(ctx, today)
	if mem.livery == "" || len(y.writes) != 0 {
		t.Fatalf("faded in the grace: %v", y.writes)
	}
	s.anchor = 0
	m.fade(ctx, today+1)
	if mem.livery == "" {
		t.Fatal("faded though the role couldn't come off")
	}
	s.anchor = 10
	m.fade(ctx, today+1)
	if mem.livery != "" || !mem.dirty || strings.Join(y.writes, ", ") != "remove 30" {
		t.Errorf("after the grace: %q, writes %v", mem.livery, y.writes)
	}
	// A role an admin since gave a permission isn't touched, and the lease
	// is done with anyway.
	y.writes = nil
	mem.livery, mem.liveryUntil = "tallow", today-10
	y.roles[3].Permissions = discord.PermissionAdministrator
	m.fade(ctx, today)
	if mem.livery != "" || len(y.writes) != 0 {
		t.Errorf("a role with a permission: %q, writes %v", mem.livery, y.writes)
	}
}

// The yard's errors are all the member's to read.
func TestYardTells(t *testing.T) {
	for _, err := range []error{errYardShut, errNoDye, errDyePerch, errDyeShort, errDyeAhead, errYardOpens} {
		if _, ok := errors.AsType[core.Tell](err); !ok {
			t.Errorf("%v isn't a Tell", err)
		}
	}
}

// A role an admin deleted is made again; one they gave a permission is
// refused with a word for the admins; and the journal names a lease.
func TestLeaseRoleChangedByHand(t *testing.T) {
	ctx := context.Background()
	m, y := yardServer(2, 10000)
	s := m.st.server(3)
	s.setDye("ink", 404)
	ink, _ := dyeOf("ink")
	if _, err := m.leased(ctx, 3, 5, ink); err != nil {
		t.Fatal(err)
	}
	if s.dyeRoles["ink"] != 99 || !strings.HasPrefix(strings.Join(y.writes, ", "), "create, move, add 99") {
		t.Errorf("a deleted role wasn't made again: %d %v", s.dyeRoles["ink"], y.writes)
	}
	s.setDye("stair", 31) // carries Administrator
	stair, _ := dyeOf("stair")
	if _, err := m.leased(ctx, 3, 5, stair); !errors.Is(err, errDyeMoved) {
		t.Errorf("a role with a permission: %v", err)
	}
	if icon, what := journalLine(reasonLease, []int64{int64(dyeIndex("ink")), 20800}); icon != "scales" || what != "counting house ink" {
		t.Errorf("the journal reads a lease as %s %q", icon, what)
	}
	if _, what := journalLine(reasonLease, []int64{99}); what != "a livery" {
		t.Errorf("an unknown lease reads %q", what)
	}
}

// If the purse can't pay once the member is dressed, the dressing is
// undone and their old livery put back.
func TestLeaseUndone(t *testing.T) {
	ctx := context.Background()
	m, y := yardServer(1, 10000)
	s := m.st.server(3)
	s.setDye("tallow", 30)
	s.setDye("hide", 98)
	y.roles = append(y.roles, discord.Role{ID: 98, Position: 4})
	mem := s.members[5]
	mem.livery, mem.liveryUntil = "tallow", DayOf(m.now())+2
	// The purse empties between the checks, the way a climb would.
	y.onAdd = func() { s.mu.Lock(); mem.purse = 0; s.mu.Unlock() }
	hide, _ := dyeOf("hide")
	if _, err := m.leased(ctx, 3, 5, hide); !errors.Is(err, errDyeShort) {
		t.Fatalf("a lease the purse emptied under: %v", err)
	}
	if got := strings.Join(y.writes, ", "); got != "add 98, remove 30, remove 98, add 30" || mem.livery != "tallow" {
		t.Errorf("not undone: %s, %q", got, mem.livery)
	}
}

// Every livery has a garment, 16 cells square, drawn only in the icons'
// inks and the three dye cells.
func TestGarments(t *testing.T) {
	for _, d := range dyes {
		g := Garment(d.key)
		if len(g) != pixel.IconCells {
			t.Errorf("%s has %d rows", d.key, len(g))
		}
		dyed := false
		for _, row := range g {
			if len(row) != pixel.IconCells {
				t.Errorf("%s: row %q", d.key, row)
			}
			for i := range len(row) {
				_, ink := pixel.IconInks[row[i]]
				dyed = dyed || strings.IndexByte("123", row[i]) >= 0
				if !ink && strings.IndexByte(".123", row[i]) < 0 {
					t.Errorf("%s: %q isn't an ink", d.key, row[i])
				}
			}
		}
		if !dyed {
			t.Errorf("%s takes no dye", d.key)
		}
	}
	if len(garments) != len(dyes) {
		t.Errorf("%d garments for %d liveries", len(garments), len(dyes))
	}
}
