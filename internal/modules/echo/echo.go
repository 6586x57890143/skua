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
// Known gaps, deliberately open: webhook posts skip AutoMod and the
// channel's slowmode. The per-member guard cap bounds the damage; whether
// echo should run text past AutoMod's rules first is a product call.
package echo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

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

type Module struct {
	guard *guard.Guard
	hooks sync.Map // channel snowflake.ID -> hook
}

func New(g *guard.Guard) *Module { return &Module{guard: g} }

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
	if strings.HasPrefix(strings.TrimLeft(text, ">-*+0123456789.) "), "#") {
		return "", errHeading
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

func (m *Module) echo(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
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
		return e.CreateMessage(discord.MessageCreate{Content: "✓ echoed", Flags: discord.MessageFlagEphemeral})
	}
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
