// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/nbd-wtf/go-nostr"

	"github.com/6586x57890143/skua/internal/concord"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/store"
)

func TestToArmada(t *testing.T) {
	got := toArmada("<@!1> and <@2> say <a:wave:9>", 7, map[string]string{"1": "kit"}, []string{"https://a/x.png", "https://a/y"})
	if got != "@kit and <@2> say :wave:\nhttps://a/x.png\nhttps://a/y" {
		t.Errorf("%q", got)
	}
	if got := toArmada("", 7, nil, []string{"https://a/x.png"}); got != "https://a/x.png" {
		t.Errorf("attachment only: %q", got)
	}
	if got := toArmada(strings.Repeat("é", armadaMaxChars+5), 7, nil, nil); len([]rune(got)) != armadaMaxChars || !strings.HasSuffix(got, "...") {
		t.Error("not cut to the cap")
	}
	if tags := emojiTags("<:a:1> <a:b:2> <:a:1>"); len(tags) != 2 || tags[1][2] != "https://cdn.discordapp.com/emojis/2.gif" {
		t.Errorf("%v", tags)
	}
}

func TestToDiscord(t *testing.T) {
	tags := [][]string{
		{"emoji", "blob", "https://cdn.discordapp.com/emojis/123.png"},
		{"emoji", "party", "https://cdn.discordapp.com/emojis/9.gif"},
		{"emoji", "evil", "https://evil.example/?x=cdn.discordapp.com/emojis/1."},
		{"emoji", "x", "https://cdn.discordapp.com/emojis/1.png"},
		{"emoji", "plain", "http://cdn.discordapp.com/emojis/1.png"},
		{"emoji"},
	}
	got := toDiscord("nostr:npub1qqqqqqqqqqqqqqqqqqqq :blob: :party: :evil: :x: :plain: nostr:nevent1abc", tags)
	want := "@npub1qqqqqqqq... <:blob:123> <a:party:9> :evil: :x: :plain: nostr:nevent1abc"
	if got != want {
		t.Errorf("%q\nwant %q", got, want)
	}
	if _, _, ok := discordEmoji("%%"); ok {
		t.Error("parsed garbage")
	}
}

func TestSplitAndCap(t *testing.T) {
	if p := split("short"); len(p) != 1 {
		t.Fatal(p)
	}
	lines := strings.Repeat(strings.Repeat("a", 1500)+"\n", 3)
	if p := split(lines); len(p) != 3 || p[0] != strings.Repeat("a", 1500) {
		t.Errorf("by line: %d", len(p))
	}
	if p := split(strings.Repeat("b", 4500)); len(p) != 3 || len(p[0]) != discordMax {
		t.Errorf("hard: %d", len(p))
	}
	parts := capParts(split(strings.Repeat("c", discordMax*12)))
	if len(parts) != maxParts || !strings.HasSuffix(parts[maxParts-1], "2 more parts not bridged") || len([]rune(parts[maxParts-1])) > discordMax {
		t.Errorf("cap: %d %q", len(parts), parts[maxParts-1][discordMax-40:])
	}
	if parts := capParts(split(strings.Repeat("c", discordMax*11))); !strings.HasSuffix(parts[maxParts-1], "1 more part not bridged") {
		t.Error("singular")
	}
}

func TestEscapeAndStrip(t *testing.T) {
	if got := escape("[#general](https://x)"); got != `\[\#general\]\(https://x\)` {
		t.Errorf("%q", got)
	}
	if got := escape(strings.Repeat("n", 90)); len([]rune(got)) != maxNameChars+3 {
		t.Error("not cut")
	}
	if got := stripURLs("see https://a/x  \n\n\n\nhttps://a/y", []string{"https://a/x", "https://a/y"}); got != "see" {
		t.Errorf("%q", got)
	}
	if got := stripURLs(" keep ", nil); got != " keep " {
		t.Errorf("%q", got)
	}
}

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "::ffff:7f00:1": false, "10.1.2.3": false, "192.168.1.1": false, "172.16.0.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false, "fd00::1": false,
		"fe80::1": false, "64:ff9b::7f00:1": false, "2002:7f00:1::": false, "224.0.0.1": false, "240.0.0.1": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			_, _ = w.Write(make([]byte, 100))
		case "/file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/gone":
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Type", "image/png; charset=x")
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	// The guard refuses a server on loopback, however it was reached.
	if _, _, err := fetch(ctx, guarded, srv.URL+"/ok", 10); !errors.Is(err, errBlocked) {
		t.Fatalf("loopback: %v", err)
	}
	if _, _, err := fetch(ctx, guarded, "file:///etc/passwd", 10); err == nil {
		t.Fatal("file scheme")
	}
	plain := &http.Client{CheckRedirect: guarded.CheckRedirect}
	if b, typ, err := fetch(ctx, plain, srv.URL+"/ok", 10); err != nil || string(b) != "ok" || typ != "image/png" {
		t.Fatalf("%q %q %v", b, typ, err)
	}
	if _, _, err := fetch(ctx, plain, srv.URL+"/big", 10); !errors.Is(err, errTooBig) {
		t.Errorf("big: %v", err)
	}
	if _, _, err := fetch(ctx, plain, srv.URL+"/file", 10); err == nil {
		t.Error("redirect to file")
	}
	if _, _, err := fetch(ctx, plain, srv.URL+"/gone", 10); err == nil {
		t.Error("404")
	}
	if _, _, err := fetch(ctx, plain, "http://[::1", 10); err == nil {
		t.Error("bad url")
	}
}

func TestMedia(t *testing.T) {
	if keyMaterial(base64.StdEncoding.EncodeToString([]byte{0xab, 0xcd})) != "abcd" || keyMaterial("AbCd") != "abcd" ||
		keyMaterial(base64.RawURLEncoding.EncodeToString([]byte{0xfb, 0xff})) != "fbff" || keyMaterial("!!") != "!!" {
		t.Error("key material")
	}
	if a, _, _, _ := (imeta{"encryption-algorithm": "xchacha"}).sealedWith(); a != "bad" {
		t.Error("an unknown scheme is not refused")
	}
	if _, err := decrypt([]byte("short"), strings.Repeat("00", 32), strings.Repeat("00", 12), ""); err == nil {
		t.Error("decrypted garbage")
	}
	if _, err := decrypt(nil, "00", "00", ""); err == nil {
		t.Error("short key")
	}
	for mime, ext := range map[string]string{"image/png": "png", "application/x-thing+json": "", "text/plain; charset=utf-8": "plain", "nonsense": ""} {
		if got := extFor(mime); got != ext {
			t.Errorf("%s: %q", mime, got)
		}
	}
	if mimeFromURL("https://a/b/c.JPG?x=1") != "image/jpeg" || mimeFromURL("https://a/b") != "" || mimeFromURL("%%") != "" {
		t.Error("mime from url")
	}
	if withExt("https://b.example/abc", "image/png") != "https://b.example/abc.png" || withExt("https://b.example/abc.gif", "image/png") != "https://b.example/abc.gif" ||
		withExt("https://b.example/", "image/png") != "https://b.example/" || withExt("https://b.example/abc", "nonsense") != "https://b.example/abc" {
		t.Error("with ext")
	}
	for in, want := range map[[3]string]string{
		{"https://a/x/../../etc/passwd", "", ""}:        "passwd.bin",
		{"https://a/abc", "image/png", ""}:              "abc.png",
		{"https://a/", "", "..hidden"}:                  "hidden.bin",
		{"https://a/", "", "<script>.js"}:               "_script_.js",
		{"https://a/", "", strings.Repeat("n", 200)}:    strings.Repeat("n", 92) + ".bin",
		{"https://a/", "image/gif", "  "}:               "file.gif",
		{"https://a/x.tar.gz", "application/gzip", ""}:  "x.tar.gz",
		{"https://a/x", "application/octet-stream", ""}: "x.bin",
	} {
		if got := filename(in[0], in[1], in[2]); got != want {
			t.Errorf("%v: %q", in, got)
		}
	}
}

func TestBlossom(t *testing.T) {
	var auth nostr.Event
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer refuse.Close()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(r.Header.Get("Authorization"), "Nostr "))
		_ = json.Unmarshal(raw, &auth)
		_, _ = io.ReadAll(r.Body)
		if r.Header.Get("Content-Type") == "image/gif" {
			_, _ = w.Write([]byte(`{"url":"javascript:alert(1)"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"url":"https://cdn.example/%s"}`, strings.Repeat("ab", 32))
	}))
	defer ok.Close()
	b := &blossom{servers: []string{refuse.URL, ok.URL}, http: http.DefaultClient}
	k := key(t, 7)
	u, hash, err := b.upload(context.Background(), []byte("data"), "image/png", k.SK)
	if err != nil || u != "https://cdn.example/"+strings.Repeat("ab", 32)+".png" || len(hash) != 64 {
		t.Fatalf("%q %q %v", u, hash, err)
	}
	if auth.PubKey != k.PK || auth.Kind != 24242 || concord.Tag(tagsOf(auth.Tags), "x") != hash {
		t.Errorf("auth %+v", auth)
	}
	// A descriptor URL that isn't http(s) is not trusted: the hash URL goes.
	if u, _, _ := b.upload(context.Background(), []byte("gif"), "image/gif", k.SK); !strings.HasPrefix(u, ok.URL+"/") {
		t.Errorf("%q", u)
	}
	b.servers = []string{refuse.URL}
	if _, _, err := b.upload(context.Background(), []byte("x"), "image/png", k.SK); err == nil {
		t.Error("uploaded to a server that refused")
	}
}

func tagsOf(tags nostr.Tags) [][]string {
	out := make([][]string, len(tags))
	for i, t := range tags {
		out[i] = t
	}
	return out
}

func TestRehostAttachments(t *testing.T) {
	h := newHarness(t, "100="+general)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("png bytes"))
	}))
	defer cdn.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()
	h.m.blob = &blossom{servers: []string{up.URL}, http: http.DefaultClient}
	png := "image/png"
	urls, tags := h.m.rehost(context.Background(), []discord.Attachment{
		{URL: cdn.URL + "/a.png", Filename: "a.png", Size: 9},
		{URL: cdn.URL + "/b", Filename: "b", Size: 9, ContentType: &png},
		{URL: cdn.URL + "/huge", Size: maxFile + 1},
		{URL: "http://127.0.0.1:1/dead", Size: 1},
	}, key(t, 8))
	if len(urls) != 4 || len(tags) != 2 || !strings.HasPrefix(urls[0], up.URL+"/") || urls[2] != cdn.URL+"/huge" || urls[3] != "http://127.0.0.1:1/dead" {
		t.Fatalf("%v %v", urls, tags)
	}
	if !slices.Contains(tags[0], "m image/png") || !slices.Contains(tags[0], "name a.png") || !slices.Contains(tags[1], "m image/png") {
		t.Errorf("%v", tags)
	}
}

// A pasted Discord attachment link is dead outside Discord until it is
// signed, so it crosses refreshed, and as a file when Blossom is there.
func TestPastedDiscordLinks(t *testing.T) {
	h := newHarness(t, "100="+general)
	bare := "https://cdn.discordapp.com/attachments/1/2/bounce.gif"
	other := "https://media.discordapp.net/attachments/3/4/x.png"
	signed := bare + "?ex=1&is=2&hm=3&"
	msg := discord.Message{Content: "why doesn't this load " + bare + " " + bare + " " + other}

	// Discord refusing leaves the text as it was.
	if got, _ := h.m.compose(context.Background(), 7, msg, key(t, 8)); got != msg.Content {
		t.Errorf("refused %q", got)
	}

	// Each link is sent once; one Discord won't sign stays where it was.
	h.rest.refreshed = map[string]string{bare: signed, other: "https://evil.example/x.png"}
	got, tags := h.m.compose(context.Background(), 7, msg, key(t, 8))
	if got != "why doesn't this load   "+other+"\n"+signed || len(tags) != 0 {
		t.Errorf("no blossom %q %v", got, tags)
	}

	// With Blossom it goes across as a file.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write([]byte("gif bytes"))
	}))
	defer cdn.Close()
	h.m.blob = &blossom{servers: []string{up.URL}, http: http.DefaultClient}
	h.m.fetcher = &http.Client{Transport: redirectTo(cdn.URL)}
	got, tags = h.m.compose(context.Background(), 7, discord.Message{Content: bare}, key(t, 8))
	if !strings.HasPrefix(got, up.URL+"/") || !strings.HasSuffix(got, ".gif") || len(tags) != 1 || !slices.Contains(tags[0], "name bounce.gif") {
		t.Errorf("rehosted %q %v", got, tags)
	}
}

// One 403 from the refresh endpoint ends refreshing for the run, in every
// guild: a 401 or 403 is never retried, and each counts toward the invalid
// request ban on the IP. It leaves the guild's breaker alone.
func TestPastedLinksStopOnRefusal(t *testing.T) {
	h := newHarness(t, "100="+general)
	h.rest.fail = &rest.Error{Response: &http.Response{StatusCode: http.StatusForbidden}}
	msg := discord.Message{Content: "https://cdn.discordapp.com/attachments/1/2/a.gif"}
	for _, guild := range []snowflake.ID{7, 7, 8} {
		if got, _ := h.m.compose(context.Background(), guild, msg, key(t, 8)); got != msg.Content {
			t.Fatalf("refused %q", got)
		}
	}
	if h.rest.calls != 1 {
		t.Errorf("asked %d times after a 403", h.rest.calls)
	}
	if err := h.m.guard.Allow(7, guard.MessageSend); err != nil {
		t.Errorf("breaker: %v", err)
	}
}

// redirectTo sends every request to base, whatever host it names.
type redirectTo string

func (r redirectTo) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(string(r))
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestReadyRunsTheBridge(t *testing.T) {
	v := loadVectors(t)
	pool := &fakeRelays{stored: append([]*nostr.Event{v.Invite.Event}, v.Community.A.Wraps...)}
	m, err := New(slog.New(slog.DiscardHandler), guard.New(), &fakePoster{}, filter.Default(), nil, Config{
		Invite: v.Invite.URL, Master: v.Puppet.Master, Primary: strings.Repeat("0a", 32), Links: "100=" + general,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.dial = func([]string) relays { return pool }
	r := &guildRest{}
	client := &bot.Client{Rest: r, ApplicationID: 1}
	m.OnEvent(&events.Ready{GenericEvent: events.NewGenericEvent(client, 0, 0)})
	m.OnEvent(&events.Ready{GenericEvent: events.NewGenericEvent(client, 0, 0)}) // once
	waitFor(t, func() bool { _, _, _, chans := m.state(); _, ok := chans[general]; return ok })
	l := m.links[0]
	if l.guild.Load() != 7 {
		t.Errorf("guild %d", l.guild.Load())
	}
	// A message queued from the gateway is carried by the worker.
	user, _ := snowflake.Parse(v.Puppet.User)
	m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{
		Message: discord.Message{ID: 1, ChannelID: 100, Content: "hi", Author: discord.User{ID: user}}, ChannelID: 100, GuildID: 7,
	}})
	waitFor(t, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return len(pool.published) >= 5 // skua's profile and join, the puppet's profile, join and message
	})
	// The server's emoji went up as skua's pack, and the puppet's palette
	// names it, so Armada can offer the pack from the puppet's emoji.
	has := func(kind int, pk string, tag ...string) func() bool {
		return func() bool {
			pool.mu.Lock()
			defer pool.mu.Unlock()
			return slices.ContainsFunc(pool.published, func(ev *nostr.Event) bool {
				return ev.Kind == kind && ev.PubKey == pk && slices.ContainsFunc(ev.Tags, func(t nostr.Tag) bool {
					return len(t) >= len(tag) && slices.Equal(t[:len(tag)], tag)
				})
			})
		}
	}
	waitFor(t, has(30030, m.primary.PK, "emoji", "blob", "https://cdn.discordapp.com/emojis/123.png"))
	waitFor(t, has(10030, v.Puppet.PK, "a", "30030:"+m.primary.PK+":discord-7"))
	pool.take()
	m.OnEvent(&events.GuildMessageDelete{GenericGuildMessage: &events.GenericGuildMessage{MessageID: 1, ChannelID: 100, GuildID: 7}})
	waitFor(t, func() bool { pool.mu.Lock(); defer pool.mu.Unlock(); return len(pool.published) >= 1 })
	m.Close()
	if fingerprint(&concord.Community{RootEpoch: 1}) == fingerprint(&concord.Community{RootEpoch: 2}) {
		t.Error("a new epoch is not a new session")
	}
	t.Setenv("SKUA_ARMADA_INVITE", "x")
	if FromEnv().Invite != "x" {
		t.Error("env")
	}
}

type guildRest struct{ rest.Rest }

func (guildRest) GetGuild(snowflake.ID, bool, ...rest.RequestOpt) (*discord.RestGuild, error) {
	return &discord.RestGuild{Guild: discord.Guild{ID: 7, Name: "the cove"}}, nil
}

func (guildRest) GetEmojis(snowflake.ID, ...rest.RequestOpt) ([]discord.Emoji, error) {
	return []discord.Emoji{{ID: 123, Name: "blob", Available: true}}, nil
}

func (guildRest) GetStickers(snowflake.ID, ...rest.RequestOpt) ([]discord.Sticker, error) {
	return nil, nil
}

func (guildRest) GetCurrentUser(string, ...rest.RequestOpt) (*discord.OAuth2User, error) {
	return nil, errors.New("not in this test")
}

func (guildRest) GetChannel(snowflake.ID, ...rest.RequestOpt) (discord.Channel, error) {
	var ch discord.GuildTextChannel
	err := json.Unmarshal([]byte(`{"id":"100","type":0,"guild_id":"7","name":"general"}`), &ch)
	return ch, err
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestPostgresMappings(t *testing.T) {
	dsn := os.Getenv("SKUA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKUA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rumor := fmt.Sprint("test-", time.Now().UnixNano())
	p := pgMappings{pool}
	for i, r := range []row{
		{Message: 11, Channel: 5, Webhook: 9, Rumor: rumor, Origin: "armada", Author: "pk", Part: 1},
		{Message: 10, Channel: 5, Webhook: 9, Rumor: rumor, Origin: "armada", Author: "pk", ReplyTo: "parent"},
		{Message: 10, Channel: 5, Webhook: 9, Rumor: rumor, Origin: "armada", Author: "pk"},
		{Message: 12, Channel: 5, Rumor: rumor + "d", Origin: "discord", Author: "1"},
	} {
		if err := p.insert(ctx, r); err != nil {
			t.Fatal(i, err)
		}
	}
	rows, err := p.byRumor(ctx, rumor, 5)
	if err != nil || len(rows) != 2 || rows[0].Message != 10 || rows[1].Part != 1 || rows[0].Webhook != 9 || rows[0].ReplyTo != "parent" {
		t.Fatalf("%+v %v", rows, err)
	}
	if rows, _ := p.byRumor(ctx, rumor, 6); len(rows) != 0 {
		t.Error("crossed links")
	}
	if rows, _ := p.byMessage(ctx, 12); len(rows) != 1 || rows[0].Webhook != 0 || rows[0].Origin != "discord" {
		t.Errorf("%+v", rows)
	}
	_, _ = pool.Exec(ctx, "delete from armada_messages where rumor_id like $1", rumor+"%")

	for _, x := range []reaction{
		{Rumor: rumor + "a", Channel: 5, Message: 10, Emoji: "🔥", Origin: "armada", Author: "pk"},
		{Rumor: rumor + "b", Channel: 5, Message: 10, Emoji: "🔥", Origin: "armada", Author: "pk2"},
		{Rumor: rumor + "c", Channel: 5, Message: 10, Emoji: "🔥", Origin: "discord", Author: "1"},
	} {
		if err := p.addReaction(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := p.armadaReactions(ctx, 10, "🔥"); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if x, ok, err := p.reactionByDiscord(ctx, 10, "1", "🔥"); err != nil || !ok || x.Rumor != rumor+"c" || x.Channel != 5 {
		t.Fatalf("%+v %v %v", x, ok, err)
	}
	if _, ok, _ := p.reactionByDiscord(ctx, 10, "pk", "🔥"); ok {
		t.Error("found an armada reaction as a discord one")
	}
	if _, ok, _ := p.reactionByRumor(ctx, rumor+"a", 6); ok {
		t.Error("crossed links")
	}
	if err := p.dropReaction(ctx, rumor+"a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.reactionByRumor(ctx, rumor+"a", 5); ok {
		t.Error("a dropped reaction stayed")
	}
	if err := p.clearReactions(ctx, 10, "👀"); err != nil {
		t.Fatal(err)
	}
	if n, _ := p.armadaReactions(ctx, 10, "🔥"); n != 1 {
		t.Error("clearing one emoji took another")
	}
	if err := p.clearReactions(ctx, 10, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.reactionByRumor(ctx, rumor+"b", 5); ok {
		t.Error("clearing all left a reaction")
	}
	_, _ = pool.Exec(ctx, "delete from armada_reactions where rumor_id like $1", rumor+"%")
}

// A burst of wraps addressed to skua reads the invite again at most once a
// wakeGap: anyone can send them, and each read dials relays and folds.
func TestWakesAreCapped(t *testing.T) {
	v := loadVectors(t)
	for _, tc := range []struct {
		gap  time.Duration
		want int32
	}{{time.Hour, 1}, {0, 2}} {
		pool := &fakeRelays{stored: append([]*nostr.Event{v.Invite.Event}, v.Community.A.Wraps...)}
		m, err := New(slog.New(slog.DiscardHandler), guard.New(), &fakePoster{}, filter.Default(), nil, Config{
			Invite: v.Invite.URL, Master: v.Puppet.Master, Primary: v.Direct.Recipient, Links: "100=" + general,
		})
		if err != nil {
			t.Fatal(err)
		}
		var reads atomic.Int32
		m.dial = func(urls []string) relays {
			if slices.Equal(urls, m.invite.Bootstrap) {
				reads.Add(1)
			}
			return pool
		}
		m.rest, m.wakeGap = guildRest{}, tc.gap
		go m.run()
		waitFor(t, func() bool { _, _, _, chans := m.state(); _, ok := chans[general]; return ok })
		for range 50 {
			select {
			case m.wake <- struct{}{}:
			default:
			}
		}
		if tc.want > 1 {
			waitFor(t, func() bool { return reads.Load() >= tc.want })
		}
		time.Sleep(200 * time.Millisecond)
		// With no gap a burst still coalesces, into a read or two more.
		if got := reads.Load(); got < tc.want || (tc.gap > 0 && got != tc.want) {
			t.Errorf("gap %v: %d reads of the invite, want %d", tc.gap, got, tc.want)
		}
		m.Close()
	}
}

func TestChannelAndCommandMentions(t *testing.T) {
	got := toArmada("join <#555> and run </purge now:42> or </ping:1>", 7, nil, nil)
	if want := "join https://discord.com/channels/7/555 and run /purge now or /ping"; got != want {
		t.Errorf("%q\nwant %q", got, want)
	}
}

func TestKlipyItem(t *testing.T) {
	for raw, want := range map[string]string{
		"https://klipy.com/gifs/dancing-cat":       "gifs/dancing-cat",
		" https://www.klipy.com/stickers/wave-9/ ": "stickers/wave-9",
		"https://klipy.com/clips/x":                "",
		"http://klipy.com/gifs/x":                  "",
		"https://evil.example/gifs/x":              "",
		"https://klipy.com/gifs/a b":               "",
		"look https://klipy.com/gifs/x":            "",
	} {
		kind, slug, ok := klipyItem(raw)
		if got := kind + "/" + slug; (ok && got != want) || (!ok && want != "") {
			t.Errorf("%q: %q %v", raw, got, ok)
		}
	}
}

// fakeKlipy is Klipy's Items API: nested data for gifs, flat for stickers,
// and a 403 for a bad key.
func fakeKlipy(t *testing.T) *klipy {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/bad/"):
			w.WriteHeader(http.StatusForbidden)
		case strings.Contains(r.URL.Path, "/gifs/items") && r.URL.Query().Get("slugs") == "dancing-cat":
			_, _ = fmt.Fprint(w, `{"result":true,"data":{"data":[{"slug":"dancing-cat","file":{
				"hd":{"gif":{"url":"https://static.klipy.com/a/HD.gif","width":498,"height":280,"size":20000000}},
				"md":{"gif":{"url":"https://static.klipy.com/a/MD.gif","width":320,"height":180,"size":900000}}}}]}}`)
		case strings.Contains(r.URL.Path, "/stickers/items"):
			_, _ = fmt.Fprint(w, `{"data":[{"slug":"wave","file":{"sm":{"gif":{"url":"https://evil.example/x.gif"}},"xs":{"gif":{"url":"https://static.klipy.com/w/XS.gif"}}}}]}`)
		default:
			_, _ = fmt.Fprint(w, `{"data":{"data":[]}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return &klipy{key: "key", api: srv.URL, http: srv.Client()}
}

func TestKlipyGIF(t *testing.T) {
	k := fakeKlipy(t)
	ctx := context.Background()
	// hd is over the 10 MB cap, so md wins.
	if g, ok := k.gif(ctx, "https://klipy.com/gifs/dancing-cat"); !ok || g.URL != "https://static.klipy.com/a/MD.gif" || !slices.Equal(g.tag(), []string{"imeta", "url https://static.klipy.com/a/MD.gif", "m image/gif", "dim 320x180"}) {
		t.Fatalf("%+v %v", g, ok)
	}
	// A rendition off Klipy's CDN is never taken.
	if g, ok := k.gif(ctx, "https://klipy.com/stickers/wave"); !ok || g.URL != "https://static.klipy.com/w/XS.gif" || len(g.tag()) != 3 {
		t.Fatalf("%+v %v", g, ok)
	}
	for _, page := range []string{"https://klipy.com/gifs/unknown", "https://example.com/gifs/x"} {
		if _, ok := k.gif(ctx, page); ok {
			t.Errorf("%s resolved", page)
		}
	}
	k.key = "bad"
	if _, ok := k.gif(ctx, "https://klipy.com/gifs/dancing-cat"); ok {
		t.Error("a refused key resolved")
	}
}

func TestKlipySendCrossesAsTheGIF(t *testing.T) {
	h := newHarness(t, "100="+general)
	h.m.klipy = fakeKlipy(t)
	user, _ := snowflake.Parse(h.v.Puppet.User)
	h.m.toArmada(h.l, discord.Message{ID: 700, ChannelID: 100, Content: "https://klipy.com/gifs/dancing-cat", Author: discord.User{ID: user}})
	o := h.opened(t, concord.KindMessage)
	if o == nil || o.Content != "https://static.klipy.com/a/MD.gif" || concord.Tag(o.Tags, "imeta") != "url https://static.klipy.com/a/MD.gif" || !concord.HasTag(o.Tags, "proxy") {
		t.Fatalf("%+v", o)
	}
	// Not a Klipy item, or a failed lookup: the link goes as it was.
	h.m.toArmada(h.l, discord.Message{ID: 701, ChannelID: 100, Content: "https://klipy.com/gifs/unknown", Author: discord.User{ID: user}})
	if o := h.opened(t, concord.KindMessage); o == nil || o.Content != "https://klipy.com/gifs/unknown" || concord.HasTag(o.Tags, "imeta") {
		t.Fatalf("%+v", o)
	}
	if c := (Config{Klipy: "k"}); c.Klipy == "" || FromEnv().Klipy != os.Getenv("SKUA_ARMADA_KLIPY_KEY") {
		t.Error("config")
	}
}

func TestRelayHealthInTheReport(t *testing.T) {
	now := time.Now()
	rows, notes := relayHealth([]concord.RelayHealth{
		{URL: "wss://a", Up: true, Heard: now.Add(-5 * time.Second)},
		{URL: "wss://b", Up: true, Heard: now.Add(-20 * time.Second), Drops: 2},
		{URL: "wss://c", Heard: now.Add(-3 * time.Minute), Drops: 1},
		{URL: "wss://d"},
	}, now, nil, nil)
	got := core.Readout(rows) + strings.Join(notes, "\n")
	for _, want := range []string{"relays  2 of 4 up", "heard   20s ago", "drops   3", "! wss://c is down, last heard 3m ago", "! wss://d is not connected"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	// None up: no heard row, nothing to measure.
	if rows, _ := relayHealth([]concord.RelayHealth{{URL: "wss://a"}}, now, nil, nil); len(rows) != 1 || rows[0][1] != "0 of 1 up" {
		t.Errorf("%v", rows)
	}
}

func TestReactionEmoji(t *testing.T) {
	for in, want := range map[string]string{
		"": "👍", "+": "👍", " - ": "👎", "🔥": "🔥", "👨‍👩‍👧‍👦": "👨‍👩‍👧‍👦", "👍🏽": "👍🏽",
		"❤️": "❤️", "🇩🇰": "🇩🇰", "1️⃣": "1️⃣",
		"👍 nice": "", ":blob:": "", "12": "", "a": "", "🔥/../x": "",
		strings.Repeat("🔥", maxReactionRunes+1): "",
	} {
		got, ok := reactionEmoji(in)
		if ok != (want != "") || got != want && ok {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
}

func TestArmadaEmoji(t *testing.T) {
	cdn := []string{"emoji", "blob", "https://cdn.discordapp.com/emojis/123.png"}
	for _, tc := range []struct {
		content string
		tags    [][]string
		want    string
	}{
		{":blob:", [][]string{cdn}, "blob:123"},
		{":blob:", [][]string{{"emoji", "blob", "https://example.com/emojis/123.png"}}, ""},
		{":other:", [][]string{cdn}, ""},
		{"blob", [][]string{cdn}, ""},
		{":a:", [][]string{{"emoji", "a", "https://cdn.discordapp.com/emojis/1.png"}}, ""},
	} {
		if got, ok := armadaEmoji(tc.content, tc.tags); got != tc.want || ok != (tc.want != "") {
			t.Errorf("%q: %q %v", tc.content, got, ok)
		}
	}
}
