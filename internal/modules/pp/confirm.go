package pp

import (
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// Anything that spends pp asks once more first: the first press answers
// with a small card naming what it buys and a confirm button carrying a
// ticket, and only that confirm buys. A member holds one ticket at a time:
// a new card replaces it, the confirm spends it in the same locked step
// that checks it, and anything else they press or send, or the day
// turning, voids it. So a second card, a double tap, a draft rewritten
// under an open card or a price that moved at 05:30 can't buy what the
// card didn't show. The card is its own reply; the screen under it is left
// as it was.

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
	id, _, _ := confirmed(e.Data.CustomID())
	return id
}

// confirmed splits a confirm's ID into the press it confirms and its
// ticket; ok is false for an ID that's no confirm.
func confirmed(id string) (press string, ticket int64, ok bool) {
	rest, ok := strings.CutSuffix(id, sure)
	if !ok {
		return id, 0, false
	}
	i := strings.LastIndexByte(rest, ':')
	if i < 0 {
		return rest, 0, false
	}
	t, err := strconv.ParseInt(rest[i+1:], 10, 64)
	return rest[:i], t, err == nil
}

// errStale is a confirm whose ticket is spent or void.
var errStale = core.Tell("that's changed since you were asked; look again before buying")

// spend takes the ticket a confirm press carries, and is false when it
// isn't the member's ticket any more: spent, replaced or voided.
func (m *Module) spend(guild snowflake.ID, e *events.ComponentInteractionCreate) bool {
	_, ticket, ok := confirmed(e.Data.CustomID())
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := s.members[e.User().ID]
	if !ok || mem == nil || ticket == 0 || mem.ticket != ticket {
		return false
	}
	mem.ticket = 0
	return true
}

// forget voids user's ticket: they've done something else since.
func (m *Module) forget(guild, user snowflake.ID) {
	s := m.st.server(guild)
	s.mu.Lock()
	if mem := s.members[user]; mem != nil {
		mem.ticket = 0
	}
	s.mu.Unlock()
}

// confirm answers a buying press with the card that asks once more.
func (m *Module) confirm(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	id := e.Data.CustomID()
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, e.User().ID, DayOf(m.now()))
	s.tickets++
	mem.ticket = s.tickets
	ticket := mem.ticket
	s.mu.Unlock()
	what := labelOf(e.Message.Components, id)
	if what == "" {
		what = "this"
	}
	return e.CreateMessage(discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorWarn, "pp", "sure?", what,
			discord.NewActionRow(discord.NewDangerButton("confirm", id+":"+strconv.FormatInt(ticket, 10)+sure)))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
}

// labelOf is the label of the button id in cs, "" for none.
func labelOf(cs []discord.LayoutComponent, id string) string {
	row := func(r discord.ActionRowComponent) string {
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
