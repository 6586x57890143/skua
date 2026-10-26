package pp

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// inviteRest serves the server's invites as they stand; down refuses.
type inviteRest struct {
	rest.Rest
	invs []discord.ExtendedInvite
	down bool
}

func (f *inviteRest) GetGuildInvites(snowflake.ID, ...rest.RequestOpt) ([]discord.ExtendedInvite, error) {
	if f.down {
		return nil, errors.New("missing access")
	}
	return append([]discord.ExtendedInvite(nil), f.invs...), nil
}

func inv(code string, by snowflake.ID, uses, most int) discord.ExtendedInvite {
	return discord.ExtendedInvite{Invite: discord.Invite{Code: code, Inviter: &discord.User{ID: by}}, Uses: uses, MaxUses: most}
}

// joinEvent is user joining guild at at.
func joinEvent(user snowflake.ID, at time.Time) *events.GuildMemberJoin {
	return &events.GuildMemberJoin{GenericGuildMember: &events.GenericGuildMember{GuildID: guild,
		Member: discord.Member{User: discord.User{ID: user}, JoinedAt: &at}}}
}

// The one invite whose uses went up by one since the last look made the
// join; the first look, two at once and a vanished unlimited invite are
// nobody's, and a limited one gone at its last use is its maker's.
func TestAttribute(t *testing.T) {
	a, b := old(1), old(2)
	s := newServer(guild)
	if got := s.attribute([]discord.ExtendedInvite{inv("aa", a, 3, 0)}); got != 0 {
		t.Errorf("the first look gave %d", got)
	}
	if got := s.attribute([]discord.ExtendedInvite{inv("aa", a, 4, 0), inv("bb", b, 0, 0)}); got != a {
		t.Errorf("a's invite went up: %d", got)
	}
	if got := s.attribute([]discord.ExtendedInvite{inv("aa", a, 5, 0), inv("bb", b, 1, 0)}); got != 0 {
		t.Errorf("two went up: %d", got)
	}
	if got := s.attribute([]discord.ExtendedInvite{inv("aa", a, 5, 0), inv("bb", b, 1, 0), inv("cc", b, 1, 0)}); got != b {
		t.Errorf("a new invite used once: %d", got)
	}
	if got := s.attribute([]discord.ExtendedInvite{inv("bb", b, 1, 0), inv("cc", b, 1, 0), inv("dd", a, 0, 1)}); got != 0 {
		t.Errorf("an unlimited invite went away: %d", got)
	}
	if got := s.attribute([]discord.ExtendedInvite{inv("bb", b, 1, 0), inv("cc", b, 1, 0)}); got != a {
		t.Errorf("a single use went at its use: %d", got)
	}
	bot := inv("ee", a, 1, 0)
	bot.Inviter.Bot = true
	if got := s.attribute([]discord.ExtendedInvite{inv("bb", b, 1, 0), inv("cc", b, 1, 0), bot}); got != 0 {
		t.Errorf("a bot's invite: %d", got)
	}
}

// A guest joins on a member's invite, counts once they've stayed and done
// something, lights the buff and grows the key, and stops when they go
// quiet or leave; a bird /pp knew, a young account and a return don't.
func TestSuretyLifecycle(t *testing.T) {
	db := &fakeDB{}
	m := testModule(db)
	host, fresh, young, known := old(1), old(2), snowflake.New(t0.Add(-24*time.Hour)), old(4)
	r := &inviteRest{invs: []discord.ExtendedInvite{inv("aa", host, 0, 0)}}
	m.rest = r
	s := m.st.server(guild)
	s.mu.Lock()
	m.st.member(s, guild, host, DayOf(t0))
	m.st.member(s, guild, known, DayOf(t0))
	s.mu.Unlock()
	m.ready(guild, discord.GatewayGuild{})

	for i, u := range []snowflake.ID{fresh, young, known} {
		r.invs[0].Uses = i + 1
		m.OnEvent(joinEvent(u, t0))
	}
	if got := s.guests[fresh].by; got != host {
		t.Fatalf("fresh is entered under %d", got)
	}
	if s.guests[young].by != 0 || s.guests[known].by != 0 {
		t.Errorf("a young account or a known bird is vouched for: %+v %+v", s.guests[young], s.guests[known])
	}
	if len(db.execs) != 3 || !strings.Contains(db.execs[0], "pp_guest") {
		t.Errorf("writes: %q", db.execs)
	}

	// Seen once on their first day, then a week on: they count.
	s.mu.Lock()
	m.st.chat(s, guild, fresh, "hello all", t0)
	week := t0.Add(time.Duration(Default.GuestStay) * 24 * time.Hour)
	m.st.member(s, guild, host, DayOf(week.Add(-24*time.Hour)))
	if s.members[host].wear.Guests != 0 {
		t.Error("a guest counts before their week")
	}
	m.st.chat(s, guild, fresh, "still here", week)
	host1 := m.st.member(s, guild, host, DayOf(week))
	if host1.wear.Guests != 1 || Default.Buff(host1.wear, 0) != Default.Surety {
		t.Errorf("after a week: %+v buff %d", host1.wear, Default.Buff(host1.wear, 0))
	}
	ever := func(day int64) int64 { _, e := m.st.vouched(s, host, day); return e }
	if ever(DayOf(week)) != 1 {
		t.Error("the key didn't grow")
	}
	sh := m.sheet(s, host1, guild, host, DayOf(week))
	if sh.Key != "key" || sh.Vouched != 1 || !sh.Surety {
		t.Errorf("the sheet: %q %d %v", sh.Key, sh.Vouched, sh.Surety)
	}
	// Quiet past GuestSeen: the buff goes, the key stays.
	quiet := week.Add(time.Duration(Default.GuestSeen) * 24 * time.Hour)
	if m.st.member(s, guild, host, DayOf(quiet)).wear.Guests != 0 || ever(DayOf(quiet)) != 1 {
		t.Error("a quiet guest still counts, or the key shrank")
	}
	m.st.chat(s, guild, fresh, "back again", quiet)
	s.mu.Unlock()
	m.now = func() time.Time { return quiet }
	m.left(guild, fresh)
	s.mu.Lock()
	m.st.sureties(s, DayOf(quiet))
	if s.members[host].wear.Guests != 0 || s.guests[fresh].gone == 0 {
		t.Error("a guest who left still counts")
	}
	if ever(DayOf(quiet)) != 1 {
		t.Error("leaving took the key")
	}
	s.mu.Unlock()

	// Back again: they stay as they were.
	r.invs[0].Uses = 4
	m.OnEvent(joinEvent(fresh, quiet))
	if s.guests[fresh].gone == 0 {
		t.Error("a return was entered again")
	}
	m.OnEvent(&events.GuildMemberLeave{GuildID: guild, User: discord.User{ID: known}})
	if s.guests[known].gone == 0 {
		t.Error("a leave wasn't kept")
	}
}

// The badge grows with every guest who ever counted.
func TestKeys(t *testing.T) {
	for n, want := range map[int64]string{0: "", 1: "key", 2: "key", 3: "key_gold", 9: "key_gold", 10: "freedom", 99: "freedom"} {
		if got, _ := keyOf(n); got != want {
			t.Errorf("%d guests: %q, want %q", n, got, want)
		}
	}
}

// Without Manage Server nobody is entered under anyone, and the admins'
// report says why.
func TestGateRow(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(guild)
	row := func() string { return m.Report(guild).Rows[len(m.Report(guild).Rows)-1][1] }
	if got := row(); !strings.HasPrefix(got, "nobody yet") {
		t.Errorf("empty: %q", got)
	}
	m.rest = &inviteRest{down: true}
	m.OnEvent(joinEvent(old(7), t0))
	if got := row(); !strings.Contains(got, "manage server") || s.guests[old(7)].by != 0 {
		t.Errorf("refused: %q", got)
	}
	m.rest = &inviteRest{}
	m.OnEvent(joinEvent(old(8), t0))
	if got := row(); got != "2 joined · 0 vouched for · 0 counting" {
		t.Errorf("after: %q", got)
	}
}

// The buffs card lights surety with what it adds and the key earned.
func TestSuretyCard(t *testing.T) {
	st, s := testState()
	mem := st.member(s, guild, old(1), DayOf(t0))
	mem.wear.Guests = 9
	b := st.buffList(s, mem, "", 3)[3]
	if !b.On || b.Icon != "key_gold" || b.Effect != "+10%" || !strings.HasPrefix(b.How, "9 of your guests count") {
		t.Errorf("surety: %+v", b)
	}
}
