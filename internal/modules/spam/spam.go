// Package spam is the module that stops one member flooding a channel. It
// watches the channels a server's admins name, or all of them, scores each
// member's messages with internal/flood, and hands whoever trips it to
// internal/sanction: the burst that tripped it comes down, the sentence
// grows with their record, and the first sanction in line carries it out.
// skua ships the bird; a jail, a timeout or a ban is another Sanction
// passed to New, ahead of or behind it.
//
// Staff are never sanctioned: the owner, the bootstrap admin, and anyone
// whose roles can manage messages, moderate, kick, ban, manage the server or
// administer it. Roles are read only on a trip, and a trip whose roles
// can't be read is let go.
package spam

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/flood"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
	"github.com/6586x57890143/skua/internal/sanction"
)

// Name is the module's; Name() repeats it as a literal, which brand's icon
// test reads.
const Name = "spam"

// Ladder is a spammer's sentence: 10 minutes, doubled for each sanction on
// their record in the last week, up to a day.
var Ladder = sanction.Ladder{Base: 10 * time.Minute, Doublings: 7, Max: 24 * time.Hour, Window: 7 * 24 * time.Hour}

// actBy bounds one trip: the roles, the deletes, the sanction and the line.
const actBy = 30 * time.Second

// staff is the permissions that put a member beyond the module.
const staff = discord.PermissionAdministrator | discord.PermissionManageGuild | discord.PermissionManageMessages |
	discord.PermissionModerateMembers | discord.PermissionKickMembers | discord.PermissionBanMembers

// DB is where the watched channels are kept.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// watch is one guild's scope, replaced whole on a change so a message
// reads it without a lock.
type watch struct {
	all      bool
	channels map[snowflake.ID]bool
}

type Module struct {
	log       *slog.Logger
	guard     *guard.Guard
	db        DB // nil keeps the scope in memory
	history   sanction.History
	sanctions []sanction.Sanction
	owner     func(guild snowflake.ID) (snowflake.ID, bool)
	bootstrap snowflake.ID
	// Admin is the router's check, for the controls on spam's /help page.
	Admin func(discord.Interaction) bool

	flood   flood.Detector
	mu      sync.Mutex // one scope change at a time
	watches sync.Map   // guild -> *watch
	trips   sync.Map   // guild -> *atomic.Int64, since boot
	now     func() time.Time
}

// New loads every guild's scope. history is nil without a database, and
// then a record lasts only as long as the process. owner reads a guild's
// owner from the cache. sanctions are tried in order.
func New(ctx context.Context, log *slog.Logger, g *guard.Guard, db DB, history sanction.History,
	owner func(snowflake.ID) (snowflake.ID, bool), bootstrap snowflake.ID, sanctions ...sanction.Sanction) (*Module, error) {
	m := &Module{log: log, guard: g, db: db, history: history, owner: owner, bootstrap: bootstrap, sanctions: sanctions, now: time.Now}
	if db == nil {
		return m, nil
	}
	rows, _ := db.Query(ctx, "select guild_id, channel_id from spam_scope")
	scopes := map[snowflake.ID]*watch{}
	var guild, channel int64
	_, err := pgx.ForEachRow(rows, []any{&guild, &channel}, func() error {
		w := scopes[snowflake.ID(guild)]
		if w == nil {
			w = &watch{channels: map[snowflake.ID]bool{}}
			scopes[snowflake.ID(guild)] = w
		}
		if channel == 0 {
			w.all = true
		} else {
			w.channels[snowflake.ID(channel)] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("spam: reading the watched channels: %w", err)
	}
	for g, w := range scopes {
		m.watches.Store(g, w)
	}
	return m, nil
}

func (*Module) Name() string { return "spam" }

// Want is guild messages to score. Content sharpens it (repeats, walls of
// text, @everyone typed by someone who can't), and without it rate and
// pings still trip.
func (*Module) Want() intents.Want {
	return intents.Want{Required: gateway.IntentGuildMessages, Optional: gateway.IntentMessageContent}
}

// Perms is clearing the burst, reading the roles that say who is staff,
// and saying what happened in the channel.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionSendMessages | discord.PermissionManageMessages | discord.PermissionReadMessageHistory
}

func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorWarn,
		Line:  "turns a member flooding a channel into a bird",
		About: "she watches the channels an admin picks on this page, or every channel. a member who floods one, pings the same people over and over or posts the same thing again and again loses the messages that did it and becomes a bird for 10 minutes. each time on their record in the last week doubles it, up to a day. talking fast and splitting a thought over a few messages is left alone, and so are staff",
	}
}

// Commands is none: where she watches is set from spam's /help page.
func (*Module) Commands() []core.Command { return nil }

// watchable is the channels a member can flood.
var watchable = []discord.ChannelType{discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews, discord.ChannelTypeGuildVoice, discord.ChannelTypeGuildStageVoice}

// maxWatched is a channel select's limit; past it, every channel.
const maxWatched = 25

func (m *Module) Components() []core.Component {
	return []core.Component{{ID: "spam", Run: m.press}}
}

var errNotAdmin = core.Tell("only this server's admins can choose where she watches")

// press is the channel picker or the everywhere button on spam's /help
// page, admins only.
func (m *Module) press(ctx context.Context, e *events.ComponentInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil || m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	var said string
	switch id := e.Data.CustomID(); id {
	case "spam:watch":
		data, ok := e.Data.(discord.ChannelSelectMenuInteractionData)
		if !ok {
			return errNotAdmin
		}
		if err := m.pick(ctx, *guild, data.Values); err != nil {
			return err
		}
		said = "✓ watching no channel by name"
		if len(data.Values) > 0 {
			names := make([]string, len(data.Values))
			for i, ch := range data.Values {
				names[i] = "<#" + ch.String() + ">"
			}
			said = "✓ watching " + strings.Join(names, " ")
		}
	case "spam:all:on", "spam:all:off":
		on := id == "spam:all:on"
		if err := m.everywhere(ctx, *guild, on); err != nil {
			return err
		}
		said = "✓ watching only the channels picked"
		if on {
			said = "✓ watching every channel, threads too"
		}
	default:
		return errNotAdmin
	}
	return e.CreateMessage(discord.MessageCreate{Content: said, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}

// pick makes channels the guild's watched channels, in one statement so
// the store never holds half a pick. The store goes first, so memory never
// claims what a restart would forget.
func (m *Module) pick(ctx context.Context, guild snowflake.ID, channels []snowflake.ID) error {
	channels = channels[:min(len(channels), maxWatched)]
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db != nil {
		ids := make([]int64, len(channels))
		for i, ch := range channels {
			ids[i] = int64(ch)
		}
		if _, err := m.db.Exec(ctx, `with gone as (
				delete from spam_scope where guild_id = $1 and channel_id <> 0 and channel_id <> all($2::bigint[]))
			insert into spam_scope (guild_id, channel_id) select $1, unnest($2::bigint[]) on conflict do nothing`,
			int64(guild), ids); err != nil {
			return fmt.Errorf("spam: saving the channels: %w", err)
		}
	}
	w := m.scope(guild)
	w.channels = make(map[snowflake.ID]bool, len(channels))
	for _, ch := range channels {
		w.channels[ch] = true
	}
	m.watches.Store(guild, w)
	return nil
}

// everywhere watches every channel, or only the picked ones.
func (m *Module) everywhere(ctx context.Context, guild snowflake.ID, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db != nil {
		q := "insert into spam_scope (guild_id, channel_id) values ($1, 0) on conflict do nothing"
		if !on {
			q = "delete from spam_scope where guild_id = $1 and channel_id = 0"
		}
		if _, err := m.db.Exec(ctx, q, int64(guild)); err != nil {
			return fmt.Errorf("spam: saving the scope: %w", err)
		}
	}
	w := m.scope(guild)
	w.all = on
	m.watches.Store(guild, w)
	return nil
}

// scope is a copy of guild's watch to change and store whole.
func (m *Module) scope(guild snowflake.ID) *watch {
	w := &watch{channels: map[snowflake.ID]bool{}}
	if v, ok := m.watches.Load(guild); ok {
		old := v.(*watch)
		w.all, w.channels = old.all, maps.Clone(old.channels)
	}
	return w
}

// watched is whether channel is in guild's scope.
//
// ponytail: a thread is its own channel ID and the gateway doesn't say its
// parent, so a thread is watched only under everywhere; the guild's channel
// cache is the upgrade if spam moves into threads.
func (m *Module) watched(guild, channel snowflake.ID) bool {
	v, ok := m.watches.Load(guild)
	if !ok {
		return false
	}
	w := v.(*watch)
	return w.all || w.channels[channel]
}

// OnEvent scores a message in a watched channel. A trip is acted on off
// the gateway's goroutine.
func (m *Module) OnEvent(ev bot.Event) {
	e, ok := ev.(*events.GuildMessageCreate)
	if !ok {
		return
	}
	msg := e.Message
	if msg.Author.Bot || msg.Author.System || msg.WebhookID != nil ||
		(msg.Type != discord.MessageTypeDefault && msg.Type != discord.MessageTypeReply) || !m.watched(e.GuildID, msg.ChannelID) {
		return
	}
	pings := make([]snowflake.ID, 0, len(msg.Mentions)+len(msg.MentionRoles))
	for _, u := range msg.Mentions {
		if u.ID != msg.Author.ID {
			pings = append(pings, u.ID)
		}
	}
	pings = append(pings, msg.MentionRoles...)
	text, media := carried(msg)
	trip, burst := m.flood.Observe(flood.Message{
		Guild: e.GuildID, User: msg.Author.ID, Channel: msg.ChannelID, ID: msg.ID,
		At: msg.CreatedAt, Text: text, Pings: pings, Media: media,
		// Their own text only: a forwarded @everyone pings nobody.
		Everyone: msg.MentionEveryone || strings.Contains(msg.Content, "@everyone") || strings.Contains(msg.Content, "@here"),
	})
	if trip {
		go m.act(e.Client().Rest, e.GuildID, msg, burst)
	}
}

// carried is what a message shows: its text and, for flood.Message.Media,
// a string per sticker (its ID) and per attachment (its size and type, the
// same for the same file posted again, whatever its name). A forward
// carries nothing of its own, so it is read as what it forwards.
func carried(msg discord.Message) (string, []string) {
	text, stickers, files := msg.Content, msg.StickerItems, msg.Attachments
	for _, s := range msg.MessageSnapshots {
		text += s.Message.Content
		stickers = append(stickers, s.Message.StickerItems...)
		files = append(files, s.Message.Attachments...)
	}
	var media []string
	for _, s := range stickers {
		media = append(media, "s:"+s.ID.String())
	}
	for _, a := range files {
		kind := ""
		if a.ContentType != nil {
			kind = *a.ContentType
		}
		media = append(media, fmt.Sprintf("a:%d:%s", a.Size, kind))
	}
	return text, media
}

// act clears the burst and sentences the member, unless they are staff.
func (m *Module) act(r rest.Rest, guild snowflake.ID, msg discord.Message, burst []flood.Seen) {
	ctx, cancel := context.WithTimeout(context.Background(), actBy)
	defer cancel()
	user := msg.Author.ID
	if is, err := m.staff(r, guild, user, msg.Member); is || err != nil {
		m.log.Info("spam: tripped, let go", "guild", guild, "user", user, "staff", is, "err", err)
		return
	}
	v, _ := m.trips.LoadOrStore(guild, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
	cleared := m.clear(ctx, r, guild, burst)
	verdict := sanction.Judge(ctx, m.history, Ladder, sanction.Offence{Guild: guild, User: user, Channel: msg.ChannelID, Rule: Name, At: msg.CreatedAt}, m.sanctions...)
	m.log.Info("spam: tripped", "guild", guild, "user", user, "cleared", cleared, "priors", verdict.Priors,
		"for", verdict.For, "by", verdict.By, "err", verdict.Err)
	if verdict.Said == "" || m.guard.Allow(guild, guard.MessageSend) != nil {
		return
	}
	_, err := r.CreateMessage(msg.ChannelID, discord.MessageCreate{
		Content:         fmt.Sprintf("-# @%s got too loud and is %s", markdown.Replace(msg.Author.Username), verdict.Said),
		AllowedMentions: core.NoPings(),
		Flags:           discord.MessageFlagSuppressEmbeds,
	}, rest.WithCtx(ctx))
	m.guard.Done(guild, err)
}

// staff is whether user is beyond the module. The roles are read here,
// once per trip, rather than kept in a cache every message would pay for.
func (m *Module) staff(r rest.Rest, guild, user snowflake.ID, member *discord.Member) (bool, error) {
	if user == m.bootstrap {
		return true, nil
	}
	if owner, ok := m.owner(guild); ok && owner == user {
		return true, nil
	}
	if member == nil {
		return false, fmt.Errorf("spam: no member on the message")
	}
	roles, err := r.GetRoles(guild)
	if err != nil {
		return false, fmt.Errorf("spam: reading roles: %w", err)
	}
	var perms discord.Permissions
	for _, role := range roles {
		// The @everyone role's ID is the guild's.
		if role.ID == guild || slices.Contains(member.RoleIDs, role.ID) {
			perms |= role.Permissions
		}
	}
	return perms&staff != 0, nil
}

// clear takes the burst down, a channel at a time, and says how many went.
// Each call is one MessageDelete from the guard, a bulk delete of many
// included; the burst is under a minute old, well inside bulk delete's two
// weeks.
func (m *Module) clear(ctx context.Context, r rest.Rest, guild snowflake.ID, burst []flood.Seen) int {
	by := map[snowflake.ID][]snowflake.ID{}
	var order []snowflake.ID
	for _, s := range burst {
		if by[s.Channel] == nil {
			order = append(order, s.Channel)
		}
		by[s.Channel] = append(by[s.Channel], s.ID)
	}
	n := 0
	for _, ch := range order {
		if m.guard.Allow(guild, guard.MessageDelete) != nil {
			break
		}
		ids, why := by[ch], rest.WithReason("spam: a burst from one member")
		var err error
		if len(ids) == 1 {
			err = r.DeleteMessage(ch, ids[0], rest.WithCtx(ctx), why)
		} else {
			err = r.BulkDeleteMessages(ch, ids, rest.WithCtx(ctx), why)
		}
		m.guard.Done(guild, err)
		if err == nil {
			n += len(ids)
		}
	}
	return n
}

// Report is the scope and what it has caught, for the server's admins.
func (m *Module) Report(guild snowflake.ID) core.Report {
	where := "nowhere yet"
	if v, ok := m.watches.Load(guild); ok {
		switch w := v.(*watch); {
		case w.all:
			where = "every channel"
		case len(w.channels) == 1:
			where = "1 channel"
		case len(w.channels) > 1:
			where = fmt.Sprintf("%d channels", len(w.channels))
		}
	}
	var caught int64
	if v, ok := m.trips.Load(guild); ok {
		caught = v.(*atomic.Int64).Load()
	}
	return core.Report{
		Rows:     [][2]string{{"watching", where}, {"caught", fmt.Sprintf("%d since she woke", caught)}},
		Controls: m.controls(guild),
	}
}

// controls are the channel picker and the everywhere button.
func (m *Module) controls(guild snowflake.ID) []discord.ContainerSubComponent {
	w := m.scope(guild)
	pick := discord.ChannelSelectMenuComponent{CustomID: "spam:watch", Placeholder: "pick the channels she watches", ChannelTypes: watchable, MinValues: new(0), MaxValues: maxWatched}
	for _, ch := range slices.Sorted(maps.Keys(w.channels)) {
		pick.DefaultValues = append(pick.DefaultValues, discord.NewSelectMenuDefaultChannel(ch))
	}
	all := discord.NewSecondaryButton("watch every channel", "spam:all:on")
	if w.all {
		all = discord.NewSecondaryButton("watch only the picked", "spam:all:off")
	}
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay("### where\n-# she watches the channels picked here, or every channel and its threads"),
		discord.NewActionRow(pick),
		discord.NewActionRow(all),
	}
}

// markdown escapes a username, so it reads as typed.
var markdown = strings.NewReplacer(`\`, `\\`, `_`, `\_`, `*`, `\*`, `~`, `\~`, "`", "\\`", `|`, `\|`, `>`, `\>`, `[`, `\[`, `]`, `\]`)
