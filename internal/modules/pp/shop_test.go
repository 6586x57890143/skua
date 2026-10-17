package pp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// A rung costs twice the one before; climbing burns its price into the
// ledger and the season's burn, and a purse short of it can't climb.
func TestLadder(t *testing.T) {
	m := testModule(&fakeDB{})
	st := m.st
	s := st.server(guild)
	a := old(1)
	pd := st.potDay
	if rungPrice(pd, 0) != 2*pd || rungPrice(pd, 3) != 16*pd {
		t.Fatalf("rungs cost %d and %d", rungPrice(pd, 0), rungPrice(pd, 3))
	}
	mem := st.member(s, guild, a, DayOf(t0))
	mem.purse = 7 * pd
	if err := st.climb(s, guild, a, DayOf(t0)); err != nil {
		t.Fatal(err)
	}
	if err := st.climb(s, guild, a, DayOf(t0)); err != nil {
		t.Fatal(err)
	}
	if mem.rung != 2 || mem.purse != pd || s.seasonBurn != 6*pd {
		t.Errorf("rung %d purse %d burned %d", mem.rung, mem.purse, s.seasonBurn)
	}
	if last := s.pending[len(s.pending)-1]; last.reason != reasonShop || last.delta != -4*pd {
		t.Errorf("the ledger got %+v", last)
	}
	if err := st.climb(s, guild, a, DayOf(t0)); !errors.Is(err, errCantClimb) {
		t.Errorf("a short purse climbed: %v", err)
	}

	// Through the buttons: the shop opens, and climbing redraws it.
	mem.purse = 10 * pd
	var opened []discord.MessageCreate
	e, sent := coretest.Button(t, "pp:shop", func(p map[string]any) {
		p["member"].(map[string]any)["user"] = map[string]any{"id": a.String(), "username": "member"}
	})
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	opened = *sent
	if len(opened) != 1 || !strings.Contains(js(opened[0].Components), "climb to the chitin perch") {
		t.Fatalf("the shop showed %v", js(opened))
	}
	c, _ := coretest.Button(t, "pp:climb", func(p map[string]any) {
		p["member"].(map[string]any)["user"] = map[string]any{"id": a.String(), "username": "member"}
	})
	updated := false
	c.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
		updated = true
		return nil
	}
	if err := m.press(context.Background(), c); err != nil || !updated || mem.rung != 3 {
		t.Errorf("climbing: %v, updated %v, rung %d", err, updated, mem.rung)
	}
	if !strings.Contains(js(m.Report(guild).Rows), "burned") {
		t.Error("the help page doesn't show the season's burn")
	}
}

// Two seasons that burn under the floor switch the heavy purse line on:
// past it a purse keeps a tenth of new earning, the level all of it. A
// season that burns enough switches it off.
func TestBackstop(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	quiet := func() { s.seasonMint, s.seasonBurn = 1000, 10; st.seasonTurn(s) }
	busy := func() { s.seasonMint, s.seasonBurn = 1000, 300; st.seasonTurn(s) }
	quiet()
	if s.heavy {
		t.Fatal("one quiet season switched the line on")
	}
	quiet()
	if !s.heavy {
		t.Fatal("two quiet seasons left the line off")
	}
	if !strings.Contains(sinksRow(s), "backstop on") {
		t.Errorf("the help page says %q", sinksRow(s))
	}

	a := old(1)
	mem := st.member(s, guild, a, DayOf(t0))
	mem.purse = Rules.Line * st.potDay
	before, total := mem.purse, mem.total
	st.chat(s, guild, a, "a heavy purse earns", t0)
	gained, earned := mem.purse-before, mem.total-total
	if earned == 0 || gained*10 > earned || mem.dayWithheld == 0 {
		t.Errorf("past the line it earned %d and kept %d (withheld %d)", earned, gained, mem.dayWithheld)
	}
	st.chat(s, guild, a, "the day closes", t0.Add(24*time.Hour))
	if in := s.pending[0].inputs; len(in) != 11 || in[10] == 0 {
		t.Errorf("the closed day's row doesn't say what was withheld: %v", in)
	}

	busy()
	if s.heavy {
		t.Error("a busy season left the line on")
	}
}
