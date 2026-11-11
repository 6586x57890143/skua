// Package forever is /forever: a server's own ladder for World of Warcraft:
// Forever. Members list the characters they play, in any region, and skua
// keeps the ladder of who is highest, posts their levels as they climb and
// hands out the server's forever role. Each character is read from
// Blizzard's armory once however many servers list it.
package forever

import (
	"context"
	"image"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// Name is the module's name: its command, its emoji and its switch.
const Name = "forever"

// Launch is when Forever opened, the start of every launch race.
var Launch = time.Date(2026, time.November, 4, 23, 0, 0, 0, time.UTC)

// Cap is Forever's level cap.
const Cap = 60

const (
	perMember = 10  // characters one member lists in a server
	perGuild  = 500 // characters one server lists
	every     = 5 * time.Minute
	// staleAfter is how long unread a character's next change is old news.
	staleAfter = 30 * time.Minute
	inFlight   = 8
)

// Config is skua's Battle.net app, and the hooks address Blizzard sends a
// member back to after they sign in. Without the app forever is off;
// without the hooks, characters are only added by name.
type Config struct{ ID, Secret, HooksURL, HooksSecret string }

func FromEnv() Config {
	return Config{
		ID: os.Getenv("SKUA_BNET_ID"), Secret: os.Getenv("SKUA_BNET_SECRET"),
		HooksURL: strings.TrimRight(os.Getenv("SKUA_HOOKS_URL"), "/"), HooksSecret: os.Getenv("SKUA_HOOKS_SECRET"),
	}
}

func (c Config) Configured() bool { return c.ID != "" && c.Secret != "" }

// Poster is the REST forever uses on its own: the feed and roles.
type Poster interface {
	CreateMessage(channel snowflake.ID, m discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error)
}

// health is how a region's last round went.
type health struct {
	ok   time.Time
	err  error
	gone int // characters it didn't answer for
}

type Module struct {
	log     *slog.Logger
	guard   *guard.Guard
	db      DB
	api     *bnet
	link    *link
	icons   icons   // every item icon rebuilt, by media id
	screens screens // each character's last armory screen
	admin   func(discord.Interaction) bool
	// on is whether a server has forever switched on: the rounds skip the
	// characters and feeds of one that doesn't.
	on   func(guild snowflake.ID) bool
	now  func() time.Time
	boot sync.Once
	// roleMu keeps two first adds in one server from making two roles.
	roleMu sync.Map // guild -> *sync.Mutex

	mu     sync.Mutex
	chars  map[key]*char
	claims []claim
	guilds map[snowflake.ID]setup
	health map[string]health
	// days is each server's ladder as its day began, the chart a place's
	// move is read against, and dayOf which day that was.
	days  map[snowflake.ID]map[key]int
	dayOf map[snowflake.ID]string
	round time.Time // the last round's end
	rest  Poster
	self  snowflake.ID
}

// New is forever on db (nil keeps it in memory), with what it holds read
// back.
func New(ctx context.Context, log *slog.Logger, g *guard.Guard, db DB, cfg Config, admin func(discord.Interaction) bool) (*Module, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := load(ctx, db)
	if err != nil {
		return nil, err
	}
	return &Module{
		log: log, guard: g, db: db, admin: admin, now: time.Now,
		on:  func(snowflake.ID) bool { return true },
		api: newBnet(&http.Client{Timeout: 15 * time.Second}, cfg.ID, cfg.Secret),
		link: &link{
			hooks: cfg.HooksURL, secret: cfg.HooksSecret, used: map[string]time.Time{},
			authorize: "https://oauth.battle.net/authorize", token: "https://oauth.battle.net/token",
		},
		chars: st.chars, claims: st.claims, guilds: st.guilds, health: map[string]health{},
		days: st.days, dayOf: st.dayOf, icons: icons{have: map[int]image.Image{}}, screens: screens{have: map[key]drawn{}},
	}, nil
}

func (*Module) Name() string { return "forever" }

// Gate is core.Gated: the rounds ask on before reading a server's
// characters or posting its feed.
func (m *Module) Gate(on func(guild snowflake.ID) bool) { m.on = on }

// Want is guilds, for leaving one.
func (*Module) Want() intents.Want { return intents.Want{Required: gateway.IntentGuilds} }

// Perms is posting the feed and shared armories, and handing out the
// forever role.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionSendMessages | discord.PermissionAttachFiles | discord.PermissionManageRoles
}

func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorNotice,
		Line:  "this server's wow forever ladder, every region on one board",
		About: "run /forever me and add the characters you play with their name, surname and region. blizzard's armory only shows a character from level 10, once it has logged out, so she finds yours after your first logout past 10. your card shows each one's level, place here and legacy track: forever's account checkpoints she can read, a class at 25, 45 and 60 and a crafting profession at 150, 225 and 300. /forever ladder is who in the server is highest right now, every region on one board, a tie going to whoever got there first, with each place's move since the day began. she checks every character every five minutes and tells the server's forever channel about tens levels and legacy checkpoints, a ✦ when it earns someone a legacy point. a 60 or a first in the server gets a card of its own. pick a character under armory for and she draws their armory, the game's character pane in her pixels with what they wear, and share posts it here. link battle.net signs you in once so she adds every forever character on your account, in every region, marked as yours, and nobody else can list them. /forever who shows who plays where, to find people on your side. the first character added makes the server's forever role, and everyone who lists a character gets it",
	}
}

// Report is what this server lists and how the armory is answering.
func (m *Module) Report(guild snowflake.ID) core.Report {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, used, gone := 0, map[string]bool{}, map[string]int{}
	for _, c := range m.claims {
		if c.guild == guild {
			n++
			used[c.k.region] = true
			if ch := m.chars[c.k]; ch != nil && ch.missing > 0 {
				gone[c.k.region]++
			}
		}
	}
	r := core.Report{Controls: m.controls(guild)}
	if n == 0 {
		return r
	}
	r.Rows = append(r.Rows, [2]string{"characters", strconv.Itoa(n)})
	if !m.round.IsZero() {
		r.Rows = append(r.Rows, [2]string{"last check", core.Duration(m.now().Sub(m.round)) + " ago"})
	}
	for _, reg := range Regions {
		if !used[reg] {
			continue
		}
		h := m.health[reg]
		state := "not checked yet"
		switch {
		case h.err != nil:
			state = "failing"
		case gone[reg] > 0:
			state = strconv.Itoa(gone[reg]) + " not answering"
		case !h.ok.IsZero():
			state = "ok"
		}
		r.Rows = append(r.Rows, [2]string{reg, state})
	}
	s := m.guilds[guild]
	if s.feed == 0 {
		r.Notes = append(r.Notes, "! no forever channel yet; pick one below for the level feed")
	}
	return r
}

// OnEvent starts the rounds once skua is up and forgets a server she
// leaves.
func (m *Module) OnEvent(ev bot.Event) {
	switch e := ev.(type) {
	case *events.Ready:
		m.boot.Do(func() {
			m.mu.Lock()
			m.rest, m.self = e.Client().Rest, e.User.ID
			m.mu.Unlock()
			go m.loop(context.Background())
		})
	case *events.GuildLeave:
		m.mu.Lock()
		m.claims = slices.DeleteFunc(m.claims, func(c claim) bool { return c.guild == e.GuildID })
		delete(m.guilds, e.GuildID)
		m.mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := dropGuild(ctx, m.db, e.GuildID); err != nil {
				m.log.Warn("forever: forgetting a server", "guild", e.GuildID, "err", err)
			}
			m.mu.Lock()
			var ks []key
			for k := range m.chars {
				ks = append(ks, k)
			}
			m.mu.Unlock()
			m.prune(ctx, ks...)
		}()
	}
}

// The custom IDs: "forever:<verb>[:<arg>...]" for buttons and selects,
// "forever-add" the add form.
const (
	id    = Name
	addID = "forever-add"
)

func (m *Module) Commands() []core.Command {
	return []core.Command{{
		Create: discord.SlashCommandCreate{
			Name:        Name,
			Description: "your wow forever characters and this server's ladder",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{Name: "me", Description: "your characters, and add or drop one"},
				discord.ApplicationCommandOptionSubCommand{Name: "ladder", Description: "who in this server is highest right now"},
				discord.ApplicationCommandOptionSubCommand{Name: "who", Description: "who plays where, by region and ruleset"},
			},
		},
		Tier: core.Public,
		Run:  m.command,
	}}
}

func (m *Module) Components() []core.Component { return []core.Component{{ID: id, Run: m.click}} }

func (m *Module) Modals() []core.Modal { return []core.Modal{{ID: addID, Run: m.add}} }
