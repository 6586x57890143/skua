// Package purge is /purge: a member deletes their own messages in a server.
// Only their own. skua holds Manage Messages to do it, which would let it
// delete anyone's, so what is deleted is decided by one thing: the author on
// each message skua fetched.
//
// Bots can't search, so skua reads every channel and thread it can see
// once, oldest first, and keeps an index of who wrote which message
// (postings.go). A purge catches the index up on what's new, which is
// usually nothing to read at all, then deletes from it, many channels at
// once. Recent messages go a hundred to a call; Discord only lets anything
// over 14 days go one at a time, which is the long tail of a first purge
// and nothing makes it shorter. Every request waits for a slot on one
// process-wide pacer, so many purges at once share Discord's global limit
// instead of tripping it.
package purge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// confirmModal is the custom ID of the box that asks for "delete", and
// progressButton of the button that opens a fresh readout.
const (
	confirmModal   = "purge-now"
	progressButton = "purge-progress"
)

// tokenLife is how long an interaction's token can edit its response:
// Discord's 15 minutes, less a margin. A purge outlives it easily, and
// nothing can post an ephemeral reply without a token, so a readout about
// to lose its token carries on in a DM to whoever was watching it, edited
// every dmTick until the purge ends. If skua can't DM them, the readout
// ends on a progress button instead: a press is a new interaction, whose
// token starts a fresh readout of the same purge.
const (
	tokenLife = 14 * time.Minute
	dmTick    = 15 * time.Second
)

type target struct{ guild, user snowflake.ID }

// run is a purge in progress, there to be cancelled by /purge stop,
// watched by the progress button and listed by /purge jobs.
type run struct {
	cancel    context.CancelFunc
	job       *job
	scheduled bool // started by /purge every, not /purge now
}

// DB is the slice of pgxpool.Pool purge uses.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Module struct {
	// bootstrap is the break-glass admin, the one user who can run /purge
	// for someone else. 0 is nobody.
	bootstrap snowflake.ID
	guard     *guard.Guard
	db        DB // nil without SKUA_DATABASE_URL: only /purge now works
	idx       index
	log       *slog.Logger
	pace      *pacer
	running   sync.Map // target -> *run
	now       func() time.Time
	tick      time.Duration // how often the progress reply is edited
	dmTick    time.Duration // how often a readout carried on in a DM is
	boot      sync.Once

	// Scheduled sweeps: the guilds with one running, and how often due
	// sweeps are looked for.
	sweeping  sync.Map // guild -> struct{}
	catching  sync.Map // guild -> *catchup: the index catch-up running there
	schedTick time.Duration

	// Live mode: who is live, at what delay, and what is waiting to go.
	live   sync.Map // target -> time.Duration
	liveN  atomic.Int64
	mu     sync.Mutex
	queues map[snowflake.ID]*queue // channel ->
	window time.Duration           // how long a due delete waits for neighbours
}

// New takes the process's one guard, the database (which may be nil) and
// the break-glass admin (0 for none).
func New(g *guard.Guard, db DB, log *slog.Logger, bootstrap snowflake.ID) *Module {
	m := &Module{
		bootstrap: bootstrap, guard: g, log: log, pace: newPacer(rate), now: time.Now, tick: 5 * time.Second, dmTick: dmTick,
		queues: map[snowflake.ID]*queue{}, window: batchWindow, schedTick: scheduleTick,
	}
	m.useDB(db)
	return m
}

// useDB keeps the index in db, or in memory when there is none.
func (m *Module) useDB(db DB) {
	m.db, m.idx = db, newMemIndex()
	if db != nil {
		m.idx = pgIndex{db}
	}
}

func (*Module) Name() string { return "purge" }

// Want is guild messages, for live mode to see a member post. Content is
// not needed: the author and the IDs are enough.
func (*Module) Want() intents.Want { return intents.Want{Required: gateway.IntentGuildMessages} }

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
				discord.ApplicationCommandOptionSubCommand{Name: "now", Description: "delete every message you've sent here", Options: forMember()},
				discord.ApplicationCommandOptionSubCommand{Name: "stop", Description: "stop deleting", Options: forMember()},
				discord.ApplicationCommandOptionSubCommand{
					Name: "live", Description: "delete each message you send here a while after you send it",
					Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
						Name: "after", Description: "how long each message stays up", Required: true, Choices: choices(delays),
					}, member},
				},
				discord.ApplicationCommandOptionSubCommand{
					Name: "every", Description: "sweep your messages here on a schedule",
					Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
						Name: "every", Description: "how often", Required: true, Choices: choices(everyChoices),
					}, member},
				},
				discord.ApplicationCommandOptionSubCommand{Name: "status", Description: "what's set up here and how your last sweep went", Options: forMember()},
				discord.ApplicationCommandOptionSubCommand{Name: "jobs", Description: "break-glass admin only: every purge running or coming up, in every server"},
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

// Components is the progress button.
func (m *Module) Components() []core.Component {
	return []core.Component{{ID: progressButton, Run: m.progress}}
}

// progress opens a fresh readout of a running purge, on the token of the
// press. The button names whose purge it watches; only they, or the
// break-glass admin, can open it.
func (m *Module) progress(_ context.Context, e *events.ComponentInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return errNotServer
	}
	by := e.User().ID
	user := by
	if _, rest, ok := strings.Cut(e.Data.CustomID(), ":"); ok {
		id, err := snowflake.Parse(rest)
		if err != nil {
			return core.Tell("that button is out of date; /purge status has how it went")
		}
		if id != by {
			if err := m.breakGlass(by, id, *guild, "progress"); err != nil {
				return err
			}
		}
		user = id
	}
	v, ok := m.running.Load(target{*guild, user})
	if !ok {
		return core.Tell("that purge is over; /purge status has how it went")
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	go m.watch(v.(*run).job, e.Client().Rest, e.ApplicationID(), e.Token(), user, by)
	return nil
}

var (
	errNotServer = core.Tell("/purge only works in a server")
	errRunning   = core.Tell("a purge is already running here; /purge stop ends it")
)

func (m *Module) purge(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return errNotServer
	}
	data := e.SlashCommandInteractionData()
	sub := ""
	if data.SubCommandName != nil {
		sub = *data.SubCommandName
	}
	if sub == "jobs" {
		return m.jobsCmd(ctx, e)
	}
	user, behalf := e.User().ID, false
	if id, ok := data.OptSnowflake("member"); ok && id != user {
		if err := m.breakGlass(user, id, *guild, sub); err != nil {
			return err
		}
		user, behalf = id, true
	}
	k := target{*guild, user}
	switch sub {
	case "stop":
		v, ok := m.running.Load(k)
		if !ok {
			return core.Tell("no purge is running here")
		}
		v.(*run).cancel()
		return reply(e, "✓ stopping; what's already deleted stays deleted")
	case "live":
		return m.setLiveCmd(ctx, e, k, data.String("after"))
	case "every":
		return m.setEveryCmd(ctx, e, k, data.String("every"))
	case "status":
		return m.statusCmd(ctx, e, k)
	}
	if _, ok := m.running.Load(k); ok {
		return errRunning
	}
	id, title, whose := confirmModal, "delete your messages", "every message you've sent"
	if behalf {
		id, title, whose = fmt.Sprintf("%s:%d", confirmModal, user), "delete a member's messages", "every message they've sent"
	}
	return e.Modal(discord.ModalCreate{
		CustomID: id,
		Title:    title,
		Components: []discord.LayoutComponent{discord.LabelComponent{
			Label:       "type delete to confirm",
			Description: whose + " in this server, in every channel skua can read; this can't be undone",
			Component: discord.TextInputComponent{
				CustomID: "confirm", Style: discord.TextInputStyleShort, Required: true, MaxLength: 6,
			},
		}},
	})
}

// member is the break-glass option: whose messages, when not the caller's.
var member = discord.ApplicationCommandOptionUser{Name: "member", Description: "break-glass admin only: whose messages"}

func forMember() []discord.ApplicationCommandOption {
	return []discord.ApplicationCommandOption{member}
}

var errNotYours = core.Tell("only skua's break-glass admin can do that for someone else; leave member out to do it for yourself")

// breakGlass lets by act for user only if by is the break-glass admin.
// Server admins and owners get nothing here: a member's messages are
// theirs. Every use is logged.
func (m *Module) breakGlass(by, user, guild snowflake.ID, sub string) error {
	if m.bootstrap == 0 || by != m.bootstrap {
		return errNotYours
	}
	m.log.Warn("purge: break-glass", "sub", sub, "by", by, "for", user, "guild", guild)
	return nil
}

func reply(e *events.ApplicationCommandInteractionCreate, text string) error {
	return e.CreateMessage(discord.MessageCreate{Content: text, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}

var errNoDB = core.Tell("skua runs without a database here, so it can't remember this; only /purge now works")

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
	by := e.User().ID
	user := by
	if _, rest, ok := strings.Cut(e.Data.CustomID, ":"); ok {
		// The ID came back from the client: check it all again.
		id, err := snowflake.Parse(rest)
		if err != nil {
			return core.Tell("that box is out of date; run /purge now again")
		}
		if id != by {
			if err := m.breakGlass(by, id, *guild, "now"); err != nil {
				return err
			}
		}
		user = id
	}
	k := target{*guild, user}
	ctx, cancel := context.WithCancel(context.Background())
	j := m.newJob(e.Client().Rest, *guild, []snowflake.ID{user})
	if _, loaded := m.running.LoadOrStore(k, &run{cancel, j, false}); loaded {
		cancel()
		return errRunning
	}
	start := func() error {
		if m.guard.Allow(by, guard.PurgeMember) != nil {
			return core.Tell("you've started /purge as often as an hour allows; try again later")
		}
		return e.DeferCreateMessage(true)
	}
	if err := start(); err != nil {
		m.running.Delete(k)
		cancel()
		return err
	}
	go m.follow(ctx, cancel, k, j)
	go m.watch(j, e.Client().Rest, e.ApplicationID(), e.Token(), user, by)
	return nil
}

// job is one purge: catch the guild's index up, then delete from it.
type job struct {
	scan  atomic.Pointer[scan] // the catch-up it is waiting on, for the readout
	sweep *sweep
	began time.Time
	// deleting is set once the catch-up is done: the counts before it
	// aren't measured yet. deleteFrom and deleteTo time the deletes, in
	// unix nanoseconds, for the rate.
	deleting   atomic.Bool
	deleteFrom atomic.Int64
	deleteTo   atomic.Int64
	done       chan struct{} // closed when it ends
	err        error         // how it ended, set before done closes
}

func (m *Module) newJob(r rest.Rest, guild snowflake.ID, authors []snowflake.ID) *job {
	counts := map[snowflake.ID]*atomic.Int64{}
	for _, a := range authors {
		counts[a] = new(atomic.Int64)
	}
	return &job{began: m.now(), done: make(chan struct{}), sweep: &sweep{
		r: r, guard: m.guard, pace: m.pace, idx: m.idx, guild: guild,
		authors: counts, cutoff: snowflake.New(m.now()), now: m.now,
	}}
}

// work runs j: catch-ups until one has read past the moment j began, so
// nothing sent before the member asked is missed, then the deletes.
func (m *Module) work(ctx context.Context, j *job) error {
	for {
		c := m.catchUp(j.sweep.r, j.sweep.guild)
		j.scan.Store(c.scan)
		select {
		case <-c.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		if c.err != nil {
			return c.err
		}
		if c.scan.cutoff >= j.sweep.cutoff {
			j.deleteFrom.Store(m.now().UnixNano())
			j.deleting.Store(true)
			err := j.sweep.run(ctx)
			j.deleteTo.Store(m.now().UnixNano())
			return err
		}
	}
}

// catchup is a scan other purges in the guild can wait on.
type catchup struct {
	scan *scan
	done chan struct{}
	err  error // set before done closes
}

// catchUp joins the guild's running catch-up, or starts one. It runs to
// the end whoever stops waiting: what it reads serves every member.
func (m *Module) catchUp(r rest.Rest, guild snowflake.ID) *catchup {
	c := &catchup{done: make(chan struct{}), scan: &scan{
		r: r, guard: m.guard, pace: m.pace, idx: m.idx, guild: guild,
		cutoff: snowflake.New(m.now()), began: m.now(),
	}}
	if v, loaded := m.catching.LoadOrStore(guild, c); loaded {
		return v.(*catchup)
	}
	go func() {
		c.err = c.scan.run(context.Background())
		m.catching.Delete(guild)
		close(c.done)
	}()
	return c
}

// unreachable is every channel the job couldn't read or delete in.
func (j *job) unreachable() []snowflake.ID {
	var ids []snowflake.ID
	if sc := j.scan.Load(); sc != nil {
		sc.mu.Lock()
		ids = append(ids, sc.unreachable...)
		sc.mu.Unlock()
	}
	j.sweep.mu.Lock()
	ids = append(ids, j.sweep.unreachable...)
	j.sweep.mu.Unlock()
	slices.Sort(ids)
	return slices.Compact(ids)
}

// follow runs j, records how it went and ends it, for every readout
// watching it.
func (m *Module) follow(ctx context.Context, cancel context.CancelFunc, k target, j *job) {
	defer m.running.Delete(k)
	defer cancel()
	j.err = m.work(ctx, j)
	if m.db != nil {
		m.record(j, j.err)
	}
	close(j.done)
}

// watch keeps one ephemeral readout of owner's purge j counting until j
// ends. When its token is about to go, the readout moves to a DM to
// viewer, who started it or pressed progress; failing that it ends on the
// progress button.
func (m *Module) watch(j *job, r rest.Rest, app snowflake.ID, token string, owner, viewer snowflake.ID) {
	opened := m.now()
	t := time.NewTicker(m.tick)
	defer t.Stop()
	for {
		select {
		case <-j.done:
			if m.now().Sub(opened) <= tokenLife {
				m.show(r, app, token, m.readout(j, outcome(j.err, m.now().Sub(j.began))), nil)
			}
			return
		case <-t.C:
			left := tokenLife - m.now().Sub(opened)
			if left < 0 {
				return
			}
			line := "still going · " + span(m.now().Sub(j.began))
			if left >= 2*m.tick {
				m.show(r, app, token, m.readout(j, line), nil)
				continue
			}
			if m.dm(j, r, viewer) {
				m.show(r, app, token, m.readout(j, line+" · the rest is in your dms"), nil)
				return
			}
			button := discord.NewSecondaryButton("progress", fmt.Sprintf("%s:%d", progressButton, owner))
			m.show(r, app, token, m.readout(j, line+" · skua can't dm you, so this readout stops here; progress opens a new one"),
				[]discord.LayoutComponent{discord.NewActionRow(button)})
			return
		}
	}
}

// dm sends viewer the readout of j and keeps editing it until j ends. It
// reports whether the DM went: a member can close DMs from server members,
// and the guild's send budget counts it.
func (m *Module) dm(j *job, r rest.Rest, viewer snowflake.ID) bool {
	guild := j.sweep.guild
	if m.guard.Allow(guild, guard.MessageSend) != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dm, err := r.CreateDMChannel(viewer, rest.WithCtx(ctx))
	m.guard.Report(guild, struggling(err))
	if err != nil {
		return false
	}
	where := fmt.Sprintf("\n-# /purge now in https://discord.com/channels/%d", guild)
	text := m.readout(j, "still going · "+span(m.now().Sub(j.began))) + where
	msg, err := r.CreateMessage(dm.ID(), discord.MessageCreate{Content: text, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx))
	m.guard.Report(guild, struggling(err))
	if err != nil {
		return false
	}
	go func() {
		edit := func(text string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = r.UpdateMessage(dm.ID(), msg.ID, discord.MessageUpdate{Content: &text, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx))
		}
		t := time.NewTicker(m.dmTick)
		defer t.Stop()
		for {
			select {
			case <-j.done:
				edit(m.readout(j, outcome(j.err, m.now().Sub(j.began))) + where)
				return
			case <-t.C:
				edit(m.readout(j, "still going · "+span(m.now().Sub(j.began))) + where)
			}
		}
	}()
	return true
}

// show edits a readout. components replaces its buttons; nil clears them.
func (m *Module) show(r rest.Rest, app snowflake.ID, token string, text string, components []discord.LayoutComponent) {
	if components == nil {
		components = []discord.LayoutComponent{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.UpdateInteractionResponse(app, token, discord.MessageUpdate{Content: &text, Components: &components, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx))
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
// deleted is out of every message the index holds for the member, its bar
// counting misses too; rate is deletes a minute since deleting began.
// scanned is what this purge had to read; channels is how far that got.
func render(j *job, line string, now time.Time) string {
	deleted, missed, rate, scanned, channels := notYet, notYet, notYet, notYet, listing
	if j.deleting.Load() {
		s := j.sweep
		n, total := s.deleted.Load(), s.total.Load()
		deleted = fmt.Sprintf("%d of %d %s", n, total, bar(s.handled.Load(), total))
		missed = fmt.Sprint(s.missed.Load())
		to := now
		if t := j.deleteTo.Load(); t != 0 {
			to = time.Unix(0, t)
		}
		if took := to.Sub(time.Unix(0, j.deleteFrom.Load())); took >= rateAfter {
			rate = fmt.Sprintf("%d a minute", int64(float64(n)/took.Minutes()))
		} else if j.deleteTo.Load() != 0 {
			rate = "too quick to measure"
		}
	}
	if sc := j.scan.Load(); sc != nil && sc.listed.Load() {
		scanned, channels = fmt.Sprint(sc.scanned.Load()), sc.progress()
	}
	unreachable := j.unreachable()
	return grid([][2]string{
		{"deleted", deleted},
		{"rate", rate},
		{"missed", missed},
		{"scanned", scanned},
		{"channels", channels},
		{"unreachable", fmt.Sprint(len(unreachable))},
	}) + "\n-# " + line + couldnt(unreachable)
}

// readout is render at the module's now.
func (m *Module) readout(j *job, line string) string { return render(j, line, m.now()) }

// rateAfter is how long deletes must run before their rate means much.
const rateAfter = 10 * time.Second

// barCells is how wide a progress bar is: the widest value it sits beside
// ("1234567 of 2345678") still fits UX.md's 40 columns.
const barCells = 8

// bar is done of total as barCells cells, or nothing without a total.
func bar(done, total int64) string {
	if total <= 0 {
		return ""
	}
	full := int(min(done, total) * barCells / total)
	return strings.Repeat("▰", full) + strings.Repeat("▱", barCells-full)
}

// notYet and listing are counts that haven't been measured yet (UX.md).
const (
	notYet  = "not yet"
	listing = "listing"
)

// grid is facts in a code block, labels padded to one column.
func grid(rows [][2]string) string {
	var b strings.Builder
	b.WriteString("```\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-*s%s\n", labelWidth, r[0], r[1])
	}
	b.WriteString("```")
	return b.String()
}

// couldnt names the channels a sweep couldn't read, ten at most.
func couldnt(ids []snowflake.ID) string {
	if len(ids) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n-# skua couldn't read all of")
	for i, id := range ids {
		if i == 10 {
			fmt.Fprintf(&b, " and %d more", len(ids)-10)
			break
		}
		fmt.Fprintf(&b, " <#%d>", id)
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
