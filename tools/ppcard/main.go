// Command ppcard draws /pp's screens from fixtures into a directory, so a
// layout can be looked at before it reaches Discord.
//
//	go run ./tools/ppcard -out <dir>
package main

import (
	"flag"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"

	"github.com/6586x57890143/skua/internal/modules/pp"
	"github.com/6586x57890143/skua/internal/pixel"
)

func main() {
	out := flag.String("out", ".", "where the PNGs go")
	flag.Parse()
	b := &pp.Default
	sheet := func(name, class string, total, vigor int64, phase string, buffs []pp.Buff, season, week int64, place, held int, reroll bool) pp.Sheet {
		l, into, need := b.Level(total)
		return pp.Sheet{Portrait: pp.Portrait(pp.ClassOf(class)), Name: name, Class: pp.ClassOf(class), Level: l, Into: into, Need: need, Total: total, Purse: total,
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
		"status-raven": func() ([]byte, error) {
			return pp.StatusScreen(sheet("BooCoconuts", "sorcerer", 1800*100, 70, "warm", buff("plume", "hearth iii"), 0, 0, 37, 1, true))
		},
		"status-albatross": func() ([]byte, error) {
			return pp.StatusScreen(sheet("glow", "wanderer", 900000*100, 70, "warm", nil, 0, 0, 2, 0, true))
		},
		"quests": func() ([]byte, error) {
			return pp.QuestScreen("heavywinds", []pp.QuestLine{
				{Icon: "quill", Tier: 0, Reward: 250 * 100, Tasks: []pp.TaskLine{{What: "messages", Progress: 23, Goal: 40}}},
				{Icon: "compass", Tier: 1, Reward: 400 * 100, Tasks: []pp.TaskLine{{What: "minutes in voice", Progress: 20, Goal: 20}, {What: "messages", Progress: 3, Goal: 17}}},
				{Icon: "lantern", Tier: 2, Reward: 750 * 100, Tasks: []pp.TaskLine{{What: "minutes in voice", Progress: 9, Goal: 18}, {What: "messages", Progress: 15, Goal: 15}, {What: "minutes by the hearth", Goal: 10}}},
			}, 2)
		},
		"quests-empty": func() ([]byte, error) { return pp.QuestScreen("steve", nil, 0) },
		"own-empty":    func() ([]byte, error) { return pp.OwnScreen(pp.Own{Fee: 20350, Purse: 9000 * 100}) },
		"own-plain": func() ([]byte, error) {
			return pp.OwnScreen(pp.Own{Name: "the lamplighter", Style: "plain dye", Colors: []uint32{0xa2803e}, Week: 40700, Fee: 20350, Purse: 9000 * 100})
		},
		"own-silk": func() ([]byte, error) {
			return pp.OwnScreen(pp.Own{Name: "heavywinds of the sump", Style: "shot silk", Colors: []uint32{0xc66e34, 0x6286c3}, Week: 101750, Fee: 20350, Purse: 9000 * 100})
		},
		"own-nacre": func() ([]byte, error) {
			return pp.OwnScreen(pp.Own{Name: "touched by the nothing", Style: "nacre", Colors: []uint32{0xa9c9ff, 0xffbbec, 0xffc3a0}, Icon: pp.Portrait(pp.ClassOf("raven")),
				Week: 203500, Fee: 20350, Purse: 31000 * 100})
		},
		"yard-trades": func() ([]byte, error) {
			return pp.YardScreen("the sump's trades", []pp.YardRow{
				{Name: "chandlers' tallow", Color: 0xa2803e, Price: 20350},
				{Name: "tanners' hide", Color: 0xbe715d, Price: 20350, Worn: true, Left: 5},
				{Name: "the pits' bruise", Color: 0x9d78a9, Price: 20350},
				{Name: "gin lane juniper", Color: 0x5c916a, Price: 20350},
				{Name: "ferrymen's tidewater", Color: 0x5f8aa6, Price: 20350},
				{Name: "fish market pewter", Color: 0x81868b, Price: 20350},
			}, 1234*100, pp.Livery{Name: "tanners' hide", Color: 0xbe715d, Left: 5})
		},
		"yard-factions": func() ([]byte, error) {
			return pp.YardScreen("the factions", []pp.YardRow{
				{Name: "counting house ink", Color: 0x887fb2, Price: 40700},
				{Name: "stair blue", Color: 0x6286c3, Price: 40700},
				{Name: "hedge company thorn", Color: 0x708e5b, Price: 40700},
				{Name: "wet guild sea green", Color: 0x4a9183, Price: 40700},
				{Name: "hearth ember", Color: 0xc66e34, Price: 40700, From: "shale perch"},
				{Name: "choir rose", Color: 0xb1738a, Price: 40700, From: "shale perch"},
				{Name: "wardens' moss", Color: 0x818b42, Price: 40700, From: "shale perch"},
				{Name: "kiln glass", Color: 0x618c92, Price: 40700, From: "shale perch"},
			}, 312*100, pp.Livery{})
		},
		"item-livery": func() ([]byte, error) {
			return pp.ItemScreen(pp.Item{Kind: "livery", Name: "hearth ember", What: "a scarf the colour of a banked fire",
				Story:  "the herons tie an ember scarf on every bird they feed through a hard winter, and the sump remembers who wore one. the dyers sell the colour on the herons' terms: the lease pays for a bowl at the hearth on a cold night",
				Effect: "your name wears ember in the server for a week", Price: 40700, You: "heavywinds", Dye: 0xc66e34, Garment: pp.Garment("ember")})
		},
		"garments": func() ([]byte, error) {
			keys := []string{"tallow", "hide", "bruise", "juniper", "tidewater", "pewter", "ink", "stair", "thorn", "sea", "ember", "rose", "moss", "glass"}
			colors := []uint32{0xa2803e, 0xbe715d, 0x9d78a9, 0x5c916a, 0x5f8aa6, 0x81868b, 0x887fb2, 0x6286c3, 0x708e5b, 0x4a9183, 0xc66e34, 0xb1738a, 0x818b42, 0x618c92}
			c := pixel.New(7*56+8, 2*80+8)
			c.Window(c.Rect)
			for i, k := range keys {
				c.DyedGrid(pp.Garment(k), image.Pt(8+i%7*56, 8+i/7*80), 3, colors[i])
				c.Text(8+i%7*56, 64+i/7*80, k, pixel.Ink, 52)
			}
			return c.PNG(2)
		},
		"icons": iconSheet,
		"type":  typeSheet,
	}
	for name, draw := range shots {
		data, err := draw()
		if err == nil {
			ext := ".jpg" // the screens, softened as on a television
			if name == "icons" || name == "type" {
				ext = ".png"
			}
			err = os.WriteFile(filepath.Join(*out, name+ext), data, 0o644)
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

// typeSheet sets every face of the family on a window: its name, the
// lowercase, the figures and a line of prose.
func typeSheet() ([]byte, error) {
	faces := []struct {
		name string
		f    *pixel.Face
	}{{"regular", pixel.Regular}, {"bold", pixel.Bold}, {"light", pixel.Light}, {"serif", pixel.Serif}}
	c := pixel.New(384, 16+len(faces)*58)
	c.Window(c.Rect)
	for i, f := range faces {
		y := 10 + i*58
		c.Print(pixel.Bold, 12, y, f.name, pixel.Gold)
		c.Print(f.f, 80, y, "abcdefghijklmnopqrstuvwxyz", pixel.Ink)
		c.Print(f.f, 80, y+17, "0123456789 lv 12 / 3,428 pp", pixel.Ink)
		c.Print(f.f, 12, y+34, "the flock burned their lamps low, and stayed.", pixel.Ink)
	}
	return c.PNG(3)
}
