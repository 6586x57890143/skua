package purge

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
)

func router(t *testing.T, m *Module) *core.Router {
	t.Helper()
	r := core.NewRouter(0, func(snowflake.ID) (snowflake.ID, bool) { return 0, false }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := r.Add(m); err != nil {
		t.Fatal(err)
	}
	return r
}

// command is /purge <sub> from member 5 in guild 3, with whatever the
// handler answers recorded: a message's content, or "modal".
func command(t *testing.T, m *Module, sub string, edit func(p map[string]any)) string {
	t.Helper()
	e, _ := coretest.Event(t, "purge", func(p map[string]any) {
		p["data"].(map[string]any)["options"] = []any{map[string]any{"name": sub, "type": 1}}
		if edit != nil {
			edit(p)
		}
	})
	var got string
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		switch d := d.(type) {
		case discord.ModalCreate:
			got = "modal"
		case discord.MessageCreate:
			got = d.Content
		}
		return nil
	}
	router(t, m).OnCommand(e)
	return got
}

// submit is the confirmation box sent back with text, against f.
func submit(t *testing.T, m *Module, f *fake, text string) []discord.MessageCreate {
	t.Helper()
	e, sent := coretest.Modal(t, confirmModal, map[string]string{"confirm": text}, nil)
	e.Client().Rest = f
	router(t, m).OnModal(e)
	return *sent
}

func newModule() *Module {
	m := New(guard.New(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.pace = newPacer(1e9)
	return m
}

// final waits for the readout that ends a sweep.
func final(t *testing.T, f *fake) string {
	t.Helper()
	for {
		select {
		case u := <-f.updates:
			if !strings.Contains(u, "still going") {
				return u
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the sweep never reported")
		}
	}
}

func TestPurgeNowAsksThenSweeps(t *testing.T) {
	m := newModule()
	m.tick = time.Millisecond
	if got := command(t, m, "now", nil); got != "modal" {
		t.Fatalf("/purge now answered %q, want the confirmation box", got)
	}
	f := server(t, time.Now())
	sent := submit(t, m, f, " Delete ")
	if len(sent) != 1 || !sent[0].Flags.Has(discord.MessageFlagEphemeral) {
		t.Fatalf("confirm answered %+v, want one deferred ephemeral reply", sent)
	}
	got := final(t, f)
	for _, want := range []string{"deleted      ", "✓ done in", "<#12>"} {
		if !strings.Contains(got, want) {
			t.Errorf("readout lacks %q:\n%s", want, got)
		}
	}
	for _, m := range f.msgs[textCh] {
		if m.Author.ID == me && !f.gone[m.ID] {
			t.Fatal("confirming left the member's messages")
		}
	}
}

func TestPurgeRefusals(t *testing.T) {
	m := newModule()
	if got := command(t, m, "now", func(p map[string]any) { delete(p, "guild_id") }); got != "✗ "+string(errNotServer) {
		t.Errorf("outside a server: %q", got)
	}
	if got := command(t, m, "stop", nil); got != "✗ you have no purge running here" {
		t.Errorf("stop with nothing running: %q", got)
	}
	f := newFake()
	if sent := submit(t, m, f, "yes"); len(sent) != 1 || sent[0].Content != "✗ nothing deleted: type delete to confirm" {
		t.Errorf("wrong confirmation: %+v", sent)
	}
	e, sent := coretest.Modal(t, confirmModal, map[string]string{"confirm": "delete"}, func(p map[string]any) { delete(p, "guild_id") })
	router(t, m).OnModal(e)
	if len(*sent) != 1 || (*sent)[0].Content != "✗ "+string(errNotServer) {
		t.Errorf("confirm outside a server: %+v", *sent)
	}
}

func TestPurgeMemberBudget(t *testing.T) {
	m := newModule()
	for m.guard.Allow(me, guard.PurgeMember) == nil {
	}
	sent := submit(t, m, newFake(), "delete")
	if len(sent) != 1 || !strings.Contains(sent[0].Content, "as often as an hour allows") {
		t.Fatalf("over budget: %+v", sent)
	}
	if _, ok := m.running.Load(target{guildID, me}); ok {
		t.Fatal("a refused start stayed registered as running")
	}
}

// One sweep per member per server: a second is refused while the first
// runs, and /purge stop ends the first.
func TestOneSweepAtATimeAndStop(t *testing.T) {
	m := newModule()
	m.tick = time.Hour
	f := server(t, time.Now())
	f.hold = make(chan struct{})
	submit(t, m, f, "delete")
	if got := command(t, m, "now", nil); got != "✗ "+string(errRunning) {
		t.Errorf("/purge now while running: %q", got)
	}
	if sent := submit(t, m, f, "delete"); len(sent) != 1 || sent[0].Content != "✗ "+string(errRunning) {
		t.Errorf("a second confirmation while running: %+v", sent)
	}
	if got := command(t, m, "stop", nil); !strings.HasPrefix(got, "✓ stopping") {
		t.Errorf("stop: %q", got)
	}
	close(f.hold)
	if got := final(t, f); !strings.Contains(got, "✗ stopped after") {
		t.Errorf("after stop:\n%s", got)
	}
	if len(f.gone) != 0 {
		t.Error("a stopped sweep deleted something")
	}
}

func TestOutcomeAndRender(t *testing.T) {
	if got := outcome(errors.New("boom"), 90*time.Second); !strings.Contains(got, "went wrong on skua's side after 1m") {
		t.Errorf("unknown error: %q", got)
	}
	if got := outcome(errRefused, 0); got != "✗ "+string(errRefused) {
		t.Errorf("tell: %q", got)
	}
	s := &sweep{}
	for i := range 12 {
		s.unreach(snowflake.ID(100 + i))
	}
	s.unreach(100)
	got := render(s, "x")
	if !strings.Contains(got, "unreachable  12") || !strings.Contains(got, "<#109> and 2 more") {
		t.Errorf("render:\n%s", got)
	}
	for d, want := range map[time.Duration]string{
		45 * time.Second: "45s", 12 * time.Minute: "12m",
		3*time.Hour + 12*time.Minute: "3h 12m", 52 * time.Hour: "2d 4h",
	} {
		if got := span(d); got != want {
			t.Errorf("span(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestModuleDeclares(t *testing.T) {
	m := newModule()
	if m.Name() != "purge" || m.Want().Required != gateway.IntentGuildMessages {
		t.Error("purge needs guild messages and nothing else")
	}
	for _, p := range []discord.Permissions{discord.PermissionReadMessageHistory, discord.PermissionManageMessages, discord.PermissionManageThreads} {
		if !m.Perms().Has(p) {
			t.Errorf("Perms lacks %v", p)
		}
	}
	// A progress edit past the token's life is not sent.
	f := newFake()
	m.show(f, 2, "t", time.Now().Add(-time.Hour), "late")
	select {
	case u := <-f.updates:
		t.Fatalf("sent %q on an expired token", u)
	default:
	}
}
