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

func (m *Module) Commands() []core.Command {
	return []core.Command{
		{
			Create: discord.SlashCommandCreate{Name: "ping", Description: "Gateway round trip"},
			Tier:   core.Public,
			Run:    m.ping,
		},
		{
			Create: discord.SlashCommandCreate{Name: "status", Description: "Intents, database and uptime"},
			Tier:   core.Admin,
			Run:    m.status,
		},
	}
}

func (m *Module) ping(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
	return e.CreateMessage(discord.MessageCreate{
		Content:         fmt.Sprintf("pong · gateway %s", m.latency().Round(time.Millisecond)),
		Flags:           discord.MessageFlagEphemeral,
		AllowedMentions: core.NoPings(),
	})
}

func (m *Module) status(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	p := m.probe()
	color := brand.ColorOK
	var b strings.Builder

	fmt.Fprintf(&b, "**privileged granted** %s\n", names(p.Granted&gateway.IntentsPrivileged))
	fmt.Fprintf(&b, "**identified** %s\n", names(p.Identified))
	if len(p.Skipped) > 0 {
		color = brand.ColorWarn
		fmt.Fprintf(&b, "**skipped modules** %s (a required intent is off in the Developer Portal)\n", strings.Join(p.Skipped, ", "))
	}

	if m.db == nil {
		b.WriteString("**database** none configured\n")
	} else {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		start := time.Now()
		err := m.db.Ping(ctx)
		cancel()
		if err != nil {
			color = brand.ColorError
			fmt.Fprintf(&b, "**database** unreachable: %s\n", truncate(err.Error(), 200))
		} else {
			fmt.Fprintf(&b, "**database** %s\n", time.Since(start).Round(time.Microsecond))
		}
	}
	fmt.Fprintf(&b, "**gateway** %s · **up** %s · %s", m.latency().Round(time.Millisecond),
		time.Since(m.started).Round(time.Second), runtime.Version())

	embed, file := brand.Embed(color, "skua", b.String())
	return e.CreateMessage(discord.MessageCreate{
		Embeds:          []discord.Embed{embed},
		Files:           []*discord.File{file},
		Flags:           discord.MessageFlagEphemeral,
		AllowedMentions: core.NoPings(),
	})
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
		out = append(out, "+others")
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
