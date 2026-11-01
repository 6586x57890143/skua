package pp

import (
	"os"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// TestLiveLetters posts a sealed letter, then the same letter opened on a
// made-up code, to the test bot's bench channel, as /pp builds them, so
// the owner can see them in Discord. Nothing is claimed.
//
//	set -a; . ../bench.env; set +a
//	SKUA_LIVE=1 go test ./internal/modules/pp -run TestLiveLetters -v
func TestLiveLetters(t *testing.T) {
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
	m := testModule(&fakeDB{})
	s := m.st.server(3)
	s.letters = map[snowflake.ID]*letter{5: {code: "DEMO-0000-NOT-A-CODE", at: t0.Unix()}}
	for _, opened := range []bool{false, true} {
		s.letters[5].opened = opened
		msg, err := m.letterMessage(3, 5)
		if err != nil {
			t.Fatal(err)
		}
		msg.Flags &^= discord.MessageFlagEphemeral
		posted, err := r.CreateMessage(ch, msg)
		if err != nil {
			t.Fatalf("posting: %v", err)
		}
		t.Logf("opened %v: message %d", opened, posted.ID)
	}
}
