// Package games is skua's games module: the Discord half of the games
// built on internal/game, every bird game in one place. Counting is the
// first. A game decides in internal/game and this package does the
// talking: it reacts, posts the cards a channel should see and keeps what
// was played in Postgres.
//
// A counting channel is set up on the games /help page by an admin, and
// each keeps its own rules. A message in one goes to the engine as it
// arrives, in the order and at the time Discord's ids give it, and its
// reaction is sent without waiting on anything else; state is written
// behind a few times a second.
package games

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/game"
	"github.com/6586x57890143/skua/internal/game/count"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// Name is the module's; Name() repeats it as a literal, which brand's icon
// test reads.
const Name = "games"

// maxChannels is how many counting channels a server may have.
const maxChannels = 3

// DB is where the games keep what was played.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Module is the games module.
type Module struct {
	log   *slog.Logger
	guard *guard.Guard
	db    DB // nil keeps everything in memory
	now   func() time.Time
	// Admin is the router's check, for the controls on the /help page.
	Admin func(discord.Interaction) bool

	en      count.Engine
	chans   game.Dirty[uint64]
	members game.Dirty[count.Who]
	mu      sync.Mutex // the journal and setup changes
	fmu     sync.Mutex // one write at a time, so an older one never lands last
	journal journal

	reactors sync.Map // channel -> *reactor
	refused  sync.Map // channel -> time.Time, reactions refused until
	catching sync.Map // channel -> *backlog, while skua reads what she missed
	lost     sync.Map // channel -> struct{}, picking up from the next count
	noticed  sync.Map // channel -> snowflake.ID, the count last called out
	bmu      sync.Mutex
	breaks   map[count.Who][]time.Time // timeouts served in the last week
	wg       sync.WaitGroup
}

// New loads every counting channel and member. db is nil without a
// database, and then the games last as long as the process.
func New(ctx context.Context, log *slog.Logger, g *guard.Guard, db DB) (*Module, error) {
	m := &Module{log: log, guard: g, db: db, now: time.Now}
	if db == nil {
		return m, nil
	}
	if err := m.load(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (*Module) Name() string { return "games" }

// Want is the messages a game is played in and what they say.
func (*Module) Want() intents.Want {
	return intents.Want{Required: gateway.IntentGuildMessages | gateway.IntentMessageContent}
}

// Perms is reacting to a count, reading the history it reacts in and
// what she misses while away, and saying when a run breaks.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionSendMessages | discord.PermissionAddReactions | discord.PermissionReadMessageHistory
}

func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorInfo,
		Line:  "counting together, and the games to come",
		About: "an admin picks up to three channels on this page for counting, each with its own rules. members count up one number a message and nobody counts twice in a row. a wrong number ends the run unless a save covers it: everyone earns one for every hundred of their own counts, and the channel earns one for every 250 in a run. two people sending the same number at once is a tie, not a miss, and she reads back whatever was counted while she was away.\n\n" +
			"the rulesets are standard, hardcore with no saves, lives with three misses a run, solo where one member may count alone with a cooldown, brain where every count is a sum, and fizzbuzz, binary, hex and roman. the arcade layer adds combos, a hidden bounty number after every hundred, a boss stretch of ten different members before every thousand and levels. it never changes the count. a run can also time out whoever breaks it, doubling with each break in a week, for anyone discord lets her time out. every run is a sha256 chain, so a result can be checked.\n\n" +
			"**/count** shows the run, the board for the month and all time and where you stand, with a proof that checks the run link by link. **/recount** starts a channel from a number, for a server moving from another bot",
	}
}

func (m *Module) Components() []core.Component {
	return []core.Component{{ID: "games", Run: m.press}}
}

var (
	errNotAdmin = core.Tell("only this server's admins can set up the games")
	errNoGame   = core.Tell("that channel doesn't count anymore; open the page again")
)

// options are the switches a counting channel has on top of its preset.
var options = []struct{ value, label, about string }{
	{"arcade", "arcade", "combos, bounties, the boss stretch and levels"},
	{"solo", "solo", "one member may count twice in a row"},
	{"timeout", "timeouts", "the breaker sits out 10m, doubling; needs moderate members"},
}

// press is a button on a /count card, anyone's, or a control on the
// games /help page, admins only.
func (m *Module) press(ctx context.Context, e *events.ComponentInteractionCreate) error {
	parts := strings.Split(e.Data.CustomID(), ":")
	if len(parts) >= 3 && slices.Contains([]string{"view", "board", "proof", "file"}, parts[1]) {
		return m.open(ctx, e, parts)
	}
	guild := e.GuildID()
	if guild == nil || m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	var said string
	switch {
	case len(parts) == 2 && parts[1] == "channels":
		data, ok := e.Data.(discord.ChannelSelectMenuInteractionData)
		if !ok {
			return errNotAdmin
		}
		if err := m.pick(ctx, *guild, data.Values); err != nil {
			return err
		}
		said = "✓ counting nowhere"
		if len(data.Values) > 0 {
			names := make([]string, 0, len(data.Values))
			for _, ch := range data.Values[:min(len(data.Values), maxChannels)] {
				names = append(names, "<#"+ch.String()+">")
			}
			said = "✓ counting in " + strings.Join(names, " ")
		}
	case len(parts) == 3 && (parts[1] == "rules" || parts[1] == "opts"):
		ch, err := snowflake.Parse(parts[2])
		if err != nil {
			return errNoGame
		}
		data, ok := e.Data.(discord.StringSelectMenuInteractionData)
		if !ok {
			return errNotAdmin
		}
		r, err := m.rules(ctx, *guild, ch, parts[1], data.Values)
		if err != nil {
			return err
		}
		said = fmt.Sprintf("✓ <#%s> counts by %s rules", ch, r.Name)
		if parts[1] == "opts" {
			said = fmt.Sprintf("✓ <#%s> runs with %s", ch, describe(r))
		}
	default:
		return errNotAdmin
	}
	return e.CreateMessage(discord.MessageCreate{Content: said, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}

// pick makes channels the guild's counting channels: new ones start on the
// standard rules, ones dropped stop counting and keep their history.
func (m *Module) pick(ctx context.Context, guild snowflake.ID, channels []snowflake.ID) error {
	channels = channels[:min(len(channels), maxChannels)]
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := map[uint64]bool{}
	for _, ch := range channels {
		keep[uint64(ch)] = true
	}
	for _, ch := range m.counting(guild) {
		if keep[ch] {
			continue
		}
		if m.db != nil {
			if _, err := m.db.Exec(ctx, "delete from count_channel where channel_id = $1", int64(ch)); err != nil {
				return fmt.Errorf("games: dropping a counting channel: %w", err)
			}
		}
		m.en.Close(ch)
	}
	for _, ch := range channels {
		if _, ok := m.en.Channel(uint64(ch)); ok {
			continue
		}
		last, err := m.lastRun(ctx, uint64(ch))
		if err != nil {
			return err
		}
		m.en.OpenAfter(uint64(guild), uint64(ch), count.Standard(), last)
		if err := m.saveChannel(ctx, uint64(ch)); err != nil {
			m.en.Close(uint64(ch))
			return err
		}
	}
	return nil
}

// rules changes a counting channel's preset or its options.
func (m *Module) rules(ctx context.Context, guild, ch snowflake.ID, what string, values []string) (count.Ruleset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.en.Channel(uint64(ch))
	if !ok || s.Guild != uint64(guild) {
		return count.Ruleset{}, errNoGame
	}
	r := s.Rules
	if what == "rules" {
		if len(values) != 1 {
			return r, errNoGame
		}
		p, ok := count.Preset(values[0])
		if !ok {
			return r, errNoGame
		}
		// A preset keeps the channel's switches.
		p.Arcade, p.Timeout = r.Arcade, r.Timeout
		r = p
	} else {
		r.Arcade = slices.Contains(values, "arcade")
		r.Solo = slices.Contains(values, "solo")
		r.Timeout = 0
		if slices.Contains(values, "timeout") {
			r.Timeout = baseTimeout
		}
		if p, ok := count.Preset(r.Name); ok && r.Solo != p.Solo {
			r.Name = "custom"
		}
	}
	m.en.Open(uint64(guild), uint64(ch), r)
	if err := m.saveChannel(ctx, uint64(ch)); err != nil {
		// Back to exactly what it was: reopening under the old rules would
		// start yet another run.
		m.en.Restore(uint64(ch), s)
		return r, err
	}
	s, _ = m.en.Channel(uint64(ch))
	return s.Rules, nil
}

// describe is a ruleset's switches in a few words.
func describe(r count.Ruleset) string {
	var on []string
	if r.Arcade {
		on = append(on, "arcade")
	}
	if r.Solo {
		on = append(on, "solo")
	}
	if r.Timeout > 0 {
		on = append(on, "timeouts")
	}
	if len(on) == 0 {
		return "no extras"
	}
	return strings.Join(on, " and ")
}

// counting is guild's counting channels, in order.
func (m *Module) counting(guild snowflake.ID) []uint64 {
	var out []uint64
	for _, ch := range m.en.Channels() {
		if s, ok := m.en.Channel(ch); ok && s.Guild == uint64(guild) {
			out = append(out, ch)
		}
	}
	slices.Sort(out)
	return out
}

// Report is each counting channel's run, for the server's admins, and the
// controls that set them up.
func (m *Module) Report(guild snowflake.ID) core.Report {
	var rows [][2]string
	chans := m.counting(guild)
	if len(chans) == 0 {
		rows = append(rows, [2]string{"counting", "nowhere yet"})
	}
	for _, ch := range chans {
		s, _ := m.en.Channel(ch)
		rows = append(rows, [2]string{"<#" + strconv.FormatUint(ch, 10) + ">",
			fmt.Sprintf("%s, next %s, best %d, %d saves", s.Rules.Name, s.Next(), s.Best, s.Saves)})
	}
	return core.Report{Rows: rows, Controls: m.controls(guild)}
}

// controls are the channel picker and, for each counting channel, its
// preset and its switches.
func (m *Module) controls(guild snowflake.ID) []discord.ContainerSubComponent {
	chans := m.counting(guild)
	pick := discord.ChannelSelectMenuComponent{CustomID: "games:channels", Placeholder: "pick the channels to count in",
		ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText}, MinValues: new(0), MaxValues: maxChannels}
	for _, ch := range chans {
		pick.DefaultValues = append(pick.DefaultValues, discord.NewSelectMenuDefaultChannel(snowflake.ID(ch)))
	}
	out := []discord.ContainerSubComponent{
		discord.NewTextDisplay("### counting\n-# up to three channels, each with its own rules"),
		discord.NewActionRow(pick),
	}
	for _, ch := range chans {
		s, _ := m.en.Channel(ch)
		id := strconv.FormatUint(ch, 10)
		presets := discord.StringSelectMenuComponent{CustomID: "games:rules:" + id, Placeholder: "rules"}
		for _, name := range count.Presets() {
			presets.Options = append(presets.Options, discord.StringSelectMenuOption{Label: name, Value: name, Default: name == s.Rules.Name})
		}
		opts := discord.StringSelectMenuComponent{CustomID: "games:opts:" + id, Placeholder: "no extras", MinValues: new(0), MaxValues: len(options)}
		for _, o := range options {
			on := o.value == "arcade" && s.Rules.Arcade || o.value == "solo" && s.Rules.Solo || o.value == "timeout" && s.Rules.Timeout > 0
			opts.Options = append(opts.Options, discord.StringSelectMenuOption{Label: o.label, Value: o.value, Description: o.about, Default: on})
		}
		out = append(out,
			discord.NewTextDisplay(fmt.Sprintf("**<#%s>** · %s · next %s", id, s.Rules.Name, s.Next())),
			discord.NewActionRow(presets),
			discord.NewActionRow(opts))
	}
	return out
}
