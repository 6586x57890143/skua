package guard

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func fixed(t *time.Time) func() time.Time { return func() time.Time { return *t } }

func TestCapAndRefill(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := New()
	g.now = fixed(&now)

	for i := range caps[ChannelEdit] {
		if err := g.Allow(1, ChannelEdit); err != nil {
			t.Fatalf("write %d of the burst refused: %v", i, err)
		}
	}
	if err := g.Allow(1, ChannelEdit); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over cap: got %v, want ErrRateLimited", err)
	}
	if err := g.Allow(2, ChannelEdit); err != nil {
		t.Fatalf("another guild was throttled: %v", err)
	}
	if err := g.Allow(1, MessageSend); err != nil {
		t.Fatalf("another op was throttled: %v", err)
	}

	now = now.Add(window / time.Duration(caps[ChannelEdit]))
	if err := g.Allow(1, ChannelEdit); err != nil {
		t.Fatalf("one step later still refused: %v", err)
	}
	if err := g.Allow(1, ChannelEdit); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("refill gave more than one step: %v", err)
	}
}

func TestConcurrentNeverExceedsCap(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := New()
	g.now = fixed(&now)

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 1000 {
		wg.Go(func() {
			if g.Allow(7, MessageSend) == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if ok != int(caps[MessageSend]) {
		t.Fatalf("allowed %d concurrent writes, want exactly %d", ok, caps[MessageSend])
	}
}

func TestBreaker(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := New()
	g.now = fixed(&now)

	for range tripAfter - 1 {
		g.Report(1, true)
	}
	g.Report(1, false) // a success resets the streak
	for range tripAfter - 1 {
		g.Report(1, true)
	}
	if err := g.Allow(1, MessageSend); err != nil {
		t.Fatalf("breaker opened before %d consecutive failures: %v", tripAfter, err)
	}
	g.Report(1, true)
	if err := g.Allow(1, MessageSend); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("got %v, want ErrCircuitOpen", err)
	}
	if err := g.Allow(2, MessageSend); err != nil {
		t.Fatalf("breaker leaked to another guild: %v", err)
	}
	now = now.Add(coolDown + time.Nanosecond)
	if err := g.Allow(1, MessageSend); err != nil {
		t.Fatalf("breaker still open after cool-down: %v", err)
	}
}

func BenchmarkAllow(b *testing.B) {
	g := New()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = g.Allow(1, MessageSend)
		}
	})
}

// Purge and PurgeMember are separate budgets: a member out of /purge now
// starts leaves the guild's running sweeps alone.
func TestPurgeBudgetsAreIndependent(t *testing.T) {
	g := New()
	for range caps[PurgeMember] {
		if err := g.Allow(5, PurgeMember); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.Allow(5, PurgeMember); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("start %d: got %v, want ErrRateLimited", caps[PurgeMember]+1, err)
	}
	if err := g.Allow(5, Purge); err != nil {
		t.Fatalf("Purge refused after PurgeMember ran out: %v", err)
	}
}

func TestStepIsTheWindowOverTheCap(t *testing.T) {
	if Step(CommandSync) != window/8 {
		t.Fatalf("Step(CommandSync) = %v", Step(CommandSync))
	}
}
