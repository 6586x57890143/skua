// Package echo is /echo: a member Discord has chat-restricted can still run
// slash commands, so they post through a per-channel webhook wearing their
// display name and avatar, with one subtext line naming who really sent it.
//
// That line is the whole transparency story. It carries the username, not
// the display name the post already wears: a webhook message has no profile
// to click, and a username cannot be made to look like a mod's. check keeps
// every echo to one line that cannot start a heading or subtext, so the
// marker is always the last line and the only subtext.
//
// Webhook posts skip Discord's AutoMod and slowmode, so echo applies both
// itself. Slowmode is honoured per channel and member. skua's automod
// screens member messages with rung 0 (which skips webhooks outright) and
// a rung 1 regex suite; echo must run its text through rung 1 before
// posting and refuse on a match, so nothing is ever posted to delete. Until
// the automod module lands that screen is missing: when it does, echo
// takes its rung 1 matcher as a constructor argument.
package echo

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
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// maxText leaves room under Discord's 2000 for the marker and a 32-rune
// username escaped at worst to 64.
const maxText = 1800

type hook struct {
	id    snowflake.ID
	token string
}

type slow struct{ channel, user snowflake.ID }

type Module struct {
	guard *guard.Guard
	hooks sync.Map // channel snowflake.ID -> hook
	// ponytail: never evicted, one entry per member per slowmode channel
	// they have echoed in. Sweep entries older than six hours (the longest
	// slowmode) if that ever shows up in a heap profile.
	last sync.Map // slow -> time.Time of their last echo there
	now  func() time.Time
}

func New(g *guard.Guard) *Module { return &Module{guard: g, now: time.Now} }

func (*Module) Name() string { return "echo" }

// Want is empty: everything echo reads arrives in the interaction.
func (*Module) Want() intents.Want { return intents.Want{} }

func (m *Module) Commands() []core.Command {
	return []core.Command{{
		Create: discord.SlashCommandCreate{
			Name:        "echo",
			Description: "Post one line through skua under your own name",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
			Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
				Name: "text", Description: "One line", Required: true, MaxLength: new(maxText),
			}},
		},
		Tier: core.Public,
		Run:  m.echo,
	}}
}

var (
	errEmpty     = errors.New("nothing to echo")
	errMultiline = errors.New("an echo is one line")
	errHeading   = errors.New("an echo cannot start with # or -#")
)

// check returns text ready to post, or why it cannot be. It is the only
// thing between a member and a forged marker, so it refuses rather than
// rewrites.
func check(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errEmpty
	}
	if strings.ContainsFunc(text, func(r rune) bool {
		return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp)
	}) {
		return "", errMultiline
	}
	// Quote and list prefixes, ordered ones included, still let a heading
	// or subtext render after them ("> -# x", "1. -# x"), so look past them.
	// Only "#" to "###" followed by a space is a heading; "#1 fan" is not.
	tail := strings.TrimLeft(text, ">-*+0123456789.) ")
	if n := len(tail) - len(strings.TrimLeft(tail, "#")); n >= 1 && n <= 3 {
		if r, _ := utf8.DecodeRuneInString(tail[n:]); unicode.IsSpace(r) {
			return "", errHeading
		}
	}
	if len([]rune(text)) > maxText {
		return "", fmt.Errorf("an echo is at most %d characters", maxText)
	}
	return text, nil
}

var markdown = strings.NewReplacer(`\`, `\\`, `_`, `\_`, `*`, `\*`, `~`, `\~`, "`", "\\`", `|`, `\|`, `>`, `\>`)

// marker is the last line of every echo. The username is escaped so
// "a_b_c" cannot render as "a" + italic "b" + "c".
func marker(username string) string {
	return "\n-# echoed through skua by @" + markdown.Replace(username)
}

func (m *Module) echo(ctx context.Context, e *events.ApplicationCommandInteractionCreate) (err error) {
	text, err := check(e.SlashCommandInteractionData().String("text"))
	if err != nil {
		return err
	}
	member, guild := e.Member(), e.GuildID()
	if member == nil || guild == nil {
		return errors.New("/echo only works in a server")
	}
	ch := e.Channel()
	switch ch.Type() {
	case discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews, discord.ChannelTypeGuildVoice:
	default:
		return errors.New("/echo only works in text channels, not threads or forums")
	}
	// Echo is for the restriction Discord applied, never a way around the
	// ones this server applied.
	if t := member.CommunicationDisabledUntil; t != nil && t.After(time.Now()) {
		return errors.New("you are timed out here")
	}
	if !member.Permissions.Has(discord.PermissionSendMessages) {
		return errors.New("you cannot send messages in this channel")
	}
	if p := e.AppPermissions(); p == nil || !p.Has(discord.PermissionManageWebhooks) {
		return errors.New("skua needs Manage Webhooks in this channel")
	}
	wait, undo := m.slowmode(ch, member)
	if wait > 0 {
		return fmt.Errorf("slowmode is on here; try again in %s", wait.Round(time.Second))
	}
	// Discord charges slowmode only for a message that went out.
	defer func() {
		if err != nil {
			undo()
		}
	}()
	// Per member before per guild, so one member cannot spend the whole
	// guild's webhook budget and lock everyone else out.
	if err := m.guard.Allow(member.User.ID, guard.EchoMember); err != nil {
		return errors.New("you are echoing too fast; try again later")
	}

	msg := discord.WebhookMessageCreate{
		Content:         text + marker(member.User.Username),
		Username:        member.EffectiveName(),
		AvatarURL:       member.EffectiveAvatarURL(),
		AllowedMentions: core.NoPings(),
	}
	// A webhook would unfurl links the member could not embed themselves.
	if !member.Permissions.Has(discord.PermissionEmbedLinks) {
		msg.Flags = discord.MessageFlagSuppressEmbeds
	}
	r, opt := e.Client().Rest, rest.WithCtx(ctx)
	for attempt := 0; ; attempt++ {
		h, err := m.hook(r, opt, *guild, ch.ID(), e.ApplicationID())
		if err != nil {
			return err
		}
		if err := m.guard.Allow(*guild, guard.WebhookExecute); err != nil {
			return err
		}
		_, err = r.CreateWebhookMessage(h.id, h.token, msg, rest.CreateWebhookMessageParams{}, opt)
		m.guard.Report(*guild, struggling(err))
		// A mod deleted the webhook: forget it and make another, once.
		if isCode(err, rest.JSONErrorCodeUnknownWebhook) && attempt == 0 {
			m.hooks.Delete(ch.ID())
			continue
		}
		if err != nil {
			return fmt.Errorf("posting the echo: %w", err)
		}
		return e.CreateMessage(discord.MessageCreate{Content: "✓ echoed", Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
	}
}

// slowmode applies the channel's slowmode as Discord would to a message.
// It returns how long the member must still wait, or 0 having recorded this
// echo as their latest, plus an undo for when the echo then fails. Members
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
	// Put their real last echo back, unless a concurrent echo of theirs
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

// hook returns this channel's webhook: cached, else the one skua already
// owns there (so a restart reuses it), else a new one.
//
// ponytail: two first echoes in one channel at the same moment can each
// create a webhook, against Discord's 15 per channel. Both work and later
// lookups settle on one; a per-channel singleflight is the fix if it bites.
func (m *Module) hook(r rest.Rest, opt rest.RequestOpt, guild, ch, app snowflake.ID) (hook, error) {
	if v, ok := m.hooks.Load(ch); ok {
		return v.(hook), nil
	}
	existing, err := r.GetWebhooks(ch, opt)
	if err != nil {
		return hook{}, fmt.Errorf("listing webhooks: %w", err)
	}
	for _, w := range existing {
		if in, ok := w.(discord.IncomingWebhook); ok && in.ApplicationID != nil && *in.ApplicationID == app && in.Token != "" {
			h := hook{in.ID(), in.Token}
			m.hooks.Store(ch, h)
			return h, nil
		}
	}
	if err := m.guard.Allow(guild, guard.WebhookCreate); err != nil {
		return hook{}, err
	}
	in, err := r.CreateWebhook(ch, discord.WebhookCreate{Name: "skua echo"}, opt)
	m.guard.Report(guild, struggling(err))
	if err != nil {
		return hook{}, fmt.Errorf("creating the webhook: %w", err)
	}
	h := hook{in.ID(), in.Token}
	m.hooks.Store(ch, h)
	return h, nil
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

func isCode(err error, code rest.JSONErrorCode) bool {
	re, ok := errors.AsType[*rest.Error](err)
	return ok && re.Code == code
}
