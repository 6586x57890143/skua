package pp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

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

// An admin opens /pp to everyone once, from the help page, and then every
// member plays without the role; nobody else can open it.
func TestOpenToEveryone(t *testing.T) {
	db := &fakeDB{}
	m := testModule(db)
	admin := false
	m.Admin = func(discord.Interaction) bool { return admin }
	if err := m.press(context.Background(), firstOf(coretest.Button(t, "pp:open", nil))); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member opened /pp: %v", err)
	}
	if !strings.Contains(js(m.betaControls(3)), "pp:open\"") {
		t.Fatal("the help page has no switch to open /pp")
	}
	admin = true
	e, sent := coretest.Button(t, "pp:open", nil)
	if err := m.press(context.Background(), e); err != nil || !m.st.server(3).open || !strings.Contains(db.execs[len(db.execs)-1], "open = true") {
		t.Fatalf("opening /pp: %v %v", err, db.execs)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Content, "open") {
		t.Errorf("the admin was told %v", *sent)
	}
	if strings.Contains(js(m.betaControls(3)), "pp:open\"") {
		t.Error("the switch is still there once /pp is open")
	}
	admin = false
	if err := m.press(context.Background(), firstOf(coretest.Button(t, "pp:class", nil))); err != nil {
		t.Errorf("a member without the role can't play an open /pp: %v", err)
	}
}
