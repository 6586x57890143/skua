package pp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
)

// grantFor is the grant form submitted by user from, in guild g: amount
// pot days for to, who is an admin when admin.
func grantFor(t *testing.T, g, from, to snowflake.ID, amount string, bot, admin bool) *events.ModalSubmitInteractionCreate {
	t.Helper()
	id := to.String()
	perms := "0"
	if admin {
		perms = "8"
	}
	e, _ := coretest.Modal(t, grantForm, nil, func(p map[string]any) {
		p["guild_id"] = g.String()
		p["member"].(map[string]any)["user"] = map[string]any{"id": from.String(), "username": "admin"}
		d := p["data"].(map[string]any)
		d["components"] = []any{
			map[string]any{"type": 18, "component": map[string]any{"type": 5, "custom_id": "to", "values": []any{id}}},
			map[string]any{"type": 18, "component": map[string]any{"type": 4, "custom_id": "amount", "value": amount}},
			map[string]any{"type": 18, "component": map[string]any{"type": 4, "custom_id": "reason", "value": "won the  screenshot\ncontest"}},
		}
		d["resolved"] = map[string]any{
			"users":   map[string]any{id: map[string]any{"id": id, "username": "winner", "bot": bot}},
			"members": map[string]any{id: map[string]any{"permissions": perms, "roles": []any{}, "joined_at": "2026-01-01T00:00:00Z"}},
		}
	})
	return e
}

// A contest fund in guild with a ledger channel, holding fund pp, all of
// the treasury.
func openFund(m *Module, g snowflake.ID, fund int64) *server {
	s := m.st.server(g)
	s.logChannel, s.treasury, s.fund, s.seeded = 88, fund, fund, true
	return s
}

// Grants follow the charter: admins only, never to an admin, a bot or
// themselves, only with a ledger channel, never past the biggest prize,
// the contest fund or a member's season.
func TestGrantRefusals(t *testing.T) {
	m := testModule(&fakeDB{})
	m.rest, m.Guard = &postRest{}, guard.New()
	pd := Default.PotDay(Rules)
	g := snowflake.ID(3)
	winner := old(7)
	ctx := context.Background()

	admin := false
	m.Admin = func(discord.Interaction) bool { return admin }
	if err := m.press(ctx, firstOf(coretest.Button(t, "pp:grant", nil))); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member opened the grant form: %v", err)
	}
	if err := m.submitGrant(ctx, grantFor(t, g, 5, winner, "1", false, false)); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member granted: %v", err)
	}
	admin = true
	if err := m.press(ctx, firstOf(coretest.Button(t, "pp:grant", nil))); err != nil {
		t.Fatalf("the form didn't open: %v", err)
	}

	s := m.st.server(g)
	for _, c := range []struct {
		name string
		e    *events.ModalSubmitInteractionCreate
		want string
	}{
		{"no ledger channel", grantFor(t, g, 5, winner, "0.5", false, false), "ledger channel"},
	} {
		if err := m.submitGrant(ctx, c.e); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	s.logChannel, s.treasury = 88, 100*pd
	if err := m.submitGrant(ctx, grantFor(t, g, 5, winner, "0.5", false, false)); err == nil || !strings.Contains(err.Error(), "contest fund holds") {
		t.Errorf("the treasury outside the fund granted: %v", err)
	}
	openFund(m, g, 100*pd)
	for _, c := range []struct {
		name string
		e    *events.ModalSubmitInteractionCreate
		want error
	}{
		{"to a bot", grantFor(t, g, 5, winner, "0.5", true, false), errGrantBot},
		{"to themselves", grantFor(t, g, 5, 5, "0.5", false, false), errGrantAdmin},
		{"to an admin", grantFor(t, g, 5, winner, "0.5", false, true), errGrantAdmin},
		{"nothing", grantFor(t, g, 5, winner, "zero", false, false), errGrantSize},
		{"less than nothing", grantFor(t, g, 5, winner, "-1", false, false), errGrantSize},
	} {
		if err := m.submitGrant(ctx, c.e); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// No prize past PrizeMax golden quests.
	over := fmt.Sprint(float64(Rules.Prize(&Default))/float64(pd) + 0.1)
	if err := m.submitGrant(ctx, grantFor(t, g, 5, winner, over, false, false)); err == nil || !strings.Contains(err.Error(), "golden quests") {
		t.Errorf("a prize past the biggest: %v", err)
	}
	// A season's room for one member.
	season, _ := Default.Season(DayOf(t0))
	mem := m.st.member(s, g, winner, DayOf(t0))
	mem.grantSeason, mem.granted = season, (Rules.GrantSeason-1)*pd
	if err := m.submitGrant(ctx, grantFor(t, g, 5, winner, "1.5", false, false)); err == nil || !strings.Contains(err.Error(), "this season") {
		t.Errorf("past a member's season: %v", err)
	}
	// No more than the fund holds.
	openFund(m, g, pd/2)
	if err := m.submitGrant(ctx, grantFor(t, g, 5, old(8), "1", false, false)); err == nil || !strings.Contains(err.Error(), "contest fund holds") {
		t.Errorf("past the contest fund: %v", err)
	}
}

// A grant up to a pot day pays at once: out of the contest fund, into the
// purse but not the level, both into the ledger, saved at once, and posted.
func TestGrantPays(t *testing.T) {
	db := &fakeDB{}
	m := testModule(db)
	r := &postRest{}
	m.rest, m.Guard = r, guard.New()
	pd := Default.PotDay(Rules)
	g := snowflake.ID(3)
	s := openFund(m, g, 50*pd)
	winner := old(7)
	e := grantFor(t, g, 5, winner, "1", false, false)
	var got []string
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		got = append(got, d.(discord.MessageCreate).Content)
		return nil
	}
	if err := m.submitGrant(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	mem := s.members[winner]
	if mem.purse != pd || mem.total != 0 || s.treasury != 49*pd || s.fund != 49*pd || mem.granted != pd {
		t.Errorf("purse %d total %d treasury %d granted %d", mem.purse, mem.total, s.treasury, mem.granted)
	}
	if len(s.pending) != 0 || len(db.execs) == 0 || !strings.Contains(db.execs[0], "insert into pp_ledger") {
		t.Errorf("not saved at once: pending %+v, wrote %d", s.pending, len(db.execs))
	}
	if s.seq != 2 {
		t.Errorf("the ledger got %d rows", s.seq)
	}
	if len(r.posts) != 1 || !strings.Contains(r.posts[0], "won the screenshot contest") || !strings.Contains(r.posts[0], "granted by") {
		t.Errorf("posted %v", r.posts)
	}
	if len(got) != 1 || !strings.Contains(got[0], "✓") {
		t.Errorf("the admin was told %v", got)
	}
	if !strings.Contains(js(m.Report(g).Rows), "in the contest fund") || !strings.Contains(js(m.Report(g).Controls), "pp:grant") {
		t.Errorf("the help page: %s", js(m.Report(g)))
	}
	if row := grantsRow(newServer(1)); row != "0 pp in the contest fund" {
		t.Errorf("a new server's grants read %q", row)
	}
}

// Over a pot day, a grant waits in the ledger channel for a second admin:
// the one who asked can't approve it, another can, once; a refusal ends it.
func TestGrantApproval(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	m := testModule(db)
	r := &postRest{}
	m.rest, m.Guard = r, guard.New()
	pd := Default.PotDay(Rules)
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	s := openFund(m, g, 40*pd)
	s.append(0, DayOf(t0), reasonOpen, 40*pd, nil) // what the treasury holds, in its ledger
	winner := old(7)

	if err := m.submitGrant(ctx, grantFor(t, g, 5, winner, "3", false, false)); err != nil {
		t.Fatal(err)
	}
	if s.treasury != 40*pd || len(r.posts) != 1 || !strings.Contains(r.posts[0], "pp:approve:") {
		t.Fatalf("a waiting grant: treasury %d, posted %v", s.treasury, r.posts)
	}
	id := r.posts[0][strings.Index(r.posts[0], "pp:approve:")+len("pp:approve:"):]
	id = id[:strings.IndexAny(id, `"`)]
	press := func(custom string, user snowflake.ID) error {
		e, _ := coretest.Button(t, custom, func(p map[string]any) {
			p["guild_id"] = g.String()
			p["member"].(map[string]any)["user"] = map[string]any{"id": user.String(), "username": "admin"}
		})
		e.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
			return nil
		}
		return m.press(ctx, e)
	}
	if err := press("pp:approve:"+id, 5); !errors.Is(err, errGrantSelf) {
		t.Fatalf("the asker approved their own grant: %v", err)
	}
	if err := press("pp:approve:"+id, 6); err != nil {
		t.Fatal(err)
	}
	if s.treasury != 37*pd || s.members[winner].purse != 3*pd {
		t.Errorf("approved: treasury %d, purse %d", s.treasury, s.members[winner].purse)
	}
	if err := press("pp:approve:"+id, 6); !errors.Is(err, errGrantGone) {
		t.Errorf("a grant paid twice: %v", err)
	}

	if err := m.submitGrant(ctx, grantFor(t, g, 5, old(8), "2", false, false)); err != nil {
		t.Fatal(err)
	}
	last := r.posts[len(r.posts)-1]
	id = last[strings.Index(last, "pp:refuse:")+len("pp:refuse:"):]
	id = id[:strings.IndexAny(id, `"`)]
	if err := press("pp:refuse:"+id, 5); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.members[old(8)]; ok && s.members[old(8)].purse != 0 {
		t.Error("a refused grant paid")
	}
	if err := press("pp:approve:nope", 6); !errors.Is(err, errGrantGone) {
		t.Errorf("a broken id: %v", err)
	}
	if n, err := m.st.audit(ctx, db); err != nil || n != 0 {
		t.Errorf("the audit after grants: %d, %v", n, err)
	}
	var state string
	if err := queryOne(ctx, db, `select state from pp_grant where guild_id = $1 and to_id = $2`, []any{int64(g), int64(winner)}, &state); err != nil || state != "paid" {
		t.Errorf("an approved grant is %q after its save: %v", state, err)
	}

}

// Two grants at once can't both take the room for one: each checks the
// fund under the lock it pays under.
func TestGrantRace(t *testing.T) {
	m := testModule(&fakeDB{})
	pd := Default.PotDay(Rules)
	g := snowflake.ID(3)
	s := openFund(m, g, pd)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() { errs <- m.pay(g, grant{from: 5, to: old(10 + i), amount: pd}) })
	}
	wg.Wait()
	close(errs)
	var ok int
	for err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 || s.fund != 0 || s.treasury != 0 {
		t.Errorf("%d grants paid, fund %d, treasury %d", ok, s.fund, s.treasury)
	}
}

// A grant a crash left paying, claimed but never flushed, was never paid:
// the next boot puts it back to waiting. A paid one stays paid.
func TestGrantPayingWaitsAgain(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	var paying, paid int64
	for _, c := range []struct {
		state string
		id    *int64
	}{{"paying", &paying}, {"paid", &paid}} {
		if err := queryOne(ctx, db, `insert into pp_grant (guild_id, from_id, to_id, amount, reason, state, second_id) values ($1, 5, 7, 1, 'x', $2, 6) returning id`,
			[]any{int64(g), c.state}, c.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := newState(&Default, 42).load(ctx, db); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{paying: "waiting", paid: "paid"} {
		var state string
		if err := queryOne(ctx, db, `select state from pp_grant where id = $1`, []any{id}, &state); err != nil || state != want {
			t.Errorf("grant %d is %q, want %q: %v", id, state, want, err)
		}
	}
}

// A grant whose save fails stays queued to be marked paid with its ledger
// rows, and the next save writes both.
func TestGrantSaveFails(t *testing.T) {
	db := &fakeDB{fail: errors.New("down")}
	m := testModule(db)
	pd := Default.PotDay(Rules)
	g := snowflake.ID(3)
	s := openFund(m, g, 10*pd)
	if err := m.pay(g, grant{id: 9, from: 5, to: old(7), amount: pd}); err != nil {
		t.Fatal(err)
	}
	if len(s.paid) != 1 || len(s.pending) != 2 || !s.dirty {
		t.Fatalf("after a failed save: paid %v, pending %d, dirty %v", s.paid, len(s.pending), s.dirty)
	}
	db.fail, db.execs = nil, nil
	m.Save()
	if len(s.paid) != 0 || len(s.pending) != 0 || !strings.Contains(strings.Join(db.execs, "\n"), "set state = 'paid'") {
		t.Errorf("the next save: paid %v, pending %d, wrote %v", s.paid, len(s.pending), db.execs)
	}
}
