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
	"github.com/6586x57890143/skua/internal/modules/purge"
	"github.com/6586x57890143/skua/internal/modules/status"
	"github.com/6586x57890143/skua/internal/modules/whisper"
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

var errIntentsChanged = errors.New("privileged intents changed in the Developer Portal; restarting to re-identify")

func main() {
	level := new(slog.LevelVar)
	_ = level.UnmarshalText([]byte(env("SKUA_LOG_LEVEL", "info")))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

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
	// One guard for every writer: its breaker is per guild across modules.
	g := guard.New()
	// One poster too: whisper and bird share each channel's webhook.
	hooks := webhook.New(g)
	all := []core.Module{
		status.New(func() status.Probe { return probe }, db, func() time.Duration {
			if client == nil || client.Gateway == nil {
				return 0
			}
			return client.Gateway.Latency()
		}),
		whisper.New(g, hooks, filter.Default()),
		bird.New(g, hooks, filter.Default(), os.Getenv("SKUA_XENO_CANTO_KEY")),
		purge.New(g, purgeDB, log, bootstrap),
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

	router := core.NewRouter(bootstrap, func(g snowflake.ID) (snowflake.ID, bool) {
		guild, ok := client.Caches.Guild(g)
		return guild.OwnerID, ok
	}, log)
	var running []core.Module
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
		if l, ok := m.(bot.EventListener); ok {
			listeners = append(listeners, l)
		}
	}
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
		if _, err := c.Rest.SetGuildCommands(c.ApplicationID, guild, router.Creates()); err != nil {
			log.Warn("registering commands", "guild", guild, "err", err)
		}
	}

	client, err = disgo.New(token,
		bot.WithLogger(log),
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
	go func() {
		log.Info("pprof listening", "addr", addr)
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
