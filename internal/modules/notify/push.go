package notify

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // WebSub signs with HMAC-SHA1; it is the hub's choice, not ours
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Push: twitch (EventSub), kick (webhooks) and youtube (WebSub) call skua
// at SKUA_HOOKS_URL when something happens, through the tunnel to
// SKUA_HOOKS_ADDR. A push only says which account to look at: skua then
// checks that one account at once, the way a timer would, so a card is
// built and deduplicated exactly as before. The timers stay behind it as
// a slow safety net (reconcile), so a lost push makes a card late, never
// missing.

// pusher is a source that can be told to push.
type pusher interface {
	// subscribe makes the platform push for exactly accounts, adding and
	// dropping subscriptions to match.
	subscribe(ctx context.Context, accounts []string, h hooks) error
	// reconcile is how often to check anyway while push is on.
	reconcile() time.Duration
}

// hooks is where pushes arrive and the secret they are signed with.
type hooks struct {
	url, secret string
}

func (h hooks) on() bool { return h.url != "" }

// maxPush caps a push body: every platform sends a few kilobytes.
const maxPush = 1 << 20

// replay is how old a signed twitch push may be before it's refused.
const replay = 10 * time.Minute

// refreshAt is when an account is checked after a push: at once, then
// twice more, since a stream's details can trail the push that says it
// started by a few seconds.
var refreshAt = []time.Duration{0, 20 * time.Second, time.Minute}

// Hooks is the handler for SKUA_HOOKS_ADDR: one path per platform.
func (m *Module) Hooks() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /twitch", m.twitchPush)
	mux.HandleFunc("POST /kick", m.kickPush)
	mux.HandleFunc("GET /youtube", m.youtubeVerify)
	mux.HandleFunc("POST /youtube", m.youtubePush)
	return mux
}

// refresh checks one account now and at each of refreshAt.
func (m *Module) refresh(platform, account string) {
	src, ok := m.sources[platform]
	if !ok || !slices.Contains(m.accounts(platform), account) {
		return
	}
	for _, d := range refreshAt {
		time.AfterFunc(d, func() {
			m.mu.Lock()
			p := m.poster
			m.mu.Unlock()
			if p == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			got, err := src.check(ctx, []string{account})
			if items, ok := got[account]; ok {
				m.observe(ctx, p, key{platform, account}, items, src)
			} else if err != nil {
				m.log.Warn("notify: checking after a push", "platform", platform, "account", account, "err", err)
			}
		})
	}
}

// subscribe brings a platform's push subscriptions in line with what is
// followed there. Called at boot, after every change and once a day,
// which renews youtube's leases.
func (m *Module) subscribe(platform string) {
	src, ok := m.sources[platform].(pusher)
	if !ok || !m.hooks.on() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := src.subscribe(ctx, m.accounts(platform), m.hooks); err != nil {
		m.log.Warn("notify: subscribing to pushes", "platform", platform, "err", err)
	}
}

// body reads a push up to maxPush.
func body(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPush))
	if err != nil {
		http.Error(w, "too big", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return b, true
}

// twitchPush is EventSub: a signed challenge when a subscription is made,
// then a notification each time a stream starts.
func (m *Module) twitchPush(w http.ResponseWriter, r *http.Request) {
	b, ok := body(w, r)
	if !ok {
		return
	}
	id, at := r.Header.Get("Twitch-Eventsub-Message-Id"), r.Header.Get("Twitch-Eventsub-Message-Timestamp")
	mac := hmac.New(sha256.New, []byte(m.hooks.secret))
	mac.Write([]byte(id + at))
	mac.Write(b)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	sent, err := time.Parse(time.RFC3339Nano, at)
	if !hmac.Equal([]byte(want), []byte(r.Header.Get("Twitch-Eventsub-Message-Signature"))) || err != nil || m.now().Sub(sent).Abs() > replay {
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}
	var msg struct {
		Challenge string `json:"challenge"`
		Event     struct {
			Login string `json:"broadcaster_user_login"`
		} `json:"event"`
		Subscription struct {
			Status string `json:"status"`
		} `json:"subscription"`
	}
	if err := json.Unmarshal(b, &msg); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	switch r.Header.Get("Twitch-Eventsub-Message-Type") {
	case "webhook_callback_verification":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, msg.Challenge)
		return
	case "notification":
		m.refresh("twitch", strings.ToLower(msg.Event.Login))
	case "revocation":
		m.log.Warn("notify: twitch revoked a push", "status", msg.Subscription.Status)
	}
	w.WriteHeader(http.StatusNoContent)
}

// kickPush is kick's webhook, signed with kick's own key over the message
// ID, its timestamp and the body.
func (m *Module) kickPush(w http.ResponseWriter, r *http.Request) {
	b, ok := body(w, r)
	if !ok {
		return
	}
	k, ok := m.sources["kick"].(*kick)
	if !ok {
		http.NotFound(w, r)
		return
	}
	signed := r.Header.Get("Kick-Event-Message-Id") + "." + r.Header.Get("Kick-Event-Message-Timestamp") + "." + string(b)
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("Kick-Event-Signature"))
	if err == nil {
		err = k.verify(r.Context(), []byte(signed), sig)
	}
	if err != nil {
		m.log.Warn("notify: refused a kick push", "err", err)
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}
	if r.Header.Get("Kick-Event-Type") == "livestream.status.updated" {
		var ev struct {
			Broadcaster struct {
				Slug string `json:"channel_slug"`
			} `json:"broadcaster"`
		}
		// Started or ended: either way the card changes, live or to its VOD.
		if json.Unmarshal(b, &ev) == nil {
			m.log.Info("notify: kick pushed", "account", ev.Broadcaster.Slug)
			m.refresh("kick", strings.ToLower(ev.Broadcaster.Slug))
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// youtubeVerify answers the hub's check that skua asked for a channel: it
// did if that channel is followed, or if it is being let go. An unsubscribe
// is confirmed for any topic, so a third party could drop a lease; the
// daily resubscribe restores it and the timer covers the gap, so that is a
// late card at worst.
func (m *Module) youtubeVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	topic, _ := url.Parse(q.Get("hub.topic"))
	followed := topic != nil && slices.Contains(m.accounts("youtube"), topic.Query().Get("channel_id"))
	if q.Get("hub.mode") == "unsubscribe" || (q.Get("hub.mode") == "subscribe" && followed) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, q.Get("hub.challenge"))
		return
	}
	http.NotFound(w, r)
}

// youtubePush is the hub passing on a channel's feed when a video is
// published or changed. Unsigned or wrongly signed, it is dropped with a
// 2xx, as WebSub asks, so the hub doesn't retry it.
func (m *Module) youtubePush(w http.ResponseWriter, r *http.Request) {
	b, ok := body(w, r)
	if !ok {
		return
	}
	mac := hmac.New(sha1.New, []byte(m.hooks.secret))
	mac.Write(b)
	if hmac.Equal([]byte("sha1="+hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-Hub-Signature"))) {
		var feed struct {
			Entries []struct {
				Channel string `xml:"channelId"`
			} `xml:"entry"`
		}
		if xml.Unmarshal(b, &feed) == nil && len(feed.Entries) > 0 {
			m.refresh("youtube", feed.Entries[0].Channel)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// subscribe asks the hub for each channel's feed, for ten days at a time;
// the daily resubscribe renews it. A channel nobody follows any more is
// asked to stop.
func (y *youtube) subscribe(ctx context.Context, ids []string, h hooks) error {
	var errs []error
	y.mu.Lock()
	gone := slices.DeleteFunc(slices.Clone(y.subbed), func(id string) bool { return slices.Contains(ids, id) })
	y.mu.Unlock()
	for mode, list := range map[string][]string{"subscribe": ids, "unsubscribe": gone} {
		for _, id := range list {
			form := url.Values{
				"hub.callback": {h.url + "/youtube"}, "hub.mode": {mode}, "hub.verify": {"async"},
				"hub.topic":         {"https://www.youtube.com/xml/feeds/videos.xml?channel_id=" + id},
				"hub.secret":        {h.secret},
				"hub.lease_seconds": {strconv.Itoa(10 * 24 * 3600)},
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, y.hub, strings.NewReader(form.Encode()))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if _, err := do(y.c, req, nil); err != nil {
				errs = append(errs, fmt.Errorf("%s %s: %w", mode, id, err))
			}
		}
	}
	y.mu.Lock()
	y.subbed = slices.Clone(ids)
	y.mu.Unlock()
	return errors.Join(errs...)
}

// reconcile is youtube's usual check: a stream starting is never pushed,
// so its /live page is still read every few minutes.
func (y *youtube) reconcile() time.Duration { return y.every() }

// twitchEvents are what skua takes from twitch: a stream starting, for its
// card, and ending, to turn that card into its VOD.
var twitchEvents = []string{"stream.online", "stream.offline"}

// subscribe keeps one subscription per followed login for each of
// twitchEvents, all pointing at skua's hook.
func (t *twitch) subscribe(ctx context.Context, logins []string, h hooks) error {
	want := map[string]bool{} // broadcaster id
	for _, b := range batches(logins, 100) {
		if len(b) == 0 {
			continue
		}
		var res struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := t.app.call(ctx, t.api+"/users?"+url.Values{"login": b}.Encode(), t.header(), &res); err != nil {
			return err
		}
		for _, u := range res.Data {
			want[u.ID] = true
		}
	}
	var errs []error
	for _, typ := range twitchEvents {
		if err := t.keepSubs(ctx, typ, want, h); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// keepSubs makes twitch's typ subscriptions exactly one working one per
// broadcaster in want: it drops the rest, and adds what's missing.
func (t *twitch) keepSubs(ctx context.Context, typ string, want map[string]bool, h hooks) error {
	type sub struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Condition struct {
			Broadcaster string `json:"broadcaster_user_id"`
		} `json:"condition"`
		Transport struct {
			Callback string `json:"callback"`
		} `json:"transport"`
	}
	have := map[string]bool{}
	var errs []error
	for after := ""; ; {
		var page struct {
			Data       []sub `json:"data"`
			Pagination struct {
				Cursor string `json:"cursor"`
			} `json:"pagination"`
		}
		q := url.Values{"type": {typ}}
		if after != "" {
			q.Set("after", after)
		}
		if err := t.app.call(ctx, t.api+"/eventsub/subscriptions?"+q.Encode(), t.header(), &page); err != nil {
			return err
		}
		for _, s := range page.Data {
			ok := s.Transport.Callback == h.url+"/twitch" && want[s.Condition.Broadcaster] &&
				(s.Status == "enabled" || s.Status == "webhook_callback_verification_pending")
			if ok && !have[s.Condition.Broadcaster] {
				have[s.Condition.Broadcaster] = true
				continue
			}
			if err := t.app.send(ctx, http.MethodDelete, t.api+"/eventsub/subscriptions?id="+url.QueryEscape(s.ID), t.header(), nil, nil); err != nil {
				errs = append(errs, err)
			}
		}
		if after = page.Pagination.Cursor; after == "" {
			break
		}
	}
	for id := range want {
		if have[id] {
			continue
		}
		body := map[string]any{
			"type": typ, "version": "1",
			"condition": map[string]string{"broadcaster_user_id": id},
			"transport": map[string]string{"method": "webhook", "callback": h.url + "/twitch", "secret": h.secret},
		}
		if err := t.app.send(ctx, http.MethodPost, t.api+"/eventsub/subscriptions", t.header(), body, nil); err != nil {
			errs = append(errs, fmt.Errorf("subscribing %s to %s: %w", id, typ, err))
		}
	}
	return errors.Join(errs...)
}

func (t *twitch) reconcile() time.Duration { return 15 * time.Minute }

// kickEvent is the one event skua takes from kick.
const kickEvent = "livestream.status.updated"

// subscribe keeps one livestream.status.updated subscription per followed
// channel. Kick sends them to the webhook URL in the app's settings, which
// has to be SKUA_HOOKS_URL/kick.
func (k *kick) subscribe(ctx context.Context, slugs []string, _ hooks) error {
	want := map[int64]bool{}
	for _, b := range batches(slugs, 50) {
		if len(b) == 0 {
			continue
		}
		cs, err := k.channels(ctx, b)
		if err != nil {
			return err
		}
		for _, c := range cs {
			want[c.ID] = true
		}
	}
	var list struct {
		Data []struct {
			ID          string `json:"id"`
			Event       string `json:"event"`
			Broadcaster int64  `json:"broadcaster_user_id"`
		} `json:"data"`
	}
	if err := k.app.call(ctx, k.api+"/events/subscriptions", nil, &list); err != nil {
		return err
	}
	have := map[int64]bool{}
	var drop []string
	for _, s := range list.Data {
		if s.Event != kickEvent {
			continue
		}
		if want[s.Broadcaster] && !have[s.Broadcaster] {
			have[s.Broadcaster] = true
			continue
		}
		drop = append(drop, s.ID)
	}
	var errs []error
	if len(drop) > 0 {
		if err := k.app.send(ctx, http.MethodDelete, k.api+"/events/subscriptions?"+url.Values{"id": drop}.Encode(), nil, nil, nil); err != nil {
			errs = append(errs, err)
		}
	}
	for id := range want {
		if have[id] {
			continue
		}
		body := map[string]any{
			"broadcaster_user_id": id, "method": "webhook",
			"events": []map[string]any{{"name": kickEvent, "version": 1}},
		}
		if err := k.app.send(ctx, http.MethodPost, k.api+"/events/subscriptions", nil, body, nil); err != nil {
			errs = append(errs, fmt.Errorf("subscribing %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// reconcile is kick's usual minute: one call covers fifty channels, and a
// push that never comes (the webhook URL unset in kick's app settings, or a
// subscription kick dropped) would otherwise hold a card for 15 minutes.
func (k *kick) reconcile() time.Duration { return k.every() }

// kickKey is kick's signing key, fetched on first use and again when a
// signature fails against it, since kick may rotate it: at most once a
// minute, so forged pushes can't make skua hammer kick for its key.
type kickKey struct {
	mu      sync.Mutex
	key     *rsa.PublicKey
	fetched time.Time
}

var errStaleKey = errors.New("notify: kick's signature doesn't match its key")

// verify checks sig over signed with kick's key, fetching it again once if
// the one held doesn't match.
func (k *kick) verify(ctx context.Context, signed, sig []byte) error {
	sum := sha256.Sum256(signed)
	k.key.mu.Lock()
	defer k.key.mu.Unlock()
	if k.key.key != nil && rsa.VerifyPKCS1v15(k.key.key, crypto.SHA256, sum[:], sig) == nil {
		return nil
	}
	if time.Since(k.key.fetched) < time.Minute {
		return errStaleKey
	}
	k.key.fetched = time.Now()
	var res struct {
		Data struct {
			Key string `json:"public_key"`
		} `json:"data"`
	}
	if _, err := get(ctx, k.app.c, k.api+"/public-key", nil, &res); err != nil {
		return err
	}
	block, _ := pem.Decode([]byte(res.Data.Key))
	if block == nil {
		return errors.New("notify: kick's public key isn't PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return err
	}
	key, ok := pub.(*rsa.PublicKey)
	if !ok {
		return errors.New("notify: kick's public key isn't RSA")
	}
	k.key.key = key
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig)
}
