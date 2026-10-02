// Package core is the module contract and the one place slash commands are
// registered and dispatched.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
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

// Command is one top-level slash command. Run's ctx ends when Discord would
// stop waiting for the first response, so REST calls made with
// rest.WithCtx(ctx) fail in time to say why instead of leaving the member
// on "the application did not respond". A handler that defers and keeps
// working past that derives its own with context.WithoutCancel.
type Command struct {
	Create discord.SlashCommandCreate
	Tier   Tier
	Run    func(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error
}

// respondBy is Discord's 3s initial-response window, less room for the
// error reply itself.
const respondBy = 2500 * time.Millisecond

// Router owns registration and dispatch. Not safe for Add after Freeze.
type Router struct {
	cmds      map[string]Command
	creates   []discord.ApplicationCommandCreate
	bootstrap snowflake.ID
	owner     func(guild snowflake.ID) (snowflake.ID, bool)
	log       *slog.Logger
}

// NewRouter takes the bootstrap admin (0 for none) and a guild-owner lookup,
// which in production is the guild cache.
func NewRouter(bootstrap snowflake.ID, owner func(snowflake.ID) (snowflake.ID, bool), log *slog.Logger) *Router {
	return &Router{cmds: map[string]Command{}, bootstrap: bootstrap, owner: owner, log: log}
}

// Add registers a module's commands, refusing anything malformed.
func (r *Router) Add(m Module) error {
	for _, c := range m.Commands() {
		name := c.Create.Name
		switch {
		case c.Tier == tierUnset || c.Tier > Admin:
			return fmt.Errorf("core: /%s from %s has no valid tier", name, m.Name())
		case c.Run == nil:
			return fmt.Errorf("core: /%s from %s has no handler", name, m.Name())
		case r.cmds[name].Run != nil:
			return fmt.Errorf("core: /%s registered twice (second by %s)", name, m.Name())
		}
		r.cmds[name] = c
		r.creates = append(r.creates, c.Create)
	}
	return nil
}

// Creates is the command set to bulk-overwrite into a guild.
func (r *Router) Creates() []discord.ApplicationCommandCreate { return r.creates }

var errDenied = errors.New("not allowed")

// OnCommand dispatches one interaction. It never panics out: the gateway
// library's dispatch has no recover of its own.
func (r *Router) OnCommand(e *events.ApplicationCommandInteractionCreate) {
	name := e.Data.CommandName()
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("command panicked", "command", name, "panic", p, "stack", string(debug.Stack()))
		}
	}()
	c, ok := r.cmds[name]
	if !ok {
		return
	}
	// Once a handler has answered or deferred, the error has to go out as a
	// followup: a second initial response is refused and nobody sees it.
	responded := false
	respond := e.Respond
	e.Respond = func(t discord.InteractionResponseType, d discord.InteractionResponseData, opts ...rest.RequestOpt) error {
		err := respond(t, d, opts...)
		responded = responded || err == nil
		return err
	}
	err := errDenied
	if r.allowed(e, c.Tier) {
		// Anchored to when Discord created the interaction, so time spent
		// queued before dispatch is not silently spent twice.
		ctx, cancel := context.WithDeadline(context.Background(), e.ID().Time().Add(respondBy))
		defer cancel()
		err = c.Run(ctx, e)
	}
	if err == nil {
		return
	}
	if !errors.Is(err, errDenied) {
		r.log.Warn("command failed", "command", name, "err", err)
	}
	msg := discord.MessageCreate{Content: "✗ " + err.Error(), Flags: discord.MessageFlagEphemeral, AllowedMentions: NoPings()}
	if responded {
		_, err = e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(), msg)
	} else {
		err = e.CreateMessage(msg)
	}
	if err != nil {
		r.log.Warn("reporting a command error", "command", name, "err", err)
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
