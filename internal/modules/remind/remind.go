// Package remind is /remind: a reminder at the time a member asks for,
// once or on a repeat, in their dms or a channel, sent so it lands on the
// second. clock.go is how it keeps time, when.go how it reads one.
package remind

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// perMember is the most reminders one member keeps waiting.
const perMember = 50

type Module struct {
	log   *slog.Logger
	guard *guard.Guard
	db    DB
	now   func() time.Time
	after func(time.Duration, func()) *time.Timer
	boot  sync.Once
	// leadNS is how early a send starts, learned (clock.go).
	leadNS atomic.Int64

	mu       sync.Mutex
	rest     Poster
	q        queue
	held     map[int64]time.Time
	timer    *time.Timer
	tries    map[int64]int
	dms      map[snowflake.ID]snowflake.ID // member -> their dm channel
	tzs      map[snowflake.ID]string       // member -> their zone, as kept
	pending  map[snowflake.ID]int          // guild -> reminders waiting
	offs     map[snowflake.ID][]time.Duration
	fellBack map[snowflake.ID]int // guild -> dms that went to the channel
}

// New is remind on db, which it needs: without one it isn't offered.
func New(log *slog.Logger, g *guard.Guard, db DB) *Module {
	m := &Module{
		log: log, guard: g, db: db, now: time.Now, after: time.AfterFunc,
		held: map[int64]time.Time{}, tries: map[int64]int{}, dms: map[snowflake.ID]snowflake.ID{}, tzs: map[snowflake.ID]string{},
		pending: map[snowflake.ID]int{}, offs: map[snowflake.ID][]time.Duration{}, fellBack: map[snowflake.ID]int{},
	}
	m.leadNS.Store(int64(firstLead))
	return m
}

func (*Module) Name() string { return "remind" }

// Want is guilds, for leaving one.
func (*Module) Want() intents.Want { return intents.Want{Required: gateway.IntentGuilds} }

// Perms is posting a reminder into a channel.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionSendMessages
}

func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorInfo,
		Line:  "reminds you at the time you ask, to the second",
		About: "run /remind with when and what. she reads a time the way you'd type it: in 20m, 5pm, fri 17:30, 7 nov or every weekday at 9, and the list under when shows how she read it before you send. pick your timezone once with tz and she keeps it. the reminder comes to your dms with buttons to snooze it or mark it done, or to a channel if you ask, and to the channel you set it in when your dms are closed. she learns how long discord takes to make a message and sends that much early, so it lands on the second",
	}
}

// Report is how many reminders this server has waiting and how close to
// the second its last ones landed.
func (m *Module) Report(guild snowflake.ID) core.Report {
	m.mu.Lock()
	n, fell := m.pending[guild], m.fellBack[guild]
	m.mu.Unlock()
	p50, p99, sent := m.offsets(guild)
	if n == 0 && sent == 0 {
		return core.Report{}
	}
	r := core.Report{Rows: [][2]string{{"waiting", strconv.Itoa(n)}}}
	if sent > 0 {
		r.Rows = append(r.Rows, [2]string{"landed", "p50 " + ms(p50) + " · p99 " + ms(p99)}, [2]string{"sends", strconv.Itoa(sent) + " measured"})
	} else {
		r.Rows = append(r.Rows, [2]string{"landed", "not measured yet"})
	}
	r.Rows = append(r.Rows, [2]string{"lead", ms(m.lead())})
	if fell > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("-# %d went to the channel they were set in because dms were closed", fell))
	}
	return r
}

// ms is a duration in milliseconds, one decimal under 10 (UX.md).
func ms(d time.Duration) string {
	f := float64(d) / float64(time.Millisecond)
	if f < 10 {
		return strconv.FormatFloat(f, 'f', 1, 64) + " ms"
	}
	return strconv.Itoa(int(f)) + " ms"
}

// OnEvent starts the clock once skua is up, answers the when and tz lists
// as they're typed, and forgets a server she leaves.
func (m *Module) OnEvent(ev bot.Event) {
	switch e := ev.(type) {
	case *events.Ready:
		m.boot.Do(func() {
			m.mu.Lock()
			m.rest = e.Client().Rest
			m.mu.Unlock()
			go m.run(context.Background())
		})
	case *events.AutocompleteInteractionCreate:
		if e.Data.CommandName == "remind" {
			if err := m.suggest(e); err != nil {
				m.log.Warn("remind: answering a list", "err", err)
			}
		}
	case *events.GuildLeave:
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := dropGuild(ctx, m.db, e.GuildID); err != nil {
				m.log.Warn("remind: forgetting a server", "guild", e.GuildID, "err", err)
			}
		}()
	}
}

// The custom IDs: "remind:<verb>:<id>[:<due ms>[:<arg>]]" for every button
// and select, "remind-edit:<id>" the edit form.
const (
	id     = "remind"
	editID = "remind-edit"
)

var textChannels = []discord.ChannelType{discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews, discord.ChannelTypeGuildPublicThread, discord.ChannelTypeGuildPrivateThread}

func (m *Module) Commands() []core.Command {
	maxWhen, maxWhat, maxTZ := 100, 300, 64
	return []core.Command{{
		Create: discord.SlashCommandCreate{
			Name:        "remind",
			Description: "remind you of something at a time you pick",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{Name: "when", Description: "in 20m, 5pm, fri 17:30, 7 nov or every weekday at 9", Required: true, Autocomplete: true, MaxLength: &maxWhen},
				discord.ApplicationCommandOptionString{Name: "what", Description: "what to remind you of", Required: true, MaxLength: &maxWhat},
				discord.ApplicationCommandOptionString{Name: "to", Description: "where it comes; your dms unless you say", Choices: []discord.ApplicationCommandOptionChoiceString{
					{Name: "your dms", Value: "dm"}, {Name: "this channel", Value: "here"},
				}},
				discord.ApplicationCommandOptionChannel{Name: "channel", Description: "post it in this channel instead", ChannelTypes: textChannels},
				discord.ApplicationCommandOptionString{Name: "repeat", Description: "how often it comes back", Choices: []discord.ApplicationCommandOptionChoiceString{
					{Name: "never", Value: "never"}, {Name: "every day", Value: "day"}, {Name: "weekdays", Value: "weekdays"}, {Name: "every week", Value: "week"}, {Name: "every month", Value: "month"},
				}},
				discord.ApplicationCommandOptionString{Name: "tz", Description: "your timezone, kept for next time", Autocomplete: true, MaxLength: &maxTZ},
			},
		},
		Tier: core.Public,
		Run:  m.remind,
	}}
}

func (m *Module) Components() []core.Component { return []core.Component{{ID: id, Run: m.click}} }

func (m *Module) Modals() []core.Modal { return []core.Modal{{ID: editID, Run: m.submit}} }

// zoneOf is user's zone as kept, or "".
func (m *Module) zoneOf(ctx context.Context, user snowflake.ID) string {
	m.mu.Lock()
	tz, ok := m.tzs[user]
	m.mu.Unlock()
	if ok {
		return tz
	}
	p, err := personOf(ctx, m.db, user)
	if err != nil {
		m.log.Warn("remind: reading a member's zone", "err", err)
		return ""
	}
	m.mu.Lock()
	m.tzs[user] = p.tz
	m.mu.Unlock()
	return p.tz
}

func (m *Module) keepZone(ctx context.Context, user snowflake.ID, tz string) error {
	if err := saveTZ(ctx, m.db, user, tz); err != nil {
		return err
	}
	m.mu.Lock()
	m.tzs[user] = tz
	m.mu.Unlock()
	return nil
}

// suggest answers the when and tz lists as a member types.
func (m *Module) suggest(e *events.AutocompleteInteractionCreate) error {
	f := e.Data.Focused()
	typed := e.Data.String(f.Name)
	now := m.now()
	if f.Name == "tz" {
		return e.AutocompleteResult(zoneChoices(typed, now))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tz := known(e.Data.String("tz"))
	if tz == "" {
		tz = m.zoneOf(ctx, e.User().ID)
	}
	return e.AutocompleteResult(whenChoices(typed, tz, now))
}

// whenChoices is how skua read what was typed, each reading a choice that
// carries back the exact time it shows. Before anything is typed it
// offers a few to start from; when nothing reads, it says what does.
func whenChoices(typed, tz string, now time.Time) []discord.AutocompleteChoice {
	loc := zone(tz)
	suffix := ""
	if tz == "" {
		suffix = " · utc; set tz"
	}
	var rs []reading
	if strings.TrimSpace(typed) == "" {
		for _, s := range []string{"in 20m", "in 1h", "tomorrow 9am", "every weekday at 9am"} {
			rs = append(rs, read(s, now, loc)...)
		}
	} else {
		rs = read(typed, now, loc)
	}
	out := make([]discord.AutocompleteChoice, 0, len(rs))
	for _, rd := range rs {
		out = append(out, discord.AutocompleteChoiceString{Name: clip(rd.label(now, loc)+suffix, 100), Value: rd.value()})
	}
	if len(out) == 0 {
		out = append(out, discord.AutocompleteChoiceString{Name: "skua can't read that; try in 20m, fri 5pm or every weekday at 9", Value: clip(typed, 100)})
	}
	return out
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// oneLine is what a member typed, fit for a heading: no line breaks, no
// heading marks of its own.
func oneLine(s string) string {
	return strings.TrimLeft(strings.Join(strings.Fields(s), " "), "#>- ")
}

// remind is /remind.
func (m *Module) remind(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return core.Tell("remind only works in a server")
	}
	d := e.SlashCommandInteractionData()
	user := e.User().ID
	now := m.now()
	tz := m.zoneOf(ctx, user)
	if t, ok := d.OptString("tz"); ok {
		if tz = known(t); tz == "" {
			return core.Tell("skua doesn't know the timezone " + clip(t, 40) + "; pick one from the list")
		}
		if err := m.keepZone(ctx, user, tz); err != nil {
			return err
		}
	}
	loc := zone(tz)
	rs := read(d.String("when"), now, loc)
	if len(rs) == 0 {
		return core.Tell("skua can't read " + clip(d.String("when"), 40) + " as a time; try in 20m, fri 5pm or 7 nov 09:00")
	}
	rd := rs[0]
	if rep, ok := d.OptString("repeat"); ok {
		rd = repeatAs(rd, rep, loc)
	}
	if !rd.at.After(now) || rd.at.Sub(now) > horizonYr {
		return core.Tell("pick a time between now and a year from now")
	}
	what := oneLine(d.String("what"))
	if what == "" {
		return core.Tell("say what to remind you of")
	}
	r := reminder{guild: *guild, owner: user, channel: e.Channel().ID(), target: "dm", text: what, due: rd.at, r: rd.r, tz: tz, state: "pending", created: now}
	if r.tz == "" {
		r.tz = "UTC"
	}
	if ch, ok := d.OptChannel("channel"); ok {
		if !ch.Permissions.Has(discord.PermissionViewChannel | discord.PermissionSendMessages) {
			return core.Tell("you can't post in <#" + ch.ID.String() + ">, so skua won't either")
		}
		r.target, r.targetID = "channel", ch.ID
	} else if d.String("to") == "here" {
		if mem := e.Member(); mem == nil || !mem.Permissions.Has(discord.PermissionSendMessages) {
			return core.Tell("you can't post here, so skua won't either")
		}
		r.target = "channel"
	}
	n, err := pendingOf(ctx, m.db, user)
	if err != nil {
		return err
	}
	if n >= perMember {
		return core.Tell(fmt.Sprintf("you have %d reminders waiting, which is the most skua keeps; cancel one first", perMember))
	}
	if r.id, err = insert(ctx, m.db, r); err != nil {
		return err
	}
	m.hold(r)
	m.mu.Lock()
	m.pending[r.guild]++
	m.mu.Unlock()
	return e.CreateMessage(set(r, now, tz == ""))
}

// repeatAs is rd made to come back as rep says, keeping its time of day.
func repeatAs(rd reading, rep string, loc *time.Location) reading {
	l := rd.at.In(loc)
	at := l.Hour()*60 + l.Minute()
	switch rep {
	case "day":
		rd.r = rule{mask: everyDay, at: at}
	case "weekdays":
		rd.r = rule{mask: weekdays, at: at}
		rd.at = rd.r.next(rd.at.Add(-time.Second), loc)
	case "week":
		rd.r = rule{mask: 1 << l.Weekday(), at: at}
	case "month":
		rd.r = rule{mday: l.Day(), at: at}
	default:
		rd.r = rule{}
	}
	return rd
}
