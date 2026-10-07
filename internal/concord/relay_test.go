// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
)

// fakeRelay is just enough of a NIP-01 relay to test the pool against:
// stored events, live subscriptions, OK answers, and NIP-42 gating, where
// a REQ for an author needs an AUTH as that author first.
type fakeRelay struct {
	*httptest.Server
	gated bool

	mu     sync.Mutex
	events []*nostr.Event
	subs   map[*websocket.Conn]map[string]nostr.Filter
	authed map[*websocket.Conn]map[string]bool
	conns  []*websocket.Conn
}

func newFakeRelay(t *testing.T, gated bool) *fakeRelay {
	r := &fakeRelay{gated: gated, subs: map[*websocket.Conn]map[string]nostr.Filter{}, authed: map[*websocket.Conn]map[string]bool{}}
	up := websocket.Upgrader{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c, err := up.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		r.mu.Lock()
		r.subs[c], r.authed[c] = map[string]nostr.Filter{}, map[string]bool{}
		r.conns = append(r.conns, c)
		r.mu.Unlock()
		r.send(c, "AUTH", "challenge")
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				return
			}
			var msg []json.RawMessage
			if json.Unmarshal(raw, &msg) != nil || len(msg) < 2 {
				continue
			}
			var verb string
			_ = json.Unmarshal(msg[0], &verb)
			r.handle(c, verb, msg[1:])
		}
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *fakeRelay) url() string { return "ws" + strings.TrimPrefix(r.URL, "http") }

func (r *fakeRelay) send(c *websocket.Conn, v ...any) {
	raw, _ := json.Marshal(v)
	_ = c.WriteMessage(websocket.TextMessage, raw)
}

func (r *fakeRelay) handle(c *websocket.Conn, verb string, args []json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch verb {
	case "AUTH":
		var ev nostr.Event
		if json.Unmarshal(args[0], &ev) == nil && validEvent(&ev) && Tag(tagsOf(&ev), "challenge") == "challenge" {
			r.authed[c][ev.PubKey] = true
		}
		r.send(c, "OK", ev.ID, true, "")
	case "EVENT":
		var ev nostr.Event
		_ = json.Unmarshal(args[0], &ev)
		if r.gated && !r.authed[c][ev.PubKey] {
			r.send(c, "OK", ev.ID, false, "auth-required: publish as yourself")
			return
		}
		r.events = append(r.events, &ev)
		r.send(c, "OK", ev.ID, true, "")
		for other, subs := range r.subs {
			for id, f := range subs {
				if f.Matches(&ev) {
					r.send(other, "EVENT", id, ev)
				}
			}
		}
	case "REQ":
		var id string
		var f nostr.Filter
		_ = json.Unmarshal(args[0], &id)
		_ = json.Unmarshal(args[1], &f)
		if r.gated {
			for _, a := range f.Authors {
				if !r.authed[c][a] {
					r.send(c, "CLOSED", id, "auth-required: these streams")
					return
				}
			}
		}
		r.subs[c][id] = f
		for _, ev := range r.events {
			if f.Matches(ev) {
				r.send(c, "EVENT", id, ev)
			}
		}
		r.send(c, "EOSE", id)
	case "CLOSE":
		var id string
		_ = json.Unmarshal(args[0], &id)
		delete(r.subs[c], id)
	}
}

// drop closes every socket, as a relay restarting would.
func (r *fakeRelay) drop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.conns {
		_ = c.Close()
	}
	r.conns = nil
}

func signed(t *testing.T, k Key, kind int, content string) *nostr.Event {
	t.Helper()
	ev := &nostr.Event{Kind: kind, Content: content, CreatedAt: nostr.Now(), Tags: nostr.Tags{}}
	if err := ev.Sign(k.SK); err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestPoolAuthPublishQuerySubscribe(t *testing.T) {
	gated, open := newFakeRelay(t, true), newFakeRelay(t, false)
	stream, _ := KeyFromSecret([32]byte{9})
	p := NewPool([]string{gated.url(), open.url()})
	defer p.Close()
	p.Register(stream.Stream(), StreamKey{PK: "unsigned"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got := make(chan *nostr.Event, 8)
	p.Subscribe(ctx, nostr.Filter{Kinds: []int{1}, Authors: []string{stream.PK}}, func(ev *nostr.Event) { got <- ev })

	ev := signed(t, stream, 1, "one")
	if err := p.Publish(ctx, ev); err != nil {
		t.Fatal(err)
	}
	// Both relays end up holding it: the gated one after an AUTH.
	waitFor(t, func() bool { return len(gated.stored()) == 1 && len(open.stored()) == 1 })
	if q := p.Query(ctx, nostr.Filter{Kinds: []int{1}, Authors: []string{stream.PK}}); len(q) != 1 || q[0].ID != ev.ID {
		t.Fatalf("query %v", q)
	}
	select {
	case e := <-got:
		if e.ID != ev.ID {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("no live event")
	}

	// A dropped socket is reconnected and the REQ asked again.
	gated.drop()
	open.drop()
	time.Sleep(100 * time.Millisecond)
	two := signed(t, stream, 1, "two")
	waitFor(t, func() bool { return p.Publish(ctx, two) == nil })
	deadline := time.After(15 * time.Second)
	for {
		select {
		case e := <-got:
			if e.ID == two.ID {
				return
			}
		case <-deadline:
			t.Fatal("no event after reconnect")
		}
	}
}

func TestPoolFailures(t *testing.T) {
	ctx := context.Background()
	p := NewPool([]string{"ws://127.0.0.1:1"})
	k, _ := KeyFromSecret([32]byte{3})
	if err := p.Publish(ctx, signed(t, k, 1, "x")); err == nil {
		t.Error("published to nothing")
	}
	if q := p.Query(ctx, nostr.Filter{Kinds: []int{1}}); len(q) != 0 {
		t.Error(q)
	}
	p.Close()
	if _, err := p.relay(ctx, "ws://127.0.0.1:1"); err == nil {
		t.Error("closed pool connected")
	}
}

func (r *fakeRelay) stored() []*nostr.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*nostr.Event{}, r.events...)
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestResolve(t *testing.T) {
	u := load(t)
	r := newFakeRelay(t, false)
	p := NewPool([]string{r.url()})
	defer p.Close()
	ctx := context.Background()
	inv, _ := ParseInvite(u.Invite.URL)
	if _, err := Resolve(ctx, inv, p); err == nil {
		t.Error("resolved with no bundle")
	}
	if err := p.Publish(ctx, u.Invite.Event); err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(ctx, inv, p)
	if err != nil || c.IDHex != u.Community.ID {
		t.Fatalf("%v %v", c, err)
	}
	if ControlStream(c).PK == "" || GuestbookKey(c).SK == "" {
		t.Error("keys")
	}
	j, err := Join(c, u.Community.Members["x"], strings.Repeat("04", 32), 1)
	if err != nil || j.Kind != KindWrap {
		t.Fatal(err)
	}
	if o, err := Open(j, GuestbookKey(c)); err != nil || o.Kind != KindJoinLeave || o.Content != "join" {
		t.Fatalf("%v %v", o, err)
	}
}

// A relay that stops answering is dropped once pongWait passes, so the pool
// dials again; one that answers pings is kept.
func TestSilentRelayIsDropped(t *testing.T) {
	// Upgrades, then never reads: pings go unanswered, as on a dead link.
	up := websocket.Upgrader{}
	hold := make(chan struct{})
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := up.Upgrade(w, r, nil); err == nil {
			<-hold
			_ = c.Close()
		}
	}))
	defer silent.Close()
	defer close(hold)
	healthy := newFakeRelay(t, false)

	silentURL := "ws" + strings.TrimPrefix(silent.URL, "http")
	p := NewPool([]string{silentURL, healthy.url(), "ws://127.0.0.1:1"})
	p.pingEvery, p.pongWait = 50*time.Millisecond, 300*time.Millisecond
	defer p.Close()
	ctx := context.Background()
	dead, err := p.relay(ctx, "ws"+strings.TrimPrefix(silent.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	alive, err := p.relay(ctx, healthy.url())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, dead.isDone)
	time.Sleep(2 * p.pongWait)
	if alive.isDone() {
		t.Fatal("a relay answering pings was dropped")
	}
	// Health says which is which: the silent one down with one drop, the
	// healthy one up and heard within the wait, the never-dialled one zero.
	h := p.Health()
	if len(h) != 3 || h[0].Up || h[0].Drops != 1 || !h[1].Up || h[1].Drops != 0 || time.Since(h[1].Heard) > p.pongWait || h[2].Up || !h[2].Heard.IsZero() {
		t.Fatalf("health %+v", h)
	}
	// The next use of the dead relay's URL dials a fresh socket.
	again, err := p.relay(ctx, "ws"+strings.TrimPrefix(silent.URL, "http"))
	if err != nil || again == dead {
		t.Fatalf("not redialled: %v", err)
	}
}
