// Command ppcard draws /pp's screens from fixtures into a directory, so a
// layout can be looked at before it reaches Discord.
//
//	go run ./tools/ppcard -out <dir>
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/6586x57890143/skua/internal/modules/pp"
)

func main() {
	out := flag.String("out", ".", "where the PNGs go")
	flag.Parse()
	b := &pp.Default
	sheet := func(name, class string, total, vigor int64, phase string, buffs []string, season, week int64, place, held int, reroll bool) pp.Sheet {
		l, into, need := b.Level(total)
		return pp.Sheet{Name: name, Class: pp.ClassOf(class), Level: l, Into: into, Need: need, Total: total,
			Vigor: vigor, Phase: phase, Buffs: buffs, Season: season, Week: week, Place: place, Held: held, RerollReady: reroll}
	}
	shots := map[string]func() ([]byte, error){
		"status-new": func() ([]byte, error) {
			return pp.StatusScreen(sheet("tester", "wanderer", 120*100, 100, "fresh", nil, 0, 0, 0, 1, true))
		},
		"status-mid": func() ([]byte, error) {
			return pp.StatusScreen(sheet("heavywinds", "thief", 9400*100, 38, "tired", []string{"crest", "plume", "hearth ii"}, 1, 2, 14, 2, false))
		},
		"status-late": func() ([]byte, error) {
			return pp.StatusScreen(sheet("steve the very long named one of the melting pot", "knight", 61000*100, 0, "spent",
				[]string{"crest", "banner", "plume", "hearth iii"}, 3, 4, 1, 3, true))
		},
		"quests": func() ([]byte, error) {
			return pp.QuestScreen("heavywinds", []pp.QuestLine{
				{What: "send 40 messages", Progress: 23, Goal: 40, Tier: 0, Reward: 250 * 100},
				{What: "spend 45 minutes in voice", Progress: 10, Goal: 45, Tier: 1, Reward: 400 * 100},
				{What: "post a selfie in #selfies", Progress: 0, Goal: 1, Tier: 2, Reward: 750 * 100},
			}, true)
		},
		"quests-empty": func() ([]byte, error) { return pp.QuestScreen("steve", nil, false) },
	}
	for name, draw := range shots {
		data, err := draw()
		if err == nil {
			err = os.WriteFile(filepath.Join(*out, name+".png"), data, 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "✗", name+":", err)
			os.Exit(1)
		}
	}
}
