package status

import (
	"testing"
	"unicode/utf8"

	"github.com/disgoorg/disgo/gateway"
)

func TestNames(t *testing.T) {
	cases := map[gateway.Intents]string{
		0: "none",
		gateway.IntentGuilds | gateway.IntentMessageContent:  "guilds, message content",
		gateway.IntentGuilds | gateway.IntentGuildModeration: "guilds, +others",
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
