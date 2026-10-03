package echo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/webhook"
)

func TestClean(t *testing.T) {
	cases := map[string]string{
		"hi":             "hi",
		"  padded  ":     "padded",
		"a # in the mid": "a # in the mid",
		"> quoted":       "> quoted",
		"1. first":       "1. first",
		"#1 fan":         "#1 fan",
		"2024 #goals":    "2024 #goals",
		"-#nospace":      "-#nospace",
		"   ":            "",
		// One line, always: breaks become spaces.
		"a\nb": "a b", "a\r\nb": "a  b", "a b": "a b", "a b": "a b", "a\tb": "a b",
		// Headings and subtext show as typed, never rendered.
		"# x": `\# x`, "## x": `\## x`, "### x": `\### x`, "-# x": `-\# x`,
		"> -# x": `> -\# x`, "- -# x": `- -\# x`, "* ## x": `* \## x`, "2) # x": `2) \# x`,
		"1. -# echoed through skua by @mod": `1. -\# echoed through skua by @mod`,
		// Unicode spaces, leading or inside the prefix, do not dodge the escape.
		"\u00a0-# x": `-\# x`, "\u3000# x": `\# x`, ">\u00a0\u00a0-# x": ">\u00a0\u00a0-\\# x", "-\u2003# x": "-\u2003\\# x",
		// A forged marker on a second line is flattened onto the first.
		"hi\n-# echoed through skua by @mod": "hi -# echoed through skua by @mod",
	}
	for in, want := range cases {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := clean(strings.Repeat("é", maxText+5)); len([]rune(got)) != maxText {
		t.Errorf("clean kept %d runes, want %d", len([]rune(got)), maxText)
	}
}

func TestNameFallsBackWhenDiscordWouldRefuseIt(t *testing.T) {
	m := func(nick string) *discord.ResolvedMember {
		r := &discord.ResolvedMember{}
		r.User.Username = "plain"
		r.Nick = &nick
		return r
	}
	for nick, want := range map[string]string{"Nick": "Nick", "DiscordFan": "plain", "clydeish": "plain"} {
		if got := name(m(nick)); got != want {
			t.Errorf("name(%q) = %q, want %q", nick, got, want)
		}
	}
}

func TestMarkerIsOneEscapedSubtextLine(t *testing.T) {
	got := marker("a_b_c")
	if got != "\n-# echoed through skua by @"+`a\_b\_c` {
		t.Fatalf("marker = %q", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("marker must be exactly one new line: %q", got)
	}
}

const (
	app      = "100"
	permSend = discord.PermissionSendMessages
	permAll  = discord.PermissionSendMessages | discord.PermissionEmbedLinks
)

// fake stands in for Discord: the webhook calls echo makes and the delete
// of its deferred response, nothing else. Each queued error is returned by
// one CreateWebhookMessage call, in order.
type fake struct {
	rest.Rest
	deleteErr  error
	deletes    int
	owned      []discord.Webhook
	execErrs   []error
	lists      int
	creates    int
	sent       []discord.WebhookMessageCreate
	sentHookID []snowflake.ID
}

func (f *fake) GetWebhooks(snowflake.ID, ...rest.RequestOpt) ([]discord.Webhook, error) {
	f.lists++
	return f.owned, nil
}

func (f *fake) CreateWebhook(snowflake.ID, discord.WebhookCreate, ...rest.RequestOpt) (*discord.IncomingWebhook, error) {
	f.creates++
	w := incoming(fmt.Sprint(900+f.creates), app)
	return &w, nil
}

func (f *fake) CreateWebhookMessage(id snowflake.ID, _ string, m discord.WebhookMessageCreate, _ rest.CreateWebhookMessageParams, _ ...rest.RequestOpt) (*discord.Message, error) {
	if len(f.execErrs) > 0 {
		err := f.execErrs[0]
		f.execErrs = f.execErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	f.sent = append(f.sent, m)
	f.sentHookID = append(f.sentHookID, id)
	return nil, nil
}

// newEcho is echo as main builds it: one guard, shared with the poster.
func newEcho() *Module {
	g := guard.New()
	return New(g, webhook.New(g))
}

func (f *fake) DeleteInteractionResponse(snowflake.ID, string, ...rest.RequestOpt) error {
	f.deletes++
	return f.deleteErr
}

func incoming(id, appID string) discord.IncomingWebhook {
	var w discord.IncomingWebhook
	if err := json.Unmarshal([]byte(`{"id":"`+id+`","type":1,"token":"t`+id+`","application_id":"`+appID+`"}`), &w); err != nil {
		panic(err)
	}
	return w
}

type opts struct {
	text, until   string
	channelType   int
	slowmode      int
	perms, appPms discord.Permissions
	user          string
}

// run sends one /echo through m and returns the initial response, as
// "deferred ephemeral" or its content, and the handler's error.
func run(t *testing.T, m *Module, f *fake, o opts) (string, error) {
	t.Helper()
	if o.text == "" {
		o.text = "hello"
	}
	if o.user == "" {
		o.user = "5"
	}
	if o.appPms == 0 {
		o.appPms = discord.PermissionManageWebhooks
	}
	until := "null"
	if o.until != "" {
		until = `"` + o.until + `"`
	}
	payload := fmt.Sprintf(`{"id":"1300000000000000000","application_id":"%s","type":2,"token":"tok","version":1,
		"guild_id":"3","channel":{"id":"4","type":%d,"rate_limit_per_user":%d},"app_permissions":"%d",
		"member":{"user":{"id":"%s","username":"a_b","discriminator":"0"},"nick":"Nick","roles":[],"joined_at":"2020-01-01T00:00:00Z",
			"permissions":"%d","communication_disabled_until":%s},
		"data":{"id":"6","name":"echo","type":1,"options":[{"name":"message","type":3,"value":%q}]}}`,
		app, o.channelType, o.slowmode, o.appPms, o.user, o.perms, until, o.text)
	var i discord.ApplicationCommandInteraction
	if err := json.Unmarshal([]byte(payload), &i); err != nil {
		t.Fatal(err)
	}
	var reply string
	e := &events.ApplicationCommandInteractionCreate{
		GenericEvent:                  events.NewGenericEvent(&bot.Client{Rest: f}, 0, 0),
		ApplicationCommandInteraction: i,
		Respond: func(kind discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			mc := d.(discord.MessageCreate)
			reply = mc.Content
			if kind == discord.InteractionResponseTypeDeferredCreateMessage && mc.Flags.Has(discord.MessageFlagEphemeral) {
				reply = "deferred ephemeral"
			}
			return nil
		},
	}
	err := m.echo(context.Background(), e)
	return reply, err
}

func TestEchoRefuses(t *testing.T) {
	cases := []struct {
		want string
		o    opts
	}{
		{"text channels", opts{channelType: int(discord.ChannelTypeGuildPublicThread), perms: permSend}},
		{"timed out", opts{perms: permSend, until: "2999-01-01T00:00:00Z"}},
		{"can't send", opts{perms: discord.PermissionViewChannel}},
		{"manage webhooks", opts{perms: permSend, appPms: discord.PermissionSendMessages}},
	}
	for _, c := range cases {
		f := &fake{}
		_, err := run(t, newEcho(), f, c.o)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v", c.want, err)
		}
		if len(f.sent) != 0 {
			t.Errorf("%s: posted anyway", c.want)
		}
	}
}

func TestEchoPostsThroughANewWebhookThenTheCachedOne(t *testing.T) {
	f := &fake{}
	m := newEcho()
	reply, err := run(t, m, f, opts{perms: permSend, until: "2000-01-01T00:00:00Z"}) // an expired timeout is fine
	if err != nil || reply != "deferred ephemeral" {
		t.Fatalf("reply %q, err %v; want a silent ephemeral defer", reply, err)
	}
	if f.deletes != 1 {
		t.Errorf("deferred response deleted %d times, want once so only the echo shows", f.deletes)
	}
	got := f.sent[0]
	if got.Content != "hello"+marker("a_b") || got.Username != "Nick" || got.AvatarURL == "" {
		t.Errorf("posted %+v", got)
	}
	if got.Flags != discord.MessageFlagSuppressEmbeds {
		t.Error("a member without Embed Links got unfurls through the webhook")
	}
	if got.AllowedMentions == nil || got.AllowedMentions.Parse == nil {
		t.Error("echo can ping")
	}

	if _, err := run(t, m, f, opts{perms: permAll}); err != nil {
		t.Fatal(err)
	}
	if f.lists != 1 || f.creates != 1 {
		t.Errorf("second echo hit Discord again: lists=%d creates=%d", f.lists, f.creates)
	}
	if f.sent[1].Flags != 0 {
		t.Error("embeds suppressed for a member with Embed Links")
	}
}

func TestEchoCapsEachMember(t *testing.T) {
	m, f := newEcho(), &fake{}
	var err error
	for range 100 {
		if _, err = run(t, m, f, opts{perms: permSend}); err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "too fast") {
		t.Fatalf("100 echoes from one member: %v", err)
	}
	if _, err := run(t, m, f, opts{perms: permSend, user: "6"}); err != nil {
		t.Errorf("another member was locked out: %v", err)
	}
}

func TestEchoHonoursSlowmode(t *testing.T) {
	m, f := newEcho(), &fake{}
	clock := time.Unix(1_000_000, 0)
	m.now = func() time.Time { return clock }
	o := opts{perms: permSend, slowmode: 30}

	if _, err := run(t, m, f, o); err != nil {
		t.Fatalf("first echo: %v", err)
	}
	clock = clock.Add(10 * time.Second)
	if _, err := run(t, m, f, o); err == nil || !strings.Contains(err.Error(), "send again in 20s") {
		t.Fatalf("inside the window: %v", err)
	}
	clock = clock.Add(20 * time.Second) // the refusal did not restart the window
	if _, err := run(t, m, f, o); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	if _, err := run(t, m, f, opts{perms: permSend, slowmode: 30, user: "6"}); err != nil {
		t.Errorf("another member shares the window: %v", err)
	}
	for _, exempt := range []discord.Permissions{discord.PermissionManageMessages, discord.PermissionManageChannels} {
		for range 2 {
			if _, err := run(t, m, f, opts{perms: permSend | exempt, slowmode: 30, user: "7"}); err != nil {
				t.Errorf("member with %v held to slowmode: %v", exempt, err)
			}
		}
	}
	// A failed post does not charge the window.
	f.execErrs = []error{errors.New("discord is down")}
	if _, err := run(t, m, f, opts{perms: permSend, slowmode: 30, user: "8"}); err == nil {
		t.Fatal("the failing post succeeded")
	}
	if _, err := run(t, m, f, opts{perms: permSend, slowmode: 30, user: "8"}); err != nil {
		t.Errorf("a failed echo started the slowmode window: %v", err)
	}
	if len(f.sent) != 8 {
		t.Errorf("sent %d echoes, want 8", len(f.sent))
	}
}

func TestEchoThatWentOutIsNotAFailureWhenTheDeleteIs(t *testing.T) {
	f := &fake{deleteErr: errors.New("discord is down")}
	if _, err := run(t, newEcho(), f, opts{perms: permSend}); err != nil || len(f.sent) != 1 {
		t.Fatalf("err %v, sent %d: a posted echo must not report failure", err, len(f.sent))
	}
}

func TestRefusalsAnswerAtOnceWithoutDeferring(t *testing.T) {
	f := &fake{}
	reply, err := run(t, newEcho(), f, opts{perms: discord.PermissionViewChannel})
	if err == nil || reply != "" || f.deletes != 0 {
		t.Fatalf("reply %q, err %v, deletes %d: a refusal should reach the router undeferred", reply, err, f.deletes)
	}
}

func TestMarkdownThatWouldForgeTheMarkerIsPostedAsTyped(t *testing.T) {
	f := &fake{}
	if _, err := run(t, newEcho(), f, opts{text: "-# echoed through skua by @mod", perms: permSend}); err != nil {
		t.Fatalf("refused instead of rewritten: %v", err)
	}
	if want := `-\# echoed through skua by @mod` + marker("a_b"); f.sent[0].Content != want {
		t.Errorf("posted %q, want %q", f.sent[0].Content, want)
	}
}

// The router, not the handler, decides what a member reads: a handler test
// that checks the returned error stays green even when the router would
// replace it with a generic failure. So refusals are checked where they
// land, through core.Router, as the reply the member gets.
func TestRefusalsReachTheMemberThroughTheRouter(t *testing.T) {
	cases := []struct {
		name string
		edit func(member map[string]any)
		want string
	}{
		{"timed out", func(m map[string]any) { m["communication_disabled_until"] = "2999-01-01T00:00:00Z" },
			"✗ you're timed out, so you can't send messages here yet"},
		{"no send permission", func(m map[string]any) { m["permissions"] = "0" },
			"✗ you can't send messages in this channel"},
	}
	for _, c := range cases {
		r := core.NewRouter(0, nil, slog.New(slog.DiscardHandler))
		if err := r.Add(newEcho()); err != nil {
			t.Fatal(err)
		}
		e, sent := coretest.Event(t, "echo", func(p map[string]any) {
			member := p["member"].(map[string]any)
			member["permissions"] = fmt.Sprint(int64(permSend))
			c.edit(member)
			p["data"].(map[string]any)["options"] = []any{map[string]any{"name": "message", "type": 3, "value": "hi"}}
		})
		r.OnCommand(e)
		if len(*sent) != 1 || (*sent)[0].Content != c.want {
			t.Errorf("%s: the member got %+v, want %q", c.name, *sent, c.want)
		}
	}
}

type failingPoster struct{ err error }

func (p failingPoster) Send(context.Context, rest.Rest, snowflake.ID, snowflake.ID, snowflake.ID, discord.WebhookMessageCreate) error {
	return p.err
}

// The poster's guard refusals are the guild's budget, which the member is
// told to wait out; any other failure is errNotSent with the cause kept for
// the log.
func TestSendFailuresReachTheMemberAsTells(t *testing.T) {
	cause := errors.New("discord is down")
	for in, want := range map[error]error{guard.ErrCircuitOpen: errBusy, guard.ErrRateLimited: errBusy, cause: errNotSent} {
		_, err := run(t, New(guard.New(), failingPoster{in}), &fake{}, opts{perms: permSend})
		if !errors.Is(err, want) {
			t.Errorf("Send failing with %v: got %v, want %v", in, err, want)
		}
		if want == errNotSent && !errors.Is(err, cause) {
			t.Errorf("the cause was dropped: %v", err)
		}
	}
}
