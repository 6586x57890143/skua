package core

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/intents"
)

type mod struct{ cmds []Command }

func (mod) Name() string                                                     { return "test" }
func (mod) Want() intents.Want                                               { return intents.Want{} }
func (m mod) Commands() []Command                                            { return m.cmds }
func run(context.Context, *events.ApplicationCommandInteractionCreate) error { return nil }

func TestAddRefusesMalformed(t *testing.T) {
	cases := map[string]Command{
		"no valid tier": {Create: discord.SlashCommandCreate{Name: "a"}, Run: run},
		"no handler":    {Create: discord.SlashCommandCreate{Name: "a"}, Tier: Public},
	}
	for want, c := range cases {
		err := NewRouter(0, nil, slog.Default()).Add(mod{[]Command{c}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want an error containing %q", err, want)
		}
	}
	ok := Command{Create: discord.SlashCommandCreate{Name: "a"}, Tier: Admin, Run: run}
	if err := NewRouter(0, nil, slog.Default()).Add(mod{[]Command{ok, ok}}); err == nil {
		t.Error("a duplicate name was accepted")
	}
	r := NewRouter(0, nil, slog.Default())
	if err := r.Add(mod{[]Command{ok}}); err != nil || len(r.Creates()) != 1 {
		t.Errorf("valid command: err=%v creates=%d", err, len(r.Creates()))
	}
}

func TestNoPingsMarshalsAnEmptyParse(t *testing.T) {
	b, err := json.Marshal(NoPings())
	if err != nil || !strings.Contains(string(b), `"parse":[]`) {
		t.Fatalf("got %s (%v), want \"parse\":[]", b, err)
	}
}

// fakeRest records followups; every other method is unimplemented.
type fakeRest struct {
	rest.Rest
	followups []string
}

func (f *fakeRest) CreateFollowupMessage(_ snowflake.ID, _ string, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.followups = append(f.followups, m.Content)
	return &discord.Message{}, nil
}

func router(t *testing.T, c Command) *Router {
	t.Helper()
	r := NewRouter(9, func(g snowflake.ID) (snowflake.ID, bool) { return 7, g == 3 }, slog.New(slog.DiscardHandler))
	c.Create.Name = "a"
	if err := r.Add(mod{[]Command{c}}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOnCommandTiers(t *testing.T) {
	admin := func(p map[string]any) { p["member"].(map[string]any)["permissions"] = "8" }
	asUser := func(id string) func(map[string]any) {
		return func(p map[string]any) { p["member"].(map[string]any)["user"].(map[string]any)["id"] = id }
	}
	cases := []struct {
		name string
		tier Tier
		edit func(map[string]any)
		ran  bool
	}{
		{"public runs for anyone", Public, nil, true},
		{"admin refused for a plain member", Admin, nil, false},
		{"admin by Administrator bit", Admin, admin, true},
		{"admin by bootstrap user", Admin, asUser("9"), true},
		{"admin by guild owner", Admin, asUser("7"), true},
		{"owner of another guild is not admin", Admin, func(p map[string]any) { asUser("7")(p); p["guild_id"] = "8" }, false},
	}
	for _, c := range cases {
		ran := false
		r := router(t, Command{Tier: c.tier, Run: func(context.Context, *events.ApplicationCommandInteractionCreate) error { ran = true; return nil }})
		e, sent := coretest.Event(t, "a", c.edit)
		r.OnCommand(e)
		if ran != c.ran {
			t.Errorf("%s: ran = %v", c.name, ran)
		}
		if !c.ran && (len(*sent) != 1 || (*sent)[0].Content != "✗ not allowed") {
			t.Errorf("%s: refusal reply = %+v", c.name, *sent)
		}
	}
}

func TestOnCommandReportsErrorsWhereTheMemberWillSeeThem(t *testing.T) {
	fail := errors.New("boom @everyone")

	// Before any response: the error is the initial response, ephemeral
	// and ping-free.
	r := router(t, Command{Tier: Public, Run: func(context.Context, *events.ApplicationCommandInteractionCreate) error { return fail }})
	e, sent := coretest.Event(t, "a", nil)
	f := &fakeRest{}
	e.Client().Rest = f
	r.OnCommand(e)
	if len(*sent) != 1 || len(f.followups) != 0 {
		t.Fatalf("early error: sent=%+v followups=%q", *sent, f.followups)
	}
	if m := (*sent)[0]; m.Content != "✗ boom @everyone" || m.Flags != discord.MessageFlagEphemeral || m.AllowedMentions == nil || m.AllowedMentions.Parse == nil {
		t.Fatalf("early error reply = %+v", m)
	}

	// After a response: a second initial response would be refused, so
	// the error goes out as a followup.
	r = router(t, Command{Tier: Public, Run: func(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
		if err := e.CreateMessage(discord.MessageCreate{Content: "working"}); err != nil {
			return err
		}
		return fail
	}})
	e, sent = coretest.Event(t, "a", nil)
	f = &fakeRest{}
	e.Client().Rest = f
	r.OnCommand(e)
	if len(*sent) != 1 || len(f.followups) != 1 || f.followups[0] != "✗ boom @everyone" {
		t.Errorf("error after responding: sent=%+v followups=%q", *sent, f.followups)
	}
}

func TestOnCommandDeadlineIsCreationPlusWindow(t *testing.T) {
	r := router(t, Command{Tier: Public, Run: func(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
		if d, ok := ctx.Deadline(); !ok || !d.Equal(e.ID().Time().Add(respondBy)) {
			t.Errorf("deadline = %v, want creation time + %v", d, respondBy)
		}
		if ctx.Err() != nil {
			t.Error("a fresh interaction's ctx is already done")
		}
		return nil
	}})
	e, _ := coretest.Event(t, "a", nil)
	r.OnCommand(e)
}

func TestOnCommandSurvivesPanicsAndUnknownCommands(t *testing.T) {
	r := router(t, Command{Tier: Public, Run: func(context.Context, *events.ApplicationCommandInteractionCreate) error { panic("x") }})
	e, _ := coretest.Event(t, "a", nil)
	r.OnCommand(e) // must not panic out
	e, sent := coretest.Event(t, "nope", nil)
	r.OnCommand(e)
	if len(*sent) != 0 {
		t.Fatalf("an unknown command got a reply: %+v", *sent)
	}
}
