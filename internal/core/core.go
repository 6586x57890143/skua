// Package core is the module contract and the one place slash commands are
// registered and dispatched.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/intents"
)

// Module is a feature compiled into the binary. Modules never import each
// other; whatever one needs it takes as a constructor argument.
type Module interface {
	Name() string
	Want() intents.Want
	// Perms is what the module needs skua's role to hold in a channel.
	// Interaction replies need none; only calls skua makes as itself do.
	Perms() discord.Permissions
	Commands() []Command
}

// Tier is who may run a command. The zero value is deliberately invalid,
// so a forgotten tier fails boot instead of defaulting to public.
type Tier uint8

const (
	tierUnset Tier = iota
	Public
	Admin
)

// Command is one top-level application command: a slash command, or a
// message command (right-click, Apps). Run's ctx ends when Discord would
// stop waiting for the first response, so REST calls made with
// rest.WithCtx(ctx) fail in time to say why instead of leaving the member
// on "the application did not respond". A handler that defers and keeps
// working past that derives its own with context.WithoutCancel.
type Command struct {
	Create discord.ApplicationCommandCreate
	Tier   Tier
	Run    func(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error
}

// Modals is a Module that opens modals. It is optional, so modules without
// any implement nothing.
type Modals interface {
	Modals() []Modal
}

// Modal handles the submissions of every modal whose custom ID is ID, or
// starts with ID and a colon ("whisper-edit:<channel>:<message>"). The custom
// ID arrives from the member's client, so Run re-checks everything it
// relies on; Discord only promises the modal was one skua opened. Run's ctx
// is a command's: it ends with the response window.
type Modal struct {
	ID  string
	Run func(ctx context.Context, e *events.ModalSubmitInteractionCreate) error
}

// Components is a Module with buttons or other message components. Optional,
// like Modals.
type Components interface {
	Components() []Component
}

// Component handles every interaction with a component whose custom ID is
// ID, or starts with ID and a colon. Same caveats as Modal: the custom ID
// comes back from the member's client, and Run's ctx is a command's.
type Component struct {
	ID  string
	Run func(ctx context.Context, e *events.ComponentInteractionCreate) error
}

// cmdKey is how commands are found: Discord lets a slash command and a
// message command share a name.
type cmdKey struct {
	typ  discord.ApplicationCommandType
	name string
}

// respondBy is Discord's 3s initial-response window, less room for the
// error reply itself.
const respondBy = 2500 * time.Millisecond

// Router owns registration and dispatch. Not safe for Add after Freeze.
type Router struct {
	cmds      map[cmdKey]Command
	modals    map[string]Modal
	comps     map[string]Component
	creates   []discord.ApplicationCommandCreate
	bootstrap snowflake.ID
	owner     func(guild snowflake.ID) (snowflake.ID, bool)
	log       *slog.Logger
}

// NewRouter takes the bootstrap admin (0 for none) and a guild-owner lookup,
// which in production is the guild cache.
func NewRouter(bootstrap snowflake.ID, owner func(snowflake.ID) (snowflake.ID, bool), log *slog.Logger) *Router {
	return &Router{cmds: map[cmdKey]Command{}, modals: map[string]Modal{}, comps: map[string]Component{}, bootstrap: bootstrap, owner: owner, log: log}
}

// Add registers a module's commands, modals and components, refusing
// anything malformed.
func (r *Router) Add(m Module) error {
	for _, c := range m.Commands() {
		if c.Create == nil {
			return fmt.Errorf("core: a command from %s has no Create", m.Name())
		}
		k := cmdKey{c.Create.Type(), c.Create.CommandName()}
		switch {
		case c.Tier == tierUnset || c.Tier > Admin:
			return fmt.Errorf("core: /%s from %s has no valid tier", k.name, m.Name())
		case c.Run == nil:
			return fmt.Errorf("core: /%s from %s has no handler", k.name, m.Name())
		case r.cmds[k].Run != nil:
			return fmt.Errorf("core: /%s registered twice (second by %s)", k.name, m.Name())
		}
		r.cmds[k] = c
		r.creates = append(r.creates, c.Create)
	}
	if mm, ok := m.(Modals); ok {
		for _, md := range mm.Modals() {
			if err := claim("modal", md.ID, m.Name(), md.Run != nil, r.modals[md.ID].Run != nil); err != nil {
				return err
			}
			r.modals[md.ID] = md
		}
	}
	if cm, ok := m.(Components); ok {
		for _, c := range cm.Components() {
			if err := claim("component", c.ID, m.Name(), c.Run != nil, r.comps[c.ID].Run != nil); err != nil {
				return err
			}
			r.comps[c.ID] = c
		}
	}
	return nil
}

// claim checks one modal or component ID before it is registered.
func claim(kind, id, module string, hasRun, taken bool) error {
	switch {
	case id == "" || strings.Contains(id, ":"):
		return fmt.Errorf("core: %s %q from %s needs an ID with no colon", kind, id, module)
	case !hasRun:
		return fmt.Errorf("core: %s %s from %s has no handler", kind, id, module)
	case taken:
		return fmt.Errorf("core: %s %s registered twice (second by %s)", kind, id, module)
	}
	return nil
}

// Creates is the command set to bulk-overwrite into a guild.
func (r *Router) Creates() []discord.ApplicationCommandCreate { return r.creates }

var errDenied = errors.New("not allowed")

// Tell is an error written for the member. The router shows a Tell's text
// as it is, anywhere in the error's chain, and logs whatever else the chain
// carries; any other error is logged, and the member sees only that
// something failed on skua's side, so REST bodies and internal wording
// never reach Discord. A Tell is in skua's voice (UX.md): lowercase, no
// closing full stop.
type Tell string

func (t Tell) Error() string { return string(t) }

// failed is what the member sees for an error that is not a Tell.
const failed = "something went wrong on skua's side; try again in a moment"

// OnCommand dispatches one command. It never panics out: the gateway
// library's dispatch has no recover of its own.
func (r *Router) OnCommand(e *events.ApplicationCommandInteractionCreate) {
	name := e.Data.CommandName()
	defer r.recover(name)
	c, ok := r.cmds[cmdKey{e.Data.Type(), name}]
	if !ok {
		return
	}
	responded := track(&e.Respond)
	err := errDenied
	if r.allowed(e, c.Tier) {
		ctx, cancel := deadline(e.ID())
		defer cancel()
		err = c.Run(ctx, e)
	}
	r.report(name, err, *responded, e.CreateMessage, func(m discord.MessageCreate) error {
		_, err := e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(), m)
		return err
	})
}

// OnModal dispatches one modal submission by the part of its custom ID
// before the first colon. Same guarantees as OnCommand.
func (r *Router) OnModal(e *events.ModalSubmitInteractionCreate) {
	id, _, _ := strings.Cut(e.Data.CustomID, ":")
	defer r.recover(id)
	md, ok := r.modals[id]
	if !ok {
		return
	}
	responded := track(&e.Respond)
	ctx, cancel := deadline(e.ID())
	defer cancel()
	err := md.Run(ctx, e)
	r.report(id, err, *responded, e.CreateMessage, func(m discord.MessageCreate) error {
		_, err := e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(), m)
		return err
	})
}

// OnComponent dispatches one component interaction, a button press, by the
// part of its custom ID before the first colon. Same guarantees as
// OnCommand.
func (r *Router) OnComponent(e *events.ComponentInteractionCreate) {
	id, _, _ := strings.Cut(e.Data.CustomID(), ":")
	defer r.recover(id)
	c, ok := r.comps[id]
	if !ok {
		return
	}
	responded := track(&e.Respond)
	ctx, cancel := deadline(e.ID())
	defer cancel()
	err := c.Run(ctx, e)
	r.report(id, err, *responded, e.CreateMessage, func(m discord.MessageCreate) error {
		_, err := e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(), m)
		return err
	})
}

func (r *Router) recover(what string) {
	if p := recover(); p != nil {
		r.log.Error("interaction panicked", "handler", what, "panic", p, "stack", string(debug.Stack()))
	}
}

// deadline is the response window, anchored to when Discord created the
// interaction, so time spent queued before dispatch is not spent twice.
func deadline(id snowflake.ID) (context.Context, context.CancelFunc) {
	return context.WithDeadline(context.Background(), id.Time().Add(respondBy))
}

// track wraps a responder to record whether anything answered. Once a
// handler has answered or deferred, an error has to go out as a followup: a
// second initial response is refused and nobody sees it.
func track(respond *events.InteractionResponderFunc) *bool {
	responded, inner := new(bool), *respond
	*respond = func(t discord.InteractionResponseType, d discord.InteractionResponseData, opts ...rest.RequestOpt) error {
		err := inner(t, d, opts...)
		*responded = *responded || err == nil
		return err
	}
	return responded
}

// report tells the member what went wrong, where they will see it.
func (r *Router) report(what string, err error, responded bool, create func(discord.MessageCreate, ...rest.RequestOpt) error, followup func(discord.MessageCreate) error) {
	if err == nil {
		return
	}
	text := failed
	tell, isTell := errors.AsType[Tell](err)
	switch {
	case isTell:
		text = string(tell)
		// A Tell wrapping a cause (fmt.Errorf("%w: %w", tell, err)) still
		// shows the member only the Tell; the cause is for the log.
		if err.Error() != text {
			r.log.Warn("interaction failed", "handler", what, "err", err)
		}
	case errors.Is(err, errDenied):
		text = "only this server's admins can use /" + what
	default:
		r.log.Warn("interaction failed", "handler", what, "err", err)
	}
	msg := discord.MessageCreate{Content: "✗ " + text, Flags: discord.MessageFlagEphemeral, AllowedMentions: NoPings()}
	if responded {
		err = followup(msg)
	} else {
		err = create(msg)
	}
	if err != nil {
		r.log.Warn("reporting an interaction error", "handler", what, "err", err)
	}
}

// allowed fails closed: no guild, no member or an unknown owner is a no
// for anything above Public.
func (r *Router) allowed(e *events.ApplicationCommandInteractionCreate, t Tier) bool {
	if t == Public {
		return true
	}
	user := e.User().ID
	if r.bootstrap != 0 && user == r.bootstrap {
		return true
	}
	if m := e.Member(); m != nil && m.Permissions.Has(discord.PermissionAdministrator) {
		return true
	}
	if g := e.GuildID(); g != nil {
		if owner, ok := r.owner(*g); ok && owner == user {
			return true
		}
	}
	return false
}

// NoPings is what every message skua sends carries. It has to be an empty
// slice, not the zero value: disgo marshals a nil Parse as null, and only
// "parse":[] is an explicit "parse nothing" to Discord.
func NoPings() *discord.AllowedMentions {
	return &discord.AllowedMentions{Parse: []discord.AllowedMentionType{}}
}
