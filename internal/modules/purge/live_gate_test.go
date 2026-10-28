package purge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

func TestLiveGateWithdrawsQueuedDeletes(t *testing.T) {
	m, f := liveModule(), newFake()
	now := time.Now()
	m.now = func() time.Time { return now }
	var enabled atomic.Bool
	enabled.Store(true)
	m.Gate(func(snowflake.ID) bool { return enabled.Load() })
	m.enqueue(f, guildID, textCh, msg{900, me}, now.Add(time.Hour))
	m.enqueue(f, guildID, textCh, msg{901, me}, now.Add(3*time.Hour))
	m.mu.Lock()
	m.queues[textCh].timer.Stop()
	m.mu.Unlock()
	enabled.Store(false)
	now = now.Add(2 * time.Hour)
	m.flush(textCh)
	if bulk, single := f.state(); len(bulk)+len(single) != 0 {
		t.Fatalf("deleted after module disable: bulk=%v single=%v", bulk, single)
	}
	m.mu.Lock()
	remaining := len(m.queues)
	m.mu.Unlock()
	if remaining != 0 {
		t.Fatal("disabled queue was retained")
	}
	enabled.Store(true)
	m.flush(textCh)
	if bulk, single := f.state(); len(bulk)+len(single) != 0 {
		t.Fatal("reenabling revived revoked deletes")
	}
}

type gateDeleteRest struct {
	*fake
	afterBulk func()
}

func (r *gateDeleteRest) BulkDeleteMessages(ch snowflake.ID, ids []snowflake.ID, opts ...rest.RequestOpt) error {
	err := r.fake.BulkDeleteMessages(ch, ids, opts...)
	r.afterBulk()
	return err
}

func TestLiveGateStopsBetweenWrites(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "chunks", true: "fallback"}[fallback], func(t *testing.T) {
			m, f := liveModule(), newFake()
			var enabled atomic.Bool
			enabled.Store(true)
			m.Gate(func(snowflake.ID) bool { return enabled.Load() })
			r := &gateDeleteRest{fake: f, afterBulk: func() { enabled.Store(false) }}
			ids := make([]msg, bulkMax+1)
			first := snowflake.New(time.Now())
			for i := range ids {
				ids[i] = msg{first + snowflake.ID(i), me}
			}
			if fallback {
				f.bulkErr = refusal(400, 50034)
			}
			m.remove(r, guildID, textCh, ids)
			bulk, single := f.state()
			if len(single) != 0 {
				t.Fatalf("new singles started after disable: %v", single)
			}
			if !fallback && len(bulk) != 1 {
				t.Fatalf("bulks=%v", bulk)
			}
		})
	}
}

func TestJobGateStopsWritesAfterStart(t *testing.T) {
	m, f := liveModule(), newFake()
	var enabled atomic.Bool
	enabled.Store(true)
	m.Gate(func(snowflake.ID) bool { return enabled.Load() })
	j := m.newJob(f, guildID, []snowflake.ID{me}, "test purge for")
	enabled.Store(false)
	if err := j.sweep.bulk(context.Background(), textCh, []msg{{900, me}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("disabled sweep returned %v", err)
	}
	if bulk, single := f.state(); len(bulk)+len(single) != 0 {
		t.Fatal("job started a delete after module disable")
	}
}
