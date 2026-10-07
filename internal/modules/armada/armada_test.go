// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
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
)

// The concord package's interop vectors: a community made by upstream's
// TypeScript with an owner, an admin, a moderator, a banned member x and a
// helper y, and channel 2121... public.
type vectors struct {
	Puppet    struct{ Master, User, SK, PK string }
	Community struct {
		Members map[string]string
		A       struct{ Wraps []*nostr.Event }
	}
	Invite struct {
		URL   string
		Event *nostr.Event
	}
	Direct struct {
		Recipient              string
		Owner, Helper, Foreign *nostr.Event
	}
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("../../concord/testdata/upstream.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

var general = strings.Repeat("21", 32)

func key(t *testing.T, b byte) concord.Key {
	t.Helper()
	var s [32]byte
	for i := range s {
		s[i] = b
	}
	k, err := concord.KeyFromSecret(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

type fakeRelays struct {
	mu        sync.Mutex
	published []*nostr.Event
	stored    []*nostr.Event
	subs      []nostr.Filter
	fns       []func(*nostr.Event)
	health    []concord.RelayHealth
	refuse    bool
}

func (f *fakeRelays) Query(_ context.Context, flt nostr.Filter) []*nostr.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*nostr.Event
	for _, ev := range f.stored {
		if flt.Matches(ev) {
			out = append(out, ev)
		}
	}
	return out
}

func (f *fakeRelays) Publish(_ context.Context, ev *nostr.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse {
		return io.ErrUnexpectedEOF
	}
	f.published = append(f.published, ev)
	return nil
}

func (f *fakeRelays) Subscribe(_ context.Context, flt nostr.Filter, fn func(*nostr.Event)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, flt)
	f.fns = append(f.fns, fn)
}

func (f *fakeRelays) Register(...concord.StreamKey) {}
func (f *fakeRelays) Health() []concord.RelayHealth {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.health
}
func (f *fakeRelays) Close() {}

func (f *fakeRelays) take() []*nostr.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.published
	f.published = nil
	return out
}

type fakePoster struct {
	mu      sync.Mutex
	sent    []discord.WebhookMessageCreate
	edits   map[snowflake.ID]string
	deleted []snowflake.ID
	next    snowflake.ID
}

func (p *fakePoster) Send(_ context.Context, _ rest.Rest, _, _, _ snowflake.ID, msg discord.WebhookMessageCreate) (*discord.Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	p.sent = append(p.sent, msg)
	hook := snowflake.ID(77)
	return &discord.Message{ID: 1000 + p.next, WebhookID: &hook}, nil
}

func (p *fakePoster) Edit(_ context.Context, _ rest.Rest, _, _, _, _, message snowflake.ID, u discord.WebhookMessageUpdate) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.edits == nil {
		p.edits = map[snowflake.ID]string{}
	}
	p.edits[message] = *u.Content
	return nil
}

func (p *fakePoster) Delete(_ context.Context, _ rest.Rest, _, _, _, _, message snowflake.ID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleted = append(p.deleted, message)
	return nil
}

type fakeRest struct {
	rest.Rest
	deleted []snowflake.ID
	reacts  []string // "+msg emoji", "-msg emoji", "-msg emoji user"
	// refreshed answers /attachments/refresh-urls; nil fails the call.
	refreshed map[string]string
	fail      error // what refresh-urls answers instead, when set
	calls     int
	emojis    []discord.Emoji // the server's, for its pack
	stickers  []discord.Sticker
}

func (r *fakeRest) GetStickers(snowflake.ID, ...rest.RequestOpt) ([]discord.Sticker, error) {
	return r.stickers, nil
}

func (r *fakeRest) GetGuild(id snowflake.ID, _ bool, _ ...rest.RequestOpt) (*discord.RestGuild, error) {
	icon := "abc"
	return &discord.RestGuild{ID: id, Name: "the cove", Icon: &icon}, nil
}

func (r *fakeRest) GetEmojis(snowflake.ID, ...rest.RequestOpt) ([]discord.Emoji, error) {
	if r.emojis == nil {
		return nil, errors.New("refused")
	}
	return r.emojis, nil
}

func (r *fakeRest) Do(_ *rest.CompiledEndpoint, rq, rs any, _ ...rest.RequestOpt) error {
	r.calls++
	if r.fail != nil {
		return r.fail
	}
	if r.refreshed == nil {
		return errors.New("refused")
	}
	var out []map[string]string
	for _, u := range rq.(map[string][]string)["attachment_urls"] {
		out = append(out, map[string]string{"original": u, "refreshed": r.refreshed[u]})
	}
	b, _ := json.Marshal(map[string]any{"refreshed_urls": out})
	return json.Unmarshal(b, rs)
}

func (r *fakeRest) GetChannel(id snowflake.ID, _ ...rest.RequestOpt) (discord.Channel, error) {
	return discord.GuildTextChannel{}, nil
}

func (r *fakeRest) DeleteMessage(_, id snowflake.ID, _ ...rest.RequestOpt) error {
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *fakeRest) AddReaction(_, msg snowflake.ID, emoji string, _ ...rest.RequestOpt) error {
	r.reacts = append(r.reacts, fmt.Sprint("+", msg, " ", emoji))
	return nil
}

func (r *fakeRest) RemoveOwnReaction(_, msg snowflake.ID, emoji string, _ ...rest.RequestOpt) error {
	r.reacts = append(r.reacts, fmt.Sprint("-", msg, " ", emoji))
	return nil
}

func (r *fakeRest) RemoveUserReaction(_, msg snowflake.ID, emoji string, user snowflake.ID, _ ...rest.RequestOpt) error {
	r.reacts = append(r.reacts, fmt.Sprint("-", msg, " ", emoji, " ", user))
	return nil
}

type harness struct {
	m     *Module
	l     *link
	pool  *fakeRelays
	post  *fakePoster
	rest  *fakeRest
	v     vectors
	chans map[string]concord.ChatChannel
	ms    int64 // each rumor is sent a moment after the last
}

func newHarness(t *testing.T, links string) *harness {
	t.Helper()
	v := loadVectors(t)
	m, err := New(slog.New(slog.DiscardHandler), guard.New(), &fakePoster{}, filter.Default(), nil, Config{
		Invite: v.Invite.URL, Master: v.Puppet.Master, Primary: v.Direct.Recipient, Links: links,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	h := &harness{m: m, l: m.links[0], pool: &fakeRelays{}, post: m.post.(*fakePoster), rest: &fakeRest{}, v: v}
	h.pool.stored = append([]*nostr.Event{v.Invite.Event, v.Direct.Owner, v.Direct.Helper, v.Direct.Foreign}, v.Community.A.Wraps...)
	m.dial = func([]string) relays { return h.pool }
	m.fetcher = http.DefaultClient
	m.rest, m.app = h.rest, 1
	h.l.guild.Store(7)
	c, err := m.resolve()
	if err != nil {
		t.Fatal(err)
	}
	m.session(m.ctx, c)
	_, _, _, h.chans = m.state()
	h.pool.take() // skua's profile and join
	return h
}

// chat is a rumor in general as the member with secret byte b.
func (h *harness) chat(t *testing.T, b byte, kind int, content string, tags ...[]string) (*nostr.Event, string) {
	t.Helper()
	k := key(t, b)
	ch := h.chans[general]
	h.ms++
	r, err := concord.NewChat(ch, kind, content, tags, k.PK, 1_800_000_000_000+h.ms)
	if err != nil {
		t.Fatal(err)
	}
	w, err := concord.SealChat(r, ch, k.SK)
	if err != nil {
		t.Fatal(err)
	}
	return w, r.ID
}

// deliver is a wrap arriving from the relays, carried through to Discord.
func (h *harness) deliver(t *testing.T, w *nostr.Event) {
	t.Helper()
	h.m.onWrap(h.l, w)
	select {
	case o := <-h.l.toDisc:
		h.m.toDiscord(h.l, o)
	default:
	}
}

func (h *harness) opened(t *testing.T, kind int) *concord.Opened {
	t.Helper()
	for _, ev := range h.pool.take() {
		if o, err := concord.OpenChat(ev, h.chans[general]); err == nil && o.Kind == kind {
			return o
		}
	}
	return nil
}

func TestSessionSubscribesTheLinkedChannel(t *testing.T) {
	h := newHarness(t, "100="+general+",200="+strings.Repeat("22", 32)+",300="+strings.Repeat("99", 32))
	// The control plane, skua's inbox, the private channel's rotation
	// addresses and both readable linked channels: the
	// private one is readable because the invite granted its key. The third
	// is not in the community and is skipped by name.
	if len(h.pool.subs) != 5 || !slices.ContainsFunc(h.pool.subs, func(f nostr.Filter) bool {
		return slices.Equal(f.Authors, h.chans[general].Authors()) && f.Since != nil
	}) {
		t.Fatalf("subs %+v", h.pool.subs)
	}
	if !h.m.missing[strings.Repeat("99", 32)] {
		t.Error("the unreadable link was not noted")
	}
	// A refold with the same keys does not subscribe again.
	c, f, _ := h.m.comm, h.m.folded, 0
	h.m.apply(h.m.ctx, c, f)
	if len(h.pool.subs) != 5 {
		t.Error("subscribed twice")
	}
}

func TestDiscordToArmada(t *testing.T) {
	h := newHarness(t, "100="+general)
	user, _ := snowflake.Parse(h.v.Puppet.User)
	msg := discord.Message{
		ID: 500, ChannelID: 100, Content: "hi <@5> <:blob:123>",
		Author:   discord.User{ID: user, Username: "kit"},
		Mentions: []discord.User{{ID: 5, Username: "wren"}},
	}
	h.m.toArmada(h.l, msg)
	o := h.opened(t, concord.KindMessage)
	if o == nil {
		t.Fatal("nothing published")
	}
	// The puppet is upstream's derivation, so npubs carry over.
	if o.Author != h.v.Puppet.PK || o.Content != "hi @wren :blob:" {
		t.Fatalf("published %+v", o)
	}
	if concord.Tag(o.Tags, "proxy") != "https://discord.com/channels/7/100/500" || concord.Tag(o.Tags, "emoji") != "blob" {
		t.Errorf("tags %v", o.Tags)
	}
	// A reply quotes the rumor it answers and its author.
	ref := snowflake.ID(500)
	h.m.toArmada(h.l, discord.Message{ID: 501, ChannelID: 100, Content: "again", Author: msg.Author, MessageReference: &discord.MessageReference{MessageID: &ref}})
	r := h.opened(t, concord.KindMessage)
	if r == nil || concord.Tag(r.Tags, "q") != o.RumorID {
		t.Fatalf("reply %+v", r)
	}
	// Deleting it on Discord tombstones it as its puppet.
	h.m.discordDelete(h.l, 7, 500)
	if d := h.opened(t, concord.KindDelete); d == nil || concord.Tag(d.Tags, "e") != o.RumorID || d.Author != h.v.Puppet.PK {
		t.Fatalf("delete %+v", d)
	}
	// A banned member's puppet never speaks, and an empty message is nothing.
	h.m.folded.Banned[h.v.Puppet.PK] = true
	h.m.toArmada(h.l, discord.Message{ID: 502, ChannelID: 100, Content: "let me in", Author: msg.Author})
	delete(h.m.folded.Banned, h.v.Puppet.PK)
	h.m.toArmada(h.l, discord.Message{ID: 503, ChannelID: 100, Author: msg.Author})
	if got := h.pool.take(); len(got) != 0 {
		t.Errorf("published %d", len(got))
	}
	// A refused publish is not recorded.
	h.pool.refuse = true
	h.m.toArmada(h.l, discord.Message{ID: 504, ChannelID: 100, Content: "lost", Author: msg.Author})
	if rows, _ := h.m.maps.byMessage(context.Background(), 504); len(rows) != 0 {
		t.Error("recorded a message that never crossed")
	}
}

func TestArmadaToDiscord(t *testing.T) {
	h := newHarness(t, "100="+general)
	mod := h.v.Community.Members["mod"]
	h.pool.stored = append(h.pool.stored, signedProfile(t, key(t, 3), `{"display_name":"discord mod","name":"sooty tern","picture":"https://img.example/t.png"}`))

	plain := []byte("pretend this is a png")
	srv, tag := encryptedBlob(t, plain)
	defer srv.Close()
	w, id := h.chat(t, 3, concord.KindMessage, "look @everyone "+srv.URL+"/blob", tag)
	h.deliver(t, w)
	if len(h.post.sent) != 1 {
		t.Fatalf("sent %d", len(h.post.sent))
	}
	got := h.post.sent[0]
	// The name with discord in it is refused for a webhook; the next one wins.
	if got.Username != "sooty tern" || got.AvatarURL != "https://img.example/t.png" || got.Content != "look @everyone" {
		t.Errorf("sent %+v", got)
	}
	if got.AllowedMentions == nil || len(got.AllowedMentions.Parse) != 0 {
		t.Error("it could ping")
	}
	if len(got.Files) != 1 || got.Files[0].Name != "blob.png" {
		t.Fatalf("files %+v", got.Files)
	}
	if b, _ := io.ReadAll(got.Files[0].Reader); string(b) != string(plain) {
		t.Errorf("file %q", b)
	}

	// Seen once, or recorded once, it is never posted again.
	h.deliver(t, w)
	h.m.toDiscord(h.l, &concord.Opened{RumorID: id, Author: mod, Kind: concord.KindMessage, Content: "x"})
	if len(h.post.sent) != 1 {
		t.Fatalf("posted twice: %d", len(h.post.sent))
	}

	// A reply names who it answers, escaped, and links the Discord copy.
	r, rid := h.chat(t, 5, concord.KindMessage, strings.Repeat("long ", 900), []string{"q", id, "", mod})
	h.deliver(t, r)
	if len(h.post.sent) != 4 {
		t.Fatalf("a 4,500 character message became %d posts", len(h.post.sent)-1)
	}
	line := "\n-# replying to [sooty tern](https://discord.com/channels/7/100/1001)"
	if got := h.post.sent[3]; len(got.Embeds) != 0 || !strings.HasSuffix(got.Content, "long "+line) {
		t.Errorf("reply %q %+v", got.Content[max(0, len(got.Content)-120):], got.Embeds)
	}

	// An edit keeps the line, though the edit itself names no q.
	edit, _ := h.chat(t, 5, concord.KindEdit, "short now", []string{"e", rid})
	h.deliver(t, edit)
	if got := h.post.edits[1002]; got != "short now"+line {
		t.Errorf("edited reply %q", got)
	}

	// A Discord author is named by their puppet's profile, not a mention.
	_ = h.m.maps.insert(context.Background(), row{Message: 600, Channel: 100, Rumor: "from-discord", Origin: "discord", Author: "42"})
	d, _ := h.chat(t, 5, concord.KindMessage, "yes", []string{"q", "from-discord", "", mod})
	h.deliver(t, d)
	if got := h.post.sent[len(h.post.sent)-1].Content; got != "yes\n-# replying to [nostr:"+h.m.puppet("42").PK[:8]+"](https://discord.com/channels/7/100/600)" {
		t.Errorf("reply to discord %q", got)
	}
}

func TestArmadaLoopsAndBans(t *testing.T) {
	h := newHarness(t, "100="+general)
	user, _ := snowflake.Parse(h.v.Puppet.User)
	puppet := h.m.puppet(user.String())
	primary := h.m.primary
	tagged, _ := h.chat(t, 3, concord.KindMessage, "from another bridge", []string{"proxy", "https://discord.com/channels/1/2/3", "web"})
	banned, _ := h.chat(t, 4, concord.KindMessage, "x is banned")
	for name, w := range map[string]*nostr.Event{"proxy tag": tagged, "banned": banned} {
		h.deliver(t, w)
		if len(h.post.sent) != 0 {
			t.Fatalf("%s crossed", name)
		}
	}
	for name, pk := range map[string]string{"puppet": puppet.PK, "skua": primary.PK} {
		h.m.toDiscord(h.l, &concord.Opened{RumorID: name, Author: pk, Kind: concord.KindMessage, Content: "echo"})
		if len(h.post.sent) != 0 {
			t.Fatalf("%s crossed", name)
		}
	}
	// A server that turned the bridge off gets nothing.
	h.m.Gate(func(snowflake.ID) bool { return false })
	ok, _ := h.chat(t, 3, concord.KindMessage, "hello")
	h.deliver(t, ok)
	if len(h.post.sent) != 0 {
		t.Fatal("posted into a server that turned it off")
	}
}

func TestArmadaDeletes(t *testing.T) {
	h := newHarness(t, "100="+general)
	w, id := h.chat(t, 3, concord.KindMessage, "mine")
	h.deliver(t, w)
	user, _ := snowflake.Parse(h.v.Puppet.User)
	h.m.toArmada(h.l, discord.Message{ID: 600, ChannelID: 100, Content: "from discord", Author: discord.User{ID: user}})
	fromDiscord := h.opened(t, concord.KindMessage)

	stranger, _ := h.chat(t, 0x42, concord.KindDelete, "", []string{"e", id})
	h.deliver(t, stranger)
	if len(h.post.deleted) != 0 {
		t.Fatal("a stranger deleted someone's message")
	}
	own, _ := h.chat(t, 3, concord.KindDelete, "", []string{"e", id}, []string{"e", "nothing"})
	h.deliver(t, own)
	if !slices.Equal(h.post.deleted, []snowflake.ID{1001}) {
		t.Fatalf("deleted %v", h.post.deleted)
	}
	// The owner moderates: their delete reaches the member's own message.
	mod, _ := h.chat(t, 1, concord.KindDelete, "", []string{"e", fromDiscord.RumorID})
	h.deliver(t, mod)
	if !slices.Equal(h.rest.deleted, []snowflake.ID{600}) {
		t.Fatalf("rest deleted %v", h.rest.deleted)
	}
}

func TestOnEvent(t *testing.T) {
	h := newHarness(t, "100="+general)
	send := func(msg discord.Message) {
		h.m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{Message: msg, ChannelID: msg.ChannelID, GuildID: 9}})
	}
	send(discord.Message{ChannelID: 100, Author: discord.User{Bot: true}, Content: "a bot"})
	hook := snowflake.ID(1)
	send(discord.Message{ChannelID: 100, WebhookID: &hook, Content: "a webhook"})
	send(discord.Message{ChannelID: 101, Content: "unlinked"})
	send(discord.Message{ChannelID: 100, Type: discord.MessageTypeUserJoin})
	if len(h.l.toArm) != 0 {
		t.Fatal("queued what never crosses")
	}
	send(discord.Message{ChannelID: 100, Content: "hello"})
	h.m.OnEvent(&events.GuildMessageUpdate{GenericGuildMessage: &events.GenericGuildMessage{Message: discord.Message{ChannelID: 100, Content: "edited"}, ChannelID: 100, GuildID: 9}})
	if len(h.l.toArm) != 2 || h.l.guild.Load() != 9 {
		t.Fatal("a member's message was not queued")
	}
	if first, second := <-h.l.toArm, <-h.l.toArm; first.edit || !second.edit {
		t.Fatal("a new message and its edit were queued as the wrong kinds")
	}
}

func TestConfig(t *testing.T) {
	v := loadVectors(t)
	good := Config{Invite: v.Invite.URL, Master: v.Puppet.Master, Primary: strings.Repeat("0a", 32), Links: "1=" + general}
	for name, edit := range map[string]func(*Config){
		"invite":          func(c *Config) { c.Invite = "https://example.com" },
		"master":          func(c *Config) { c.Master = strings.Repeat("AB", 32) },
		"primary":         func(c *Config) { c.Primary = "nsec1nope" },
		"primary hex":     func(c *Config) { c.Primary = "zz" },
		"no links":        func(c *Config) { c.Links = " , " },
		"bad link":        func(c *Config) { c.Links = "general=" + general },
		"discord twice":   func(c *Config) { c.Links = "1=" + general + ",1=" + strings.Repeat("22", 32) },
		"armada twice":    func(c *Config) { c.Links = "1=" + general + ",2=" + general },
		"short armada id": func(c *Config) { c.Links = "1=abc" },
	} {
		c := good
		edit(&c)
		if _, err := New(slog.New(slog.DiscardHandler), guard.New(), &fakePoster{}, filter.Default(), nil, c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	good.Blossom = "https://a.example/, https://b.example"
	m, err := New(slog.New(slog.DiscardHandler), guard.New(), &fakePoster{}, filter.Default(), nil, good)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.blob.servers, []string{"https://a.example", "https://b.example"}) {
		t.Errorf("blossom %v", m.blob.servers)
	}
	if !good.Configured() || (Config{}).Configured() {
		t.Error("configured")
	}
	if m.Name() != "armada" || m.Commands() != nil || m.Want().Required == 0 || m.Perms() == 0 || m.Help().Line == "" {
		t.Error("module surface")
	}
}

func TestPuppetMatchesUpstream(t *testing.T) {
	v := loadVectors(t)
	master, _ := hex.DecodeString(v.Puppet.Master)
	if k := derivePuppet(master, v.Puppet.User); k.SK != v.Puppet.SK || k.PK != v.Puppet.PK {
		t.Fatalf("puppet %s, upstream %s", k.PK, v.Puppet.PK)
	}
}

func signedProfile(t *testing.T, k concord.Key, content string) *nostr.Event {
	t.Helper()
	ev := &nostr.Event{Kind: 0, Content: content, CreatedAt: nostr.Now(), Tags: nostr.Tags{}}
	if err := ev.Sign(k.SK); err != nil {
		t.Fatal(err)
	}
	return ev
}

// encryptedBlob serves plain encrypted as Armada does and returns its tag.
func encryptedBlob(t *testing.T, plain []byte) (*httptest.Server, []string) {
	t.Helper()
	k, n := make([]byte, 32), make([]byte, 12)
	for i := range k {
		k[i] = byte(i)
	}
	block, _ := aes.NewCipher(k)
	gcm, _ := cipher.NewGCM(block)
	sealed := gcm.Seal(nil, n, plain, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(sealed)
	}))
	sum := sha256.Sum256(plain)
	return srv, []string{"imeta", "url " + srv.URL + "/blob", "m image/png", "encryption-algorithm aes-gcm",
		"decryption-key " + hex.EncodeToString(k), "decryption-nonce " + hex.EncodeToString(n), "ox " + hex.EncodeToString(sum[:]), "size 21"}
}

func TestDiscordEdit(t *testing.T) {
	h := newHarness(t, "100="+general)
	user, _ := snowflake.Parse(h.v.Puppet.User)
	author := discord.User{ID: user}
	h.m.toArmada(h.l, discord.Message{ID: 500, ChannelID: 100, Content: "frist", Author: author})
	orig := h.opened(t, concord.KindMessage)
	at := time.Unix(1_800_000_000, 0)
	edit := discord.Message{ID: 500, ChannelID: 100, Content: "first", Author: author, EditedTimestamp: &at}
	h.m.discordEdit(h.l, edit)
	e := h.opened(t, concord.KindEdit)
	if e == nil || e.Content != "first" || concord.Tag(e.Tags, "e") != orig.RumorID || e.Author != h.v.Puppet.PK || !concord.HasTag(e.Tags, "proxy") {
		t.Fatalf("edit %+v", e)
	}
	// An unfurl repeats the same edit, and isn't one; nor is an unedited update.
	h.m.discordEdit(h.l, edit)
	h.m.discordEdit(h.l, discord.Message{ID: 500, ChannelID: 100, Content: "x", Author: author})
	later := at.Add(time.Second)
	h.m.discordEdit(h.l, discord.Message{ID: 999, ChannelID: 100, Content: "never bridged", Author: author, EditedTimestamp: &later})
	if got := h.pool.take(); len(got) != 0 {
		t.Fatalf("published %d", len(got))
	}
}

func TestArmadaEdit(t *testing.T) {
	h := newHarness(t, "100="+general)
	w, id := h.chat(t, 3, concord.KindMessage, strings.Repeat("long ", 900))
	h.deliver(t, w)
	if len(h.post.sent) != 3 {
		t.Fatalf("parts %d", len(h.post.sent))
	}
	// Someone else's edit is not the author's to make.
	other, _ := h.chat(t, 5, concord.KindEdit, "hijack", []string{"e", id})
	h.deliver(t, other)
	if len(h.post.edits) != 0 {
		t.Fatal("edited by someone else")
	}
	short, _ := h.chat(t, 3, concord.KindEdit, "short now", []string{"e", id})
	stale, _ := h.chat(t, 3, concord.KindEdit, "an older edit", []string{"e", id})
	grown, _ := h.chat(t, 3, concord.KindEdit, strings.Repeat("grown ", 1500), []string{"e", id})
	h.deliver(t, short)
	if h.post.edits[1001] != "short now" || h.post.edits[1002] != "-# removed in an edit" || h.post.edits[1003] != "-# removed in an edit" {
		t.Fatalf("edits %v", h.post.edits)
	}
	h.deliver(t, grown)
	if len(h.post.sent) != 5 {
		t.Fatalf("a grown edit made %d posts", len(h.post.sent))
	}
	if rows, _ := h.m.maps.byRumor(context.Background(), id, 100); len(rows) != 5 || rows[4].Part != 4 {
		t.Fatalf("rows %+v", rows)
	}
	// stale was sent before grown, so it arrives too late to apply.
	h.post.edits = nil
	o, err := concord.OpenChat(stale, h.chans[general])
	if err != nil {
		t.Fatal(err)
	}
	h.m.toDiscord(h.l, o)
	if len(h.post.edits) != 0 {
		t.Fatal("an older edit replaced a newer one")
	}
}

func TestArmadaTextIsScreened(t *testing.T) {
	h := newHarness(t, "100="+general)
	grabber, _ := h.chat(t, 3, concord.KindMessage, "look https://grabify.link/abc123")
	h.deliver(t, grabber)
	if len(h.post.sent) != 0 {
		t.Fatal("posted an ip grabber link")
	}
	w, id := h.chat(t, 3, concord.KindMessage, "hello f*ggot")
	h.deliver(t, w)
	if len(h.post.sent) != 1 || strings.Contains(h.post.sent[0].Content, "ggot") || !strings.HasPrefix(h.post.sent[0].Content, "hello ") {
		t.Fatalf("sent %+v", h.post.sent)
	}
	// An edit that would not be posted leaves the copy as it was.
	edit, _ := h.chat(t, 3, concord.KindEdit, "now https://grabify.link/abc123", []string{"e", id})
	h.deliver(t, edit)
	if len(h.post.edits) != 0 {
		t.Fatalf("edited to %v", h.post.edits)
	}
}

func TestDirectInviteKeyIsTaken(t *testing.T) {
	h := newHarness(t, "100="+general)
	// The invite carried the private channel at epoch 4; the owner's Direct
	// Invite moved it to 7. The helper's grant of general and the bundle
	// for another community were not taken.
	if k := h.m.comm.Private[strings.Repeat("22", 32)]; k.Epoch != 7 {
		t.Fatalf("private channel at epoch %d", k.Epoch)
	}
	if _, ok := h.m.comm.Private[general]; ok {
		t.Fatal("took a key from someone without manage channels")
	}
	// skua said where her inbox is.
	h.m.announce(context.Background(), h.pool, h.m.comm)
	var inbox *nostr.Event
	for _, ev := range h.pool.take() {
		if ev.Kind == concord.KindDMRelays {
			inbox = ev
		}
	}
	if inbox == nil || inbox.PubKey != h.m.primary.PK {
		t.Fatal("no inbox relay list")
	}
}

func TestInboxWakesOncePerWrap(t *testing.T) {
	h := newHarness(t, "100="+general)
	var inbox func(*nostr.Event)
	for i, f := range h.pool.subs {
		if len(f.Tags["k"]) > 0 {
			inbox = h.pool.fns[i]
		}
	}
	if inbox == nil {
		t.Fatal("no inbox subscription")
	}
	wakes := func() int {
		select {
		case <-h.m.wake:
			return 1
		default:
			return 0
		}
	}
	// A replay of a wrap already seen, from a second relay or a new
	// session, does not wake run again; a new one does.
	inbox(&nostr.Event{ID: "a"})
	first := wakes()
	inbox(&nostr.Event{ID: "a"})
	again := wakes()
	inbox(&nostr.Event{ID: "b"})
	if first != 1 || again != 0 || wakes() != 1 {
		t.Fatal("a replayed wrap woke run, or a new one didn't")
	}
}

func (r *fakeRest) GetCurrentUser(string, ...rest.RequestOpt) (*discord.OAuth2User, error) {
	avatar, banner := "a1b2", "c3d4"
	return &discord.OAuth2User{ID: 42, Avatar: &avatar, Banner: &banner}, nil
}

func TestSkuasProfileWearsHerDiscordLook(t *testing.T) {
	h := newHarness(t, "100="+general)
	h.m.announce(context.Background(), h.pool, h.m.comm)
	for _, ev := range h.pool.take() {
		if ev.Kind != 0 || ev.PubKey != h.m.primary.PK {
			continue
		}
		var meta map[string]any
		_ = json.Unmarshal([]byte(ev.Content), &meta)
		if meta["picture"] != "https://cdn.discordapp.com/avatars/42/a1b2.png?size=1024" ||
			meta["banner"] != "https://cdn.discordapp.com/banners/42/c3d4.png?size=1024" || meta["bot"] != true || meta["name"] != "skua" {
			t.Fatalf("profile %v", meta)
		}
		return
	}
	t.Fatal("no profile published")
}

func TestReportForItsServerOnly(t *testing.T) {
	h := newHarness(t, "100="+general+",200="+strings.Repeat("99", 32))
	h.m.links[1].guild.Store(7)
	w, _ := h.chat(t, 3, concord.KindMessage, "hello")
	h.deliver(t, w)
	h.m.links[0].tally.fail()
	h.pool.health = []concord.RelayHealth{{URL: "wss://a", Up: true, Heard: time.Now()}}
	r := h.m.Report(7)
	all := core.Readout(r.Rows) + strings.Join(r.Notes, "\n")
	for _, want := range []string{"health     outage", "✗ outage: #99999999 isn't readable", "community  test", "relays     1 of 1 up",
		"<#100> ↔ #general · readable", "1 in · 0 out · last 0s ago · 1 failed", "<#200> ↔ #99999999 · not readable"} {
		if !strings.Contains(all, want) {
			t.Errorf("report is missing %q:\n%s", want, all)
		}
	}
	if r := h.m.Report(8); len(r.Rows)+len(r.Notes) != 0 {
		t.Errorf("another server sees %+v", r)
	}
}

// Every verdict, from what skua holds: the worst reason leads.
func TestHealthVerdicts(t *testing.T) {
	h := newHarness(t, "100="+general)
	l := h.m.links[0]
	up := []concord.RelayHealth{{URL: "wss://a", Up: true}, {URL: "wss://b", Up: true}}
	base := func() snapshot {
		s := h.m.snapshot()
		s.relays = up
		return s
	}
	now := time.Now()
	for name, tc := range map[string]struct {
		edit  func(*snapshot)
		level string
		why   string
	}{
		"ok":             {func(*snapshot) {}, healthOK, ""},
		"no invite":      {func(s *snapshot) { s.comm, s.failing = nil, "invite bundle not found on its relays" }, healthOutage, "can't read the invite: invite bundle not found"},
		"connecting":     {func(s *snapshot) { s.comm = nil }, healthDegraded, "still connecting"},
		"invite stopped": {func(s *snapshot) { s.failing = "this invite link has expired" }, healthDegraded, "the invite stopped reading"},
		"no relays":      {func(s *snapshot) { s.relays = []concord.RelayHealth{{URL: "wss://a"}} }, healthOutage, "no relay is reachable"},
		"a relay down":   {func(s *snapshot) { s.relays = []concord.RelayHealth{{URL: "wss://a", Up: true}, {URL: "wss://b"}} }, healthDegraded, "1 of 2 relays are down"},
		"unreadable":     {func(s *snapshot) { s.chans = nil }, healthOutage, "#general isn't readable"},
		"stale key": {func(s *snapshot) {
			s.drift = map[string]concord.Drift{general: {Epoch: 0, Why: "the key moved on without skua: give her the channel again", Stale: true}}
		}, healthOutage, "#general: the key moved on without skua"},
		"arriving": {func(s *snapshot) {
			s.drift = map[string]concord.Drift{general: {Why: "a rotation is still arriving"}}
		}, healthDegraded, "#general: a rotation is still arriving"},
	} {
		s := base()
		tc.edit(&s)
		v := s.health([]*link{l}, now)
		if v.level != tc.level || !strings.HasPrefix(v.why, tc.why) {
			t.Errorf("%s: %+v", name, v)
		}
	}
	// One way for an hour: messages out, nothing back. Then one comes back.
	l.tally.crossedOut()
	if v := base().health([]*link{l}, now.Add(oneWay+time.Minute)); v.level != healthDegraded || !strings.Contains(v.why, "with nothing back") {
		t.Errorf("one way: %+v", v)
	}
	l.tally.crossedIn()
	if v := base().health([]*link{l}, now.Add(oneWay+time.Minute)); v.level != healthOK {
		t.Errorf("after an answer: %+v", v)
	}
}

// recorder keeps the records a logger was given.
type recorder struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec)
	return nil
}
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recorder) WithGroup(string) slog.Handler      { return r }

func (r *recorder) health() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, rec := range r.recs {
		if strings.HasPrefix(rec.Message, "armada: health") {
			out = append(out, rec.Level.String()+" "+rec.Message)
		}
	}
	return out
}

// The watcher logs a change of verdict once, not on every tick, and the
// return to ok.
func TestHealthIsLoggedOnChangeOnly(t *testing.T) {
	h := newHarness(t, "100="+general)
	rec := &recorder{}
	h.m.log = slog.New(rec)
	h.pool.health = []concord.RelayHealth{{URL: "wss://a", Up: true}}
	go h.m.watchHealth(10 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	h.pool.mu.Lock()
	h.pool.health = []concord.RelayHealth{{URL: "wss://a"}}
	h.pool.mu.Unlock()
	waitFor(t, func() bool { return len(rec.health()) == 1 })
	time.Sleep(50 * time.Millisecond)
	h.pool.mu.Lock()
	h.pool.health = []concord.RelayHealth{{URL: "wss://a", Up: true}}
	h.pool.mu.Unlock()
	waitFor(t, func() bool { return len(rec.health()) == 2 })
	time.Sleep(50 * time.Millisecond)
	if got := rec.health(); !slices.Equal(got, []string{"WARN armada: health outage", "INFO armada: health ok again"}) {
		t.Errorf("logged %v", got)
	}
}

func TestReportBeforeAndWithoutTheInvite(t *testing.T) {
	v := loadVectors(t)
	m, err := New(slog.New(slog.DiscardHandler), guard.New(), &fakePoster{}, filter.Default(), nil, Config{
		Invite: v.Invite.URL, Master: v.Puppet.Master, Primary: v.Direct.Recipient, Links: "100=" + general,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.links[0].guild.Store(7)
	if r := m.Report(7); r.Rows[0][1] != healthDegraded || r.Notes[0] != "! degraded: still connecting" {
		t.Errorf("before the first read: %+v", r)
	}
	// No bundle on the relays: the read fails, the loop carries on, and the
	// report says why.
	m.dial = func([]string) relays { return &fakeRelays{} }
	m.rest = guildRest{}
	go m.run()
	defer m.Close()
	waitFor(t, func() bool { return m.Report(7).Rows[0][1] == healthOutage })
	if r := m.Report(7); !strings.HasPrefix(r.Notes[0], "✗ outage: can't read the invite") {
		t.Errorf("no reason: %+v", r)
	}
}

func TestDiscordReactions(t *testing.T) {
	h := newHarness(t, "100="+general)
	h.m.self = 99
	user, _ := snowflake.Parse(h.v.Puppet.User)
	h.m.toArmada(h.l, discord.Message{ID: 500, ChannelID: 100, Content: "hi", Author: discord.User{ID: user}})
	msg := h.opened(t, concord.KindMessage)
	thumb, blob, id := "👍", "blob", snowflake.ID(123)

	h.m.discordReaction(h.l, reacted{user: user, message: 500, emoji: discord.PartialEmoji{Name: &thumb}, add: true, name: "kit"})
	r := h.opened(t, concord.KindReaction)
	if r == nil || r.Author != h.v.Puppet.PK || r.Content != "👍" || concord.Tag(r.Tags, "e") != msg.RumorID ||
		concord.Tag(r.Tags, "k") != "9" || concord.Tag(r.Tags, "proxy") == "" {
		t.Fatalf("reaction %+v", r)
	}
	h.m.discordReaction(h.l, reacted{user: user, message: 500, emoji: discord.PartialEmoji{Name: &blob, ID: &id, Animated: true}, add: true})
	c := h.opened(t, concord.KindReaction)
	if c == nil || c.Content != ":blob:" || !slices.ContainsFunc(c.Tags, func(t []string) bool {
		return slices.Equal(t, []string{"emoji", "blob", "https://cdn.discordapp.com/emojis/123.gif"})
	}) {
		t.Fatalf("custom reaction %+v", c)
	}

	h.m.discordReaction(h.l, reacted{user: user, message: 500, emoji: discord.PartialEmoji{Name: &thumb}})
	d := h.opened(t, concord.KindDelete)
	if d == nil || concord.Tag(d.Tags, "e") != r.RumorID || concord.Tag(d.Tags, "k") != "7" {
		t.Fatalf("removal %+v", d)
	}
	// What has no row never crosses: a second removal, skua's own reaction,
	// and a reaction on a message from before the bridge.
	h.m.discordReaction(h.l, reacted{user: user, message: 500, emoji: discord.PartialEmoji{Name: &thumb}})
	h.m.discordReaction(h.l, reacted{user: 99, message: 500, emoji: discord.PartialEmoji{Name: &thumb}, add: true})
	h.m.discordReaction(h.l, reacted{user: user, message: 1, emoji: discord.PartialEmoji{Name: &thumb}, add: true})
	if evs := h.pool.take(); len(evs) != 0 {
		t.Fatalf("published %d more", len(evs))
	}

	// An Armada moderator's delete takes the member's reaction off Discord.
	mod, _ := h.chat(t, 1, concord.KindDelete, "", []string{"e", c.RumorID})
	h.deliver(t, mod)
	if want := fmt.Sprint("-500 blob:123 ", user); !slices.Equal(h.rest.reacts, []string{want}) {
		t.Fatalf("reacts %v", h.rest.reacts)
	}
}

func TestArmadaReactions(t *testing.T) {
	h := newHarness(t, "100="+general)
	w, id := h.chat(t, 3, concord.KindMessage, "mine")
	h.deliver(t, w) // posted as 1001
	react := func(b byte, content string, tags ...[]string) string {
		w, rid := h.chat(t, b, concord.KindReaction, content, append([][]string{{"e", id}}, tags...)...)
		h.deliver(t, w)
		return rid
	}
	mine := react(3, "🔥")
	theirs := react(6, "🔥") // shares skua's one reaction
	react(5, ":x:", []string{"emoji", "x", "https://example.com/x.png"})
	react(5, "lol")
	react(5, ":blob:", []string{"emoji", "blob", "https://cdn.discordapp.com/emojis/123.png"})
	if want := []string{"+1001 🔥", "+1001 blob:123"}; !slices.Equal(h.rest.reacts, want) {
		t.Fatalf("reacts %v", h.rest.reacts)
	}

	unreact := func(b byte, rid string) {
		w, _ := h.chat(t, b, concord.KindDelete, "", []string{"e", rid}, []string{"k", "7"})
		h.deliver(t, w)
	}
	unreact(0x42, mine) // a stranger
	unreact(3, mine)    // theirs still stands
	if len(h.rest.reacts) != 2 {
		t.Fatalf("reacts %v", h.rest.reacts)
	}
	unreact(6, theirs)
	if h.rest.reacts[2] != "-1001 🔥" {
		t.Fatalf("reacts %v", h.rest.reacts)
	}
	// The message itself is untouched by a reaction's delete.
	if len(h.post.deleted) != 0 {
		t.Fatal("a reaction's delete removed the message")
	}
}

func TestReactionEvents(t *testing.T) {
	h := newHarness(t, "100="+general)
	thumb := "👍"
	generic := func(ch snowflake.ID, name *string) *events.GenericGuildMessageReaction {
		return &events.GenericGuildMessageReaction{UserID: 5, ChannelID: ch, MessageID: 500, GuildID: 9, Emoji: discord.PartialEmoji{Name: name}}
	}
	h.m.OnEvent(&events.GuildMessageReactionAdd{GenericGuildMessageReaction: generic(100, &thumb), Member: discord.Member{User: discord.User{Bot: true}}})
	h.m.OnEvent(&events.GuildMessageReactionAdd{GenericGuildMessageReaction: generic(101, &thumb)})
	h.m.OnEvent(&events.GuildMessageReactionRemove{GenericGuildMessageReaction: generic(100, nil)})
	if len(h.l.toArm) != 0 {
		t.Fatal("queued a bot's, an unlinked or a nameless reaction")
	}
	nick := "wren"
	h.m.OnEvent(&events.GuildMessageReactionAdd{GenericGuildMessageReaction: generic(100, &thumb), Member: discord.Member{Nick: &nick}})
	h.m.OnEvent(&events.GuildMessageReactionRemove{GenericGuildMessageReaction: generic(100, &thumb)})
	add, remove := <-h.l.toArm, <-h.l.toArm
	if add.react == nil || !add.react.add || add.react.name != "wren" || add.react.user != 5 || remove.react == nil || remove.react.add {
		t.Fatalf("queued %+v %+v", add.react, remove.react)
	}
}

// A Discord moderator clearing reactions takes skua's shared one off, so
// its rows go too: the next Armada reactor of that emoji is shown again.
func TestDiscordClearsReactions(t *testing.T) {
	h := newHarness(t, "100="+general)
	w, id := h.chat(t, 3, concord.KindMessage, "mine")
	h.deliver(t, w) // posted as 1001
	react := func(b byte, content string) {
		w, _ := h.chat(t, b, concord.KindReaction, content, []string{"e", id})
		h.deliver(t, w)
	}
	run := func(ev bot.Event) {
		h.m.OnEvent(ev)
		job := <-h.l.toArm
		h.m.discordReaction(h.l, *job.react)
	}
	fire := "🔥"
	react(3, "🔥")
	react(3, "👀")
	run(&events.GuildMessageReactionRemoveEmoji{ChannelID: 100, MessageID: 1001, GuildID: 9, Emoji: discord.PartialEmoji{Name: &fire}})
	react(6, "🔥")
	react(6, "👀") // 👀 was not cleared, so it still stands
	run(&events.GuildMessageReactionRemoveAll{ChannelID: 100, MessageID: 1001, GuildID: 9})
	react(5, "👀")
	if want := []string{"+1001 🔥", "+1001 👀", "+1001 🔥", "+1001 👀"}; !slices.Equal(h.rest.reacts, want) {
		t.Fatalf("reacts %v", h.rest.reacts)
	}
	h.m.OnEvent(&events.GuildMessageReactionRemoveAll{ChannelID: 101, MessageID: 1001})
	if len(h.l.toArm) != 0 {
		t.Fatal("queued a clear in an unlinked channel")
	}
}

// A server's emoji go to Armada as skua's pack, under the names and URLs a
// bridged message's tags carry, and again only when they change.
func TestEmojiPack(t *testing.T) {
	h := newHarness(t, "100="+general)
	packs := func() []*nostr.Event {
		var out []*nostr.Event
		for _, ev := range h.pool.take() {
			if ev.Kind == kindEmojiPack {
				out = append(out, ev)
			}
		}
		return out
	}
	h.m.syncPacks(h.m.ctx) // the server refuses: nothing goes, nothing is remembered
	if len(packs()) != 0 {
		t.Fatal("published without the server's emoji")
	}
	h.rest.emojis = []discord.Emoji{
		{ID: 300, Name: "blob", Available: true, Animated: true},
		{ID: 200, Name: "blob", Available: true},
		{ID: 100, Name: "gone", Available: false},
	}
	no := false
	h.rest.stickers = []discord.Sticker{
		{ID: 600, Name: "blob", FormatType: discord.StickerFormatTypeGIF},
		{ID: 500, Name: "happy wumpus!", FormatType: discord.StickerFormatTypeAPNG},
		{ID: 700, Name: "spin", FormatType: discord.StickerFormatTypeLottie},
		{ID: 800, Name: "gone", FormatType: discord.StickerFormatTypePNG, Available: &no},
		{ID: 900, Name: "?", FormatType: discord.StickerFormatTypePNG},
	}
	h.m.syncPacks(h.m.ctx)
	got := packs()
	if len(got) != 1 || got[0].PubKey != h.m.primary.PK {
		t.Fatalf("packs %+v", got)
	}
	// Stickers follow the emoji, never taking a name one has.
	want := nostr.Tags{
		{"d", "discord-7"}, {"title", "the cove emoji"}, {"image", "https://cdn.discordapp.com/icons/7/abc.png?size=256"},
		{"emoji", "blob", "https://cdn.discordapp.com/emojis/200.png"},
		{"emoji", "blob_2", "https://cdn.discordapp.com/emojis/300.gif"},
		{"emoji", "happy_wumpus_", "https://media.discordapp.net/stickers/500.png"},
		{"emoji", "blob_3", "https://media.discordapp.net/stickers/600.gif"},
		{"emoji", "sticker", "https://media.discordapp.net/stickers/900.png"},
	}
	if fmt.Sprint(got[0].Tags) != fmt.Sprint(want) {
		t.Fatalf("tags %v", got[0].Tags)
	}
	h.m.syncPacks(h.m.ctx)
	if len(packs()) != 0 {
		t.Fatal("published an unchanged pack")
	}
	h.rest.emojis = h.rest.emojis[1:]
	h.m.syncPacks(h.m.ctx)
	if len(packs()) != 1 {
		t.Fatal("a changed pack was not published")
	}

	// Each puppet's palette names the pack, once.
	user, _ := snowflake.Parse(h.v.Puppet.User)
	lists := func() (n int) {
		for _, ev := range h.pool.take() {
			if ev.Kind == kindEmojiList && ev.PubKey == h.v.Puppet.PK && ev.Tags.Find("a")[1] == "30030:"+h.m.primary.PK+":discord-7" {
				n++
			}
		}
		return n
	}
	h.m.toArmada(h.l, discord.Message{ID: 1, ChannelID: 100, Content: "a", Author: discord.User{ID: user}})
	h.m.toArmada(h.l, discord.Message{ID: 2, ChannelID: 100, Content: "b", Author: discord.User{ID: user}})
	if n := lists(); n != 1 {
		t.Fatalf("%d palettes", n)
	}
}

// Whatever name an Armada palette gave a Discord emoji, it crosses back as
// that emoji: Discord draws it by id.
func TestEmojiNamesCrossBack(t *testing.T) {
	cdn := "https://cdn.discordapp.com/emojis/123.png"
	for in, want := range map[string]string{
		"blob":            "blob",
		"discord-7-blob":  "blob",
		"blob_2":          "blob_2",
		"pack-x":          "emoji",
		"discord-7-blob-": "emoji",
	} {
		text := toDiscord("hi :"+in+":", [][]string{{"emoji", in, cdn}})
		if text != "hi <:"+want+":123>" {
			t.Errorf("%s: %s", in, text)
		}
		if got, ok := armadaEmoji(":"+in+":", [][]string{{"emoji", in, cdn}}); !ok || got != want+":123" {
			t.Errorf("%s: %s %v", in, got, ok)
		}
	}
	for _, bad := range []string{"a", "has space", "dot.ted", strings.Repeat("a", 97)} {
		if text := toDiscord(":"+bad+":", [][]string{{"emoji", bad, cdn}}); text != ":"+bad+":" {
			t.Errorf("%q became %s", bad, text)
		}
	}
}

// A sticker crosses as its image at the URL its pack entry has, so a
// sticker alone is no longer an empty message, and a Lottie one Armada
// can't draw crosses as its name.
func TestDiscordStickers(t *testing.T) {
	h := newHarness(t, "100="+general)
	user, _ := snowflake.Parse(h.v.Puppet.User)
	h.m.toArmada(h.l, discord.Message{ID: 1, ChannelID: 100, Author: discord.User{ID: user}, StickerItems: []discord.MessageSticker{
		{ID: 600, Name: "blob", FormatType: discord.StickerFormatTypeGIF},
		{ID: 700, Name: "spin", FormatType: discord.StickerFormatTypeLottie},
	}})
	o := h.opened(t, concord.KindMessage)
	url := "https://media.discordapp.net/stickers/600.gif"
	if o == nil || o.Content != "sticker: spin\n"+url {
		t.Fatalf("published %+v", o)
	}
	if !slices.ContainsFunc(o.Tags, func(t []string) bool {
		return slices.Equal(t, []string{"imeta", "url " + url, "m image/gif"})
	}) {
		t.Errorf("tags %v", o.Tags)
	}
}
