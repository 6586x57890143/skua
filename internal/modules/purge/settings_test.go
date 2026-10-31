package purge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/jackc/pgx/v5/pgconn"
)

type settingsDB struct {
	DB
	mu               sync.Mutex
	secs             int32
	fail             error
	started, release chan struct{}
}

func (d *settingsDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	d.mu.Lock()
	if d.fail != nil {
		d.mu.Unlock()
		return pgconn.CommandTag{}, d.fail
	}
	secs := int32(0)
	if len(args) > 2 {
		if value, ok := args[2].(*int32); ok && value != nil {
			secs = *value
		}
	}
	d.secs = secs
	d.mu.Unlock()
	if secs > 0 && d.started != nil {
		close(d.started)
		<-d.release
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func TestStopWithdrawsLiveAndQueuedDeletes(t *testing.T) {
	m, f := liveModule(), newFake()
	k := target{guildID, me}
	m.setLive(k, time.Hour, nil)
	v, _ := m.live.Load(k)
	version := v.(liveSet).version
	m.enqueueLive(f, guildID, textCh, msg{900, me}, time.Now().Add(time.Hour), version)
	m.enqueue(f, guildID, textCh, msg{901, them}, time.Now().Add(time.Hour))
	if got := command(t, m, "stop", nil); !strings.Contains(got, "live and every are off") {
		t.Fatal(got)
	}
	if _, on := m.live.Load(k); on {
		t.Fatal("live still enabled")
	}
	// A channel lookup started before stop may finish afterwards.
	m.enqueueLive(f, guildID, textCh, msg{902, me}, time.Now(), version)
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queues[textCh]
	defer q.timer.Stop()
	if len(q.items) != 1 || q.items[0].author != them {
		t.Fatalf("remaining queue: %+v", q.items)
	}
}

func TestLiveSettingAndOffAreSerialized(t *testing.T) {
	m := liveModule()
	d := &settingsDB{started: make(chan struct{}), release: make(chan struct{})}
	m.db = d
	a, _ := coretest.Event(t, "purge", nil)
	b, _ := coretest.Event(t, "purge", nil)
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- m.setLiveCmd(t.Context(), a, target{3, 5}, "1m", nil) }()
	<-d.started
	go func() { second <- m.setLiveCmd(t.Context(), b, target{3, 5}, "off", nil) }()
	select {
	case err := <-second:
		close(d.release)
		<-first
		t.Fatalf("off overtook the pending setting: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(d.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if _, on := m.live.Load(target{3, 5}); on || d.secs != 0 {
		t.Fatal("database and memory disagree about off")
	}
}

func TestFailedStopPreservesLiveSetting(t *testing.T) {
	m := liveModule()
	m.db = &settingsDB{fail: errors.New("database unavailable")}
	k := target{guildID, me}
	m.setLive(k, time.Hour, nil)
	if got := command(t, m, "stop", nil); !strings.Contains(got, "something went wrong") {
		t.Fatal(got)
	}
	if _, ok := m.live.Load(k); !ok {
		t.Fatal("memory changed after persistence failed")
	}
}

func TestStopPersistsBothSubscriptionsOff(t *testing.T) {
	db := testDB(t)
	m := liveModule()
	m.useDB(db)
	g := freshGuild()
	liveCmd(t, m, "1m", g)
	subCmd(t, m, "every", "every", "1d", g)
	if got := subCmd(t, m, "stop", "", "", g); !strings.Contains(got, "live and every are off") {
		t.Fatal(got)
	}
	got := subCmd(t, m, "status", "", "", g)
	for _, want := range []string{"live off", "every off", "next sweep none"} {
		if !strings.Contains(oneSpace(got), want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	again := liveModule()
	again.useDB(db)
	if err := again.loadLive(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, on := again.live.Load(target{g, me}); on {
		t.Fatal("restart restored stopped live mode")
	}
}
