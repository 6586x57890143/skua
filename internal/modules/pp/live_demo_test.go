package pp

import (
	"fmt"
	"os"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// TestLiveDemo posts every slack tide surface to the test bot's bench
// channel for the owner to look at: the tide card in each light and in
// the bell, a table with the deckhead's scraps up, and a duel at the rail
// by night. It only posts pictures; nothing is staked. Same setup as
// TestLiveTide:
//
//	set -a; . ../bench.env; set +a
//	SKUA_LIVE=1 go test ./internal/modules/pp -run TestLiveDemo -v
func TestLiveDemo(t *testing.T) {
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
	post := func(what string, pic []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		msg := discord.MessageCreate{
			Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", what, "", discord.NewMediaGallery(screenItem(pic, what)))},
			Files:           []*discord.File{screenAttachment(pic, what)},
			Flags:           discord.MessageFlagIsComponentsV2,
			AllowedMentions: core.NoPings(),
		}
		posted, err := r.CreateMessage(ch, msg)
		if err != nil {
			t.Fatalf("posting %s: %v", what, err)
		}
		t.Logf("%-34s %4d KB  message %d", what, len(pic)/1024, posted.ID)
	}

	hold := int64(31000 * unit)
	for light, name := range []string{"dusk", "night", "dawn", "day"} {
		h := House{Hold: hold, Line: houseLine(), MaxStake: 310 * unit, Staked: 125 * unit,
			Beam: "steve", BeamThrow: 97, Scraps: 3, Sump: Sump{Hold: hold, Line: houseLine(), Light: light, Pumping: light == dawn}}
		pic, err := HouseScreen(h)
		post(fmt.Sprintf("demo: the slack tide at %s", name), pic, err)
	}
	for _, stage := range []int{bellDrawing, bellRinging} {
		h := House{Hold: houseLine(), Line: houseLine(), MaxStake: 500 * unit, Bell: stage == bellRinging,
			Sump: Sump{Hold: houseLine(), Line: houseLine(), Stage: stage}}
		pic, err := HouseScreen(h)
		post(fmt.Sprintf("demo: the bell, %s", map[int]string{bellDrawing: "the water draws back", bellRinging: "ringing"}[stage]), pic, err)
	}

	seats := []TableSeat{
		{Name: "heavywinds", Class: Classes[1], Stake: stakes[0].pp, Throw: 37},
		{Name: "steve", Class: Classes[3], Stake: stakes[3].pp, Throw: 100, Won: stakes[3].pp * 10},
		{Name: "tester", Class: Classes[7], Stake: stakes[2].pp, Throw: 92, Won: stakes[2].pp * 5 / 2},
	}
	pic, err := TableScreen(Table{Seats: seats, Thrown: true, Slate: "3e8d0b5512aa",
		Scraps: []Scrap{{Up: true, Color: 0xC0392B}, {}, {Up: true}, {Up: true, Color: 0x2E86AB}, {}, {}, {Up: true, Color: 0xE5B53A}}})
	post("demo: a table thrown, the deckhead", pic, err)

	duel := []TableSeat{
		{Name: "steve", Class: Classes[1], Stake: stakes[1].pp, Throw: 83, Won: stakes[1].pp * 3 / 2},
		{Name: "tester", Class: Classes[4], Stake: stakes[1].pp, Throw: 41},
	}
	pic, err = RailScreen(Rail{Light: night, Seats: duel, Thrown: true, Slate: "9f2c41ab77e0"})
	post("demo: a duel at the rail by night", pic, err)
	pic, err = RailScreen(Rail{Light: dawn, Seats: duel[:1], Called: "tester", Slate: "9f2c41ab77e0"})
	post("demo: a duel waiting, at dawn", pic, err)
}
