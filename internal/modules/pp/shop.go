package pp

import (
	"bytes"
	"context"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/pixel"
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

// perchNames are the rookery stair's rungs, as members read them; past the
// last, every rung is a perch nobody built.
var perchNames = []string{"tallow perch", "shale perch", "chitin perch", "pale-glass perch", "drowned-bell perch", "a perch nobody built"}

// perch is rung n's name and icon, from 1.
func perch(n int64) (name, icon string) {
	i := min(int(n), len(perchNames)) - 1
	return perchNames[i], pixel.Perches[i]
}

// shopMessage is the shop as a member sees it: the stair with what they
// hold and what comes next, their purse, and the button to climb.
func (st *state) shopMessage(s *server, m *member) (discord.MessageCreate, error) {
	price := rungPrice(st.potDay, m.rung)
	first := max(1, m.rung-1)
	var rungs []Rung
	for n := first; n < first+6; n++ {
		name, icon := perch(n)
		rungs = append(rungs, Rung{N: n, Icon: icon, Name: name, PotDays: rungPrice(st.potDay, n-1) / st.potDay, Held: n <= m.rung, Next: n == m.rung+1})
	}
	heldName, heldIcon := "", ""
	if m.rung > 0 {
		heldName, heldIcon = perch(m.rung)
	}
	pic, err := ShopScreen(rungs, m.purse, price, heldIcon, heldName)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	next, _ := perch(m.rung + 1)
	alt := fmt.Sprintf("the rookery stair: you sit on %s, the %s costs %s pp and you hold %s pp", or(heldName, "the ground"), next, grouped(price/unit), grouped(m.purse/unit))
	note := "-# each rung costs twice the one before; everything bought is burned"
	if s.heavy {
		note += fmt.Sprintf("\n-# the shop has been quiet for two seasons, so a purse past %d pot days keeps a tenth of new earning until it picks up", Rules.Line)
	}
	climb := discord.NewPrimaryButton("climb to the "+next, "pp:climb")
	if m.purse < price {
		climb = climb.AsDisabled()
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", note,
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}),
			discord.NewActionRow(climb))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// or is s, or else when it's empty.
func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
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
	msg, drawn := m.st.shopMessage(s, m.st.member(s, guild, e.User().ID, today))
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if drawn != nil {
		return drawn
	}
	if climbing {
		return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...))
	}
	return e.CreateMessage(msg)
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
