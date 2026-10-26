package pp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
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
	m.left(context.Background(), guild, fresh)
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
	// The next join within the hour doesn't ask again; one after it does.
	r := &inviteRest{invs: []discord.ExtendedInvite{inv("aa", old(1), 1, 0)}}
	m.rest = r
	m.OnEvent(joinEvent(old(8), t0))
	if got := row(); !strings.Contains(got, "manage server") {
		t.Errorf("asked again within the hour: %q", got)
	}
	m.now = func() time.Time { return t0.Add(lookAgain) }
	m.OnEvent(joinEvent(old(9), t0))
	if got := row(); got != "3 in the book · 0 vouched for · 0 counting" {
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

// A leave handled before its join closes the book on them, and a bird
// Discord says was here before is nobody's guest.
func TestLeaveFirstAndRejoin(t *testing.T) {
	m := testModule(&fakeDB{})
	host := old(1)
	r := &inviteRest{invs: []discord.ExtendedInvite{inv("aa", host, 0, 0)}}
	m.rest = r
	m.ready(guild, discord.GatewayGuild{})
	s := m.st.server(guild)
	m.OnEvent(&events.GuildMemberLeave{GuildID: guild, User: discord.User{ID: old(2)}})
	r.invs[0].Uses = 1
	m.OnEvent(joinEvent(old(2), t0))
	if g := s.guests[old(2)]; g.by != 0 || g.gone == 0 {
		t.Errorf("a leave before its join: %+v", g)
	}
	r.invs[0].Uses = 2
	back := joinEvent(old(3), t0)
	back.Member.Flags = discord.MemberFlagDidRejoin
	m.OnEvent(back)
	if g := s.guests[old(3)]; g.by != 0 {
		t.Errorf("a rejoin was vouched for: %+v", g)
	}
	// Flushed, a guest's seen day goes with the members.
	s.mu.Lock()
	r.invs[0].Uses = 3
	s.mu.Unlock()
	m.OnEvent(joinEvent(old(4), t0))
	s.mu.Lock()
	m.st.chat(s, guild, old(4), "hello all", t0)
	s.mu.Unlock()
	db := &fakeDB{}
	if _, err := m.st.flush(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(db.execs, "\n"), "pp_guest") || len(s.seenGuests) != 0 {
		t.Errorf("the seen day wasn't flushed: %q", db.execs)
	}
}

// A guest someone vouched for gets a button on the closed card that lets
// them into the beta on that word; a bird nobody vouched for, or a guest
// who left, gets only the key.
func TestJoinOnWord(t *testing.T) {
	m := testModule(&fakeDB{})
	m.Admin = func(discord.Interaction) bool { return false }
	ctx := context.Background()
	var granted []snowflake.ID
	m.Grant = func(_ context.Context, guild, user, role snowflake.ID) error {
		granted = append(granted, guild, user, role)
		return nil
	}
	s := m.st.server(guild)
	s.beta = 77
	card := func() string {
		e, sent := coretest.Event(t, "pp", nil)
		if err := m.Commands()[0].Run(ctx, e); err != nil {
			t.Fatal(err)
		}
		return js(*sent)
	}
	press := func() ([]discord.MessageUpdate, error) {
		e, _ := coretest.Button(t, "pp:vouched", nil)
		r := &redeemRest{}
		e.Client().Rest = r
		return r.updates, m.press(ctx, e)
	}
	if got := card(); strings.Contains(got, "pp:vouched") {
		t.Errorf("nobody's guest got the button: %s", got)
	}
	if _, err := press(); !errors.Is(err, errNoWord) {
		t.Errorf("nobody's guest pressed: %v", err)
	}

	s.enter(5, &guest{by: old(1), day: DayOf(t0)})
	if got := card(); !strings.Contains(got, "pp:vouched") || !strings.Contains(got, "@"+old(1).String()) {
		t.Errorf("the guest's card: %s", got)
	}
	updates, err := press()
	if err != nil || len(granted) != 3 || granted[1] != 5 || granted[2] != 77 || !strings.Contains(js(updates), "you're in the beta") {
		t.Errorf("joining: %v %v %s", err, granted, js(updates))
	}

	s.guests[5].gone = DayOf(t0)
	if got := card(); strings.Contains(got, "pp:vouched") {
		t.Errorf("a guest who left got the button: %s", got)
	}
}
