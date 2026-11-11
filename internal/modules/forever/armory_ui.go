package forever

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// screens keeps each character's last armory screen with the profile it
// was drawn from, so a screen is drawn once per logout however often it is
// opened or shared.
type screens struct {
	mu   sync.Mutex
	have map[key]drawn
}

// drawn is a screen at 1x, as a PNG at best compression: a few KB a
// character, scaled up only when it's sent.
type drawn struct {
	modified string
	png      []byte
}

// screenOf is c's armory screen as Discord shows it, drawn again only when
// its profile has changed since or the last one was drawn short.
func (m *Module) screenOf(ctx context.Context, c char) ([]byte, error) {
	m.screens.mu.Lock()
	d, ok := m.screens.have[c.key]
	m.screens.mu.Unlock()
	if ok && c.modified != "" && d.modified == c.modified {
		if img, err := png.Decode(bytes.NewReader(d.png)); err == nil {
			return encode(img)
		}
	}
	img, whole, err := m.armory(ctx, c)
	if err != nil {
		return nil, err
	}
	if whole {
		var b bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, img); err == nil {
			m.screens.mu.Lock()
			m.screens.have[c.key] = drawn{modified: c.modified, png: b.Bytes()}
			m.screens.mu.Unlock()
		}
	}
	return encode(img)
}

// parseValue reads a select's "eu.7177273" back, from the member's client.
func parseValue(v string) (key, bool) {
	region, idStr, ok := strings.Cut(v, ".")
	n, err := strconv.ParseInt(idStr, 10, 64)
	if !ok || err != nil || !validRegion(region) {
		return key{}, false
	}
	return key{region, n}, true
}

// option is c in a select.
func option(c char) discord.StringSelectMenuOption {
	return discord.StringSelectMenuOption{Label: fullName(c), Value: value(c), Description: "lv " + itoa(c.level) + " · " + c.region + " " + c.ruleset, Emoji: brand.ComponentEmoji("fv_" + c.class)}
}

// options is chars in a select.
func options(chars []*char) []discord.StringSelectMenuOption {
	out := make([]discord.StringSelectMenuOption, 0, len(chars))
	for _, c := range chars {
		out = append(out, option(*c))
	}
	return out
}

// listed is the character v names ("eu.7177273"), if guild lists it.
func (m *Module) listed(guild snowflake.ID, v string) (char, bool) {
	k, ok := parseValue(v)
	if !ok {
		return char{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.chars[k]
	if ch == nil || !slices.ContainsFunc(m.claims, func(c claim) bool { return c.guild == guild && c.k == k }) {
		return char{}, false
	}
	return *ch, true
}

// value is how a select names c: "eu.7177273".
func value(c char) string { return c.region + "." + strconv.FormatInt(c.id, 10) }

// armoryPick is an "armory for..." select over chars.
func armoryPick(chars []*char) discord.ActionRowComponent {
	return discord.NewActionRow(discord.NewStringSelectMenu(id+":armory", "armory for...", options(chars)...))
}

// armoryCard is the screen in a card: forever's head, who it is, the
// picture, and the share button until it's shared, in the class's colour.
func armoryCard(c char, pic []byte, shared bool) (discord.ContainerComponent, *discord.File) {
	head := discord.NewTextDisplay(headLine("armory") + "\n-# " + fullName(c) + " · lv " + itoa(c.level) + " · " + where(c))
	alt := fullName(c) + ", level " + itoa(c.level) + " " + c.race + " " + c.class + ", and what they wear"
	parts := []discord.ContainerSubComponent{head, discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://armory.png"}, Description: alt})}
	if !shared {
		parts = append(parts, discord.NewActionRow(button(discord.ButtonStyleSecondary, "share", id+":share:"+value(c), "fv_share")))
	}
	return discord.NewContainer(parts...).WithAccentColor(accent(c.class)), discord.NewFile("armory.png", alt, bytes.NewReader(pic))
}

// showArmory answers a pick from an "armory for..." select: the screen,
// for the member's eyes, with a button to share it.
func (m *Module) showArmory(ctx context.Context, e *events.ComponentInteractionCreate, guild snowflake.ID) error {
	vals := e.StringSelectMenuInteractionData().Values
	if len(vals) != 1 {
		return core.Tell("pick a character")
	}
	c, ok := m.listed(guild, vals[0])
	if !ok {
		return core.Tell("that character isn't listed here any more")
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	r := e.Client().Rest
	pic, err := m.screenOf(ctx, c)
	if err != nil {
		_, err = r.UpdateInteractionResponse(e.ApplicationID(), e.Token(), update(m.undrawn(c, err)), rest.WithCtx(ctx))
		return err
	}
	card, file := armoryCard(c, pic, false)
	_, err = r.UpdateInteractionResponse(e.ApplicationID(), e.Token(), core.Edit(update(card), file), rest.WithCtx(ctx))
	return err
}

// share posts a character's armory screen in the channel for everyone, as
// skua, and tells the member it's there.
func (m *Module) share(ctx context.Context, e *events.ComponentInteractionCreate, guild snowflake.ID, v string) error {
	c, ok := m.listed(guild, v)
	if !ok {
		return core.Tell("that character isn't listed here any more")
	}
	// The press edits the card it came from: once shared, it has no share
	// button left to press again.
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	r := e.Client().Rest
	say := func(c discord.ContainerComponent) error {
		_, err := r.CreateFollowupMessage(e.ApplicationID(), e.Token(), discord.MessageCreate{
			Components: []discord.LayoutComponent{c}, Flags: discord.MessageFlagIsComponentsV2 | discord.MessageFlagEphemeral, AllowedMentions: core.NoPings(),
		}, rest.WithCtx(ctx))
		return err
	}
	pic, err := m.screenOf(ctx, c)
	if err != nil {
		return say(m.undrawn(c, err))
	}
	if err := m.guard.Allow(guild, guard.MessageSend); err != nil {
		return say(brand.Card(brand.ColorError, Name, "armory", "✗ skua has posted a lot here this hour; try again in a bit"))
	}
	card, file := armoryCard(c, pic, true)
	_, err = r.CreateMessage(e.Channel().ID(), discord.MessageCreate{
		Components: []discord.LayoutComponent{card}, Files: []*discord.File{file},
		Flags: discord.MessageFlagIsComponentsV2, AllowedMentions: core.NoPings(),
	}, rest.WithCtx(ctx))
	m.guard.Done(guild, err)
	if err != nil {
		m.log.Warn("forever: sharing an armory", "err", err)
		return say(brand.Card(brand.ColorError, Name, "armory", "✗ skua couldn't post here; she needs to send messages and attach files in this channel"))
	}
	mine, file := armoryCard(c, pic, true)
	_, err = r.UpdateInteractionResponse(e.ApplicationID(), e.Token(), core.Edit(update(mine), file), rest.WithCtx(ctx))
	return err
}

// undrawn is why c's armory couldn't be drawn, as a card.
func (m *Module) undrawn(c char, err error) discord.ContainerComponent {
	text := "✗ blizzard's armory didn't answer; try again in a moment"
	if errors.Is(err, errNoGear) {
		text = "✗ blizzard doesn't show " + fullName(c) + "'s gear yet; it does after their next logout"
	} else {
		m.log.Warn("forever: drawing an armory", "err", err)
	}
	return brand.Card(brand.ColorError, Name, "armory", text)
}
