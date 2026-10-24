package fix

import (
	"context"
	"errors"
	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"net/http"
	"time"
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

// If the final payload fails, replace the loading card with a small terminal
// error using a fresh bounded context. Never retry authentication or permission
// refusals, and never create another public message.
func (m *Module) finishLoading(e *events.ApplicationCommandInteractionCreate, g snowflake.ID, l Link, cause error) bool {
	var re *rest.Error
	if errors.As(cause, &re) && re.Response != nil {
		switch re.Response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return false
		}
	}
	if m.ctx.Err() != nil || m.guard.Allow(g, guard.FixCard) != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()
	message := card(l, Post{Title: "preview unavailable", Context: "the preview couldn't be delivered; try again in a moment", Provider: "unavailable"})
	_, err := e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.NewMessageUpdateV2(message.Components...).WithAllowedMentions(core.NoPings()), rest.WithCtx(ctx))
	m.guard.Report(g, guard.Struggling(err))
	return err == nil
}
