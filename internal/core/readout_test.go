package core

import (
	"testing"
	"time"
)

func TestReadoutIsAGrid(t *testing.T) {
	got := Readout([][2]string{
		{"gateway", "42 ms"},
		{"intents", "guilds, members, presences, guild messages, direct messages, message content"},
	})
	want := "```\n" +
		"gateway  42 ms\n" +
		"intents  guilds, members, presences,\n" +
		"         guild messages,\n" +
		"         direct messages,\n" +
		"         message content\n" +
		"```\n"
	if got != want {
		t.Errorf("readout:\n%s\nwant:\n%s", got, want)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second: "45s", 12*time.Minute + 59*time.Second: "12m",
		3*time.Hour + 12*time.Minute: "3h 12m", 52 * time.Hour: "2d 4h",
	} {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}
