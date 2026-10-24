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
// with a small card naming what it buys and a confirm button, whose ID is
// the press's, then a token of what the member stood at when asked, then
// sure; only that press buys, and only while the token still holds, so a
// second card or a double tap can't buy the next rung or another week.
// The card is its own reply, so the screen under it is left as it was.

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

// confirmed splits a confirm's ID into the press it confirms and the token
// it was asked under; ok is false for an ID that's no confirm.
func confirmed(id string) (press, token string, ok bool) {
	rest, ok := strings.CutSuffix(id, sure)
	if !ok {
		return id, "", false
	}
	i := strings.LastIndexByte(rest, ':')
	if i < 0 {
		return rest, "", false
	}
	return rest[:i], rest[i+1:], true
}

// buyToken is what a buy depends on for user: the rung for a climb, the
// livery and its last day for the rest. s.mu held.
func buyToken(s *server, user snowflake.ID, id string) string {
	mem := s.members[user]
	if mem == nil {
		return "0"
	}
	if id == "pp:climb" {
		return strconv.FormatInt(mem.rung, 10)
	}
	return strconv.FormatInt(mem.liveryUntil, 10) + "." + strconv.Itoa(len(mem.livery))
}

// errStale is a confirm pressed after what it named has changed.
var errStale = core.Tell("that's changed since you were asked; look again before buying")

// stale is whether a confirmed press no longer names what its card did.
func (m *Module) stale(guild snowflake.ID, e *events.ComponentInteractionCreate) bool {
	press, token, ok := confirmed(e.Data.CustomID())
	if !ok {
		return false
	}
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	return buyToken(s, e.User().ID, press) != token
}

// confirm answers a buying press with the card that asks once more.
func (m *Module) confirm(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	id := e.Data.CustomID()
	s := m.st.server(guild)
	s.mu.Lock()
	token := buyToken(s, e.User().ID, id)
	s.mu.Unlock()
	what := labelOf(e.Message.Components, id)
	if what == "" {
		what = "this"
	}
	return e.CreateMessage(discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorWarn, "pp", "sure?", what,
			discord.NewActionRow(discord.NewDangerButton("confirm", id+":"+token+sure)))},
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
