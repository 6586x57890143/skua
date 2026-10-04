package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
func (mod) Perms() discord.Permissions                                       { return 0 }
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
	c.Create = discord.SlashCommandCreate{Name: "a"}
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
		if !c.ran && (len(*sent) != 1 || (*sent)[0].Content != "✗ only this server's admins can use /a") {
			t.Errorf("%s: refusal reply = %+v", c.name, *sent)
		}
	}
}

func TestOnCommandReportsErrorsWhereTheMemberWillSeeThem(t *testing.T) {
	fail := fmt.Errorf("wrapped: %w", Tell("boom @everyone"))

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

func TestOnCommandHidesErrorsThatAreNotATell(t *testing.T) {
	raw := errors.New(`POST /webhooks: 403 Forbidden {"message": "Missing Permissions"}`)
	r := router(t, Command{Tier: Public, Run: func(context.Context, *events.ApplicationCommandInteractionCreate) error { return raw }})
	e, sent := coretest.Event(t, "a", nil)
	r.OnCommand(e)
	if len(*sent) != 1 || (*sent)[0].Content != "✗ "+failed {
		t.Fatalf("raw error reply = %+v", *sent)
	}
}

// A Tell is shown as written either way; only one with a cause under it is
// logged, so a failed post is in the logs and a refusal is not noise there.
func TestOnCommandLogsATellOnlyWhenItCarriesACause(t *testing.T) {
	cause := errors.New(`403 Forbidden {"message": "Missing Permissions"}`)
	for _, c := range []struct {
		name string
		err  error
		logs bool
	}{
		{"bare", Tell("your message didn't go through"), false},
		{"wrapped", fmt.Errorf("%w: %w", Tell("your message didn't go through"), cause), true},
	} {
		var log strings.Builder
		r := NewRouter(0, nil, slog.New(slog.NewTextHandler(&log, nil)))
		if err := r.Add(mod{[]Command{{
			Create: discord.SlashCommandCreate{Name: "a"},
			Tier:   Public,
			Run:    func(context.Context, *events.ApplicationCommandInteractionCreate) error { return c.err },
		}}}); err != nil {
			t.Fatal(err)
		}
		e, sent := coretest.Event(t, "a", nil)
		r.OnCommand(e)
		if len(*sent) != 1 || (*sent)[0].Content != "✗ your message didn't go through" {
			t.Fatalf("%s: reply = %+v", c.name, *sent)
		}
		if logged := strings.Contains(log.String(), "interaction failed"); logged != c.logs {
			t.Errorf("%s: logged = %v, want %v: %q", c.name, logged, c.logs, log.String())
		}
		if c.logs && !strings.Contains(log.String(), "Missing Permissions") {
			t.Errorf("%s: the cause is not in the log: %q", c.name, log.String())
		}
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

// modalMod is a Module that also opens modals.
type modalMod struct {
	mod
	modals []Modal
}

func (m modalMod) Modals() []Modal { return m.modals }

func TestAddRefusesMalformedModals(t *testing.T) {
	run := func(context.Context, *events.ModalSubmitInteractionCreate) error { return nil }
	for want, md := range map[string]Modal{
		"needs an ID":    {Run: run},
		"no colon":       {ID: "a:b", Run: run},
		"has no handler": {ID: "a"},
	} {
		err := NewRouter(0, nil, slog.Default()).Add(modalMod{modals: []Modal{md}})
		if err == nil || !strings.Contains(err.Error(), strings.Fields(want)[len(strings.Fields(want))-1]) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	r := NewRouter(0, nil, slog.Default())
	if err := r.Add(modalMod{modals: []Modal{{ID: "a", Run: run}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(modalMod{modals: []Modal{{ID: "a", Run: run}}}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("a duplicate modal ID: %v", err)
	}
	if err := r.Add(mod{[]Command{{Tier: Public}}}); err == nil || !strings.Contains(err.Error(), "no Create") {
		t.Errorf("a command with no Create: %v", err)
	}
}

func TestSlashAndMessageCommandsMayShareAName(t *testing.T) {
	var ran []string
	r := NewRouter(0, nil, slog.New(slog.DiscardHandler))
	if err := r.Add(mod{[]Command{
		{Create: discord.SlashCommandCreate{Name: "a"}, Tier: Public, Run: func(context.Context, *events.ApplicationCommandInteractionCreate) error {
			ran = append(ran, "slash")
			return nil
		}},
		{Create: discord.MessageCommandCreate{Name: "a"}, Tier: Public, Run: func(context.Context, *events.ApplicationCommandInteractionCreate) error {
			ran = append(ran, "message")
			return nil
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	e, _ := coretest.Event(t, "a", nil)
	r.OnCommand(e)
	e, _ = coretest.Event(t, "a", func(p map[string]any) {
		d := p["data"].(map[string]any)
		d["type"], d["target_id"] = 3, "8"
		d["resolved"] = map[string]any{"messages": map[string]any{"8": map[string]any{
			"id": "8", "channel_id": "4", "content": "hi", "author": map[string]any{"id": "5", "username": "member"},
		}}}
	})
	r.OnCommand(e)
	if strings.Join(ran, ",") != "slash,message" {
		t.Errorf("ran %v, want each command once by its own type", ran)
	}
}

type compMod struct {
	mod
	comps []Component
}

func (c compMod) Components() []Component { return c.comps }

func TestOnComponentRoutesByPrefix(t *testing.T) {
	var got string
	r := NewRouter(0, nil, slog.New(slog.DiscardHandler))
	if err := r.Add(compMod{comps: []Component{
		{ID: "more", Run: func(_ context.Context, e *events.ComponentInteractionCreate) error {
			got = e.Data.CustomID()
			return nil
		}},
		{ID: "fails", Run: func(context.Context, *events.ComponentInteractionCreate) error { return Tell("boom") }},
	}}); err != nil {
		t.Fatal(err)
	}
	e, _ := coretest.Button(t, "more:7", nil)
	r.OnComponent(e)
	if got != "more:7" {
		t.Errorf("handler saw %q", got)
	}
	e, sent := coretest.Button(t, "fails", nil)
	r.OnComponent(e)
	if len(*sent) != 1 || (*sent)[0].Content != "✗ boom" {
		t.Errorf("error: sent %+v", *sent)
	}
	e, sent = coretest.Button(t, "nobody", nil)
	r.OnComponent(e)
	if len(*sent) != 0 {
		t.Errorf("an unknown button got a reply: %+v", *sent)
	}
	run := func(context.Context, *events.ComponentInteractionCreate) error { return nil }
	if err := r.Add(compMod{comps: []Component{{ID: "more", Run: run}}}); err == nil || !strings.Contains(err.Error(), "component more registered twice") {
		t.Errorf("a duplicate component ID: %v", err)
	}
	if err := r.Add(compMod{comps: []Component{{ID: "x:y", Run: run}}}); err == nil || !strings.Contains(err.Error(), "no colon") {
		t.Errorf("a component ID with a colon: %v", err)
	}
}

func TestOnModalRoutesByPrefixAndReportsErrors(t *testing.T) {
	fail := Tell("boom") // a Tell, so the member reads it as written
	var got string
	r := NewRouter(0, nil, slog.New(slog.DiscardHandler))
	if err := r.Add(modalMod{modals: []Modal{
		{ID: "edit", Run: func(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
			if d, ok := ctx.Deadline(); !ok || !d.Equal(e.ID().Time().Add(respondBy)) {
				t.Errorf("deadline = %v, want creation time + %v", d, respondBy)
			}
			got = e.Data.CustomID + "=" + e.Data.Text("text")
			return nil
		}},
		{ID: "early", Run: func(context.Context, *events.ModalSubmitInteractionCreate) error { return fail }},
		{ID: "late", Run: func(_ context.Context, e *events.ModalSubmitInteractionCreate) error {
			if err := e.DeferCreateMessage(true); err != nil {
				return err
			}
			return fail
		}},
		{ID: "panics", Run: func(context.Context, *events.ModalSubmitInteractionCreate) error { panic("x") }},
	}}); err != nil {
		t.Fatal(err)
	}

	e, _ := coretest.Modal(t, "edit:4:8", map[string]string{"text": "new words"}, nil)
	r.OnModal(e)
	if got != "edit:4:8=new words" {
		t.Errorf("handler saw %q", got)
	}

	e, sent := coretest.Modal(t, "early", nil, nil)
	r.OnModal(e)
	if len(*sent) != 1 || (*sent)[0].Content != "✗ boom" {
		t.Errorf("early error: sent %+v", *sent)
	}

	e, sent = coretest.Modal(t, "late:x", nil, nil)
	f := &fakeRest{}
	e.Client().Rest = f
	r.OnModal(e)
	if len(*sent) != 1 || len(f.followups) != 1 || f.followups[0] != "✗ boom" {
		t.Errorf("error after deferring: sent %+v, followups %q", *sent, f.followups)
	}

	e, _ = coretest.Modal(t, "panics", nil, nil)
	r.OnModal(e) // must not panic out
	e, sent = coretest.Modal(t, "nobody", nil, nil)
	r.OnModal(e)
	if len(*sent) != 0 {
		t.Errorf("an unknown modal got a reply: %+v", *sent)
	}
}
