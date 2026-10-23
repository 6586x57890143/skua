// Package perf is /perf: where skua spends her time, module by module and
// stage by stage, from the Go runtime up to Discord's API. It reads what
// internal/obs records and lays it out as an aligned timing table, budget misses first, so the
// outlier leads.
//
// The numbers are the whole process's, every server skua is in, so they say
// how busy other servers keep her. /perf is BreakGlass: a server's own
// admins don't see them.
package perf

import (
	"context"
	"fmt"
	"slices"
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
		Line:  "where her time goes and what misses its target",
		About: "/perf lists where her time goes in each module as a timing table. a target is a p99 budget; a warning marks one she missed, and those come first. every call is split four ways: in is the trip from discord's gateway to her, run is her own work, wait is time held back by discord's rate limits and http is the request itself. drop counts calls that gave up while waiting. the go runtime's own overhead is listed underneath. it covers every server she's in, so only her keeper can run it",
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
	text, over := m.readoutRows(m.rec.Rows())
	color := brand.ColorInfo
	if over {
		color = brand.ColorWarn
	}
	return e.CreateMessage(discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(color, "perf", "timings", text)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
}

func (m *Module) readout() string { text, _ := m.readoutRows(m.rec.Rows()); return text }

// Fixed columns survive Discord markdown parsing and remain copyable. A row
// over its explicit p99 budget leads even when a longer network wait is healthy.
func (m *Module) readoutRows(stages []obs.Row) (string, bool) {
	over := func(r obs.Row) bool { return r.Target > 0 && r.P99 > r.Target }
	stages = slices.Clone(stages)
	slices.SortStableFunc(stages, func(a, b obs.Row) int {
		if over(a) != over(b) {
			if over(a) {
				return -1
			}
			return 1
		}
		return 0
	})
	breaches := 0
	for _, row := range stages {
		if over(row) {
			breaches++
		}
	}
	var b strings.Builder
	if len(stages) == 0 {
		b.WriteString("nothing measured yet\n")
	}
	b.WriteString("```text\n")
	if len(stages) > 0 {
		fmt.Fprintf(&b, "%-26s %8s %8s %8s %6s\n", "stage", "p50", "p99", "target", "n")
		for _, row := range stages[:min(top, len(stages))] {
			mark := " "
			if over(row) {
				mark = "!"
			}
			fmt.Fprintf(&b, "%s%-25s %8s %8s %8s %6s\n", mark, stageLabel(row.Who, row.Stage), dur(row.P50), dur(row.P99), dur(row.Target), count(row.N))
		}
	}
	sched, gc := m.runtime()
	fmt.Fprintf(&b, "%-26s %8s %8s %8s %6s\n%-26s %8s %8s %8s %6s\n", "go sched", "-", dur(sched), "-", "-", "go gc pause", "-", dur(gc), "-", "-")
	b.WriteString("```\n")
	if breaches > 0 {
		fmt.Fprintf(&b, "! %d over target\n", breaches)
	}
	if len(stages) > top {
		fmt.Fprintf(&b, "-# showing %d of %d stages\n", top, len(stages))
	}
	b.WriteString("-# since boot · target: p99 budget · !: over target · n: samples\n")
	b.WriteString("-# in: gateway to dispatch · run: handler · wait: rate limit · http: round trip · drop: cancelled in queue\n")
	b.WriteString("-# build " + stageName(core.Revision()))
	return b.String(), breaches > 0
}
func stageName(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "`", "'")), " ")
	runes := []rune(s)
	if len(runes) > 25 {
		return string(runes[:22]) + "..."
	}
	return s
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

func stageLabel(who string, stage obs.Stage) string {
	name := stageName(who)
	n := 25 - 1 - len(stage.String())
	runes := []rune(name)
	if len(runes) > n {
		name = string(runes[:n-3]) + "..."
	}
	return name + " " + stage.String()
}
