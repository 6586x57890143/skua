// Package coretest builds slash-command interactions for handler tests, with
// no gateway and no network: the payload is Discord's JSON, and every
// response is recorded instead of sent.
package coretest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// Event is /name run in guild 3, channel 4, by user 5 holding no
// permissions, created just now so the router's response deadline is live.
// edit changes the raw payload before it is decoded, the same way Discord
// would have sent it. The returned slice collects each message the handler
// responds with. e.Client().Rest is nil; a test that needs REST sets it to a
// fake that embeds rest.Rest and overrides only the calls it expects.
func Event(t testing.TB, name string, edit func(p map[string]any)) (*events.ApplicationCommandInteractionCreate, *[]discord.MessageCreate) {
	t.Helper()
	p := map[string]any{
		"id": snowflake.New(time.Now()).String(), "application_id": "2", "type": 2, "token": "t", "version": 1,
		"guild_id": "3",
		"channel":  map[string]any{"id": "4", "type": 0},
		"member": map[string]any{
			"user":        map[string]any{"id": "5", "username": "member"},
			"permissions": "0",
			"roles":       []any{},
			"joined_at":   "2026-01-01T00:00:00Z",
		},
		"data": map[string]any{"id": "6", "name": name, "type": 1},
	}
	if edit != nil {
		edit(p)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var i discord.ApplicationCommandInteraction
	if err := json.Unmarshal(raw, &i); err != nil {
		t.Fatalf("decoding the interaction: %v", err)
	}
	var sent []discord.MessageCreate
	return &events.ApplicationCommandInteractionCreate{
		GenericEvent:                  events.NewGenericEvent(&bot.Client{}, 0, 0),
		ApplicationCommandInteraction: i,
		Respond: func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			if m, ok := d.(discord.MessageCreate); ok {
				sent = append(sent, m)
			}
			return nil
		},
	}, &sent
}
