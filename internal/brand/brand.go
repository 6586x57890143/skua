// Package brand is skua's palette, mood icons and emoji. The mood is
// derived from the embed's colour rather than passed beside it, so the two
// cannot disagree. Once Sync has run an icon is an application emoji on
// Discord's CDN and nothing is uploaded with a reply; before that, it is an
// attachment derived from the finished embed, so an attachment:// URL never
// points at nothing.
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

// Embed returns an embed in color wearing that colour's mood, and the
// files to send with it: none once the emoji are synced, when the
// thumbnail is the mood's emoji, otherwise the icon its attachment:// URL
// names. An unknown colour gets "info": never claim success for something
// unrecognised.
func Embed(color int, title, description string) (discord.Embed, []*discord.File) {
	file, url := Icon(color)
	var files []*discord.File
	if file != nil {
		files = []*discord.File{file}
	}
	return discord.Embed{
		Title:       title,
		Description: description,
		Color:       color,
		Thumbnail:   &discord.EmbedResource{URL: url},
	}, files
}

// Icon is color's mood icon for a component that shows it: its emoji's URL
// and a nil file once synced, otherwise the file and the attachment:// URL
// that points at it. Unknown colours get "info", as in Embed.
func Icon(color int) (*discord.File, string) {
	mood, ok := moods[color]
	if !ok {
		mood = "info"
	}
	return asset("skua_" + mood)
}

// Avatar is the bird alone, with no badge, as Icon gives it.
func Avatar() (*discord.File, string) { return asset("skua_avatar") }

// ModuleIcon is module's own icon, as Icon gives it, or the mood icon of
// color when the module has none.
func ModuleIcon(module string, color int) (*discord.File, string) {
	key := "mod_" + module
	if _, err := emojiArt.Open("emoji/" + key + ".png"); err != nil {
		return Icon(color)
	}
	if url, ok := emojiURL(key); ok {
		return nil, url
	}
	data, _ := emojiArt.ReadFile("emoji/" + key + ".png")
	return discord.NewFile(key+".png", "", bytes.NewReader(data)), "attachment://" + key + ".png"
}

func asset(key string) (*discord.File, string) {
	if url, ok := emojiURL(key); ok {
		return nil, url
	}
	name := key + ".png"
	data, _ := assets.ReadFile("assets/" + name) // embedded; cannot fail for a listed name
	return discord.NewFile(name, "", bytes.NewReader(data)), "attachment://" + name
}
