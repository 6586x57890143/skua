// Package preen is for members who react to their own posts: skua covers
// the post in Birds birds drawn from the flock and takes the reaction off.
//
// How fast the birds go up is Discord's to decide: its reaction route is a
// per-channel bucket with a per-user limit under it, and disgo's limiter
// paces every call by the headers Discord sends back, a route 429 waited
// out to the millisecond (internal/ratelimit). The birds go up in order on
// one goroutine, the first of Strategies: tools/reactbench measured every
// way of keeping more in flight and none was faster. Each fill's time goes
// to /perf. Every bird is spent through the guard.
//
// The removal of the member's own reaction is the one thing preen must
// always do, whatever happens to the birds. It goes first, skips the guard
// (whose cap and breaker hold back birds, never a removal), keeps trying
// through Discord trouble, and a shutdown waits for it (Drain).
package preen

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
	"github.com/6586x57890143/skua/internal/obs"
)

// Flock is twenty birds, Discord's cap of distinct reactions on a message.
// Every fill puts up Birds of them, drawn at random.
var Flock = []string{
	"🐦", "🐦‍⬛", "🕊️", "🦅", "🦆", "🦢", "🦉", "🦤", "🦩", "🦚",
	"🦜", "🐧", "🐔", "🐓", "🦃", "🐤", "🐣", "🐥", "🪿", "🪶",
}

// Birds is how many go up a self-react. preenBy bounds one self-react, the
// fill and the removal, and is long enough for all of them at any pace
// Discord has shown (350 to 600ms a bird).
const (
	Birds   = 15
	preenBy = 30 * time.Second
)

// removeBy bounds one removal with its retries, which back off from
// firstRetry, doubling.
const (
	removeBy   = 2 * time.Minute
	firstRetry = time.Second
)

// errGuard is a bird the guard would not spend.
var errGuard = errors.New("preen: over the reaction cap")

// Whispers says whether user wrote the whisper message, which skua posted
// through a webhook, so the reaction names the webhook as its author. It
// answers without a REST call. whisper.Module is one.
type Whispers interface {
	Wrote(message, user snowflake.ID) bool
}

type Module struct {
	guard    *guard.Guard
	rec      *obs.Recorder
	whispers Whispers
	// flocked is the messages that got a flock, so a later self-react on one,
	// during the fill or after it, only comes off.
	flocked flocked
	// order draws the birds; tests fix it.
	order func(n int) []int
	// removing counts the self-reacts still being answered, removal and
	// fill, for Drain. draining, under mu, turns new ones away once Drain
	// has begun: the gateway's Close doesn't wait for an event already
	// being dispatched, and an Add after Wait has started is a misuse.
	removing sync.WaitGroup
	mu       sync.Mutex
	draining bool
	// retry is the first wait between removal attempts; tests shorten it.
	retry time.Duration
}

// New takes the guard, the recorder and, optionally, the whispers: nil
// leaves whispers alone.
func New(g *guard.Guard, rec *obs.Recorder, w Whispers) *Module {
	return &Module{guard: g, rec: rec, whispers: w, order: rand.Perm, retry: firstRetry, flocked: newFlocked(remember)}
}

// Drain waits up to d for every self-react still being answered, its
// removal and its fill, and turns away any that arrive after it starts.
// Call it once the gateway is closed and before the REST client closes. It
// reports whether all of them finished; a fill cut short is forgotten with
// the process, so a later self-react on that message gets its flock.
func (m *Module) Drain(d time.Duration) bool {
	m.mu.Lock()
	m.draining = true
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.removing.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func (*Module) Name() string { return "preen" }

// Want is reaction events, which carry the message's author, so no message
// is ever fetched.
func (*Module) Want() intents.Want {
	return intents.Want{Required: gateway.IntentGuildMessageReactions}
}

// Perms is removing a member's reaction and adding skua's own; adding one
// needs the channel's history.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionReadMessageHistory |
		discord.PermissionAddReactions | discord.PermissionManageMessages
}

// Help is preen's page in /help.
func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorNotice,
		Line:  "puts up a little flock when you react to your own post",
		About: "react to your own message or your own whisper and she covers it in birds, fifteen of them picked at random, then takes your reaction away. there's nothing to run",
	}
}

func (*Module) Commands() []core.Command { return nil }

// OnEvent returns at once: the work runs on its own goroutine. A reaction
// on a whisper by the member who wrote it counts as a self-react.
func (m *Module) OnEvent(ev bot.Event) {
	e, ok := ev.(*events.GuildMessageReactionAdd)
	if !ok || e.MessageAuthorID == nil || e.Member.User.Bot {
		return
	}
	own := *e.MessageAuthorID == e.UserID || (m.whispers != nil && m.whispers.Wrote(e.MessageID, e.UserID))
	if !own {
		return
	}
	// Counted before the goroutine starts, so Drain cannot miss one it let in.
	m.mu.Lock()
	if m.draining {
		m.mu.Unlock()
		return
	}
	m.removing.Add(1)
	m.mu.Unlock()
	go m.react(e.Client().Rest, e.GuildID, e.ChannelID, e.MessageID, e.UserID, e.Emoji.Reaction())
}

// react answers a member reacting to what they wrote: the reaction comes
// off, then a flock the first time. A fill that fails is forgotten, so the
// next self-react on that message tries again. Drain waits for all of it.
//
// ponytail: work owed when the process dies without a clean shutdown is
// lost; persist owed removals and replay them at boot if that is seen.
func (m *Module) react(r rest.Rest, guild, channel, msg, user snowflake.ID, emoji string) {
	defer m.removing.Done()
	m.takeOff(r, guild, channel, msg, user, emoji)
	if !m.flocked.add(msg) {
		return
	}
	if err := m.preen(r, guild, channel, msg); err != nil {
		m.flocked.forget(msg)
		slog.Warn("preen: the flock didn't go up", "guild", guild, "channel", channel, "message", msg, "err", err)
	}
}

// takeOff removes the member's reaction. It retries anything that is
// Discord's trouble rather than the request's (a 429, a 5xx, no answer)
// until removeBy; a 4xx such as a missing permission or a reaction already
// gone ends it.
func (m *Module) takeOff(r rest.Rest, guild, channel, msg, user snowflake.ID, emoji string) {
	ctx, cancel := context.WithTimeout(context.Background(), removeBy)
	defer cancel()
	for wait := m.retry; ; wait *= 2 {
		err := r.RemoveUserReaction(channel, msg, emoji, user, rest.WithCtx(ctx))
		m.report(guild, err)
		if err == nil || !transient(err) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// transient is an error worth another try: Discord struggling, or no
// answer at all.
func transient(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if _, ok := errors.AsType[*rest.Error](err); !ok {
		return true
	}
	return struggling(err)
}

// remember is how many flocked messages preen keeps; an older one that is
// self-reacted to again gets a fresh flock, which is harmless.
const remember = 4096

// flocked is a bounded set of message IDs, oldest out first. set holds each
// ID's ring slot, so a slot coming round only drops the ID if it is still
// that ID's slot: one forgotten and added again lives in its newer slot.
type flocked struct {
	mu   sync.Mutex
	set  map[snowflake.ID]int
	ring []snowflake.ID
	next int
}

func newFlocked(n int) flocked {
	return flocked{set: map[snowflake.ID]int{}, ring: make([]snowflake.ID, n)}
}

// add reports whether id is new, remembering it either way.
func (f *flocked) add(id snowflake.ID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.set[id]; ok {
		return false
	}
	if old := f.ring[f.next]; f.set[old] == f.next {
		delete(f.set, old)
	}
	f.ring[f.next] = id
	f.set[id] = f.next
	f.next = (f.next + 1) % len(f.ring)
	return true
}

// forget drops id, so it counts as new again. Its ring slot stays until it
// comes round, and is then skipped.
func (f *flocked) forget(id snowflake.ID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.set, id)
}

// preen puts the birds up on msg, each spent through the guard, and says
// why it stopped short if it did.
func (m *Module) preen(r rest.Rest, guild, channel, msg snowflake.ID) error {
	ctx, cancel := context.WithTimeout(context.Background(), preenBy)
	defer cancel()
	birds := make([]string, Birds)
	for i, j := range m.order(len(Flock))[:Birds] {
		birds[i] = Flock[j]
	}
	start := time.Now()
	err := Strategies[0].Fill(ctx, birds, func(ctx context.Context, bird string) error {
		if m.guard.Allow(guild, guard.Reaction) != nil {
			return errGuard
		}
		err := r.AddReaction(channel, msg, bird, rest.WithCtx(ctx))
		m.report(guild, err)
		return err
	})
	// Only a whole flock says how fast a fill is.
	if err == nil {
		m.rec.Add("preen fill", obs.Run, time.Since(start))
	}
	return err
}

// report feeds a call's outcome to the breaker. A call cut off by a
// deadline or a sibling's failure says nothing about Discord, so it is not
// reported.
func (m *Module) report(guild snowflake.ID, err error) {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		m.guard.Report(guild, struggling(err))
	}
}

// struggling is true only for answers that say Discord is, not the request.
func struggling(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	if !ok || re.Response == nil {
		return false
	}
	s := re.Response.StatusCode
	return s == 429 || s >= 500
}
