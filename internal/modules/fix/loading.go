package fix

import (
	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/disgoorg/disgo/discord"
)

func loading() discord.MessageCreate {
	mark := brand.Mention("fix_loading")
	if mark == "" {
		mark = brand.Mention("mod_fix")
	}
	text := "**fetching post**"
	if mark != "" {
		text = mark + " " + text
	}
	return discord.MessageCreate{Flags: discord.MessageFlagIsComponentsV2, AllowedMentions: core.NoPings(), Components: []discord.LayoutComponent{
		discord.NewContainer(discord.NewTextDisplay(text)).WithAccentColor(brand.ColorIdle),
	}}
}
