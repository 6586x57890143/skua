package game

import "sync"

// Table is state per key, each behind its own lock, so a busy channel never
// waits on a quiet one and no lock is global.
type Table[K comparable, S any] struct{ m sync.Map }

type slot[S any] struct {
	mu   sync.Mutex
	s    S
	gone bool
}

// Do runs fn on k's state under its lock and reports whether k was there.
func (t *Table[K, S]) Do(k K, fn func(*S)) bool {
	for {
		v, ok := t.m.Load(k)
		if !ok {
			return false
		}
		sl := v.(*slot[S])
		sl.mu.Lock()
		if sl.gone {
			// Replaced or deleted while we waited: look again.
			sl.mu.Unlock()
			continue
		}
		fn(&sl.s)
		sl.mu.Unlock()
		return true
	}
}

// Upsert runs fn on k's state under its lock, making it first when k is
// new; fresh says which.
func (t *Table[K, S]) Upsert(k K, fn func(s *S, fresh bool)) {
	for {
		sl := &slot[S]{}
		sl.mu.Lock()
		if _, loaded := t.m.LoadOrStore(k, sl); !loaded {
			fn(&sl.s, true)
			sl.mu.Unlock()
			return
		}
		sl.mu.Unlock()
		if t.Do(k, func(s *S) { fn(s, false) }) {
			return
		}
	}
}

// Delete forgets k. A Do already waiting on it finds it gone.
func (t *Table[K, S]) Delete(k K) {
	v, ok := t.m.LoadAndDelete(k)
	if !ok {
		return
	}
	sl := v.(*slot[S])
	sl.mu.Lock()
	sl.gone = true
	sl.mu.Unlock()
}

// Get is a copy of k's state, read under its lock.
func (t *Table[K, S]) Get(k K) (S, bool) {
	var out S
	ok := t.Do(k, func(s *S) { out = *s })
	return out, ok
}

// Keys is every key the table holds right now.
func (t *Table[K, S]) Keys() []K {
	var out []K
	t.m.Range(func(k, _ any) bool {
		out = append(out, k.(K))
		return true
	})
	return out
}
