package pp

import (
	"context"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// The shop is where pp goes: everything bought is burned, nothing in it is
// scarce, and it's priced in pot days. It opens with the prestige ladder,
// whose rungs cost twice the one before, so the members holding the most
// are the ones who choose to spend the most.
//
// The shop is also what keeps the charter's backstop off: each season's
// burn is held against what the season minted, and two seasons under
// Rules.SinkFloor switch the heavy purse line on (a purse past Rules.Line
// pot days keeps Rules.Keep of new earning) until a season burns enough.

var errCantClimb = core.Tell("the next rung costs more than you hold; keep earning and come back")

// rungPrice is what the rung after rung costs: 2, 4, 8... pot days.
func rungPrice(potDay, rung int64) int64 { return potDay << min(rung+1, 40) }

// climb buys user the next rung, burning its price. s.mu held.
func (st *state) climb(s *server, guild, user snowflake.ID, today int64) error {
	m := st.member(s, guild, user, today)
	price := rungPrice(st.potDay, m.rung)
	if m.purse < price {
		return errCantClimb
	}
	m.purse -= price
	m.rung++
	st.burn(s, user, today, reasonShop, price, []int64{m.rung})
	m.dirty = true
	return nil
}

// burn writes pp spent into the ledger and the season's burn. s.mu held.
func (st *state) burn(s *server, user snowflake.ID, today int64, reason string, amount int64, inputs []int64) {
	s.append(user, today, reason, -amount, inputs)
	s.seasonBurn += amount
	s.dirty = true
}

// seasonTurn holds the season just ended to the sink floor and switches
// the heavy purse line on or off for the next. s.mu held.
func (st *state) seasonTurn(s *server) {
	burned := s.seasonBurn * 100 / max(s.seasonMint, 1)
	low := burned < Rules.SinkFloor
	switch {
	case !s.heavy && low && s.sinkLast >= 0 && s.sinkLast < Rules.SinkFloor:
		s.heavy = true
	case s.heavy && !low:
		s.heavy = false
	}
	s.sinkLast, s.seasonMint, s.seasonBurn = burned, 0, 0
}

// shopCard is the shop as a member sees it: the ladder, what the next rung
// costs and what they hold.
func (st *state) shopCard(s *server, m *member) discord.ContainerComponent {
	price := rungPrice(st.potDay, m.rung)
	body := fmt.Sprintf("the prestige ladder: each rung costs twice the last and shows on your status\n```\nrung   %d\nnext   %d pot days · %s pp\npurse  %s pp\n```",
		m.rung, price/st.potDay, grouped(price/unit), grouped(m.purse/unit))
	if s.heavy {
		body += fmt.Sprintf("\n-# the shop has been quiet for two seasons, so a purse past %d pot days keeps a tenth of new earning until it picks up", Rules.Line)
	}
	climb := discord.NewPrimaryButton(fmt.Sprintf("climb to rung %d", m.rung+1), "pp:climb")
	if m.purse < price {
		climb = climb.AsDisabled()
	}
	return brand.Card(brand.ColorNotice, "pp", "shop", body, discord.NewActionRow(climb))
}

// shop opens the shop, or climbs a rung and shows it again.
func (m *Module) shop(_ context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate, climbing bool) error {
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	var err error
	if climbing {
		err = m.st.climb(s, guild, e.User().ID, today)
	}
	card := m.st.shopCard(s, m.st.member(s, guild, e.User().ID, today))
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if climbing {
		return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(card)))
	}
	return e.CreateMessage(discord.MessageCreate{
		Components:      []discord.LayoutComponent{card},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
}

// sinksRow is the season's burn so far and the backstop, for the help
// page. s.mu held.
func sinksRow(s *server) string {
	state := "backstop off"
	if s.heavy {
		state = "backstop on"
	}
	return fmt.Sprintf("%d%% burned · %s", s.seasonBurn*100/max(s.seasonMint, 1), state)
}
