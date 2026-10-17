package pp

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

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

// perchLore is what each perch's card says of it: the stair's story, told
// the way an old game's item text tells it. Past the last, every rung is
// a perch nobody built and says the same.
var perchLore = []string{
	"poured from the fat of a winter that went on too long. it softens in the sun and keeps the print of every foot that waited on it",
	"split from the cliff where the first rookery fell. its layers still hold the shapes of wings older than anyone's",
	"the cast shell of something that climbed here before birds did. it hums in wet weather and nobody asks it why",
	"blown in the mire kilns from ash and lamp smoke. look down through it and the ground seems further off than it is",
	"bronze from a bell pulled out of the sunk chapel. sit very still at the hour the water came in and it rings",
	"it was there when the stair reached this high, with no maker's mark and no nails. those who sit on it stop talking about it",
}

// perch is rung n's name and icon, from 1.
func perch(n int64) (name, icon string) {
	i := min(int(n), len(perchNames)) - 1
	return perchNames[i], pixel.Perches[i]
}

// shopRungs is how many rungs the shop's front shows, from the one under
// the member's.
const shopRungs = 6

// shopMessage is the shop's front as a member sees it: the stair with what
// they hold and what comes next, their purse, a menu to look closer at a
// rung and the button to climb.
func (st *state) shopMessage(s *server, m *member) (discord.MessageCreate, error) {
	price := rungPrice(st.potDay, m.rung)
	first := max(1, m.rung-1)
	var rungs []Rung
	look := make([]discord.StringSelectMenuOption, 0, shopRungs)
	for n := first; n < first+shopRungs; n++ {
		name, icon := perch(n)
		g := Rung{N: n, Icon: icon, Name: name, Price: rungPrice(st.potDay, n-1), Held: n <= m.rung, Next: n == m.rung+1}
		rungs = append(rungs, g)
		o := discord.NewStringSelectMenuOption(name, strconv.FormatInt(n, 10))
		o.Description = fmt.Sprintf("rung %d · %s pp", n, grouped(g.Price/unit))
		if g.Held {
			o.Description = fmt.Sprintf("rung %d · climbed", n)
		}
		look = append(look, o)
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
		note += fmt.Sprintf("\n-# the shop has been quiet for two seasons, so a purse past %s pp keeps a tenth of new earning until it picks up", grouped(Rules.Line*st.potDay/unit))
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", note,
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}),
			discord.NewActionRow(discord.NewStringSelectMenu("pp:look", "look closer at a rung", look...)),
			discord.NewActionRow(climbButton("climb to the "+next, m.purse >= price)))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// climbButton buys the next rung; it's greyed out until the member can
// pay for it.
func climbButton(label string, can bool) discord.ButtonComponent {
	b := withIcon(discord.NewPrimaryButton(label, "pp:climb"), "pot")
	if !can {
		b = b.AsDisabled()
	}
	return b
}

// itemMessage is rung n's own card: its perch large with its name and
// lore, what it costs against the purse, what it does in subtext, and the
// way back to the stair with, when it's the next rung, the button to
// climb.
func (st *state) itemMessage(m *member, n int64) (discord.MessageCreate, error) {
	name, icon := perch(n)
	it := Item{Icon: icon, Name: name, Lore: perchLore[min(int(n), len(perchLore))-1], Kind: fmt.Sprintf("rung %d", n),
		Price: rungPrice(st.potDay, n-1), Purse: m.purse}
	switch {
	case n <= m.rung:
		it.State = "climbed"
	case n == m.rung+1:
		it.State = "next"
	}
	pic, err := ItemScreen(it)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := fmt.Sprintf("the %s, rung %d of the rookery stair: %s. it costs %s pp", name, n, it.Lore, grouped(it.Price/unit))
	what := fmt.Sprintf("-# rung %d of the prestige ladder: /pp names the highest rung you've climbed. each rung costs twice the one before and what you pay is burned", n)
	row := discord.NewActionRow(discord.NewSecondaryButton("◂ shop", "pp:back"))
	if it.State == "next" {
		row = row.AddComponents(climbButton("climb · "+grouped(it.Price/unit)+" pp", m.purse >= it.Price))
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", what,
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}),
			row)},
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

// errNoRung is a look at a rung the menu never offered.
var errNoRung = core.Tell("that rung isn't on the stair; open the shop again")

// shop is a press in the shop: shop opens its front as a new message,
// climb buys the next rung and shows the front again, back shows the
// front again and look shows the rung picked in the menu.
func (m *Module) shop(_ context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	id := e.Data.CustomID()
	look := int64(0)
	if id == "pp:look" {
		// The custom ID comes from the client, so the data may not be a
		// select's at all.
		data, ok := e.Data.(discord.StringSelectMenuInteractionData)
		if !ok || len(data.Values) != 1 {
			return errNoRung
		}
		n, err := strconv.ParseInt(data.Values[0], 10, 64)
		if err != nil || n < 1 || n > 64 {
			return errNoRung
		}
		look = n
	}
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	var err error
	if id == "pp:climb" {
		err = m.st.climb(s, guild, e.User().ID, today)
	}
	mem := m.st.member(s, guild, e.User().ID, today)
	var msg discord.MessageCreate
	var drawn error
	if look > 0 {
		msg, drawn = m.st.itemMessage(mem, look)
	} else {
		msg, drawn = m.st.shopMessage(s, mem)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if drawn != nil {
		return drawn
	}
	if id == "pp:shop" {
		return e.CreateMessage(msg)
	}
	return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...))
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
