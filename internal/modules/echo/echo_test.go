package echo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

func TestCheck(t *testing.T) {
	ok := map[string]string{
		"hi":             "hi",
		"  padded  ":     "padded",
		"a # in the mid": "a # in the mid",
		"> quoted":       "> quoted",
		"1. first":       "1. first",
		"#1 fan":         "#1 fan",
		"2024 #goals":    "2024 #goals",
		"-#nospace":      "-#nospace",
	}
	for in, want := range ok {
		if got, err := check(in); err != nil || got != want {
			t.Errorf("check(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "   ",
		"a\nb", "a\rb", "a b", "a b", "a\tb",
		"# x", "## x", "### x", "-# x", "> -# x", "- -# x", "* ## x", "  -# echoed through skua by @mod",
		"1. -# echoed through skua by @mod", "2) # x",
		strings.Repeat("a", maxText+1),
	} {
		if got, err := check(in); err == nil {
			t.Errorf("check(%q) = %q, want refused", in, got)
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
		"data":{"id":"6","name":"echo","type":1,"options":[{"name":"text","type":3,"value":%q}]}}`,
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
		{"cannot start with", opts{text: "-# fake", perms: permSend}},
		{"text channels", opts{channelType: int(discord.ChannelTypeGuildPublicThread), perms: permSend}},
		{"timed out", opts{perms: permSend, until: "2999-01-01T00:00:00Z"}},
		{"cannot send", opts{perms: discord.PermissionViewChannel}},
		{"Manage Webhooks", opts{perms: permSend, appPms: discord.PermissionSendMessages}},
	}
	for _, c := range cases {
		f := &fake{}
		_, err := run(t, New(guard.New()), f, c.o)
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
	m := New(guard.New())
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

func TestEchoReusesTheWebhookSkuaAlreadyOwns(t *testing.T) {
	f := &fake{owned: []discord.Webhook{incoming("700", "999"), incoming("701", app)}}
	if _, err := run(t, New(guard.New()), f, opts{perms: permSend}); err != nil {
		t.Fatal(err)
	}
	if f.creates != 0 || f.sentHookID[0] != 701 {
		t.Errorf("creates=%d used=%v, want skua's own 701", f.creates, f.sentHookID)
	}
}

func TestEchoRetriesADeletedWebhookOnce(t *testing.T) {
	unknown := &rest.Error{Code: rest.JSONErrorCodeUnknownWebhook}

	f := &fake{execErrs: []error{unknown}}
	if _, err := run(t, New(guard.New()), f, opts{perms: permSend}); err != nil {
		t.Fatalf("one deleted webhook: %v", err)
	}
	if f.creates != 2 || len(f.sent) != 1 {
		t.Errorf("creates=%d sent=%d, want a fresh webhook and one post", f.creates, len(f.sent))
	}

	f = &fake{execErrs: []error{unknown, unknown, nil}}
	if _, err := run(t, New(guard.New()), f, opts{perms: permSend}); !errors.Is(err, unknown) {
		t.Fatalf("deleted twice: err %v, want the second UnknownWebhook", err)
	}
	if f.creates != 2 || len(f.sent) != 0 {
		t.Errorf("creates=%d sent=%d, want exactly one retry", f.creates, len(f.sent))
	}
}

func TestEchoCapsEachMember(t *testing.T) {
	m, f := New(guard.New()), &fake{}
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

func TestStruggling(t *testing.T) {
	status := func(code int) error { return &rest.Error{Response: &http.Response{StatusCode: code}} }
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("network"), false},
		{status(429), true},
		{status(502), true},
		{status(404), false},
		{&rest.Error{Code: 1}, false},
		{fmt.Errorf("wrapped: %w", status(503)), true},
	}
	for _, c := range cases {
		if got := struggling(c.err); got != c.want {
			t.Errorf("struggling(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestEchoHonoursSlowmode(t *testing.T) {
	m, f := New(guard.New()), &fake{}
	clock := time.Unix(1_000_000, 0)
	m.now = func() time.Time { return clock }
	o := opts{perms: permSend, slowmode: 30}

	if _, err := run(t, m, f, o); err != nil {
		t.Fatalf("first echo: %v", err)
	}
	clock = clock.Add(10 * time.Second)
	if _, err := run(t, m, f, o); err == nil || !strings.Contains(err.Error(), "try again in 20s") {
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
	if _, err := run(t, New(guard.New()), f, opts{perms: permSend}); err != nil || len(f.sent) != 1 {
		t.Fatalf("err %v, sent %d: a posted echo must not report failure", err, len(f.sent))
	}
}

func TestRefusalsAnswerAtOnceWithoutDeferring(t *testing.T) {
	f := &fake{}
	reply, err := run(t, New(guard.New()), f, opts{perms: discord.PermissionViewChannel})
	if err == nil || reply != "" || f.deletes != 0 {
		t.Fatalf("reply %q, err %v, deletes %d: a refusal should reach the router undeferred", reply, err, f.deletes)
	}
}
