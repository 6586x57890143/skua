package pp

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// sureID is the confirm a buying press asked with.
var sureID = regexp.MustCompile(`pp:[a-z:]+:[0-9.]+:sure`)

// confirmOf presses id as user and returns the confirm its card asks with.
func confirmOf(t *testing.T, m *Module, id string, edit func(map[string]any)) string {
	t.Helper()
	e, sent := coretest.Button(t, id, edit)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatalf("asking about %s: %v", id, err)
	}
	if len(*sent) != 1 || !(*sent)[0].Flags.Has(discord.MessageFlagEphemeral) {
		t.Fatalf("%s didn't ask: %+v", id, *sent)
	}
	sure := sureID.FindString(js((*sent)[0]))
	if !strings.HasPrefix(sure, id+":") {
		t.Fatalf("%s asked with %q", id, sure)
	}
	return sure
}

// A confirm holds one ticket: a second card replaces the first, the
// confirm spends it, and pressing anything else or the day turning voids
// it, so no card buys what it didn't show.
func TestStaleConfirm(t *testing.T) {
	m, s, a, mem := ladderServer(t)
	who := func(p map[string]any) {
		p["member"].(map[string]any)["user"] = map[string]any{"id": a.String(), "username": "member"}
	}
	press := func(id string) error {
		e, _ := coretest.Button(t, id, who)
		e.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
			return nil
		}
		return m.press(context.Background(), e)
	}
	first, second := confirmOf(t, m, "pp:climb", who), confirmOf(t, m, "pp:climb", who)
	if err := press(first); !errors.Is(err, errStale) || mem.rung != 2 {
		t.Fatalf("a card another replaced bought: %v, rung %d", err, mem.rung)
	}
	if err := press(second); err != nil || mem.rung != 3 {
		t.Fatalf("the confirm: %v, rung %d", err, mem.rung)
	}
	if err := press(second); !errors.Is(err, errStale) || mem.rung != 3 {
		t.Errorf("a double tap bought the next rung: %v, rung %d", err, mem.rung)
	}
	// Something else pressed between: void.
	third := confirmOf(t, m, "pp:climb", who)
	if err := press("pp:quests"); err != nil {
		t.Fatal(err)
	}
	if err := press(third); !errors.Is(err, errStale) || mem.rung != 3 {
		t.Errorf("a confirm survived another press: %v", err)
	}
	// The day turning: void.
	fourth := confirmOf(t, m, "pp:climb", who)
	s.mu.Lock()
	m.st.member(s, guild, a, DayOf(t0)+1)
	s.mu.Unlock()
	if err := press(fourth); !errors.Is(err, errStale) {
		t.Errorf("a confirm survived the day: %v", err)
	}
	if p, tk, ok := confirmed("pp:lease:tallow:12:sure"); !ok || p != "pp:lease:tallow" || tk != 12 {
		t.Errorf("confirmed: %s %d %v", p, tk, ok)
	}
	if _, _, ok := confirmed("pp:climb"); ok {
		t.Error("a plain press read as a confirm")
	}
}

// ladderServer is a member on the second rung with plenty to climb on.
func ladderServer(t *testing.T) (*Module, *server, snowflake.ID, *member) {
	t.Helper()
	m := testModule(&fakeDB{})
	s := m.st.server(guild)
	a := old(1)
	mem := m.st.member(s, guild, a, DayOf(t0))
	mem.rung, mem.purse = 2, 100*m.st.pot()
	return m, s, a, mem
}
