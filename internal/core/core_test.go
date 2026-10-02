package core

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

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

// event builds a slash-command interaction for /name in a guild, with
// Respond recording each initial response's content.
func event(t *testing.T, name string, r rest.Rest, responses *[]string) *events.ApplicationCommandInteractionCreate {
	t.Helper()
	var i discord.ApplicationCommandInteraction
	payload := `{"id":"1300000000000000000","application_id":"2","type":2,"token":"tok","version":1,
		"guild_id":"3","channel":{"id":"4","type":0},
		"member":{"user":{"id":"5","username":"u"},"roles":[],"joined_at":"2020-01-01T00:00:00Z","permissions":"0"},
		"data":{"id":"6","name":"` + name + `","type":1}}`
	if err := json.Unmarshal([]byte(payload), &i); err != nil {
		t.Fatal(err)
	}
	return &events.ApplicationCommandInteractionCreate{
		GenericEvent:                  events.NewGenericEvent(&bot.Client{Rest: r}, 0, 0),
		ApplicationCommandInteraction: i,
		Respond: func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			*responses = append(*responses, d.(discord.MessageCreate).Content)
			return nil
		},
	}
}

func TestOnCommandReportsErrorsWhereTheMemberWillSeeThem(t *testing.T) {
	fail := errors.New("boom")
	r := NewRouter(0, nil, slog.Default())
	if err := r.Add(mod{[]Command{
		{Create: discord.SlashCommandCreate{Name: "early"}, Tier: Public, Run: func(context.Context, *events.ApplicationCommandInteractionCreate) error {
			return fail
		}},
		{Create: discord.SlashCommandCreate{Name: "late"}, Tier: Public, Run: func(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
			if err := e.CreateMessage(discord.MessageCreate{Content: "working"}); err != nil {
				return err
			}
			return fail
		}},
		{Create: discord.SlashCommandCreate{Name: "deadline"}, Tier: Public, Run: func(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
			if d, ok := ctx.Deadline(); !ok || !d.Equal(e.ID().Time().Add(respondBy)) {
				t.Errorf("deadline = %v, want creation time + %v", d, respondBy)
			}
			return nil
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	var responses []string
	f := &fakeRest{}
	r.OnCommand(event(t, "early", f, &responses))
	if len(responses) != 1 || responses[0] != "✗ boom" || len(f.followups) != 0 {
		t.Errorf("early error: responses=%q followups=%q", responses, f.followups)
	}

	responses, f = nil, &fakeRest{}
	r.OnCommand(event(t, "late", f, &responses))
	if len(responses) != 1 || len(f.followups) != 1 || f.followups[0] != "✗ boom" {
		t.Errorf("error after responding: responses=%q followups=%q", responses, f.followups)
	}

	r.OnCommand(event(t, "deadline", &fakeRest{}, &responses))
}
