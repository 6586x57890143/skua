// Package obs times every module from the metal to Discord's API, in four
// stages: in (Discord creating an interaction to skua dispatching it, which
// is the gateway, decoding and the scheduler), run (the module's handler),
// wait (queued behind Discord's rate limit) and http (the round trip), plus
// drop, a call whose ctx ended while it was still queued. The router
// records in and run; Limiter wraps disgo's rate limiter for wait, http and
// drop. Runtime adds what the Go runtime itself costs, and Flight keeps
// the last seconds of execution trace, with a region per module, for when
// a number alone does not say why.
//
// A sample costs one read lock, a map lookup and three atomic adds, and
// allocates nothing (BenchmarkAdd), so it is always on.
package obs

import (
	"context"
	"math"
	"math/bits"
	"net/http"
	"runtime/metrics"
	"runtime/trace"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/rest"
)

// Stage is one leg of a call's way from the gateway to the API and back.
type Stage uint8

const (
	In Stage = iota
	Run
	Wait
	HTTP
	// Drop is a call that never left the queue: its ctx ended in Wait. Kept
	// apart from Wait, so a deadline doesn't read as Discord being slow.
	Drop
)

func (s Stage) String() string { return [...]string{"in", "run", "wait", "http", "drop"}[s] }

// buckets are half octaves of nanoseconds: bucket i holds durations whose
// top two bits put them in [2^(i/2), 2^(i/2+1)), so a quantile is good to
// within about 20%, which is enough to say which part is slow.
const buckets = 128

type hist struct {
	b   [buckets]atomic.Uint64
	n   atomic.Uint64
	max atomic.Int64
}

func bucket(ns int64) int {
	if ns < 2 {
		return int(max(ns, 0))
	}
	top := bits.Len64(uint64(ns)) - 1
	half := int(uint64(ns)>>(top-1)) & 1
	return min(2*top+half, buckets-1)
}

// lower is the smallest duration bucket i holds.
func lower(i int) time.Duration {
	if i < 2 {
		return time.Duration(i)
	}
	top := i / 2
	return time.Duration(1<<top + (i%2)<<(top-1))
}

func (h *hist) add(d time.Duration) {
	h.b[bucket(int64(d))].Add(1)
	h.n.Add(1)
	for m := h.max.Load(); int64(d) > m && !h.max.CompareAndSwap(m, int64(d)); m = h.max.Load() {
	}
}

// quantile is the midpoint of the bucket holding q of the samples.
func (h *hist) quantile(q float64) time.Duration {
	n := h.n.Load()
	if n == 0 {
		return 0
	}
	rank := uint64(math.Ceil(q * float64(n)))
	var seen uint64
	for i := range buckets {
		if seen += h.b[i].Load(); seen >= rank {
			return min((lower(i)+lower(i+1))/2, time.Duration(h.max.Load()))
		}
	}
	return time.Duration(h.max.Load())
}

type key struct {
	who   string
	stage Stage
}

// Recorder is safe for concurrent use. The zero value is not; use New.
type Recorder struct {
	mu sync.RWMutex
	h  map[key]*hist
}

func New() *Recorder { return &Recorder{h: map[key]*hist{}} }

// Default is the process's recorder.
var Default = New()

// Add records that who spent d in stage.
func (r *Recorder) Add(who string, s Stage, d time.Duration) {
	k := key{who, s}
	r.mu.RLock()
	h := r.h[k]
	r.mu.RUnlock()
	if h == nil {
		r.mu.Lock()
		if h = r.h[k]; h == nil {
			h = new(hist)
			r.h[k] = h
		}
		r.mu.Unlock()
	}
	h.add(d)
}

// Row is one module or route's stage since boot.
type Row struct {
	Who      string
	Stage    Stage
	N        uint64
	P50, P99 time.Duration
	Max      time.Duration
}

// Rows is every stage seen, slowest p99 first.
func (r *Recorder) Rows() []Row {
	r.mu.RLock()
	rows := make([]Row, 0, len(r.h))
	for k, h := range r.h {
		rows = append(rows, Row{k.who, k.stage, h.n.Load(), h.quantile(0.5), h.quantile(0.99), time.Duration(h.max.Load())})
	}
	r.mu.RUnlock()
	slices.SortFunc(rows, func(a, b Row) int {
		if a.P99 != b.P99 {
			return int(b.P99 - a.P99)
		}
		return strings.Compare(a.Who+a.Stage.String(), b.Who+b.Stage.String())
	})
	return rows
}

type moduleKey struct{}

// With marks ctx as module's, so the REST calls made with it are counted
// against that module rather than their route.
func With(ctx context.Context, module string) context.Context {
	return context.WithValue(ctx, moduleKey{}, module)
}

// Module is who ctx belongs to, or "".
func Module(ctx context.Context) string {
	m, _ := ctx.Value(moduleKey{}).(string)
	return m
}

// Region times fn as a region named module in the execution trace, so a
// flight recording shows which module a goroutine was in. It costs next to
// nothing while no trace is being taken.
func Region(ctx context.Context, module string, fn func()) { trace.WithRegion(ctx, module, fn) }

// Listen times l's handling of every event as module's run, in a trace
// region named for it. A listener that hands its work to a goroutine is
// timed only up to the handoff; its REST calls still show as wait and
// http, against their route.
func Listen(rec *Recorder, module string, l bot.EventListener) bot.EventListener {
	return bot.NewListenerFunc(func(ev bot.Event) {
		start := time.Now()
		Region(context.Background(), module, func() { l.OnEvent(ev) })
		rec.Add(module, Run, time.Since(start))
	})
}

// limiter times disgo's rate limiter: Wait is the queue, and Wait returning
// to Unlock is the request itself. disgo holds a bucket from Wait to
// Unlock, so the endpoint pointer pairs the two.
type limiter struct {
	rest.RateLimiter
	rec      *Recorder
	inFlight sync.Map // *rest.CompiledEndpoint -> sent
}

type sent struct {
	who string
	at  time.Time
}

// Limiter wraps inner, recording wait and http into rec. A call made with
// a ctx from With counts against its module; any other against its route.
func Limiter(inner rest.RateLimiter, rec *Recorder) rest.RateLimiter {
	return &limiter{RateLimiter: inner, rec: rec}
}

func (l *limiter) Wait(ctx context.Context, ep *rest.CompiledEndpoint) error {
	start := time.Now()
	err := l.RateLimiter.Wait(ctx, ep)
	who := Module(ctx)
	if who == "" {
		who = Route(ep.Endpoint)
	}
	if err != nil {
		l.rec.Add(who, Drop, time.Since(start))
		return err
	}
	l.rec.Add(who, Wait, time.Since(start))
	l.inFlight.Store(ep, sent{who, time.Now()})
	return nil
}

func (l *limiter) Unlock(ep *rest.CompiledEndpoint, rs *http.Response) error {
	if v, ok := l.inFlight.LoadAndDelete(ep); ok {
		s := v.(sent)
		l.rec.Add(s.who, HTTP, time.Since(s.at))
	}
	return l.RateLimiter.Unlock(ep, rs)
}

// Route is a short name for an endpoint: its method and the last two fixed
// parts of its path, "put reactions/@me" or "post channels/messages".
func Route(e *rest.Endpoint) string {
	var fixed []string
	for p := range strings.SplitSeq(e.Route, "/") {
		if p != "" && !strings.HasPrefix(p, "{") {
			fixed = append(fixed, p)
		}
	}
	return strings.ToLower(e.Method) + " " + strings.Join(fixed[max(0, len(fixed)-2):], "/")
}

// Runtime is what the Go runtime itself costs: the p99 of how long a
// runnable goroutine waits to run, and of GC pauses, since boot.
func Runtime() (sched, gc time.Duration) {
	s := []metrics.Sample{{Name: "/sched/latencies:seconds"}, {Name: "/sched/pauses/total/gc:seconds"}}
	metrics.Read(s)
	return p99(s[0].Value), p99(s[1].Value)
}

func p99(v metrics.Value) time.Duration {
	if v.Kind() != metrics.KindFloat64Histogram {
		return 0
	}
	h := v.Float64Histogram()
	var n uint64
	for _, c := range h.Counts {
		n += c
	}
	rank := uint64(math.Ceil(0.99 * float64(n)))
	var seen uint64
	for i, c := range h.Counts {
		if seen += c; n > 0 && seen >= rank {
			hi := h.Buckets[i+1]
			if math.IsInf(hi, 1) {
				hi = h.Buckets[i]
			}
			return time.Duration(hi * float64(time.Second))
		}
	}
	return 0
}

// Flight starts a flight recorder holding about the last ten seconds of
// execution trace, and returns a handler that writes it out for go tool
// trace. Only one can run in a process.
func Flight() (http.HandlerFunc, error) {
	fr := trace.NewFlightRecorder(trace.FlightRecorderConfig{MinAge: 10 * time.Second})
	if err := fr.Start(); err != nil {
		return nil, err
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="skua.trace"`)
		_, _ = fr.WriteTo(w)
	}, nil
}
