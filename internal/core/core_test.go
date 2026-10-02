package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

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
