// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
)

// A relay connection can die without closing: an idle timeout, a dropped NAT
// mapping, a relay restarted under it. Nothing would ever be read from it
// again, so a subscription would hang and every publish would time out on
// it, forever. Pings every pingEvery, and silence past pongWait, any frame
// or pong resetting it, close the socket so the pool dials again. A pool's own
// fields, so a test can shorten them for its pool alone.
const pingEvery, pongWait = 30 * time.Second, 75 * time.Second

const (
	queryTimeout   = 10 * time.Second
	publishTimeout = 8 * time.Second
	writeTimeout   = 10 * time.Second
	maxBackoff     = time.Minute
	// maxFrame bounds one message from a relay. A wrap is at most about
	// 90 KB of base64; anything far past that is not one.
	maxFrame = 512 << 10
)

type querier interface {
	Query(ctx context.Context, f nostr.Filter) []*nostr.Event
}

// Pool talks to a community's relays. Relays may gate stream addresses
// behind NIP-42 and want AUTH as each stream key queried, so whenever a
// relay sends a challenge the pool answers as every stream key it holds,
// and a REQ or publish refused for auth is tried once more after that.
//
// It is its own small client rather than go-nostr's, whose relay races on
// every disconnect and on the challenge it keeps.
type Pool struct {
	urls []string
	next atomic.Uint64

	pingEvery, pongWait time.Duration

	mu     sync.Mutex
	conns  map[string]*conn
	keys   map[string]string // stream pk -> sk, for AUTH
	drops  map[string]int    // sockets that died under use, per relay
	closed bool
}

// NewPool is a pool over urls. Nothing connects until it is used.
func NewPool(urls []string) *Pool {
	return &Pool{urls: urls, conns: map[string]*conn{}, keys: map[string]string{}, drops: map[string]int{}, pingEvery: pingEvery, pongWait: pongWait}
}

// Register makes the pool AUTH as these stream keys, on sockets already
// challenged too. A key without its secret can't sign and is skipped.
func (p *Pool) Register(keys ...StreamKey) {
	p.mu.Lock()
	added := false
	for _, k := range keys {
		if k.SK != "" && p.keys[k.PK] == "" {
			p.keys[k.PK], added = k.SK, true
		}
	}
	conns := make([]*conn, 0, len(p.conns))
	for _, c := range p.conns {
		conns = append(conns, c)
	}
	p.mu.Unlock()
	if added {
		for _, c := range conns {
			go p.auth(context.Background(), c)
		}
	}
}

// Close drops every socket.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, c := range p.conns {
		c.close()
	}
	clear(p.conns)
}

// frame is what a subscription hears: an event, the end of stored events,
// or the relay closing it with a reason.
type frame struct {
	ev     *nostr.Event
	eose   bool
	closed *string
}

type okFrame struct {
	ok     bool
	reason string
}

// conn is one socket. Every write goes through wmu; everything else the
// reader shares is under mu.
type conn struct {
	url string
	// pongWait is how long the socket may be silent before it counts as dead.
	pongWait time.Duration
	// heard is when anything last came in, a frame or a pong; unix ms.
	heard atomic.Int64
	ws    *websocket.Conn
	wmu   sync.Mutex

	mu        sync.Mutex
	subs      map[string]chan frame
	oks       map[string]chan okFrame
	challenge string
	authed    map[string]chan struct{} // closed once the relay has answered
	done      chan struct{}
}

func (p *Pool) relay(ctx context.Context, url string) (*conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("concord: pool closed")
	}
	if c := p.conns[url]; c != nil && !c.isDone() {
		return c, nil
	}
	dctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	ws, res, err := websocket.DefaultDialer.DialContext(dctx, url, nil)
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(maxFrame)
	wait := p.pongWait
	_ = ws.SetReadDeadline(time.Now().Add(wait))
	c := &conn{url: url, pongWait: wait, ws: ws, subs: map[string]chan frame{}, oks: map[string]chan okFrame{}, authed: map[string]chan struct{}{}, done: make(chan struct{})}
	c.heard.Store(time.Now().UnixMilli())
	ws.SetPongHandler(func(string) error {
		c.heard.Store(time.Now().UnixMilli())
		return ws.SetReadDeadline(time.Now().Add(wait))
	})
	p.conns[url] = c
	go p.read(c)
	go c.keepalive(p.pingEvery)
	return c, nil
}

// keepalive pings until the socket closes, and closes it when a ping can't
// be written.
func (c *conn) keepalive(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.wmu.Lock()
			err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout))
			c.wmu.Unlock()
			if err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *conn) isDone() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *conn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isDone() {
		return
	}
	close(c.done)
	_ = c.ws.Close()
}

func (c *conn) write(v ...any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.ws.WriteMessage(websocket.TextMessage, raw)
}

// read is the socket's one reader. An event that fails its id or signature
// check is dropped here, before anything sees it.
func (p *Pool) read(c *conn) {
	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			p.mu.Lock()
			if !p.closed {
				p.drops[c.url]++
			}
			p.mu.Unlock()
			c.close()
			return
		}
		c.heard.Store(time.Now().UnixMilli())
		_ = c.ws.SetReadDeadline(time.Now().Add(c.pongWait))
		var msg []json.RawMessage
		if json.Unmarshal(raw, &msg) != nil || len(msg) < 2 {
			continue
		}
		var verb, first string
		_ = json.Unmarshal(msg[0], &verb)
		_ = json.Unmarshal(msg[1], &first)
		switch {
		case verb == "EVENT" && len(msg) >= 3:
			var ev nostr.Event
			if json.Unmarshal(msg[2], &ev) != nil || !validEvent(&ev) {
				continue
			}
			c.deliver(first, frame{ev: &ev})
		case verb == "EOSE":
			c.deliver(first, frame{eose: true})
		case verb == "CLOSED":
			var reason string
			if len(msg) >= 3 {
				_ = json.Unmarshal(msg[2], &reason)
			}
			c.deliver(first, frame{closed: &reason})
		case verb == "OK" && len(msg) >= 3:
			var ok bool
			var reason string
			_ = json.Unmarshal(msg[2], &ok)
			if len(msg) >= 4 {
				_ = json.Unmarshal(msg[3], &reason)
			}
			c.mu.Lock()
			ch := c.oks[first]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- okFrame{ok, reason}:
				default:
				}
			}
		case verb == "AUTH":
			c.mu.Lock()
			c.challenge = first
			clear(c.authed) // a new challenge authenticates nothing yet
			c.mu.Unlock()
			go p.auth(context.Background(), c)
		}
	}
}

// deliver hands a frame to its subscription, waiting while it is busy so
// nothing is lost, but never past the socket closing.
func (c *conn) deliver(id string, f frame) {
	c.mu.Lock()
	ch := c.subs[id]
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- f:
	case <-c.done:
	}
}

// auth answers the socket's challenge as every stream key not yet
// authenticated on it, and waits for the relay's answers.
func (p *Pool) auth(ctx context.Context, c *conn) {
	c.mu.Lock()
	challenge := c.challenge
	c.mu.Unlock()
	if challenge == "" {
		return
	}
	p.mu.Lock()
	keys := make(map[string]string, len(p.keys))
	for pk, sk := range p.keys {
		keys[pk] = sk
	}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for pk, sk := range keys {
		c.mu.Lock()
		pending, started := c.authed[pk]
		answered := make(chan struct{})
		if !started {
			c.authed[pk] = answered
		}
		c.mu.Unlock()
		if started {
			// In flight from another caller: wait, so a retried REQ goes after it.
			wg.Go(func() {
				select {
				case <-pending:
				case <-c.done:
				case <-ctx.Done():
				}
			})
			continue
		}
		ev := &nostr.Event{Kind: 22242, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"relay", c.url}, {"challenge", challenge}}}
		if ev.Sign(sk) != nil {
			close(answered)
			continue
		}
		wg.Go(func() {
			defer close(answered)
			if _, err := c.send(ctx, "AUTH", ev); err != nil {
				c.mu.Lock()
				delete(c.authed, pk)
				c.mu.Unlock()
			}
		})
	}
	wg.Wait()
}

// send writes an event the relay answers with OK, and waits for it.
func (c *conn) send(ctx context.Context, verb string, ev *nostr.Event) (okFrame, error) {
	ch := make(chan okFrame, 1)
	c.mu.Lock()
	c.oks[ev.ID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.oks, ev.ID)
		c.mu.Unlock()
	}()
	if err := c.write(verb, ev); err != nil {
		return okFrame{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	select {
	case r := <-ch:
		if !r.ok {
			return r, errors.New("concord: relay refused: " + r.reason)
		}
		return r, nil
	case <-c.done:
		return okFrame{}, errors.New("concord: relay connection closed")
	case <-ctx.Done():
		return okFrame{}, ctx.Err()
	}
}

// req opens a subscription; stop closes it.
func (p *Pool) req(c *conn, f nostr.Filter) (chan frame, func(), error) {
	id := "s" + strconv.FormatUint(p.next.Add(1), 10)
	frames := make(chan frame, 64)
	c.mu.Lock()
	c.subs[id] = frames
	c.mu.Unlock()
	stop := func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
		_ = c.write("CLOSE", id)
	}
	if err := c.write("REQ", id, f); err != nil {
		stop()
		return nil, nil, err
	}
	return frames, stop, nil
}

func authRefused(reason string) bool {
	reason = strings.ToLower(reason)
	return strings.Contains(reason, "auth") || strings.Contains(reason, "restricted")
}

// Query asks every relay and merges what they hold, by id.
func (p *Pool) Query(ctx context.Context, f nostr.Filter) []*nostr.Event {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	var mu sync.Mutex
	byID := map[string]*nostr.Event{}
	var wg sync.WaitGroup
	for _, url := range p.urls {
		wg.Go(func() {
			for _, ev := range p.queryOne(ctx, url, f) {
				mu.Lock()
				byID[ev.ID] = ev
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	out := make([]*nostr.Event, 0, len(byID))
	for _, ev := range byID {
		out = append(out, ev)
	}
	return out
}

func (p *Pool) queryOne(ctx context.Context, url string, f nostr.Filter) []*nostr.Event {
	for attempt := range 2 {
		c, err := p.relay(ctx, url)
		if err != nil {
			return nil
		}
		frames, stop, err := p.req(c, f)
		if err != nil {
			return nil
		}
		var out []*nostr.Event
		retry := false
	read:
		for {
			select {
			case fr := <-frames:
				switch {
				case fr.ev != nil:
					out = append(out, fr.ev)
				case fr.eose:
					break read
				case fr.closed != nil:
					retry = attempt == 0 && authRefused(*fr.closed)
					break read
				}
			case <-c.done:
				break read
			case <-ctx.Done():
				break read
			}
		}
		stop()
		if !retry {
			return out
		}
		p.auth(ctx, c)
	}
	return nil
}

// Publish sends ev to every relay and succeeds when any accepts it.
func (p *Pool) Publish(ctx context.Context, ev *nostr.Event) error {
	errs := make(chan error, len(p.urls))
	for _, url := range p.urls {
		go func() { errs <- p.publishOne(ctx, url, ev) }()
	}
	var all []error
	for range p.urls {
		err := <-errs
		if err == nil {
			return nil
		}
		all = append(all, err)
	}
	return errors.Join(append([]error{errors.New("concord: no relay accepted the event")}, all...)...)
}

func (p *Pool) publishOne(ctx context.Context, url string, ev *nostr.Event) error {
	c, err := p.relay(ctx, url)
	if err != nil {
		return err
	}
	r, err := c.send(ctx, "EVENT", ev)
	if err != nil && authRefused(r.reason) {
		p.auth(ctx, c)
		_, err = c.send(ctx, "EVENT", ev)
	}
	return err
}

// Subscribe keeps a REQ open on every relay until ctx ends, reconnecting
// with backoff. A reconnect asks again from a minute before the newest
// event it saw, so a drop costs a few repeats, which the caller dedupes.
// onEvent may be called from several goroutines at once.
func (p *Pool) Subscribe(ctx context.Context, f nostr.Filter, onEvent func(*nostr.Event)) {
	for _, url := range p.urls {
		go p.keep(ctx, url, f, onEvent)
	}
}

func (p *Pool) keep(ctx context.Context, url string, f nostr.Filter, onEvent func(*nostr.Event)) {
	backoff := 2 * time.Second
	retried := false
	for ctx.Err() == nil {
		last, authed := p.stream(ctx, url, f, onEvent)
		if last > 0 {
			since := last - 60
			f.Since = &since
			backoff = 2 * time.Second
		}
		// Refused for auth and now authenticated: ask again at once, but
		// only once in a row, so a relay that keeps refusing gets backoff.
		if authed && !retried {
			retried = true
			continue
		}
		retried = false
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// stream runs one REQ to its end. It returns the newest created_at it saw,
// and whether the relay refused for auth and skua has just authenticated.
func (p *Pool) stream(ctx context.Context, url string, f nostr.Filter, onEvent func(*nostr.Event)) (nostr.Timestamp, bool) {
	c, err := p.relay(ctx, url)
	if err != nil {
		return 0, false
	}
	frames, stop, err := p.req(c, f)
	if err != nil {
		return 0, false
	}
	defer stop()
	var last nostr.Timestamp
	for {
		select {
		case fr := <-frames:
			switch {
			case fr.ev != nil:
				last = max(last, fr.ev.CreatedAt)
				onEvent(fr.ev)
			case fr.closed != nil:
				if authRefused(*fr.closed) {
					p.auth(ctx, c)
					return last, true
				}
				return last, false
			}
		case <-c.done:
			return last, false
		case <-ctx.Done():
			return last, false
		}
	}
}

// RelayHealth is one relay as the pool last saw it.
type RelayHealth struct {
	URL   string
	Up    bool
	Heard time.Time // zero when it has never been connected
	Drops int       // sockets that died under use since the pool began
}

// Health is every relay the pool talks to, in order.
func (p *Pool) Health() []RelayHealth {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]RelayHealth, 0, len(p.urls))
	for _, url := range p.urls {
		h := RelayHealth{URL: url, Drops: p.drops[url]}
		if c := p.conns[url]; c != nil {
			h.Up = !c.isDone()
			h.Heard = time.UnixMilli(c.heard.Load())
		}
		out = append(out, h)
	}
	return out
}
