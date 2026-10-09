package notify

import (
	"context"
	"net/url"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// A test card is one an admin posts from the panel before anything real
// has happened: it says it's a test, pings no one, and carries ping me
// when skua can grant the follow's role, so members can take the role
// ahead of the first real card.

// profile is where an account lives on its platform.
func profile(platform, account string) string {
	a := url.PathEscape(account)
	switch platform {
	case "youtube":
		return "https://www.youtube.com/channel/" + a
	case "twitch":
		return "https://www.twitch.tv/" + a
	case "kick":
		return "https://kick.com/" + a
	case "x":
		return "https://x.com/" + a
	case "tiktok":
		return "https://www.tiktok.com/@" + a
	}
	return ""
}

func testCard(f follow, grant bool) discord.MessageCreate {
	what := "posts"
	if f.platform == "twitch" || f.platform == "kick" {
		what = "goes live"
	}
	it := item{
		Title:  "just a test so you can grab the role before the real thing",
		URL:    profile(f.platform, f.account),
		Author: f.name,
		Detail: "press ping me to hear when " + f.name + " " + what,
	}
	if !grant || f.role == 0 {
		it.Detail = ""
	}
	msg := card(brand.PlatformColor(f.platform), f.platform, "test card", "open", it, f.role, grant)
	// A test pings no one: the role's mention line goes, and so does its
	// allowance.
	if f.role != 0 {
		msg.Components = msg.Components[1:]
	}
	msg.AllowedMentions = core.NoPings()
	return msg
}

// test posts a test card for the follow picked, where its cards go, and
// redraws the panel saying so. Reading the roles and posting can outrun a
// press's three seconds, so the panel is deferred first.
func (m *Module) test(ctx context.Context, e *events.ComponentInteractionCreate, guild snowflake.ID, values []string) error {
	if len(values) != 1 {
		return core.Tell("pick one follow")
	}
	m.mu.Lock()
	var f follow
	found := false
	for _, o := range m.follows {
		if o.guild == guild && o.value() == values[0] {
			f, found = o, true
			break
		}
	}
	to := m.dest(f)
	m.mu.Unlock()
	switch {
	case !found:
		return core.Tell("that follow is gone; run /notify again")
	case to == 0:
		return core.Tell("pick where cards go first")
	}
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	r := e.Client().Rest
	if err := m.guard.Allow(guild, guard.MessageSend); err != nil {
		return core.Tell("too many cards here this hour; try again in a bit")
	}
	_, err := r.CreateMessage(to, testCard(f, m.canGrant(withCtx{r, ctx}, guild, f.role)), rest.WithCtx(ctx))
	m.guard.Report(guild, struggling(err))
	if err != nil {
		return core.Tell(why(err))
	}
	_, err = r.UpdateInteractionResponse(e.ApplicationID(), e.Token(), m.update(guild, "✓ test card for "+f.name+" posted in <#"+to.String()+">"), rest.WithCtx(ctx))
	return err
}
