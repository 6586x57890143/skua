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
	"github.com/6586x57890143/skua/internal/guard"
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
	if _, err := st.sign(s, guild, pactKey{opener: a, day: today}, a, today); !errors.Is(err, errPactYours) {
		t.Errorf("signing your own: %v", err)
	}
	if _, err := st.sign(s, guild, pactKey{opener: a, day: today}, snowflake.New(t0), today); !errors.Is(err, errPactYoung) {
		t.Errorf("a fresh account: %v", err)
	}
	if _, err := st.sign(s, guild, pactKey{opener: a, day: today}, b, today); err != nil {
		t.Fatal(err)
	}
	if _, err := st.sign(s, guild, pactKey{opener: a, day: today}, c, today); !errors.Is(err, errPactHeld) && !errors.Is(err, errPactFull) {
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
	if ma.signs != (signs{chirographs: 1, signatures: 1, paidDay: today, postedDay: today}) || mb.signs != (signs{chirographs: 1, signatures: 1, paidDay: today}) {
		t.Errorf("signs %+v %+v", ma.signs, mb.signs)
	}
	last := s.pending[len(s.pending)-1]
	if last.reason != reasonJoinder || last.inputs[0] != 2 || last.inputs[1] != Default.JoinderPay(2) || ma.purse <= before {
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
		if _, err := st.sign(s, guild, pactKey{opener: a, day: today}, u, today); err != nil {
			t.Fatal(err)
		}
	}
	at := together(st, s, t0, 2, a, b)
	if p.minutes != 0 {
		t.Fatal("two of a round robin counted")
	}
	// Three signed and together, but not started: nothing counts until
	// its poster starts it, and nobody else can.
	at = together(st, s, at, 1, a, b, c)
	if p.minutes != 0 {
		t.Fatal("a ring counted before it was started")
	}
	if _, err := st.start(s, b); !errors.Is(err, errPactStart) {
		t.Errorf("a signer started it: %v", err)
	}
	if _, err := st.start(s, a); err != nil {
		t.Fatal(err)
	}
	at = together(st, s, at, 1, a, b, c)
	if _, err := st.sign(s, guild, pactKey{opener: a, day: today}, d, today); !errors.Is(err, errPactActive) {
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
	if last := s.pending[len(s.pending)-1]; last.inputs[0] != 3 || last.inputs[1] != Default.JoinderPay(3) {
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
		if _, err := st.sign(s, guild, pactKey{opener: us[0], day: today}, u, today); err != nil {
			t.Fatal(err)
		}
	}
	together(st, s, t0, int(Default.PactGoal), us[:4]...)
	for _, u := range us[:4] {
		if s.members[u].signs.rings != 1 {
			t.Errorf("%v signs %+v", u, s.members[u].signs)
		}
	}
	if last := s.pending[len(s.pending)-1]; last.inputs[0] != 4 || last.inputs[1] != Default.JoinderPay(4) {
		t.Errorf("four paid: %+v", last)
	}

	// The next day a fourth who sits out is signed but unpaid.
	next := t0.Add(24 * time.Hour)
	if _, err := st.post(s, guild, us[0], true, DayOf(next)); err != nil {
		t.Fatal(err)
	}
	for _, u := range us[1:4] {
		if _, err := st.sign(s, guild, pactKey{opener: us[0], day: DayOf(next)}, u, DayOf(next)); err != nil {
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
	sg := signs{1, 2, 3, 4, 5}
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
	if err != nil || (*sent)[0].Flags.Has(discord.MessageFlagEphemeral) || !strings.Contains(js((*sent)[0]), s.members[5].pact.key()) || len((*sent)[0].Files) != 1 {
		t.Fatalf("the posted round robin: %v", err)
	}
	s.mu.Lock()
	m.st.member(s, 3, 6, DayOf(t0)).class = "fighter"
	s.mu.Unlock()
	if _, updated, err := press("pp:sign:"+s.members[5].pact.key(), "6"); err != nil || !strings.Contains(updated, "pale clay") {
		t.Fatalf("signing on the post: %v %s", err, updated)
	}
	if _, _, err := press("pp:sign:"+s.members[5].pact.key(), "6"); !errors.Is(err, errPactHeld) {
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
	if sent, _, err := press("pp:rally", ""); err != nil || !strings.Contains(js((*sent)[0]), s.members[5].pact.key()) {
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
		if _, err := st.sign(s, guild, pactKey{opener: a, day: DayOf(start)}, u, DayOf(start)); err != nil {
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

func BenchmarkNoticesScreen(b *testing.B) {
	n := &Notice{Ring: true, Places: 4, Goal: 30, Together: 17, Pays: "90 / 120 pp each"}
	for i := range 3 {
		n.Signers = append(n.Signers, NoticeSigner{Name: "steve", Portrait: Portrait(Classes[i]), Minutes: 17})
	}
	for b.Loop() {
		if _, err := NoticesScreen("heavywinds", n, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// A bird posts one notice a day, so a post's key names only that notice;
// a post pressed once its notice is full is redrawn rather than refused.
func TestOnePostADay(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(3)
	today := DayOf(t0)
	s.mu.Lock()
	if _, err := m.st.post(s, 3, 5, false, today); err != nil {
		t.Fatal(err)
	}
	if _, err := m.st.sign(s, 3, pactKey{opener: 5, day: today}, old(6), today); err != nil {
		t.Fatal(err)
	}
	s.members[5].pact, s.members[old(6)].pact = nil, nil
	if _, err := m.st.post(s, 3, 5, true, today); !errors.Is(err, errPactPosted) {
		t.Errorf("a second post the same day: %v", err)
	}
	s.mu.Unlock()

	next := today + 1
	m.now = func() time.Time { return dayStart(next).Add(time.Hour) }
	s.mu.Lock()
	if _, err := m.st.post(s, 3, 5, false, next); err != nil {
		t.Fatal(err)
	}
	if _, err := m.st.sign(s, 3, pactKey{opener: 5, day: next}, old(6), next); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	e, _ := coretest.Button(t, "pp:sign:5:"+strconv.FormatInt(next, 10), func(p map[string]any) {
		p["member"].(map[string]any)["user"].(map[string]any)["id"] = old(7).String()
	})
	var updated string
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		updated = js(d)
		return nil
	}
	if err := m.press(context.Background(), e); err != nil || !strings.Contains(updated, `"disabled":true`) {
		t.Errorf("a full notice's post pressed: %v %s", err, updated)
	}
}

// A finished pact tells the channel it went up in who was paid and who
// learned a page of their lore, once its rows are saved; one posted
// nowhere tells nobody. A pact saved before pacts kept a channel reads.
func TestJoinderNews(t *testing.T) {
	db := &fakeDB{}
	m := testModule(db)
	r := &postRest{}
	m.rest, m.Guard = r, guard.New()
	s := m.st.server(guild)
	today := DayOf(t0)
	a, b := old(1), old(2)
	s.mu.Lock()
	p, err := m.st.post(s, guild, a, false, today)
	if err != nil {
		t.Fatal(err)
	}
	p.channel = 77
	if _, err := m.st.sign(s, guild, pactKey{opener: a, day: today}, b, today); err != nil {
		t.Fatal(err)
	}
	s.members[b].signs.chirographs = 1 // b has the first page already
	together(m.st, s, t0, int(Default.PactGoal), a, b)
	if len(s.joinders) != 1 {
		t.Fatalf("%d joinders to tell", len(s.joinders))
	}
	s.mu.Unlock()
	// Not told until a flush has committed the rows that paid it.
	m.postJoinders()
	if len(r.posts) != 0 {
		t.Fatal("told before its rows were saved")
	}
	db.fail = errors.New("down")
	m.Save()
	m.postJoinders()
	if len(r.posts) != 0 {
		t.Fatal("told after a save that failed")
	}
	db.fail = nil
	m.Save()
	m.postJoinders()
	if len(r.posts) != 1 || r.to[0] != 77 || !strings.Contains(r.posts[0], "teeth match") || !strings.Contains(r.posts[0], grouped(Default.JoinderPay(2)/unit)+" pp each to") ||
		!strings.Contains(r.posts[0], "**learned** \\u003c@"+a.String()) || strings.Contains(r.posts[0], "learned** \\u003c@"+b.String()) {
		t.Errorf("posted %v to %v", r.posts, r.to)
	}
	m.postJoinders()
	if len(r.posts) != 1 {
		t.Error("told twice")
	}
	// Discord down: the news waits for the next tick rather than going.
	s.mu.Lock()
	s.joinders = []joinderNews{{channel: 77, got: Default.JoinderPay(2), paid: []snowflake.ID{a, b}}}
	s.mu.Unlock()
	r.down = true
	m.postJoinders()
	if len(s.joinders) != 0 {
		t.Error("a plain failure was kept")
	}

	// Two signers' rows, one that heard the channel: joined, the pact
	// keeps it.
	q := &member{pact: decodePact("5,9,0,3,5,3,6,2")}
	q.pact.channel = 77
	srv := newServer(guild)
	srv.members[5], srv.members[6] = &member{pact: decodePact("5,9,0,3,5,3,6,2")}, q
	joinPacts(srv)
	if srv.members[5].pact != srv.members[6].pact || srv.members[5].pact.channel != 77 {
		t.Error("the joined pact lost its channel")
	}
	if mentions([]snowflake.ID{1, 2, 3}) != "<@1>, <@2> and <@3>" || mentions([]snowflake.ID{1}) != "<@1>" {
		t.Error("mentions")
	}
	if !strings.Contains(joinderText(joinderNews{ring: true, got: 90 * unit, paid: []snowflake.ID{1, 2, 3}}), "ring closes") {
		t.Error("a round robin's news")
	}
}

// Before the work starts a signer leaves and the place opens, and the
// poster leaving takes the notice down for all; after, one who leaves
// isn't paid, and too few left to count ends it for everyone.
func TestWithdraw(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	today := DayOf(t0)
	a, b, c, d := old(1), old(2), old(3), old(4)
	if err := st.withdraw(s, a, DayOf(t0)); !errors.Is(err, errPactNone) {
		t.Errorf("withdrawing nothing: %v", err)
	}
	p, err := st.post(s, guild, a, true, today)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []snowflake.ID{b, c} {
		if _, err := st.sign(s, guild, pactKey{opener: a, day: today}, u, today); err != nil {
			t.Fatal(err)
		}
	}
	p.rallied = t0
	if err := st.withdraw(s, c, DayOf(t0)); err != nil || s.members[c].pact != nil || len(p.signers) != 2 || s.members[a].pact != p || !p.rallied.IsZero() {
		t.Fatalf("a signer leaving before the work: %v %+v", err, p.signers)
	}
	if _, err := st.sign(s, guild, pactKey{opener: a, day: today}, c, today); err != nil {
		t.Fatalf("signing again after withdrawing: %v", err)
	}
	if err := st.withdraw(s, a, DayOf(t0)); err != nil || s.members[b].pact != nil || s.members[c].pact != nil {
		t.Fatal("the poster withdrawing left the notice up")
	}

	// After the work starts: four signed, one leaves and the ring goes on
	// without paying them; then one more leaves and it ends.
	next := today + 1
	at := dayStart(next).Add(time.Hour)
	p, _ = st.post(s, guild, b, true, next)
	for _, u := range []snowflake.ID{a, c, d} {
		if _, err := st.sign(s, guild, pactKey{opener: b, day: next}, u, next); err != nil {
			t.Fatal(err)
		}
	}
	at = together(st, s, at, 1, a, b, c, d)
	if err := st.withdraw(s, d, DayOf(t0)); err != nil || len(p.signers) != 3 || s.members[a].pact != p {
		t.Fatalf("one leaving a working ring: %v", err)
	}
	together(st, s, at, int(Default.PactGoal)-1, a, b, c)
	if s.members[d].signs.signatures != 0 || s.members[a].signs.signatures != 1 {
		t.Errorf("one who left was paid, or the rest weren't: %+v %+v", s.members[d].signs, s.members[a].signs)
	}
	if _, err := st.post(s, guild, d, false, next); err != nil {
		t.Fatal(err)
	}
	if _, err := st.sign(s, guild, pactKey{opener: d, day: next}, old(5), next); err != nil {
		t.Fatal(err)
	}
	together(st, s, at.Add(time.Hour), 1, d, old(5))
	if err := st.withdraw(s, old(5), DayOf(t0)); err != nil || s.members[d].pact != nil {
		t.Error("a chirograph left with one stayed")
	}
}

// The notices screen offers withdraw to whoever holds a notice, and the
// press takes their name off it and shows the board again.
func TestWithdrawButton(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(3)
	s.mu.Lock()
	if _, err := m.st.post(s, 3, 5, true, DayOf(t0)); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	e, sent := coretest.Button(t, "pp:notices", nil)
	if err := m.press(context.Background(), e); err != nil || !strings.Contains(js((*sent)[0]), "pp:withdraw") {
		t.Fatalf("no withdraw on the notices: %v", err)
	}
	e, _ = coretest.Button(t, "pp:withdraw", nil)
	var shown string
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		shown = js(d)
		return nil
	}
	if err := m.press(context.Background(), e); err != nil || s.members[5].pact != nil || strings.Contains(shown, "pp:withdraw") {
		t.Fatalf("withdrawing: %v", err)
	}
	if err := m.press(context.Background(), firstOf(coretest.Button(t, "pp:withdraw", nil))); !errors.Is(err, errPactNone) {
		t.Errorf("withdrawing twice: %v", err)
	}
}

// A fourth name fills a round robin and starts it; a chirograph starts with
// its second. Withdrawing as the poster gives back the day's post, twice a
// day, and the buttons say it as the city does.
func TestStartAndFreeWithdraws(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	today := DayOf(t0)
	us := []snowflake.ID{old(1), old(2), old(3), old(4)}
	p, _ := st.post(s, guild, us[0], true, today)
	for _, u := range us[1:] {
		if _, err := st.sign(s, guild, pactKey{opener: us[0], day: today}, u, today); err != nil {
			t.Fatal(err)
		}
	}
	if !p.started {
		t.Error("a full ring didn't start")
	}
	c, _ := st.post(s, guild, old(5), false, today)
	if _, err := st.sign(s, guild, pactKey{opener: old(5), day: today}, old(6), today); err != nil || !c.started {
		t.Errorf("a chirograph with two didn't start: %v", err)
	}
	// Post and tear up twice, the second once its work has started: the
	// post comes back each time. The third time it stays spent.
	b := old(7)
	for i := range 3 {
		if _, err := st.post(s, guild, b, false, today); err != nil {
			t.Fatalf("post %d: %v", i+1, err)
		}
		if i == 1 {
			if _, err := st.sign(s, guild, pactKey{opener: b, day: today}, old(8), today); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.withdraw(s, b, today); err != nil {
			t.Fatal(err)
		}
		if spent := s.members[b].signs.postedDay == today; spent != (i == 2) {
			t.Errorf("after withdrawal %d the post is spent: %v", i+1, spent)
		}
	}
}

// The notices screen offers its poster the start once three have signed,
// and names the way out as the city does.
func TestStartButton(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(3)
	today := DayOf(t0)
	s.mu.Lock()
	if _, err := m.st.post(s, 3, 5, true, today); err != nil {
		t.Fatal(err)
	}
	for _, u := range []snowflake.ID{old(2), old(3)} {
		if _, err := m.st.sign(s, 3, pactKey{opener: 5, day: today}, u, today); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Unlock()
	e, sent := coretest.Button(t, "pp:notices", nil)
	if err := m.press(context.Background(), e); err != nil || !strings.Contains(js((*sent)[0]), "pp:start") || !strings.Contains(js((*sent)[0]), "strike your name") {
		t.Fatalf("no start on the poster's notices: %v", err)
	}
	e, _ = coretest.Button(t, "pp:start", nil)
	e.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
		return nil
	}
	if err := m.press(context.Background(), e); err != nil || !s.members[5].pact.started {
		t.Fatalf("starting: %v", err)
	}
	if err := m.press(context.Background(), firstOf(coretest.Button(t, "pp:start", nil))); !errors.Is(err, errPactStart) {
		t.Errorf("started twice: %v", err)
	}
}

// A bird's second post in a day has its own key: the first one's post
// signs and starts nothing.
func TestKeysApart(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	today := DayOf(t0)
	a := old(1)
	first, _ := st.post(s, guild, a, false, today)
	old := first.key()
	if err := st.withdraw(s, a, today); err != nil {
		t.Fatal(err)
	}
	second, err := st.post(s, guild, a, true, today)
	if err != nil {
		t.Fatalf("posting again after taking one down: %v", err)
	}
	k, _ := parseKey(old)
	if second.key() == old || pactOf(s, k) != nil {
		t.Error("an old post's key names the new notice")
	}
	if k, ok := parseKey(second.key()); !ok || pactOf(s, k) != second {
		t.Error("a key doesn't name its own notice")
	}
	if k, ok := parseKey(a.String() + ":" + strconv.FormatInt(today, 10)); !ok || pactOf(s, k) != second {
		t.Error("a post from before keys had a time doesn't name its notice")
	}
}

// The review's edges: a ring its poster left is still found by its key; a
// sign as the day turns on a lapsing notice finds it gone rather than
// stranding the signer; a post says nothing of a board it doesn't show.
func TestReviewEdges(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	today := DayOf(t0)
	us := []snowflake.ID{old(1), old(2), old(3), old(4)}
	p, _ := st.post(s, guild, us[0], true, today)
	for _, u := range us[1:] {
		if _, err := st.sign(s, guild, pactKey{opener: us[0], day: today}, u, today); err != nil {
			t.Fatal(err)
		}
	}
	k, _ := parseKey(p.key())
	if err := st.withdraw(s, us[0], today); err != nil || pactOf(s, k) != p {
		t.Fatalf("a ring its poster left isn't found: %v", err)
	}

	// Last day: posted PactLife-1 days ago, and the sign comes as the day
	// turns it over.
	day := today - Default.PactLife + 1
	if _, err := st.post(s, guild, old(5), false, day); err != nil {
		t.Fatal(err)
	}
	st.rollServer(s, day)
	if _, err := st.sign(s, guild, pactKey{opener: old(5), day: day}, old(6), today+1); !errors.Is(err, errPactGone) || s.members[old(6)].pact != nil {
		t.Errorf("a sign onto a lapsing notice: %v, holds %v", err, s.members[old(6)].pact)
	}

	checkLayout(t, "a post", noticesCanvas("x", &Notice{Places: 2, Goal: 30, Signers: []NoticeSigner{{Name: "a"}}}, nil))
	m := testModule(&fakeDB{})
	if alt := m.noticesAlt(&Notice{Places: 2, Goal: 30, Signers: []NoticeSigner{{Name: "a", Mark: "x"}, {Name: "b", Mark: "y"}}}, nil); strings.Contains(alt, "board") {
		t.Errorf("a post's alt speaks of the board: %s", alt)
	}
	if andList([]string{"a", "b", "c"}) != "a, b and c" {
		t.Error("andList")
	}
}

// A poster who lost today's post leaving a started notice gets it back at
// boot; one still holding a notice, or already paid, does not.
func TestRegrant(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	today := DayOf(t0)
	b, held, paid := old(1), old(2), old(3)
	for _, u := range []snowflake.ID{b, held, paid} {
		if _, err := st.post(s, guild, u, false, today); err != nil {
			t.Fatal(err)
		}
	}
	s.members[b].pact = nil
	s.members[paid].pact, s.members[paid].signs.paidDay = nil, today
	st.regrant(today)
	if _, err := st.post(s, guild, b, false, today); err != nil {
		t.Errorf("the poster didn't get their post back: %v", err)
	}
	if s.members[held].signs.postedDay != today || s.members[paid].signs.postedDay != today {
		t.Error("a held or paid post came back")
	}
}
