package pp

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// together runs n voice minutes from at with users in room, everyone else
// out of voice.
func together(st *state, s *server, at time.Time, n int, users ...snowflake.ID) time.Time {
	clear(s.seats)
	for _, u := range users {
		st.sit(s, guild, u, in(room), false, at)
	}
	for range n {
		at = at.Add(time.Minute)
		for _, u := range users {
			st.member(s, guild, u, DayOf(at)).life = at
		}
		st.minute(s, guild, 0, at)
	}
	return at
}

// A chirograph takes two: posted, signed once, it fills with minutes the
// two spend together and pays both its joinder; a third can't sign it,
// nor the poster, nor anyone holding one already or paid today.
func TestChirograph(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	today := DayOf(t0)
	a, b, c := old(1), old(2), old(3)
	p, err := st.post(s, guild, a, false, today)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.post(s, guild, a, true, today); !errors.Is(err, errPactHeld) {
		t.Errorf("a second notice: %v", err)
	}
	if _, err := st.sign(s, guild, a, today, a, today); !errors.Is(err, errPactYours) {
		t.Errorf("signing your own: %v", err)
	}
	if _, err := st.sign(s, guild, a, today, snowflake.New(t0), today); !errors.Is(err, errPactYoung) {
		t.Errorf("a fresh account: %v", err)
	}
	if _, err := st.sign(s, guild, a, today, b, today); err != nil {
		t.Fatal(err)
	}
	if _, err := st.sign(s, guild, a, today, c, today); !errors.Is(err, errPactHeld) && !errors.Is(err, errPactFull) {
		t.Errorf("a third signature: %v", err)
	}
	before := s.members[a].purse
	at := together(st, s, t0, int(Default.PactGoal)-1, a, b, c)
	if p.minutes != Default.PactGoal-1 || s.members[a].pact == nil {
		t.Fatalf("after %d minutes: %+v", Default.PactGoal-1, p)
	}
	together(st, s, at, 1, a, b)
	ma, mb := s.members[a], s.members[b]
	if ma.pact != nil || mb.pact != nil || s.members[c].signs.signatures != 0 {
		t.Fatal("a finished chirograph wasn't let go, or paid an outsider")
	}
	if ma.signs != (signs{chirographs: 1, signatures: 1, paidDay: today}) || mb.signs != ma.signs {
		t.Errorf("signs %+v %+v", ma.signs, mb.signs)
	}
	last := s.pending[len(s.pending)-1]
	if last.reason != reasonJoinder || last.inputs[0] != 2 || last.inputs[1] != Default.Joinder[0] || ma.purse <= before {
		t.Errorf("the joinder: %+v", last)
	}
	if _, err := st.post(s, guild, a, false, today); !errors.Is(err, errPactPaid) {
		t.Errorf("a second joinder the same day: %v", err)
	}
}

// A round robin takes three or four, counts a minute with three of them
// together, pays only those there for half its minutes, at the rate for
// how many, and takes no signature once its work has started.
func TestRoundRobin(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	today := DayOf(t0)
	a, b, c, d, e := old(1), old(2), old(3), old(4), old(5)
	p, _ := st.post(s, guild, a, true, today)
	for _, u := range []snowflake.ID{b, c} {
		if _, err := st.sign(s, guild, a, today, u, today); err != nil {
			t.Fatal(err)
		}
	}
	at := together(st, s, t0, 2, a, b)
	if p.minutes != 0 {
		t.Fatal("two of a round robin counted")
	}
	at = together(st, s, at, 1, a, b, c)
	if _, err := st.sign(s, guild, a, today, d, today); !errors.Is(err, errPactActive) {
		t.Errorf("a signature once work started: %v", err)
	}
	together(st, s, at, int(Default.PactGoal)-1, a, b, c, e)
	if s.members[e].signs.signatures != 0 {
		t.Error("a bird who never signed was paid")
	}
	for _, u := range []snowflake.ID{a, b, c} {
		if m := s.members[u]; m.signs.signatures != 1 || m.signs.rings != 0 || m.signs.chirographs != 0 {
			t.Errorf("%v signs %+v", u, m.signs)
		}
	}
	if last := s.pending[len(s.pending)-1]; last.inputs[0] != 3 || last.inputs[1] != Default.Joinder[1] {
		t.Errorf("three paid: %+v", last)
	}
}

// A full ring pays the four rate and counts for the ring's lore; one who
// signed and wasn't there for half isn't paid.
func TestFullRing(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	today := DayOf(t0)
	us := []snowflake.ID{old(1), old(2), old(3), old(4), old(5)}
	if _, err := st.post(s, guild, us[0], true, today); err != nil {
		t.Fatal(err)
	}
	for _, u := range us[1:4] {
		if _, err := st.sign(s, guild, us[0], today, u, today); err != nil {
			t.Fatal(err)
		}
	}
	together(st, s, t0, int(Default.PactGoal), us[:4]...)
	for _, u := range us[:4] {
		if s.members[u].signs.rings != 1 {
			t.Errorf("%v signs %+v", u, s.members[u].signs)
		}
	}
	if last := s.pending[len(s.pending)-1]; last.inputs[0] != 4 || last.inputs[1] != Default.Joinder[2] {
		t.Errorf("four paid: %+v", last)
	}

	// The next day a fourth who sits out is signed but unpaid.
	next := t0.Add(24 * time.Hour)
	if _, err := st.post(s, guild, us[0], true, DayOf(next)); err != nil {
		t.Fatal(err)
	}
	for _, u := range us[1:4] {
		if _, err := st.sign(s, guild, us[0], DayOf(next), u, DayOf(next)); err != nil {
			t.Fatal(err)
		}
	}
	together(st, s, next, int(Default.PactGoal), us[:3]...)
	if s.members[us[3]].signs.signatures != 1 || s.members[us[3]].pact != nil {
		t.Errorf("one who sat out: %+v", s.members[us[3]].signs)
	}
}

// An unfinished pact lapses PactLife days after it was posted.
func TestPactLapses(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	a := old(1)
	st.rollServer(s, DayOf(t0))
	if _, err := st.post(s, guild, a, false, DayOf(t0)); err != nil {
		t.Fatal(err)
	}
	st.rollServer(s, DayOf(t0)+Default.PactLife-1)
	if s.members[a].pact == nil {
		t.Fatal("lapsed early")
	}
	st.rollServer(s, DayOf(t0)+Default.PactLife)
	if s.members[a].pact != nil {
		t.Error("didn't lapse")
	}
}

// A pact saved on each signer's row reads back as one, shared; a copy that
// doesn't name its holder, or won't read, is dropped.
func TestPactSaved(t *testing.T) {
	p := &pact{opener: old(1), day: 9, ring: true, minutes: 4, signers: []signer{{old(1), 4}, {old(2), 3}, {old(3), 4}}}
	s := newServer(guild)
	for _, u := range []snowflake.ID{old(1), old(2), old(3), old(4)} {
		s.members[u] = &member{pact: decodePact(encodePact(p))}
	}
	s.members[old(5)] = &member{pact: decodePact("1,2,x")}
	joinPacts(s)
	got := s.members[old(1)].pact
	if got == nil || got != s.members[old(2)].pact || got != s.members[old(3)].pact || s.members[old(4)].pact != nil || s.members[old(5)].pact != nil {
		t.Fatal("pacts weren't joined")
	}
	if encodePact(got) != encodePact(p) || encodePact(nil) != "" {
		t.Errorf("round trip %q", encodePact(got))
	}
	sg := signs{1, 2, 3, 4}
	if decodeSigns(encodeSigns(sg)) != sg || encodeSigns(signs{}) != "" || decodeSigns("x") != (signs{}) {
		t.Error("signs round trip")
	}
	if decodePact(encodePact(&pact{opener: 1, signers: []signer{{1, 0}, {2, 0}, {3, 0}}})) != nil {
		t.Error("a chirograph with three signers read")
	}
}

// The quests card leads to the notices; from there a member posts a round
// robin, which goes up in the channel with its sign button, another signs
// it there and a third picks it off the board.
func TestNoticeButtons(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(3)
	m.st.member(s, 3, 5, DayOf(t0)).classSeason = 0
	press := func(id string, user string) (*[]discord.MessageCreate, string, error) {
		e, sent := coretest.Button(t, id, func(p map[string]any) {
			if user != "" {
				p["member"].(map[string]any)["user"].(map[string]any)["id"] = user
			}
		})
		var updated string
		e.Respond = func(typ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			if mc, ok := d.(discord.MessageCreate); ok {
				*sent = append(*sent, mc)
			}
			updated = js(d)
			return nil
		}
		return sent, updated, m.press(context.Background(), e)
	}
	if sent, _, err := press("pp:quests", ""); err != nil || !strings.Contains(js((*sent)[0].Components), "pp:notices") {
		t.Fatalf("the quests card: %v", err)
	}
	sent, _, err := press("pp:notices", "")
	if err != nil || !strings.Contains(js((*sent)[0]), "pp:pact:4") || !strings.Contains(js((*sent)[0]), "nothing on the board") {
		t.Fatalf("the notices with nothing up: %v", err)
	}
	sent, _, err = press("pp:pact:4", "")
	if err != nil || (*sent)[0].Flags.Has(discord.MessageFlagEphemeral) || !strings.Contains(js((*sent)[0]), "pp:sign:5:"+strconv.FormatInt(DayOf(t0), 10)) || len((*sent)[0].Files) != 1 {
		t.Fatalf("the posted round robin: %v", err)
	}
	s.mu.Lock()
	m.st.member(s, 3, 6, DayOf(t0)).class = "fighter"
	s.mu.Unlock()
	if _, updated, err := press("pp:sign:5:"+strconv.FormatInt(DayOf(t0), 10), "6"); err != nil || !strings.Contains(updated, "pale clay") {
		t.Fatalf("signing on the post: %v %s", err, updated)
	}
	if _, _, err := press("pp:sign:5:"+strconv.FormatInt(DayOf(t0), 10), "6"); !errors.Is(err, errPactHeld) {
		t.Errorf("signed twice: %v", err)
	}
	if _, _, err := press("pp:sign:x:1", "7"); !errors.Is(err, errPactGone) {
		t.Errorf("a sign button naming nobody: %v", err)
	}
	if sent, _, err := press("pp:notices", "7"); err != nil || !strings.Contains(js((*sent)[0]), "pp:signpick") {
		t.Fatalf("the board for someone else: %v", err)
	}
	e, _ := coretest.Button(t, "pp:signpick", func(p map[string]any) {
		p["member"].(map[string]any)["user"].(map[string]any)["id"] = "7"
		p["data"] = map[string]any{"custom_id": "pp:signpick", "component_type": 3, "values": []any{"5:" + strconv.FormatInt(DayOf(t0), 10)}}
	})
	e.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
		return nil
	}
	if err := m.press(context.Background(), e); err != nil || len(s.members[5].pact.signers) != 3 {
		t.Fatalf("signing from the board: %v", err)
	}
	if _, _, err := press("pp:rally", ""); !errors.Is(err, errPactRally) {
		t.Errorf("put up again straight away: %v", err)
	}
	m.now = func() time.Time { return t0.Add(rallyWait) }
	if sent, _, err := press("pp:rally", ""); err != nil || !strings.Contains(js((*sent)[0]), "pp:sign:5:"+strconv.FormatInt(DayOf(t0), 10)) {
		t.Fatalf("putting it up again: %v", err)
	}
	// A post from an older notice by the same bird signs nothing.
	if _, _, err := press("pp:sign:5:1", "9"); !errors.Is(err, errPactGone) {
		t.Errorf("an old post signed a newer notice: %v", err)
	}
	if _, _, err := press("pp:rally", "8"); !errors.Is(err, errPactGone) {
		t.Errorf("rallying with none held: %v", err)
	}
}

// A signer there for half who left before the day turned is paid in the
// day it finishes, and can't take a second joinder that day.
func TestLateSignerPaidToday(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	start := dayStart(DayOf(t0) + 1).Add(-20 * time.Minute)
	a, b, c, d := old(1), old(2), old(3), old(4)
	if _, err := st.post(s, guild, a, true, DayOf(start)); err != nil {
		t.Fatal(err)
	}
	for _, u := range []snowflake.ID{b, c, d} {
		if _, err := st.sign(s, guild, a, DayOf(start), u, DayOf(start)); err != nil {
			t.Fatal(err)
		}
	}
	at := together(st, s, start, 20, a, b, c, d)
	together(st, s, at, int(Default.PactGoal)-20, a, b, c)
	today := DayOf(at)
	if md := s.members[d]; md.signs.paidDay != today || md.dayN != today {
		t.Fatalf("the one who left: paid day %d in day %d, want %d", md.signs.paidDay, md.dayN, today)
	}
	if _, err := st.post(s, guild, d, false, today); !errors.Is(err, errPactPaid) {
		t.Errorf("a second joinder the same day: %v", err)
	}
}
