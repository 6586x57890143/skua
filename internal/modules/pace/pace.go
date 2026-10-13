// Package pace answers cygnet's skua.pace: how many messages a minute a
// server is sending now, and as usual, so cygnet's launch valve can hold a
// wave while the server is already loud. It is a question, never data: the
// answer is those two numbers, nothing about channels, authors or content,
// and only cygnet is answered. A server opts in from /help; until then it is
// never counted and the answer is denied.
package pace

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/6586x57890143/cygnet/link"
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
const Name = "pace"

// Method is the link verb, which cygnet calls as skua.pace.
const Method = "pace"

const (
	nowMinutes = 5      // now is the last five minutes
	usualHours = 7 * 24 // usual is up to the last week
	warmUp     = time.Hour
)

// Answer is the reply body.
type Answer struct {
	Now   float64 `json:"now"`
	Usual float64 `json:"usual"`
}

type bucket struct {
	at int64 // which minute or hour since the epoch
	n  int
}

// counts is one guild's messages: a ring of minutes for now and a ring of
// hours for usual.
type counts struct {
	mu    sync.Mutex
	since time.Time // the first message counted, so usual covers no more than was seen
	mins  [nowMinutes]bucket
	hours [usualHours]bucket
}

func (c *counts) add(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.since.IsZero() {
		c.since = t
	}
	bump(c.mins[:], t.Unix()/60)
	bump(c.hours[:], t.Unix()/3600)
}

func bump(ring []bucket, at int64) {
	b := &ring[at%int64(len(ring))]
	if b.at != at {
		*b = bucket{at: at}
	}
	b.n++
}

// answer is now over the last five minutes, this one included, and usual
// as the mean over the whole hours seen in the last week. ok is false until
// an hour has been seen.
//
// ponytail: a mean, not a median, and in memory, so a restart warms up
// again for an hour. A median of per-minute rates is mostly zero in a quiet
// server, which would hold every wave; a table if a week's baseline must
// outlive deploys.
func (c *counts) answer(t time.Time) (Answer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hour := t.Unix() / 3600
	from := max(hour-usualHours, (c.since.Unix()+3599)/3600) // the first whole hour seen
	if c.since.IsZero() || hour-from < int64(warmUp/time.Hour) {
		return Answer{}, false
	}
	var a Answer
	for _, b := range c.hours {
		if b.at >= from && b.at < hour {
			a.Usual += float64(b.n)
		}
	}
	a.Usual /= float64((hour - from) * 60)
	minute := t.Unix() / 60
	for _, b := range c.mins {
		if b.at > minute-nowMinutes && b.at <= minute {
			a.Now += float64(b.n)
		}
	}
	a.Now /= nowMinutes
	return a, true
}

type Module struct {
	on     func(guild snowflake.ID) bool
	guilds sync.Map // snowflake.ID -> *counts
	now    func() time.Time
}

// New takes whether a guild has pace switched on, which the link's handler
// asks before it answers.
func New(on func(guild snowflake.ID) bool) *Module {
	return &Module{on: on, now: time.Now}
}

func (*Module) Name() string { return "pace" }

// Want is guild messages to count them. Content is never read.
func (*Module) Want() intents.Want { return intents.Want{Required: gateway.IntentGuildMessages} }

// Perms is none: pace only listens.
func (*Module) Perms() discord.Permissions { return 0 }

func (*Module) Commands() []core.Command { return nil }

// Help is pace's page in /help.
func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorIdle,
		Line:  "tells cygnet how busy the server is",
		About: "off until an admin switches it on. then she keeps a tally of how many messages a minute the server sends, and when cygnet asks before letting the next wave of a launch in she answers with two numbers, the pace now and the usual one. no channels, names or words leave, and nobody but cygnet gets an answer. the usual pace needs an hour of listening first",
	}
}

// OnEvent counts members' messages. Toggles.Listen has already dropped
// every guild that hasn't switched pace on.
func (m *Module) OnEvent(ev bot.Event) {
	switch e := ev.(type) {
	case *events.GuildMessageCreate:
		if e.Message.Author.Bot || e.Message.WebhookID != nil {
			return
		}
		v, _ := m.guilds.LoadOrStore(e.GuildID, new(counts))
		v.(*counts).add(m.now())
	case *events.GuildLeave:
		m.guilds.Delete(e.GuildID)
	}
}

// Answer is the link handler for skua.pace.
func (m *Module) Answer(_ context.Context, c link.Call) (any, error) {
	if c.From != "cygnet" {
		return nil, link.Err(link.Denied, "pace only answers cygnet")
	}
	var req struct {
		Guild string `json:"guild"`
	}
	if err := c.Bind(&req); err != nil {
		return nil, err
	}
	guild, err := snowflake.Parse(req.Guild)
	if err != nil {
		return nil, link.Err(link.Invalid, "guild is not a snowflake")
	}
	if !m.on(guild) {
		// Switched off, so what was counted goes: a later switch on starts
		// fresh rather than reading the hours it was off as silence.
		m.guilds.Delete(guild)
		return nil, link.Err(link.Denied, "this server hasn't switched pace on")
	}
	a, ok := m.read(guild)
	if !ok {
		return nil, link.Err(link.Busy, "still learning this server's usual pace")
	}
	return a, nil
}

func (m *Module) read(guild snowflake.ID) (Answer, bool) {
	v, ok := m.guilds.Load(guild)
	if !ok {
		return Answer{}, false
	}
	return v.(*counts).answer(m.now())
}

// Report is the two numbers cygnet would get, for the server's admins.
func (m *Module) Report(guild snowflake.ID) core.Report {
	if !m.on(guild) {
		return core.Report{}
	}
	a, ok := m.read(guild)
	if !ok {
		return core.Report{Notes: []string{"still listening; the usual pace needs an hour"}}
	}
	return core.Report{Rows: [][2]string{
		{"now", fmt.Sprintf("%.1f a minute", a.Now)},
		{"usual", fmt.Sprintf("%.1f a minute", a.Usual)},
	}}
}
