package pp

import (
	"os"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// TestLiveShop posts the stair's and the yard's cards as a member would see
// them, in every light, to the test bot's bench channel, and reads each
// back: the picture arrived and every button with it. The buttons are for
// looking at; nothing answers them. Same setup as TestLiveTide:
//
//	set -a; . ../bench.env; set +a
//	SKUA_LIVE=1 go test ./internal/modules/pp -run TestLiveShop -v
//
// Never point it at skua's own token.
func TestLiveShop(t *testing.T) {
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
	m, _ := yardServer(5, 1234)
	s := m.st.server(3)
	today := DayOf(m.now())
	mem := m.st.member(s, 3, 5, today)
	tallow, _ := dyeOf("tallow")
	mem.livery, mem.liveryUntil = tallow.key, today+4

	post := func(what string, build func() (discord.MessageCreate, error), want ...string) {
		t.Helper()
		msg, err := build()
		if err != nil {
			t.Fatal(what, err)
		}
		msg.Flags &^= discord.MessageFlagEphemeral
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
		t.Logf("%-22s message %d", what, posted.ID)
	}

	for light, name := range []string{"dusk", "night", "dawn", "day"} {
		post("the stair at "+name, m.st.shopMessage(s, mem, true, false, true, light), "pp:ladder", "pp:climb", "pp:yard", "pp:look")
	}
	post("the ladder", m.st.shopMessage(s, mem, true, true, true, dusk), "pp:back", "pp:climb", "pp:look")
	for light, name := range []string{"dusk", "night", "dawn", "day"} {
		post("the yard at "+name, m.st.yardFront(s, 5, mem, today, light), "pp:back", "pp:yard:0", "pp:yard:1")
	}
	post("the sump's trades", func() (discord.MessageCreate, error) { return m.st.yardMessage(s, 5, mem, tradesPage, today, true) }, "pp:dye", "pp:yard\"")
}
