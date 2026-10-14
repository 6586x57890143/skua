// Command ppcard draws /pp's screens from fixtures into a directory, so a
// layout can be looked at before it reaches Discord.
//
//	go run ./tools/ppcard -out <dir>
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"

	"github.com/6586x57890143/skua/internal/modules/pp"
	"github.com/6586x57890143/skua/internal/pixel"
)

func main() {
	out := flag.String("out", ".", "where the PNGs go")
	portrait := flag.String("portrait", "", "a 32 by 48 class portrait PNG for the status screens")
	flag.Parse()
	var face image.Image
	if *portrait != "" {
		f, err := os.Open(*portrait)
		if err == nil {
			face, err = png.Decode(f)
			f.Close()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "✗ portrait:", err)
			os.Exit(1)
		}
	}
	b := &pp.Default
	sheet := func(name, class string, total, vigor int64, phase string, buffs []pp.Buff, season, week int64, place, held int, reroll bool) pp.Sheet {
		l, into, need := b.Level(total)
		return pp.Sheet{Portrait: face, Name: name, Class: pp.ClassOf(class), Level: l, Into: into, Need: need, Total: total,
			Vigor: vigor, Phase: phase, Buffs: buffs, Season: season, Week: week, Place: place, Held: held, RerollReady: reroll}
	}
	buff := func(names ...string) []pp.Buff {
		var bs []pp.Buff
		for _, n := range names {
			icon := n
			if len(n) > 6 && n[:6] == "hearth" {
				icon = "hearth"
			}
			bs = append(bs, pp.Buff{Icon: icon, Label: n})
		}
		return bs
	}
	shots := map[string]func() ([]byte, error){
		"status-new": func() ([]byte, error) {
			return pp.StatusScreen(sheet("tester", "wanderer", 120*100, 100, "fresh", nil, 0, 0, 0, 1, true))
		},
		"status-mid": func() ([]byte, error) {
			s := sheet("heavywinds", "thief", 9400*100, 38, "tired", buff("crest", "plume", "hearth ii"), 1, 2, 14, 2, false)
			s.Laurels = 1
			return pp.StatusScreen(s)
		},
		"status-late": func() ([]byte, error) {
			s := sheet("steve the very long named one of the melting pot", "knight", 61000*100, 0, "spent",
				buff("crest", "banner", "plume", "hearth iii"), 3, 4, 1, 3, true)
			s.Laurels = 3
			return pp.StatusScreen(s)
		},
		"quests": func() ([]byte, error) {
			return pp.QuestScreen("heavywinds", []pp.QuestLine{
				{Icon: "quill", What: "send 40 messages", Progress: 23, Goal: 40, Tier: 0, Reward: 250 * 100},
				{Icon: "drum", What: "spend 45 minutes in voice", Progress: 10, Goal: 45, Tier: 1, Reward: 400 * 100},
				{Icon: "mirror", What: "post a selfie in #selfies", Progress: 0, Goal: 1, Tier: 2, Reward: 750 * 100},
			}, true)
		},
		"quests-empty": func() ([]byte, error) { return pp.QuestScreen("steve", nil, false) },
		"icons":        iconSheet,
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

// iconSheet sets every icon on a window with its name, in rows of three.
func iconSheet() ([]byte, error) {
	names := make([]string, 0, len(pixel.Icons))
	for n := range pixel.Icons {
		names = append(names, n)
	}
	slices.Sort(names)
	rows := (len(names) + 2) / 3
	c := pixel.New(384, 16+rows*22)
	c.Window(c.Rect)
	for i, n := range names {
		x, y := 12+i%3*124, 12+i/3*22
		c.Icon(n, image.Pt(x, y-2))
		c.Text(x+20, y, n, pixel.Ink, 0)
	}
	return c.PNG(3)
}
