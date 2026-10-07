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

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/nbd-wtf/go-nostr"

	"github.com/6586x57890143/skua/internal/concord"
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
func (f *fakeRelays) Close()                        {}

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
}

func (r *fakeRest) GetChannel(id snowflake.ID, _ ...rest.RequestOpt) (discord.Channel, error) {
	return discord.GuildTextChannel{}, nil
}

func (r *fakeRest) DeleteMessage(_, id snowflake.ID, _ ...rest.RequestOpt) error {
	r.deleted = append(r.deleted, id)
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
	// The control plane, skua's inbox and both readable linked channels: the
	// private one is readable because the invite granted its key. The third
	// is not in the community and is skipped by name.
	if len(h.pool.subs) != 4 || !slices.ContainsFunc(h.pool.subs, func(f nostr.Filter) bool {
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
	if len(h.pool.subs) != 4 {
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
	r, _ := h.chat(t, 5, concord.KindMessage, strings.Repeat("long ", 900), []string{"q", id, "", mod})
	h.deliver(t, r)
	if len(h.post.sent) != 4 {
		t.Fatalf("a 4,500 character message became %d posts", len(h.post.sent)-1)
	}
	if e := h.post.sent[1].Embeds; len(e) != 1 || !strings.HasPrefix(e[0].Description, "replying to sooty tern ([view message](https://discord.com/channels/7/100/1001))") {
		t.Errorf("reply %+v", e)
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
