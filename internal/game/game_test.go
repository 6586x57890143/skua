package game

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLinkIsStableAndUnambiguous(t *testing.T) {
	a := Link(Hash{}, "ab", "c")
	if a != Link(Hash{}, "ab", "c") {
		t.Fatal("same fields, different hash")
	}
	// Length prefixes keep "ab","c" apart from "a","bc".
	if a == Link(Hash{}, "a", "bc") {
		t.Fatal("field boundaries collide")
	}
	if Link(Hash{}, int64(1)) != Link(Hash{}, uint64(1)) || Link(Hash{}, 1) != Link(Hash{}, int64(1)) {
		t.Fatal("integers of one value hash differently")
	}
	if Link(Hash{}, []byte("x")) != Link(Hash{}, "x") {
		t.Fatal("bytes and string differ")
	}
	if Genesis("d", 1) == Genesis("e", 1) {
		t.Fatal("domains collide")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a float should panic")
		}
	}()
	Link(Hash{}, 1.5)
}

func TestHashText(t *testing.T) {
	h := Genesis("x")
	b, _ := h.MarshalText()
	var back Hash
	if err := back.UnmarshalText(b); err != nil || back != h {
		t.Fatalf("round trip: %v %v", back, err)
	}
	if len(h.Short()) != 8 || h.Short() != h.String()[:8] {
		t.Fatal("short")
	}
	for _, bad := range []string{"zz", "abcd", ""} {
		if _, err := ParseHash(bad); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
}

func TestTable(t *testing.T) {
	var tb Table[int, int]
	if tb.Do(1, func(*int) {}) {
		t.Fatal("empty table did something")
	}
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for range 100 {
				tb.Upsert(1, func(s *int, _ bool) { *s++ })
			}
		})
	}
	wg.Wait()
	if v, _ := tb.Get(1); v != 5000 {
		t.Fatalf("lost updates: %d", v)
	}
	fresh := 0
	tb.Upsert(2, func(_ *int, f bool) {
		if f {
			fresh++
		}
	})
	tb.Upsert(2, func(_ *int, f bool) {
		if f {
			fresh++
		}
	})
	if fresh != 1 {
		t.Fatalf("fresh %d times", fresh)
	}
	keys := tb.Keys()
	slices.Sort(keys)
	if !slices.Equal(keys, []int{1, 2}) {
		t.Fatalf("keys %v", keys)
	}
	tb.Delete(1)
	tb.Delete(1)
	if _, ok := tb.Get(1); ok {
		t.Fatal("deleted key still there")
	}
}

// A Do that waits on a key deleted meanwhile finds it gone, and one that
// waits on a key replaced finds the new one.
func TestTableDeleteWhileWaiting(t *testing.T) {
	var tb Table[int, int]
	tb.Upsert(1, func(s *int, _ bool) { *s = 1 })
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 200 {
				tb.Do(1, func(s *int) { *s++ })
				tb.Delete(1)
				tb.Upsert(1, func(*int, bool) {})
			}
		})
	}
	wg.Wait()
}

func TestDirty(t *testing.T) {
	var d Dirty[string]
	d.Mark("a")
	d.Mark("a")
	d.Mark("b")
	got := d.Take()
	slices.Sort(got)
	if !slices.Equal(got, []string{"a", "b"}) || len(d.Take()) != 0 {
		t.Fatalf("take %v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	var flushed sync.Map
	var failures atomic.Int32
	done := make(chan struct{})
	go func() {
		d.Run(ctx, time.Millisecond, time.Second, func(ctx context.Context, keys []string) error {
			if calls.Add(1) == 1 {
				return errors.New("db down")
			}
			for _, k := range keys {
				flushed.Store(k, ctx.Err() == nil)
			}
			return nil
		}, func(error) { failures.Add(1) })
		close(done)
	}()
	d.Mark("x")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := flushed.Load("x"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a failed flush was not retried")
		}
		time.Sleep(time.Millisecond)
	}
	if failures.Load() != 1 {
		t.Fatalf("failures %d", failures.Load())
	}
	// What is marked at shutdown is flushed on a live context.
	d.Mark("y")
	cancel()
	<-done
	if live, ok := flushed.Load("y"); !ok || !live.(bool) {
		t.Fatal("the last flush was lost or ran on a dead context")
	}
}

func TestRankAndTop(t *testing.T) {
	b := Rank(map[string]int64{"a": 5, "b": 9, "c": 5, "d": 1, "z": 0})
	want := []Standing[string]{{"b", 9, 1}, {"a", 5, 2}, {"c", 5, 2}, {"d", 1, 4}}
	if !slices.Equal(b, want) {
		t.Fatalf("rank %v", b)
	}
	if top := Top(b, "d", 2); !slices.Equal(top, []Standing[string]{{"b", 9, 1}, {"d", 1, 4}}) {
		t.Fatalf("top with me below %v", top)
	}
	if top := Top(b, "b", 2); !slices.Equal(top, b[:2]) {
		t.Fatalf("top with me in it %v", top)
	}
	if top := Top(b, "q", 2); !slices.Equal(top, b[:2]) {
		t.Fatalf("top with me absent %v", top)
	}
	if top := Top(b, "a", 10); len(top) != 4 {
		t.Fatal("short board")
	}
}

func TestPeriods(t *testing.T) {
	at := time.Date(2026, 11, 26, 23, 59, 0, 0, time.FixedZone("x", -5*3600)) // 27th UTC
	s, e, k := Month(at)
	if k != "2026-11" || !s.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !e.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("month %v %v %s", s, e, k)
	}
	s, e, k = Week(at)
	if k != "2026-W48" || s.Weekday() != time.Monday || !s.Equal(time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)) || e.Sub(s) != 7*24*time.Hour {
		t.Fatalf("week %v %v %s", s, e, k)
	}
	// A Sunday is the end of its week, and the ISO year can differ.
	if _, _, k := Week(time.Date(2027, 1, 3, 12, 0, 0, 0, time.UTC)); k != "2026-W53" {
		t.Fatalf("iso year %s", k)
	}
}

func TestCurve(t *testing.T) {
	c := Curve{Base: 100}
	for xp, want := range map[int64]int{0: 0, 99: 0, 100: 1, 399: 1, 400: 2, 899: 2, 900: 3, 1_000_000: 100} {
		if got := c.Level(xp); got != want {
			t.Fatalf("level(%d) = %d, want %d", xp, got, want)
		}
	}
	for l := range 2000 {
		if c.Level(c.Need(l)) != l || (l > 0 && c.Level(c.Need(l)-1) != l-1) {
			t.Fatalf("edge of level %d", l)
		}
	}
	if (Curve{}).Level(50) != 0 {
		t.Fatal("zero curve")
	}
}

func TestCombo(t *testing.T) {
	var c Combo
	t0 := time.Unix(1000, 0)
	if c.Hit(t0, 10*time.Second) != 1 {
		t.Fatal("first hit")
	}
	if got := c.Now(t0.Add(10*time.Second), 10*time.Second); got < 0.36 || got > 0.37 {
		t.Fatalf("decay %v", got)
	}
	// An out of order hit adds without decaying or moving time back.
	c.Hit(t0.Add(-time.Second), 10*time.Second)
	if c.Score != 2 || !c.At.Equal(t0) {
		t.Fatalf("late hit %v", c)
	}
	steady := Combo{}
	for i := range 200 {
		steady.Hit(t0.Add(time.Duration(i)*time.Second), 10*time.Second)
	}
	if steady.Score < 10 || steady.Score > 11 || Tier(steady.Score, 4, 9, 16) != 2 {
		t.Fatalf("steady combo %v", steady.Score)
	}
	if Tier(3, 4, 9, 16) != 0 || Tier(16, 4, 9, 16) != 3 {
		t.Fatal("tier")
	}
}
