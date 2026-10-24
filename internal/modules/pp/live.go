package pp

import (
	"hash/maphash"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/pixel"
)

// What /pp knows lives here, in memory: everyone's day in every server and
// who sits where in voice. The database is where it is kept, written once a
// minute, never read on a message.
// ponytail: every member ever seen stays in memory, a few hundred bytes
// each; a server of a hundred thousand earners would want to load lazily.

// member is one member's standing in one server.
type member struct {
	total, purse int64
	seasonPP     int64
	season, week int64
	weekAct      [Boards]int64
	dayN         int64
	day          Day
	class        string
	classSeason  int64
	classChanges int64
	// classDay is the day they last picked or changed class.
	classDay int64
	// livery is the key of the livery they lease, "" for none, and
	// liveryUntil the day its lease runs out.
	livery      string
	liveryUntil int64
	voiceMin    int64
	messages    int64
	// dayGot is what the open day has paid into the purse, not yet in the
	// ledger, dayLevy what it paid the treasury, and buffLo and buffHi the
	// lowest and highest buff it paid under.
	dayGot, dayLevy, buffLo, buffHi int64
	// granted is what grants paid them in grantSeason.
	granted, grantSeason int64
	// rung is their prestige rung, and dayWithheld what the open day earned
	// past the heavy purse line and so was never minted.
	rung, dayWithheld int64
	// name is how they were last seen called, for the board.
	name string
	// quests are those held, questDay the day they were last dealt,
	// questsDone how many ever finished.
	quests               []heldQuest
	questDay, questsDone int64

	// Only in memory: when they last earned by chat and last counted as
	// active, a fingerprint of their last message, when they last showed a
	// sign of life, and what they were last seen wearing.
	lastPay, lastCount time.Time
	lastHash           uint64
	life               time.Time
	wear               Wearing
	dirty              bool
}

// seat is one member in a voice channel.
type seat struct {
	channel                                snowflake.ID
	bot, deaf, muted, suppressed, selfMute bool
	video                                  bool
}

// server is one guild's state, behind its own lock.
type server struct {
	mu sync.Mutex
	// leasing is held through a lease or a fade, round its Discord calls,
	// so one member's liveries change one at a time.
	leasing sync.Mutex
	members map[snowflake.ID]*member
	seats   map[snowflake.ID]*seat
	// banners are those wearing the server's invite in their custom
	// status who aren't members yet, so the buff is on from their first
	// earning; presences counts the presence updates seen and presenceAt
	// the last, for the help page.
	banners    map[snowflake.ID]bool
	presences  int64
	presenceAt time.Time
	// The server's day, for the hearth.
	day, raw       int64
	history        []int64
	lit, litBefore int
	dirty          bool
	// beta is the role whose members may play while /pp is in closed
	// beta, beside admins; 0 is none.
	beta snowflake.ID
	// logChannel is where the ledger's head and the seasons' seeds are
	// posted; 0 is nowhere.
	logChannel snowflake.ID
	// The ledger: the server, the last row's number and hash, and the rows
	// not yet written.
	id      snowflake.ID
	seq     int64
	head    []byte
	pending []entry
	// paid are the grants approved since the last flush, which marks them
	// paid in the transaction that writes their ledger rows.
	paid []int64
	// The treasury: what it holds, the levy it took today and hasn't put in
	// the ledger yet, and the levy at the full rate today and on each of
	// the days before (up to a week), which says when it's full.
	treasury, levyOpen, levyFull int64
	levyDays                     []int64
	// fund is the contest fund, the part of treasury only grants spend,
	// and seeded whether it has had its one seed.
	fund   int64
	seeded bool
	// checkpoints are the weekly checkpoints paid and not yet posted.
	checkpoints []checkpointNews
	// The season so far, for the backstop: what it minted and burned, the
	// last season's burn in percent (-1 before one), and whether the heavy
	// purse line is on.
	seasonMint, seasonBurn, sinkLast int64
	heavy                            bool
	// selfies is the channel selfie quests count pictures in, 0 none.
	selfies snowflake.ID
	// anchor is the role liveries sit directly under, and heralds the
	// channel livery petitions are posted in for review; 0 none.
	anchor, heralds snowflake.ID
	// yardOpen is whether the dyers' yard sells to everyone yet, not only
	// admins, and dyeRoles each livery's role here once it's made.
	yardOpen bool
	dyeRoles map[string]snowflake.ID
}

func newServer(id snowflake.ID) *server {
	return &server{id: id, members: map[snowflake.ID]*member{}, seats: map[snowflake.ID]*seat{}, banners: map[snowflake.ID]bool{}, sinkLast: -1}
}

// weekOf is the week day falls in, counted from a Monday (1970-01-05 is
// day 4).
func weekOf(day int64) int64 { return (day + 3) / 7 }

// state is everything /pp holds, by guild. seed draws the rolls of any
// season without a committed seed of its own in seeds.
type state struct {
	cur  atomic.Pointer[era]
	seed uint64
	mu   sync.Mutex
	// flushing keeps flushes one at a time, so an older one can't land last.
	flushing sync.Mutex
	srv      map[snowflake.ID]*server
	seeds    map[int64]*seasonSeed
}

// era is the balance the day runs under and its pot day under the charter,
// worked out once. A deploy's new balance waits for the next day to turn.
type era struct {
	b      *Balance
	potDay int64
}

func newState(b *Balance, seed uint64) *state {
	st := &state{seed: seed, srv: map[snowflake.ID]*server{}, seeds: map[int64]*seasonSeed{}}
	st.use(b)
	return st
}

// use runs st under b from now on.
func (st *state) use(b *Balance) { st.cur.Store(&era{b: b, potDay: b.PotDay(Rules)}) }

// bal is the balance in force.
func (st *state) bal() *Balance { return st.cur.Load().b }

// pot is the pot day in force.
func (st *state) pot() int64 { return st.cur.Load().potDay }

// server is guild's state, made on first sight.
func (st *state) server(guild snowflake.ID) *server {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.srv[guild]
	if !ok {
		s = newServer(guild)
		st.srv[guild] = s
	}
	return s
}

// servers is every guild with state, for the minute's work.
func (st *state) servers() map[snowflake.ID]*server {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make(map[snowflake.ID]*server, len(st.srv))
	for g, s := range st.srv {
		out[g] = s
	}
	return out
}

func (st *state) pools(guild, user snowflake.ID, day int64) Pools {
	return st.bal().Pools(st.seedOf(day), uint64(guild), uint64(user), day)
}

// member is user in s, made on first sight, rolled into today with s.
// s.mu held.
func (st *state) member(s *server, guild, user snowflake.ID, today int64) *member {
	st.rollServer(s, today)
	m, ok := s.members[user]
	if !ok {
		m = &member{class: Classes[0].Key, classSeason: -1, dayN: today}
		m.wear.Banner = s.banners[user]
		delete(s.banners, user)
		s.members[user] = m
	}
	st.roll(s, m, guild, user, today)
	return m
}

// roll moves m into today: the day before goes into the ledger, a new day
// carries rested pp from the last one, and a new week or season starts its
// counts over. s.mu held.
func (st *state) roll(s *server, m *member, guild, user snowflake.ID, today int64) {
	if m.dayN != today {
		st.close(s, user, m)
		m.day = Day{Rested: st.bal().Rested(m.day, st.pools(guild, user, m.dayN), today-m.dayN)}
		m.dayN = today
		m.dirty = true
	}
	if w := weekOf(today); m.week != w {
		m.week, m.weekAct, m.dirty = w, [Boards]int64{}, true
	}
	if season, _ := st.bal().Season(today); m.season != season {
		m.season, m.seasonPP, m.dirty = season, 0, true
	}
	st.deal(s, m, guild, user, today)
}

// rollServer moves s into today: every member's day before goes into the
// ledger, so a day's rows are all in by the time its head is posted,
// yesterday joins the history the hearth measures against, and the tier it
// lit carries a day.
func (st *state) rollServer(s *server, today int64) {
	if s.day == today {
		return
	}
	for u, m := range s.members {
		if m.dayN < today {
			st.close(s, u, m)
		}
	}
	if s.day > 0 && weekOf(s.day) != weekOf(today) {
		st.checkpoint(s, weekOf(s.day), today)
	}
	if s.day > 0 {
		before, _ := st.bal().Season(s.day)
		if now, _ := st.bal().Season(today); before != now {
			st.seasonTurn(s)
		}
	}
	if s.levyOpen != 0 {
		s.append(0, s.day, reasonLevy, s.levyOpen, nil)
		s.levyOpen = 0
	}
	if s.day > 0 {
		s.levyDays = append(s.levyDays, s.levyFull)
		if len(s.levyDays) > 7 {
			s.levyDays = s.levyDays[len(s.levyDays)-7:]
		}
	}
	s.levyFull = 0
	if s.day > 0 && s.raw > 0 {
		s.history = append(s.history, s.raw)
		if len(s.history) > 7 {
			s.history = s.history[len(s.history)-7:]
		}
	}
	s.litBefore = 0
	if s.day == today-1 {
		s.litBefore = s.lit
	}
	s.day, s.raw, s.lit, s.dirty = today, 0, 0, true
}

// levying is whether the treasury takes its levy: until it holds
// TreasuryWeeks of what a week's levy comes to at the full rate beside the
// contest fund, and always while there's no week to go by. s.mu held.
func (s *server) levying() bool {
	var week int64
	for _, l := range s.levyDays {
		week += l
	}
	return week == 0 || s.treasury-s.fund < Rules.TreasuryWeeks*week
}

// hearth is the tier s's members earn under now. s.mu held.
func (s *server) hearth() int { return max(s.lit, s.litBefore) }

// earn pays m raw pp from voice or chat, as the engine says, and counts it
// toward the server's hearth and m's week. s.mu held.
func (st *state) earn(s *server, m *member, guild, user snowflake.ID, chat bool, raw int64) int64 {
	before, act := m.day.Voice+m.day.Chat, m.day.Activity()
	buff := st.bal().Buff(m.wear, s.hearth())
	got := st.bal().Earn(&m.day, st.pools(guild, user, m.dayN), chat, raw, buff)
	if m.day.Minutes+m.day.Paid == 1 {
		m.buffLo, m.buffHi = buff, buff
	}
	m.buffLo, m.buffHi = min(m.buffLo, buff), max(m.buffHi, buff)
	// The treasury's levy, unless it's full; the level counts it all.
	full := Rules.levy(got)
	s.levyFull += full
	lv := int64(0)
	if s.levying() {
		lv = full
	}
	s.treasury += lv
	s.levyOpen += lv
	m.dayLevy += lv
	// Past the heavy purse line, while it's on, a purse keeps a tenth.
	net := got - lv
	credit := net
	if s.heavy && m.purse >= Rules.Line*st.pot() {
		credit = net * Rules.Keep / 100
	}
	m.dayWithheld += net - credit
	m.dayGot += credit
	m.total += got
	m.purse += credit
	m.seasonPP += got
	s.seasonMint += got
	for i, a := range m.day.Activity() {
		m.weekAct[i] += a - act[i]
	}
	switch {
	case chat:
		st.progress(s, m, user, taskTalk)
	case s.hearth() > 0:
		st.progress(s, m, user, taskVoice)
		st.progress(s, m, user, taskHearth)
	default:
		st.progress(s, m, user, taskVoice)
	}
	s.raw += m.day.Voice + m.day.Chat - before
	if tier := st.bal().HearthTier(s.raw, s.history); tier > s.lit {
		s.lit = tier
	}
	m.dirty, s.dirty = true, true
	return got
}

// Wearing, as a member object shows it: the server's tag worn here, and a
// boost.
func crestOf(u discord.User, guild snowflake.ID) bool {
	pg := u.PrimaryGuild
	return pg != nil && pg.IdentityGuildID != nil && *pg.IdentityGuildID == guild &&
		pg.IdentityEnabled != nil && *pg.IdentityEnabled
}

// seen refreshes what m is wearing from a member object that came with an
// event. s.mu held.
func seen(m *member, mem *discord.Member, u discord.User, guild snowflake.ID) {
	m.wear.Crest = crestOf(u, guild)
	name := u.EffectiveName()
	if mem != nil {
		m.wear.Plume = mem.PremiumSince != nil
		if mem.Nick != nil && *mem.Nick != "" {
			name = *mem.Nick
		}
	}
	if pixel.Plain(name) == "" {
		name = u.Username
	}
	if name != "" && name != m.name {
		m.name, m.dirty = name, true
	}
}

// wearBanner is user's custom status, as a presence brought it: their
// banner on or off now, kept for later when they aren't a member yet. It
// reports whether a member's banner changed. s.mu held.
func (s *server) wearBanner(user snowflake.ID, on bool, now time.Time) bool {
	s.presences++
	s.presenceAt = now
	m, ok := s.members[user]
	if !ok {
		if on {
			s.banners[user] = true
		} else {
			delete(s.banners, user)
		}
		return false
	}
	changed := m.wear.Banner != on
	m.wear.Banner = on
	return changed
}

// bannerOf is whether a member's custom status carries the server's own
// invite, .gg/<vanity>.
func bannerOf(acts []discord.Activity, vanity string) bool {
	if vanity == "" {
		return false
	}
	want := ".gg/" + strings.ToLower(vanity)
	for _, a := range acts {
		if a.Type == discord.ActivityTypeCustom && a.State != nil && strings.Contains(strings.ToLower(*a.State), want) {
			return true
		}
	}
	return false
}

var hashSeed = maphash.MakeSeed()

// chat counts one message by user: as activity once per ActivityGap, as pay
// once per cooldown, neither when it is too short or the last one again.
// It returns what it paid. s.mu held.
func (st *state) chat(s *server, guild, user snowflake.ID, content string, now time.Time) int64 {
	m := st.member(s, guild, user, DayOf(now))
	runes, repeat := -1, false
	if content != "" {
		runes = utf8.RuneCountInString(content)
		h := maphash.String(hashSeed, content)
		repeat, m.lastHash = h == m.lastHash, h
	}
	if !st.bal().ChatCounts(now.Sub(m.lastCount), runes, repeat) {
		return 0
	}
	m.lastCount, m.life = now, now
	m.messages++
	m.dirty = true
	if !st.bal().ChatPays(now.Sub(m.lastPay), runes, repeat) {
		return 0
	}
	m.lastPay = now
	r := Roll(st.seedOf(m.dayN), uint64(guild), uint64(user), m.dayN, uint64(m.day.Paid))
	return st.earn(s, m, guild, user, true, st.bal().ChatRaw(r))
}

// sit puts user in voice as vs says, or takes them out. Joining, and
// turning a camera or a stream on, is a sign of life; a mute toggle isn't,
// since a macro can do it. s.mu held.
func (st *state) sit(s *server, guild, user snowflake.ID, vs discord.VoiceState, bot bool, now time.Time) {
	if vs.ChannelID == nil {
		delete(s.seats, user)
		return
	}
	old, ok := s.seats[user]
	video := vs.SelfVideo || vs.SelfStream
	if !bot && (!ok || old.channel != *vs.ChannelID || video && !old.video) {
		st.member(s, guild, user, DayOf(now)).life = now
	}
	s.seats[user] = &seat{
		channel: *vs.ChannelID, bot: bot,
		deaf: vs.GuildDeaf || vs.SelfDeaf, muted: vs.GuildMute, suppressed: vs.Suppress,
		selfMute: vs.SelfMute, video: video,
	}
}

// minute pays everyone in voice in s for the minute just gone, the AFK
// channel aside, and returns how many earned. s.mu held.
func (st *state) minute(s *server, guild, afk snowflake.ID, now time.Time) int {
	today := DayOf(now)
	st.rollServer(s, today)
	rooms := map[snowflake.ID][]snowflake.ID{}
	for u, se := range s.seats {
		if se.channel != afk {
			rooms[se.channel] = append(rooms[se.channel], u)
		}
	}
	earning := 0
	for _, users := range rooms {
		if len(users) < 2 {
			continue
		}
		for _, u := range users {
			se := s.seats[u]
			if se.bot {
				continue
			}
			m := st.member(s, guild, u, today)
			me := Seat{Deaf: se.deaf, Muted: se.muted, Suppressed: se.suppressed, SelfMute: se.selfMute, Idle: now.Sub(m.life)}
			others := make([]Seat, 0, len(users)-1)
			for _, o := range users {
				if o != u {
					them := s.seats[o]
					others = append(others, Seat{Bot: them.bot, Deaf: them.deaf, Age: now.Sub(o.Time())})
				}
			}
			raw := st.bal().VoiceRaw(me, others, st.pools(guild, u, today).Pace)
			if raw == 0 {
				continue
			}
			m.voiceMin++
			st.earn(s, m, guild, u, false, raw)
			earning++
		}
	}
	return earning
}
