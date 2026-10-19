// Package perf is /perf: where skua spends her time, module by module and
// stage by stage, from the Go runtime up to Discord's API. It reads what
// internal/obs records and lays it out as facts, slowest first, so the
// outlier leads.
//
// The numbers are the whole process's, every server skua is in, so they say
// how busy other servers keep her. /perf is BreakGlass: a server's own
// admins don't see them.
package perf

import (
	"context"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/intents"
	"github.com/6586x57890143/skua/internal/obs"
)

var adminOnly = discord.PermissionAdministrator

// top is how many stages the readout shows.
const top = 12

type Module struct {
	rec     *obs.Recorder
	runtime func() (sched, gc time.Duration)
}

func New(rec *obs.Recorder) *Module { return &Module{rec: rec, runtime: obs.Runtime} }

func (*Module) Name() string { return "perf" }

func (*Module) Want() intents.Want { return intents.Want{} }

// Perms is none: /perf only replies to interactions.
func (*Module) Perms() discord.Permissions { return 0 }

// Help is perf's page in /help.
func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorInfo,
		Line:  "where her time goes with the slowest first",
		About: "/perf lists where her time goes in each module with the slowest first. every call is split four ways: in is the trip from discord's gateway to her, run is her own work, wait is time held back by discord's rate limits and http is the request itself. drop counts calls that gave up while waiting. the go runtime's own overhead is listed underneath. it covers every server she's in, so only her keeper can run it",
	}
}

func (m *Module) Commands() []core.Command {
	return []core.Command{{
		// Hidden from members in the picker; the tier is the real gate.
		Create: discord.SlashCommandCreate{Name: "perf", Description: "see where skua spends her time", DefaultMemberPermissions: omit.New(&adminOnly)},
		Tier:   core.BreakGlass,
		Run:    m.perf,
	}}
}

func (m *Module) perf(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
	embed, files := brand.Embed(brand.ColorInfo, "perf", m.readout())
	return e.CreateMessage(discord.MessageCreate{
		Embeds:          []discord.Embed{embed},
		Files:           files,
		Flags:           discord.MessageFlagEphemeral,
		AllowedMentions: core.NoPings(),
	})
}

// readout is one fact per stage, slowest p99 first, then the runtime's own.
func (m *Module) readout() string {
	stages := m.rec.Rows()
	var rows [][2]string
	for _, r := range stages[:min(top, len(stages))] {
		rows = append(rows, [2]string{r.Who + " " + r.Stage.String(), dur(r.P99) + " p99 · " + dur(r.P50) + " p50 · " + count(r.N)})
	}
	text := "nothing measured yet\n"
	if len(rows) > 0 {
		text = core.Readout(rows)
	}
	sched, gc := m.runtime()
	text += core.Readout([][2]string{{"go sched", dur(sched)}, {"go gc pause", dur(gc)}})
	return text + "-# since boot · in: gateway to dispatch · run: handler · wait: rate limit · http: round trip · build " + core.Revision()
}

// dur is a duration the way UX.md writes numbers, down to microseconds.
func dur(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Millisecond:
		return fmt.Sprintf("%d µs", d.Microseconds())
	case d < 10*time.Millisecond:
		return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond))
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func count(n uint64) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%dk", n/1000)
}
