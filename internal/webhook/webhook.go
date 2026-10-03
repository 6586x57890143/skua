// Package webhook posts on a member's behalf through one webhook per
// channel that skua owns. Echo uses it now; automod's rewrite, which
// reposts a member's message with a slur replaced, is meant to use the same
// Poster, so both share one cache and one set of guard budgets.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

type hook struct {
	id    snowflake.ID
	token string
}

// Poster is safe for concurrent use.
type Poster struct {
	guard *guard.Guard
	hooks sync.Map // channel snowflake.ID -> hook
}

// New takes the process's one guard, so webhook writes share each guild's
// breaker with every other writer.
func New(g *guard.Guard) *Poster { return &Poster{guard: g} }

// Send posts msg in channel through skua's webhook there, finding or
// creating it on first use. Every write spends guild budget through the
// guard and reports how Discord answered.
func (p *Poster) Send(ctx context.Context, r rest.Rest, guild, channel, app snowflake.ID, msg discord.WebhookMessageCreate) error {
	opt := rest.WithCtx(ctx)
	for attempt := 0; ; attempt++ {
		h, err := p.hook(r, opt, guild, channel, app)
		if err != nil {
			return err
		}
		if err := p.guard.Allow(guild, guard.WebhookExecute); err != nil {
			return err
		}
		_, err = r.CreateWebhookMessage(h.id, h.token, msg, rest.CreateWebhookMessageParams{}, opt)
		p.guard.Report(guild, struggling(err))
		// A mod deleted the webhook: forget it and make another, once.
		if isCode(err, rest.JSONErrorCodeUnknownWebhook) && attempt == 0 {
			p.hooks.Delete(channel)
			continue
		}
		return err
	}
}

// hook returns this channel's webhook: cached, else the one skua already
// owns there (so a restart reuses it), else a new one.
//
// ponytail: two first posts in one channel at the same moment can each
// create a webhook, against Discord's 15 per channel. Both work and later
// lookups settle on one; a per-channel singleflight is the fix if it bites.
func (p *Poster) hook(r rest.Rest, opt rest.RequestOpt, guild, ch, app snowflake.ID) (hook, error) {
	if v, ok := p.hooks.Load(ch); ok {
		return v.(hook), nil
	}
	existing, err := r.GetWebhooks(ch, opt)
	if err != nil {
		return hook{}, fmt.Errorf("listing webhooks: %w", err)
	}
	for _, w := range existing {
		if in, ok := w.(discord.IncomingWebhook); ok && in.ApplicationID != nil && *in.ApplicationID == app && in.Token != "" {
			h := hook{in.ID(), in.Token}
			p.hooks.Store(ch, h)
			return h, nil
		}
	}
	if err := p.guard.Allow(guild, guard.WebhookCreate); err != nil {
		return hook{}, err
	}
	in, err := r.CreateWebhook(ch, discord.WebhookCreate{Name: "skua"}, opt)
	p.guard.Report(guild, struggling(err))
	if err != nil {
		return hook{}, fmt.Errorf("creating the webhook: %w", err)
	}
	h := hook{in.ID(), in.Token}
	p.hooks.Store(ch, h)
	return h, nil
}

// struggling is true only for answers that say Discord is, not the request.
func struggling(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	if !ok || re.Response == nil {
		return false
	}
	s := re.Response.StatusCode
	return s == 429 || s >= 500
}

func isCode(err error, code rest.JSONErrorCode) bool {
	re, ok := errors.AsType[*rest.Error](err)
	return ok && re.Code == code
}
