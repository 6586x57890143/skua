// SPDX-License-Identifier: AGPL-3.0-only
package concord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
)

// This relay deliberately ignores REQ author, kind and count constraints.
func unfilteredRelay(t *testing.T, events []*nostr.Event) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c, err := up.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				return
			}
			var msg []json.RawMessage
			if json.Unmarshal(raw, &msg) != nil || len(msg) < 2 {
				continue
			}
			var verb, id string
			_ = json.Unmarshal(msg[0], &verb)
			_ = json.Unmarshal(msg[1], &id)
			if verb != "REQ" {
				continue
			}
			for _, ev := range events {
				if c.WriteJSON([]any{"EVENT", id, ev}) != nil {
					return
				}
			}
			if c.WriteJSON([]any{"EOSE", id}) != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestPoolRejectsOffFilterEvents(t *testing.T) {
	k, _ := KeyFromSecret([32]byte{11})
	other, _ := KeyFromSecret([32]byte{12})
	valid := signed(t, k, 1, "wanted")
	url := unfilteredRelay(t, []*nostr.Event{signed(t, other, 1, "wrong author"), signed(t, k, 2, "wrong kind"), valid})
	p := NewPool([]string{url})
	defer p.Close()
	f := nostr.Filter{Authors: []string{k.PK}, Kinds: []int{1}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := p.Query(ctx, f); len(got) != 1 || got[0].ID != valid.ID {
		t.Fatalf("off-filter query results: %v", got)
	}
	sctx, stop := context.WithCancel(ctx)
	var seen []*nostr.Event
	p.stream(sctx, url, f, func(ev *nostr.Event) {
		seen = append(seen, ev)
		if ev.ID == valid.ID {
			stop()
		}
	})
	if len(seen) != 1 || seen[0].ID != valid.ID {
		t.Fatalf("off-filter subscription results: %v", seen)
	}
}

func TestPoolQueryLimitIsLocal(t *testing.T) {
	k, _ := KeyFromSecret([32]byte{13})
	url := unfilteredRelay(t, []*nostr.Event{signed(t, k, 1, "one"), signed(t, k, 1, "two"), signed(t, k, 1, "three")})
	p := NewPool([]string{url})
	defer p.Close()
	if got := p.Query(context.Background(), nostr.Filter{Kinds: []int{1}, Limit: 2}); len(got) != 2 {
		t.Fatalf("relay ignored limit: got %d", len(got))
	}
}
