// Package brand is skua's palette and mood icons. The mood is derived from the embed's colour rather than passed beside it, so the two
// cannot disagree, and the attachment is derived from the finished embed so
// an attachment:// URL never points at nothing.
package brand

import (
	"bytes"
	"embed"

	"github.com/disgoorg/disgo/discord"
)

// The palette: slate night, steel, and
// muted signal colours that match the badge on each icon.
const (
	ColorPrimary = 0x1E2228
	ColorInfo    = 0x8A9CB2
	ColorOK      = 0x6E9A6A
	ColorWarn    = 0xD89B45
	ColorError   = 0xC04A3E
	ColorNotice  = 0x4F7C86
	ColorIdle    = 0x6A707A
)

//go:embed assets/*.png
var assets embed.FS

var moods = map[int]string{
	ColorPrimary: "notice",
	ColorNotice:  "notice",
	ColorInfo:    "info",
	ColorOK:      "ok",
	ColorWarn:    "warn",
	ColorError:   "error",
	ColorIdle:    "idle",
}

// Embed returns an embed in color wearing that colour's mood, and the file
// its thumbnail points at. An unknown colour gets "info": never claim
// success for something unrecognised.
func Embed(color int, title, description string) (discord.Embed, *discord.File) {
	mood, ok := moods[color]
	if !ok {
		mood = "info"
	}
	name := "skua_" + mood + ".png"
	data, _ := assets.ReadFile("assets/" + name) // embedded; cannot fail for a listed mood
	return discord.Embed{
		Title:       title,
		Description: description,
		Color:       color,
		Thumbnail:   &discord.EmbedResource{URL: "attachment://" + name},
	}, discord.NewFile(name, "", bytes.NewReader(data))
}
