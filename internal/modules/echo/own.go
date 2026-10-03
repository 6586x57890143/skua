package echo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/webhook"
)

// Members own what they echo: "Edit echo" and "Delete echo" sit under Apps
// when an echo is right-clicked. There is no table of who wrote what. The
// echo says so itself: it came through one of skua's webhooks (Discord
// stamps skua's application ID on it), and its last line is the marker with
// the member's username, which no one else's echo can end with.

// editModal is the custom ID prefix of the edit box; the webhook and the
// message being edited follow it, "echo-edit:<webhook>:<message>".
const editModal = "echo-edit"

var (
	errNotYours   = core.Tell("you can only change your own echoes")
	errNotServer  = core.Tell("this only works in a server")
	errNotDeleted = core.Tell("your echo wasn't deleted; try again in a moment")
	errNotFound   = core.Tell("your echo couldn't be found; it may have been deleted")
	errNotChanged = core.Tell("your echo wasn't changed; try again in a moment")
)

func (m *Module) ownCommands() []core.Command {
	guild := []discord.InteractionContextType{discord.InteractionContextTypeGuild}
	return []core.Command{
		{Create: discord.MessageCommandCreate{Name: "Edit echo", Contexts: guild}, Tier: core.Public, Run: m.editEcho},
		{Create: discord.MessageCommandCreate{Name: "Delete echo", Contexts: guild}, Tier: core.Public, Run: m.deleteEcho},
	}
}

// Modals is the edit box's submission.
func (m *Module) Modals() []core.Modal {
	return []core.Modal{{ID: editModal, Run: m.submitEdit}}
}

// ours is an echo skua posted: through a webhook, stamped with skua's
// application ID.
func ours(msg discord.Message, app snowflake.ID) bool {
	return msg.WebhookID != nil && msg.ApplicationID != nil && *msg.ApplicationID == app
}

// byMember returns the text of an echo whose marker names username, and
// whether it does.
func byMember(msg discord.Message, username string) (string, bool) {
	return strings.CutSuffix(msg.Content, marker(username))
}

func (m *Module) deleteEcho(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	msg := e.MessageCommandInteractionData().TargetMessage()
	member, guild := e.Member(), e.GuildID()
	if member == nil || guild == nil {
		return errNotServer
	}
	if _, mine := byMember(msg, member.User.Username); !mine || !ours(msg, e.ApplicationID()) {
		return errNotYours
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postBy)
	defer cancel()
	r := e.Client().Rest
	if err := m.post.Delete(ctx, r, *guild, msg.ChannelID, e.ApplicationID(), *msg.WebhookID, msg.ID); err != nil {
		return failed(errNotDeleted, err)
	}
	_ = r.DeleteInteractionResponse(e.ApplicationID(), e.Token(), rest.WithCtx(ctx))
	return nil
}

// editEcho opens the edit box, prefilled with the echo as it reads now.
func (m *Module) editEcho(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
	msg := e.MessageCommandInteractionData().TargetMessage()
	member := e.Member()
	if member == nil {
		return errNotServer
	}
	text, mine := byMember(msg, member.User.Username)
	if !mine || !ours(msg, e.ApplicationID()) {
		return errNotYours
	}
	return e.Modal(discord.ModalCreate{
		CustomID: fmt.Sprintf("%s:%d:%d", editModal, *msg.WebhookID, msg.ID),
		Title:    "Edit echo",
		Components: []discord.LayoutComponent{discord.LabelComponent{
			Label: "Message",
			Component: discord.TextInputComponent{
				CustomID: "message", Style: discord.TextInputStyleShort,
				Required: true, MaxLength: maxText, Value: text,
			},
		}},
	})
}

// submitEdit applies the edit box. Everything in its custom ID came back
// from the member's client, so the echo is fetched through skua's webhook
// and its marker checked again before anything changes.
func (m *Module) submitEdit(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	hookID, msgID, ok := parseEdit(e.Data.CustomID)
	if !ok {
		return core.Tell("that edit box is out of date; open it again")
	}
	member, guild := e.Member(), e.GuildID()
	if member == nil || guild == nil {
		return errNotServer
	}
	if err := gates(member); err != nil {
		return err
	}
	text := clean(e.Data.Text("message"))
	if text == "" {
		return errEmpty
	}
	v := m.screen.Check(text)
	if v.Block != "" {
		return core.Tell("skua won't post that: it contains " + v.Block)
	}
	if err := m.guard.Allow(member.User.ID, guard.EchoMember); err != nil {
		return errTooFast
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postBy)
	defer cancel()
	r, ch, app := e.Client().Rest, e.Channel().ID(), e.ApplicationID()
	msg, err := m.post.Get(ctx, r, ch, app, hookID, msgID)
	if errors.Is(err, webhook.ErrNotOurs) {
		return errNotYours
	}
	if err != nil {
		return failed(errNotFound, err)
	}
	// The webhook is skua's, or Get would have refused; the marker says
	// whose echo it is.
	if _, mine := byMember(*msg, member.User.Username); !mine {
		return errNotYours
	}
	content := v.Text + marker(member.User.Username)
	update := discord.WebhookMessageUpdate{Content: &content, AllowedMentions: core.NoPings()}
	if !member.Permissions.Has(discord.PermissionEmbedLinks) {
		update.Flags = new(discord.MessageFlagSuppressEmbeds)
	}
	if err := m.post.Edit(ctx, r, *guild, ch, app, hookID, msgID, update); err != nil {
		return failed(errNotChanged, err)
	}
	_ = r.DeleteInteractionResponse(app, e.Token(), rest.WithCtx(ctx))
	return nil
}

func parseEdit(customID string) (hook, msg snowflake.ID, ok bool) {
	parts := strings.Split(customID, ":")
	if len(parts) != 3 || parts[0] != editModal {
		return 0, 0, false
	}
	h, err1 := snowflake.Parse(parts[1])
	m, err2 := snowflake.Parse(parts[2])
	return h, m, err1 == nil && err2 == nil
}
