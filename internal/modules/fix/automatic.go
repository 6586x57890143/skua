package fix

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/obs"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

type source struct{ guild, channel, message snowflake.ID }
type deletion struct {
	id      snowflake.ID
	next    time.Time
	attempt int
	stopped bool
}

// Every tracking field belongs to Module.mu. No state lock spans network I/O.
// Only validated links for the latest pending revision are retained, not text.
type tracking struct {
	version   uint64
	gone      bool
	forgotten bool
	cancel    context.CancelFunc
	replies   []snowflake.ID
	published []string
	completed int
	cleanup   []deletion
	touched   time.Time
	pending   []Link
	dirty     bool
	running   bool
	nextFetch time.Time
}

func (t *tracking) retire(from int) {
	for _, id := range t.replies[from:] {
		t.cleanup = append(t.cleanup, deletion{id: id})
	}
	t.replies = t.replies[:from]
	t.published = t.published[:min(from, len(t.published))]
}

// Tracking is deliberately bounded. Edits/deletes follow up to 512 recent
// source messages until restart. Pending work coalesces instead of being lost
// when every fetch slot is occupied.
func (m *Module) queue(r rest.Rest, key source, text string, edit bool) {
	if !m.enabled(key.guild) || m.ctx.Err() != nil {
		return
	}
	links := Links(text)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rest = r
	t := m.tracked[key]
	if t == nil {
		if edit || len(links) == 0 {
			return
		}
		if len(m.tracked) >= 512 {
			var oldest source
			var when time.Time
			for k, v := range m.tracked {
				// At most eight entries run, so an idle entry always exists.
				// Never evict a worker while it owns a write.
				if !v.running && (when.IsZero() || v.touched.Before(when)) {
					oldest, when = k, v.touched
				}
			}
			delete(m.tracked, oldest)
		}
		t = &tracking{}
		m.tracked[key] = t
	} else if !edit || t.gone {
		return
	}
	t.touched = time.Now()
	t.version++
	if t.cancel != nil {
		t.cancel()
	}
	t.pending, t.dirty = links, true
	t.nextFetch = time.Time{}

	t.completed = 0
	// Retire the actual links removed from the source, even when another link
	// remains. This removes a newly hidden post before waiting on fresh metadata.
	desired := make(map[string]bool, len(links))
	for _, l := range links {
		desired[l.URL] = true
	}
	kept := t.replies[:0]
	published := t.published[:0]
	for i, id := range t.replies {
		u := ""
		if i < len(t.published) {
			u = t.published[i]
		}
		if desired[u] {
			kept = append(kept, id)
			published = append(published, u)
		} else {
			t.cleanup = append(t.cleanup, deletion{id: id})
		}
	}
	t.replies, t.published = kept, published

	if len(m.slots) == cap(m.slots) {
		m.count(key.guild, func(c *counts) { c.Busy++ })
	}
	m.kickLocked()
}

// release wakes automatic work after a manual or automatic shared slot ends.
func (m *Module) release() {
	<-m.slots
	m.mu.Lock()
	m.kickLocked()
	m.mu.Unlock()
}

// One timer covers every retry deadline. Cleanup has priority over new fetches,
// uses the same bounded workers, and is never dropped behind the semaphore.
func (m *Module) kickLocked() {
	if m.ctx.Err() != nil {
		if m.wake != nil {
			m.wake.Stop()
			m.wake = nil
		}
		return
	}
	now := time.Now()
	var next time.Time
	for pass := range 2 {
		for key, t := range m.tracked {
			if t.running || t.forgotten {
				continue
			}
			if t.dirty && t.nextFetch.After(now) && (next.IsZero() || t.nextFetch.Before(next)) {
				next = t.nextFetch
			}
			cleanupReady := false
			for _, d := range t.cleanup {
				if d.stopped {
					continue
				}
				if !d.next.After(now) {
					cleanupReady = true
				} else if next.IsZero() || d.next.Before(next) {
					next = d.next
				}
			}
			if (pass == 0 && !cleanupReady) || (pass == 1 && (len(t.cleanup) != 0 || !t.dirty || t.nextFetch.After(now))) {
				continue
			}
			select {
			case m.slots <- struct{}{}:
			default:
				continue
			}
			ctx, cancel := context.WithTimeout(obs.With(m.ctx, Name), buildBy)
			t.cancel, t.running = cancel, true
			version, links, refresh := t.version, t.pending, t.dirty
			t.pending, t.dirty = nil, false
			go m.work(ctx, m.rest, key, t, version, links, refresh, t.touched)
		}
	}
	if m.wake != nil {
		m.wake.Stop()
		m.wake = nil
	}
	if !next.IsZero() {
		m.wake = time.AfterFunc(time.Until(next), func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.kickLocked()
		})
	}
}

func (m *Module) work(ctx context.Context, r rest.Rest, key source, t *tracking, version uint64, links []Link, refresh bool, queued time.Time) {
	if refresh {
		defer func() {
			m.mu.Lock()
			done := t.version == version && !t.gone && len(links) > 0 && t.completed == len(links)
			m.mu.Unlock()
			if done {
				obs.Default.Add("fix end-to-end", obs.Run, time.Since(queued))
			}
		}()
	}
	defer func() {
		m.mu.Lock()
		t.cancel()
		t.cancel, t.running = nil, false
		if t.gone && len(t.cleanup) == 0 && m.tracked[key] == t {
			delete(m.tracked, key)
		}
		m.mu.Unlock()
		m.release()
	}()
	m.cleanReplies(r, key, t)
	if !refresh {
		return
	}
	m.mu.Lock()
	if len(t.cleanup) != 0 {
		if t.version == version && !t.gone {
			t.pending, t.dirty = links, true
		}
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	m.convert(ctx, r, key, t, version, links)
}

func (m *Module) current(ctx context.Context, key source, t *tracking, version uint64) bool {
	return !t.gone && version == t.version && ctx.Err() == nil && m.enabled(key.guild)
}

func (m *Module) convert(ctx context.Context, r rest.Rest, key source, t *tracking, version uint64, links []Link) {
	fetch, stopFetch := context.WithTimeout(ctx, 12*time.Second)
	defer stopFetch()
	for i, l := range links {
		m.mu.Lock()
		current := m.current(ctx, key, t, version)
		completed := t.completed
		m.mu.Unlock()
		if i < completed {
			continue
		}
		if !current {
			return
		}
		if err := m.guard.Allow(key.guild, guard.MessageSend); err != nil {
			m.mu.Lock()
			if m.current(ctx, key, t, version) {
				t.pending, t.dirty = links, true
				t.nextFetch = time.Now().Add(30 * time.Second)
			}
			m.mu.Unlock()
			return
		}
		msg := m.preview(fetch, key.guild, l)
		m.mu.Lock()
		if !m.current(ctx, key, t, version) {
			m.mu.Unlock()
			return
		}
		var id snowflake.ID
		for j, u := range t.published {
			if u == l.URL {
				id = t.replies[j]
				break
			}
		}
		m.mu.Unlock()
		var err error
		if id != 0 {
			_, err = r.UpdateMessage(key.channel, id, discord.NewMessageUpdateV2(msg.Components...).WithAllowedMentions(core.NoPings()), rest.WithCtx(ctx))
		} else {
			msg.MessageReference = &discord.MessageReference{MessageID: &key.message, ChannelID: &key.channel, GuildID: &key.guild, FailIfNotExists: true}
			var sent *discord.Message
			sent, err = r.CreateMessage(key.channel, msg, rest.WithCtx(ctx))
			if err == nil {
				m.mu.Lock()
				if t.gone || t.version != version {
					// Cancellation can race with a successful write. Retain
					// its ID so the next job removes the stale card.
					t.cleanup = append(t.cleanup, deletion{id: sent.ID})
				} else {
					t.replies = append(t.replies, sent.ID)
					t.published = append(t.published, l.URL)
				}
				m.mu.Unlock()
			}
		}
		m.guard.Report(key.guild, guard.Struggling(err))
		if err != nil {
			m.countTracked(key, t, func(c *counts) { c.Failed++ })
			m.log.Warn("fix: card delivery failed", "guild", key.guild)
			return
		}
		m.mu.Lock()
		if m.current(ctx, key, t, version) {
			t.completed = i + 1
		}
		m.mu.Unlock()
		m.countTracked(key, t, func(c *counts) { c.Cards++ })
	}
}

func (m *Module) remove(r rest.Rest, key source) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rest = r
	t := m.tracked[key]
	if t == nil {
		return
	}
	t.gone = true
	t.pending, t.dirty = nil, false
	t.retire(0)
	if t.cancel != nil {
		t.cancel()
	}
	if !t.running && len(t.cleanup) == 0 {
		delete(m.tracked, key)
	}
	m.kickLocked()
}

func retryDelete(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var re *rest.Error
	if errors.As(err, &re) && re.Response != nil {
		return re.Response.StatusCode == http.StatusTooManyRequests || re.Response.StatusCode >= 500
	}
	var ne net.Error
	return errors.As(err, &ne)
}

func (m *Module) countTracked(key source, t *tracking, change func(*counts)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.forgotten {
		m.count(key.guild, change)
	}
}

func (m *Module) cleanReplies(r rest.Rest, key source, t *tracking) {
	ctx, cancel := context.WithTimeout(obs.With(m.ctx, Name), 5*time.Second)
	defer cancel()
	m.mu.Lock()
	batch := append([]deletion(nil), t.cleanup...)
	m.mu.Unlock()
	for _, d := range batch {
		if d.stopped || d.next.After(time.Now()) || m.ctx.Err() != nil {
			continue
		}
		err := m.guard.Allow(key.guild, guard.MessageDelete)
		denied := err != nil
		if !denied {
			err = r.DeleteMessage(key.channel, d.id, rest.WithCtx(ctx))
			m.guard.Report(key.guild, guard.Struggling(err))
		}
		var re *rest.Error
		absent := errors.As(err, &re) && re.Response != nil && re.Response.StatusCode == http.StatusNotFound
		m.mu.Lock()
		for i := range t.cleanup {
			if t.cleanup[i].id != d.id {
				continue
			}
			if err == nil || absent {
				t.cleanup = append(t.cleanup[:i], t.cleanup[i+1:]...)
			} else {
				d.attempt++
				d.stopped = !denied && !retryDelete(err)
				d.next = time.Now().Add(time.Second * time.Duration(min(30, 1<<min(d.attempt-1, 5))))
				if denied {
					d.next = time.Now().Add(30 * time.Second)
				}
				t.cleanup[i] = d
			}
			break
		}
		m.mu.Unlock()
		if err != nil && !absent {
			m.countTracked(key, t, func(c *counts) { c.Failed++ })
		}
	}
}
