package pp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/pixel"
)

// A rung costs twice the one before; climbing burns its price into the
// ledger and the season's burn, and a purse short of it can't climb.
func TestLadder(t *testing.T) {
	m := testModule(&fakeDB{})
	st := m.st
	s := st.server(guild)
	a := old(1)
	pd := st.pot()
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
	// The first press asks; only the confirm climbs.
	c, asked := coretest.Button(t, "pp:climb", func(p map[string]any) {
		p["member"].(map[string]any)["user"] = map[string]any{"id": a.String(), "username": "member"}
	})
	if err := m.press(context.Background(), c); err != nil || mem.rung != 2 || len(*asked) != 1 || !strings.HasPrefix(sureID.FindString(js((*asked)[0])), "pp:climb:") {
		t.Fatalf("the climb didn't ask first: %v, rung %d", err, mem.rung)
	}
	c, _ = coretest.Button(t, sureID.FindString(js((*asked)[0])), func(p map[string]any) {
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
	mem.purse = Rules.Line * st.pot()
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

// The shop's front opens a rung's own card from its menu, in place: the
// next rung's card can climb, a climbed one can't, back shows the front
// again, and a rung the menu never offered is refused.
func TestShopCards(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(guild)
	a := old(1)
	mem := m.st.member(s, guild, a, DayOf(t0))
	mem.rung, mem.purse = 2, 10*m.st.pot()
	as := func(p map[string]any) {
		p["member"].(map[string]any)["user"] = map[string]any{"id": a.String(), "username": "member"}
	}
	e, sent := coretest.Button(t, "pp:shop", as)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := js((*sent)[0].Components); !strings.Contains(got, "pp:look") || !strings.Contains(got, "rung 1 · climbed") || strings.Contains(got, "pot days") {
		t.Fatalf("the front showed %s", got)
	}
	press := func(id string, value string) (string, error) {
		e, _ := coretest.Button(t, id, func(p map[string]any) {
			as(p)
			if value != "" {
				p["data"] = map[string]any{"custom_id": id, "component_type": 3, "values": []any{value}}
			}
		})
		var got string
		e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			got = js(d)
			return nil
		}
		err := m.press(context.Background(), e)
		return got, err
	}
	got, err := press("pp:look", "3")
	if err != nil || strings.Contains(got, "-# ") || !strings.Contains(got, "pp:climb") || !strings.Contains(got, "pp:back") {
		t.Errorf("the next rung's card: %v %s", err, got)
	}
	if got, err := press("pp:look", "1"); err != nil || strings.Contains(got, "pp:climb") {
		t.Errorf("a climbed rung's card: %v %s", err, got)
	}
	if got, err := press("pp:back", ""); err != nil || !strings.Contains(got, "pp:look") {
		t.Errorf("back: %v %s", err, got)
	}
	for _, bad := range []string{"0", "65", "x", ""} {
		if _, err := press("pp:look", bad); !errors.Is(err, errNoRung) {
			t.Errorf("a look at %q: %v", bad, err)
		}
	}
	if mem.rung != 2 {
		t.Errorf("looking climbed to %d", mem.rung)
	}
}

// The status card's buttons sit at most four to a row, as UX.md has them,
// and every one wears an icon that's drawn.
func TestMenuRows(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, row := range menuRows("sorcerer", admin, "pp:status") {
			if n := len(row.(discord.ActionRowComponent).Components); n > 4 {
				t.Errorf("a row of %d buttons", n)
			}
		}
	}
	for _, icon := range []string{"compass", "laurel", "scales", "quill", "hearth", "bell", "pot", "lantern", "envelope"} {
		if _, ok := pixel.Icons[icon]; !ok {
			t.Errorf("a button wears %s, which isn't drawn", icon)
		}
	}
}

// The buffs button explains every buff, lighting the ones worn, and the
// hearth names its tier while it's lit.
func TestBuffsCard(t *testing.T) {
	m := testModule(&fakeDB{})
	m.info = func(snowflake.ID) (snowflake.ID, string) { return 0, "Melted" }
	s := m.st.server(guild)
	mem := m.st.member(s, guild, old(1), DayOf(t0))
	mem.wear.Crest = true
	s.lit = 2
	buffs := m.st.buffList(s, mem, "Melted", 0)
	if len(buffs) != 5 || !buffs[0].On || buffs[1].On || buffs[1].How != "put .gg/melted in your custom status" || buffs[3].Name != "surety" || buffs[3].On || buffs[4].Name != "hearth ii" || !buffs[4].On || !strings.Contains(buffs[4].How, "of its day, rises at") {
		t.Errorf("buffs: %+v", buffs)
	}
	// A fire left from yesterday, nothing earned yet today: the next
	// threshold keeps it for tomorrow; a fresh day lights; past it, rises.
	for _, c := range []struct {
		lit, before int
		want        string
	}{{0, 0, "lights at 80%"}, {0, 1, "burns on tomorrow at 80%"}, {1, 0, "rises at 100%"}, {1, 2, "burns on tomorrow at 100%"}} {
		s.lit, s.litBefore = c.lit, c.before
		if got := m.st.hearthHow(s); !strings.HasSuffix(got, c.want) {
			t.Errorf("lit %d, yesterday %d: %q", c.lit, c.before, got)
		}
	}
	s.lit, s.litBefore = 2, 0
	e, sent := coretest.Button(t, "pp:buffs", func(p map[string]any) {
		p["member"].(map[string]any)["user"] = map[string]any{"id": old(1).String(), "username": "member"}
	})
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := js((*sent)[0]); strings.Contains(got, "-# ") || !strings.Contains(got, "crest, hearth ii worn") {
		t.Errorf("the buffs card: %s", got)
	}
}
