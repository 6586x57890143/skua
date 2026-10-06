// Package help is /help (and /hjælp), skua's field guide: an index of every running
// module that has a page, and a page per module, in one Components V2
// container you move around with a select menu.
//
// The guide holds no state. The custom ID and the value picked are the whole
// of it, so any guide skua ever sent keeps working across restarts. Picking a
// page on an ephemeral guide turns it in place. A guide an admin posted to the
// channel stays as it is, and the member who clicked gets their own copy of
// the page, so one member's click doesn't change it for everyone else.
package help

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/intents"
)

// Where the index's two link buttons go.
const (
	invite = "https://skua.melting.lol"
	source = "https://github.com/6586x57890143/skua"
)

// id is the one component ID: "help:pick" is the select, "help:index" the
// back button, "help:on:<module>" and "help:off:<module>" the switches.
const id = "help"

// Toggles is which modules are on in which server. A nil one means the
// guide has no switches.
type Toggles interface {
	On(guild snowflake.ID, module string) bool
	Set(ctx context.Context, guild snowflake.ID, module string, on bool) error
}

// fixed can't be switched off: without help nobody could switch anything
// back on, and status is how an admin sees what skua is doing.
var fixed = map[string]bool{"help": true, "status": true}

type Module struct {
	running func() []core.Module
	admin   func(discord.Interaction) bool
	toggles Toggles
}

// New takes the modules that are running, read when the guide is drawn so a
// skipped one is never listed, the router's admin rule, and the switches.
func New(running func() []core.Module, admin func(discord.Interaction) bool, toggles Toggles) *Module {
	return &Module{running: running, admin: admin, toggles: toggles}
}

func (*Module) Name() string { return "help" }

func (*Module) Want() intents.Want { return intents.Want{} }

// Perms is none: the guide is only ever an interaction reply.
func (*Module) Perms() discord.Permissions { return 0 }

// Commands is the guide under two names: /help and the danish /hjælp.
func (m *Module) Commands() []core.Command {
	var out []core.Command
	for _, name := range []string{"help", "hjælp"} {
		out = append(out, core.Command{
			Create: discord.SlashCommandCreate{
				Name:        name,
				Description: "what skua can do and where admins switch her modules",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{Name: "post", Description: "admins: post it in the channel for everyone"},
				},
			},
			Tier: core.Public,
			Run:  m.help,
		})
	}
	return out
}

func (m *Module) Components() []core.Component {
	return []core.Component{{ID: id, Run: m.turn}}
}

func (m *Module) help(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
	post, _ := e.SlashCommandInteractionData().OptBool("post")
	if post && !m.admin(e) {
		return core.Tell("only this server's admins can post the guide; /help on its own shows it to you")
	}
	msg := m.index(e.GuildID())
	if !post {
		msg.Flags = msg.Flags.Add(discord.MessageFlagEphemeral)
	}
	return e.CreateMessage(msg)
}

// turn answers the select, the back button and the switches.
func (m *Module) turn(ctx context.Context, e *events.ComponentInteractionCreate) error {
	guild := e.GuildID()
	msg := m.index(guild)
	action, name, _ := strings.Cut(strings.TrimPrefix(e.Data.CustomID(), id+":"), ":")
	switch action {
	case "on", "off":
		// The custom ID came from the member's client: check it all again,
		// down to the name being a module with a page, so a forged one never
		// reaches the store.
		if m.toggles == nil || guild == nil || fixed[name] || !m.admin(e) {
			return core.Tell("only this server's admins can switch modules")
		}
		if !slices.ContainsFunc(m.entries(), func(en entry) bool { return en.mod.Name() == name }) {
			return core.Tell("that module isn't running anymore; run /help again")
		}
		if err := m.toggles.Set(ctx, *guild, name, action == "on"); err != nil {
			return err
		}
	case "pick":
		if v := e.StringSelectMenuInteractionData().Values; len(v) == 1 {
			name = v[0]
		}
	}
	if action != "index" {
		var ok bool
		if msg, ok = m.page(name, guild, m.admin(e)); !ok {
			return core.Tell("that module isn't running anymore; run /help again")
		}
	}
	if !e.Message.Flags.Has(discord.MessageFlagEphemeral) {
		msg.Flags = msg.Flags.Add(discord.MessageFlagEphemeral)
		return e.CreateMessage(msg)
	}
	// An empty attachment list drops the last page's files; the new page
	// brings exactly the ones it points at.
	return e.UpdateMessage(discord.MessageUpdate{
		Components:      &msg.Components,
		Attachments:     &[]discord.AttachmentUpdate{},
		Files:           msg.Files,
		AllowedMentions: msg.AllowedMentions,
	})
}

type entry struct {
	mod  core.Module
	help core.Help
}

func (m *Module) entries() []entry {
	var out []entry
	for _, mod := range m.running() {
		if h, ok := mod.(core.Helper); ok {
			out = append(out, entry{mod, h.Help()})
		}
	}
	return out
}

// off is whether name is switched off in guild. Outside a server nothing is.
func (m *Module) off(guild *snowflake.ID, name string) bool {
	return m.toggles != nil && guild != nil && !m.toggles.On(*guild, name)
}

// index is compact: one small avatar by the title, then every module in one
// text block, so a page holds as many modules as Discord's text limit allows
// rather than as many thumbnails. Each module is its name on a short line and
// what it does as subtext below, which can wrap on a phone without dragging
// anything out of line.
func (m *Module) index(guild *snowflake.ID) discord.MessageCreate {
	es := m.entries()
	var att files
	commands := 0
	var list strings.Builder
	for _, en := range es {
		commands += len(en.mod.Commands())
		fmt.Fprintf(&list, "%s**%s**", icon(en.mod.Name()), en.mod.Name())
		if m.off(guild, en.mod.Name()) {
			list.WriteString(" · `off here`")
		}
		fmt.Fprintf(&list, "\n-# %s\n", en.help.Line)
	}
	body := []discord.ContainerSubComponent{
		discord.NewSection(
			discord.NewTextDisplay("## skua\n-# stercorarius · small grey · seen in this server"),
		).WithAccessory(discord.NewThumbnail(att.add(brand.Avatar()))),
		discord.NewSmallSeparator(),
		discord.NewTextDisplay(strings.TrimSuffix(list.String(), "\n")),
		discord.NewSmallSeparator(),
		discord.NewTextDisplay(fmt.Sprintf("-# %d modules · %d commands · build `%s` · nothing she sends pings anyone", len(es), commands, core.Revision())),
		discord.NewActionRow(menu(es, "")),
		discord.NewActionRow(discord.NewLinkButton("invite", invite), discord.NewLinkButton("source", source)),
	}
	return message(brand.ColorPrimary, body, att)
}

// page is one module's page, or false when name isn't running. An admin in
// a server also gets the module's switch.
func (m *Module) page(name string, guild *snowflake.ID, admin bool) (discord.MessageCreate, bool) {
	es := m.entries()
	for _, en := range es {
		if en.mod.Name() != name {
			continue
		}
		var att files
		body := []discord.ContainerSubComponent{
			discord.NewSection(
				discord.NewTextDisplay("## " + name + "\n-# " + en.help.Line + "\n" + en.help.About),
			).WithAccessory(discord.NewThumbnail(att.add(brand.ModuleIcon(name, en.help.Color)))),
			discord.NewTextDisplay(grid(en.mod.Commands())),
		}
		if admin && m.toggles != nil && guild != nil && !fixed[name] {
			state, button := "**on** in this server", discord.NewDangerButton("switch off", id+":off:"+name)
			if m.off(guild, name) {
				state, button = "**off** in this server", discord.NewSuccessButton("switch on", id+":on:"+name)
			}
			body = append(body, discord.NewSmallSeparator(),
				discord.NewSection(discord.NewTextDisplay(state+"\n-# off hides its commands and stops it here; other servers keep it")).WithAccessory(button))
		}
		body = append(body,
			discord.NewSmallSeparator(),
			discord.NewActionRow(menu(es, name)),
			discord.NewActionRow(discord.NewSecondaryButton("◂ index", id+":index")),
		)
		return message(en.help.Color, body, att), true
	}
	return discord.MessageCreate{}, false
}

func message(accent int, body []discord.ContainerSubComponent, f files) discord.MessageCreate {
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{discord.NewContainer(body...).WithAccentColor(accent)},
		Files:           f,
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}
}

// menu is the select over every page, with current, if any, marked.
func menu(es []entry, current string) discord.StringSelectMenuComponent {
	opts := make([]discord.StringSelectMenuOption, len(es))
	for i, en := range es {
		opts[i] = discord.StringSelectMenuOption{
			Label:       en.mod.Name(),
			Value:       en.mod.Name(),
			Description: en.help.Line,
			Default:     en.mod.Name() == current,
		}
	}
	return discord.NewStringSelectMenu(id+":pick", "pick a module", opts...)
}

// files collects what a page points at, each file once: two modules can
// wear the same mood.
type files []*discord.File

// icon is module's emoji and a space before its name in the index, or
// nothing until the emoji are synced.
func icon(module string) string {
	if e := brand.Mention("mod_" + module); e != "" {
		return e + " "
	}
	return ""
}

// add keeps file to upload with the reply, once, and returns url. A nil
// file is an icon served from an emoji: nothing to upload.
func (f *files) add(file *discord.File, url string) string {
	if file == nil {
		return url
	}
	for _, have := range *f {
		if have.Name == file.Name {
			return url
		}
	}
	*f = append(*f, file)
	return url
}

// grid is a module's commands as a code block grid (UX.md): the invocation,
// then its options, and who may run it when that isn't everyone.
func grid(cmds []core.Command) string {
	var rows [][2]string
	for _, c := range cmds {
		who := ""
		switch c.Tier {
		case core.Admin:
			who = "admin"
		case core.BreakGlass:
			who = "keeper"
		}
		switch cr := c.Create.(type) {
		case discord.SlashCommandCreate:
			subs := false
			for _, o := range cr.Options {
				if s, ok := o.(discord.ApplicationCommandOptionSubCommand); ok {
					subs = true
					rows = append(rows, [2]string{"/" + cr.Name + " " + s.Name, join(names(s.Options), who)})
				}
			}
			if !subs {
				rows = append(rows, [2]string{"/" + cr.Name, join(names(cr.Options), who)})
			}
		default:
			rows = append(rows, [2]string{c.Create.CommandName(), join("apps menu", who)})
		}
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r[0]))
	}
	var b strings.Builder
	b.WriteString("```\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-*s%s\n", width+2, r[0], r[1])
	}
	b.WriteString("```")
	return b.String()
}

func names(opts []discord.ApplicationCommandOption) string {
	var out []string
	for _, o := range opts {
		out = append(out, o.OptionName())
	}
	return strings.Join(out, ", ")
}

func join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " · " + b
}
