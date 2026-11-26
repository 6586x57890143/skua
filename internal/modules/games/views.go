package games

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/game"
	"github.com/6586x57890143/skua/internal/game/count"
)

const (
	// boardRows is how many members a board shows, the member's own line
	// in the last place when they're further down.
	boardRows = 10
	// proofMost is the longest run a proof file carries; a longer one is
	// checked all the same, and its file holds the end of it.
	proofMost = 20000
)

var (
	errNotCounting = core.Tell("there's no counting here yet; an admin sets it up on the games /help page")
	errNoLog       = core.Tell("skua keeps no log without a database, so there's nothing to check")
)

// Commands is /count for anyone and /recount for admins.
func (m *Module) Commands() []core.Command {
	minNumber := 0
	return []core.Command{{
		Create: discord.SlashCommandCreate{
			Name:        "count",
			Description: "the count here, the board and where you stand",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionChannel{Name: "channel", Description: "a counting channel other than this one", ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText}},
			},
		},
		Tier: core.Public,
		Run:  m.show,
	}, {
		Create: discord.SlashCommandCreate{
			Name:        "recount",
			Description: "start a counting channel from a number, moving from another bot",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionInt{Name: "number", Description: "the last number counted there", Required: true, MinValue: &minNumber},
				discord.ApplicationCommandOptionChannel{Name: "channel", Description: "a counting channel other than this one", ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText}},
			},
		},
		Tier: core.Admin,
		Run:  m.recount,
	}}
}

// where is the counting channel a command means: the one named, this one,
// or the server's first.
func (m *Module) where(guild, here snowflake.ID, named *snowflake.ID) (uint64, error) {
	chans := m.counting(guild)
	if named != nil {
		for _, ch := range chans {
			if ch == uint64(*named) {
				return ch, nil
			}
		}
		return 0, errNotCounting
	}
	for _, ch := range chans {
		if ch == uint64(here) {
			return ch, nil
		}
	}
	if len(chans) == 0 {
		return 0, errNotCounting
	}
	return chans[0], nil
}

func named(e *events.ApplicationCommandInteractionCreate) *snowflake.ID {
	if c, ok := e.SlashCommandInteractionData().OptChannel("channel"); ok {
		return &c.ID
	}
	return nil
}

// show is /count: the channel's run and the member's standing.
func (m *Module) show(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return errNotCounting
	}
	ch, err := m.where(*guild, e.Channel().ID(), named(e))
	if err != nil {
		return err
	}
	return e.CreateMessage(ephemeral(m.view(ch, e.User().ID)))
}

func ephemeral(c discord.ContainerComponent) discord.MessageCreate {
	return discord.MessageCreate{Flags: discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		Components: []discord.LayoutComponent{c}, AllowedMentions: core.NoPings()}
}

func inPlace(c discord.ContainerComponent) discord.MessageUpdate {
	u := discord.NewMessageUpdateV2(c)
	u.Attachments = &[]discord.AttachmentUpdate{}
	u.AllowedMentions = core.NoPings()
	return u
}

// view is a counting channel and where user stands in it.
func (m *Module) view(ch uint64, user snowflake.ID) discord.ContainerComponent {
	s, _ := m.en.Channel(ch)
	mem := m.en.Member(count.Who{Guild: s.Guild, User: uint64(user)})
	id := strconv.FormatUint(ch, 10)
	left := fmt.Sprintf("%d saves in the bank", s.Saves)
	switch s.Rules.Fail {
	case count.Lives:
		left = plural(s.Lives, "life") + " left this run"
	case count.Hardcore:
		left = "no saves in hardcore"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**<#%s>** · %s rules · flock level %d\n", id, s.Rules.Name, count.Flock.Level(s.Total))
	b.WriteString(core.Readout([][2]string{{"next", s.Next()}, {"run", strconv.FormatInt(s.N-s.Start, 10)}, {"best", strconv.FormatInt(s.Best, 10)}}))
	b.WriteString("-# " + left + "\n### you\n")
	lvl := count.Ranks.Level(mem.XP)
	fmt.Fprintf(&b, "%s, rank %d\n%s\n", count.Title(lvl), lvl, bar(mem.XP, lvl))
	b.WriteString(core.Readout([][2]string{{"counts", strconv.FormatInt(mem.Counts, 10)}, {"misses", strconv.FormatInt(mem.Misses, 10)}, {"saves", fmt.Sprintf("%d of 2", mem.Saves)}}))
	row := discord.NewActionRow(
		discord.NewPrimaryButton("board", "games:board:"+id+":month").WithEmoji(emojiOf("cnt_record")),
		discord.NewSecondaryButton("proof", "games:proof:"+id).WithEmoji(emojiOf("cnt_ok")),
	)
	return brand.Card(brand.ColorInfo, Name, "count", strings.TrimRight(b.String(), "\n"), row)
}

// emojiOf is a button's app emoji, none until the sync has it.
func emojiOf(key string) discord.ComponentEmoji {
	if e := brand.ComponentEmoji(key); e != nil {
		return *e
	}
	return discord.ComponentEmoji{}
}

// bar is a rank's progress, ten cells and the xp in it.
func bar(xp int64, lvl int) string {
	from, to := count.Ranks.Need(lvl), count.Ranks.Need(lvl+1)
	filled := int((xp - from) * 10 / max(to-from, 1))
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled) + fmt.Sprintf(" %d of %d xp", xp-from, to-from)
}

// open is a press on a /count card: anyone's, never an admin's alone.
func (m *Module) open(ctx context.Context, e *events.ComponentInteractionCreate, parts []string) error {
	ch, err := strconv.ParseUint(parts[2], 10, 64)
	guild := e.GuildID()
	if err != nil || guild == nil {
		return errNotCounting
	}
	if s, ok := m.en.Channel(ch); !ok || s.Guild != uint64(*guild) {
		return errNotCounting
	}
	user := e.User().ID
	switch {
	case parts[1] == "view":
		return e.UpdateMessage(inPlace(m.view(ch, user)))
	case parts[1] == "board" && len(parts) == 4:
		c, err := m.board(ctx, ch, user, parts[3] == "all")
		if err != nil {
			return err
		}
		return e.UpdateMessage(inPlace(c))
	case parts[1] == "proof":
		c, err := m.proof(ctx, ch)
		if err != nil {
			return err
		}
		return e.UpdateMessage(inPlace(c))
	case parts[1] == "file":
		msg, err := m.proofFile(ctx, ch)
		if err != nil {
			return err
		}
		return e.CreateMessage(msg)
	}
	return errNotCounting
}

// board is the server's counters, this month or all time, and the
// server's place among every server by best run.
func (m *Module) board(ctx context.Context, ch uint64, user snowflake.ID, all bool) (discord.ContainerComponent, error) {
	s, _ := m.en.Channel(ch)
	_, _, period := game.Month(m.now())
	scores := map[uint64]int64{}
	if all || m.db == nil {
		for u, mem := range m.en.Members(s.Guild) {
			scores[u] = mem.Counts
		}
	} else {
		// What was just counted is written first, so the board is now.
		if err := m.flush(ctx); err != nil {
			return discord.ContainerComponent{}, fmt.Errorf("games: writing before a board: %w", err)
		}
		rows, _ := m.db.Query(ctx, "select user_id, counts from count_season where guild_id = $1 and period = $2", int64(s.Guild), period)
		var u, n int64
		if _, err := pgx.ForEachRow(rows, []any{&u, &n}, func() error {
			scores[uint64(u)] = n
			return nil
		}); err != nil {
			return discord.ContainerComponent{}, fmt.Errorf("games: reading the month's board: %w", err)
		}
	}
	what := "this month"
	if all || m.db == nil {
		what = "all time"
	}
	var b strings.Builder
	b.WriteString("### " + what + "\n")
	board := game.Rank(scores)
	if len(board) == 0 {
		b.WriteString("nobody has counted yet\n")
	}
	for _, st := range game.Top(board, uint64(user), boardRows) {
		lvl := count.Ranks.Level(m.en.Member(count.Who{Guild: s.Guild, User: st.Key}).XP)
		mark := ""
		if st.Key == uint64(user) {
			mark = " ◂"
		}
		fmt.Fprintf(&b, "**%d** <@%d> · %d · %s%s\n", st.Place, st.Key, st.Score, count.Title(lvl), mark)
	}
	place, of := m.standing(s.Guild)
	if of > 1 && place > 0 {
		fmt.Fprintf(&b, "-# this server's best run, %d, is number %d of %d servers counting with skua\n", m.best(s.Guild), place, of)
	}
	id := strconv.FormatUint(ch, 10)
	flip := discord.NewSecondaryButton("all time", "games:board:"+id+":all")
	if all || m.db == nil {
		flip = discord.NewSecondaryButton("this month", "games:board:"+id+":month")
	}
	row := discord.NewActionRow(discord.NewSecondaryButton("◂ count", "games:view:"+id), flip)
	if m.db == nil {
		row = discord.NewActionRow(discord.NewSecondaryButton("◂ count", "games:view:"+id))
	}
	return brand.Card(brand.ColorInfo, Name, "board", strings.TrimRight(b.String(), "\n"), row), nil
}

// best is a server's best run in any of its counting channels.
func (m *Module) best(guild uint64) int64 {
	var b int64
	for _, ch := range m.counting(snowflake.ID(guild)) {
		if s, ok := m.en.Channel(ch); ok {
			b = max(b, s.Best)
		}
	}
	return b
}

// standing is a server's place among every server counting with skua, by
// best run, and how many there are. Other servers are never named.
func (m *Module) standing(guild uint64) (place, of int) {
	bests := map[uint64]int64{}
	for _, ch := range m.en.Channels() {
		if s, ok := m.en.Channel(ch); ok {
			bests[s.Guild] = max(bests[s.Guild], s.Best)
		}
	}
	for _, st := range game.Rank(bests) {
		if st.Key == guild {
			place = st.Place
		}
	}
	return place, len(bests)
}

// runLog is a run's entries from the log, in order, up to most from its
// end.
func (m *Module) runLog(ctx context.Context, ch uint64, run int64, most int) ([]count.Entry, error) {
	rows, _ := m.db.Query(ctx, `select n, user_id, message_id from (
		select n, user_id, message_id from count_log where channel_id = $1 and run = $2 order by n desc limit $3) t order by n`,
		int64(ch), run, most)
	var out []count.Entry
	var n, u, msg int64
	_, err := pgx.ForEachRow(rows, []any{&n, &u, &msg}, func() error {
		out = append(out, count.Entry{N: n, User: uint64(u), Msg: uint64(msg)})
		return nil
	})
	return out, err
}

// proof checks the current run against its log, link by link from its
// genesis, and the last run that ended against the head it ended on.
func (m *Module) proof(ctx context.Context, ch uint64) (discord.ContainerComponent, error) {
	if m.db == nil {
		return discord.ContainerComponent{}, errNoLog
	}
	// What was just counted is written first, so the log is the run.
	if err := m.flush(ctx); err != nil {
		return discord.ContainerComponent{}, fmt.Errorf("games: writing before a proof: %w", err)
	}
	s, _ := m.en.Channel(ch)
	entries, err := m.runLog(ctx, ch, s.Run, int(s.N-s.Start)+1)
	if err != nil {
		return discord.ContainerComponent{}, fmt.Errorf("games: reading a run's log: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### run %d\n", s.Run)
	b.WriteString(verdict(count.Verify(s.Guild, ch, s.Run, s.Start, entries, s.Head), s.N-s.Start))
	fmt.Fprintf(&b, "```\n%s\n```\n", s.Head)

	rows, _ := m.db.Query(ctx, "select run, start, length, head from count_run where channel_id = $1 order by run desc limit 1", int64(ch))
	type ended struct {
		Run, Start, Length int64
		Head               string
	}
	last, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ended])
	if err != nil {
		return discord.ContainerComponent{}, fmt.Errorf("games: reading the last run: %w", err)
	}
	if len(last) == 1 {
		r := last[0]
		h, _ := game.ParseHash(r.Head)
		prev, err := m.runLog(ctx, ch, r.Run, int(r.Length)+1)
		if err != nil {
			return discord.ContainerComponent{}, fmt.Errorf("games: reading a run's log: %w", err)
		}
		fmt.Fprintf(&b, "### run %d, ended\n", r.Run)
		b.WriteString(verdict(count.Verify(s.Guild, ch, r.Run, r.Start, prev, h), r.Length))
		fmt.Fprintf(&b, "```\n%s\n```\n", r.Head)
	}
	b.WriteString("-# each count is sha256 over the one before, the run, its number, who counted and the message. the file has every link to check it yourself")
	id := strconv.FormatUint(ch, 10)
	row := discord.NewActionRow(discord.NewSecondaryButton("◂ count", "games:view:"+id), discord.NewSecondaryButton("the file", "games:file:"+id))
	return brand.Card(brand.ColorInfo, Name, "proof", b.String(), row), nil
}

func verdict(ok bool, length int64) string {
	if ok {
		return fmt.Sprintf("✓ all %d counts check out against the head\n", length)
	}
	return fmt.Sprintf("✗ the log doesn't reach this head; %d counts in the run\n", length)
}

// proofFile is the current run as a text file anyone can check: the
// genesis's fields, then every count, then the head.
func (m *Module) proofFile(ctx context.Context, ch uint64) (discord.MessageCreate, error) {
	if m.db == nil {
		return discord.MessageCreate{}, errNoLog
	}
	if err := m.flush(ctx); err != nil {
		return discord.MessageCreate{}, fmt.Errorf("games: writing before a proof: %w", err)
	}
	s, _ := m.en.Channel(ch)
	entries, err := m.runLog(ctx, ch, s.Run, proofMost)
	if err != nil {
		return discord.MessageCreate{}, fmt.Errorf("games: reading a run's log: %w", err)
	}
	var f bytes.Buffer
	fmt.Fprintf(&f, "# skua count run, checked as sha256 links. each field is 8 bytes big endian, a string after its length.\n")
	fmt.Fprintf(&f, "# genesis = link(32 zero bytes, %q, guild, channel, run, start)\n", "skua/count/v1")
	fmt.Fprintf(&f, "# each count = link(previous, run, n, user, message)\n")
	fmt.Fprintf(&f, "guild %d\nchannel %d\nrun %d\nstart %d\ngenesis %s\n", s.Guild, ch, s.Run, s.Start, count.Genesis(s.Guild, ch, s.Run, s.Start))
	if int64(len(entries)) < s.N-s.Start {
		fmt.Fprintf(&f, "# the run is longer than the file: this is its last %d counts\n", len(entries))
	}
	for _, e := range entries {
		fmt.Fprintf(&f, "%d %d %d\n", e.N, e.User, e.Msg)
	}
	fmt.Fprintf(&f, "head %s\n", s.Head)
	name := fmt.Sprintf("count-%d-run-%d.txt", ch, s.Run)
	c := brand.Card(brand.ColorInfo, Name, "proof", fmt.Sprintf("run %d, %d counts", s.Run, s.N-s.Start), discord.NewFileComponent("attachment://"+name))
	msg := ephemeral(c)
	msg.Files = []*discord.File{discord.NewFile(name, "", &f)}
	return msg, nil
}

// recount is /recount: a counting channel starts from the number given,
// the run it was on recorded as ended there.
func (m *Module) recount(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return errNotCounting
	}
	ch, err := m.where(*guild, e.Channel().ID(), named(e))
	if err != nil {
		return err
	}
	number := e.SlashCommandInteractionData().Int("number")
	m.mu.Lock()
	before, _ := m.en.Channel(ch)
	s, _ := m.en.Set(ch, int64(number))
	if before.N > before.Start {
		m.journal.runs = append(m.journal.runs, runRow{ch: ch, run: before.Run, guild: before.Guild, start: before.Start,
			length: before.N - before.Start, head: before.Head.String(), at: m.now()})
	}
	m.mu.Unlock()
	if err := m.saveChannel(ctx, ch); err != nil {
		return err
	}
	return e.CreateMessage(discord.MessageCreate{Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings(),
		Content: fmt.Sprintf("✓ <#%d> counts on from **%s**: the next number is **%s**", ch, count.Say(s.N*s.Rules.Step, s.Rules), s.Next())})
}
