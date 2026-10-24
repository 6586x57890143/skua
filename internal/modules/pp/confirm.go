package pp

import (
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// Anything that spends pp asks once more first: the first press answers
// with a small card naming what it buys and a confirm button, whose ID is
// the press's with sure on the end; only that press buys. The card is its
// own reply, so the screen under it is left as it was.

// sure marks a buying press as confirmed.
const sure = ":sure"

// buys is whether id spends pp and hasn't been confirmed yet.
func buys(id string) bool {
	if strings.HasSuffix(id, sure) {
		return false
	}
	return id == "pp:climb" || id == "pp:ownsend" || id == "pp:ownweek" || strings.HasPrefix(id, "pp:lease:")
}

// pressed is the button's ID without its confirmation.
func pressed(e *events.ComponentInteractionCreate) string {
	return strings.TrimSuffix(e.Data.CustomID(), sure)
}

// confirm answers a buying press with the card that asks once more.
func (m *Module) confirm(e *events.ComponentInteractionCreate) error {
	id := e.Data.CustomID()
	what := labelOf(e.Message.Components, id)
	if what == "" {
		what = "this"
	}
	return e.CreateMessage(discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorWarn, "pp", "sure?", what,
			discord.NewActionRow(discord.NewDangerButton("confirm", id+sure)))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
}

// labelOf is the label of the button id in cs, "" for none.
func labelOf(cs []discord.LayoutComponent, id string) string {
	var row func(r discord.ActionRowComponent) string
	row = func(r discord.ActionRowComponent) string {
		for _, c := range r.Components {
			if b, ok := c.(discord.ButtonComponent); ok && b.CustomID == id {
				return b.Label
			}
		}
		return ""
	}
	for _, c := range cs {
		switch c := c.(type) {
		case discord.ActionRowComponent:
			if l := row(c); l != "" {
				return l
			}
		case discord.ContainerComponent:
			for _, sc := range c.Components {
				if r, ok := sc.(discord.ActionRowComponent); ok {
					if l := row(r); l != "" {
						return l
					}
				}
			}
		}
	}
	return ""
}
