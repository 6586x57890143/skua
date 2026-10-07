// SPDX-License-Identifier: AGPL-3.0-only

package armada

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/nbd-wtf/go-nostr"

	"github.com/6586x57890143/skua/internal/concord"
)

// publicPool is where skua's public events go: the community's relays and
// the stock ones, which are Armada's default app relays, where its client
// looks for a member's emoji. nil until the first session.
func (m *Module) publicPool() relays {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pub
}

func (m *Module) community() *concord.Community {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.comm
}

// The kinds that give Armada a Discord server's emoji (NIP-30, NIP-51).
const (
	kindEmojiList = 10030 // a member's palette: the packs they use
	kindEmojiPack = 30030 // one pack, addressable by its d tag
)

// packD names the pack skua keeps for a Discord server.
func packD(guild snowflake.ID) string { return "discord-" + guild.String() }

// packTags are a server's custom emoji as a NIP-51 emoji pack. Each one
// goes under its Discord name and its CDN image, the same name and URL a
// bridged message's NIP-30 tag carries, so the emoji in a message and in
// the pack are one emoji to Armada. Discord lets two emoji share a name and
// a pack doesn't, so a later one takes name_2. An emoji the server can't use
// (it lost the boost that held it) is left out.
func packTags(guild snowflake.ID, name, icon string, emojis []discord.Emoji) nostr.Tags {
	tags := nostr.Tags{{"d", packD(guild)}, {"title", name + " emoji"}}
	if icon != "" {
		tags = append(tags, nostr.Tag{"image", icon})
	}
	emojis = slices.Clone(emojis)
	slices.SortFunc(emojis, func(a, b discord.Emoji) int { return cmp.Compare(a.ID, b.ID) }) // oldest keeps the name
	used := map[string]bool{}
	for _, e := range emojis {
		if !e.Available || !emojiName.MatchString(e.Name) {
			continue
		}
		code := e.Name
		for n := 2; used[code]; n++ {
			code = fmt.Sprintf("%s_%d", e.Name, n)
		}
		used[code] = true
		tags = append(tags, nostr.Tag{"emoji", code, emojiURL(e.ID.String(), e.Animated)})
	}
	return tags
}

// guilds is every linked Discord server skua knows, once each.
func (m *Module) guilds() []snowflake.ID {
	var out []snowflake.ID
	for _, l := range m.links {
		if g := snowflake.ID(l.guild.Load()); g != 0 && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// syncPacks publishes each linked server's emoji pack as skua, when it is
// new this run or its emoji changed. An Armada member who taps a Discord
// emoji is offered the pack, and once it is added the whole server's emoji
// are theirs to use, crossing back as the native emoji.
func (m *Module) syncPacks(ctx context.Context) {
	pub := m.publicPool()
	if pub == nil || m.rest == nil {
		return
	}
	for _, guild := range m.guilds() {
		g, err := m.rest.GetGuild(guild, false, rest.WithCtx(ctx))
		var emojis []discord.Emoji
		if err == nil {
			emojis, err = m.rest.GetEmojis(guild, rest.WithCtx(ctx))
		}
		if err != nil {
			m.log.Warn("armada: reading a server's emoji", "guild", guild, "err", err)
			continue
		}
		icon := ""
		if u := g.IconURL(discord.WithSize(256)); u != nil {
			icon = *u
		}
		tags := packTags(guild, g.Name, icon, emojis)
		fp := fmt.Sprint(tags)
		m.mu.RLock()
		same := m.packs[guild] == fp
		m.mu.RUnlock()
		if same {
			continue
		}
		ev := &nostr.Event{Kind: kindEmojiPack, CreatedAt: nostr.Now(), Tags: tags}
		if err := ev.Sign(m.primary.SK); err != nil {
			continue
		}
		if err := pub.Publish(ctx, ev); err != nil {
			m.log.Warn("armada: publishing a server's emoji pack", "guild", guild, "err", err)
			continue
		}
		m.mu.Lock()
		m.packs[guild] = fp
		m.mu.Unlock()
	}
}

// listPacks gives a puppet a kind 10030 naming every linked server's pack.
// Armada looks for a tapped emoji's pack among its sender's lists, so this
// is what turns a Discord emoji in Armada into "add this pack". Published
// again only when the packs change.
func (m *Module) listPacks(ctx context.Context, user string, puppet concord.Key) {
	pub, c := m.publicPool(), m.community()
	if pub == nil {
		return
	}
	hint := ""
	if c != nil && len(c.Relays) > 0 {
		hint = c.Relays[0]
	}
	tags := nostr.Tags{}
	for _, g := range m.guilds() {
		tags = append(tags, nostr.Tag{"a", fmt.Sprintf("%d:%s:%s", kindEmojiPack, m.primary.PK, packD(g)), hint})
	}
	if len(tags) == 0 {
		return
	}
	fp := fmt.Sprint(tags)
	m.mu.RLock()
	same := m.listed[user] == fp
	m.mu.RUnlock()
	if same {
		return
	}
	ev := &nostr.Event{Kind: kindEmojiList, CreatedAt: nostr.Now(), Tags: tags}
	if ev.Sign(puppet.SK) != nil || pub.Publish(ctx, ev) != nil {
		return // tried again on their next message
	}
	m.mu.Lock()
	if len(m.listed) >= 4096 {
		clear(m.listed)
	}
	m.listed[user] = fp
	m.mu.Unlock()
}

// discordName is the name Discord's markup gets for an Armada shortcode.
// An Armada palette renames a shortcode two packs share to
// <pack>-<shortcode>, and Discord names hold no dash, so the part after the
// last one is the emoji's own name. Discord draws the emoji by its id; the
// name is only what the markup shows while it loads.
func discordName(code string) string {
	name := code[strings.LastIndexByte(code, '-')+1:]
	if !emojiName.MatchString(name) {
		return "emoji"
	}
	return name
}
