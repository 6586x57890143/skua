package game

import (
	"context"
	"sync"
	"time"
)

// Dirty is the write-behind every game uses: the hot path marks what
// changed, and Run hands the marked keys to a flush every interval, so a
// burst of moves costs one write per key, not one per move.
type Dirty[K comparable] struct {
	mu   sync.Mutex
	keys map[K]struct{}
}

// Mark records that k changed.
func (d *Dirty[K]) Mark(k K) {
	d.mu.Lock()
	if d.keys == nil {
		d.keys = map[K]struct{}{}
	}
	d.keys[k] = struct{}{}
	d.mu.Unlock()
}

// Take is every marked key, unmarking them.
func (d *Dirty[K]) Take() []K {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]K, 0, len(d.keys))
	for k := range d.keys {
		out = append(out, k)
	}
	clear(d.keys)
	return out
}

// Run flushes the marked keys every interval until ctx ends, then once more
// with up to last to finish, so a shutdown keeps what was played. A flush
// that fails marks its keys again for the next turn and goes to failed.
func (d *Dirty[K]) Run(ctx context.Context, every, last time.Duration, flush func(context.Context, []K) error, failed func(error)) {
	t := time.NewTicker(every)
	defer t.Stop()
	turn := func(ctx context.Context) {
		keys := d.Take()
		if len(keys) == 0 {
			return
		}
		if err := flush(ctx, keys); err != nil {
			for _, k := range keys {
				d.Mark(k)
			}
			failed(err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			end, cancel := context.WithTimeout(context.WithoutCancel(ctx), last)
			turn(end)
			cancel()
			return
		case <-t.C:
			turn(ctx)
		}
	}
}
