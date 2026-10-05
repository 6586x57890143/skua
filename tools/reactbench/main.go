// Command reactbench measures, live, how fast preen fills a post. It posts a
// message in a test channel, puts preen.Birds of the flock on it by one of
// preen's Strategies, times it, deletes it, and repeats, interleaving the
// strategies so drift in Discord's latency hits them all alike. It goes
// through the same limiter stack the bot runs on, and prints each
// strategy's fill times and what Discord's rate limit headers said.
//
// It is how preen's fill was chosen, and the place to try the next idea:
// add a Strategy to preen.Strategies and run this. See "Live benchmarks" in
// CLAUDE.md for the test bot and channel.
//
// It stops a run that draws more than max429 rate limits, well inside
// Discord's invalid request limit (10,000 401, 403 and 429 answers per 10
// minutes per IP; crossing it bans the IP, and any bot on it, at
// Cloudflare).
//
//	set -a; . ../bench.env; set +a
//	go run ./tools/reactbench [-trials 5]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/modules/preen"
	"github.com/6586x57890143/skua/internal/ratelimit"
)

// max429 stops a run that is drawing rate limits instead of riding them.
// Discord's per-user reaction limit draws about seven a fill whatever skua
// does, so a run of five trials of four strategies sees around 140.
const max429 = 300

func main() {
	def, _ := strconv.ParseUint(os.Getenv("SKUA_BENCH_CHANNEL"), 10, 64)
	channel := flag.Uint64("channel", def, "a test channel the bot can post and react in (default SKUA_BENCH_CHANNEL)")
	trials := flag.Int("trials", 3, "fills per method")
	compare := flag.String("compare", "strategy", "strategy: every preen strategy; policy: serial under each 429 wait policy")
	flag.Parse()
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" || *channel == 0 {
		fmt.Fprintln(os.Stderr, "✗ needs DISCORD_BOT_TOKEN, and -channel or SKUA_BENCH_CHANNEL")
		os.Exit(2)
	}
	if err := run(token, snowflake.ID(*channel), *trials, *compare); err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+err.Error())
		os.Exit(1)
	}
}

// seen counts what Discord answered on the reaction route: each rate
// limit's limit and scope, the 429s, and on each 429 the wait its body gave
// (retry_after) beside the bucket's reset (X-RateLimit-Reset-After), the
// two candidates for how long to wait.
type seen struct {
	mu         sync.Mutex
	buckets    map[string]int
	n429       int
	retryAfter []time.Duration
	resetAfter []time.Duration
}

func (s *seen) note(rs *http.Response) {
	if !strings.Contains(rs.Request.URL.Path, "/reactions/") {
		return
	}
	scope := rs.Header.Get("X-RateLimit-Scope")
	if scope == "" {
		scope = "route"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs.StatusCode == http.StatusTooManyRequests {
		s.n429++
		scope += " 429"
		body, _ := io.ReadAll(io.LimitReader(rs.Body, 4<<10))
		_ = rs.Body.Close()
		rs.Body = io.NopCloser(bytes.NewReader(body))
		var b struct {
			RetryAfter float64 `json:"retry_after"`
		}
		reset, err := strconv.ParseFloat(rs.Header.Get("X-RateLimit-Reset-After"), 64)
		if json.Unmarshal(body, &b) == nil && err == nil {
			s.retryAfter = append(s.retryAfter, time.Duration(b.RetryAfter*float64(time.Second)))
			s.resetAfter = append(s.resetAfter, time.Duration(reset*float64(time.Second)))
		}
	}
	s.buckets[fmt.Sprintf("limit %s · %s", rs.Header.Get("X-RateLimit-Limit"), scope)]++
}

func (s *seen) rateLimits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n429
}

type noting struct {
	next http.RoundTripper
	seen *seen
}

func (n noting) RoundTrip(rq *http.Request) (*http.Response, error) {
	rs, err := n.next.RoundTrip(rq)
	if err == nil {
		n.seen.note(rs)
	}
	return rs, err
}

// method is one way of filling: a strategy through a client.
type method struct {
	name string
	r    rest.Rest
	st   preen.Strategy
}

func run(token string, channel snowflake.ID, trials int, compare string) error {
	s := &seen{buckets: map[string]int{}}
	hc := &http.Client{Transport: noting{http.DefaultTransport, s}, Timeout: 30 * time.Second}
	// The bot's own limiter stack, so a method's time here is its time in
	// production. disgo's 429 warnings would drown the table; the 429s are
	// counted instead.
	quiet := slog.New(slog.DiscardHandler)
	client := func(userReset bool) rest.Rest {
		return rest.New(rest.NewClient(token, rest.WithHTTPClient(hc), rest.WithLogger(quiet),
			rest.WithRateLimiter(ratelimit.Precise(rest.NewRateLimiter(rest.WithRateLimiterLogger(quiet)), userReset))))
	}
	var methods []method
	switch compare {
	case "strategy":
		r := client(false)
		for _, st := range preen.Strategies {
			methods = append(methods, method{st.Name, r, st})
		}
	case "policy":
		methods = []method{{"retry_after", client(false), preen.Strategies[0]}, {"user reset", client(true), preen.Strategies[0]}}
	default:
		return fmt.Errorf("-compare is strategy or policy, not %q", compare)
	}

	times := map[string][]time.Duration{}
	for k := range trials {
		for _, m := range methods {
			before := s.rateLimits()
			took, err := fill(m.r, channel, m.st, fmt.Sprintf("-# reactbench · %s · %d of %d", m.name, k+1, trials))
			if err != nil {
				return fmt.Errorf("%s, trial %d: %w", m.name, k+1, err)
			}
			times[m.name] = append(times[m.name], took)
			fmt.Printf("    ✓ %-11s %d of %d  %v  %d × 429\n", m.name, k+1, trials, took.Round(time.Millisecond), s.rateLimits()-before)
			if n := s.rateLimits(); n > max429 {
				return fmt.Errorf("stopped after %d rate limits to stay inside discord's invalid request limit", n)
			}
			// Let the bucket drain so one fill's tail is not the next one's head.
			time.Sleep(2 * time.Second)
		}
	}
	report(times, s)
	return nil
}

// fill posts a test message, fills it by st, deletes it and says how long
// the fill took.
func fill(r rest.Rest, channel snowflake.ID, st preen.Strategy, label string) (time.Duration, error) {
	msg, err := r.CreateMessage(channel, discord.MessageCreate{
		Content:         label,
		AllowedMentions: &discord.AllowedMentions{Parse: []discord.AllowedMentionType{}},
	})
	if err != nil {
		return 0, fmt.Errorf("posting the test message: %w", err)
	}
	defer func() { _ = r.DeleteMessage(channel, msg.ID) }()
	birds := slices.Clone(preen.Flock)
	rand.Shuffle(len(birds), func(i, j int) { birds[i], birds[j] = birds[j], birds[i] })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start := time.Now()
	err = st.Fill(ctx, birds[:preen.Birds], func(ctx context.Context, b string) error {
		return r.AddReaction(channel, msg.ID, b, rest.WithCtx(ctx))
	})
	return time.Since(start), err
}

func report(times map[string][]time.Duration, s *seen) {
	names := make([]string, 0, len(times))
	for n := range times {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b string) int { return int(median(times[a]) - median(times[b])) })
	fmt.Printf("\n▸ fill time for %d birds, fastest median first\n", preen.Birds)
	fmt.Printf("    %-12s%9s%9s%9s\n", "method", "median", "min", "max")
	for _, n := range names {
		ts := times[n]
		fmt.Printf("    %-12s%9v%9v%9v\n", n, median(ts).Round(time.Millisecond), slices.Min(ts).Round(time.Millisecond), slices.Max(ts).Round(time.Millisecond))
	}
	fmt.Println("\n▸ what discord said on the reaction route")
	keys := make([]string, 0, len(s.buckets))
	for k := range s.buckets {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Printf("    %5d × %s\n", s.buckets[k], k)
	}
	if len(s.retryAfter) > 0 {
		fmt.Printf("    on a 429, median wait: body retry_after %v, header reset-after %v\n",
			median(s.retryAfter).Round(time.Millisecond), median(s.resetAfter).Round(time.Millisecond))
	}
}

func median(ts []time.Duration) time.Duration {
	s := slices.Sorted(slices.Values(ts))
	return s[len(s)/2]
}
