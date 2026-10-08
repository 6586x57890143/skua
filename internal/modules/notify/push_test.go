package notify

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // WebSub's signature
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

// secret is the test hook secret: long enough, and plainly not a real one.
var secret = strings.Repeat("test-hook-secret", 2)

// safePoster is a poster pushes can post to from their own goroutines.
type safePoster struct {
	mu   sync.Mutex
	sent []snowflake.ID
}

func (p *safePoster) CreateMessage(channel snowflake.ID, _ discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, channel)
	return &discord.Message{}, nil
}

func (p *safePoster) UpdateMessage(snowflake.ID, snowflake.ID, discord.MessageUpdate, ...rest.RequestOpt) (*discord.Message, error) {
	return &discord.Message{}, nil
}

func (p *safePoster) GetRoles(snowflake.ID, ...rest.RequestOpt) ([]discord.Role, error) {
	return nil, nil
}

func (p *safePoster) GetMember(snowflake.ID, snowflake.ID, ...rest.RequestOpt) (*discord.Member, error) {
	return &discord.Member{}, nil
}

func (p *safePoster) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

// pushed is a module with push on, platform served by src, one follow of
// account into channel 10 and a baseline already taken, so the next new
// thing src shows is a card.
func pushed(t *testing.T, platform, account string, src source) (*Module, *safePoster) {
	t.Helper()
	old := refreshAt
	refreshAt = []time.Duration{0}
	t.Cleanup(func() { refreshAt = old })
	m, err := New(context.Background(), quiet(), guard.New(), nil, Config{HooksURL: "https://hooks", HooksSecret: secret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.sources = map[string]source{platform: src}
	m.turns = map[string]*sync.Mutex{platform: {}}
	m.follows = []follow{{guild: 1, channel: 10, platform: platform, account: account, name: account}}
	m.seen[key{platform, account}] = []string{}
	p := &safePoster{}
	m.poster = p
	return m, p
}

func waitFor(t *testing.T, p *safePoster, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for p.count() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p.count() != n {
		t.Fatalf("%d cards, want %d", p.count(), n)
	}
}

func twitchReq(t *testing.T, kind, body string, at time.Time, key string) *http.Request {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339Nano)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("msg1" + ts + body))
	r := httptest.NewRequest(http.MethodPost, "/twitch", strings.NewReader(body))
	r.Header.Set("Twitch-Eventsub-Message-Id", "msg1")
	r.Header.Set("Twitch-Eventsub-Message-Timestamp", ts)
	r.Header.Set("Twitch-Eventsub-Message-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	r.Header.Set("Twitch-Eventsub-Message-Type", kind)
	return r
}

func TestTwitchPush(t *testing.T) {
	src := newFake()
	m, p := pushed(t, "twitch", "bird", src)
	h := m.Hooks()
	now := time.Now()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, twitchReq(t, "webhook_callback_verification", `{"challenge":"pong"}`, now, secret))
	if w.Code != 200 || w.Body.String() != "pong" {
		t.Fatalf("challenge: %d %q", w.Code, w.Body)
	}
	for name, r := range map[string]*http.Request{
		"forged": twitchReq(t, "notification", `{}`, now, "wrong"),
		"stale":  twitchReq(t, "notification", `{}`, now.Add(-time.Hour), secret),
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, twitchReq(t, "notification", `not json`, now, secret))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, twitchReq(t, "revocation", `{"subscription":{"status":"authorization_revoked"}}`, now, secret))
	if w.Code != http.StatusNoContent {
		t.Fatalf("revocation: %d", w.Code)
	}

	src.show("bird", item{ID: "live:1", URL: "https://t/bird"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, twitchReq(t, "notification", `{"event":{"broadcaster_user_login":"Bird"}}`, now, secret))
	if w.Code != http.StatusNoContent {
		t.Fatalf("notification: %d", w.Code)
	}
	waitFor(t, p, 1)

	// A push for someone nobody follows here checks nothing.
	m.refresh("twitch", "stranger")
	m.refresh("nowhere", "bird")
}

func TestPushBeforeSkuaIsUpWaits(t *testing.T) {
	src := newFake()
	m, p := pushed(t, "twitch", "bird", src)
	m.mu.Lock()
	m.poster = nil
	m.mu.Unlock()
	src.show("bird", item{ID: "live:1"})
	m.refresh("twitch", "bird")
	time.Sleep(50 * time.Millisecond)
	if p.count() != 0 {
		t.Fatal("a card before skua is up")
	}
	src.err = errors.New("down")
	src.mu.Lock()
	delete(src.shows, "bird")
	src.mu.Unlock()
	m.mu.Lock()
	m.poster = p
	m.mu.Unlock()
	m.refresh("twitch", "bird") // logs, posts nothing
	time.Sleep(50 * time.Millisecond)
	if p.count() != 0 {
		t.Fatal("a card from a failed check")
	}
}

func TestKickPush(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	var keyFetches int
	var serveKey = pemKey
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/public-key":
			keyFetches++
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"public_key": serveKey}})
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
		case "/channels":
			_, _ = w.Write([]byte(`{"data":[{"slug":"bird","stream":{"is_live":true,"start_time":"s1"}}]}`))
		}
	}))
	defer srv.Close()
	k := &kick{app: &app{c: srv.Client(), url: srv.URL + "/token", id: "id", secret: "s"}, api: srv.URL}
	m, p := pushed(t, "kick", "bird", k)
	h := m.Hooks()

	send := func(body string, sign func([]byte) []byte) int {
		signed := []byte("id1.2026-10-07T12:00:00Z." + body)
		r := httptest.NewRequest(http.MethodPost, "/kick", strings.NewReader(body))
		r.Header.Set("Kick-Event-Message-Id", "id1")
		r.Header.Set("Kick-Event-Message-Timestamp", "2026-10-07T12:00:00Z")
		r.Header.Set("Kick-Event-Signature", base64.StdEncoding.EncodeToString(sign(signed)))
		r.Header.Set("Kick-Event-Type", "livestream.status.updated")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	good := func(b []byte) []byte {
		sum := sha256.Sum256(b)
		sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
		return sig
	}
	if code := send(`{"is_live":true,"broadcaster":{"channel_slug":"Bird"}}`, good); code != http.StatusNoContent {
		t.Fatal(code)
	}
	waitFor(t, p, 1)
	if code := send(`{"is_live":false,"broadcaster":{"channel_slug":"bird"}}`, good); code != http.StatusNoContent || keyFetches != 1 {
		t.Fatalf("a second push reuses the key: %d, %d fetches", code, keyFetches)
	}
	// A forged signature fetches the key again, in case kick rotated it,
	// but at most once a minute.
	forged := func([]byte) []byte { return []byte("forged") }
	for range 3 {
		if code := send(`{}`, forged); code != http.StatusForbidden {
			t.Fatal(code)
		}
	}
	if keyFetches != 1 {
		t.Fatalf("within a minute: %d key fetches", keyFetches)
	}
	k.key.fetched = time.Now().Add(-2 * time.Minute)
	for range 3 {
		send(`{}`, forged)
	}
	if keyFetches != 2 {
		t.Fatalf("after a minute: %d key fetches", keyFetches)
	}

	// A key that isn't one is refused.
	for _, bad := range []string{"not pem", string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("junk")}))} {
		serveKey = bad
		k.key = kickKey{}
		if err := k.verify(context.Background(), []byte("x"), []byte("y")); err == nil {
			t.Errorf("verified against %q", bad)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/kick", strings.NewReader("{}"))
	r.Header.Set("Kick-Event-Signature", "%%%")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}

	// Without kick set up, /kick is not there.
	m.sources = map[string]source{}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/kick", strings.NewReader("{}")))
	if w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}
}

func TestYoutubePush(t *testing.T) {
	src := newFake()
	m, p := pushed(t, "youtube", ytID, src)
	h := m.Hooks()

	verify := func(mode, channel string) (int, string) {
		q := url.Values{"hub.mode": {mode}, "hub.challenge": {"c1"}, "hub.topic": {"https://www.youtube.com/xml/feeds/videos.xml?channel_id=" + channel}}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/youtube?"+q.Encode(), nil))
		return w.Code, w.Body.String()
	}
	if code, body := verify("subscribe", ytID); code != 200 || body != "c1" {
		t.Fatalf("subscribe: %d %q", code, body)
	}
	if code, _ := verify("subscribe", "UCnobody"); code != http.StatusNotFound {
		t.Fatalf("a channel nobody follows: %d", code)
	}
	if code, body := verify("unsubscribe", "UCnobody"); code != 200 || body != "c1" {
		t.Fatalf("unsubscribe: %d %q", code, body)
	}

	push := func(body, key string) int {
		mac := hmac.New(sha1.New, []byte(key))
		mac.Write([]byte(body))
		r := httptest.NewRequest(http.MethodPost, "/youtube", strings.NewReader(body))
		r.Header.Set("X-Hub-Signature", "sha1="+hex.EncodeToString(mac.Sum(nil)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	src.show(ytID, item{ID: "v1", URL: "https://y/v1"})
	feed := `<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015"><entry><yt:videoId>v1</yt:videoId><yt:channelId>` + ytID + `</yt:channelId></entry></feed>`
	if code := push(feed, "wrong"); code != http.StatusNoContent {
		t.Fatal(code)
	}
	time.Sleep(50 * time.Millisecond)
	if p.count() != 0 {
		t.Fatal("a forged push made a card")
	}
	if code := push(feed, secret); code != http.StatusNoContent {
		t.Fatal(code)
	}
	waitFor(t, p, 1)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/youtube", io.LimitReader(strings.NewReader(strings.Repeat("a", maxPush+10)), maxPush+10)))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big: %d", w.Code)
	}
}

func TestSubscribeYoutube(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		got = append(got, r.Form.Get("hub.mode")+" "+r.Form.Get("hub.topic")+" "+r.Form.Get("hub.callback")+" "+r.Form.Get("hub.secret"))
		mu.Unlock()
		if strings.Contains(r.Form.Get("hub.topic"), "UCbad") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	y := &youtube{c: srv.Client(), hub: srv.URL}
	h := hooks{url: "https://hooks", secret: secret}
	if err := y.subscribe(context.Background(), []string{"UCa", "UCb"}, h); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "subscribe https://www.youtube.com/xml/feeds/videos.xml?channel_id=UC") || !strings.HasSuffix(got[0], " https://hooks/youtube "+secret) {
		t.Fatalf("%q", got)
	}
	got = nil
	if err := y.subscribe(context.Background(), []string{"UCa", "UCbad"}, h); err == nil || !strings.Contains(err.Error(), "UCbad") {
		t.Fatalf("a refused channel is named: %v", err)
	}
	if !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "unsubscribe") && strings.Contains(s, "UCb ") }) {
		t.Fatalf("a channel no longer followed is let go: %q", got)
	}
	if y.reconcile() != y.every() {
		t.Fatal("youtube keeps its pace: streams aren't pushed")
	}
}

func TestSubscribeTwitch(t *testing.T) {
	var tokens atomic.Int32
	var mu sync.Mutex
	var calls []string
	srv := fakeAPI(t, &tokens, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		switch {
		case r.URL.Path == "/users":
			_, _ = w.Write([]byte(`{"data":[{"id":"1"},{"id":"2"},{"id":"3"}]}`))
		case r.Method == http.MethodGet && r.URL.Query().Get("after") == "":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"s1","status":"enabled","condition":{"broadcaster_user_id":"1"},"transport":{"callback":"https://hooks/twitch"}},
				{"id":"s2","status":"enabled","condition":{"broadcaster_user_id":"9"},"transport":{"callback":"https://hooks/twitch"}}
			],"pagination":{"cursor":"p2"}}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[
				{"id":"s3","status":"notification_failures_exceeded","condition":{"broadcaster_user_id":"2"},"transport":{"callback":"https://hooks/twitch"}},
				{"id":"s4","status":"enabled","condition":{"broadcaster_user_id":"1"},"transport":{"callback":"https://hooks/twitch"}}
			],"pagination":{}}`))
		case r.Method == http.MethodPost:
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["transport"].(map[string]any)["secret"] != secret || (b["type"] != "stream.online" && b["type"] != "stream.offline") {
				t.Errorf("subscription %v", b)
			}
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	tw := &twitch{app: &app{c: srv.Client(), url: srv.URL + "/token", id: "id", secret: "s"}, api: srv.URL}
	if err := tw.subscribe(context.Background(), []string{"a", "b", "c"}, hooks{url: "https://hooks", secret: secret}); err != nil {
		t.Fatal(err)
	}
	var deletes, posts int
	for _, c := range calls {
		switch {
		case strings.HasPrefix(c, "DELETE"):
			deletes++
		case strings.HasPrefix(c, "POST"):
			posts++
		}
	}
	// For each of online and offline: s2 is someone unfollowed, s3 failed
	// and s4 doubles s1, so three go; 2 and 3 have none that works, so two
	// come.
	if deletes != 6 || posts != 4 {
		t.Fatalf("%d deletes, %d posts: %q", deletes, posts, calls)
	}
	if tw.reconcile() != 15*time.Minute {
		t.Fatal("reconcile")
	}
	if err := tw.subscribe(context.Background(), nil, hooks{url: "https://hooks", secret: secret}); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribeKick(t *testing.T) {
	var tokens atomic.Int32
	var mu sync.Mutex
	var calls []string
	srv := fakeAPI(t, &tokens, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		switch {
		case r.URL.Path == "/channels":
			_, _ = w.Write([]byte(`{"data":[{"broadcaster_user_id":1,"slug":"a"},{"broadcaster_user_id":2,"slug":"b"}]}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[
				{"id":"k1","event":"livestream.status.updated","broadcaster_user_id":1},
				{"id":"k2","event":"livestream.status.updated","broadcaster_user_id":7},
				{"id":"k3","event":"chat.message.sent","broadcaster_user_id":1}]}`))
		case r.Method == http.MethodPost:
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["broadcaster_user_id"] != float64(2) || b["method"] != "webhook" {
				t.Errorf("subscription %v", b)
			}
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodDelete:
			if r.URL.Query()["id"][0] != "k2" {
				t.Errorf("deleted %v", r.URL.Query())
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	k := &kick{app: &app{c: srv.Client(), url: srv.URL + "/token", id: "id", secret: "s"}, api: srv.URL}
	if err := k.subscribe(context.Background(), []string{"a", "b"}, hooks{}); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "DELETE") }) || !slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "POST") }) {
		t.Fatalf("%q", calls)
	}
	if k.reconcile() != k.every() {
		t.Fatal("reconcile")
	}
}

// pushFake is a fake platform that can push.
type pushFake struct {
	*fake
	mu   sync.Mutex
	subs [][]string
	err  error
}

func (p *pushFake) subscribe(_ context.Context, accounts []string, _ hooks) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subs = append(p.subs, accounts)
	return p.err
}

func (p *pushFake) reconcile() time.Duration { return 15 * time.Minute }

func (p *pushFake) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.subs)
}

func TestPushWiring(t *testing.T) {
	pf := &pushFake{fake: newFake()}
	m, _ := pushed(t, "fake", "bird", pf)
	if m.every(pf) != 15*time.Minute || m.every(newFake()) != time.Hour {
		t.Fatal("with push on, a pusher slows to its safety net")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.start(ctx, &safePoster{})
	deadline := time.Now().Add(5 * time.Second)
	for pf.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if pf.calls() == 0 {
		t.Fatal("start subscribes")
	}
	pf.err = errors.New("refused")
	m.subscribe("fake") // logs
	m.hooks = hooks{}
	m.subscribe("fake")
	if pf.calls() != 2 {
		t.Fatalf("push off subscribes nothing: %d", pf.calls())
	}
}

// A bad optional setting never stops skua: New boots, logs one error
// saying what to fix, and runs without what the setting was for.
func TestBadSettingsNeverStopSkua(t *testing.T) {
	good := Config{HooksURL: "https://hooks.skua.lol", HooksSecret: secret, TwitchID: "a", TwitchSecret: "b", KickID: "c", KickSecret: "d"}
	for name, c := range map[string]struct {
		edit         func(*Config)
		push         bool
		twitch, kick bool
		errors       int
		mentions     string
	}{
		"all good":          {func(*Config) {}, true, true, true, 0, ""},
		"no push at all":    {func(c *Config) { c.HooksURL, c.HooksSecret = "", "" }, false, true, true, 0, ""},
		"url, no secret":    {func(c *Config) { c.HooksSecret = "" }, false, true, true, 1, "SKUA_HOOKS_SECRET"},
		"url, short secret": {func(c *Config) { c.HooksSecret = "short" }, false, true, true, 1, "SKUA_HOOKS_SECRET"},
		"url not https":     {func(c *Config) { c.HooksURL = "http://hooks.skua.lol" }, false, true, true, 1, "SKUA_HOOKS_URL"},
		"url not a url":     {func(c *Config) { c.HooksURL = "::nope" }, false, true, true, 1, "SKUA_HOOKS_URL"},
		"twitch id only":    {func(c *Config) { c.TwitchSecret = "" }, true, false, true, 1, "SKUA_TWITCH_CLIENT_SECRET"},
		"kick secret only":  {func(c *Config) { c.KickID = "" }, true, true, false, 1, "SKUA_KICK_CLIENT_ID"},
	} {
		cfg := good
		c.edit(&cfg)
		var logs strings.Builder
		log := slog.New(slog.NewTextHandler(&logs, nil))
		m, err := New(context.Background(), log, guard.New(), nil, cfg, nil)
		if err != nil {
			t.Fatalf("%s: New failed: %v", name, err)
		}
		_, twitch := m.sources["twitch"]
		_, kick := m.sources["kick"]
		if m.Pushing() != c.push || twitch != c.twitch || kick != c.kick {
			t.Errorf("%s: push %v twitch %v kick %v", name, m.Pushing(), twitch, kick)
		}
		if n := strings.Count(logs.String(), "level=ERROR"); n != c.errors || !strings.Contains(logs.String(), c.mentions) {
			t.Errorf("%s: %d errors, want %d naming %q: %s", name, n, c.errors, c.mentions, logs.String())
		}
	}
}
