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
	// admin is the router's rule, checked again on every team control.
	admin func(discord.Interaction) bool
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
	pending  map[snowflake.ID]int // guild -> reminders waiting
	offs     map[snowflake.ID][]time.Duration
	fellBack map[snowflake.ID]int // guild -> dms that went to the channel
}

// New is remind on db, which it needs: without one it isn't offered.
func New(log *slog.Logger, g *guard.Guard, db DB, admin func(discord.Interaction) bool) *Module {
	m := &Module{
		log: log, guard: g, db: db, admin: admin, now: time.Now, after: time.AfterFunc,
		held: map[int64]time.Time{}, tries: map[int64]int{},
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
		About: "run /remind with when and what. she reads a time the way you'd type it: in 20m, 5pm, fri 17:30, 7 nov or every weekday at 9, and the list under when shows how she read it before you send. pick your timezone once with tz and she keeps it. the reminder comes to your dms with buttons to snooze it or mark it done, or to a channel if you ask, and to the channel you set it in when your dms are closed. /reminders lists what you have waiting by day, opens one to change it or sets a new one, and keeps how they come to you: where they go, your three snooze buttons and quiet hours, when a dm waits until morning. with team or role it goes to a group instead: a team is a list admins make for a channel, which members join from the card she posts there, and its reminder pings exactly who is on it, with a here button that counts who showed up. she learns how long discord takes to make a message and sends that much early, so it lands on the second",
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
				discord.ApplicationCommandOptionString{Name: "team", Description: "ping a team in its channel instead", Autocomplete: true},
				discord.ApplicationCommandOptionRole{Name: "role", Description: "ping a role, here or in channel, instead"},
			},
		},
		Tier: core.Public,
		Run:  m.remind,
	}, m.agendaCommand()}
}

func (m *Module) Components() []core.Component { return []core.Component{{ID: id, Run: m.click}} }

func (m *Module) Modals() []core.Modal {
	return []core.Modal{{ID: editID, Run: m.submit}, {ID: newID, Run: m.create}, {ID: zoneID, Run: m.setZone}, {ID: teamID, Run: m.makeTeam}}
}

// zoneOf is user's zone as kept, or "".
func (m *Module) zoneOf(ctx context.Context, user snowflake.ID) string {
	p, err := m.person(ctx, user)
	if err != nil {
		m.log.Warn("remind: reading a member's zone", "err", err)
	}
	return p.tz
}

func (m *Module) keepZone(ctx context.Context, user snowflake.ID, tz string) error {
	if _, err := m.change(ctx, user, func(p *person) { p.tz = tz }); err != nil {
		return err
	}
	return m.rezone(ctx, user, tz)
}

// rezone moves user's waiting repeats into tz, so "every weekday at 9"
// stays 9 where they are now. A one-off keeps the moment it was set for.
func (m *Module) rezone(ctx context.Context, user snowflake.ID, tz string) error {
	rs, err := repeatsOf(ctx, m.db, user)
	if err != nil {
		return err
	}
	for _, r := range rs {
		if r.tz == tz {
			continue
		}
		r.tz, r.due = tz, r.r.next(m.now(), zone(tz))
		if _, err := retime(ctx, m.db, r); err != nil {
			return err
		}
		m.hold(r)
	}
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
	if f.Name == "team" {
		guild := e.GuildID()
		if guild == nil {
			return e.AutocompleteResult(nil)
		}
		cs, err := m.teamChoices(ctx, *guild, typed)
		if err != nil {
			return err
		}
		return e.AutocompleteResult(cs)
	}
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
		suffix = " · set tz"
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
	if t, ok := d.OptString("tz"); ok {
		tz := known(t)
		if tz == "" {
			return core.Tell("skua doesn't know the timezone " + clip(t, 40) + "; pick one from the list")
		}
		if err := m.keepZone(ctx, user, tz); err != nil {
			return err
		}
	}
	rep, _ := d.OptString("repeat")
	r, err := m.draft(ctx, *guild, user, e.Channel().ID(), d.String("when"), d.String("what"), rep)
	if err != nil {
		return err
	}
	to, said := d.OptString("to")
	if err := m.aimGroup(ctx, e, &r); err != nil {
		return err
	}
	// A team's or a role's is aimed already.
	if ch, ok := d.OptChannel("channel"); ok && r.target == "dm" {
		if !ch.Permissions.Has(discord.PermissionViewChannel | discord.PermissionSendMessages) {
			return core.Tell("you can't post in <#" + ch.ID.String() + ">, so skua won't either")
		}
		r.target, r.targetID = "channel", ch.ID
	} else if r.target == "dm" {
		if err := m.aim(ctx, &r, to, said, e.Member()); err != nil {
			return err
		}
	}
	if r, err = m.keep(ctx, r); err != nil {
		return err
	}
	return e.CreateMessage(set(r, m.now(), m.zoneOf(ctx, user) == ""))
}

// draft is the reminder a member asked for, read in their zone and
// checked: when it is, how it repeats, what it says. It goes to their dms
// until aim says otherwise.
func (m *Module) draft(ctx context.Context, guild, user, channel snowflake.ID, when, what, rep string) (reminder, error) {
	now := m.now()
	tz := m.zoneOf(ctx, user)
	loc := zone(tz)
	rs := read(when, now, loc)
	if len(rs) == 0 {
		return reminder{}, core.Tell("skua can't read " + clip(when, 40) + " as a time; try in 20m, fri 5pm or 7 nov 09:00")
	}
	rd := rs[0]
	if rep != "" {
		rd = repeatAs(rd, rep, loc)
	}
	if !rd.at.After(now) || rd.at.Sub(now) > horizonYr {
		return reminder{}, core.Tell("pick a time between now and a year from now")
	}
	what = oneLine(what)
	if what == "" {
		return reminder{}, core.Tell("say what to remind you of")
	}
	if tz == "" {
		tz = "UTC"
	}
	return reminder{guild: guild, owner: user, channel: channel, target: "dm", text: what, due: rd.at, r: rd.r, tz: tz, state: "pending", created: now}, nil
}

// aim sends r where to says, or where its owner's settings do when they
// didn't say. Asking for here needs the right to post here; a setting
// that can't be met here falls back to their dms.
func (m *Module) aim(ctx context.Context, r *reminder, to string, said bool, mem *discord.ResolvedMember) error {
	if !said {
		who, err := m.person(ctx, r.owner)
		if err != nil {
			return err
		}
		to = who.deliver
	}
	if to != "here" {
		return nil
	}
	if mem == nil || !mem.Permissions.Has(discord.PermissionSendMessages) {
		if said {
			return core.Tell("you can't post here, so skua won't either")
		}
		return nil
	}
	r.target = "channel"
	return nil
}

// keep stores r and starts its clock, within the member's cap.
func (m *Module) keep(ctx context.Context, r reminder) (reminder, error) {
	n, err := pendingOf(ctx, m.db, r.owner)
	if err != nil {
		return r, err
	}
	if n >= perMember {
		return r, core.Tell(fmt.Sprintf("you have %d reminders waiting, which is the most skua keeps; cancel one first", perMember))
	}
	if r.id, err = insert(ctx, m.db, r); err != nil {
		return r, err
	}
	m.hold(r)
	m.mu.Lock()
	m.pending[r.guild]++
	m.mu.Unlock()
	return r, nil
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
