package purge

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
)

// clock is a now a test moves by hand.
type clock struct{ t atomic.Int64 }

func (c *clock) now() time.Time      { return time.Unix(0, c.t.Load()) }
func (c *clock) add(d time.Duration) { c.t.Add(int64(d)) }
func newClock(at time.Time) *clock   { c := &clock{}; c.t.Store(at.UnixNano()); return c }
func (f *fake) lastButtons() (n int) { f.mu.Lock(); defer f.mu.Unlock(); return f.buttons }
func (f *fake) lastToken() (tok string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens[len(f.tokens)-1]
}

// watchUntilHandoff runs a readout of a purge of me's, watched by viewer,
// up to the moment its token is nearly spent, and returns how it ended.
func watchUntilHandoff(t *testing.T, m *Module, f *fake, j *job, viewer snowflake.ID) string {
	t.Helper()
	m.tick, m.dmTick = 2*time.Millisecond, 2*time.Millisecond
	c := newClock(time.Now())
	m.now = c.now
	stopped := make(chan struct{})
	go func() { m.watch(j, f, 2, "first", me, viewer); close(stopped) }()
	u := <-f.updates
	if !strings.Contains(u, "still going") || f.lastButtons() != 0 {
		t.Fatalf("first readout %q with %d button rows", u, f.lastButtons())
	}
	c.add(tokenLife - m.tick)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the readout kept going past its token")
	}
	for {
		select {
		case u = <-f.updates:
		default:
			return u
		}
	}
}

// A readout about to lose its token carries on in a DM to whoever watched
// it, counting until the purge ends.
func TestReadoutMovesToADM(t *testing.T) {
	m := newModule()
	f := newFake()
	j := m.newJob(f, guildID, []snowflake.ID{me})
	last := watchUntilHandoff(t, m, f, j, 7) // the break-glass admin watching
	if !strings.Contains(last, "the rest is in your dms") || f.lastButtons() != 0 {
		t.Fatalf("last readout %q with %d button rows", last, f.lastButtons())
	}
	if first := <-f.dms; !strings.Contains(first, "still going") || !strings.Contains(first, "https://discord.com/channels/3") {
		t.Fatalf("first dm %q", first)
	}
	<-f.dms // edited as it goes
	j.err = nil
	close(j.done)
	for d := <-f.dms; !strings.Contains(d, "done in"); d = <-f.dms {
	}
	if f.dmTo[0] != 7 {
		t.Errorf("dmed %v, want the one watching", f.dmTo)
	}
}

// A member skua can't DM is left the progress button instead.
func TestReadoutWithoutDMsOffersProgress(t *testing.T) {
	m := newModule()
	f := newFake()
	f.dmErr = refusal(403, rest.JSONErrorCodeCannotSendMessagesToThisUser)
	j := m.newJob(f, guildID, []snowflake.ID{me})
	last := watchUntilHandoff(t, m, f, j, me)
	if !strings.Contains(last, "skua can't dm you") || f.lastButtons() != 1 {
		t.Fatalf("last readout %q with %d button rows", last, f.lastButtons())
	}
	close(j.done)

	// A spent send budget is the same as closed DMs.
	f = newFake()
	m = newModule()
	for m.guard.Allow(guildID, guard.MessageSend) == nil {
	}
	j = m.newJob(f, guildID, []snowflake.ID{me})
	if last := watchUntilHandoff(t, m, f, j, me); f.lastButtons() != 1 {
		t.Fatalf("with no send budget: %q with %d button rows", last, f.lastButtons())
	}
	close(j.done)
}

// Pressing progress opens a readout on the press's own token, which ends
// with the purge, its button gone.
func TestProgressOpensAFreshReadout(t *testing.T) {
	m := newModule()
	m.tick = 2 * time.Millisecond
	f := newFake()
	j := m.newJob(f, guildID, []snowflake.ID{me})
	m.running.Store(target{guildID, me}, &run{func() {}, j, false})

	e, sent := coretest.Button(t, fmt.Sprintf("%s:%d", progressButton, me), nil)
	e.Client().Rest = f
	if err := m.progress(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 || (*sent)[0].Content != "" || !(*sent)[0].Flags.Has(discord.MessageFlagEphemeral) {
		t.Fatalf("answered %+v, want an ephemeral deferral", *sent)
	}
	<-f.updates
	if f.lastToken() != "t" || f.lastButtons() != 0 {
		t.Fatalf("readout on token %q with %d button rows", f.lastToken(), f.lastButtons())
	}
	j.err = nil
	close(j.done)
	for u := <-f.updates; !strings.Contains(u, "done in"); u = <-f.updates {
	}
	if f.lastButtons() != 0 {
		t.Error("the finished readout kept its button")
	}
}

func TestProgressRefuses(t *testing.T) {
	m := newModule()
	for _, tc := range []struct{ id, want string }{
		{progressButton, "that purge is over"},
		{progressButton + ":6", string(errNotYours)},
		{progressButton + ":x", "out of date"},
	} {
		e, _ := coretest.Button(t, tc.id, nil)
		err := m.progress(context.Background(), e)
		if tell, ok := err.(core.Tell); !ok || !strings.Contains(string(tell), tc.want) {
			t.Errorf("%s: %v, want %q", tc.id, err, tc.want)
		}
	}
}
