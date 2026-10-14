package pp

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/intents"
)

// Name is the module's, and its switch's in core.Toggles; Name() repeats
// it as a literal, which brand's icon test reads.
const Name = "pp"

// GuildInfo is what /pp needs to know of a server from skua's cache: its
// AFK channel, where nobody earns, and its vanity invite, which the
// banner buff looks for in a custom status.
type GuildInfo func(guild snowflake.ID) (afk snowflake.ID, vanity string)

// Module is /pp, the melting pot's rpg engine.
type Module struct {
	log   *slog.Logger
	db    DB
	st    *state
	info  GuildInfo
	now   func() time.Time
	on    func(guild snowflake.ID) bool
	boot  sync.Once
	saved atomic.Int64 // unix nanos of the last flush that wrote
	// earning is how many earned in voice in each guild the last minute.
	earning sync.Map
	// Admin reports whether an interaction comes from one of its server's
	// admins or skua's break-glass admin. Unset, nobody is.
	Admin func(discord.Interaction) bool
}

// New loads what /pp has kept. seed keeps each member's daily pool from
// being worked out from the source: it comes from a secret skua already
// holds, so there is nothing new to configure.
func New(ctx context.Context, log *slog.Logger, db DB, seed uint64, info GuildInfo) (*Module, error) {
	m := &Module{log: log, db: db, st: newState(&Default, seed), info: info, now: time.Now}
	if err := m.st.load(ctx, db); err != nil {
		return nil, err
	}
	return m, nil
}

func (*Module) Name() string { return "pp" }

// Want: messages and voice states earn. Content tells a real message from
// a repeat, and presences carry the custom status the banner buff reads;
// without either, /pp still runs.
func (*Module) Want() intents.Want {
	return intents.Want{
		Required: gateway.IntentGuilds | gateway.IntentGuildMessages | gateway.IntentGuildVoiceStates,
		Optional: gateway.IntentMessageContent | gateway.IntentGuildPresences,
	}
}

// Perms is none: /pp only answers interactions.
func (*Module) Perms() discord.Permissions { return 0 }

// Gate is how the minute's work skips a server that switched /pp off.
func (m *Module) Gate(on func(guild snowflake.ID) bool) { m.on = on }

func (m *Module) enabled(guild snowflake.ID) bool { return m.on == nil || m.on(guild) }

// Help is /pp's page in /help.
func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorNotice,
		Line:  "pot points for time in voice and talk",
		About: "she keeps score of who shows up. time in voice with company earns pot points and so does talking, and a day of real talk earns as much as a day in voice. every day has a pool to earn from that slows as it empties, so showing up most days beats grinding one; she won't say how deep it is. /pp shows where you stand and lets you pick a class",
	}
}

// Report is /pp in this server for its admins: who earned in voice the last
// minute, the hearth, how many have pp, and when it last saved.
func (m *Module) Report(guild snowflake.ID) core.Report {
	s := m.st.server(guild)
	s.mu.Lock()
	members, hearth := len(s.members), s.hearth()
	s.mu.Unlock()
	earning := 0
	if v, ok := m.earning.Load(guild); ok {
		earning = v.(int)
	}
	saved := "not yet"
	if at := m.saved.Load(); at > 0 {
		saved = core.Duration(m.now().Sub(time.Unix(0, at))) + " ago"
	}
	return core.Report{Rows: [][2]string{
		{"earning in voice", strconv.Itoa(earning)},
		{"hearth", hearthWord(hearth)},
		{"members", strconv.Itoa(members)},
		{"saved", saved},
	}, Controls: m.betaControls(guild)}
}

// hearthWord is a hearth tier as members read it.
func hearthWord(tier int) string {
	return [...]string{"unlit", "hearth i", "hearth ii", "hearth iii"}[tier]
}

// OnEvent keeps the live state: who sits where in voice, what each message
// is worth and what members wear. It never waits on anything but a lock.
func (m *Module) OnEvent(ev bot.Event) {
	switch e := ev.(type) {
	case *events.Ready:
		m.boot.Do(func() { go m.loop(context.Background()) })
	case *events.GuildReady:
		m.ready(e.GuildID, e.Guild)
	case *events.GuildMessageCreate:
		msg := e.Message
		if msg.Author.Bot || msg.Author.System || msg.WebhookID != nil {
			return
		}
		s := m.st.server(e.GuildID)
		s.mu.Lock()
		defer s.mu.Unlock()
		now := m.now()
		seen(m.st.member(s, e.GuildID, msg.Author.ID, DayOf(now)), msg.Member, msg.Author, e.GuildID)
		m.st.chat(s, e.GuildID, msg.Author.ID, msg.Content, now)
	case *events.GuildVoiceStateUpdate:
		s := m.st.server(e.VoiceState.GuildID)
		s.mu.Lock()
		defer s.mu.Unlock()
		m.st.sit(s, e.VoiceState.GuildID, e.VoiceState.UserID, e.VoiceState, e.Member.User.Bot, m.now())
		if mem, ok := s.members[e.VoiceState.UserID]; ok {
			seen(mem, &e.Member, e.Member.User, e.VoiceState.GuildID)
		}
	case *events.PresenceUpdate:
		_, vanity := m.info(e.GuildID)
		s := m.st.server(e.GuildID)
		s.mu.Lock()
		defer s.mu.Unlock()
		if mem, ok := s.members[e.PresenceUser.ID]; ok {
			mem.wear.Banner = bannerOf(e.Activities, vanity)
		}
	}
}

// ready seats everyone already in voice when skua (re)connects, and reads
// the custom statuses Discord sends with the server.
func (m *Module) ready(guild snowflake.ID, g discord.GatewayGuild) {
	bots := map[snowflake.ID]bool{}
	for _, mem := range g.Members {
		bots[mem.User.ID] = mem.User.Bot
	}
	_, vanity := m.info(guild)
	now := m.now()
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seats = map[snowflake.ID]*seat{}
	for _, vs := range g.VoiceStates {
		m.st.sit(s, guild, vs.UserID, vs, bots[vs.UserID], now)
	}
	for _, p := range g.Presences {
		if mem, ok := s.members[p.PresenceUser.ID]; ok {
			mem.wear.Banner = bannerOf(p.Activities, vanity)
		}
	}
}

// loop pays voice once a minute and saves what changed.
func (m *Module) loop(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.minute()
			m.Save()
		}
	}
}

// minute is one minute of voice in every server /pp is on in.
func (m *Module) minute() {
	now := m.now()
	for g, s := range m.st.servers() {
		if !m.enabled(g) {
			continue
		}
		afk, _ := m.info(g)
		s.mu.Lock()
		n := m.st.minute(s, g, afk, now)
		s.mu.Unlock()
		m.earning.Store(g, n)
	}
}

// Save writes what changed. main calls it once more on the way down, so a
// deploy loses at most what this minute earned.
func (m *Module) Save() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := m.st.flush(ctx, m.db); err != nil {
		m.log.Warn("pp: saving", "err", err)
		return
	}
	m.saved.Store(m.now().UnixNano())
}
