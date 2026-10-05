// Package preen is for members who react to their own posts: skua covers
// the post in Birds birds drawn from the flock and takes the reaction off.
//
// How fast the birds go up is Discord's to decide: its reaction route is a
// per-channel bucket with a per-user limit under it, and disgo's limiter
// paces every call by the headers Discord sends back, a route 429 waited
// out to the millisecond (internal/ratelimit). The birds go up in order on
// one goroutine, the first of Strategies: tools/reactbench measured every
// way of keeping more in flight and none was faster. Each fill's time goes
// to /perf. The removal goes on its own goroutine beside the fill. Every
// call is spent through the guard.
package preen

import (
	"context"
	"errors"
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

// errGuard is a bird the guard would not spend.
var errGuard = errors.New("preen: over the reaction cap")

type Module struct {
	guard *guard.Guard
	rec   *obs.Recorder
	// flocked is the messages that got a flock, so a later self-react on one,
	// during the fill or after it, only comes off.
	flocked flocked
	// order draws the birds; tests fix it.
	order func(n int) []int
}

func New(g *guard.Guard, rec *obs.Recorder) *Module {
	return &Module{guard: g, rec: rec, order: rand.Perm, flocked: flocked{set: map[snowflake.ID]struct{}{}, ring: make([]snowflake.ID, remember)}}
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
		About: "react to your own message and she covers it in birds, fifteen of them picked at random, then takes your reaction away. there's nothing to run",
	}
}

func (*Module) Commands() []core.Command { return nil }

// OnEvent returns at once: the work runs on its own goroutine.
func (m *Module) OnEvent(ev bot.Event) {
	e, ok := ev.(*events.GuildMessageReactionAdd)
	if !ok || e.MessageAuthorID == nil || *e.MessageAuthorID != e.UserID || e.Member.User.Bot {
		return
	}
	r := e.Client().Rest
	if !m.flocked.add(e.MessageID) {
		go m.takeOff(r, e.GuildID, e.ChannelID, e.MessageID, e.UserID, e.Emoji.Reaction())
		return
	}
	go m.preen(r, e.GuildID, e.ChannelID, e.MessageID, e.UserID, e.Emoji.Reaction())
}

// takeOff removes a self-react from a message that already has its flock.
func (m *Module) takeOff(r rest.Rest, guild, channel, msg, user snowflake.ID, emoji string) {
	if m.guard.Allow(guild, guard.Reaction) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), preenBy)
	defer cancel()
	m.report(guild, r.RemoveUserReaction(channel, msg, emoji, user, rest.WithCtx(ctx)))
}

// remember is how many flocked messages preen keeps; an older one that is
// self-reacted to again gets a fresh flock, which is harmless.
const remember = 4096

// flocked is a bounded set of message IDs, oldest out first.
type flocked struct {
	mu   sync.Mutex
	set  map[snowflake.ID]struct{}
	ring []snowflake.ID
	next int
}

// add reports whether id is new, remembering it either way.
func (f *flocked) add(id snowflake.ID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.set[id]; ok {
		return false
	}
	delete(f.set, f.ring[f.next])
	f.ring[f.next] = id
	f.next = (f.next + 1) % len(f.ring)
	f.set[id] = struct{}{}
	return true
}

func (m *Module) preen(r rest.Rest, guild, channel, msg, user snowflake.ID, emoji string) {
	ctx, cancel := context.WithTimeout(context.Background(), preenBy)
	defer cancel()
	var off sync.WaitGroup
	if m.guard.Allow(guild, guard.Reaction) == nil {
		off.Go(func() { m.report(guild, r.RemoveUserReaction(channel, msg, emoji, user, rest.WithCtx(ctx))) })
	}
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
	off.Wait()
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
