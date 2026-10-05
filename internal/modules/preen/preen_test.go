package preen

import (
	"errors"
	"net/http"
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

type fakeRest struct {
	rest.Rest
	mu      sync.Mutex
	removed []string
	added   []string
	err     error
	done    chan struct{}
}

func (f *fakeRest) RemoveUserReaction(_, _ snowflake.ID, emoji string, _ snowflake.ID, _ ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, emoji)
	return f.err
}

func (f *fakeRest) AddReaction(_, _ snowflake.ID, emoji string, _ ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, emoji)
	if len(f.added) == len(flock) && f.done != nil {
		close(f.done)
	}
	return nil
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

func TestSelfReactBringsTheFlock(t *testing.T) {
	r := &fakeRest{done: make(chan struct{})}
	m := New(guard.New())
	m.OnEvent(reaction(r, 7, 7, false))
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the flock never arrived")
	}
	if len(r.removed) != 1 || r.removed[0] != "🔥" || len(flock) > 20 {
		t.Fatalf("removed %v, flock of %d", r.removed, len(flock))
	}
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
	if len(r.removed)+len(r.added) != 0 {
		t.Fatalf("touched %v %v", r.removed, r.added)
	}
}

func TestAFailedRemovalStopsTheFlock(t *testing.T) {
	r := &fakeRest{err: &rest.Error{Response: &http.Response{StatusCode: 503}}}
	m := New(guard.New())
	m.preen(r, 3, 4, 99, 7, "🔥")
	if len(r.added) != 0 {
		t.Fatalf("added %v after a failed removal", r.added)
	}
	if _, busy := m.busy.Load(snowflake.ID(99)); busy {
		t.Fatal("the message stayed busy")
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
	if m.Name() != "preen" || m.Commands() != nil || m.Want().Required == 0 || m.Perms() == 0 || m.Help().Line == "" {
		t.Fatal("module surface changed")
	}
}
