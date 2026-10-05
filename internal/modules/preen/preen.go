// Package preen is for members who react to their own posts: skua puts up a
// burst of birds and takes the reaction off.
//
// Every call goes out at once, each on a goroutine of its own, so nothing in
// skua waits on another: twelve birds and the removal. They still land at
// Discord's pace, because its reaction route is a per-channel bucket, about
// one call every 250ms, that disgo's rate limiter queues them behind; twelve
// is what three seconds holds. The birds share one deadline at the end of
// the window, so any Discord hasn't taken by then are dropped from the
// queue, and a failed bird drops the rest the same way. The removal has its
// own deadline and goes ahead whatever the birds do. Each call is spent
// through the guard.
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
)

// flock is twenty birds, Discord's cap of distinct reactions on a message.
// A burst is drawn from it at random.
var flock = []string{
	"🐦", "🐦‍⬛", "🕊️", "🦅", "🦆", "🦢", "🦉", "🦤", "🦩", "🦚",
	"🦜", "🐧", "🐔", "🐓", "🦃", "🐤", "🐣", "🐥", "🪿", "🪶",
}

// birds is how many go up, and burst the deadline they share: twelve at
// Discord's pace fills three seconds. preenBy bounds the removal.
const (
	birds   = 12
	burst   = 3 * time.Second
	preenBy = 30 * time.Second
)

type Module struct {
	guard *guard.Guard
	// busy holds the messages being preened, so a second self-react on one
	// does not start a second burst racing the first.
	busy sync.Map // snowflake.ID -> struct{}
	// order draws the birds; tests fix it.
	order func(n int) []int
}

func New(g *guard.Guard) *Module { return &Module{guard: g, order: rand.Perm} }

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
		Line:  "a member who reacts to their own post gets a burst of birds",
		About: "she puts up twelve birds in about three seconds, then takes the self-react off. nothing to run; it just happens",
	}
}

func (*Module) Commands() []core.Command { return nil }

// OnEvent returns at once: the work runs on its own goroutine.
func (m *Module) OnEvent(ev bot.Event) {
	e, ok := ev.(*events.GuildMessageReactionAdd)
	if !ok || e.MessageAuthorID == nil || *e.MessageAuthorID != e.UserID || e.Member.User.Bot {
		return
	}
	if _, dup := m.busy.LoadOrStore(e.MessageID, struct{}{}); dup {
		return
	}
	go m.preen(e.Client().Rest, e.GuildID, e.ChannelID, e.MessageID, e.UserID, e.Emoji.Reaction())
}

func (m *Module) preen(r rest.Rest, guild, channel, msg, user snowflake.ID, emoji string) {
	defer m.busy.Delete(msg)
	flight, land := context.WithTimeout(context.Background(), burst)
	defer land()
	off, done := context.WithTimeout(context.Background(), preenBy)
	defer done()
	var wg sync.WaitGroup
	// fire spends one call through the guard and sends it at once; a failure
	// calls stop.
	fire := func(call func() error, stop func()) {
		if m.guard.Allow(guild, guard.Reaction) != nil {
			return
		}
		wg.Go(func() {
			if !m.spend(guild, call()) {
				stop()
			}
		})
	}
	fire(func() error { return r.RemoveUserReaction(channel, msg, emoji, user, rest.WithCtx(off)) }, func() {})
	for _, i := range m.order(len(flock))[:birds] {
		fire(func() error { return r.AddReaction(channel, msg, flock[i], rest.WithCtx(flight)) }, land)
	}
	wg.Wait()
}

// spend reports a call to the breaker and says whether to keep going. Any
// failure stops the burst: a missing permission or a deleted message fails
// every bird after it the same way. A call dropped at its deadline says
// nothing about Discord, so it is not reported.
func (m *Module) spend(guild snowflake.ID, err error) bool {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		m.guard.Report(guild, struggling(err))
	}
	return err == nil
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
