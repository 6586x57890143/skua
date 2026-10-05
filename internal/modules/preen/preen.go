// Package preen is for members who react to their own posts: skua takes the
// reaction off and puts up the whole flock instead.
//
// One self-react costs one removal and len(flock) adds, each spent through
// the guard and run in turn on a goroutine of its own, since Discord's
// reaction route is a per-channel bucket that parallel calls only queue
// behind.
package preen

import (
	"context"
	"errors"
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

// flock is every bird Discord has, at its cap of 20 distinct reactions.
var flock = []string{
	"🐦", "🐦‍⬛", "🕊️", "🦅", "🦆", "🦢", "🦉", "🦤", "🦩", "🦚",
	"🦜", "🐧", "🐔", "🐓", "🦃", "🐤", "🐣", "🐥", "🪿", "🪶",
}

// preenBy bounds one self-react: the removal and the whole flock.
const preenBy = 30 * time.Second

type Module struct {
	guard *guard.Guard
	// busy holds the messages being preened, so a second self-react on one
	// does not start a second flock racing the first.
	busy sync.Map // snowflake.ID -> struct{}
}

func New(g *guard.Guard) *Module { return &Module{guard: g} }

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
		Line:  "a member who reacts to their own post gets the whole flock",
		About: "she takes the self-react off and puts twenty birds up in its place. nothing to run; it just happens",
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
	ctx, cancel := context.WithTimeout(context.Background(), preenBy)
	defer cancel()
	if m.guard.Allow(guild, guard.Reaction) != nil || !m.spend(guild, r.RemoveUserReaction(channel, msg, emoji, user, rest.WithCtx(ctx))) {
		return
	}
	for _, bird := range flock {
		if m.guard.Allow(guild, guard.Reaction) != nil {
			return
		}
		if !m.spend(guild, r.AddReaction(channel, msg, bird, rest.WithCtx(ctx))) {
			return
		}
	}
}

// spend reports a call to the breaker and says whether to keep going. Any
// failure stops the flock: a missing permission or a deleted message fails
// every bird after it the same way.
func (m *Module) spend(guild snowflake.ID, err error) bool {
	m.guard.Report(guild, struggling(err))
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
