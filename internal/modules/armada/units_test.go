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
	m.OnEvent(&events.GuildMessageDelete{GenericGuildMessage: &events.GenericGuildMessage{MessageID: 1, ChannelID: 100, GuildID: 7}})
	waitFor(t, func() bool { pool.mu.Lock(); defer pool.mu.Unlock(); return len(pool.published) >= 6 })
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
		{Message: 10, Channel: 5, Webhook: 9, Rumor: rumor, Origin: "armada", Author: "pk"},
		{Message: 10, Channel: 5, Webhook: 9, Rumor: rumor, Origin: "armada", Author: "pk"},
		{Message: 12, Channel: 5, Rumor: rumor + "d", Origin: "discord", Author: "1"},
	} {
		if err := p.insert(ctx, r); err != nil {
			t.Fatal(i, err)
		}
	}
	rows, err := p.byRumor(ctx, rumor, 5)
	if err != nil || len(rows) != 2 || rows[0].Message != 10 || rows[1].Part != 1 || rows[0].Webhook != 9 {
		t.Fatalf("%+v %v", rows, err)
	}
	if rows, _ := p.byRumor(ctx, rumor, 6); len(rows) != 0 {
		t.Error("crossed links")
	}
	if rows, _ := p.byMessage(ctx, 12); len(rows) != 1 || rows[0].Webhook != 0 || rows[0].Origin != "discord" {
		t.Errorf("%+v", rows)
	}
	_, _ = pool.Exec(ctx, "delete from armada_messages where rumor_id like $1", rumor+"%")
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
