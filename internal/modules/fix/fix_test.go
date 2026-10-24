package fix

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

type resolverFunc func(context.Context, Link) (Post, error)

func (f resolverFunc) Resolve(ctx context.Context, l Link) (Post, error) { return f(ctx, l) }
func module() *Module {
	return New(context.Background(), guard.New(), resolverFunc(func(context.Context, Link) (Post, error) {
		return Post{Text: "Source Text", Author: "Creator", Provider: "test"}, nil
	}), slog.New(slog.DiscardHandler))
}

type recordingRest struct {
	rest.Rest
	mu         sync.Mutex
	creates    []discord.MessageCreate
	updates    []discord.MessageUpdate
	suppressed []discord.MessageUpdate
	deleted    []snowflake.ID
	err        error
}

func (r *recordingRest) CreateMessage(_ snowflake.ID, msg discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creates = append(r.creates, msg)
	return &discord.Message{ID: snowflake.ID(100 + len(r.creates))}, r.err
}
func (r *recordingRest) UpdateMessage(_ snowflake.ID, _ snowflake.ID, msg discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if msg.Flags != nil && msg.Components == nil {
		r.suppressed = append(r.suppressed, msg)
	} else {
		r.updates = append(r.updates, msg)
	}
	return &discord.Message{}, r.err
}
func (r *recordingRest) UpdateInteractionResponse(_ snowflake.ID, _ string, msg discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	return r.UpdateMessage(0, 0, msg)
}
func (r *recordingRest) DeleteMessage(_ snowflake.ID, id snowflake.ID, _ ...rest.RequestOpt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, id)
	return r.err
}
func (r *recordingRest) totals() (int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.creates), len(r.updates), len(r.deleted)
}
func (r *recordingRest) setError(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}
func route(t *testing.T, m *Module) *core.Router {
	t.Helper()
	r := core.NewRouter(0, nil, slog.New(slog.DiscardHandler))
	if err := r.Add(m); err != nil {
		t.Fatal(err)
	}
	return r
}
func slash(t *testing.T, link string, share bool) (*events.ApplicationCommandInteractionCreate, *[]discord.MessageCreate) {
	return coretest.Event(t, "fix", func(p map[string]any) {
		p["data"].(map[string]any)["options"] = []any{map[string]any{"name": "link", "type": 3, "value": link}, map[string]any{"name": "share", "type": 5, "value": share}}
		p["member"].(map[string]any)["permissions"] = "18432"
	})
}
func TestShareNeedsEmbedLinks(t *testing.T) {
	e, sent := coretest.Event(t, "fix", func(p map[string]any) {
		p["data"].(map[string]any)["options"] = []any{map[string]any{"name": "link", "type": 3, "value": "https://x.com/user/status/123"}, map[string]any{"name": "share", "type": 5, "value": true}}
		p["member"].(map[string]any)["permissions"] = "2048"
	})
	if err := module().command(context.Background(), e); err == nil || len(*sent) != 0 {
		t.Fatal("shared without embed links", err)
	}
}
func TestModuleContractAndCommandDelivery(t *testing.T) {
	m := module()
	if m.Name() != Name || m.Want().Required != gateway.IntentGuilds || m.Want().Optional&gateway.IntentMessageContent == 0 || m.Perms()&discord.PermissionManageMessages == 0 || m.Help().About == "" {
		t.Fatal("module contract")
	}
	for _, share := range []bool{false, true} {
		e, sent := slash(t, "https://x.com/user/status/123", share)
		rr := &recordingRest{}
		e.Client().Rest = rr
		if err := m.command(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		_, updates, _ := rr.totals()
		expected := 1

		if updates != 1 || len(*sent) != expected {
			t.Fatalf("%d %+v", updates, *sent)
		}
		if !share && (*sent)[0].Flags&discord.MessageFlagEphemeral == 0 {
			t.Fatal("private default")
		}
		if share && (*sent)[0].Flags&discord.MessageFlagEphemeral != 0 {
			t.Fatal("not shared")
		}
		if rr.updates[0].AllowedMentions == nil {
			t.Fatal("pings")
		}
	}
	if m.Report(3).Rows[0][1] != "2" || m.Report(9).Rows[0][1] != "0" {
		t.Fatal(m.Report(3), m.Report(9))
	}
	m.Gate(func(g snowflake.ID) bool { return g == 3 })
	if !m.enabled(3) || m.enabled(4) {
		t.Fatal("gate")
	}
}
func TestCommandRefusalsThroughRouter(t *testing.T) {
	for _, which := range []string{"invalid", "busy", "limited", "dm"} {
		t.Run(which, func(t *testing.T) {
			m := module()
			r := route(t, m)
			e, sent := slash(t, "https://x.com/user/status/123", false)
			switch which {
			case "invalid":
				e, sent = slash(t, "https://example.com", false)
			case "busy":
				for range cap(m.slots) {
					m.slots <- struct{}{}
				}
			case "limited":
				for range 120 {
					_ = m.guard.Allow(3, guard.MessageSend)
				}
			case "dm":
				e, sent = coretest.Event(t, "fix", func(p map[string]any) {
					delete(p, "guild_id")
					p["data"].(map[string]any)["options"] = []any{map[string]any{"name": "link", "type": 3, "value": "https://x.com/user/status/123"}}
				})
			}
			r.OnCommand(e)
			if len(*sent) != 1 || !strings.HasPrefix((*sent)[0].Content, "✗ ") {
				t.Fatalf("%+v", *sent)
			}
		})
	}
}
func TestContextMenuAndFailures(t *testing.T) {
	m := module()
	for _, content := range []string{"https://x.com/user/status/123", "nothing here"} {
		e, sent := coretest.Event(t, "fix embed", func(p map[string]any) {
			p["data"] = map[string]any{"id": "6", "name": "fix embed", "type": 3, "target_id": "7", "resolved": map[string]any{"messages": map[string]any{"7": map[string]any{"id": "7", "channel_id": "4", "content": content, "author": map[string]any{"id": "8", "username": "author"}, "timestamp": "2026-10-23T00:00:00Z"}}}}
		})
		rr := &recordingRest{}
		e.Client().Rest = rr
		err := m.command(context.Background(), e)
		if content == "nothing here" {
			if err == nil {
				t.Fatal("no refusal")
			}
		} else if err != nil || len(*sent) != 1 {
			t.Fatalf("%v %+v", err, *sent)
		}
	}
	m.api = resolverFunc(func(context.Context, Link) (Post, error) {
		return Post{}, errors.New("upstream error must stay private")
	})
	e, _ := slash(t, "https://x.com/user/status/123", false)
	rr := &recordingRest{}
	e.Client().Rest = rr
	if err := m.command(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rr.updates)
	if strings.Contains(string(raw), "upstream error") || !strings.Contains(string(raw), "preview unavailable") {
		t.Fatal(string(raw))
	}
	if m.Report(3).Rows[1][1] != "1" {
		t.Fatal(m.Report(3))
	}
	e, _ = slash(t, "https://x.com/user/status/123", false)
	rr.setError(errors.New("discord failed"))
	e.Client().Rest = rr
	if err := m.command(context.Background(), e); err == nil {
		t.Fatal("REST failure")
	}
	e, _ = slash(t, "https://x.com/user/status/123", false)
	e.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
		return errors.New("defer failed")
	}
	if err := m.command(context.Background(), e); err == nil {
		t.Fatal("defer failure")
	}
}
func generic(r rest.Rest, id snowflake.ID, text string) *events.GenericGuildMessage {
	return &events.GenericGuildMessage{GenericEvent: events.NewGenericEvent(&bot.Client{Rest: r}, 0, 0), GuildID: 3, ChannelID: 4, MessageID: id, Message: discord.Message{ID: id, ChannelID: 4, Content: text, Author: discord.User{ID: 5}}}
}
func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for worker")
}
func idle(t *testing.T, m *Module) {
	t.Helper()
	waitFor(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		if len(m.slots) != 0 {
			return false
		}
		for _, tracked := range m.tracked {
			if tracked.running || (tracked.dirty && len(tracked.cleanup) == 0 && !tracked.nextFetch.After(time.Now())) {
				return false
			}
		}
		return true
	})
}
func TestAutomaticLifecycleAndEventFilters(t *testing.T) {
	m := module()
	rr := &recordingRest{}
	g := generic(rr, 10, "https://x.com/user/status/123 https://x.com/user/status/124")
	m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: g})
	idle(t, m)
	n, _, _ := rr.totals()
	if n != 2 {
		t.Fatal(n)
	}
	if ref := rr.creates[0].MessageReference; ref == nil || *ref.MessageID != 10 || !ref.FailIfNotExists {
		t.Fatal(ref)
	}
	m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: g})
	idle(t, m)
	if n, _, _ := rr.totals(); n != 2 {
		t.Fatal("duplicate delivered")
	}
	now := time.Now()
	edit := generic(rr, 10, "https://x.com/user/status/123")
	edit.Message.EditedTimestamp = &now
	m.OnEvent(&events.GuildMessageUpdate{GenericGuildMessage: edit})
	idle(t, m)
	_, u, d := rr.totals()
	if u != 1 || d != 1 {
		t.Fatalf("updates=%d deleted=%d", u, d)
	}
	// A generated embed update has no edit timestamp and cannot delete a card.
	m.OnEvent(&events.GuildMessageUpdate{GenericGuildMessage: generic(rr, 10, "")})
	idle(t, m)
	if _, _, d := rr.totals(); d != 1 {
		t.Fatal("embed update removed card")
	}
	edit.Message.Content = "||https://x.com/user/status/125||"
	m.OnEvent(&events.GuildMessageUpdate{GenericGuildMessage: edit})
	idle(t, m)
	if _, _, d := rr.totals(); d != 2 {
		t.Fatal("hidden link card not removed")
	}
	m.OnEvent(&events.GuildMessageDelete{GenericGuildMessage: g})
	waitFor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.tracked) == 0 })
	for _, filter := range []string{"bot", "webhook", "system", "off", "no link", "unknown edit"} {
		g := generic(rr, 20, "https://x.com/user/status/123")
		switch filter {
		case "bot":
			g.Message.Author.Bot = true
		case "webhook":
			id := snowflake.ID(42)
			g.Message.WebhookID = &id
		case "system":
			g.Message.Type = discord.MessageTypeChannelPinnedMessage
		case "off":
			m.Gate(func(snowflake.ID) bool { return false })
		case "no link":
			g.Message.Content = "nothing"
		case "unknown edit":
			g.Message.EditedTimestamp = &now
			m.OnEvent(&events.GuildMessageUpdate{GenericGuildMessage: g})
			continue
		}
		m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: g})
		m.Gate(nil)
	}
	idle(t, m)
	if n, _, _ := rr.totals(); n != 2 {
		t.Fatal("filtered event sent a card")
	}
	m.OnEvent(&events.Ready{})
}
func TestDeletionCancelsInflightAndGuildLeaveForgets(t *testing.T) {
	m := module()
	rr := &recordingRest{}
	started := make(chan struct{})
	m.api = resolverFunc(func(ctx context.Context, _ Link) (Post, error) {
		close(started)
		<-ctx.Done()
		return Post{}, ctx.Err()
	})
	g := generic(rr, 10, "https://x.com/user/status/123")
	m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: g})
	<-started
	m.OnEvent(&events.GuildMessageDelete{GenericGuildMessage: g})
	idle(t, m)
	if n, _, _ := rr.totals(); n != 0 {
		t.Fatal("late card posted")
	}
	m = module()
	m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: g})
	idle(t, m)
	m.OnEvent(&events.GuildMessageDelete{GenericGuildMessage: g})
	waitFor(t, func() bool { _, _, d := rr.totals(); return d == 1 })
	m.OnEvent(&events.GuildMessageDelete{GenericGuildMessage: g})
	m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: generic(rr, 11, "https://x.com/user/status/123")})
	idle(t, m)
	m.OnEvent(&events.GuildLeave{GenericGuild: &events.GenericGuild{GenericEvent: events.NewGenericEvent(&bot.Client{}, 0, 0), GuildID: 3}})
	if len(m.tracked) != 0 || m.Report(3).Rows[0][1] != "0" {
		t.Fatal("guild state retained")
	}
}
func TestBoundedAutomaticWorkAndFailures(t *testing.T) {
	m := module()
	rr := &recordingRest{}
	key := source{3, 4, 10}
	for range cap(m.slots) {
		m.slots <- struct{}{}
	}
	m.queue(rr, key, "https://x.com/user/status/123", false)
	if m.Report(3).Rows[3][1] != "1" {
		t.Fatal(m.Report(3))
	}
	for range cap(m.slots) {
		<-m.slots
	}
	// Exercise eviction with no retained payloads and no active workers.
	m = module()
	for i := range 512 {
		m.tracked[source{3, 4, snowflake.ID(i + 100)}] = &tracking{touched: time.Now().Add(-time.Hour)}
	}
	m.queue(rr, key, "https://x.com/user/status/123", false)
	idle(t, m)
	if len(m.tracked) != 512 {
		t.Fatal(len(m.tracked))
	}
	m = module()
	for range 120 {
		_ = m.guard.Allow(3, guard.MessageSend)
	}
	m.queue(rr, key, "https://x.com/user/status/123", false)
	idle(t, m)
	t1 := &tracking{version: 1}
	links := Links("https://x.com/user/status/123")
	m = module()
	rr.setError(errors.New("send denied"))
	m.convert(context.Background(), rr, key, t1, 1, links)
	if m.Report(3).Rows[2][1] != "1" {
		t.Fatal(m.Report(3))
	}
	t1.replies = []snowflake.ID{101}
	m.convert(context.Background(), rr, key, t1, 1, links)
	m.cleanReplies(rr, key, &tracking{cleanup: []deletion{{id: 101}}})
	if m.Report(3).Rows[2][1] != "3" {
		t.Fatal(m.Report(3))
	}
	for range 300 {
		_ = m.guard.Allow(3, guard.MessageDelete)
	}
	m.cleanReplies(rr, key, &tracking{cleanup: []deletion{{id: 101}}})
	rr.setError(nil)
	m.convert(context.Background(), rr, key, &tracking{gone: true}, 0, links)
	m.convert(context.Background(), rr, key, &tracking{version: 2}, 1, links)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.convert(ctx, rr, key, &tracking{}, 0, nil)
	m.ctx = ctx
	m.queue(rr, key, "https://x.com/user/status/123", false)
}

type blockedRest struct {
	*recordingRest
	entered chan struct{}
	unblock chan struct{}
}

func (r *blockedRest) CreateMessage(channel snowflake.ID, msg discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error) {
	if *msg.MessageReference.MessageID == 10 {
		close(r.entered)
		<-r.unblock
	}
	return r.recordingRest.CreateMessage(channel, msg, opts...)
}

func TestBlockedWriteDoesNotBlockEventsAndLateCreateIsRemoved(t *testing.T) {
	m := module()
	r := &blockedRest{recordingRest: &recordingRest{}, entered: make(chan struct{}), unblock: make(chan struct{})}
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(r.unblock) }) })
	m.queue(r, source{3, 4, 10}, "https://x.com/user/status/123", false)
	<-r.entered
	done := make(chan struct{})
	go func() {
		m.queue(r, source{3, 4, 10}, "||https://x.com/user/status/123||", true)
		m.queue(r, source{3, 4, 11}, "https://x.com/user/status/124", false)
		m.remove(r, source{3, 4, 10})
		close(done)
	}()
	waitFor(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	// Another source completes while the first REST call is still blocked.
	waitFor(t, func() bool { n, _, _ := r.totals(); return n == 1 })
	once.Do(func() { close(r.unblock) })
	waitFor(t, func() bool { _, _, deleted := r.totals(); return deleted == 1 })
	idle(t, m)
	m.mu.Lock()
	_, tracked := m.tracked[source{3, 4, 10}]
	m.mu.Unlock()
	if tracked {
		t.Fatal("late successful create remained tracked after deletion")
	}
}

func TestSaturatedEditsCoalesceAndHiddenLinkIsRemoved(t *testing.T) {
	m := module()
	r := &recordingRest{}
	key := source{3, 4, 10}
	m.queue(r, key, "https://x.com/user/status/123", false)
	idle(t, m)
	for range cap(m.slots) {
		m.slots <- struct{}{}
	}
	m.queue(r, key, "https://x.com/user/status/124", true)
	m.queue(r, key, "||https://x.com/user/status/125||", true)
	for range cap(m.slots) {
		m.release()
	}
	waitFor(t, func() bool { _, _, d := r.totals(); return d == 1 })
	idle(t, m)
	if _, updates, _ := r.totals(); updates != 0 {
		t.Fatal("an obsolete intermediate edit was delivered")
	}
	m.mu.Lock()
	left := len(m.tracked[key].replies) + len(m.tracked[key].cleanup)
	m.mu.Unlock()
	if left != 0 {
		t.Fatal("hidden source retained replies")
	}
}

func discordError(status int) error {
	return &rest.Error{Response: &http.Response{StatusCode: status}}
}

func TestCleanupRetainsFailedIDsAndRetriesTransientFailure(t *testing.T) {
	m := module()
	r := &recordingRest{}
	key := source{3, 4, 10}
	m.queue(r, key, "https://x.com/user/status/123", false)
	idle(t, m)
	r.setError(discordError(503))
	m.remove(r, key)
	waitFor(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		t := m.tracked[key]
		return t != nil && len(t.cleanup) == 1 && t.cleanup[0].attempt == 1
	})
	r.setError(nil)
	waitFor(t, func() bool { _, _, d := r.totals(); return d == 2 })
	idle(t, m)
	m.mu.Lock()
	_, exists := m.tracked[key]
	m.mu.Unlock()
	if exists || r.deleted[0] != r.deleted[1] {
		t.Fatal("cleanup lost or failed to retry the exact reply ID")
	}
}

func TestCleanupTreats404AsSuccessAndDoesNotRetry403(t *testing.T) {
	for _, status := range []int{404, 403} {
		m := module()
		r := &recordingRest{}
		key := source{3, 4, 10}
		m.queue(r, key, "https://x.com/user/status/123", false)
		idle(t, m)
		r.setError(discordError(status))
		m.remove(r, key)
		waitFor(t, func() bool { _, _, d := r.totals(); return d == 1 })
		idle(t, m)
		m.mu.Lock()
		entry := m.tracked[key]
		m.kickLocked()
		m.mu.Unlock()
		if status == 404 && entry != nil {
			t.Fatal("404 retained a missing reply")
		}
		if status == 403 && (entry == nil || len(entry.cleanup) != 1 || !entry.cleanup[0].stopped) {
			t.Fatal("403 must retain the ID without retrying")
		}
		if _, _, d := r.totals(); d != 1 {
			t.Fatal("permanent failure retried")
		}
	}
}

func TestCleanupGuardDeferralAndCanceledScheduler(t *testing.T) {
	m := module()
	r := &recordingRest{}
	key := source{3, 4, 10}
	for range 300 {
		_ = m.guard.Allow(3, guard.MessageDelete)
	}
	entry := &tracking{gone: true, cleanup: []deletion{{id: 101}}}
	m.tracked[key] = entry
	m.rest = r
	m.cleanReplies(r, key, entry)
	if len(entry.cleanup) != 1 || entry.cleanup[0].stopped || !entry.cleanup[0].next.After(time.Now()) {
		t.Fatal("guard denial lost cleanup or stopped retries")
	}
	m.mu.Lock()
	m.kickLocked()
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.ctx = ctx
	m.mu.Lock()
	m.kickLocked()
	m.mu.Unlock()
	if m.wake != nil {
		t.Fatal("shutdown left a retry timer")
	}
	if !retryDelete(context.DeadlineExceeded) || !retryDelete(discordError(429)) || retryDelete(discordError(401)) || retryDelete(errors.New("invalid request")) {
		t.Fatal("retry classification")
	}
}

func TestPartialLinkRemovalDeletesTheHiddenPost(t *testing.T) {
	m := module()
	rr := &recordingRest{}
	key := source{3, 4, 10}
	m.queue(rr, key, "https://x.com/user/status/123 https://x.com/user/status/124", false)
	idle(t, m)
	m.queue(rr, key, "||https://x.com/user/status/123|| https://x.com/user/status/124", true)
	waitFor(t, func() bool { _, _, d := rr.totals(); return d == 1 })
	idle(t, m)
	rr.mu.Lock()
	deleted := append([]snowflake.ID(nil), rr.deleted...)
	rr.mu.Unlock()
	if len(deleted) != 1 || deleted[0] != 101 {
		t.Fatalf("deleted %v; must delete first/hidden post, not the still-visible second one", deleted)
	}
	m.mu.Lock()
	entry := m.tracked[key]
	defer m.mu.Unlock()
	if len(entry.replies) != 1 || entry.replies[0] != 102 || entry.published[0] != "https://x.com/user/status/124" {
		t.Fatalf("remaining replies %+v", entry)
	}
}
func TestRateLimitedMultiLinkJobResumesAfterCompletedPrefix(t *testing.T) {
	m := module()
	rr := &recordingRest{}
	key := source{3, 4, 10}
	links := Links("https://x.com/user/status/123 https://x.com/user/status/124")
	for range 119 {
		_ = m.guard.Allow(3, guard.MessageSend)
	}
	entry := &tracking{version: 1}
	m.tracked[key] = entry
	m.convert(context.Background(), rr, key, entry, 1, links)
	if n, _, _ := rr.totals(); n != 1 {
		t.Fatal(n)
	}
	m.mu.Lock()
	completed := entry.completed
	m.mu.Unlock()
	if completed != 1 {
		t.Fatal(completed)
	}
	// A replenished guard models the next admission interval. The completed
	// first post must not consume the only new token on every retry.
	m.guard = guard.New()
	for range 119 {
		_ = m.guard.Allow(3, guard.MessageSend)
	}
	m.convert(context.Background(), rr, key, entry, 1, links)
	if n, u, _ := rr.totals(); n != 2 || u != 0 {
		t.Fatalf("creates=%d updates=%d", n, u)
	}
}
