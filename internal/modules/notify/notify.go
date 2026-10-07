// Package notify is /notify: a card in a channel when an account a server
// follows posts on youtube, x or tiktok, or goes live on youtube, twitch or
// kick. Each account is polled once however many servers follow it, and a
// platform that breaks only stops its own cards.
//
// youtube needs nothing: its feeds and pages are public. x and tiktok come
// through an RSSHub instance (SKUA_NOTIFY_RSSHUB), since neither has an API
// a bot can use for free. twitch and kick take a developer app's client
// credentials. A platform without its setting isn't offered.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// perGuild is the most follows one server keeps: one select menu's worth,
// so the panel can always offer every one of them to remove.
const perGuild = 25

// Config is what the platforms beyond youtube need. Empty leaves them out.
type Config struct {
	RSSHub                 string
	TwitchID, TwitchSecret string
	KickID, KickSecret     string
}

func FromEnv() Config {
	return Config{
		RSSHub:   strings.TrimRight(os.Getenv("SKUA_NOTIFY_RSSHUB"), "/"),
		TwitchID: os.Getenv("SKUA_TWITCH_CLIENT_ID"), TwitchSecret: os.Getenv("SKUA_TWITCH_CLIENT_SECRET"),
		KickID: os.Getenv("SKUA_KICK_CLIENT_ID"), KickSecret: os.Getenv("SKUA_KICK_CLIENT_SECRET"),
	}
}

// sources is every platform cfg sets up, against the real hosts.
func sources(cfg Config, c *http.Client) map[string]source {
	s := map[string]source{"youtube": youtube{c: c, base: "https://www.youtube.com", img: "https://i.ytimg.com"}}
	if cfg.RSSHub != "" {
		s["x"] = feed{c: c, base: cfg.RSSHub, path: func(h string) string { return "/twitter/user/" + h }}
		s["tiktok"] = feed{c: c, base: cfg.RSSHub, path: func(h string) string { return "/tiktok/user/@" + h }}
	}
	if cfg.TwitchID != "" && cfg.TwitchSecret != "" {
		s["twitch"] = &twitch{app: &app{c: c, url: "https://id.twitch.tv/oauth2/token", id: cfg.TwitchID, secret: cfg.TwitchSecret}, api: "https://api.twitch.tv/helix"}
	}
	if cfg.KickID != "" && cfg.KickSecret != "" {
		s["kick"] = &kick{app: &app{c: c, url: "https://id.kick.com/oauth/token", id: cfg.KickID, secret: cfg.KickSecret}, api: "https://api.kick.com/public/v1"}
	}
	return s
}

// health is how a platform's last round went.
type health struct {
	ok  time.Time // the last round anything came back
	err error     // the last round's failures
}

type Module struct {
	log     *slog.Logger
	guard   *guard.Guard
	db      DB
	sources map[string]source
	on      func(guild snowflake.ID) bool
	admin   func(discord.Interaction) bool
	now     func() time.Time
	boot    sync.Once

	mu      sync.Mutex
	follows []follow
	seen    map[key][]string
	bound   map[snowflake.ID]snowflake.ID // guild -> where its cards go
	health  map[string]health
	failed  map[snowflake.ID]string // channel -> why its last post failed
}

// New reads what is followed from db, which may be nil. admin is the
// router's rule, which the panel's components check again on every click.
func New(ctx context.Context, log *slog.Logger, g *guard.Guard, db DB, cfg Config, admin func(discord.Interaction) bool) (*Module, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := load(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("notify: loading follows: %w", err)
	}
	return &Module{
		log: log, guard: g, db: db, sources: sources(cfg, &http.Client{Timeout: 20 * time.Second}),
		on: func(snowflake.ID) bool { return true }, admin: admin, now: time.Now,
		follows: st.follows, seen: st.seen, bound: st.bound, health: map[string]health{}, failed: map[snowflake.ID]string{},
	}, nil
}

func (*Module) Name() string { return "notify" }

// Want is guilds, for leaving one.
func (*Module) Want() intents.Want { return intents.Want{Required: gateway.IntentGuilds} }

// Perms is posting the cards.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionSendMessages
}

// Gate is core.Gated: a server with notify off gets no cards.
func (m *Module) Gate(on func(guild snowflake.ID) bool) { m.on = on }

func (m *Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorNotice,
		Line:  "tells a channel when someone posts or goes live",
		About: "she keeps an eye on accounts across " + strings.Join(m.platforms(), ", ") + " and drops a card in this server's notify channel when one posts or goes live, pinging a role if you give her one. the first look at an account only learns what's already there, so nobody gets a backlog. admins run /notify to pick the channel and add or drop who's followed",
	}
}

// order is how the platforms are listed; any other sorts after them.
var order = []string{"youtube", "twitch", "kick", "x", "tiktok"}

// platforms is what this skua is set up for, in order.
func (m *Module) platforms() []string {
	out := slices.Sorted(maps.Keys(m.sources))
	rank := func(p string) int {
		if i := slices.Index(order, p); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortStableFunc(out, func(a, b string) int { return rank(a) - rank(b) })
	return out
}

// forget drops what was seen of an account nobody follows any more, so a
// follow later starts from a fresh baseline.
func (m *Module) forget(ctx context.Context, keys ...key) {
	m.mu.Lock()
	var gone []key
	for _, k := range keys {
		if !slices.ContainsFunc(m.follows, func(f follow) bool { return f.platform == k.platform && f.account == k.account }) {
			delete(m.seen, k)
			gone = append(gone, k)
		}
	}
	m.mu.Unlock()
	for _, k := range gone {
		if err := saveSeen(ctx, m.db, k, nil); err != nil {
			m.log.Warn("notify: forgetting an account", "account", k.account, "err", err)
		}
	}
}

// OnEvent starts the polling once skua is up, and forgets a server she
// leaves.
func (m *Module) OnEvent(ev bot.Event) {
	switch e := ev.(type) {
	case *events.Ready:
		m.boot.Do(func() { m.start(context.Background(), e.Client().Rest) })
	case *events.GuildLeave:
		go m.leave(e.GuildID)
	}
}

// start runs a loop per platform for the life of the process.
func (m *Module) start(ctx context.Context, p Poster) {
	for platform, src := range m.sources {
		go m.loop(ctx, p, platform, src)
	}
}

func (m *Module) leave(guild snowflake.ID) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := dropGuild(ctx, m.db, guild); err != nil {
		m.log.Warn("notify: forgetting a server", "guild", guild, "err", err)
	}
	m.mu.Lock()
	delete(m.bound, guild)
	var keys []key
	m.follows = slices.DeleteFunc(m.follows, func(f follow) bool {
		if f.guild == guild {
			keys = append(keys, key{f.platform, f.account})
		}
		return f.guild == guild
	})
	m.mu.Unlock()
	m.forget(ctx, keys...)
}

// Report is core.Reporter: what this server follows, how each platform it
// uses is doing, and any channel its cards can't reach.
func (m *Module) Report(guild snowflake.ID) core.Report {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, used := 0, map[string]bool{}
	var r core.Report
	for _, f := range m.follows {
		if f.guild != guild {
			continue
		}
		n++
		used[f.platform] = true
		to := m.dest(f)
		note := "! no channel for cards yet; pick one with /notify"
		if why, ok := m.failed[to]; ok {
			note = "! <#" + to.String() + ">: " + why
		}
		if (to == 0 || m.failed[to] != "") && !slices.Contains(r.Notes, note) {
			r.Notes = append(r.Notes, note)
		}
	}
	if n == 0 {
		return core.Report{}
	}
	r.Rows = append(r.Rows, [2]string{"following", strconv.Itoa(n)})
	for _, p := range slices.Sorted(maps.Keys(used)) {
		h := m.health[p]
		state := "not checked yet"
		switch {
		case h.err != nil && h.ok.IsZero():
			state = "failing"
		case h.err != nil:
			state = "failing, last ok " + core.Duration(m.now().Sub(h.ok)) + " ago"
		case !h.ok.IsZero():
			state = "ok " + core.Duration(m.now().Sub(h.ok)) + " ago"
		}
		r.Rows = append(r.Rows, [2]string{p, state})
	}
	return r
}
