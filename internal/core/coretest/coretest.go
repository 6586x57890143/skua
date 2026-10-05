// Package coretest builds slash-command interactions for handler tests, with
// no gateway and no network: the payload is Discord's JSON, and every
// response is recorded instead of sent.
package coretest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
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

// Modal is the submission of a modal whose custom ID is customID, with a
// text input per entry in values, from the same member, guild and channel
// as Event. edit and the returned slice work as they do for Event.
func Modal(t testing.TB, customID string, values map[string]string, edit func(p map[string]any)) (*events.ModalSubmitInteractionCreate, *[]discord.MessageCreate) {
	t.Helper()
	var rows []any
	for id, v := range values {
		rows = append(rows, map[string]any{"type": 1, "components": []any{
			map[string]any{"type": 4, "custom_id": id, "value": v},
		}})
	}
	p := map[string]any{
		"id": snowflake.New(time.Now()).String(), "application_id": "2", "type": 5, "token": "t", "version": 1,
		"guild_id": "3",
		"channel":  map[string]any{"id": "4", "type": 0},
		"member": map[string]any{
			"user":        map[string]any{"id": "5", "username": "member"},
			"permissions": "0",
			"roles":       []any{},
			"joined_at":   "2026-01-01T00:00:00Z",
		},
		"data": map[string]any{"custom_id": customID, "components": rows},
	}
	if edit != nil {
		edit(p)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var i discord.ModalSubmitInteraction
	if err := json.Unmarshal(raw, &i); err != nil {
		t.Fatalf("decoding the modal submission: %v", err)
	}
	var sent []discord.MessageCreate
	return &events.ModalSubmitInteractionCreate{
		GenericEvent:           events.NewGenericEvent(&bot.Client{}, 0, 0),
		ModalSubmitInteraction: i,
		Respond: func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			if m, ok := d.(discord.MessageCreate); ok {
				sent = append(sent, m)
			}
			return nil
		},
	}, &sent
}

// Button is a member in guild 3 pressing the button customID on an
// ephemeral reply, as Discord sends it. It records replies as Event does,
// a deferral as an empty one.
func Button(t testing.TB, customID string, edit func(p map[string]any)) (*events.ComponentInteractionCreate, *[]discord.MessageCreate) {
	t.Helper()
	p := map[string]any{
		"id": snowflake.New(time.Now()).String(), "application_id": "2", "type": 3, "token": "t", "version": 1,
		"guild_id": "3",
		"channel":  map[string]any{"id": "4", "type": 0},
		"member": map[string]any{
			"user":        map[string]any{"id": "5", "username": "member"},
			"permissions": "0",
			"roles":       []any{},
			"joined_at":   "2026-01-01T00:00:00Z",
		},
		"message": map[string]any{
			"id": "9", "channel_id": "4", "content": "", "flags": 64,
			"author": map[string]any{"id": "2", "username": "skua"},
		},
		"data": map[string]any{"custom_id": customID, "component_type": 2},
	}
	if edit != nil {
		edit(p)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var i discord.ComponentInteraction
	if err := json.Unmarshal(raw, &i); err != nil {
		t.Fatalf("decoding the button press: %v", err)
	}
	var sent []discord.MessageCreate
	return &events.ComponentInteractionCreate{
		GenericEvent:         events.NewGenericEvent(&bot.Client{}, 0, 0),
		ComponentInteraction: i,
		Respond: func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			if m, ok := d.(discord.MessageCreate); ok {
				sent = append(sent, m)
			}
			return nil
		},
	}, &sent
}

// Text is what a reply says: its content, then every text display in its
// containers, a line each.
func Text(content string, components []discord.LayoutComponent) string {
	out := content
	for _, c := range components {
		box, ok := c.(discord.ContainerComponent)
		if !ok {
			continue
		}
		for _, s := range box.Components {
			if t, ok := s.(discord.TextDisplayComponent); ok {
				if out != "" {
					out += "\n"
				}
				out += t.Content
			}
		}
	}
	return out
}

// Reason is the audit log reason opts would send, decoded, or "" for none.
// disgo's request config is unexported, so it is built by reflection.
func Reason(opts ...rest.RequestOpt) string {
	for _, o := range opts {
		fn := reflect.ValueOf(o)
		cfg := reflect.New(fn.Type().In(0).Elem())
		req := &http.Request{Header: http.Header{}}
		cfg.Elem().FieldByName("Request").Set(reflect.ValueOf(req))
		fn.Call([]reflect.Value{cfg})
		if r := req.Header.Get("X-Audit-Log-Reason"); r != "" {
			s, _ := url.QueryUnescape(r)
			return s
		}
	}
	return ""
}
