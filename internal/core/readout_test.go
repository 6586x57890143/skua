package core

import (
	"testing"
	"time"
)

// Facts are a code block grid lined up in one column, a long list wraps
// inside it after a comma, and a mention or link, which a code block would
// show as typed, goes under the grid where it renders.
func TestReadoutIsACodeBlockGrid(t *testing.T) {
	got := Readout([][2]string{
		{"gateway", "42 ms"},
		{"kept", "<@1>, <@2>"},
		{"intents", "guilds, members, messages, voice, moderation"},
		{"/nest leave", ""},
	})
	want := "```\ngateway      42 ms\nintents      guilds, members, messages,\n             voice, moderation\n/nest leave\n```\n**kept** <@1>, <@2>\n"
	if got != want {
		t.Errorf("readout\n%s\nwant\n%s", got, want)
	}
	if got := Readout(nil); got != "" {
		t.Errorf("no rows drew %q", got)
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
