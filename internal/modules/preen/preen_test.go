package preen

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

// fakeRest records every call in order. Each add takes pace, standing in
// for Discord's per-channel reaction bucket; addErr fails every add.
type fakeRest struct {
	rest.Rest
	mu     sync.Mutex
	calls  []string
	pace   time.Duration
	addErr error
	done   chan struct{}
}

func (f *fakeRest) record(c string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *fakeRest) RemoveUserReaction(_, _ snowflake.ID, emoji string, _ snowflake.ID, _ ...rest.RequestOpt) error {
	f.record("-" + emoji)
	if f.done != nil {
		close(f.done)
	}
	return nil
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

func inOrder(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

// calls is r's calls sorted, since they land in any order.
func (f *fakeRest) sorted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.calls))
}

func twelveAndOff() []string {
	want := []string{"-🔥"}
	for _, b := range flock[:birds] {
		want = append(want, "+"+b)
	}
	return slices.Sorted(slices.Values(want))
}

// A self-react brings exactly twelve birds and the removal, and preen is
// done with the message once they are.
func TestSelfReactBringsTwelveAndComesOff(t *testing.T) {
	r := &fakeRest{}
	m := New(guard.New())
	m.order = inOrder
	m.OnEvent(reaction(r, 7, 7, false))
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, busy := m.busy.Load(snowflake.ID(99)); !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("preen never finished")
		}
	}
	if got := r.sorted(); !slices.Equal(got, twelveAndOff()) {
		t.Fatalf("calls %v, want %v", got, twelveAndOff())
	}
}

// Every call goes out at once: thirteen calls that each take 200ms are done
// in about 200ms, where one after another they would take 2.6s.
func TestEveryCallGoesOutAtOnce(t *testing.T) {
	r := &fakeRest{pace: 200 * time.Millisecond}
	m := New(guard.New())
	start := time.Now()
	m.preen(r, 3, 4, 99, 7, "🔥")
	if took := time.Since(start); took > time.Second {
		t.Fatalf("took %v; the calls ran in turn", took)
	}
	if len(r.sorted()) != birds+1 {
		t.Fatalf("calls %v", r.calls)
	}
}

// The birds are drawn at random, so two bursts are not always the same.
func TestBurstsDiffer(t *testing.T) {
	m := New(guard.New())
	first := m.order(len(flock))
	for range 20 {
		if !slices.Equal(m.order(len(flock)), first) {
			return
		}
	}
	t.Fatal("twenty draws in a row came out the same")
}

func TestOthersAreLeftAlone(t *testing.T) {
	r := &fakeRest{}
	m := New(guard.New())
	m.OnEvent(reaction(r, 7, 8, false)) // someone else's post
	m.OnEvent(reaction(r, 7, 7, true))  // a bot
	noAuthor := reaction(r, 7, 7, false)
	noAuthor.MessageAuthorID = nil
	m.OnEvent(noAuthor)
	m.OnEvent(&events.GuildReady{})
	m.busy.Store(snowflake.ID(99), struct{}{}) // already preening
	m.OnEvent(reaction(r, 7, 7, false))
	time.Sleep(50 * time.Millisecond)
	if len(r.calls) != 0 {
		t.Fatalf("touched %v", r.calls)
	}
}

// A failed bird drops the rest from disgo's queue, which a fake has none of;
// whatever the birds do, the self-react still comes off.
func TestAFailedBirdDoesNotStopTheRemoval(t *testing.T) {
	r := &fakeRest{addErr: &rest.Error{Response: &http.Response{StatusCode: 503}}}
	m := New(guard.New())
	m.preen(r, 3, 4, 99, 7, "🔥")
	if got := r.sorted(); !slices.Contains(got, "-🔥") {
		t.Fatalf("calls %v, want the removal among them", got)
	}
	if _, busy := m.busy.Load(snowflake.ID(99)); busy {
		t.Fatal("the message stayed busy")
	}
}

// A call dropped at its deadline stops the burst but tells the breaker
// nothing, so it can neither trip it nor reset its count.
func TestADroppedCallIsNotReported(t *testing.T) {
	g := guard.New()
	m := New(g)
	for range 4 {
		m.spend(3, &rest.Error{Response: &http.Response{StatusCode: 503}})
	}
	if m.spend(3, context.DeadlineExceeded) || m.spend(3, context.Canceled) {
		t.Fatal("a dropped call let the burst go on")
	}
	m.spend(3, &rest.Error{Response: &http.Response{StatusCode: 503}})
	if g.Allow(3, guard.Reaction) == nil {
		t.Fatal("a dropped call reset the breaker's count")
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

func TestModule(t *testing.T) {
	m := New(guard.New())
	if m.Name() != "preen" || m.Commands() != nil || m.Want().Required == 0 || m.Perms() == 0 || m.Help().Line == "" || len(flock) > 20 {
		t.Fatal("module surface changed")
	}
}
