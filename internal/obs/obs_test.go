package obs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/metrics"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
)

// Every duration lands in the bucket whose bounds hold it.
func TestBucketsHoldTheirDurations(t *testing.T) {
	for _, d := range []int64{0, 1, 2, 3, 4, 5, 7, 8, 1000, 1023, 1024, 1500, 999_999, int64(time.Second), int64(time.Hour)} {
		i := bucket(d)
		if lo, hi := int64(lower(i)), int64(lower(i+1)); d < lo || d >= hi {
			t.Errorf("%d in bucket %d [%d, %d)", d, i, lo, hi)
		}
	}
}

func TestQuantilesAndRows(t *testing.T) {
	r := New()
	for range 98 {
		r.Add("whisper", Run, time.Millisecond)
	}
	r.Add("whisper", Run, time.Second)
	r.Add("whisper", Run, 2*time.Second)
	r.Add("bird", HTTP, 300*time.Millisecond)
	rows := r.Rows()
	if len(rows) != 2 || rows[0].Who != "whisper" || rows[1].Who != "bird" {
		t.Fatalf("rows %+v, want whisper's p99 first", rows)
	}
	w := rows[0]
	near := func(got, want time.Duration) bool { return got > want*3/4 && got < want*5/4 }
	if w.N != 100 || !near(w.P50, time.Millisecond) || !near(w.P99, time.Second) || w.Max != 2*time.Second {
		t.Fatalf("whisper %+v", w)
	}
	if New().Rows() == nil || len(New().Rows()) != 0 {
		t.Fatal("an empty recorder has rows")
	}
	if (&hist{}).quantile(0.5) != 0 {
		t.Fatal("an empty histogram has a quantile")
	}
}

func TestStageNames(t *testing.T) {
	for s, want := range map[Stage]string{In: "in", Run: "run", Wait: "wait", HTTP: "http"} {
		if s.String() != want {
			t.Errorf("%d is %q", s, s.String())
		}
	}
}

func TestModuleRidesTheContext(t *testing.T) {
	if Module(context.Background()) != "" || Module(With(context.Background(), "purge")) != "purge" {
		t.Fatal("module did not ride the context")
	}
}

func TestListenTimesTheListener(t *testing.T) {
	r := New()
	got := 0
	Listen(r, "preen", bot.NewListenerFunc(func(*events.Ready) { got++ })).OnEvent(&events.Ready{})
	if rows := r.Rows(); got != 1 || len(rows) != 1 || rows[0].Who != "preen" || rows[0].Stage != Run {
		t.Fatalf("got %d, rows %+v", got, rows)
	}
}

type fakeLimiter struct {
	rest.RateLimiter
	waitErr  error
	unlocked int
}

func (f *fakeLimiter) Wait(context.Context, *rest.CompiledEndpoint) error {
	time.Sleep(time.Millisecond)
	return f.waitErr
}

func (f *fakeLimiter) Unlock(*rest.CompiledEndpoint, *http.Response) error {
	f.unlocked++
	return nil
}

func TestLimiterSplitsWaitFromTheRoundTrip(t *testing.T) {
	r := New()
	inner := &fakeLimiter{}
	l := Limiter(inner, r)
	react := &rest.CompiledEndpoint{Endpoint: &rest.Endpoint{Method: http.MethodPut, Route: "/channels/{channel.id}/messages/{message.id}/reactions/{emoji}/@me"}}
	post := &rest.CompiledEndpoint{Endpoint: &rest.Endpoint{Method: http.MethodPost, Route: "/channels/{channel.id}/messages"}}

	if err := l.Wait(With(context.Background(), "whisper"), post); err != nil {
		t.Fatal(err)
	}
	_ = l.Unlock(post, nil)
	_ = l.Wait(context.Background(), react)
	_ = l.Unlock(react, nil)

	inner.waitErr = errors.New("ctx done")
	if err := l.Wait(context.Background(), react); err == nil {
		t.Fatal("a failed wait passed")
	}
	_ = l.Unlock(react, nil) // nothing in flight: no http sample

	seen := map[string]uint64{}
	for _, row := range r.Rows() {
		seen[row.Who+" "+row.Stage.String()] = row.N
	}
	want := map[string]uint64{"whisper wait": 1, "whisper http": 1, "put reactions/@me wait": 2, "put reactions/@me http": 1}
	for k, n := range want {
		if seen[k] != n {
			t.Errorf("%s: %d samples, want %d (all %v)", k, seen[k], n, seen)
		}
	}
	if inner.unlocked != 3 {
		t.Errorf("inner unlocked %d times", inner.unlocked)
	}
}

func TestRoute(t *testing.T) {
	for route, want := range map[string]string{
		"/channels/{channel.id}/messages":                                    "post channels/messages",
		"/webhooks/{webhook.id}/{webhook.token}":                             "post webhooks",
		"/channels/{channel.id}/messages/{message.id}/reactions/{emoji}/@me": "post reactions/@me",
	} {
		if got := Route(&rest.Endpoint{Method: "POST", Route: route}); got != want {
			t.Errorf("%s: %q, want %q", route, got, want)
		}
	}
}

func TestRuntime(t *testing.T) {
	runtime.GC()
	sched, gc := Runtime()
	if sched < 0 || gc <= 0 {
		t.Fatalf("sched %v, gc %v after a GC", sched, gc)
	}
	if p99(metrics.Value{}) != 0 {
		t.Fatal("a value that is not a histogram has a p99")
	}
}

func TestFlightServesATrace(t *testing.T) {
	h, err := Flight()
	if err != nil {
		t.Fatal(err)
	}
	Region(context.Background(), "status", func() { time.Sleep(10 * time.Millisecond) })
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/debug/skua/flight", nil))
	if w.Body.Len() == 0 {
		t.Fatal("no trace written")
	}
	if _, err := Flight(); err == nil {
		t.Fatal("a second flight recorder started")
	}
}

// A sample is always on, so it has to cost nothing measurable: no
// allocation once its histogram exists.
func TestAddDoesNotAllocate(t *testing.T) {
	r := New()
	r.Add("whisper", Run, time.Millisecond)
	if n := testing.AllocsPerRun(1000, func() { r.Add("whisper", Run, time.Millisecond) }); n != 0 {
		t.Fatalf("Add allocates %v times", n)
	}
}

func BenchmarkAdd(b *testing.B) {
	r := New()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		r.Add("whisper", Run, time.Duration(i))
	}
}

// Every module recording at once, as under load.
func BenchmarkAddParallel(b *testing.B) {
	r := New()
	mods := []string{"status", "whisper", "bird", "purge", "preen", "help", "perf"}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for i := 0; pb.Next(); i++ {
			r.Add(mods[i%len(mods)], Stage(i%4), time.Duration(i))
		}
	})
}
