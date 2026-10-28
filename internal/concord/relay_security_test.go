// SPDX-License-Identifier: AGPL-3.0-only
package concord

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestPoolQueryOverflowDoesNotReturnPartialHistory(t *testing.T) {
	r := newFakeRelay(t, false)
	k, _ := KeyFromSecret([32]byte{14})
	r.events = []*nostr.Event{signed(t, k, 1, "one"), signed(t, k, 1, "two"), signed(t, k, 1, "three")}
	p := NewPool([]string{r.url()})
	p.queryEvents = 2
	defer p.Close()
	got, err := p.QueryChecked(context.Background(), nostr.Filter{Kinds: []int{1}})
	if !errors.Is(err, ErrQueryLimit) || got != nil {
		t.Fatalf("partial history: %v, %v", got, err)
	}
	if got := p.Query(context.Background(), nostr.Filter{Kinds: []int{1}}); got != nil {
		t.Fatal("legacy query returned partial history")
	}
}

func TestQueryBudgetCountsBytesAndDeduplicates(t *testing.T) {
	k, _ := KeyFromSecret([32]byte{15})
	one, two := signed(t, k, 1, "one"), signed(t, k, 1, "two")
	raw, _ := json.Marshal(one)
	set := newEventSet(3, len(raw))
	if err := set.add(one); err != nil {
		t.Fatal(err)
	}
	if err := set.add(one); err != nil {
		t.Fatal("duplicate spent budget", err)
	}
	if err := set.add(two); !errors.Is(err, ErrQueryLimit) {
		t.Fatal(err)
	}
	if len(set.events) != 1 || set.bytes != len(raw) {
		t.Fatalf("overflow changed retained history: %+v", set)
	}
}

func TestPoolMergedQueryBudget(t *testing.T) {
	k, _ := KeyFromSecret([32]byte{16})
	one := unfilteredRelay(t, []*nostr.Event{signed(t, k, 1, "one"), signed(t, k, 1, "two")})
	two := unfilteredRelay(t, []*nostr.Event{signed(t, k, 1, "three")})
	p := NewPool([]string{one, two})
	p.queryEvents = 2
	defer p.Close()
	got, err := p.QueryChecked(context.Background(), nostr.Filter{Kinds: []int{1}})
	if !errors.Is(err, ErrQueryLimit) || got != nil {
		t.Fatalf("merged query returned partial history: %v %v", got, err)
	}
}

func TestPoolExplicitZeroLimit(t *testing.T) {
	p := NewPool([]string{"ws://127.0.0.1:1"})
	defer p.Close()
	got, err := p.QueryChecked(context.Background(), nostr.Filter{LimitZero: true})
	if err != nil || len(got) != 0 {
		t.Fatalf("zero limit attempted query: %v %v", got, err)
	}
}

func TestSubscriptionCancellationReleasesBlockedDelivery(t *testing.T) {
	sub := &subscription{frames: make(chan frame, 1), done: make(chan struct{})}
	sub.frames <- frame{eose: true}
	c := &conn{subs: map[string]*subscription{"s1": sub}, done: make(chan struct{})}
	finished := make(chan struct{})
	go func() { c.deliver("s1", frame{eose: true}); close(finished) }()
	close(sub.done)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("orphan subscription blocked socket reader")
	}
}
