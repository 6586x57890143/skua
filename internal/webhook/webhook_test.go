package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

const app = 100

// fake stands in for Discord: the three webhook calls a Poster makes, and
// nothing else. Each queued error is returned by one call, in order.
type fake struct {
	rest.Rest
	owned              []discord.Webhook
	listErr, createErr error
	execErrs           []error
	lists, creates     int
	sentTo             []snowflake.ID
}

func (f *fake) GetWebhooks(snowflake.ID, ...rest.RequestOpt) ([]discord.Webhook, error) {
	f.lists++
	return f.owned, f.listErr
}

func (f *fake) CreateWebhook(snowflake.ID, discord.WebhookCreate, ...rest.RequestOpt) (*discord.IncomingWebhook, error) {
	f.creates++
	if f.createErr != nil {
		return nil, f.createErr
	}
	w := incoming(900+f.creates, app)
	return &w, nil
}

func (f *fake) CreateWebhookMessage(id snowflake.ID, _ string, _ discord.WebhookMessageCreate, _ rest.CreateWebhookMessageParams, _ ...rest.RequestOpt) (*discord.Message, error) {
	if len(f.execErrs) > 0 {
		err := f.execErrs[0]
		f.execErrs = f.execErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	f.sentTo = append(f.sentTo, id)
	return nil, nil
}

func incoming(id, appID int) discord.IncomingWebhook {
	var w discord.IncomingWebhook
	if err := json.Unmarshal(fmt.Appendf(nil, `{"id":"%d","type":1,"token":"t%d","application_id":"%d"}`, id, id, appID), &w); err != nil {
		panic(err)
	}
	return w
}

func send(p *Poster, f *fake) error {
	return p.Send(context.Background(), f, 3, 4, app, discord.WebhookMessageCreate{Content: "hi"})
}

func TestSendCreatesOnceThenUsesTheCache(t *testing.T) {
	p, f := New(guard.New()), &fake{}
	for range 3 {
		if err := send(p, f); err != nil {
			t.Fatal(err)
		}
	}
	if f.lists != 1 || f.creates != 1 || len(f.sentTo) != 3 {
		t.Errorf("lists=%d creates=%d sent=%d, want one lookup, one webhook, three posts", f.lists, f.creates, len(f.sentTo))
	}
}

func TestSendReusesTheWebhookSkuaAlreadyOwns(t *testing.T) {
	f := &fake{owned: []discord.Webhook{incoming(700, 999), incoming(701, app)}}
	if err := send(New(guard.New()), f); err != nil {
		t.Fatal(err)
	}
	if f.creates != 0 || f.sentTo[0] != 701 {
		t.Errorf("creates=%d sent to %v, want skua's own 701", f.creates, f.sentTo)
	}
}

func TestSendRetriesADeletedWebhookOnce(t *testing.T) {
	unknown := &rest.Error{Code: rest.JSONErrorCodeUnknownWebhook}

	f := &fake{execErrs: []error{unknown}}
	if err := send(New(guard.New()), f); err != nil {
		t.Fatalf("one deleted webhook: %v", err)
	}
	if f.creates != 2 || len(f.sentTo) != 1 {
		t.Errorf("creates=%d sent=%d, want a fresh webhook and one post", f.creates, len(f.sentTo))
	}

	f = &fake{execErrs: []error{unknown, unknown, nil}}
	if err := send(New(guard.New()), f); !errors.Is(err, unknown) {
		t.Fatalf("deleted twice: err %v, want the second Unknown Webhook", err)
	}
	if f.creates != 2 || len(f.sentTo) != 0 {
		t.Errorf("creates=%d sent=%d, want exactly one retry", f.creates, len(f.sentTo))
	}
}

func TestSendReportsWhatStoppedIt(t *testing.T) {
	boom := errors.New("boom")
	for name, f := range map[string]*fake{
		"listing":  {listErr: boom},
		"creating": {createErr: boom},
	} {
		if err := send(New(guard.New()), f); !errors.Is(err, boom) {
			t.Errorf("%s: err %v, want boom", name, err)
		}
	}
	// A guild over its webhook budget is refused before Discord is asked.
	p, f := New(guard.New()), &fake{}
	var err error
	for range 1000 {
		if err = send(p, f); err != nil {
			break
		}
	}
	if !errors.Is(err, guard.ErrRateLimited) {
		t.Fatalf("1000 posts in one guild: %v, want the guard's cap", err)
	}
	// Creation has its own, smaller budget.
	p = New(guard.New())
	for ch := range snowflake.ID(100) {
		if err = p.Send(context.Background(), &fake{}, 3, ch, app, discord.WebhookMessageCreate{}); err != nil {
			break
		}
	}
	if !errors.Is(err, guard.ErrRateLimited) {
		t.Fatalf("webhooks in 100 channels: %v, want the creation cap", err)
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
