package status

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
)

func TestNames(t *testing.T) {
	cases := map[gateway.Intents]string{
		0: "none",
		gateway.IntentGuilds | gateway.IntentMessageContent:  "guilds, message content",
		gateway.IntentGuilds | gateway.IntentGuildModeration: "guilds, others",
	}
	for in, want := range cases {
		if got := names(in); got != want {
			t.Errorf("names(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateKeepsUTF8(t *testing.T) {
	got := truncate("ééééé", 3) // cuts mid-rune
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
}

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

// command runs one of m's handlers directly and returns its single reply.
func command(t *testing.T, m *Module, name string) discord.MessageCreate {
	t.Helper()
	for _, c := range m.Commands() {
		if c.Create.Name != name {
			continue
		}
		e, sent := coretest.Event(t, name, nil)
		if err := c.Run(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		if len(*sent) != 1 {
			t.Fatalf("/%s replied %d times", name, len(*sent))
		}
		r := (*sent)[0]
		if r.Flags != discord.MessageFlagEphemeral || r.AllowedMentions == nil || r.AllowedMentions.Parse == nil {
			t.Fatalf("/%s reply is not ephemeral and ping-free: %+v", name, r)
		}
		return r
	}
	t.Fatalf("no /%s", name)
	return discord.MessageCreate{}
}

func TestPing(t *testing.T) {
	m := New(nil, nil, func() time.Duration { return 42 * time.Millisecond })
	if got := command(t, m, "ping").Content; got != "pong · gateway 42 ms" {
		t.Fatalf("got %q", got)
	}
	m = New(nil, nil, func() time.Duration { return 0 })
	if got := command(t, m, "ping").Content; got != "pong · gateway not measured yet" {
		t.Fatalf("before the first heartbeat: %q", got)
	}
}

func TestStatus(t *testing.T) {
	cases := []struct {
		name  string
		probe Probe
		db    Pinger
		color int
		want  string
	}{
		{"no database", Probe{Granted: gateway.IntentGuilds}, nil, brand.ColorOK, "\ndatabase    not configured\n"},
		{"database up", Probe{}, pinger{}, brand.ColorOK, "\ndatabase    0."},
		{"database down", Probe{}, pinger{errors.New("refused")}, brand.ColorError, "```\n-# database: refused"},
		{"gateway not measured", Probe{}, nil, brand.ColorOK, "\ngateway     not measured yet\n"},
		{"module skipped", Probe{Skipped: []string{"echo"}}, nil, brand.ColorWarn, "\nskipped     echo\n"},
	}
	for _, c := range cases {
		m := New(func() Probe { return c.probe }, c.db, func() time.Duration { return 0 })
		r := command(t, m, "status")
		if len(r.Embeds) != 1 || len(r.Files) != 1 {
			t.Fatalf("%s: want one embed with its icon file, got %+v", c.name, r)
		}
		if r.Embeds[0].Color != c.color || !strings.Contains(r.Embeds[0].Description, c.want) {
			t.Errorf("%s: color %x, description %q", c.name, r.Embeds[0].Color, r.Embeds[0].Description)
		}
	}
}

func TestReadoutIsAGrid(t *testing.T) {
	got := readout([][2]string{
		{"gateway", "42 ms"},
		{"intents", "guilds, members, presences, guild messages, direct messages, message content"},
	})
	want := "```\n" +
		"gateway     42 ms\n" +
		"intents     guilds, members, presences,\n" +
		"            guild messages,\n" +
		"            direct messages,\n" +
		"            message content\n" +
		"```\n"
	if got != want {
		t.Errorf("readout:\n%s\nwant:\n%s", got, want)
	}
}

func TestMsAndUptime(t *testing.T) {
	for d, want := range map[time.Duration]string{
		1234 * time.Microsecond: "1.2 ms", 42 * time.Millisecond: "42 ms", 1500 * time.Millisecond: "1500 ms",
	} {
		if got := ms(d); got != want {
			t.Errorf("ms(%v) = %q, want %q", d, got, want)
		}
	}
	for d, want := range map[time.Duration]string{
		45 * time.Second: "45s", 12*time.Minute + 59*time.Second: "12m",
		3*time.Hour + 12*time.Minute: "3h 12m", 52 * time.Hour: "2d 4h",
	} {
		if got := uptime(d); got != want {
			t.Errorf("uptime(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestModuleContract(t *testing.T) {
	m := New(nil, nil, nil)
	if m.Name() != "status" || m.Want().Required != gateway.IntentGuilds {
		t.Fatal("status must require only guilds")
	}
	for _, c := range m.Commands() {
		if c.Create.Name == "status" && c.Tier != core.Admin {
			t.Fatal("/status must be admin-only")
		}
	}
}
