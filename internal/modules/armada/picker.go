// SPDX-License-Identifier: AGPL-3.0-only

package armada

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"

	"github.com/6586x57890143/skua/internal/core"
)

// carriable is the channels a webhook can post in.
var carriable = []discord.ChannelType{discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews}

// maxOptions is a string select's limit.
const maxOptions = 25

var (
	errNotAdmin = core.Tell("only this server's admins can pick what she carries")
	errTaken    = core.Tell("that armada channel is already carried into another server")
	errGone     = core.Tell("that armada channel isn't readable anymore; open /help again")
)

// loadLinks reads the picked pairs.
func loadLinks(db DB) ([]*link, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, _ := db.Query(ctx, "select discord_channel_id, guild_id, armada_channel from armada_links")
	var out []*link
	var ch, guild int64
	var arm string
	_, err := pgx.ForEachRow(rows, []any{&ch, &guild, &arm}, func() error {
		l := newLink(snowflake.ID(ch), arm)
		l.guild.Store(uint64(guild))
		out = append(out, l)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("armada: reading the picked links: %w", err)
	}
	return out, nil
}

func (m *Module) linkOn(channel snowflake.ID) (*link, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.byDisc[channel]
	return l, ok
}

func (m *Module) linkList() []*link {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.links)
}

func (m *Module) Components() []core.Component {
	return []core.Component{{ID: "armada", Run: m.press}}
}

// press is the picker on armada's /help page: an armada channel first, then
// the channel here it carries to, or none to stop.
func (m *Module) press(ctx context.Context, e *events.ComponentInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil || m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	id := e.Data.CustomID()
	if id == "armada:pick" {
		data, ok := e.Data.(discord.StringSelectMenuInteractionData)
		if !ok || len(data.Values) != 1 {
			return errNotAdmin
		}
		if _, ok := m.readable(data.Values[0]); !ok {
			return errGone
		}
		// The page keeps both pickers; picking an armada channel points the
		// one below it at that channel, in place.
		fresh := m.controls(*guild, data.Values[0])
		comps, swapped := swapControls(e.Message.Components, fresh)
		if !swapped {
			comps = []discord.LayoutComponent{discord.NewContainer(fresh...)}
			return e.CreateMessage(discord.MessageCreate{Flags: discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
				Components: comps, AllowedMentions: core.NoPings()})
		}
		return e.UpdateMessage(discord.MessageUpdate{Components: &comps})
	}
	arm, ok := strings.CutPrefix(id, "armada:to:")
	data, isPick := e.Data.(discord.ChannelSelectMenuInteractionData)
	if !ok || !isPick {
		return errNotAdmin
	}
	name, ok := m.readable(arm)
	if !ok {
		return errGone
	}
	said := "✓ #" + name + " crosses nowhere"
	if len(data.Values) == 0 {
		if err := m.unlink(ctx, *guild, arm); err != nil {
			return err
		}
	} else {
		if err := m.link(ctx, *guild, data.Values[0], arm); err != nil {
			return err
		}
		said = fmt.Sprintf("✓ <#%s> ↔ #%s", data.Values[0], name)
	}
	return e.CreateMessage(discord.MessageCreate{Content: said, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}

// readable is an armada channel skua can read now, by name.
func (m *Module) readable(arm string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.chans[arm]
	return ch.Name, ok
}

// link pairs channel in guild with arm, dropping whatever pair either side
// was in before. The store goes first, so memory never claims what a
// restart would forget.
func (m *Module) link(ctx context.Context, guild, channel snowflake.ID, arm string) error {
	m.picking.Lock()
	defer m.picking.Unlock()
	var drop []*link
	for _, l := range m.linkList() {
		if l.discord != channel && l.armada != arm {
			continue
		}
		if l.discord == channel && l.armada == arm {
			return nil
		}
		if l.armada == arm && snowflake.ID(l.guild.Load()) != guild {
			return errTaken
		}
		drop = append(drop, l)
	}
	if m.db != nil {
		if _, err := m.db.Exec(ctx, "delete from armada_links where armada_channel = $1 and discord_channel_id <> $2", arm, int64(channel)); err != nil {
			return fmt.Errorf("armada: saving the link: %w", err)
		}
		if _, err := m.db.Exec(ctx, `insert into armada_links (discord_channel_id, guild_id, armada_channel) values ($1, $2, $3)
			on conflict (discord_channel_id) do update set guild_id = excluded.guild_id, armada_channel = excluded.armada_channel`,
			int64(channel), int64(guild), arm); err != nil {
			return fmt.Errorf("armada: saving the link: %w", err)
		}
	}
	l := newLink(channel, arm)
	l.guild.Store(uint64(guild))
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range drop {
		m.dropLocked(d)
	}
	m.links = append(m.links, l)
	m.byDisc[channel] = l
	if m.ready {
		go m.discordWorker(l)
		go m.armadaWorker(l)
	}
	m.subscribeLocked(l)
	return nil
}

// unlink stops arm crossing into guild.
func (m *Module) unlink(ctx context.Context, guild snowflake.ID, arm string) error {
	m.picking.Lock()
	defer m.picking.Unlock()
	links := m.linkList()
	i := slices.IndexFunc(links, func(l *link) bool { return l.armada == arm && snowflake.ID(l.guild.Load()) == guild })
	if i < 0 {
		return nil
	}
	l := links[i]
	if m.db != nil {
		if _, err := m.db.Exec(ctx, "delete from armada_links where discord_channel_id = $1", int64(l.discord)); err != nil {
			return fmt.Errorf("armada: saving the link: %w", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropLocked(l)
	return nil
}

// dropLocked ends l: its stream, its workers and its place. m.mu is held.
func (m *Module) dropLocked(l *link) {
	if cancel := m.subs[l.armada]; cancel != nil {
		cancel()
	}
	delete(m.subs, l.armada)
	delete(m.subKeys, l.armada)
	delete(m.missing, l.armada)
	m.links = slices.DeleteFunc(m.links, func(o *link) bool { return o == l })
	if m.byDisc[l.discord] == l {
		delete(m.byDisc, l.discord)
	}
	close(l.done)
}

// controls is the picker: the armada channels skua can read, the ones
// carried here marked, and below it the channel here that selected crosses
// to. An unset selected is the first carried here, else the first.
func (m *Module) controls(guild snowflake.ID, selected string) []discord.ContainerSubComponent {
	m.mu.RLock()
	chans := make([]struct{ id, name string }, 0, len(m.chans))
	for id, ch := range m.chans {
		chans = append(chans, struct{ id, name string }{id, ch.Name})
	}
	here := map[string]snowflake.ID{}
	for _, l := range m.links {
		if snowflake.ID(l.guild.Load()) == guild {
			here[l.armada] = l.discord
		}
	}
	m.mu.RUnlock()
	if len(chans) == 0 {
		return []discord.ContainerSubComponent{discord.NewTextDisplay("### links\n-# no armada channel is readable yet, so there is nothing to pick")}
	}
	slices.SortFunc(chans, func(a, b struct{ id, name string }) int {
		return cmp.Or(cmp.Compare(a.name, b.name), cmp.Compare(a.id, b.id))
	})
	chans = chans[:min(len(chans), maxOptions)]
	if !slices.ContainsFunc(chans, func(c struct{ id, name string }) bool { return c.id == selected }) {
		selected = chans[0].id
		if i := slices.IndexFunc(chans, func(c struct{ id, name string }) bool { _, ok := here[c.id]; return ok }); i >= 0 {
			selected = chans[i].id
		}
	}
	opts := make([]discord.StringSelectMenuOption, 0, len(chans))
	name := ""
	for _, ch := range chans {
		o := discord.StringSelectMenuOption{Label: "#" + ch.name, Value: ch.id, Default: ch.id == selected}
		if _, ok := here[ch.id]; ok {
			o.Description = "crosses into this server"
		}
		if o.Default {
			name = ch.name
		}
		opts = append(opts, o)
	}
	to := discord.ChannelSelectMenuComponent{CustomID: "armada:to:" + selected, Placeholder: "pick the channel here, or none to stop",
		ChannelTypes: carriable, MinValues: new(0), MaxValues: 1}
	if ch, ok := here[selected]; ok {
		to.DefaultValues = []discord.SelectMenuDefaultValue{discord.NewSelectMenuDefaultChannel(ch)}
	}
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay("### links\n-# pick an armada channel, then the channel here it crosses to"),
		discord.NewActionRow(discord.NewStringSelectMenu("armada:pick", "pick an armada channel", opts...)),
		discord.NewTextDisplay("-# #" + name + " crosses to"),
		discord.NewActionRow(to),
	}
}

// swapControls puts fresh in place of the picker already in comps: the run
// from its "### links" heading through the channel select. It reports
// whether there was one to replace.
func swapControls(comps []discord.LayoutComponent, fresh []discord.ContainerSubComponent) ([]discord.LayoutComponent, bool) {
	out := slices.Clone(comps)
	for i, c := range out {
		box, ok := c.(discord.ContainerComponent)
		if !ok {
			continue
		}
		start, end := -1, -1
		for j, sub := range box.Components {
			if t, ok := sub.(discord.TextDisplayComponent); ok && strings.HasPrefix(t.Content, "### links") {
				start = j
			}
			if row, ok := sub.(discord.ActionRowComponent); ok && start >= 0 && len(row.Components) == 1 {
				if sel, ok := row.Components[0].(discord.ChannelSelectMenuComponent); ok && strings.HasPrefix(sel.CustomID, "armada:to:") {
					end = j
					break
				}
			}
		}
		if start < 0 || end < 0 {
			continue
		}
		box.Components = slices.Concat(box.Components[:start], fresh, box.Components[end+1:])
		out[i] = box
		return out, true
	}
	return comps, false
}
