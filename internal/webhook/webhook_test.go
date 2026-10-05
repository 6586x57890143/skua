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

	"github.com/6586x57890143/skua/internal/core/coretest"
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
	reason             string // the last create's audit log reason
	sentTo             []snowflake.ID
	got, edited, gone  []snowflake.ID // message IDs read, updated, deleted
}

func (f *fake) GetWebhookMessage(_ snowflake.ID, _ string, m snowflake.ID, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.got = append(f.got, m)
	return &discord.Message{ID: m}, nil
}

func (f *fake) UpdateWebhookMessage(_ snowflake.ID, _ string, m snowflake.ID, _ discord.WebhookMessageUpdate, _ rest.UpdateWebhookMessageParams, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.edited = append(f.edited, m)
	return nil, nil
}

func (f *fake) DeleteWebhookMessage(_ snowflake.ID, _ string, m, _ snowflake.ID, _ ...rest.RequestOpt) error {
	f.gone = append(f.gone, m)
	return nil
}

func (f *fake) GetWebhooks(snowflake.ID, ...rest.RequestOpt) ([]discord.Webhook, error) {
	f.lists++
	return f.owned, f.listErr
}

func (f *fake) CreateWebhook(_ snowflake.ID, _ discord.WebhookCreate, opts ...rest.RequestOpt) (*discord.IncomingWebhook, error) {
	f.creates++
	f.reason = coretest.Reason(opts...)
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
	_, err := p.Send(context.Background(), f, 3, 4, app, discord.WebhookMessageCreate{Content: "hi"})
	return err
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
	if f.reason != "skua's webhook for whisper and bird" {
		t.Errorf("audit log reason %q", f.reason)
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
		if _, err = p.Send(context.Background(), &fake{}, 3, ch, app, discord.WebhookMessageCreate{}); err != nil {
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

func TestGetEditDeleteUseSkuasOwnWebhookOnly(t *testing.T) {
	ctx := context.Background()
	// Cached from a post: no lookup needed.
	p, f := New(guard.New()), &fake{}
	if err := send(p, f); err != nil {
		t.Fatal(err)
	}
	hook := f.sentTo[0]
	if _, err := p.Get(ctx, f, 4, app, hook, 50); err != nil {
		t.Fatal(err)
	}
	if err := p.Edit(ctx, f, 3, 4, app, hook, 50, discord.WebhookMessageUpdate{}); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(ctx, f, 3, 4, app, hook, 50); err != nil {
		t.Fatal(err)
	}
	if f.lists != 1 || len(f.got) != 1 || len(f.edited) != 1 || len(f.gone) != 1 {
		t.Errorf("lists=%d got=%v edited=%v gone=%v, want one of each on the cached token", f.lists, f.got, f.edited, f.gone)
	}

	// After a restart the cache is empty: the webhook is found by ID among
	// the channel's, a second one of skua's included.
	f = &fake{owned: []discord.Webhook{incoming(701, app), incoming(702, app)}}
	if err := New(guard.New()).Delete(ctx, f, 3, 4, app, 702, 51); err != nil || len(f.gone) != 1 {
		t.Fatalf("delete through skua's second webhook: err %v, gone %v", err, f.gone)
	}

	// Someone else's webhook, or another app's, is never ours.
	f = &fake{owned: []discord.Webhook{incoming(800, 999)}}
	for _, id := range []snowflake.ID{800, 801} {
		if err := New(guard.New()).Delete(ctx, f, 3, 4, app, id, 52); !errors.Is(err, ErrNotOurs) {
			t.Errorf("webhook %d: err %v, want ErrNotOurs", id, err)
		}
		if _, err := New(guard.New()).Get(ctx, f, 4, app, id, 52); !errors.Is(err, ErrNotOurs) {
			t.Errorf("get through webhook %d: err %v, want ErrNotOurs", id, err)
		}
		if err := New(guard.New()).Edit(ctx, f, 3, 4, app, id, 52, discord.WebhookMessageUpdate{}); !errors.Is(err, ErrNotOurs) {
			t.Errorf("edit through webhook %d: err %v, want ErrNotOurs", id, err)
		}
	}
	if len(f.gone)+len(f.edited)+len(f.got) != 0 {
		t.Error("a foreign webhook's message was touched")
	}

	boom := errors.New("boom")
	if err := New(guard.New()).Delete(ctx, &fake{listErr: boom}, 3, 4, app, 701, 53); !errors.Is(err, boom) {
		t.Errorf("listing failed: err %v", err)
	}
}

func TestEditAndDeleteSpendTheGuildsBudget(t *testing.T) {
	ctx := context.Background()
	f := &fake{owned: []discord.Webhook{incoming(701, app)}}
	p := New(guard.New())
	var err error
	for range 1000 {
		if err = p.Delete(ctx, f, 3, 4, app, 701, 1); err != nil {
			break
		}
	}
	if !errors.Is(err, guard.ErrRateLimited) {
		t.Errorf("1000 deletes: %v, want the guard's cap", err)
	}
	for range 1000 {
		if err = p.Edit(ctx, f, 3, 4, app, 701, 1, discord.WebhookMessageUpdate{}); err != nil {
			break
		}
	}
	if !errors.Is(err, guard.ErrRateLimited) {
		t.Errorf("1000 edits: %v, want the guard's cap", err)
	}
}
