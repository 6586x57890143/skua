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

// A confirm names what the member stood at when asked: pressed after that
// has changed it's refused, so a second card can't buy the next rung.
func TestStaleConfirm(t *testing.T) {
	m, _, a, mem := ladderServer(t)
	who := func(p map[string]any) {
		p["member"].(map[string]any)["user"] = map[string]any{"id": a.String(), "username": "member"}
	}
	first, second := confirmOf(t, m, "pp:climb", who), confirmOf(t, m, "pp:climb", who)
	if first != second {
		t.Fatalf("two cards asked differently: %s %s", first, second)
	}
	press := func(id string) error {
		e, _ := coretest.Button(t, id, who)
		e.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
			return nil
		}
		return m.press(context.Background(), e)
	}
	if err := press(first); err != nil || mem.rung != 3 {
		t.Fatalf("the confirm: %v, rung %d", err, mem.rung)
	}
	if err := press(second); !errors.Is(err, errStale) || mem.rung != 3 {
		t.Errorf("a second card bought the next rung: %v, rung %d", err, mem.rung)
	}
	if p, tok, ok := confirmed("pp:lease:tallow:12.6:sure"); !ok || p != "pp:lease:tallow" || tok != "12.6" {
		t.Errorf("confirmed: %s %s %v", p, tok, ok)
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
