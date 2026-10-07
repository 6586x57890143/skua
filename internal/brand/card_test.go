package brand

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
)

// A card's head is the module's emoji once synced, text alone before, and
// it carries what it was given after its text.
func TestCard(t *testing.T) {
	unsynced(t)
	row := discord.NewActionRow(discord.NewSecondaryButton("x", "x"))
	c := Card(ColorOK, "purge", "stop", "✓ done", row)
	if c.AccentColor != ColorOK || len(c.Components) != 2 {
		t.Fatalf("card %+v", c)
	}
	if got := c.Components[0].(discord.TextDisplayComponent).Content; got != "**purge** · stop\n✓ done" {
		t.Errorf("unsynced head %q", got)
	}
	ids := map[string]emojiRef{"mod_purge": {7, "mod_purge_ab"}}
	emojiIDs.Store(&ids)
	if got := Card(ColorOK, "purge", "", "body").Components[0].(discord.TextDisplayComponent).Content; got != "<:mod_purge_ab:7> **purge**\nbody" {
		t.Errorf("synced head %q", got)
	}
}

// Each platform has its own colour, none a mood's, so a platform card is
// never read as ok or failed; one without a colour is a notice.
func TestPlatformColor(t *testing.T) {
	seen := map[int]string{}
	for p := range platforms {
		c := PlatformColor(p)
		if _, mood := moods[c]; mood {
			t.Errorf("%s wears a mood's colour %06X", p, c)
		}
		if other, dup := seen[c]; dup {
			t.Errorf("%s and %s share %06X", p, other, c)
		}
		seen[c] = p
	}
	if PlatformColor("mastodon") != ColorNotice {
		t.Error("fallback")
	}
}
