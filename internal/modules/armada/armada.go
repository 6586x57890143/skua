// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

// Package armada bridges chosen Discord channels with chosen channels of
// one Armada community, both ways: text, replies, deletes and attachments.
// The two servers keep their own layouts; only the pairs in
// SKUA_ARMADA_LINKS cross.
//
// A Discord member speaks in Armada as a puppet, a key derived from the
// master secret and their user id, so they keep one identity and an Armada
// moderator can ban one of them alone. An Armada member speaks in Discord
// through skua's webhook wearing their kind 0 name and avatar.
//
// Bridged messages leave end-to-end encryption: anything crossing into
// Discord is plaintext to Discord.
//
// Loops are stopped three ways. Everything skua publishes carries a proxy
// tag and any rumor with one is dropped. Rumors by skua's own key or a
// puppet are dropped, as are Discord posts by any bot or webhook. And a
// rumor already mapped to a Discord message is never posted again.
package armada

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/concord"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

const (
	// reresolve is how often the invite is read again, which is how a root
	// rotation (after a ban, say) reaches skua.
	reresolve = 10 * time.Minute
	// queueCap is how many messages may wait for one link in one
	// direction. A burst never reaches it; a flood is shed rather than
	// grow the heap, and the relay copy is still the record.
	queueCap = 200
	// refoldAfter batches control wraps arriving together into one fold.
	refoldAfter = 250 * time.Millisecond
)

// Config is the bridge's environment. Every field is a secret or close to
// one, so none is ever logged.
type Config struct {
	Invite  string // SKUA_ARMADA_INVITE
	Master  string // SKUA_ARMADA_MASTER_SECRET, 64 hex
	Primary string // SKUA_ARMADA_PRIMARY_KEY, hex or nsec
	Links   string // SKUA_ARMADA_LINKS, <discord channel>=<armada channel>,...
	Blossom string // SKUA_ARMADA_BLOSSOM, comma-separated, in order
}

// FromEnv reads the bridge's environment.
func FromEnv() Config {
	return Config{
		Invite:  os.Getenv("SKUA_ARMADA_INVITE"),
		Master:  os.Getenv("SKUA_ARMADA_MASTER_SECRET"),
		Primary: os.Getenv("SKUA_ARMADA_PRIMARY_KEY"),
		Links:   os.Getenv("SKUA_ARMADA_LINKS"),
		Blossom: os.Getenv("SKUA_ARMADA_BLOSSOM"),
	}
}

// Configured is whether there is a bridge to run at all.
func (c Config) Configured() bool { return c.Invite != "" }

// fromDiscord is a Discord message on its way over: new, or an edit.
type fromDiscord struct {
	msg  discord.Message
	edit bool
}

type link struct {
	discord snowflake.ID
	armada  string
	guild   atomic.Uint64 // learned at boot, or from the first message
	tally   tally
	toArm   chan fromDiscord
	toDisc  chan *concord.Opened
}

// relays is the slice of concord.Pool the bridge uses.
type relays interface {
	Query(ctx context.Context, f nostr.Filter) []*nostr.Event
	Publish(ctx context.Context, ev *nostr.Event) error
	Subscribe(ctx context.Context, f nostr.Filter, onEvent func(*nostr.Event))
	Register(keys ...concord.StreamKey)
	Close()
}

type poster interface {
	Send(ctx context.Context, r rest.Rest, guild, channel, app snowflake.ID, msg discord.WebhookMessageCreate) (*discord.Message, error)
	Delete(ctx context.Context, r rest.Rest, guild, channel, app, webhookID, message snowflake.ID) error
	Edit(ctx context.Context, r rest.Rest, guild, channel, app, webhookID, message snowflake.ID, update discord.WebhookMessageUpdate) error
}

type screen interface {
	Check(text string) filter.Verdict
}

// Module is the bridge.
type Module struct {
	log     *slog.Logger
	guard   *guard.Guard
	post    poster
	screen  screen
	maps    mappings
	invite  concord.Invite
	master  []byte
	primary concord.Key
	links   []*link
	byDisc  map[snowflake.ID]*link
	blob    *blossom
	fetcher *http.Client
	dial    func(urls []string) relays
	gate    func(guild snowflake.ID) bool

	ctx context.Context
	// wake reads the invite again early: a Direct Invite has arrived.
	wake chan struct{}
	// wakeGap is the least time between two wake-driven reads.
	wakeGap time.Duration
	stop    context.CancelFunc
	start   sync.Once
	rest    rest.Rest
	app     snowflake.ID

	mu       sync.RWMutex
	comm     *concord.Community
	folded   *concord.Folded
	chans    map[string]concord.ChatChannel // by link's armada channel
	pool     relays
	subs     map[string]context.CancelFunc // chat subscription per link
	subKeys  map[string]string             // the authors it was opened with
	missing  map[string]bool               // links already logged as unreadable
	failing  string                        // why the invite last failed to read, or ""
	puppets  map[string]concord.Key        // discord user id -> puppet
	puppetPK map[string]bool
	profiles map[string]profile         // armada pubkey -> name and avatar
	synced   map[string]string          // discord user id -> profile published
	joined   map[string]bool            // discord user ids whose puppet joined
	seen     map[string]bool            // rumor ids already queued
	inbox    map[string]bool            // inbox wraps that have woken run
	edited   map[snowflake.ID]time.Time // the last Discord edit sent, per message
	lastEdit map[string]int64           // the newest Armada edit applied, per rumor
}

type profile struct {
	name, avatar string
	at           time.Time
}

// New checks cfg and builds the bridge; it connects to nothing until
// Discord says skua is ready. db may be nil, and then the mappings live in
// memory.
func New(log *slog.Logger, g *guard.Guard, p poster, s screen, db DB, cfg Config) (*Module, error) {
	inv, err := concord.ParseInvite(cfg.Invite)
	if err != nil {
		return nil, errors.New("armada: SKUA_ARMADA_INVITE is not an armada invite link")
	}
	master, err := hex.DecodeString(cfg.Master)
	if err != nil || len(master) != 32 || strings.ToLower(cfg.Master) != cfg.Master {
		return nil, errors.New("armada: SKUA_ARMADA_MASTER_SECRET must be 64 lowercase hex characters")
	}
	primary, err := primaryKey(cfg.Primary)
	if err != nil {
		return nil, err
	}
	links, err := parseLinks(cfg.Links)
	if err != nil {
		return nil, err
	}
	m := &Module{
		log: log, guard: g, post: p, screen: s, invite: inv, master: master, primary: primary,
		links: links, byDisc: map[snowflake.ID]*link{}, fetcher: guarded,
		dial: func(urls []string) relays { return concord.NewPool(urls) },
		subs: map[string]context.CancelFunc{}, subKeys: map[string]string{}, missing: map[string]bool{},
		puppets: map[string]concord.Key{}, puppetPK: map[string]bool{}, profiles: map[string]profile{},
		synced: map[string]string{}, joined: map[string]bool{}, seen: map[string]bool{}, inbox: map[string]bool{}, edited: map[snowflake.ID]time.Time{}, lastEdit: map[string]int64{},
	}
	m.maps = &memMappings{}
	if db != nil {
		m.maps = pgMappings{db}
	}
	if servers := splitList(cfg.Blossom); len(servers) > 0 {
		m.blob = &blossom{servers: servers, http: &http.Client{Timeout: time.Minute}}
	}
	for _, l := range links {
		m.byDisc[l.discord] = l
	}
	m.wake = make(chan struct{}, 1)
	m.wakeGap = time.Minute
	m.ctx, m.stop = context.WithCancel(context.Background())
	return m, nil
}

func primaryKey(v string) (concord.Key, error) {
	bad := errors.New("armada: SKUA_ARMADA_PRIMARY_KEY must be a secret key in hex or nsec")
	if strings.HasPrefix(v, "nsec1") {
		_, sk, err := nip19.Decode(v)
		s, ok := sk.(string)
		if err != nil || !ok {
			return concord.Key{}, bad
		}
		v = s
	}
	b, err := hex.DecodeString(v)
	if err != nil || len(b) != 32 {
		return concord.Key{}, bad
	}
	k, err := concord.KeyFromSecret([32]byte(b))
	if err != nil {
		return concord.Key{}, bad
	}
	return k, nil
}

var hex64 = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// parseLinks reads the channel map. Each side may appear once: the proxy
// tag drops bridged rumors, so one Armada channel feeding two Discord
// channels would quietly never carry a post from one to the other.
//
// ponytail: one-to-one only; fan-out needs the Discord side to relay to its
// siblings directly instead of through Armada.
func parseLinks(v string) ([]*link, error) {
	var out []*link
	seenD, seenA := map[snowflake.ID]bool{}, map[string]bool{}
	for _, pair := range splitList(v) {
		d, a, ok := strings.Cut(pair, "=")
		id, err := snowflake.Parse(strings.TrimSpace(d))
		a = strings.ToLower(strings.TrimSpace(a))
		if !ok || err != nil || !hex64.MatchString(a) {
			return nil, fmt.Errorf("armada: SKUA_ARMADA_LINKS entry %q is not <discord channel id>=<armada channel id>", pair)
		}
		if seenD[id] || seenA[a] {
			return nil, fmt.Errorf("armada: SKUA_ARMADA_LINKS names a channel twice (%q); each side may be in one pair", pair)
		}
		seenD[id], seenA[a] = true, true
		out = append(out, &link{discord: id, armada: a, toArm: make(chan fromDiscord, queueCap), toDisc: make(chan *concord.Opened, queueCap)})
	}
	if len(out) == 0 {
		return nil, errors.New("armada: SKUA_ARMADA_LINKS is empty; name at least one <discord channel id>=<armada channel id>")
	}
	return out, nil
}

func splitList(v string) []string {
	var out []string
	for s := range strings.SplitSeq(v, ",") {
		if s = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), "/")); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (*Module) Name() string { return "armada" }

// Want is guild messages and their content: the bridge reads what members
// write in the linked channels and nowhere else.
func (*Module) Want() intents.Want {
	return intents.Want{Required: gateway.IntentGuildMessages | gateway.IntentMessageContent}
}

// Perms is the webhook Armada members speak through, and deleting a
// member's message when an Armada moderator deletes its copy there.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionManageWebhooks | discord.PermissionManageMessages
}

func (*Module) Commands() []core.Command { return nil }

// Help is armada's page in /help.
func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorInfo,
		Line:  "carries chosen channels to and from an armada community",
		About: "she carries messages both ways between a discord channel and an armada channel and posts each one under its writer's name. edits and files cross too and armada's bans and deletes hold on this side. anything that crosses into discord is no longer end-to-end encrypted. which channels pair up is set when she is deployed. this part of her is under the agpl and her source is at https://github.com/6586x57890143/skua",
	}
}

// Gate is how the bridge learns a server turned it off, so it stops
// posting there too.
func (m *Module) Gate(on func(guild snowflake.ID) bool) { m.gate = on }

// Close stops every goroutine and socket the bridge started.
func (m *Module) Close() {
	m.stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pool != nil {
		m.pool.Close()
	}
}

// OnEvent starts the bridge when skua is ready and hands it the linked
// channels' messages. It returns at once; the work is queued per link.
func (m *Module) OnEvent(ev bot.Event) {
	switch e := ev.(type) {
	case *events.Ready:
		m.start.Do(func() {
			m.rest, m.app = e.Client().Rest, e.Client().ApplicationID
			for _, l := range m.links {
				go m.discordWorker(l)
				go m.armadaWorker(l)
			}
			go m.run()
		})
	case *events.GuildMessageCreate:
		m.queue(e.GenericGuildMessage, false)
	case *events.GuildMessageUpdate:
		m.queue(e.GenericGuildMessage, true)
	case *events.GuildMessageDelete:
		if l, ok := m.byDisc[e.ChannelID]; ok {
			go m.discordDelete(l, e.GuildID, e.MessageID)
		}
	}
}

// queue hands a member's message in a linked channel to its link's worker,
// in order, so an edit never overtakes the message it changes.
func (m *Module) queue(e *events.GenericGuildMessage, edit bool) {
	l, ok := m.byDisc[e.ChannelID]
	msg := e.Message
	if !ok || msg.Author.Bot || msg.WebhookID != nil || (msg.Type != discord.MessageTypeDefault && msg.Type != discord.MessageTypeReply) {
		return
	}
	l.guild.Store(uint64(e.GuildID))
	select {
	case l.toArm <- fromDiscord{msg, edit}:
	default:
		m.log.Warn("armada: dropping a discord message, the queue is full", "channel", l.discord)
	}
}

// run keeps a session on the community and starts a new one when the
// invite says its keys moved.
func (m *Module) run() {
	m.learnGuilds()
	var current string
	end := func() {}
	defer func() { end() }()
	for {
		last := time.Now()
		c, err := m.resolve()
		m.mu.Lock()
		m.failing = ""
		if err != nil {
			m.failing = strings.TrimPrefix(err.Error(), "concord: ")
		}
		m.mu.Unlock()
		switch {
		case err != nil:
			m.log.Warn("armada: reading the invite", "err", err)
		case fingerprint(c) != current:
			end()
			ctx, cancel := context.WithCancel(m.ctx)
			end = cancel
			current = fingerprint(c)
			m.session(ctx, c)
		}
		select {
		case <-m.ctx.Done():
			return
		case <-time.After(reresolve):
		case <-m.wake:
			// Anyone can address a wrap to skua, so a wake is a hint, not an
			// order: at most one wake-driven read a wakeGap, and a burst of
			// wraps meanwhile is one more read, not one each. A real grant
			// can wait that long.
			if wait := time.Until(last.Add(m.wakeGap)); wait > 0 {
				select {
				case <-m.ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}
	}
}

// learnGuilds finds each linked channel's server, so Armada posts can go
// out before anyone in Discord has spoken.
func (m *Module) learnGuilds() {
	for _, l := range m.links {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		ch, err := m.rest.GetChannel(l.discord, rest.WithCtx(ctx))
		cancel()
		if gc, ok := ch.(discord.GuildChannel); err == nil && ok {
			l.guild.Store(uint64(gc.GuildID()))
			continue
		}
		m.log.Warn("armada: can't see a linked discord channel; posts into it wait for someone to speak there", "channel", l.discord, "err", err)
	}
}

// resolve reads the invite, then the Direct Invites addressed to skua: a
// private channel granted to her after the invite was made, or a key that
// rotated with her kept in it, arrives that way. Those are read from the
// community's relays and the stock ones, where a sender puts them before
// skua has said where her inbox is.
func (m *Module) resolve() (*concord.Community, error) {
	boot := m.dial(m.invite.Bootstrap)
	c, err := concord.Resolve(m.ctx, m.invite, boot)
	boot.Close()
	if err != nil {
		return nil, err
	}
	p := m.dial(append(slices.Clone(c.Relays), concord.StockRelays...))
	defer p.Close()
	control := concord.ControlStream(c)
	p.Register(control)
	f := concord.FoldControl(c, p.Query(m.ctx, nostr.Filter{Kinds: []int{concord.KindWrap}, Authors: []string{control.PK}}))
	if learned := concord.Intake(m.ctx, p, m.primary.SK, c, f); len(learned) > 0 {
		m.log.Debug("armada: keys from a direct invite", "channels", len(learned))
	}
	return c, nil
}

// fingerprint is what changing would need a new session: the root, its
// epoch, the control address and the private keys held.
func fingerprint(c *concord.Community) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%x/%d/%s", c.Root, c.RootEpoch, c.ControlPK)
	for id, k := range c.Private {
		fmt.Fprintf(&b, "/%s:%x:%d", id, k.Key, k.Epoch)
	}
	return b.String()
}

// session folds the community's Control Plane, opens the linked channels'
// streams and keeps the fold live, all until ctx ends.
func (m *Module) session(ctx context.Context, c *concord.Community) {
	pool := m.dial(c.Relays)
	control := concord.ControlStream(c)
	pool.Register(control, concord.GuestbookKey(c))
	wraps := map[string]*nostr.Event{}
	for _, w := range pool.Query(ctx, nostr.Filter{Kinds: []int{concord.KindWrap}, Authors: []string{control.PK}}) {
		wraps[w.ID] = w
	}
	m.mu.Lock()
	if m.pool != nil {
		m.pool.Close()
	}
	m.pool, m.comm = pool, c
	clear(m.subKeys)
	for _, cancel := range m.subs {
		cancel()
	}
	clear(m.subs)
	m.mu.Unlock()
	m.apply(ctx, c, concord.FoldControl(c, slicesOf(wraps)))
	go m.announce(ctx, pool, c)

	// The REQ has no since: every relay replays the whole plane, so wraps
	// are gathered and folded once per batch, not once each.
	var mu sync.Mutex
	var pending *time.Timer
	pool.Subscribe(ctx, nostr.Filter{Kinds: []int{concord.KindWrap}, Authors: []string{control.PK}}, func(w *nostr.Event) {
		mu.Lock()
		defer mu.Unlock()
		if wraps[w.ID] != nil {
			return
		}
		wraps[w.ID] = w
		if pending == nil {
			pending = time.AfterFunc(refoldAfter, func() {
				mu.Lock()
				pending = nil
				all := slicesOf(wraps)
				mu.Unlock()
				if ctx.Err() == nil {
					m.apply(ctx, c, concord.FoldControl(c, all))
				}
			})
		}
	})
	// A grant to skua lands in her inbox; read it now, not on the next tick.
	// Senders backdate a gift wrap by up to two days on purpose, so the floor
	// is three days back; what that replays on a reconnect is one capped read.
	since := nostr.Now() - 3*24*60*60
	pool.Subscribe(ctx, nostr.Filter{Kinds: []int{concord.KindWrap}, Tags: nostr.TagMap{"p": {m.primary.PK}, "k": {"3313"}}, Since: &since}, func(w *nostr.Event) {
		// Only a wrap not seen before: a new session, or a second relay,
		// replays the ones already read.
		m.mu.Lock()
		fresh := !m.inbox[w.ID]
		if fresh {
			if len(m.inbox) >= 4096 {
				clear(m.inbox)
			}
			m.inbox[w.ID] = true
		}
		m.mu.Unlock()
		if !fresh {
			return
		}
		select {
		case m.wake <- struct{}{}:
		default:
		}
	})
	m.log.Info("armada: bridge is up", "links", len(m.links))
}

func slicesOf(m map[string]*nostr.Event) []*nostr.Event {
	out := make([]*nostr.Event, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// apply takes a new fold: bans and moderators change at once, and a link
// whose channel keys changed is subscribed again at the new streams.
func (m *Module) apply(ctx context.Context, c *concord.Community, f *concord.Folded) {
	chans := concord.Channels(c, f)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.folded, m.chans = f, chans
	for _, l := range m.links {
		ch, ok := chans[l.armada]
		if !ok {
			if !m.missing[l.armada] {
				m.log.Warn("armada: a linked armada channel is missing, deleted or private without a key", "armada", l.armada[:8])
				m.missing[l.armada] = true
			}
			continue
		}
		delete(m.missing, l.armada)
		keys := strings.Join(ch.Authors(), ",")
		if m.subKeys[l.armada] == keys {
			continue
		}
		if cancel := m.subs[l.armada]; cancel != nil {
			cancel()
		}
		sctx, cancel := context.WithCancel(ctx)
		m.subs[l.armada] = cancel
		m.subKeys[l.armada] = keys
		for _, s := range ch.Streams {
			m.pool.Register(s.Key.Stream())
		}
		// Live tail only: without a floor, relays replay the history into
		// Discord on every subscribe. A minute of overlap absorbs skew.
		since := nostr.Now() - 60
		m.pool.Subscribe(sctx, nostr.Filter{Kinds: []int{concord.KindWrap}, Authors: ch.Authors(), Since: &since}, func(w *nostr.Event) {
			m.onWrap(l, w)
		})
	}
}

// announce publishes skua's own profile, marked as a bot, and its join.
// Failures cost nothing but how she shows in Armada's member list.
func (m *Module) announce(ctx context.Context, pool relays, c *concord.Community) {
	meta := map[string]any{"name": "skua", "about": "bridges channels of this community with a discord server", "bot": true}
	// She looks the same on both sides: her Discord avatar and banner, read
	// once a session. Without them the profile still goes, just plain.
	if me, err := m.rest.GetCurrentUser("", rest.WithCtx(ctx)); err == nil {
		meta["picture"] = me.EffectiveAvatarURL(discord.WithSize(1024))
		if b := me.BannerURL(discord.WithSize(1024)); b != nil {
			meta["banner"] = *b
		}
	} else {
		m.log.Warn("armada: reading skua's discord profile", "err", err)
	}
	raw, _ := json.Marshal(meta)
	ev := &nostr.Event{Kind: 0, Content: string(raw), CreatedAt: nostr.Now(), Tags: nostr.Tags{}}
	if err := ev.Sign(m.primary.SK); err == nil {
		if err := pool.Publish(ctx, ev); err != nil {
			m.log.Warn("armada: publishing skua's profile", "err", err)
		}
	}
	// Where skua's inbox is, so a Direct Invite to her goes to the community's
	// relays rather than the stock ones.
	if ev, err := concord.DMRelays(c.Relays, m.primary.SK); err == nil {
		if err := pool.Publish(ctx, ev); err != nil {
			m.log.Warn("armada: publishing skua's inbox relays", "err", err)
		}
	}
	if j, err := concord.Join(c, m.primary.PK, m.primary.SK, time.Now().UnixMilli()); err == nil {
		if err := pool.Publish(ctx, j); err != nil {
			m.log.Warn("armada: publishing skua's join", "err", err)
		}
	}
}

// state is the current session, read once per message.
func (m *Module) state() (relays, *concord.Community, *concord.Folded, map[string]concord.ChatChannel) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pool, m.comm, m.folded, m.chans
}

// onWrap opens a wrap for its link and queues it for Discord, once.
func (m *Module) onWrap(l *link, w *nostr.Event) {
	_, _, _, chans := m.state()
	ch, ok := chans[l.armada]
	if !ok {
		return
	}
	o, err := concord.OpenChat(w, ch)
	if err != nil {
		return
	}
	m.mu.Lock()
	dup := m.seen[o.RumorID]
	if !dup {
		if len(m.seen) >= 65_536 {
			clear(m.seen) // re-delivery after this is caught by the mappings
		}
		m.seen[o.RumorID] = true
	}
	m.mu.Unlock()
	if dup {
		return
	}
	select {
	case l.toDisc <- o:
	default:
		m.log.Warn("armada: dropping an armada message, the queue is full", "channel", l.discord)
	}
}

func (m *Module) discordWorker(l *link) {
	for {
		select {
		case <-m.ctx.Done():
			return
		case job := <-l.toArm:
			if job.edit {
				m.discordEdit(l, job.msg)
			} else {
				m.toArmada(l, job.msg)
			}
		}
	}
}

func (m *Module) armadaWorker(l *link) {
	for {
		select {
		case <-m.ctx.Done():
			return
		case o := <-l.toDisc:
			m.toDiscord(l, o)
		}
	}
}

// puppet is the Discord user's Armada key: HKDF of the master secret and
// their id, the same derivation as upstream's bridge, so a puppet keeps its
// npub across bridges run from the same secret. Nothing per user is stored.
func (m *Module) puppet(user string) concord.Key {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k, ok := m.puppets[user]; ok {
		return k
	}
	k := derivePuppet(m.master, user)
	if len(m.puppets) >= 4096 {
		clear(m.puppets) // the pubkey set stays: it is the loop check
	}
	m.puppets[user] = k
	m.puppetPK[k.PK] = true
	return k
}

func (m *Module) isPuppet(pk string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.puppetPK[pk]
}
