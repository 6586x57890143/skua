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
	price := rungPrice(st.pot(), m.rung)
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

// perchKind is one rung of the rookery stair: the perch, and the text its
// card tells of it the way an old game's item screen does, a plain line on
// what it is, its story and what it does.
type perchKind struct {
	name, icon          string
	what, story, effect string
}

// perches are the rookery stair's rungs. Past the last, each rung is a
// perch out in the nothing itself: see paragon.
var perches = []perchKind{
	{"tallow perch", pixel.Perches[0], "a perch poured from rendered fat",
		"in the first winter of the rookery there was no timber and no stone, only what the flock could spare. they burned their lamps low for a month to pour this one. the birds who sat on it through that dark were the ones who stayed, and it still smells faintly of smoke",
		"your name is set in bold with this perch beside it, on every board and on your status"},
	{"shale perch", pixel.Perches[1], "a perch split from the sea cliff",
		"when the first rookery slid into the sea, the ones left climbed the bare rock above it and cut their perches from the cliff itself. the layers still hold the prints of wings older than any bird alive. the masons say the stone keeps count of everyone who sits on it",
		"your name turns slate, in bold beside your perch, on every board and on your status"},
	{"chitin perch", pixel.Perches[2], "a perch cut from a shed shell",
		"something climbed the stair long before the birds did and left its skin behind on the way up. the shell is warm to the touch and hums in wet weather. the flock cut it into perches and stopped asking what it was, though those who sit on it say it still turns toward the top",
		"your name turns chitin red, in bold beside your perch"},
	{"pale-glass perch", pixel.Perches[3], "a perch of cloudy glass from the mire kilns",
		"the kiln keepers blew it from ash and lamp smoke in the year the mire rose. look down through it and the ground seems further off than it is. a bird on glass can be seen from anywhere in the rookery, which was always the point, and the kiln keepers charged for it",
		"a pale glass plate behind your line, under your red name and perch"},
	{"drowned-bell perch", pixel.Perches[4], "a perch cast from a drowned bell",
		"the chapel bell went under with the rest of the low town. divers brought it up in pieces over three summers, and the bronze was cast into perches for those who had climbed this far. at the hour the water came in it still rings, faintly, and the birds below look up",
		"the plate turns bronze and your name is set in the book hand, beside your perch"},
	{"whalebone perch", pixel.Perches[5], "a perch carved from a whale's jaw",
		"the north sea company once brought home the jaw of a whale longer than a guildhall, and gave it to the order of the stair in place of a debt it could not pay. the order carved it into perches for the gilt. the bone is pale and cold and never quite dry, and on still nights the birds who sit on it say they can hear the sea",
		"your name in gilt on a dark plate, in the book hand, beside your perch"},
	{"perch touched by the great nothing", pixel.Perches[6], "a perch where the stair ran out",
		"past the bell the stair climbed into a place where nothing was. the climbers stepped out and the nothing held them. where it touched the last rung the wood turned to gold. those who sit there stop talking, and the flock knows not to ask what they heard",
		"your name in gilt on a dark plate, framed in gilt; your perch counts every rung past it"},
}

// perchOf is rung n's perch, from 1.
func perchOf(n int64) perchKind {
	if n > int64(len(perches)) {
		return paragon(n)
	}
	return perches[max(n, 1)-1]
}

// paragonNames are the perches past the great nothing, the order of the
// stair naming each rung for what it lacks. Past the last name they come
// round again, the rung's count beside each telling them apart.
var paragonNames = []string{
	"hushed", "unlit", "unsung", "uncounted", "unwritten", "weightless",
	"starless", "breathless", "unrung", "sunless", "boundless", "nameless",
}

// paragon is rung n's perch past the great nothing: each with a name of
// its own, the same story, and the nothing's own icon.
func paragon(n int64) perchKind {
	name := paragonNames[(n-int64(len(perches))-1)%int64(len(paragonNames))] + " perch"
	return perchKind{name, pixel.Perches[len(pixel.Perches)-1], "a perch out in the nothing itself",
		"past the gilded rung there is no stair. a few have climbed on anyway, and the nothing has held them a rung higher each time. the order of the stair names each such perch for what it lacks",
		"gilt framed, on this rung's own shade, your rung counted beside your name"}
}

// perch is rung n's name and icon, from 1.
func perch(n int64) (name, icon string) {
	p := perchOf(n)
	return p.name, p.icon
}

// shopRungs is how many rungs the shop's front shows, from the one under
// the member's.
const shopRungs = 6

// shopMessage is the shop's front as a member sees it: the stair with what
// they hold and what comes next, their purse, a menu to look closer at a
// rung and the button to climb.
func (st *state) shopMessage(s *server, m *member, yard bool) (discord.MessageCreate, error) {
	price := rungPrice(st.pot(), m.rung)
	first := max(1, m.rung-1)
	var rungs []Rung
	look := make([]discord.StringSelectMenuOption, 0, shopRungs)
	for n := first; n < first+shopRungs; n++ {
		name, icon := perch(n)
		g := Rung{N: n, Icon: icon, Name: name, Price: rungPrice(st.pot(), n-1), Held: n <= m.rung, Next: n == m.rung+1}
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
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", "",
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}),
			discord.NewActionRow(discord.NewStringSelectMenu("pp:look", "look closer at a rung", look...)),
			stairRow(climbButton("climb to the "+next, m.purse >= price), yard))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// stairRow is the stair's buttons: climbing, and the way into the dyers'
// yard for whoever may see it.
func stairRow(climb discord.ButtonComponent, yard bool) discord.ActionRowComponent {
	row := discord.NewActionRow(climb)
	if yard {
		row = row.AddComponents(withIcon(discord.NewSecondaryButton("the dyers' yard", "pp:yard"), "scales"))
	}
	return row
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

// itemMessage is rung n's own card: its perch large, its story and what
// it does, what it costs and the member's name as it will look, and the
// way back to the stair with, when it's the next rung, the button to
// climb.
func (st *state) itemMessage(m *member, n int64) (discord.MessageCreate, error) {
	p := perchOf(n)
	look := lookOf(n)
	price := rungPrice(st.pot(), n-1)
	it := Item{Icon: p.icon, Kind: fmt.Sprintf("rung %d", n), Name: p.name, What: p.what, Story: p.story, Effect: p.effect,
		Price: price, Short: n > m.rung && m.purse < price, Climbed: n <= m.rung, You: or(m.name, "you"), Look: &look}
	pic, err := ItemScreen(it)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := fmt.Sprintf("the %s, rung %d of the rookery stair, %s pp: %s. %s. effect: %s", p.name, n, grouped(price/unit), p.what, p.story, p.effect)
	row := discord.NewActionRow(discord.NewSecondaryButton("◂ shop", "pp:back"))
	if n == m.rung+1 {
		row = row.AddComponents(climbButton("climb · "+grouped(price/unit)+" pp", m.purse >= price))
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", "",
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

// shop is a press in the shop: shop and back show its front, climb buys
// the next rung and shows that rung's card, and look shows the rung picked
// in the menu.
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
	yard := m.sees(guild, e)
	s := m.st.server(guild)
	s.mu.Lock()
	var err error
	climbed := false
	if id == "pp:climb" {
		err = m.st.climb(s, guild, e.User().ID, today)
		climbed = err == nil
	}
	mem := m.st.member(s, guild, e.User().ID, today)
	if climbed {
		// What was bought shows straight away: the rung's own card, the
		// member's name already set in its look.
		look = mem.rung
	}
	var msg discord.MessageCreate
	var drawn error
	if look > 0 {
		msg, drawn = m.st.itemMessage(mem, look)
	} else {
		msg, drawn = m.st.shopMessage(s, mem, yard)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if drawn != nil {
		return drawn
	}
	return m.answer(guild, e, msg, "pp:shop")
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
