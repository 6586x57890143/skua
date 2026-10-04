// Package purge is /purge: a member deletes their own messages in a server.
// Only their own. skua holds Manage Messages to do it, which would let it
// delete anyone's, so what is deleted is decided by one thing: the author on
// each message skua fetched.
//
// Bots can't search, so a sweep reads every channel and thread skua can
// see, oldest first, and deletes as it reads. Recent messages go a hundred
// to a call; Discord only lets anything over 14 days go one at a time,
// which is the long tail of a first sweep and nothing makes it shorter.
// Every sweep request waits for a slot on one process-wide pacer, so many
// sweeps at once share Discord's global limit instead of tripping it.
package purge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// confirmModal is the custom ID of the box that asks for "delete".
const confirmModal = "purge-now"

// tokenLife is how long an interaction's token can edit its response:
// Discord's 15 minutes, less a margin. A sweep still running past it keeps
// going; the member just stops seeing it count.
//
// ponytail: nothing reports a sweep that outlives the token. /purge status
// (with the purge table) is the place for that.
const tokenLife = 14 * time.Minute

type target struct{ guild, user snowflake.ID }

// run is a sweep in progress, there to be cancelled by /purge stop.
type run struct{ cancel context.CancelFunc }

type Module struct {
	guard   *guard.Guard
	pace    *pacer
	running sync.Map // target -> *run
	now     func() time.Time
	tick    time.Duration // how often the progress reply is edited
}

// New takes the process's one guard.
func New(g *guard.Guard) *Module {
	return &Module{guard: g, pace: newPacer(rate), now: time.Now, tick: 5 * time.Second}
}

func (*Module) Name() string { return "purge" }

// Want is nothing: a sweep is REST only.
func (*Module) Want() intents.Want { return intents.Want{} }

// Perms is reading every channel's history and deleting from it, plus
// Manage Threads, without which private archived threads can't be listed.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionReadMessageHistory |
		discord.PermissionManageMessages | discord.PermissionManageThreads
}

func (m *Module) Commands() []core.Command {
	return []core.Command{{
		Create: discord.SlashCommandCreate{
			Name:        "purge",
			Description: "delete your own messages in this server",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{Name: "now", Description: "delete every message you've sent here"},
				discord.ApplicationCommandOptionSubCommand{Name: "stop", Description: "stop deleting"},
			},
		},
		Tier: core.Public,
		Run:  m.purge,
	}}
}

// Modals is the confirmation box.
func (m *Module) Modals() []core.Modal {
	return []core.Modal{{ID: confirmModal, Run: m.confirm}}
}

var (
	errNotServer = core.Tell("/purge only works in a server")
	errRunning   = core.Tell("your purge here is already running; /purge stop ends it")
)

func (m *Module) purge(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return errNotServer
	}
	k := target{*guild, e.User().ID}
	if sub := e.SlashCommandInteractionData().SubCommandName; sub != nil && *sub == "stop" {
		v, ok := m.running.Load(k)
		if !ok {
			return core.Tell("you have no purge running here")
		}
		v.(*run).cancel()
		return e.CreateMessage(discord.MessageCreate{
			Content: "✓ stopping; what's already deleted stays deleted", Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings(),
		})
	}
	if _, ok := m.running.Load(k); ok {
		return errRunning
	}
	return e.Modal(discord.ModalCreate{
		CustomID: confirmModal,
		Title:    "Delete your messages",
		Components: []discord.LayoutComponent{discord.LabelComponent{
			Label:       "Type delete to confirm",
			Description: "Every message you've sent in this server, in every channel skua can read. This can't be undone.",
			Component: discord.TextInputComponent{
				CustomID: "confirm", Style: discord.TextInputStyleShort, Required: true, MaxLength: 6,
			},
		}},
	})
}

// confirm starts the sweep once the member has typed "delete". It answers
// at once and the sweep reports by editing that answer.
func (m *Module) confirm(_ context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return errNotServer
	}
	if !strings.EqualFold(strings.TrimSpace(e.Data.Text("confirm")), "delete") {
		return core.Tell("nothing deleted: type delete to confirm")
	}
	user := e.User().ID
	k := target{*guild, user}
	ctx, cancel := context.WithCancel(context.Background())
	if _, loaded := m.running.LoadOrStore(k, &run{cancel}); loaded {
		cancel()
		return errRunning
	}
	start := func() error {
		if m.guard.Allow(user, guard.PurgeMember) != nil {
			return core.Tell("you've started /purge as often as an hour allows; try again later")
		}
		return e.DeferCreateMessage(true)
	}
	if err := start(); err != nil {
		m.running.Delete(k)
		cancel()
		return err
	}
	s := &sweep{
		r: e.Client().Rest, guard: m.guard, pace: m.pace, guild: *guild,
		authors: map[snowflake.ID]bool{user: true},
		cutoff:  snowflake.New(m.now()), now: m.now,
	}
	go m.follow(ctx, cancel, k, s, e.Client().Rest, e.ApplicationID(), e.Token())
	return nil
}

// follow runs s and keeps the member's reply counting until it ends.
func (m *Module) follow(ctx context.Context, cancel context.CancelFunc, k target, s *sweep, r rest.Rest, app snowflake.ID, token string) {
	defer m.running.Delete(k)
	defer cancel()
	began := m.now()
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	t := time.NewTicker(m.tick)
	defer t.Stop()
	for {
		select {
		case err := <-done:
			m.show(r, app, token, began, render(s, outcome(err, m.now().Sub(began))))
			return
		case <-t.C:
			m.show(r, app, token, began, render(s, "still going · "+span(m.now().Sub(began))))
		}
	}
}

func (m *Module) show(r rest.Rest, app snowflake.ID, token string, began time.Time, text string) {
	if m.now().Sub(began) > tokenLife {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.UpdateInteractionResponse(app, token, discord.MessageUpdate{Content: &text, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx))
}

// outcome is the line under the readout once a sweep has ended.
func outcome(err error, took time.Duration) string {
	switch {
	case err == nil:
		return "✓ done in " + span(took)
	case errors.Is(err, context.Canceled):
		return "✗ stopped after " + span(took)
	}
	if t, ok := errors.AsType[core.Tell](err); ok {
		return "✗ " + string(t)
	}
	return "✗ something went wrong on skua's side after " + span(took) + "; run /purge now again to pick up the rest"
}

// labelWidth is the longest label plus two (UX.md's grid).
const labelWidth = len("unreachable") + 2

// render is the readout: facts in a code block, what happened below it.
func render(s *sweep, line string) string {
	s.mu.Lock()
	unreachable := slices.Clone(s.unreachable)
	s.mu.Unlock()
	slices.Sort(unreachable)
	unreachable = slices.Compact(unreachable)
	var b strings.Builder
	b.WriteString("```\n")
	for _, r := range [][2]string{
		{"deleted", fmt.Sprint(s.deleted.Load())},
		{"missed", fmt.Sprint(s.missed.Load())},
		{"scanned", fmt.Sprint(s.scanned.Load())},
		{"channels", fmt.Sprintf("%d of %d", s.done.Load(), s.channels.Load())},
		{"unreachable", fmt.Sprint(len(unreachable))},
	} {
		fmt.Fprintf(&b, "%-*s%s\n", labelWidth, r[0], r[1])
	}
	b.WriteString("```\n-# " + line)
	if len(unreachable) > 0 {
		b.WriteString("\n-# skua couldn't read all of")
		for i, id := range unreachable {
			if i == 10 {
				fmt.Fprintf(&b, " and %d more", len(unreachable)-10)
				break
			}
			fmt.Fprintf(&b, " <#%d>", id)
		}
	}
	return b.String()
}

// span is the two largest units that matter: "45s", "12m", "3h 12m", "2d 4h".
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}
