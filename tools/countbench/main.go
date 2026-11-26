// Command countbench measures, live, how fast counting bots answer a count:
// the time from a member's message reaching the gateway to a bot's first
// reaction on it reaching the gateway, both seen by the same watcher. It
// only watches, and never posts or reacts, so it can sit in a channel
// where skua counts beside one where another counting bot does while a
// person counts in both, and every bot is timed the same way, through the
// same connection.
//
//	set -a; . ../bench.env; set +a
//	go run ./tools/countbench -channels 111,222 -for 10m
//
// It runs as the test bot (DISCORD_BOT_TOKEN, flightless.skua), never as
// skua, and needs only to read the channels: no intent beyond guild
// messages and their reactions. Members' counts are the bots' to answer,
// so a person counts; a bot's own messages aren't timed.
package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
)

// missAfter is how long a count goes unanswered before it is a miss.
const missAfter = 10 * time.Second

func main() {
	chans := flag.String("channels", "", "counting channels to watch, comma separated ids")
	d := flag.Duration("for", 10*time.Minute, "how long to watch")
	flag.Parse()
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" || *chans == "" {
		fmt.Fprintln(os.Stderr, "✗ set DISCORD_BOT_TOKEN (the test bot's, from bench.env) and -channels")
		os.Exit(2)
	}
	watch := map[snowflake.ID]bool{}
	for _, s := range strings.Split(*chans, ",") {
		id, err := snowflake.Parse(strings.TrimSpace(s))
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %q is not a channel id\n", s)
			os.Exit(2)
		}
		watch[id] = true
	}
	if err := run(token, watch, *d); err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		os.Exit(1)
	}
}

// count is a member's message, seen at when the gateway gave it.
type count struct {
	channel snowflake.ID
	at      time.Time
	by      map[snowflake.ID]bool // the bots that have answered it
}

// watcher times every bot's first reaction on every member's message.
type watcher struct {
	watch  map[snowflake.ID]bool
	mu     sync.Mutex
	counts map[snowflake.ID]*count
	names  map[snowflake.ID]string
	took   map[snowflake.ID][]time.Duration       // bot -> latencies
	where  map[snowflake.ID]map[snowflake.ID]bool // bot -> the channels it answers in
	member int
}

func (w *watcher) message(e *events.GuildMessageCreate) {
	if !w.watch[e.ChannelID] || e.Message.Author.Bot {
		return
	}
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.member++
	w.counts[e.MessageID] = &count{channel: e.ChannelID, at: now, by: map[snowflake.ID]bool{}}
}

func (w *watcher) reaction(e *events.GuildMessageReactionAdd) {
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	c, ok := w.counts[e.MessageID]
	if !ok || c.by[e.UserID] || e.Member.User.ID == 0 || !e.Member.User.Bot {
		return
	}
	c.by[e.UserID] = true
	w.names[e.UserID] = e.Member.User.Username
	if w.where[e.UserID] == nil {
		w.where[e.UserID] = map[snowflake.ID]bool{}
	}
	w.where[e.UserID][c.channel] = true
	w.took[e.UserID] = append(w.took[e.UserID], now.Sub(c.at))
}

func run(token string, watch map[snowflake.ID]bool, d time.Duration) error {
	w := &watcher{watch: watch, counts: map[snowflake.ID]*count{}, names: map[snowflake.ID]string{},
		took: map[snowflake.ID][]time.Duration{}, where: map[snowflake.ID]map[snowflake.ID]bool{}}
	client, err := disgo.New(token,
		bot.WithLogger(slog.New(slog.DiscardHandler)),
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuilds|gateway.IntentGuildMessages|gateway.IntentGuildMessageReactions)),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagsNone)),
		bot.WithEventListenerFunc(w.message),
		bot.WithEventListenerFunc(w.reaction),
	)
	if err != nil {
		return err
	}
	defer client.Close(context.Background())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("opening the gateway: %w", err)
	}
	fmt.Printf("▸ watching %d channels for %s; count in them, ctrl-c to stop early\n", len(watch), d)
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
	// Give the last counts their chance to be answered.
	time.Sleep(min(missAfter, d))
	w.report()
	return nil
}

func (w *watcher) report() {
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Printf("\n▸ %d member messages\n", w.member)
	bots := make([]snowflake.ID, 0, len(w.took))
	for b := range w.took {
		bots = append(bots, b)
	}
	slices.SortFunc(bots, func(a, b snowflake.ID) int { return cmp.Compare(w.names[a], w.names[b]) })
	if len(bots) == 0 {
		fmt.Println("    ! no bot reacted to anything")
	}
	for _, b := range bots {
		took := slices.Clone(w.took[b])
		slices.Sort(took)
		seen := 0
		for _, c := range w.counts {
			if w.where[b][c.channel] {
				seen++
			}
		}
		fmt.Printf("    ✓ %-16s n %-4d p50 %-8s p90 %-8s p99 %-8s max %-8s unanswered %d\n", w.names[b], len(took),
			ms(at(took, 50)), ms(at(took, 90)), ms(at(took, 99)), ms(took[len(took)-1]), seen-len(took))
	}
}

// at is the p-th percentile of sorted, nearest rank.
func at(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := (p*len(sorted) + 99) / 100
	return sorted[max(min(i, len(sorted))-1, 0)]
}

func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64) + "ms"
}
