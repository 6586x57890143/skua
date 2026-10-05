// Package perf is /perf: where skua spends her time, module by module and
// stage by stage, from the Go runtime up to Discord's API. It reads what
// internal/obs records and lays it out as a code block grid, slowest first,
// each row with a bar scaled to the slowest so the outlier shows at a
// glance.
//
// The numbers are the whole process's, every server skua is in, so they say
// how busy other servers keep her. /perf is BreakGlass: a server's own
// admins don't see them.
package perf

import (
	"context"
	"fmt"
	"strings"
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
		About: "every module is timed in four parts: in is the trip from the gateway to her, run is her own work, wait is time spent behind discord's rate limits and http is the round trip itself. drop counts calls that gave up while still waiting. the go runtime's own overhead sits underneath. it covers every server she's in, which is why only her keeper can run it",
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

// The grid's columns: label, bar, p99, p50, count. 16+5+7+7+5 is 40, the
// line limit UX.md sets.
const labelW, barW = 16, 5

func (m *Module) readout() string {
	rows := m.rec.Rows()
	var b strings.Builder
	b.WriteString("```\n")
	fmt.Fprintf(&b, "%-*s%7s%7s%5s\n", labelW+barW, "", "p99", "p50", "n")
	if len(rows) == 0 {
		b.WriteString("nothing measured yet\n")
	}
	var slowest time.Duration
	if len(rows) > 0 {
		slowest = rows[0].P99
	}
	for _, r := range rows[:min(top, len(rows))] {
		label := clip(r.Who, labelW-2-len(r.Stage.String())) + " " + r.Stage.String()
		fmt.Fprintf(&b, "%-*s%-*s%7s%7s%5s\n", labelW, label, barW, bar(r.P99, slowest, barW-1), dur(r.P99), dur(r.P50), count(r.N))
	}
	sched, gc := m.runtime()
	fmt.Fprintf(&b, "%s\n%-*s%7s\n%-*s%7s\n```\n", strings.Repeat("·", 40), labelW+barW, "go sched", dur(sched), labelW+barW, "go gc pause", dur(gc))
	b.WriteString("-# since boot · in: gateway to dispatch · run: handler · wait: rate limit · http: round trip · build " + core.Revision())
	return b.String()
}

// bar is d against max as up to width cells of eighths, so two stages a
// few percent apart still look apart.
func bar(d, max time.Duration, width int) string {
	if max <= 0 {
		return ""
	}
	eighths := int(int64(d) * int64(width*8) / int64(max))
	if eighths == 0 && d > 0 {
		eighths = 1
	}
	parts := []rune(" ▏▎▍▌▋▊▉")
	return strings.Repeat("█", eighths/8) + strings.TrimSpace(string(parts[eighths%8]))
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
	case d < 10*time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
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

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
