package intents

import (
	"slices"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
)

func TestGranted(t *testing.T) {
	cases := []struct {
		name  string
		flags discord.ApplicationFlags
		has   gateway.Intents
		lacks gateway.Intents
	}{
		{"none", 0, gateway.IntentGuilds, gateway.IntentsPrivileged},
		{"content", discord.ApplicationFlagGatewayMessageContent, gateway.IntentMessageContent, gateway.IntentGuildMembers | gateway.IntentGuildPresences},
		{"limited counts", discord.ApplicationFlagGatewayGuildMemberLimited, gateway.IntentGuildMembers, gateway.IntentMessageContent},
		{"all", discord.ApplicationFlagGatewayPresence | discord.ApplicationFlagGatewayGuildMembers | discord.ApplicationFlagGatewayMessageContentLimited, gateway.IntentsAll, 0},
	}
	for _, c := range cases {
		g := Granted(c.flags)
		if g&c.has != c.has {
			t.Errorf("%s: missing %v", c.name, c.has&^g)
		}
		if g&c.lacks != 0 {
			t.Errorf("%s: has %v it was not granted", c.name, g&c.lacks)
		}
	}
}

func TestResolve(t *testing.T) {
	wants := map[string]Want{
		"status": {Required: gateway.IntentGuilds},
		"reader": {Required: gateway.IntentGuildMessages | gateway.IntentMessageContent},
		"census": {Required: gateway.IntentGuilds, Optional: gateway.IntentGuildMembers},
	}
	id, skipped := Resolve(wants, Granted(0))
	if !slices.Equal(skipped, []string{"reader"}) {
		t.Fatalf("skipped = %v, want [reader]", skipped)
	}
	if id&gateway.IntentsPrivileged != 0 {
		t.Fatalf("identified with privileged %v without a grant", id&gateway.IntentsPrivileged)
	}
	if id&gateway.IntentGuildMessages != 0 {
		t.Fatal("a skipped module's intents leaked into identify")
	}

	id, skipped = Resolve(wants, Granted(discord.ApplicationFlagGatewayMessageContent|discord.ApplicationFlagGatewayGuildMembers))
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v with everything granted", skipped)
	}
	if want := gateway.IntentGuilds | gateway.IntentGuildMessages | gateway.IntentMessageContent | gateway.IntentGuildMembers; id != want {
		t.Fatalf("identify = %v, want %v", id, want)
	}
}
