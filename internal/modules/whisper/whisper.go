// Package whisper is /whisper: a member Discord has chat-restricted can still run
// slash commands, so they post through a per-channel webhook wearing their
// display name and avatar, with one subtext line naming who really sent it.
//
// That line is the whole transparency story. It carries the username, not
// the display name the post already wears: a webhook message has no profile
// to click, and a username cannot be made to look like a mod's. clean
// rewrites every whisper into one line that renders no heading or subtext, so
// the marker is always the last line and the only subtext. It rewrites
// rather than refuses: the member's only feedback is their message.
//
// Webhook posts skip Discord's AutoMod and slowmode, so whisper applies both
// itself. Slowmode is honoured per channel and member. Every whisper, and the
// display name it wears, is screened through internal/filter before it is
// posted: a slur is rewritten in place and the whisper still goes out; a bot
// token or a malicious link refuses it. Nothing is ever posted to delete.
// The filter is the same one automod's rung 1 runs over member messages
// (whose rung 0 skips webhooks), so the two cannot disagree.
//
// A member can edit or delete their own whispers afterwards (own.go).
package whisper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// maxText leaves room under Discord's 2000 for the marker and a 32-rune
// username escaped at worst to 64.
const maxText = 1800

// postBy bounds the webhook calls once the interaction is deferred.
const postBy = 10 * time.Second

// screen is the slice of filter.Filter whisper uses.
type screen interface {
	Check(text string) filter.Verdict
}

// poster is the slice of webhook.Poster whisper uses.
type poster interface {
	Send(ctx context.Context, r rest.Rest, guild, channel, app snowflake.ID, msg discord.WebhookMessageCreate) error
	Get(ctx context.Context, r rest.Rest, channel, app, webhookID, message snowflake.ID) (*discord.Message, error)
	Edit(ctx context.Context, r rest.Rest, guild, channel, app, webhookID, message snowflake.ID, update discord.WebhookMessageUpdate) error
	Delete(ctx context.Context, r rest.Rest, guild, channel, app, webhookID, message snowflake.ID) error
}

type slow struct{ channel, user snowflake.ID }

type Module struct {
	guard  *guard.Guard
	post   poster
	screen screen
	// ponytail: never evicted, one entry per member per slowmode channel
	// they have whispered in. Sweep entries older than six hours (the longest
	// slowmode) if that ever shows up in a heap profile.
	last sync.Map // slow -> time.Time of their last whisper there
	now  func() time.Time
}

// New takes the process's one guard, for the per-member cap, the poster
// whispers go out through, and the screen they pass first.
func New(g *guard.Guard, p poster, s screen) *Module {
	return &Module{guard: g, post: p, screen: s, now: time.Now}
}

func (*Module) Name() string { return "whisper" }

// Want is empty: everything whisper reads arrives in the interaction.
func (*Module) Want() intents.Want { return intents.Want{} }

// Perms is what finding or creating the per-channel webhook takes. Posting
// through it needs nothing further.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionManageWebhooks
}

func (m *Module) Commands() []core.Command {
	return append([]core.Command{{
		Create: discord.SlashCommandCreate{
			Name:        "whisper",
			Description: "send a message under your own name",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
			Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
				Name: "message", Description: "what to say", Required: true, MaxLength: new(maxText),
			}},
		},
		Tier: core.Public,
		Run:  m.whisper,
	}}, m.ownCommands()...)
}

// What a member reads when whisper says no. Each is a core.Tell, so the router
// shows it as written; anything else reaches them as a generic failure.
var (
	errEmpty = core.Tell("there is nothing to send")
	// errBusy is the guild's webhook budget or breaker, not the member's.
	errBusy    = core.Tell("skua is sending a lot in this server right now; try again in a couple of minutes")
	errNotSent = core.Tell("your message didn't go through; try again in a moment")
	errTooFast = core.Tell("you're sending too fast; try again in a couple of minutes")
)

// clean rewrites text so it can be posted as it was meant, never refusing
// it: the member sees their message go out, not an error about markdown.
// It is still the only thing between a member and a forged marker, so the
// result is always one line that renders no heading or subtext.
func clean(text string) string {
	text = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp) {
			return ' '
		}
		return r
	}, text))
	// Quote and list prefixes, ordered ones included, still let a heading
	// or subtext render after them ("> -# x", "1. -# x"), so look past them.
	// Only "#" to "###" followed by a space is a heading; "#1 fan" is not.
	// Escaping its first "#" shows it as typed instead.
	// Any Unicode space counts, as it may for Discord's parser ("> -# x"
	// with a no-break space).
	tail := strings.TrimLeftFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(">-*+0123456789.)", r)
	})
	if n := len(tail) - len(strings.TrimLeft(tail, "#")); n >= 1 && n <= 3 {
		if r, _ := utf8.DecodeRuneInString(tail[n:]); unicode.IsSpace(r) {
			at := len(text) - len(tail)
			text = text[:at] + `\` + text[at:]
		}
	}
	// Discord holds the client to maxText; this is for the API.
	if r := []rune(text); len(r) > maxText {
		text = strings.TrimSpace(string(r[:maxText]))
	}
	return text
}

// name is what the webhook posts as. Discord refuses webhook names that
// contain "discord" or "clyde"; such a member posts as their username
// rather than not at all.
func name(m *discord.ResolvedMember) string {
	n := strings.ToLower(m.EffectiveName())
	if strings.Contains(n, "discord") || strings.Contains(n, "clyde") {
		return m.User.Username
	}
	return m.EffectiveName()
}

var markdown = strings.NewReplacer(`\`, `\\`, `_`, `\_`, `*`, `\*`, `~`, `\~`, "`", "\\`", `|`, `\|`, `>`, `\>`)

// marker is the last line of every whisper. The username is escaped so
// "a_b_c" cannot render as "a" + italic "b" + "c".
func marker(username string) string {
	return "\n-# whispered through skua by @" + markdown.Replace(username)
}

func (m *Module) whisper(ctx context.Context, e *events.ApplicationCommandInteractionCreate) (err error) {
	text := clean(e.SlashCommandInteractionData().String("message"))
	if text == "" {
		return errEmpty
	}
	v := m.screen.Check(text)
	if v.Block != "" {
		return core.Tell("skua won't post that: it contains " + v.Block)
	}
	text = v.Text
	member, guild := e.Member(), e.GuildID()
	if member == nil || guild == nil {
		return core.Tell("/whisper only works in a server")
	}
	ch := e.Channel()
	switch ch.Type() {
	case discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews, discord.ChannelTypeGuildVoice:
	default:
		return core.Tell("/whisper works in text channels, not threads or forum posts")
	}
	if err := gates(member); err != nil {
		return err
	}
	if p := e.AppPermissions(); p == nil || !p.Has(discord.PermissionManageWebhooks) {
		return core.Tell("skua can't post here yet: it needs manage webhooks in this channel")
	}
	wait, undo := m.slowmode(ch, member)
	if wait > 0 {
		return core.Tell(fmt.Sprintf("slowmode is on: you can send again in %s", wait.Round(time.Second)))
	}
	// Discord charges slowmode only for a message that went out.
	defer func() {
		if err != nil {
			undo()
		}
	}()
	// Per member before per guild, so one member cannot spend the whole
	// guild's webhook budget and lock everyone else out.
	if err := m.guard.Allow(member.User.ID, guard.WhisperMember); err != nil {
		return errTooFast
	}

	msg := discord.WebhookMessageCreate{
		Content:         text + marker(member.User.Username),
		Username:        m.screen.Check(name(member)).Text,
		AvatarURL:       member.EffectiveAvatarURL(),
		AllowedMentions: core.NoPings(),
	}
	// A webhook would unfurl links the member could not embed themselves.
	if !member.Permissions.Has(discord.PermissionEmbedLinks) {
		msg.Flags = discord.MessageFlagSuppressEmbeds
	}
	// Seamless: a silent ephemeral defer holds the interaction open and is
	// deleted once the whisper is up, so the channel shows only the whisper. A
	// failure from here on reaches the member as a followup in its place.
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	// Deferred, the interaction token is good for 15 minutes, so the posts
	// no longer race the 3s window.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postBy)
	defer cancel()
	r := e.Client().Rest
	if err := m.post.Send(ctx, r, *guild, ch.ID(), e.ApplicationID(), msg); err != nil {
		return failed(errNotSent, err)
	}
	// The whisper is up. If the delete fails, the member alone is left with a
	// stale "thinking"; failing here would undo their slowmode and report an
	// error for a whisper that went out.
	_ = r.DeleteInteractionResponse(e.ApplicationID(), e.Token(), rest.WithCtx(ctx))
	return nil
}

// gates is what this server requires of a member to post here, which whisper
// holds every whisper and every edit of one to: whisper is for the restriction
// Discord applied, never a way around the ones the server applied.
func gates(member *discord.ResolvedMember) error {
	if t := member.CommunicationDisabledUntil; t != nil && t.After(time.Now()) {
		return core.Tell("you're timed out, so you can't send messages here yet")
	}
	if !member.Permissions.Has(discord.PermissionSendMessages) {
		return core.Tell("you can't send messages in this channel")
	}
	return nil
}

// failed is what a member reads when a webhook call fails. The guild's
// budget or breaker saying no is not the member's doing and needs no log;
// anything else is the given Tell, wrapping the cause for the log.
func failed(tell core.Tell, err error) error {
	if errors.Is(err, guard.ErrRateLimited) || errors.Is(err, guard.ErrCircuitOpen) {
		return errBusy
	}
	return fmt.Errorf("%w: %w", tell, err)
}

// slowmode applies the channel's slowmode as Discord would to a message.
// It returns how long the member must still wait, or 0 having recorded this
// whisper as their latest, plus an undo for when the whisper then fails. Members
// who could manage messages or the channel are exempt, as Discord exempts
// them.
func (m *Module) slowmode(ch discord.InteractionChannel, member *discord.ResolvedMember) (time.Duration, func()) {
	c, ok := ch.MessageChannel.(discord.GuildMessageChannel)
	if !ok || c.RateLimitPerUser() <= 0 ||
		member.Permissions.Has(discord.PermissionManageMessages) || member.Permissions.Has(discord.PermissionManageChannels) {
		return 0, func() {}
	}
	window := time.Duration(c.RateLimitPerUser()) * time.Second
	k, now := slow{ch.ID(), member.User.ID}, m.now()
	prev, loaded := m.last.Swap(k, now)
	// Put their real last whisper back, unless a concurrent whisper of theirs
	// has already replaced ours.
	undo := func() {
		if loaded {
			m.last.CompareAndSwap(k, now, prev)
		} else {
			m.last.CompareAndDelete(k, now)
		}
	}
	if loaded {
		if wait := prev.(time.Time).Add(window).Sub(now); wait > 0 {
			undo()
			return wait, nil
		}
	}
	return 0, undo
}
