package pp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/pixel"
)

// Quests: one is dealt a day to a member whose account is CompanionAge
// old, up to QuestHold held, so a missed day isn't lost. A quest's tier is
// drawn by QuestOdds and its kind evenly from the tier's QuestKinds, both
// from the season's committed seed. A rarer tier asks more, but less than
// it pays more, so drawing one is luck. Tasks count from when the quest was
// dealt. A finished quest pays its tier's QuestPP as labour: the levy takes
// its share, the level counts it, buffs don't touch it.

// What a task counts.
const (
	taskVoice  = "voice"  // minutes earning in voice
	taskTalk   = "talk"   // paying messages
	taskHearth = "hearth" // minutes earning in voice while the hearth is lit
	taskSelfie = "selfie" // a picture in the selfie channel still there a day later
	taskCrowd  = "crowd"  // minutes earning in voice with three or more in the room
	taskReply  = "reply"  // paying messages that answer someone else
	taskStream = "stream" // minutes earning in voice while streaming
	taskCamera = "camera" // minutes earning in voice with the camera on
)

// Task is one thing a quest asks: Goal of Metric.
type Task struct {
	Metric string
	Goal   int64
}

// QuestKind is one quest a tier can deal: its icon and its tasks.
type QuestKind struct {
	Icon  string
	Tasks []Task
}

// selfieWait is how long a selfie has to stay up.
const selfieWait = 24 * time.Hour

// heldQuest is one held quest: its tier and kind, how far each task has come,
// a selfie posted for it, waiting out selfieWait, the day it was last
// rerolled and how many times its selfie came down.
type heldQuest struct {
	tier, kind int
	prog       [3]int64
	ref, refAt int64 // the selfie message and when it was posted (unix)
	rerolled   int64
	struck     int64
}

func (q heldQuest) tasks(b *Balance) []Task { return b.QuestKinds[q.tier][q.kind].Tasks }

// aside is whether q is a selfie that's up, or came down once and waits for
// one more: it doesn't count toward QuestHold, so waiting out its day never
// costs the quest dealt at 05:30, and it can't be rerolled. A selfie not
// yet posted is an ordinary quest.
func (q heldQuest) aside() bool { return q.ref != 0 || q.struck > 0 }

// held is how many of qs take a slot.
func held(qs []heldQuest) int {
	n := 0
	for _, q := range qs {
		if !q.aside() {
			n++
		}
	}
	return n
}

// done is whether every task is met.
func (q heldQuest) done(b *Balance) bool {
	for i, t := range q.tasks(b) {
		if q.prog[i] < t.Goal {
			return false
		}
	}
	return true
}

// encodeQuests is held quests as they're saved: tier, kind, progress, ref,
// refAt, rerolled and struck, comma separated, a quest each between
// semicolons.
func encodeQuests(qs []heldQuest) string {
	parts := make([]string, len(qs))
	for i, q := range qs {
		parts[i] = fmt.Sprintf("%d,%d,%d,%d,%d,%d,%d,%d,%d", q.tier, q.kind, q.prog[0], q.prog[1], q.prog[2], q.ref, q.refAt, q.rerolled, q.struck)
	}
	return strings.Join(parts, ";")
}

// decodeQuests reads saved quests back, those saved before rerolled or
// struck were too, dropping any the balance no longer has a kind for.
func decodeQuests(b *Balance, s string) []heldQuest {
	var out []heldQuest
	for part := range strings.SplitSeq(s, ";") {
		f := strings.Split(part, ",")
		if len(f) < 7 || len(f) > 9 {
			continue
		}
		var n [9]int64
		for i := range f {
			n[i], _ = strconv.ParseInt(f[i], 10, 64)
		}
		q := heldQuest{tier: int(n[0]), kind: int(n[1]), prog: [3]int64{n[2], n[3], n[4]}, ref: n[5], refAt: n[6], rerolled: n[7], struck: n[8]}
		if q.tier >= 0 && q.tier < len(b.QuestKinds) && q.kind >= 0 && q.kind < len(b.QuestKinds[q.tier]) {
			out = append(out, q)
		}
	}
	return out
}

// Rolls a quest draws: deals and rerolls, apart from chat's slots.
const (
	slotDeal   = 1 << 40
	slotReroll = 1 << 41
)

// draw rolls a quest from seed's slot: a tier, then a kind from it, a
// selfie only where there's a channel for one and never not, the quest a
// reroll replaces. The tier's odds are the same either way: every tier has
// another kind to give.
func (b *Balance) draw(seed, guild, user uint64, day int64, slot uint64, selfies bool, not *heldQuest) heldQuest {
	tier, _ := b.QuestRoll(Roll(seed, guild, user, day, slot))
	var kinds []int
	for k, kind := range b.QuestKinds[tier] {
		if (selfies || kind.Tasks[0].Metric != taskSelfie) && (not == nil || not.tier != tier || not.kind != k) {
			kinds = append(kinds, k)
		}
	}
	return heldQuest{tier: tier, kind: kinds[Roll(seed, guild, user, day, slot+1)%uint64(len(kinds))]}
}

// old is whether user's account is old enough to be dealt quests.
func (b *Balance) old(user snowflake.ID, now time.Time) bool {
	return now.Sub(user.Time()) >= b.CompanionAge
}

// deal gives m a quest for each day since the last, up to QuestHold in
// the slots. s.mu held.
func (st *state) deal(s *server, m *member, guild, user snowflake.ID, today int64) {
	if m.questDay >= today || !st.bal().old(user, dayStart(today)) {
		return
	}
	days := today - m.questDay
	if m.questDay == 0 {
		days = 1
	}
	for k := range min(days, int64(st.bal().QuestHold-held(m.quests))) {
		q := st.bal().draw(st.seedOf(today), uint64(guild), uint64(user), today, slotDeal+uint64(k)*2, s.selfies != 0, nil)
		m.quests = append(m.quests, q)
		s.count(today, statDealt, q.tier, q.kind)
	}
	m.questDay, m.dirty = today, true
}

// progress counts one of metric toward m's quests and pays any it
// finishes. s.mu held.
func (st *state) progress(s *server, m *member, user snowflake.ID, metric string) {
	kept := m.quests[:0]
	for _, q := range m.quests {
		for i, t := range q.tasks(st.bal()) {
			if t.Metric == metric && q.prog[i] < t.Goal {
				q.prog[i]++
				m.dirty = true
			}
		}
		if q.done(st.bal()) {
			st.payQuest(s, m, user, q)
			continue
		}
		kept = append(kept, q)
	}
	m.quests = kept
}

// growFund mints the contest fund's share of a quest that paid got into
// the treasury, until the fund holds FundHold of the biggest prize. The
// first quest seeds it full instead, once. s.mu held.
func (st *state) growFund(s *server, day, got int64) {
	hold := Rules.FundHold * Rules.Prize(st.bal())
	add, reason := got*Rules.FundShare/10000, reasonFund
	if !s.seeded {
		add, reason, s.seeded = hold, reasonSeed, true
	}
	add = min(add, hold-s.fund)
	if add <= 0 {
		return
	}
	s.fund += add
	s.treasury += add
	s.seasonMint += add
	s.append(0, day, reason, add, nil)
	s.dirty = true
}

// payQuest pays q as labour, its own ledger row. s.mu held.
func (st *state) payQuest(s *server, m *member, user snowflake.ID, q heldQuest) {
	got := st.bal().QuestPP[q.tier]
	st.growFund(s, m.dayN, got)
	lv, kept := st.labour(s, m, got)
	m.questsDone++
	s.count(m.dayN, statFinished, q.tier, q.kind)
	s.append(user, m.dayN, reasonQuest, got-lv-kept, []int64{int64(q.tier), int64(q.kind), got, lv, kept})
}

// labour pays m got outside the day's pool, as a quest pays: the levy
// takes its share, the heavy purse line keeps back what it does, and the
// level counts it all. It returns the levy and what was kept back; the
// caller writes the ledger row. s.mu held.
func (st *state) labour(s *server, m *member, got int64) (lv, kept int64) {
	if most := paysMost * st.pot(); got > most {
		st.warn("pp: a payout past its ceiling, cut to it", "got", got, "most", most)
		got = most
	}
	if s.levying() {
		lv = Rules.levy(got)
	}
	s.levyFull += Rules.levy(got)
	s.treasury += lv
	s.levyOpen += lv
	net := got - lv
	credit := net
	if s.heavy && m.purse >= Rules.Line*st.pot() {
		credit = net * Rules.Keep / 100
	}
	m.purse += credit
	m.total += got
	m.seasonPP += got
	s.seasonMint += got
	m.dirty, s.dirty = true, true
	return lv, net - credit
}

// reroll swaps m's quest i for a fresh draw, each quest once a day.
// s.mu held.
func (st *state) reroll(s *server, m *member, guild, user snowflake.ID, i int, today int64) error {
	switch {
	case i < 0 || i >= len(m.quests):
		return core.Tell("that quest isn't held any more; open your quests again")
	case m.quests[i].aside():
		return core.Tell("a selfie that's up can't be rerolled; it pays once it has stayed a day")
	case m.quests[i].rerolled == today:
		return core.Tell("that quest has had today's reroll; another comes at 05:30 utc")
	}
	s.count(today, statRerolled, m.quests[i].tier, m.quests[i].kind)
	// Each place has its own slot, so two rerolls in a day draw apart.
	m.quests[i] = st.bal().draw(st.seedOf(today), uint64(guild), uint64(user), today, slotReroll+uint64(i)*2, s.selfies != 0, &m.quests[i])
	m.quests[i].rerolled, m.dirty = today, true
	return nil
}

// selfie keeps a picture posted in the selfie channel against a selfie
// quest waiting for one, first one whose picture came down. s.mu held.
func (st *state) selfie(s *server, m *member, msg discord.Message, now time.Time) {
	if s.selfies == 0 || msg.ChannelID != s.selfies {
		return
	}
	picture := false
	for _, a := range msg.Attachments {
		if a.ContentType != nil && strings.HasPrefix(*a.ContentType, "image/") {
			picture = true
		}
	}
	if !picture {
		return
	}
	pick := -1
	for i, q := range m.quests {
		if q.ref == 0 && q.tasks(st.bal())[0].Metric == taskSelfie && (pick < 0 || q.struck > m.quests[pick].struck) {
			pick = i
		}
	}
	if pick >= 0 {
		m.quests[pick].ref, m.quests[pick].refAt, m.dirty = int64(msg.ID), now.Unix(), true
	}
}

// strike counts quest i's selfie coming down: the first time the quest
// waits for one more picture, the second voids it, so reposting can't
// keep a quest going.
func (m *member) strike(i int) {
	m.dirty = true
	if m.quests[i].struck > 0 {
		m.quests = slices.Delete(m.quests, i, i+1)
		return
	}
	m.quests[i].ref, m.quests[i].refAt, m.quests[i].struck = 0, 0, 1
}

// unpost strikes the selfie quest a deleted message was up for. s.mu held.
func (s *server) unpost(msg snowflake.ID) {
	for _, mem := range s.members {
		for i, q := range mem.quests {
			if q.ref == int64(msg) {
				mem.strike(i)
				return
			}
		}
	}
}

// pendingSelfie is a selfie that has waited long enough to be checked.
type pendingSelfie struct {
	guild, user, channel, msg snowflake.ID
}

// checkSelfies looks at every selfie past selfieWait: one still there
// finishes its task, one gone is struck, as a delete seen live is.
func (m *Module) checkSelfies(ctx context.Context) {
	if m.rest == nil {
		return
	}
	now := m.now()
	var due []pendingSelfie
	for g, s := range m.st.servers() {
		s.mu.Lock()
		for u, mem := range s.members {
			for _, q := range mem.quests {
				if q.ref != 0 && now.Unix()-q.refAt >= int64(selfieWait/time.Second) {
					due = append(due, pendingSelfie{g, u, s.selfies, snowflake.ID(q.ref)})
				}
			}
		}
		s.mu.Unlock()
	}
	for _, p := range due {
		_, err := m.rest.GetMessage(p.channel, p.msg, rest.WithCtx(ctx))
		var re *rest.Error
		gone := errors.As(err, &re) && re.Response != nil && re.Response.StatusCode == http.StatusNotFound
		if err != nil && !gone {
			m.log.Warn("pp: checking a selfie", "guild", p.guild, "err", err)
			continue
		}
		s := m.st.server(p.guild)
		s.mu.Lock()
		mem := s.members[p.user]
		for i, q := range mem.quests {
			if q.ref != int64(p.msg) {
				continue
			}
			if gone {
				mem.strike(i)
			} else {
				mem.quests[i].ref = 0
				m.st.progress(s, mem, p.user, taskSelfie)
			}
			break
		}
		s.mu.Unlock()
	}
}

// taskText is a task as members read it.
func taskText(t Task) string {
	switch t.Metric {
	case taskVoice:
		return fmt.Sprintf("%d minutes in voice with company", t.Goal)
	case taskTalk:
		return fmt.Sprintf("%d messages", t.Goal)
	case taskHearth:
		return fmt.Sprintf("%d minutes in voice while the hearth burns", t.Goal)
	case taskSelfie:
		return "a picture in the selfie channel that's still there a day later"
	case taskCrowd:
		return fmt.Sprintf("%d minutes in voice with three or more in the room", t.Goal)
	case taskReply:
		return fmt.Sprintf("%d replies to other birds", t.Goal)
	case taskStream:
		return fmt.Sprintf("%d minutes streaming in voice", t.Goal)
	case taskCamera:
		return fmt.Sprintf("%d minutes in voice with your camera on", t.Goal)
	}
	return t.Metric
}

// questText is a quest as members read it: its tasks and how far along.
func (q heldQuest) text(b *Balance) string {
	var parts []string
	for i, t := range q.tasks(b) {
		if t.Metric == taskSelfie {
			if q.ref != 0 {
				return "your picture is up; it counts once it has stayed a day"
			}
			return taskText(t)
		}
		parts = append(parts, fmt.Sprintf("%s (%d/%d)", taskText(t), min(q.prog[i], t.Goal), t.Goal))
	}
	return strings.Join(parts, " and ")
}

// questsMessage is user's quests: the screen, every quest as text, and on
// their own copy the reroll. A golden quest held turns the card gold.
func (m *Module) questsMessage(guild snowflake.ID, who *discord.ResolvedMember) (discord.MessageCreate, error) {
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, who.User.ID, today)
	qs := append([]heldQuest(nil), mem.quests...)
	s.mu.Unlock()

	b := m.st.bal()
	// The screen has room for QuestHold. A selfie set aside keeps its
	// place while there's room, and goes under the screen once a quest
	// dealt at 05:30 took it.
	var shown, below []int
	for i, q := range qs {
		if !q.aside() {
			shown = append(shown, i)
		}
	}
	for i, q := range qs {
		if q.aside() && len(shown) < b.QuestHold {
			shown = append(shown, i)
		} else if q.aside() {
			below = append(below, i)
		}
	}
	slices.Sort(shown)
	lines := make([]QuestLine, len(shown))
	var text []string
	color, golden := brand.ColorNotice, false
	ready := 0
	for n, i := range shown {
		q := qs[i]
		if q.rerolled != today && !q.aside() {
			ready++
		}
		lines[n] = QuestLine{Icon: b.QuestKinds[q.tier][q.kind].Icon, Tier: q.tier, Reward: b.QuestPP[q.tier]}
		for j, t := range q.tasks(b) {
			lines[n].Tasks = append(lines[n].Tasks, TaskLine{What: shortTask(t, q.ref != 0), Progress: min(q.prog[j], t.Goal), Goal: t.Goal})
		}
	}
	for _, q := range qs {
		text = append(text, fmt.Sprintf("%s · %s · %s pp", QuestTiers[q.tier], q.text(b), grouped(b.QuestPP[q.tier]/unit)))
		golden = golden || q.tier == 2
	}
	if golden {
		color = brand.ColorWarn
	}
	pic, err := QuestScreen(pixelName(who), lines, ready)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	// Everything is on the screen; the quests as text are its description,
	// for a reader that can't see it.
	alt := "your quests: none held, a new one comes at 05:30 utc"
	if len(text) > 0 {
		alt = "your quests: " + strings.Join(text, ". ")
	}
	extra := []discord.ContainerSubComponent{
		discord.NewMediaGallery(screenItem(pic, clip(alt, altLimit))),
	}
	for _, i := range below {
		q := qs[i]
		line := fmt.Sprintf("-# your selfie is up: it pays %s pp <t:%d:R> if it's still there", grouped(b.QuestPP[q.tier]/unit), q.refAt+int64(selfieWait/time.Second))
		if q.ref == 0 {
			line = "-# your selfie came down: post another to finish it. if that one comes down too, the quest is void"
		}
		extra = append(extra, discord.NewTextDisplay(line))
	}
	if len(shown) > 0 {
		// A reroll button per quest, in the screen's order, each greyed
		// out once it has had today's or its selfie is up.
		row := discord.NewActionRow()
		for n, i := range shown {
			btn := withIcon(discord.NewSecondaryButton(fmt.Sprintf("reroll %d", n+1), "pp:reroll:"+strconv.Itoa(i)), "reroll")
			if qs[i].rerolled == today || qs[i].aside() {
				btn = btn.AsDisabled()
			}
			row = row.AddComponents(btn)
		}
		extra = append(extra, row)
	}
	// Quests done together pay a joinder on top: the way to the notices,
	// under its own ID, since the menu under the screen has notices too.
	extra = append(extra, discord.NewActionRow(withIcon(discord.NewSecondaryButton("do them in company", "pp:company"), "envelope")))
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(color, "pp", "quests", "", extra...)},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// shortTask is a task in a few words, for the screen; up is whether a
// selfie for it is waiting out its day.
func shortTask(t Task, up bool) string {
	switch t.Metric {
	case taskVoice:
		return "minutes in voice"
	case taskTalk:
		return "messages"
	case taskHearth:
		return "minutes by the hearth"
	case taskSelfie:
		if up {
			return "selfie up, a day to stay"
		}
		return "a selfie that stays a day"
	case taskCrowd:
		return "minutes in a crowd"
	case taskReply:
		return "replies"
	case taskStream:
		return "minutes streaming"
	case taskCamera:
		return "minutes on camera"
	}
	return t.Metric
}

// pixelName is who as the screen can draw it.
func pixelName(who *discord.ResolvedMember) string {
	name := who.EffectiveName()
	if pixel.Plain(name) == "" {
		return who.User.Username
	}
	return name
}

// rerollPicked rerolls the quest whose button the member pressed and
// shows their quests again.
func (m *Module) rerollPicked(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	i, err := strconv.Atoi(strings.TrimPrefix(e.Data.CustomID(), "pp:reroll:"))
	if err != nil {
		return core.Tell("that quest isn't held any more; open your quests again")
	}
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	err = m.st.reroll(s, m.st.member(s, guild, e.User().ID, today), guild, e.User().ID, i, today)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	msg, err := m.questsMessage(guild, e.Member())
	if err != nil {
		return err
	}
	return m.answer(guild, e, msg, "pp:quests")
}

// channelControls are the selfie and slack tide channel pickers for /pp's
// help page, under one heading so the page stays inside Discord's count.
func (m *Module) channelControls(guild snowflake.ID) []discord.ContainerSubComponent {
	s := m.st.server(guild)
	s.mu.Lock()
	ch, house := s.selfies, s.houseCh
	s.mu.Unlock()
	pick := discord.ChannelSelectMenuComponent{CustomID: "pp:selfies", Placeholder: "pick the selfie channel", ChannelTypes: logChannels, MinValues: new(0), MaxValues: 1}
	if ch != 0 {
		pick.DefaultValues = []discord.SelectMenuDefaultValue{discord.NewSelectMenuDefaultChannel(ch)}
	}
	tide := discord.ChannelSelectMenuComponent{CustomID: "pp:house", Placeholder: "pick the slack tide's channel", ChannelTypes: logChannels, MinValues: new(0), MaxValues: 1}
	if house != 0 {
		tide.DefaultValues = []discord.SelectMenuDefaultValue{discord.NewSelectMenuDefaultChannel(house)}
	}
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay("### selfies and the slack tide\n-# golden quests that ask for a picture want it in the first, where it stays a day; with none picked they never come up\n-# the purser's tables and the drowning hour's bell go in the second; with none picked the house stays shut"),
		discord.NewActionRow(pick),
		discord.NewActionRow(tide),
	}
}

// setSelfies keeps the selfie channel an admin picked.
func (m *Module) setSelfies(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	data, ok := e.Data.(discord.ChannelSelectMenuInteractionData)
	if !ok {
		return errNotAdmin
	}
	var ch snowflake.ID
	if len(data.Values) > 0 {
		ch = data.Values[0]
	}
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, selfie_channel) values ($1, $2)
		on conflict (guild_id) do update set selfie_channel = excluded.selfie_channel`, int64(guild), int64(ch)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	s.selfies = ch
	s.mu.Unlock()
	said := "no selfie channel now: selfie quests won't come up"
	if ch != 0 {
		said = "selfie quests count pictures in <#" + ch.String() + "> now"
	}
	return e.CreateMessage(discord.MessageCreate{Content: said, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}
