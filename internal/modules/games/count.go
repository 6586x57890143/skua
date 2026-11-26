package games

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/game/count"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/sanction"
)

const (
	// refuseFor is how long a channel that answered 403 to a reaction is
	// left alone, so a channel she can't react in isn't asked again on
	// every count.
	refuseFor = time.Hour
	// callBy bounds one call to Discord.
	callBy = 10 * time.Second
	// catchPages is how many pages of a hundred she reads back per channel
	// after being away.
	catchPages = 5
	// baseTimeout is the first timeout for breaking a run.
	baseTimeout = 10 * time.Minute
)

// ladder is a breaker's timeout: the base, doubled for each break of theirs
// in the last week, up to a day.
func ladder(base time.Duration) sanction.Ladder {
	return sanction.Ladder{Base: base, Doublings: 7, Max: 24 * time.Hour, Window: 7 * 24 * time.Hour}
}

// OnEvent plays a message in a counting channel, calls out a count deleted
// or edited away, and reads back what she missed when a server comes up.
func (m *Module) OnEvent(ev bot.Event) {
	switch e := ev.(type) {
	case *events.GuildMessageCreate:
		m.message(e.Client().Rest, e.GuildID, e.Message)
	case *events.GuildMessageUpdate:
		m.edited(e.Client().Rest, e.GuildID, e.Message)
	case *events.GuildMessageDelete:
		m.deleted(e.Client().Rest, e.GuildID, e.ChannelID, e.MessageID)
	case *events.GuildReady:
		m.catchUp(e.Client().Rest, e.GuildID)
	}
}

// playable is a message a member typed: not a bot's, a webhook's or
// Discord's own.
func playable(msg discord.Message) bool {
	return !msg.Author.Bot && !msg.Author.System && msg.WebhookID == nil &&
		(msg.Type == discord.MessageTypeDefault || msg.Type == discord.MessageTypeReply)
}

// message is a member's message, played at once or, while skua reads back
// what she missed in its channel, after that.
func (m *Module) message(r rest.Rest, guild snowflake.ID, msg discord.Message) {
	if !playable(msg) {
		return
	}
	if v, ok := m.catching.Load(msg.ChannelID); ok {
		b := v.(*backlog)
		b.mu.Lock()
		if !b.done {
			b.msgs = append(b.msgs, msg)
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()
	}
	m.play(r, guild, msg, false)
}

// play is msg's move, answered: reacted to and, when the channel should
// see it, said.
func (m *Module) play(r rest.Rest, guild snowflake.ID, msg discord.Message, forgive bool) count.Verdict {
	ch := uint64(msg.ChannelID)
	move := count.Move{User: uint64(msg.Author.ID), Msg: uint64(msg.ID), Text: msg.Content, At: msg.ID.Time(), Forgive: forgive}
	var v count.Verdict
	if _, lost := m.lost.Load(msg.ChannelID); lost && !forgive {
		// She lost track while away: the next count is taken as right.
		if v = m.en.Anchor(ch, move); v.Kind == count.Accept {
			m.lost.Delete(msg.ChannelID)
		}
	} else {
		v = m.en.Submit(ch, move)
	}
	if v.Kind == count.Ignore || v.Kind == count.Stale {
		return v
	}
	m.record(guild, msg, v)
	if !forgive {
		m.answer(r, guild, msg, v)
	}
	return v
}

// record marks what changed for the next write and keeps what the write
// adds to the history.
func (m *Module) record(guild snowflake.ID, msg discord.Message, v count.Verdict) {
	ch, user := uint64(msg.ChannelID), uint64(msg.Author.ID)
	m.chans.Mark(ch)
	m.members.Mark(count.Who{Guild: uint64(guild), User: user})
	switch v.Kind {
	case count.Accept:
		_, _, period := monthOf(msg.ID.Time())
		m.mu.Lock()
		m.journal.logs = append(m.journal.logs, logRow{ch: ch, run: v.Run, n: v.N, user: user, msg: uint64(msg.ID)})
		m.journal.add(uint64(guild), period, user)
		m.mu.Unlock()
	case count.Broken:
		m.mu.Lock()
		m.journal.runs = append(m.journal.runs, runRow{ch: ch, run: v.Run, guild: uint64(guild), start: v.Start, length: v.Length, head: v.Hash.String(), by: user, at: msg.ID.Time()})
		m.mu.Unlock()
	}
}

// A reaction, in the order they go on: what the move did first, then what
// it earned. A plain one is a count's tick or flame, which a newer count's
// may replace while it waits.
type reaction struct {
	key, fallback string
	plain         bool
}

var (
	reactOK     = reaction{"cnt_ok", "✅", true}
	reactHot    = []reaction{reactOK, {"cnt_hot1", "🔥", true}, {"cnt_hot2", "🔥", true}, {"cnt_hot3", "🔥", true}}
	reactLate   = reaction{"cnt_late", "⌛", true}
	reactSave   = reaction{key: "cnt_save", fallback: "🪶"}
	reactLife   = reaction{key: "cnt_life", fallback: "💔"}
	reactFail   = reaction{key: "cnt_fail", fallback: "❌"}
	reactMile   = reaction{key: "cnt_mile", fallback: "🚩"}
	reactRecord = reaction{key: "cnt_record", fallback: "👑"}
	reactBounty = reaction{key: "cnt_bounty", fallback: "🪙"}
	reactBoss   = reaction{key: "cnt_boss", fallback: "⚔️"}
	reactLevel  = reaction{key: "cnt_level", fallback: "⬆️"}
)

// reactions is what a verdict wears.
func reactions(v count.Verdict) []reaction {
	switch v.Kind {
	case count.Accept:
		out := []reaction{reactHot[min(max(v.Combo, 0), 3)]}
		for _, extra := range []struct {
			on bool
			r  reaction
		}{{v.Milestone, reactMile}, {v.Record, reactRecord}, {v.Bounty, reactBounty}, {v.Boss, reactBoss}, {v.Flock > 0 || v.Rank > 0, reactLevel}} {
			if extra.on {
				out = append(out, extra.r)
			}
		}
		return out
	case count.Sniped, count.Early:
		return []reaction{reactLate}
	case count.Saved:
		return []reaction{reactSave}
	case count.Lost:
		return []reaction{reactLife}
	case count.Broken:
		return []reaction{reactFail}
	}
	return nil
}

// answer reacts to a move and posts what the channel should read: a break,
// a save, a lost life, every thousandth count and a flock level.
func (m *Module) answer(r rest.Rest, guild snowflake.ID, msg discord.Message, v count.Verdict) {
	m.react(r, guild, msg.ChannelID, msg.ID, reactions(v)...)
	ch := uint64(msg.ChannelID)
	s, _ := m.en.Channel(ch)
	who := "<@" + msg.Author.ID.String() + ">"
	switch v.Kind {
	case count.Broken:
		m.wg.Go(func() { m.broken(r, guild, msg, v, s) })
	case count.Saved:
		whose := "their own save"
		if v.Saver == count.ByChannel {
			whose = "a save from the channel"
		}
		m.say(r, guild, msg.ChannelID, fmt.Sprintf("-# %s missed and %s caught it, %s left. the next number is **%s**", who, whose, plural(v.Left, "save"), v.Expected), nil)
	case count.Lost:
		m.say(r, guild, msg.ChannelID, fmt.Sprintf("-# %s missed and the run lost a life, %s left. the next number is **%s**", who, plural(v.Left, "life"), v.Expected), nil)
	case count.Accept:
		if v.N%1000 == 0 {
			m.say(r, guild, msg.ChannelID, "", m.card(brand.ColorOK, "count", fmt.Sprintf("✓ **%d** and still going. %s made it", v.N, who)))
		}
		if v.Flock > 0 {
			m.say(r, guild, msg.ChannelID, "", m.card(brand.ColorOK, "count", fmt.Sprintf("✓ this channel's flock reached level **%d**, %d counts in all", v.Flock, s.Total)))
		}
	}
}

// broken posts the card for a run that ended and times out whoever ended
// it when the channel's rules say to.
func (m *Module) broken(r rest.Rest, guild snowflake.ID, msg discord.Message, v count.Verdict, s count.State) {
	who := "<@" + msg.Author.ID.String() + ">"
	var line string
	switch v.Miss {
	case count.Twice:
		line = who + " counted twice in a row"
	case count.Boss:
		line = who + " counted twice in the boss stretch, which takes ten different members"
	default:
		line = fmt.Sprintf("%s said %s, but it was **%s**", who, firstWord(msg.Content), v.Expected)
	}
	rows := [][2]string{{"run", fmt.Sprintf("%d", v.Length)}, {"best", fmt.Sprintf("%d", v.Best)}}
	body := "✗ " + line + "\n" + core.Readout(rows)
	if v.NewBest {
		body += "that was a new best\n"
	}
	if v.Timeout > 0 {
		switch d, err := m.timeout(r, guild, msg, v.Timeout); {
		case err == nil && d > 0:
			body += fmt.Sprintf("%s sits out %s\n", who, core.Duration(d))
		case forbidden(err):
			body += "-# she couldn't time them out: that takes moderate members and a role above theirs\n"
		}
	}
	body += fmt.Sprintf("-# run %d ended on %s. start again at **%s**", v.Run, v.Hash.Short(), s.Next())
	m.say(r, guild, msg.ChannelID, "", m.card(brand.ColorError, "count", body))
}

// timeout sits the breaker out on the ladder: the channel's base, doubled
// for each run of theirs broken in the last week. Discord refuses to time
// out the owner and anyone above skua, and then nothing happens.
//
// ponytail: the week's breaks are kept in memory, so a restart forgets
// them; a column on count_member is the upgrade if that ever matters.
//
// Only a timeout served counts toward the next.
func (m *Module) timeout(r rest.Rest, guild snowflake.ID, msg discord.Message, base time.Duration) (time.Duration, error) {
	who := count.Who{Guild: uint64(guild), User: uint64(msg.Author.ID)}
	now := m.now()
	l := ladder(base)
	m.bmu.Lock()
	served := 0
	for _, t := range m.breaks[who] {
		if now.Sub(t) < l.Window {
			served++
		}
	}
	m.bmu.Unlock()
	d := l.For(served)
	if m.guard.Allow(guild, guard.CountTimeout) != nil {
		return 0, nil
	}
	until := now.Add(d)
	ctx, cancel := context.WithTimeout(context.Background(), callBy)
	defer cancel()
	_, err := r.UpdateMember(guild, msg.Author.ID, discord.MemberUpdate{CommunicationDisabledUntil: omit.New(&until)},
		rest.WithCtx(ctx), rest.WithReason("games: broke the count"))
	m.guard.Done(guild, err)
	if err != nil {
		m.log.Info("games: couldn't time out a breaker", "guild", guild, "user", msg.Author.ID, "err", err)
		return 0, err
	}
	m.bmu.Lock()
	defer m.bmu.Unlock()
	if m.breaks == nil {
		m.breaks = map[count.Who][]time.Time{}
	}
	// Forget whoever has served nothing in the window, so the map holds
	// only the week's breakers.
	for w, ts := range m.breaks {
		ts = slices.DeleteFunc(ts, func(t time.Time) bool { return now.Sub(t) >= l.Window })
		if len(ts) == 0 {
			delete(m.breaks, w)
		} else {
			m.breaks[w] = ts
		}
	}
	m.breaks[who] = append(m.breaks[who], now)
	return d, nil
}

// forbidden is Discord saying skua lacks the permission or the reach for
// a call: missing access, missing permissions, or any other 403 but a
// member blocking her.
func forbidden(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	return ok && re.Response != nil && re.Response.StatusCode == http.StatusForbidden && re.Code != reactionBlocked
}

// reactionBlocked is the 403 Discord gives for a reaction on the message
// of a member who blocked skua: one member's choice, not the channel's.
const reactionBlocked = 90001

// edited calls out the last count edited into something else, once.
func (m *Module) edited(r rest.Rest, guild snowflake.ID, msg discord.Message) {
	s, ok := m.en.Channel(uint64(msg.ChannelID))
	if !ok || s.CountMsg != uint64(msg.ID) || s.Says(msg.Content) {
		return
	}
	m.callOut(r, guild, msg.ChannelID, msg.ID, s, "edited")
}

// deleted calls out the last count deleted.
func (m *Module) deleted(r rest.Rest, guild, channel, id snowflake.ID) {
	s, ok := m.en.Channel(uint64(channel))
	if !ok || s.CountMsg != uint64(id) {
		return
	}
	m.callOut(r, guild, channel, id, s, "deleted")
}

// callOut says what the last count was after it went, so nobody is fooled
// into a miss.
func (m *Module) callOut(r rest.Rest, guild, channel, id snowflake.ID, s count.State, how string) {
	// One per count: only the last count is ever called out, so a channel
	// keeps one id.
	if prev, seen := m.noticed.Swap(channel, id); seen && prev.(snowflake.ID) == id {
		return
	}
	last := count.Say(s.N*s.Rules.Step, s.Rules)
	m.say(r, guild, channel, fmt.Sprintf("-# <@%d> %s their **%s**. the next number is **%s**", s.Last, how, last, s.Next()), nil)
}

// say posts text or a card in channel, pinging nobody, off the caller's
// goroutine: the gateway never waits on a post.
func (m *Module) say(r rest.Rest, guild, channel snowflake.ID, text string, card *discord.ContainerComponent) {
	m.wg.Go(func() { m.post(r, guild, channel, text, card) })
}

func (m *Module) post(r rest.Rest, guild, channel snowflake.ID, text string, card *discord.ContainerComponent) {
	if m.guard.Allow(guild, guard.MessageSend) != nil {
		return
	}
	msg := discord.MessageCreate{Content: text, AllowedMentions: core.NoPings()}
	if card != nil {
		msg = discord.MessageCreate{Flags: discord.MessageFlagIsComponentsV2, Components: []discord.LayoutComponent{*card}, AllowedMentions: core.NoPings()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), callBy)
	defer cancel()
	_, err := r.CreateMessage(channel, msg, rest.WithCtx(ctx))
	m.guard.Done(guild, err)
	if err != nil {
		m.log.Info("games: couldn't post", "guild", guild, "channel", channel, "err", err)
	}
}

func (m *Module) card(color int, what, body string) *discord.ContainerComponent {
	c := brand.Card(color, Name, what, body)
	return &c
}

// reactor sends one channel's reactions one at a time, in order. A plain
// reaction still waiting is dropped for a newer one, so a fast channel's
// ticks keep up with its counts rather than trailing them.
type reactor struct {
	mu    sync.Mutex
	busy  bool
	queue []job
}

type job struct {
	msg snowflake.ID
	reaction
}

// react queues reactions on msg and starts the channel's sender if it is
// idle.
func (m *Module) react(r rest.Rest, guild, channel, msg snowflake.ID, rs ...reaction) {
	if len(rs) == 0 {
		return
	}
	v, _ := m.reactors.LoadOrStore(channel, &reactor{})
	re := v.(*reactor)
	re.mu.Lock()
	for _, x := range rs {
		if x.plain {
			re.queue = slices.DeleteFunc(re.queue, func(j job) bool { return j.plain })
		}
		re.queue = append(re.queue, job{msg, x})
	}
	if re.busy {
		re.mu.Unlock()
		return
	}
	re.busy = true
	re.mu.Unlock()
	m.wg.Go(func() {
		for {
			re.mu.Lock()
			if len(re.queue) == 0 {
				re.busy = false
				re.mu.Unlock()
				return
			}
			j := re.queue[0]
			re.queue = re.queue[1:]
			re.mu.Unlock()
			m.add(r, guild, channel, j)
		}
	})
}

// add puts one reaction on, unless the channel refused the last one.
func (m *Module) add(r rest.Rest, guild, channel snowflake.ID, j job) {
	if v, ok := m.refused.Load(channel); ok && m.now().Before(v.(time.Time)) {
		return
	}
	if m.guard.Allow(guild, guard.Count) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), callBy)
	defer cancel()
	err := r.AddReaction(channel, j.msg, brand.Reaction(j.key, j.fallback), rest.WithCtx(ctx))
	m.guard.Done(guild, err)
	if forbidden(err) {
		m.refused.Store(channel, m.now().Add(refuseFor))
	}
}

// backlog holds a channel's live messages while skua reads back what she
// missed there, so the two are played in order.
type backlog struct {
	mu   sync.Mutex
	done bool
	msgs []discord.Message
}

// catchUp reads back, in each of guild's counting channels, what was sent
// while skua was away, and plays it forgiving: the right counts stand and
// a miss nobody saw answered changes nothing. The last count caught gets a
// tick, so the channel can see she is back with it.
//
// It holds each channel's live messages from the moment it is called, on
// the gateway's goroutine, so none slips in ahead of what she reads back;
// the reading is done off it.
func (m *Module) catchUp(r rest.Rest, guild snowflake.ID) {
	for _, ch := range m.counting(guild) {
		channel := snowflake.ID(ch)
		if s, ok := m.en.Channel(ch); !ok || s.LastMsg == 0 {
			continue
		}
		b := &backlog{}
		if _, busy := m.catching.LoadOrStore(channel, b); busy {
			continue
		}
		m.wg.Go(func() {
			defer m.catching.Delete(channel)
			m.catchChannel(r, guild, channel, b)
		})
	}
}

// catchChannel reads channel back and then plays what b held meanwhile.
func (m *Module) catchChannel(r rest.Rest, guild, channel snowflake.ID, b *backlog) {
	s, _ := m.en.Channel(uint64(channel))

	after := snowflake.ID(s.LastMsg)
	var last discord.Message
	caught, full, failed := 0, false, false
	for page := 0; page < catchPages; page++ {
		ctx, cancel := context.WithTimeout(context.Background(), callBy)
		msgs, err := r.GetMessages(channel, 0, 0, after, 100, rest.WithCtx(ctx))
		cancel()
		if err != nil {
			m.log.Info("games: couldn't read back a counting channel", "guild", guild, "channel", channel, "err", err)
			failed = true
			break
		}
		slices.SortFunc(msgs, func(a, b discord.Message) int { return cmp.Compare(a.ID, b.ID) })
		for _, msg := range msgs {
			if playable(msg) && m.play(r, guild, msg, true).Kind == count.Accept {
				caught++
				last = msg
			}
			after = max(after, msg.ID)
		}
		full = len(msgs) == 100
		if !full {
			break
		}
	}
	// Read back only in part, she can't know the number: the next count
	// anyone makes is taken as right, rather than judging it against where
	// she lost track.
	if full || failed {
		m.lost.Store(channel, struct{}{})
		m.say(r, guild, channel, "-# skua was away longer than she could read back, so she picks up from the next count", nil)
	}
	// Plays only touch the engine; every post and reaction is queued, so
	// holding the backlog here never holds a call to Discord.
	b.mu.Lock()
	for _, msg := range b.msgs {
		m.play(r, guild, msg, false)
	}
	b.done = true
	b.mu.Unlock()

	if caught > 0 {
		m.react(r, guild, channel, last.ID, reactOK)
	}
}

// Wait waits for every reaction, card and read-back in flight, up to d,
// and reports whether they all finished.
func (m *Module) Wait(d time.Duration) bool {
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// firstWord is what a member said, as the engine read it, for a card.
func firstWord(s string) string {
	w, _, _ := strings.Cut(strings.TrimSpace(s), " ")
	w, _, _ = strings.Cut(w, "\n")
	if r := []rune(w); len(r) > 24 {
		w = string(r[:24]) + "..."
	}
	return "**" + markdown.Replace(w) + "**"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if word == "life" {
		return fmt.Sprintf("%d lives", n)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
