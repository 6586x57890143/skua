package pp

import (
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

var (
	t0    = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) // a Tuesday in season 3
	guild = snowflake.ID(3)
	room  = snowflake.ID(40)
	afkCh = snowflake.ID(41)
)

// old is a user whose account is long past CompanionAge.
func old(n int) snowflake.ID {
	return snowflake.New(t0.Add(-365*24*time.Hour)) + snowflake.ID(n)
}

func in(ch snowflake.ID) discord.VoiceState { return discord.VoiceState{ChannelID: &ch} }

func testState() (*state, *server) {
	st := newState(&Default, 42)
	return st, st.server(guild)
}

func TestVoiceNeedsCompany(t *testing.T) {
	st, s := testState()
	a, b, bot := old(1), old(2), old(3)
	st.sit(s, guild, a, in(room), false, t0)
	if n := st.minute(s, guild, afkCh, t0.Add(time.Minute)); n != 0 {
		t.Fatalf("alone, %d earned", n)
	}
	st.sit(s, guild, bot, in(room), true, t0)
	if n := st.minute(s, guild, afkCh, t0.Add(2*time.Minute)); n != 0 {
		t.Fatalf("with only a bot, %d earned", n)
	}
	st.sit(s, guild, b, in(room), false, t0)
	if n := st.minute(s, guild, afkCh, t0.Add(3*time.Minute)); n != 2 {
		t.Fatalf("two people earned as %d", n)
	}
	// a is new, so on the warm side.
	want := Default.VoiceBase * Default.Unmuted / 100 * (100 + Default.WarmSide) / 100
	if got := s.members[a].total; got != want {
		t.Errorf("a minute with an open mic paid %d; want %d", got, want)
	}
	if s.members[a].voiceMin != 1 || s.members[bot] != nil {
		t.Errorf("minutes %d; the bot is a member: %v", s.members[a].voiceMin, s.members[bot] != nil)
	}
	st.sit(s, guild, b, discord.VoiceState{}, false, t0)
	if _, ok := s.seats[b]; ok {
		t.Error("leaving kept the seat")
	}
}

func TestAFKChannelEarnsNothing(t *testing.T) {
	st, s := testState()
	st.sit(s, guild, old(1), in(afkCh), false, t0)
	st.sit(s, guild, old(2), in(afkCh), false, t0)
	if n := st.minute(s, guild, afkCh, t0.Add(time.Minute)); n != 0 {
		t.Errorf("%d earned in the afk channel", n)
	}
}

func TestSignsOfLife(t *testing.T) {
	st, s := testState()
	a := old(1)
	st.sit(s, guild, a, in(room), false, t0)
	if !s.members[a].life.Equal(t0) {
		t.Fatal("joining isn't a sign of life")
	}
	muted := in(room)
	muted.SelfMute = true
	st.sit(s, guild, a, muted, false, t0.Add(time.Hour))
	if !s.members[a].life.Equal(t0) {
		t.Error("a mute toggle counted as a sign of life")
	}
	cam := in(room)
	cam.SelfVideo = true
	st.sit(s, guild, a, cam, false, t0.Add(2*time.Hour))
	if !s.members[a].life.Equal(t0.Add(2 * time.Hour)) {
		t.Error("turning a camera on isn't a sign of life")
	}
	st.sit(s, guild, a, cam, false, t0.Add(3*time.Hour))
	if !s.members[a].life.Equal(t0.Add(2 * time.Hour)) {
		t.Error("staying on camera counted again")
	}
	st.chat(s, guild, a, "back again", t0.Add(4*time.Hour))
	if !s.members[a].life.Equal(t0.Add(4 * time.Hour)) {
		t.Error("a message isn't a sign of life")
	}
}

func TestChatPacing(t *testing.T) {
	st, s := testState()
	a := old(1)
	if got := st.chat(s, guild, a, "first message", t0); got == 0 {
		t.Fatal("the first message paid nothing")
	}
	cases := []struct {
		at      time.Duration
		text    string
		counts  bool
		pays    bool
		because string
	}{
		{5 * time.Second, "too soon to count", false, false, "inside the activity gap"},
		{10 * time.Second, "counts but no pay", true, false, "past the gap, inside the cooldown"},
		{30 * time.Second, "counts but no pay", false, false, "a repeat"},
		{40 * time.Second, "ok", false, false, "too short"},
		{50 * time.Second, "a new thing to say", true, true, "past the cooldown"},
	}
	for _, c := range cases {
		before := s.members[a].messages
		got := st.chat(s, guild, a, c.text, t0.Add(c.at))
		if counted := s.members[a].messages > before; counted != c.counts || (got > 0) != c.pays {
			t.Errorf("%s: counted %v paid %d", c.because, counted, got)
		}
	}
	if s.members[a].day.Talk == 0 || s.members[a].weekAct[BoardChat] == 0 {
		t.Error("chat didn't count toward the chat board")
	}
	// Without content, only pace applies.
	if got := st.chat(s, guild, old(2), "", t0); got == 0 {
		t.Error("a message skua can't read paid nothing")
	}
}

func TestRolls(t *testing.T) {
	st, s := testState()
	a := old(1)
	st.chat(s, guild, a, "monday", t0)
	m := s.members[a]
	m.seasonPP, m.weekAct = 999, [Boards]int64{5, 5}
	m.dirty = false

	tomorrow := t0.Add(24 * time.Hour)
	st.member(s, guild, a, DayOf(tomorrow))
	if m.dayN != DayOf(tomorrow) || m.day.Chat != 0 || m.day.Rested == 0 || !m.dirty {
		t.Errorf("a new day: %+v rested %d", m.day, m.day.Rested)
	}
	if m.weekAct != [Boards]int64{5, 5} || m.seasonPP != 999 {
		t.Error("a new day reset the week or the season")
	}
	nextWeek := t0.Add(7 * 24 * time.Hour)
	st.member(s, guild, a, DayOf(nextWeek))
	if m.weekAct != [Boards]int64{} || m.seasonPP != 999 {
		t.Errorf("a new week: %v %d", m.weekAct, m.seasonPP)
	}
	nextSeason := t0.Add(28 * 24 * time.Hour)
	st.member(s, guild, a, DayOf(nextSeason))
	if m.seasonPP != 0 || m.total == 0 {
		t.Errorf("a new season kept %d season pp or lost the total %d", m.seasonPP, m.total)
	}
}

func TestHearth(t *testing.T) {
	st, s := testState()
	today := DayOf(t0)
	st.rollServer(s, today)
	s.history = []int64{1000}
	a := old(1)
	m := st.member(s, guild, a, today)
	st.earn(s, m, guild, a, false, 1500)
	if s.lit != 3 || s.hearth() != 3 {
		t.Fatalf("a day half again past usual lit tier %d", s.lit)
	}
	st.rollServer(s, today+1)
	if s.lit != 0 || s.litBefore != 3 || s.hearth() != 3 || s.history[len(s.history)-1] != 1500 {
		t.Errorf("the next day: lit %d before %d history %v", s.lit, s.litBefore, s.history)
	}
	st.rollServer(s, today+3)
	if s.hearth() != 0 {
		t.Error("the hearth stayed lit across a quiet day")
	}
	for range 10 {
		s.raw = 7
		st.rollServer(s, s.day+1)
	}
	if len(s.history) != 7 {
		t.Errorf("history keeps %d days; want a week", len(s.history))
	}
}

func TestWearing(t *testing.T) {
	yes := true
	other := snowflake.ID(99)
	tag := func(g snowflake.ID, on bool) discord.User {
		return discord.User{PrimaryGuild: &discord.PrimaryGuild{IdentityGuildID: &g, IdentityEnabled: &on}}
	}
	if !crestOf(tag(guild, yes), guild) || crestOf(tag(other, yes), guild) || crestOf(tag(guild, false), guild) || crestOf(discord.User{}, guild) {
		t.Error("the crest isn't the tag worn for this server")
	}
	m := &member{}
	now := time.Now()
	seen(m, &discord.Member{PremiumSince: &now}, tag(guild, true), guild)
	if !m.wear.Crest || !m.wear.Plume {
		t.Errorf("wearing %+v", m.wear)
	}
	glyphs := "🦆🦆"
	seen(m, &discord.Member{Nick: &glyphs}, discord.User{Username: "steve"}, guild)
	if m.name != "steve" {
		t.Errorf("a name the board can't draw stayed %q", m.name)
	}
	status := func(s string) []discord.Activity {
		return []discord.Activity{{Type: discord.ActivityTypeCustom, State: &s}}
	}
	if !bannerOf(status("come hang .GG/Melted"), "melted") || bannerOf(status(".gg/melted"), "") ||
		bannerOf(status("discord.gg/other"), "melted") || bannerOf(nil, "melted") {
		t.Error("the banner isn't the server's invite in a custom status")
	}
}

// A member earns on the warm side until WarmDays after they first earned,
// and the buffs card says which day they're on.
func TestWarmSideEarns(t *testing.T) {
	st, s := testState()
	a := old(1)
	st.chat(s, guild, a, "hello there", t0)
	mem := s.members[a]
	if !mem.wear.Warm || mem.buffHi != Default.WarmSide {
		t.Fatalf("a newcomer earned under %d, warm %v", mem.buffHi, mem.wear.Warm)
	}
	if b := st.buffList(s, mem, "", 0, DayOf(t0)); b[len(b)-1].Name != "warm side" || b[len(b)-1].How != "new here: day 1 of 14" {
		t.Errorf("the buffs card: %+v", b[len(b)-1])
	}
	later := t0.Add(time.Duration(Default.WarmDays) * 24 * time.Hour)
	st.chat(s, guild, a, "two weeks on", later)
	if mem.wear.Warm || mem.buffHi != 0 {
		t.Errorf("still warm after %d days: buff %d", Default.WarmDays, mem.buffHi)
	}
	if b := st.buffList(s, mem, "", 0, DayOf(later)); b[len(b)-1].Name == "warm side" {
		t.Error("the card still shows the warm side")
	}
}
