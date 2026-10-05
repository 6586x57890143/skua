// Package status is skua's first module: /ping for anyone, /status for
// admins, reporting what the intent probe found and whether the database
// answers. It exists so the scaffold has one real path end to end.
package status

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/intents"
)

// Probe is what the intent resolution decided at boot.
type Probe struct {
	Granted, Identified gateway.Intents
	Skipped             []string
}

// Pinger is the database, or nil when skua runs without one.
type Pinger interface{ Ping(context.Context) error }

type Module struct {
	probe   func() Probe
	db      Pinger
	latency func() time.Duration
	started time.Time
}

func New(probe func() Probe, db Pinger, latency func() time.Duration) *Module {
	return &Module{probe: probe, db: db, latency: latency, started: time.Now()}
}

func (*Module) Name() string { return "status" }

func (*Module) Want() intents.Want { return intents.Want{Required: gateway.IntentGuilds} }

// Perms is none: /ping and /status only reply to interactions.
func (*Module) Perms() discord.Permissions { return 0 }

// Help is status's page in /help.
func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorIdle,
		Line:  "how she's doing right now",
		About: "/ping shows how long the gateway takes to answer her. /status is for admins and lists the intents she holds, whether the database is answering and how long she's been up",
	}
}

func (m *Module) Commands() []core.Command {
	return []core.Command{
		{
			Create: discord.SlashCommandCreate{Name: "ping", Description: "gateway round trip"},
			Tier:   core.Public,
			Run:    m.ping,
		},
		{
			Create: discord.SlashCommandCreate{Name: "status", Description: "intents, database and uptime"},
			Tier:   core.Admin,
			Run:    m.status,
		},
	}
}

func (m *Module) ping(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
	return e.CreateMessage(discord.MessageCreate{
		Content:         "pong · gateway " + gatewayMs(m.latency()),
		Flags:           discord.MessageFlagEphemeral,
		AllowedMentions: core.NoPings(),
	})
}

// status is a two-column readout in a code block: a monospace grid lines up
// the same on every client, where how embed fields wrap is up to each
// client. Anything that is not a short fact goes in a line below the block.
func (m *Module) status(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	p := m.probe()
	color := brand.ColorOK
	var rows [][2]string
	var notes []string

	rows = append(rows, [2]string{"gateway", gatewayMs(m.latency())})
	if m.db == nil {
		rows = append(rows, [2]string{"database", "not configured"})
	} else {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		start := time.Now()
		err := m.db.Ping(ctx)
		cancel()
		if err != nil {
			color = brand.ColorError
			rows = append(rows, [2]string{"database", "unreachable"})
			notes = append(notes, "-# database: "+truncate(err.Error(), 200))
		} else {
			rows = append(rows, [2]string{"database", ms(time.Since(start))})
		}
	}
	rows = append(rows,
		[2]string{"uptime", uptime(time.Since(m.started))},
		[2]string{"intents", names(p.Identified)},
		[2]string{"privileged", names(p.Granted & gateway.IntentsPrivileged)},
	)
	if len(p.Skipped) > 0 {
		color = brand.ColorWarn
		rows = append(rows, [2]string{"skipped", strings.Join(p.Skipped, ", ")})
		notes = append(notes, "skipped modules need an intent that is off in the developer portal")
	}
	rows = append(rows, [2]string{"runtime", runtime.Version()}, [2]string{"build", core.Revision()})

	embed, files := brand.Embed(color, "status", readout(rows)+strings.Join(notes, "\n"))
	return e.CreateMessage(discord.MessageCreate{
		Embeds:          []discord.Embed{embed},
		Files:           files,
		Flags:           discord.MessageFlagEphemeral,
		AllowedMentions: core.NoPings(),
	})
}

// labelWidth is the label column: the longest label, "privileged", and two
// spaces. valueWidth holds every line to 40 columns, so the grid needs as
// little room as the facts in it allow.
const labelWidth, valueWidth = 12, 28

// readout lays rows out as a code block grid. A value too long for its column
// wraps after a comma, continuing in the value column.
func readout(rows [][2]string) string {
	var b strings.Builder
	b.WriteString("```\n")
	for _, r := range rows {
		for i, line := range wrap(r[1], valueWidth) {
			label := ""
			if i == 0 {
				label = r[0]
			}
			fmt.Fprintf(&b, "%-*s%s\n", labelWidth, label, line)
		}
	}
	b.WriteString("```\n")
	return b.String()
}

// wrap breaks a comma separated list into lines of at most width, breaking
// only after a comma. A single item longer than width stays whole and runs
// past the 40 column grid; every value status shows (intent names, module
// names, durations) is far shorter.
func wrap(s string, width int) []string {
	var lines []string
	line := ""
	for i, item := range strings.Split(s, ", ") {
		if i > 0 {
			item = ", " + item
		}
		if line != "" && len(line)+len(item) > width {
			lines = append(lines, line+",")
			item = strings.TrimPrefix(item, ", ")
			line = ""
		}
		line += item
	}
	return append(lines, line)
}

// ms is a round trip in milliseconds: one decimal under 10ms, where the
// decimal still means something, whole numbers above.
func ms(d time.Duration) string {
	if d < 10*time.Millisecond {
		return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%d ms", d.Milliseconds())
}

// gatewayMs is the heartbeat round trip, which is 0 until the first
// heartbeat is acknowledged.
func gatewayMs(d time.Duration) string {
	if d == 0 {
		return "not measured yet"
	}
	return ms(d)
}

// uptime is the two largest units that matter: "45s", "12m", "3h 12m", "2d 4h".
func uptime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

var intentNames = []struct {
	i    gateway.Intents
	name string
}{
	{gateway.IntentGuilds, "guilds"},
	{gateway.IntentGuildMembers, "members"},
	{gateway.IntentGuildPresences, "presences"},
	{gateway.IntentGuildMessages, "guild messages"},
	{gateway.IntentDirectMessages, "DMs"},
	{gateway.IntentMessageContent, "message content"},
}

// names lists the intents a person would ask about; the long tail of
// non-privileged ones is summarised rather than spelled out.
func names(set gateway.Intents) string {
	var out []string
	rest := set
	for _, n := range intentNames {
		if set&n.i != 0 {
			out = append(out, n.name)
			rest &^= n.i
		}
	}
	if rest != 0 {
		out = append(out, "others")
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}
