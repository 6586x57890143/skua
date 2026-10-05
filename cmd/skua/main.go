// Command skua is the bot. Wiring only: everything with a decision in it
// lives in a package with a test.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // served only when SKUA_PPROF names a loopback address
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
	"github.com/6586x57890143/skua/internal/modules/bird"
	"github.com/6586x57890143/skua/internal/modules/help"
	"github.com/6586x57890143/skua/internal/modules/perf"
	"github.com/6586x57890143/skua/internal/modules/preen"
	"github.com/6586x57890143/skua/internal/modules/purge"
	"github.com/6586x57890143/skua/internal/modules/status"
	"github.com/6586x57890143/skua/internal/modules/whisper"
	"github.com/6586x57890143/skua/internal/obs"
	"github.com/6586x57890143/skua/internal/ratelimit"
	"github.com/6586x57890143/skua/internal/store"
	"github.com/6586x57890143/skua/internal/webhook"
)

// reprobe is how often skua re-reads the portal's intent toggles. A change
// exits the process cleanly and compose's restart policy brings it back
// identifying with the new set.
//
// ponytail: restart rather than swapping the gateway connection in place;
// a reconnect inside the process is the upgrade if restarts ever hurt.
const reprobe = 10 * time.Minute

// userReset is whether a user-scope 429 is waited out to its bucket's reset
// rather than its retry_after; tools/reactbench -compare policy decided it.
const userReset = true

// drainBy is how long a shutdown waits for preen's owed removals and fills,
// inside docker-compose.prod.yml's 30s stop_grace_period.
const drainBy = 25 * time.Second

var errIntentsChanged = errors.New("privileged intents changed in the Developer Portal; restarting to re-identify")

func main() {
	level := new(slog.LevelVar)
	_ = level.UnmarshalText([]byte(env("SKUA_LOG_LEVEL", "info")))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	// For a module that logs from deep in a goroutine (preen) without a
	// logger threaded through.
	slog.SetDefault(log)

	err := run(log)
	switch {
	case errors.Is(err, errIntentsChanged):
		log.Info(err.Error())
	case err != nil:
		log.Error("skua stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		return errors.New("DISCORD_BOT_TOKEN is not set")
	}
	var bootstrap snowflake.ID
	if v := os.Getenv("SKUA_BOOTSTRAP_ADMIN_USER_ID"); v != "" {
		id, err := snowflake.Parse(v)
		if err != nil {
			return fmt.Errorf("SKUA_BOOTSTRAP_ADMIN_USER_ID: %w", err)
		}
		bootstrap = id
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := servePprof(os.Getenv("SKUA_PPROF"), log); err != nil {
		return err
	}

	var db status.Pinger
	var purgeDB purge.DB
	// Modules a guild has turned off; without a database a restart turns
	// them all back on.
	toggles := core.NewToggles(nil, nil)
	if dsn := os.Getenv("SKUA_DATABASE_URL"); dsn != "" {
		pool, err := store.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer pool.Close()
		if err := store.Migrate(ctx, pool); err != nil {
			return err
		}
		db, purgeDB = pool, pool
		off, err := store.ModulesOff(ctx, pool)
		if err != nil {
			return fmt.Errorf("reading module switches: %w", err)
		}
		toggles = core.NewToggles(off, store.SaveModule(pool))
	}

	// Ask the portal what is granted before choosing what to identify with.
	probeRest := rest.New(rest.NewClient(token))
	app, err := probeRest.GetCurrentApplication()
	if err != nil {
		return fmt.Errorf("reading application flags: %w", err)
	}
	granted := intents.Granted(app.Flags)

	var client *bot.Client
	var probe status.Probe
	// Read by help once the guide is asked for, by which point both are set.
	var router *core.Router
	var running []core.Module
	// One guard for every writer: its breaker is per guild across modules.
	g := guard.New()
	// One poster too: whisper and bird share each channel's webhook.
	hooks := webhook.New(g)
	// preen asks whisper who wrote a whisper, so its writer gets the flock.
	whispers := whisper.New(g, hooks, filter.Default())
	// Shutdown waits on preen, so a self-react's removal is never lost to a
	// deploy.
	preener := preen.New(g, obs.Default, whispers)
	all := []core.Module{
		status.New(func() status.Probe { return probe }, db, func() time.Duration {
			if client == nil || client.Gateway == nil {
				return 0
			}
			return client.Gateway.Latency()
		}),
		whispers,
		bird.New(g, hooks, filter.Default(), os.Getenv("SKUA_XENO_CANTO_KEY")),
		purge.New(g, purgeDB, log, bootstrap),
		preener,
		perf.New(obs.Default),
		help.New(func() []core.Module { return running }, func(i discord.Interaction) bool { return router.Admin(i) }, toggles),
	}

	wants := make(map[string]intents.Want, len(all))
	for _, m := range all {
		wants[m.Name()] = m.Want()
	}
	identify, skipped := intents.Resolve(wants, granted)
	probe = status.Probe{Granted: granted, Identified: identify, Skipped: skipped}
	for _, name := range skipped {
		log.Warn("module skipped: a required intent is not granted", "module", name)
	}

	router = core.NewRouter(bootstrap, func(g snowflake.ID) (snowflake.ID, bool) {
		guild, ok := client.Caches.Guild(g)
		return guild.OwnerID, ok
	}, log)
	router.Gate(toggles)
	// Gateway events reach the running modules that listen for them.
	var listeners []bot.EventListener
	for _, m := range all {
		if slices.Contains(skipped, m.Name()) {
			continue
		}
		if err := router.Add(m); err != nil {
			return err
		}
		running = append(running, m)
		if gm, ok := m.(core.Gated); ok {
			name := m.Name()
			gm.Gate(func(guild snowflake.ID) bool { return toggles.On(guild, name) })
		}
		if l, ok := m.(bot.EventListener); ok {
			listeners = append(listeners, toggles.Listen(m.Name(), obs.Listen(obs.Default, m.Name(), l)))
		}
	}
	// Icons go up once as application emoji, so no reply re-uploads one.
	// Off the boot path: until it finishes, or if it fails, they go as
	// attachments, as before.
	go func() {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err := brand.Sync(ctx, probeRest, app.ID, g); err != nil {
			log.Warn("syncing emoji; the icons missing go as attachments", "err", err)
		}
	}()
	// The install link asks for what the running modules declare, so it
	// follows every module added, removed or skipped. A failure only leaves
	// the link stale, which is no reason not to boot.
	install := core.Install(running)
	if wrote, err := core.SyncInstall(probeRest, app, install); err != nil {
		log.Warn("updating the install settings", "err", err)
	} else if wrote {
		log.Info("install settings updated", "permissions", install.Permissions)
	}

	register := func(c *bot.Client, guild snowflake.ID) {
		if _, err := c.Rest.SetGuildCommands(c.ApplicationID, guild, router.CreatesFor(guild)); err != nil {
			log.Warn("registering commands", "guild", guild, "err", err)
		}
	}
	// A module turned off or on leaves or joins the guild's command list,
	// within the guild's command budget. Over budget, it tries again once
	// the budget has room: dispatch refuses an off module meanwhile, but a
	// module turned back on would otherwise stay missing from the menu.
	var resync func(snowflake.ID)
	resync = core.Coalesce(func(guild snowflake.ID) {
		if err := g.Allow(guild, guard.CommandSync); err != nil {
			time.AfterFunc(guard.Step(guard.CommandSync), func() { resync(guild) })
			return
		}
		_, err := client.Rest.SetGuildCommands(client.ApplicationID, guild, router.CreatesFor(guild))
		g.Report(guild, err != nil)
		if err != nil {
			log.Warn("re-registering commands after a switch", "guild", guild, "err", err)
		}
	})
	toggles.OnChange = resync

	client, err = disgo.New(token,
		bot.WithLogger(log),
		// Every REST call's rate limit wait and round trip, for /perf, and a
		// route's 429 waited out to the millisecond rather than the second.
		bot.WithRestClientConfigOpts(rest.WithRateLimiter(obs.Limiter(ratelimit.Precise(rest.NewRateLimiter(rest.WithRateLimiterLogger(log)), userReset), obs.Default))),
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(identify),
			gateway.WithPresenceOpts(gateway.WithCustomActivity(brand.StatusAt(0))),
		),
		// Only the guild cache is read (owner lookup); everything else off.
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds)),
		bot.WithEventListenerFunc(router.OnCommand),
		bot.WithEventListenerFunc(router.OnModal),
		bot.WithEventListenerFunc(router.OnComponent),
		bot.WithEventListeners(listeners...),
		bot.WithEventListenerFunc(func(e *events.GuildReady) { register(e.Client(), e.GuildID) }),
		bot.WithEventListenerFunc(func(e *events.GuildJoin) { register(e.Client(), e.GuildID) }),
	)
	if err != nil {
		return err
	}
	defer client.Close(context.Background())

	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("opening gateway: %w", err)
	}
	log.Info("skua is up", "modules", len(all)-len(skipped), "intents", identify)

	line := 0
	tick := time.NewTicker(reprobe)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			// No new events, then the self-reacts already taken are answered
			// before the REST client closes (docker-compose.prod.yml gives 30s).
			client.Gateway.Close(context.Background())
			if !preener.Drain(drainBy) {
				log.Warn("shutting down with preen work still owed")
			}
			return nil
		case <-tick.C:
			// The status line rides the re-probe tick: no loop of its own.
			line++
			if err := client.SetPresence(ctx, gateway.WithCustomActivity(brand.StatusAt(line))); err != nil {
				log.Warn("setting the status line", "err", err)
			}
			app, err := probeRest.GetCurrentApplication()
			if err != nil {
				log.Warn("re-probing intents", "err", err)
				continue
			}
			if intents.Granted(app.Flags) != granted {
				return errIntentsChanged
			}
		}
	}
}

// servePprof refuses anything but a loopback address: a profiler on a
// public port is a heap dump for whoever asks.
func servePprof(addr string, log *slog.Logger) error {
	if addr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("SKUA_PPROF: %w", err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("SKUA_PPROF must be a loopback address, got %q", addr)
	}
	// The flight recorder rides the same loopback port: the last seconds of
	// execution trace, for go tool trace, with a region per module.
	flight, err := obs.Flight()
	if err != nil {
		return fmt.Errorf("SKUA_PPROF: flight recorder: %w", err)
	}
	http.Handle("/debug/skua/flight", flight)
	go func() {
		log.Info("pprof listening", "addr", addr, "flight", "/debug/skua/flight")
		log.Warn("pprof stopped", "err", http.ListenAndServe(addr, nil)) //nolint:gosec // loopback only, checked above
	}()
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
