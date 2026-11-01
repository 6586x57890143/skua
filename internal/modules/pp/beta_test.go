package pp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// While /pp is in closed beta only admins and the beta role play, and only
// admins pick the role, on the help page.
func TestBetaGate(t *testing.T) {
	db := &fakeDB{}
	m := testModule(db)
	admin := false
	m.Admin = func(discord.Interaction) bool { return admin }

	if err := m.press(context.Background(), firstOf(coretest.Button(t, "pp:class", nil))); !errors.Is(err, errBeta) {
		t.Fatalf("a member without the role played: %v", err)
	}
	if len(m.Report(3).Controls) == 0 {
		t.Fatal("the help page has no role picker")
	}

	role := func(id string) (*events.ComponentInteractionCreate, *[]discord.MessageCreate) {
		return coretest.Button(t, "pp:beta", func(p map[string]any) {
			p["data"] = map[string]any{"custom_id": "pp:beta", "component_type": 6, "values": []any{id},
				"resolved": map[string]any{"roles": map[string]any{id: map[string]any{"id": id, "name": "beta"}}}}
		})
	}
	if err := m.press(context.Background(), firstOf(role("77"))); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member set the beta role: %v", err)
	}
	admin = true
	e, sent := role("77")
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if m.st.server(3).beta != 77 || len(db.execs) == 0 || !strings.Contains(db.execs[len(db.execs)-1], "beta_role") {
		t.Fatalf("the role wasn't kept: %d, %v", m.st.server(3).beta, db.execs)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Content, "<@&77>") {
		t.Errorf("the admin was told %v", *sent)
	}

	admin = false
	tester, _ := coretest.Button(t, "pp:class", func(p map[string]any) {
		p["member"].(map[string]any)["roles"] = []any{"77"}
	})
	if err := m.press(context.Background(), tester); err != nil {
		t.Errorf("a beta tester couldn't play: %v", err)
	}
}

func firstOf(e *events.ComponentInteractionCreate, _ *[]discord.MessageCreate) *events.ComponentInteractionCreate {
	return e
}

// An admin arms the launch from the help page, and may disarm it until
// the turn; nobody else can. Until the turn members see nothing new and
// can't play without the role; from it every member plays and sees it all.
func TestLaunch(t *testing.T) {
	db := &fakeDB{}
	m := testModule(db)
	admin := false
	m.Admin = func(discord.Interaction) bool { return admin }
	arm := func() (*[]discord.MessageCreate, error) {
		e, sent := coretest.Button(t, "pp:arm", nil)
		return sent, m.press(context.Background(), e)
	}
	if _, err := arm(); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member armed the launch: %v", err)
	}
	if !strings.Contains(js(m.betaControls(3)), "pp:arm\"") {
		t.Fatal("the help page has no switch to arm the launch")
	}
	admin = true
	sent, err := arm()
	s := m.st.server(3)
	if err != nil || s.launch != DayOf(t0)+1 || !strings.Contains(db.execs[len(db.execs)-1], "launch") {
		t.Fatalf("arming: %v, launch %d, %v", err, s.launch, db.execs)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Content, fmt.Sprintf("<t:%d:R>", dayStart(DayOf(t0)+1).Unix())) {
		t.Errorf("the admin wasn't told when: %v", *sent)
	}
	if !strings.Contains(js(m.betaControls(3)), "armed") {
		t.Error("the help page doesn't say it's armed")
	}
	if _, err := arm(); err != nil || s.launch != 0 {
		t.Fatalf("disarming: %v, launch %d", err, s.launch)
	}
	if _, err := arm(); err != nil || s.launch == 0 {
		t.Fatalf("arming again: %v", err)
	}

	// The day before the turn: a member without the role plays nothing,
	// and an admin still sees what's new.
	admin = false
	if err := m.press(context.Background(), firstOf(coretest.Button(t, "pp:class", nil))); !errors.Is(err, errBeta) {
		t.Errorf("a member played before the launch: %v", err)
	}
	s.beta = 77
	member := func(id string) error {
		e, _ := coretest.Button(t, id, func(p map[string]any) { p["member"].(map[string]any)["roles"] = []any{"77"} })
		return m.press(context.Background(), e)
	}
	for _, id := range []string{"pp:tide", "pp:table", "pp:sit:1:0", "pp:letter", "pp:yardrenew"} {
		if err := member(id); !errors.Is(err, errNotYet) {
			t.Errorf("a beta tester pressed %s before the launch: %v", id, err)
		}
	}
	if rows := js(menuRows("bard", false, "pp:status", false, false, false)); strings.Contains(rows, "pp:tide") {
		t.Error("the menu shows the slack tide before the launch")
	}
	if m.seesNew(firstOf(coretest.Button(t, "pp:tide", nil)), 3) {
		t.Error("a member sees what's new before the launch")
	}
	admin = true
	if !m.seesNew(firstOf(coretest.Button(t, "pp:tide", nil)), 3) {
		t.Error("an admin doesn't see what's new before the launch")
	}

	// From the turn, everyone plays and sees it, and it can't be disarmed.
	m.now = func() time.Time { return dayStart(DayOf(t0) + 1) }
	admin = false
	if err := m.press(context.Background(), firstOf(coretest.Button(t, "pp:class", nil))); err != nil {
		t.Errorf("a member can't play once launched: %v", err)
	}
	if !m.seesNew(firstOf(coretest.Button(t, "pp:tide", nil)), 3) {
		t.Error("a member doesn't see what's new once launched")
	}
	if strings.Contains(js(m.betaControls(3)), "pp:arm\"") {
		t.Error("the switch is still there once launched")
	}
	admin = true
	if _, err := arm(); !errors.Is(err, errLaunched) {
		t.Errorf("a launch was disarmed after the turn: %v", err)
	}
}

// Once /pp is open, a member's first /pp asks for the beta role, in the
// background; not before it's open, and not for a member who holds it.
func TestEnlist(t *testing.T) {
	m := testModule(&fakeDB{})
	m.Admin = func(discord.Interaction) bool { return false }
	asked := make(chan [3]snowflake.ID, 4)
	m.Grant = func(_ context.Context, guild, user, role snowflake.ID) error {
		asked <- [3]snowflake.ID{guild, user, role}
		return nil
	}
	s := m.st.server(3)
	s.beta = 77
	run := func(roles ...any) {
		e, _ := coretest.Event(t, "pp", func(p map[string]any) {
			p["member"].(map[string]any)["roles"] = roles
		})
		_ = m.Commands()[0].Run(context.Background(), e)
	}
	none := func(why string) {
		select {
		case got := <-asked:
			t.Errorf("%s: asked %v", why, got)
		case <-time.After(50 * time.Millisecond):
		}
	}

	run()
	none("closed")
	s.open = true
	run("77")
	none("already holds it")
	run()
	select {
	case got := <-asked:
		if got != [3]snowflake.ID{3, 5, 77} {
			t.Errorf("asked %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("never asked")
	}
}

// Before the launch a member sees the season's new things nowhere: no
// morning sheet, no warm side, no bell on their screens, the stair's and
// the yard's lists in place of their painted fronts, and no letters go out.
func TestLaunchHidesNew(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, old(1), DayOf(t0))
	mem.classSeason, mem.signs.rings = 0, LetterRings
	s.sealed, s.houseCh = 1, 4
	buffs := js(m.st.buffList(s, mem, "", 0, DayOf(t0)))
	stair, _ := m.st.shopMessage(s, mem, true, false, false, dusk)()
	yard, _ := m.st.yardMessage(s, old(1), mem, tradesPage, DayOf(t0), false)
	s.mu.Unlock()
	if _, due := m.morning(guild, old(1)); due {
		t.Error("a morning sheet before the launch")
	}
	if strings.Contains(buffs, "warm side") {
		t.Error("the warm side before the launch")
	}
	if strings.Contains(js(stair), "pp:ladder") || !strings.Contains(js(yard), "pp:back\"") {
		t.Errorf("the painted fronts before the launch: %s %s", js(stair), js(yard))
	}
	m.sendLetters(context.Background())
	if len(s.letters) != 0 {
		t.Error("a letter went out before the launch")
	}
	for _, d := range []time.Duration{-30 * time.Minute, 30 * time.Minute} {
		at := Bells(m.st.seedOf(DayOf(t0)+1), DayOf(t0)+1)[0]
		m.now = func() time.Time { return at.Add(d) }
		if line := m.bellLine(guild); line != "" {
			t.Errorf("the bell before the launch: %q", line)
		}
	}
}
