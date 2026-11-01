package pp

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// TestLiveOpen posts what opening /pp changes, as Discord shows it, to the
// test bot's bench channel and reads each back: the beta panel with its
// open switch, the panel once open, and a newcomer's title screen with
// the quests button beside the class one. Nothing answers the buttons.
//
//	set -a; . ../bench.env; set +a
//	SKUA_LIVE=1 go test ./internal/modules/pp -run TestLiveOpen -v
//
// Never point it at skua's own token.
func TestLiveOpen(t *testing.T) {
	token, chs := os.Getenv("DISCORD_BOT_TOKEN"), os.Getenv("SKUA_BENCH_CHANNEL")
	if os.Getenv("SKUA_LIVE") == "" || token == "" || chs == "" {
		t.Skip("SKUA_LIVE, DISCORD_BOT_TOKEN and SKUA_BENCH_CHANNEL from bench.env")
	}
	realScreens(t)
	ch, err := snowflake.Parse(chs)
	if err != nil {
		t.Fatal(err)
	}
	r := rest.New(rest.NewClient(token))
	post := func(what string, msg discord.MessageCreate, want ...string) {
		t.Helper()
		posted, err := r.CreateMessage(ch, msg)
		if err != nil {
			t.Fatalf("posting %s: %v", what, err)
		}
		got, err := r.GetMessage(ch, posted.ID)
		if err != nil {
			t.Fatalf("reading %s back: %v", what, err)
		}
		body := js(got.Components)
		if strings.Contains(body, "attachment://") {
			t.Errorf("%s: the picture never arrived", what)
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s hasn't %q: %s", what, w, body)
			}
		}
		t.Logf("%-24s message %d", what, posted.ID)
	}
	panel := func(m *Module) discord.MessageCreate {
		return discord.MessageCreate{
			Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "the beta panel", "", m.betaControls(3)...)},
			Flags:           discord.MessageFlagIsComponentsV2,
			AllowedMentions: core.NoPings(),
		}
	}

	m := testModule(&fakeDB{})
	s := m.st.server(3)
	s.beta = snowflake.New(time.Now()) // a role Discord echoes as a string, as a real one is
	post("the beta panel", panel(m), "pp:beta", "pp:open")
	s.open = true
	post("the beta panel, open", panel(m), "open to everyone here")

	title, err := titleMessage(true, true, "")
	if err != nil {
		t.Fatal(err)
	}
	post("a newcomer's title", title, "pp:class", "pp:quests")
}
