package purge

import (
	"context"
	"sync/atomic"
	"time"
)

// rate is how many sweep requests skua makes a second, across every sweep in
// the process. Discord allows about 50 per token; the rest is headroom for
// interaction replies and live deletes, which never wait here.
const rate = 40

// pacer hands out one request slot every step. The next free slot is a
// single int64 claimed with a compare-and-swap, as guard's cells are: no
// mutex, no allocation, no goroutine refilling anything. A caller sleeps
// until its own slot, so concurrent sweeps interleave fairly instead of
// bursting into Discord's global limit and being 429ed for a minute.
//
// disgo's REST client already waits out each route's bucket, one request
// in flight per bucket. It only reacts to the global limit after a 429.
// Pacing beforehand is the part it leaves to the caller.
type pacer struct {
	next atomic.Int64 // unix nanos
	step int64
	now  func() time.Time
}

func newPacer(perSecond int) *pacer {
	return &pacer{step: int64(time.Second) / int64(perSecond), now: time.Now}
}

// wait blocks until the caller's slot, or until ctx ends. A slot given up
// to a cancelled ctx is not handed back; at 25 ms each that is noise.
func (p *pacer) wait(ctx context.Context) error {
	now := p.now().UnixNano()
	for {
		n := p.next.Load()
		slot := max(n, now)
		if !p.next.CompareAndSwap(n, slot+p.step) {
			continue
		}
		if slot <= now {
			return ctx.Err()
		}
		t := time.NewTimer(time.Duration(slot - now))
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
}
