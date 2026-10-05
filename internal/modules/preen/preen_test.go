package preen

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/obs"
)

// fakeRest records every call. Each add takes pace; addErr fails every add,
// and removeErrs are the removals' answers in turn, nil once they run out.
type fakeRest struct {
	rest.Rest
	mu         sync.Mutex
	calls      []string
	pace       time.Duration
	addErr     error
	removeErrs []error
}

func (f *fakeRest) record(c string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *fakeRest) sorted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.calls))
}

func (f *fakeRest) RemoveUserReaction(_, _ snowflake.ID, emoji string, _ snowflake.ID, _ ...rest.RequestOpt) error {
	f.record("-" + emoji)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.removeErrs) == 0 {
		return nil
	}
	err := f.removeErrs[0]
	f.removeErrs = f.removeErrs[1:]
	return err
}

func (f *fakeRest) inOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func status(code int) error { return &rest.Error{Response: &http.Response{StatusCode: code}} }

// answer runs one self-react to the end, as OnEvent's goroutine would.
func answer(m *Module, r rest.Rest) {
	m.removing.Add(1)
	m.react(r, 3, 4, 99, 7, "🔥")
}

func (f *fakeRest) AddReaction(_, _ snowflake.ID, emoji string, _ ...rest.RequestOpt) error {
	time.Sleep(f.pace)
	f.record("+" + emoji)
	return f.addErr
}

func reaction(r rest.Rest, user, author snowflake.ID, isBot bool) *events.GuildMessageReactionAdd {
	c := &bot.Client{Rest: r}
	return &events.GuildMessageReactionAdd{
		GenericGuildMessageReaction: &events.GenericGuildMessageReaction{
			GenericEvent: events.NewGenericEvent(c, 0, 0),
			UserID:       user, ChannelID: 4, MessageID: 99, GuildID: 3,
			Emoji: discord.PartialEmoji{Name: new("🔥")},
		},
		Member:          discord.Member{User: discord.User{ID: user, Bot: isBot}},
		MessageAuthorID: &author,
	}
}

func flockAndOff() []string {
	want := []string{"-🔥"}
	for _, b := range Flock[:Birds] {
		want = append(want, "+"+b)
	}
	return slices.Sorted(slices.Values(want))
}

// A self-react brings Birds birds and comes off, and the fill's time
// reaches /perf under the strategy that made it.
func TestSelfReactBringsTheWholeFlock(t *testing.T) {
	r := &fakeRest{}
	rec := obs.New()
	m := New(guard.New(), rec, nil)
	m.order = inOrder
	m.OnEvent(reaction(r, 7, 7, false))
	for deadline := time.Now().Add(5 * time.Second); len(r.sorted()) < Birds+1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("preen never finished")
		}
	}
	if got := r.sorted(); !slices.Equal(got, flockAndOff()) {
		t.Fatalf("calls %v, want %v", got, flockAndOff())
	}
	rows := rec.Rows()
	if len(rows) != 1 || rows[0].Who != "preen fill" || rows[0].N != 1 {
		t.Fatalf("perf rows %+v, want one fill", rows)
	}
}

// Each strategy puts every bird up exactly once, and keeps no more than its
// share of calls in flight.
func TestStrategiesFillOnceWithinTheirWidth(t *testing.T) {
	for _, s := range Strategies {
		var inFlight, peak atomic.Int32
		var mu sync.Mutex
		var got []string
		err := s.Fill(t.Context(), Flock, func(_ context.Context, b string) error {
			n := inFlight.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			time.Sleep(2 * time.Millisecond)
			inFlight.Add(-1)
			mu.Lock()
			got = append(got, b)
			mu.Unlock()
			return nil
		})
		if err != nil || !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(Flock))) {
			t.Errorf("%s: err %v, birds %v", s.Name, err, got)
		}
		if want := map[string]int32{"serial": 1, "pairs": 2, "quads": 4}[s.Name]; want != 0 && peak.Load() > want {
			t.Errorf("%s: %d in flight at once", s.Name, peak.Load())
		}
	}
	if peak := func() int32 {
		var inFlight, peak atomic.Int32
		_ = Strategies[len(Strategies)-1].Fill(t.Context(), Flock, func(context.Context, string) error {
			n := inFlight.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
			return nil
		})
		return peak.Load()
	}(); peak < 10 {
		t.Errorf("burst had only %d in flight at once", peak)
	}
}

// The first failure stops the strategy starting birds, and is what it returns.
func TestAFailedBirdStopsTheFill(t *testing.T) {
	boom := errors.New("boom")
	for _, s := range Strategies {
		var started atomic.Int32
		err := s.Fill(t.Context(), Flock, func(context.Context, string) error {
			started.Add(1)
			return boom
		})
		if !errors.Is(err, boom) {
			t.Errorf("%s: returned %v", s.Name, err)
		}
		if n := started.Load(); n > int32(len(Flock))/2 && s.Name != "burst" {
			t.Errorf("%s: started %d birds after the first failed", s.Name, n)
		}
	}
}

// A failed fill is not timed, the self-react still comes off, and the
// message is forgotten so the next self-react on it tries again.
func TestAFailedFillIsNotTimedAndStillComesOff(t *testing.T) {
	r := &fakeRest{addErr: status(503)}
	rec := obs.New()
	m := New(guard.New(), rec, nil)
	answer(m, r)
	if got := r.inOrder(); len(got) == 0 || got[0] != "-🔥" {
		t.Fatalf("calls %v, want the removal first", got)
	}
	if rows := rec.Rows(); len(rows) != 0 {
		t.Fatalf("a failed fill was timed: %+v", rows)
	}
	if !m.flocked.add(99) {
		t.Fatal("a failed fill was remembered, so the message would never get birds")
	}
}

// The removal goes before any bird, so a fill cut short never leaves it.
func TestTheRemovalComesFirst(t *testing.T) {
	r := &fakeRest{}
	m := New(guard.New(), obs.New(), nil)
	m.order = inOrder
	answer(m, r)
	if got := r.inOrder(); len(got) != Birds+1 || got[0] != "-🔥" {
		t.Fatalf("calls %v, want the removal then %d birds", got, Birds)
	}
}

// The guard holds back birds, never the removal: with the breaker open the
// self-react still comes off.
func TestTheRemovalIgnoresTheGuard(t *testing.T) {
	g := guard.New()
	for range 5 {
		g.Report(3, true)
	}
	if g.Allow(3, guard.Reaction) == nil {
		t.Fatal("the breaker didn't open")
	}
	r := &fakeRest{}
	m := New(g, obs.New(), nil)
	answer(m, r)
	if got := r.inOrder(); !slices.Equal(got, []string{"-🔥"}) {
		t.Fatalf("calls %v, want just the removal", got)
	}
}

// A removal is tried again while Discord is the trouble (429, 5xx, no
// answer), and not when the request is (a 4xx).
func TestTheRemovalRetriesOnlyDiscordsTrouble(t *testing.T) {
	for name, c := range map[string]struct {
		errs  []error
		tries int
	}{
		"429, 503, no answer, then done": {[]error{status(429), status(503), errors.New("reset")}, 4},
		"403 is final":                   {[]error{status(403)}, 1},
		"404 is final":                   {[]error{status(404)}, 1},
	} {
		r := &fakeRest{removeErrs: c.errs, addErr: status(403)}
		m := New(guard.New(), obs.New(), nil)
		m.retry = time.Millisecond
		answer(m, r)
		if n := len(slices.DeleteFunc(r.inOrder(), func(s string) bool { return s != "-🔥" })); n != c.tries {
			t.Errorf("%s: %d tries, want %d", name, n, c.tries)
		}
	}
}

// Drain waits for a self-react already taken, its removal and its fill,
// and says so when its time runs out first.
func TestDrainWaitsForOwedWork(t *testing.T) {
	r := &fakeRest{pace: 10 * time.Millisecond}
	m := New(guard.New(), obs.New(), nil)
	m.OnEvent(reaction(r, 7, 7, false))
	if m.Drain(time.Millisecond) {
		t.Fatal("drained before a fill that takes 150ms")
	}
	if !m.Drain(5 * time.Second) {
		t.Fatal("never drained")
	}
	if n := len(r.inOrder()); n != Birds+1 {
		t.Fatalf("%d calls when drained, want the removal and %d birds", n, Birds)
	}
}

func TestOthersAreLeftAlone(t *testing.T) {
	r := &fakeRest{}
	m := New(guard.New(), obs.New(), nil)
	m.OnEvent(reaction(r, 7, 8, false)) // someone else's post
	m.OnEvent(reaction(r, 7, 7, true))  // a bot
	noAuthor := reaction(r, 7, 7, false)
	noAuthor.MessageAuthorID = nil
	m.OnEvent(noAuthor)
	m.OnEvent(&events.GuildReady{})
	time.Sleep(50 * time.Millisecond)
	if len(r.calls) != 0 {
		t.Fatalf("touched %v", r.calls)
	}
}

// A call cut off by a deadline or a sibling's failure tells the breaker
// nothing, so it can neither trip it nor reset its count.
func TestACutOffCallIsNotReported(t *testing.T) {
	g := guard.New()
	m := New(g, obs.New(), nil)
	for range 4 {
		m.report(3, &rest.Error{Response: &http.Response{StatusCode: 503}})
	}
	m.report(3, context.DeadlineExceeded)
	m.report(3, context.Canceled)
	m.report(3, &rest.Error{Response: &http.Response{StatusCode: 503}})
	if g.Allow(3, guard.Reaction) == nil {
		t.Fatal("a cut-off call reset the breaker's count")
	}
}

func TestStruggling(t *testing.T) {
	for err, want := range map[error]bool{
		nil:             false,
		errors.New("x"): false,
		&rest.Error{}:   false,
		&rest.Error{Response: &http.Response{StatusCode: 429}}: true,
		&rest.Error{Response: &http.Response{StatusCode: 403}}: false,
	} {
		if struggling(err) != want {
			t.Errorf("struggling(%v) = %v", err, !want)
		}
	}
}

func inOrder(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

// Every fill draws its birds afresh, so posts don't all wear the same ones.
func TestBirdsAreDrawnAtRandom(t *testing.T) {
	m := New(guard.New(), obs.New(), nil)
	first := m.order(len(Flock))[:Birds]
	for range 20 {
		if !slices.Equal(m.order(len(Flock))[:Birds], first) {
			return
		}
	}
	t.Fatal("twenty draws in a row came out the same")
}

func TestModule(t *testing.T) {
	m := New(guard.New(), obs.New(), nil)
	if m.Name() != "preen" || m.Commands() != nil || m.Want().Required == 0 || m.Perms() == 0 || m.Help().Line == "" || len(Flock) != 20 || Birds > len(Flock) {
		t.Fatal("module surface changed")
	}
}

// A message that already has its flock only loses the self-react, however
// many times its author reacts again.
func TestASecondSelfReactOnlyComesOff(t *testing.T) {
	r := &fakeRest{}
	m := New(guard.New(), obs.New(), nil)
	m.flocked.add(99)
	m.OnEvent(reaction(r, 7, 7, false))
	for deadline := time.Now().Add(5 * time.Second); len(r.sorted()) < 1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the self-react never came off")
		}
	}
	time.Sleep(20 * time.Millisecond)
	if got := r.sorted(); !slices.Equal(got, []string{"-🔥"}) {
		t.Fatalf("calls %v, want only the removal", got)
	}
}

func TestFlockedForgetsTheOldest(t *testing.T) {
	f := newFlocked(2)
	if !f.add(1) || !f.add(2) || f.add(1) || !f.add(3) || !f.add(1) || f.add(3) {
		t.Fatal("not a bounded set, oldest out first")
	}
}

// A message forgotten and added again lives in its new slot: its old slot
// coming round must not drop it.
func TestFlockedKeepsAReAddedMessage(t *testing.T) {
	f := newFlocked(3)
	f.add(1) // slot 0
	f.forget(1)
	f.add(1) // slot 1
	f.add(2) // slot 2
	f.add(3) // slot 0 comes round: 1 lives in slot 1, so it stays
	if f.add(1) {
		t.Fatal("the old slot dropped a message living in a newer one")
	}
	if f.add(2) || f.add(3) {
		t.Fatal("lost a message still in its slot")
	}
}

// A self-react that arrives after Drain has begun is turned away, without
// a race on the count or a call.
func TestASelfReactDuringDrainIsTurnedAway(t *testing.T) {
	r := &fakeRest{}
	m := New(guard.New(), obs.New(), nil)
	drained := make(chan bool)
	go func() { drained <- m.Drain(time.Second) }()
	for {
		m.mu.Lock()
		d := m.draining
		m.mu.Unlock()
		if d {
			break
		}
		time.Sleep(time.Millisecond)
	}
	m.OnEvent(reaction(r, 7, 7, false))
	if !<-drained {
		t.Fatal("drain timed out")
	}
	time.Sleep(20 * time.Millisecond)
	if got := r.inOrder(); len(got) != 0 {
		t.Fatalf("a self-react after the drain began made calls %v", got)
	}
}

// whispers says who wrote what.
type whispers map[snowflake.ID]snowflake.ID

func (w whispers) Wrote(message, user snowflake.ID) bool { return w[message] == user }

func whisperReaction(r rest.Rest, user snowflake.ID) *events.GuildMessageReactionAdd {
	return reaction(r, user, 600, false) // 600, the webhook, is the author
}

// A member reacting to their own whisper gets the flock; reacting to
// someone else's, or without whispers to ask, gets nothing.
func TestAWhispersWriterGetsTheFlock(t *testing.T) {
	r := &fakeRest{}
	m := New(guard.New(), obs.New(), whispers{99: 7})
	m.order = inOrder
	m.OnEvent(whisperReaction(r, 7))
	for deadline := time.Now().Add(5 * time.Second); len(r.sorted()) < Birds+1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the writer got %v", r.sorted())
		}
	}
	if got := r.sorted(); !slices.Equal(got, flockAndOff()) {
		t.Fatalf("calls %v, want %v", got, flockAndOff())
	}

	for name, m := range map[string]*Module{
		"someone else's whisper": New(guard.New(), obs.New(), whispers{99: 8}),
		"no whispers":            New(guard.New(), obs.New(), nil),
	} {
		r := &fakeRest{}
		m.OnEvent(whisperReaction(r, 7))
		time.Sleep(50 * time.Millisecond)
		if len(r.sorted()) != 0 {
			t.Errorf("%s: touched %v", name, r.sorted())
		}
	}
}
