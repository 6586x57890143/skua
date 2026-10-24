package fix

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
	"github.com/6586x57890143/skua/internal/obs"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

const Name = "fix"
const buildBy = 15 * time.Second

type counts struct{ Cards, Fallbacks, Failed, Busy uint64 }

// Module retains only counters and bounded source/reply IDs. No post text or
// credentials are persisted. Its context is main's shutdown context.
type Module struct {
	statsMu sync.Mutex
	ctx     context.Context
	api     Resolver
	guard   *guard.Guard
	log     *slog.Logger
	slots   chan struct{}
	inline  bool
	on      func(snowflake.ID) bool
	mu      sync.Mutex
	stats   map[snowflake.ID]counts
	// refusals is under statsMu: channels that answered 403, until when.
	refusals map[refusal]time.Time
	tracked  map[source]*tracking
	rest     rest.Rest
	wake     *time.Timer
}

func New(ctx context.Context, g *guard.Guard, api Resolver, log *slog.Logger) *Module {
	return &Module{ctx: ctx, api: api, guard: g, log: log, slots: make(chan struct{}, 8), stats: map[snowflake.ID]counts{}, refusals: map[refusal]time.Time{}, tracked: map[source]*tracking{}}
}
func (*Module) Name() string { return "fix" }
func (*Module) Want() intents.Want {
	return intents.Want{Required: gateway.IntentGuilds, Optional: gateway.IntentGuildMessages | gateway.IntentMessageContent}
}
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionSendMessages | discord.PermissionSendMessagesInThreads | discord.PermissionReadMessageHistory | discord.PermissionManageMessages
}
func (*Module) Help() core.Help {
	return core.Help{Color: brand.ColorInfo, Line: "social posts with their media intact", About: "she rebuilds social links as cards with their text and media. turn her on here to fix links automatically in this server or use /fix and the fix embed apps menu. she keeps the message text and hides its original previews after successful replacement when she has manage messages. she skips code and spoilers and links wrapped in angle brackets. automatic cards need the message content intent; commands work without it. previews use fxembed for x and bluesky. fixembed handles the other supported platforms"}
}
func (m *Module) Gate(on func(snowflake.ID) bool) { m.on = on }
func (m *Module) enabled(g snowflake.ID) bool     { return m.on == nil || m.on(g) }
func (m *Module) count(g snowflake.ID, change func(*counts)) {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	c := m.stats[g]
	change(&c)
	m.stats[g] = c
}
func (m *Module) Report(g snowflake.ID) core.Report {
	m.statsMu.Lock()
	c := m.stats[g]
	m.statsMu.Unlock()
	return core.Report{Rows: [][2]string{{"cards", strconv.FormatUint(c.Cards, 10)}, {"link fallbacks", strconv.FormatUint(c.Fallbacks, 10)}, {"delivery failures", strconv.FormatUint(c.Failed, 10)}, {"busy", strconv.FormatUint(c.Busy, 10)}}, Notes: []string{"-# fxembed and fixembed provide metadata; skua builds the cards"}}
}
func (m *Module) Commands() []core.Command {
	guild := []discord.InteractionContextType{discord.InteractionContextTypeGuild}
	return []core.Command{
		{Create: discord.SlashCommandCreate{Name: "fix", Description: "preview a social post with its media", Contexts: guild, Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: "link", Description: "a supported social post URL", Required: true},
			discord.ApplicationCommandOptionBool{Name: "share", Description: "post the card for everyone to see"},
		}}, Tier: core.Public, Run: m.command},
		{Create: discord.MessageCommandCreate{Name: "fix embed", Contexts: guild}, Tier: core.Public, Run: m.command},
	}
}
func (m *Module) command(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	started := time.Now()
	var l Link
	var ok, share bool
	if e.Data.Type() == discord.ApplicationCommandTypeMessage {
		found := Links(e.MessageCommandInteractionData().TargetMessage().Content)
		if len(found) > 0 {
			l, ok = found[0], true
		}
	} else {
		d := e.SlashCommandInteractionData()
		l, ok = Parse(d.String("link"))
		share = d.Bool("share")
	}
	if !ok {
		return core.Tell("she couldn't find a supported post link; use a full https post URL")
	}
	guild := e.GuildID()
	if guild == nil {
		return core.Tell("this only works in a server")
	}
	// A shared card is a public message with media; the member must be able to post one here.
	if member := e.Member(); share && (member == nil || !member.Permissions.Has(discord.PermissionSendMessages|discord.PermissionEmbedLinks)) {
		return core.Tell("you can't post embeds in this channel; run it without share to see it yourself")
	}
	select {
	case m.slots <- struct{}{}:
	default:
		m.count(*guild, func(c *counts) { c.Busy++ })
		return core.Tell("she's already fetching eight posts; try again in a moment")
	}
	if err := m.guard.Allow(*guild, guard.FixCard); err != nil {
		m.release()
		return core.Tell("too many cards here this hour; try again in a bit")
	}
	pending := loading()
	if !share {
		pending.Flags |= discord.MessageFlagEphemeral
	}
	if err := e.CreateMessage(pending); err != nil {
		m.release()
		return err
	}
	// The router dispatches on the gateway's goroutine, so the fetch runs off
	// it and every other event keeps flowing. Tests deliver in place.
	if m.inline {
		defer m.release()
		return m.deliver(ctx, e, *guild, l, started)
	}
	go func() {
		defer m.release()
		if err := m.deliver(ctx, e, *guild, l, started); err != nil {
			m.log.Warn("fix: card not delivered", "guild", *guild, "err", err)
		}
	}()
	return nil
}

// deliver fetches the post and replaces the loading card with it.
func (m *Module) deliver(ctx context.Context, e *events.ApplicationCommandInteractionCreate, g snowflake.ID, l Link, started time.Time) error {
	delivered := false
	defer func() {
		if delivered {
			obs.Default.Add("fix end-to-end", obs.Run, time.Since(started))
		}
	}()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), buildBy)
	defer cancel()
	// The shutdown context also cancels requests from a deferred interaction.
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	fetch, stopFetch := context.WithTimeout(ctx, 12*time.Second)
	msg := m.preview(fetch, g, l)
	stopFetch()
	_, err := e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.NewMessageUpdateV2(msg.Components...).WithAllowedMentions(core.NoPings()), rest.WithCtx(ctx))
	m.guard.Report(g, guard.Struggling(err))
	if err != nil {
		m.count(g, func(c *counts) { c.Failed++ })
		if m.finishLoading(e, g, l, err) {
			return nil
		}
		return errors.Join(core.Tell("the card couldn't be delivered; try again in a moment"), err)
	}
	m.count(g, func(c *counts) { c.Cards++ })
	delivered = true
	return nil
}
func (m *Module) preview(ctx context.Context, g snowflake.ID, l Link) discord.MessageCreate {
	msg, _ := m.previewResult(ctx, g, l)
	return msg
}
func (m *Module) previewResult(ctx context.Context, g snowflake.ID, l Link) (discord.MessageCreate, bool) {
	p, err := m.api.Resolve(ctx, l)
	if err != nil {
		if !errors.Is(ctx.Err(), context.Canceled) {
			m.count(g, func(c *counts) { c.Fallbacks++ })
		}
		return fallback(l), false
	}
	return card(l, p), !p.Incomplete
}
func (m *Module) OnEvent(ev bot.Event) {
	switch e := ev.(type) {
	case *events.GuildMessageCreate:
		if e.Message.Author.Bot || e.Message.WebhookID != nil || (e.Message.Type != discord.MessageTypeDefault && e.Message.Type != discord.MessageTypeReply) {
			return
		}
		m.queue(e.Client().Rest, source{e.GuildID, e.ChannelID, e.MessageID}, e.Message.Content, false, e.Message.Flags)
	case *events.GuildMessageUpdate:
		// Partial updates that carry only Discord's generated embeds aren't edits.
		if e.Message.EditedTimestamp == nil || e.Message.Author.ID == 0 {
			return
		}
		m.queue(e.Client().Rest, source{e.GuildID, e.ChannelID, e.MessageID}, e.Message.Content, true, e.Message.Flags)
	case *events.GuildMessageDelete:
		m.remove(e.Client().Rest, source{e.GuildID, e.ChannelID, e.MessageID})
	case *events.GuildLeave:
		m.mu.Lock()
		for key, t := range m.tracked {
			if key.guild == e.GuildID {
				t.gone = true
				t.forgotten = true
				if t.cancel != nil {
					t.cancel()
				}
				delete(m.tracked, key)
			}
		}
		m.statsMu.Lock()
		delete(m.stats, e.GuildID)
		m.statsMu.Unlock()
		m.mu.Unlock()
	}
}
