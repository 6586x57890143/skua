package notify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// The panel's custom IDs. "notify:bind" is the channel select, "notify:add"
// the platform select that opens the follow form, "notify:drop" the select
// that removes follows, and "notify-follow:<platform>" the form.
const (
	id       = "notify"
	followID = "notify-follow"
)

// textChannels are where a card can go.
var textChannels = []discord.ChannelType{discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews}

// Commands is /notify: the panel. Everything else is done on it.
func (m *Module) Commands() []core.Command {
	return []core.Command{{
		Create: discord.SlashCommandCreate{
			Name:        "notify",
			Description: "who this server follows, and where their cards go",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		},
		Tier: core.Admin,
		Run:  m.open,
	}}
}

func (m *Module) Components() []core.Component {
	return []core.Component{{ID: id, Run: m.click}}
}

func (m *Module) Modals() []core.Modal {
	return []core.Modal{{ID: followID, Run: m.submit}}
}

func (m *Module) open(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return core.Tell("notify only works in a server")
	}
	msg := m.panel(*guild, "")
	msg.Flags = msg.Flags.Add(discord.MessageFlagEphemeral)
	return e.CreateMessage(msg)
}

// click is the panel's three selects. The custom ID and values come from
// the member's client, so who they are and what they picked are checked
// again here.
func (m *Module) click(ctx context.Context, e *events.ComponentInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil || !m.admin(e) {
		return core.Tell("only this server's admins can change what it follows")
	}
	switch strings.TrimPrefix(e.Data.CustomID(), id+":") {
	case "bind":
		ch := e.ChannelSelectMenuInteractionData().Values
		if len(ch) != 1 {
			return core.Tell("pick one channel")
		}
		if err := saveBound(ctx, m.db, *guild, ch[0]); err != nil {
			return err
		}
		m.mu.Lock()
		m.bound[*guild] = ch[0]
		m.mu.Unlock()
		return m.redraw(e, *guild, "✓ cards go to <#"+ch[0].String()+">")
	case "add":
		v := e.StringSelectMenuInteractionData().Values
		if len(v) != 1 {
			return core.Tell("pick a platform")
		}
		if _, ok := m.sources[v[0]]; !ok {
			return core.Tell("skua isn't set up for " + line(v[0], 20) + " here")
		}
		return e.Modal(form(v[0]))
	case "drop":
		return m.drop(ctx, e, *guild, e.StringSelectMenuInteractionData().Values)
	}
	return core.Tell("that panel is out of date; run /notify again")
}

func (m *Module) redraw(e *events.ComponentInteractionCreate, guild snowflake.ID, note string) error {
	return e.UpdateMessage(m.update(guild, note))
}

// form asks who to follow, and optionally where and whom to ping.
func form(platform string) discord.ModalCreate {
	min0 := 0
	return discord.ModalCreate{
		CustomID: followID + ":" + platform,
		Title:    "follow someone on " + platform,
		Components: []discord.LayoutComponent{
			discord.NewLabel("account", discord.TextInputComponent{
				CustomID: "account", Style: discord.TextInputStyleShort, Required: true, MaxLength: 100,
				Placeholder: "@handle or a link to them",
			}),
			discord.NewLabel("channel", discord.ChannelSelectMenuComponent{
				CustomID: "channel", ChannelTypes: textChannels, MinValues: &min0, MaxValues: 1,
				Placeholder: "this server's notify channel",
			}),
			discord.NewLabel("ping", discord.RoleSelectMenuComponent{
				CustomID: "role", MinValues: &min0, MaxValues: 1, Placeholder: "nobody",
			}),
		},
	}
}

// submit follows the account the form names. Looking it up can take a
// few seconds, so the panel is deferred and redrawn once it's done.
func (m *Module) submit(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil || !m.admin(e) {
		return core.Tell("only this server's admins can change what it follows")
	}
	platform := strings.TrimPrefix(e.Data.CustomID, followID+":")
	src, ok := m.sources[platform]
	if !ok {
		return core.Tell("skua isn't set up for " + line(platform, 20) + " here")
	}
	f := follow{guild: *guild, platform: platform}
	if ch, _ := e.Data.OptChannels("channel"); len(ch) == 1 {
		f.channel = ch[0].ID
	}
	if roles, _ := e.Data.OptRoles("role"); len(roles) == 1 {
		r := roles[0]
		switch {
		case r.ID == *guild:
			return core.Tell("skua never pings everyone; pick a role")
		case !r.Mentionable:
			return core.Tell("@" + r.Name + " can't be mentioned; turn on allow anyone to @mention this role, or pick another")
		}
		f.role = r.ID
	}
	m.mu.Lock()
	n := 0
	for _, o := range m.follows {
		if o.guild == *guild {
			n++
		}
	}
	unbound := f.channel == 0 && m.bound[*guild] == 0
	m.mu.Unlock()
	switch {
	case n >= perGuild:
		return core.Tell(fmt.Sprintf("this server follows %d accounts, which is the most it can; drop one first", perGuild))
	case unbound:
		return core.Tell("pick where cards go first, on the panel or in the form")
	}

	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	typed := e.Data.Text("account")
	acct, name, err := src.resolve(ctx, typed)
	switch {
	case errors.Is(err, errUnknown):
		return core.Tell("skua can't find " + line(typed, 40) + " on " + platform)
	case err != nil:
		return fmt.Errorf("%w: %w", core.Tell(platform+" isn't answering skua right now; try again in a few minutes"), err)
	}
	f.account, f.name = acct, name
	if err := saveFollow(ctx, m.db, f); err != nil {
		return err
	}
	m.mu.Lock()
	m.follows = slices.DeleteFunc(m.follows, func(o follow) bool { return o.same(f) })
	m.follows = append(m.follows, f)
	m.mu.Unlock()
	go m.subscribe(platform)

	_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), m.update(*guild, "✓ following "+name+" on "+platform), rest.WithCtx(ctx))
	return err
}

// same is whether two follows are one row: an account into one place.
func (f follow) same(o follow) bool {
	return f.guild == o.guild && f.platform == o.platform && f.account == o.account && f.channel == o.channel
}

// value is how a follow is named in the drop select. Discord allows 100
// characters; an account is at most 40 and a snowflake 20.
func (f follow) value() string {
	return f.platform + ":" + f.account + ":" + f.channel.String()
}

func (m *Module) drop(ctx context.Context, e *events.ComponentInteractionCreate, guild snowflake.ID, values []string) error {
	m.mu.Lock()
	var gone []follow
	for _, f := range m.follows {
		if f.guild == guild && slices.Contains(values, f.value()) {
			gone = append(gone, f)
		}
	}
	m.mu.Unlock()
	if len(gone) == 0 {
		return m.redraw(e, guild, "")
	}
	var names, keys = []string{}, []key{}
	for _, f := range gone {
		if err := dropFollow(ctx, m.db, f); err != nil {
			return err
		}
		names = append(names, f.name)
		keys = append(keys, key{f.platform, f.account})
	}
	m.mu.Lock()
	m.follows = slices.DeleteFunc(m.follows, func(f follow) bool {
		return slices.ContainsFunc(gone, f.same)
	})
	m.mu.Unlock()
	m.forget(ctx, keys...)
	for _, k := range keys {
		go m.subscribe(k.platform)
	}
	return m.redraw(e, guild, "✓ dropped "+strings.Join(names, ", "))
}

// update redraws the panel in place. An empty attachment list drops the
// icon the last draw sent, and the draw brings it again if it still needs it.
func (m *Module) update(guild snowflake.ID, note string) discord.MessageUpdate {
	msg := m.panel(guild, note)
	return discord.MessageUpdate{Components: &msg.Components, Attachments: &[]discord.AttachmentUpdate{}, Files: msg.Files, AllowedMentions: msg.AllowedMentions}
}

// panel is /notify, laid out like a /help page: the module's icon and line,
// where cards go, who's followed, and the selects that change both. note
// says what the last click did.
func (m *Module) panel(guild snowflake.ID, note string) discord.MessageCreate {
	m.mu.Lock()
	bound := m.bound[guild]
	var mine []follow
	for _, f := range m.follows {
		if f.guild == guild {
			mine = append(mine, f)
		}
	}
	failed := map[snowflake.ID]string{}
	for _, f := range mine {
		if why, ok := m.failed[m.dest(f)]; ok {
			failed[m.dest(f)] = why
		}
	}
	m.mu.Unlock()
	slices.SortFunc(mine, func(a, b follow) int { return strings.Compare(a.platform+a.name, b.platform+b.name) })

	icon, iconURL := brand.ModuleIcon("notify", brand.ColorNotice)
	var files []*discord.File
	if icon != nil {
		files = append(files, icon)
	}
	head := "## notify\n-# tells a channel when someone posts or goes live"
	if note != "" {
		head += "\n" + note
	}
	where := "**cards go to** <#" + bound.String() + ">"
	if bound == 0 {
		where = "! **no channel yet**: pick where cards go"
	}
	bind := discord.ChannelSelectMenuComponent{CustomID: id + ":bind", Placeholder: "pick where cards go", ChannelTypes: textChannels, MaxValues: 1}
	if bound != 0 {
		bind.DefaultValues = []discord.SelectMenuDefaultValue{discord.NewSelectMenuDefaultChannel(bound)}
	}
	body := []discord.ContainerSubComponent{
		discord.NewSection(discord.NewTextDisplay(head)).WithAccessory(discord.NewThumbnail(iconURL)),
		discord.NewSmallSeparator(),
		discord.NewTextDisplay(where),
		discord.NewActionRow(bind),
		discord.NewSmallSeparator(),
		discord.NewTextDisplay(following(mine, failed)),
	}
	var add []discord.StringSelectMenuOption
	for _, p := range m.platforms() {
		add = append(add, discord.StringSelectMenuOption{Label: p, Value: p, Description: does(p), Emoji: brand.ComponentEmoji("pf_" + p)})
	}
	if len(mine) < perGuild {
		body = append(body, discord.NewActionRow(discord.NewStringSelectMenu(id+":add", "follow someone on...", add...)))
	}
	if len(mine) > 0 {
		var opts []discord.StringSelectMenuOption
		for _, f := range mine {
			opts = append(opts, discord.StringSelectMenuOption{Label: f.name, Value: f.value(), Description: f.platform, Emoji: brand.ComponentEmoji("pf_" + f.platform)})
		}
		menu := discord.NewStringSelectMenu(id+":drop", "stop following...", opts...)
		menu.MaxValues = len(opts)
		body = append(body, discord.NewActionRow(menu))
	}
	body = append(body, discord.NewTextDisplay(fmt.Sprintf("-# %d of %d · %s · a card only pings the role you give it", len(mine), perGuild, strings.Join(m.platforms(), ", "))))
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{discord.NewContainer(body...).WithAccentColor(brand.ColorNotice)},
		Files:           files,
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}
}

// following is the list: each follow with its platform's icon, then where
// its cards go when that isn't the server's channel, and whom they ping.
func following(fs []follow, failed map[snowflake.ID]string) string {
	if len(fs) == 0 {
		return "nobody followed yet; pick a platform below"
	}
	var b strings.Builder
	for _, f := range fs {
		mark := brand.Mention("pf_" + f.platform)
		if mark == "" {
			mark = "▸"
		}
		fmt.Fprintf(&b, "%s **%s** · %s", mark, f.name, f.platform)
		var sub []string
		if f.channel != 0 {
			sub = append(sub, "<#"+f.channel.String()+">")
		}
		if f.role != 0 {
			sub = append(sub, "<@&"+f.role.String()+">")
		}
		if len(sub) > 0 {
			b.WriteString("\n-# → " + strings.Join(sub, " · "))
		}
		b.WriteString("\n")
	}
	for ch, why := range failed {
		fmt.Fprintf(&b, "! <#%s>: %s\n", ch, why)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// does is what following someone on p brings.
func does(p string) string {
	switch p {
	case "youtube":
		return "uploads and streams"
	case "twitch", "kick":
		return "when they go live"
	}
	return "new posts"
}
