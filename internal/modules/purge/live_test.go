package purge

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/store"
)

// testDB is a migrated Postgres from SKUA_TEST_DATABASE_URL, which CI
// runs. Unset, the test skips.
func testDB(t *testing.T) DB {
	t.Helper()
	dsn := os.Getenv("SKUA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKUA_TEST_DATABASE_URL is not set")
	}
	pool, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// freshGuild is a guild ID no other test or run has written rows for.
func freshGuild() snowflake.ID {
	return snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
}

// eventually polls cond for up to two seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

func (f *fake) state() (bulks [][]snowflake.ID, singles []snowflake.ID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.bulks), slices.Clone(f.singles)
}

func liveModule() *Module {
	m := newModule()
	m.window = 20 * time.Millisecond
	m.schedTick = time.Hour // a test that wants a tick calls due itself
	return m
}

func posted(f *fake, guild, author, ch, id snowflake.ID) *events.GuildMessageCreate {
	return &events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{
		GenericEvent: events.NewGenericEvent(&bot.Client{Rest: f}, 0, 0),
		MessageID:    id, ChannelID: ch, GuildID: guild,
		Message: discord.Message{ID: id, ChannelID: ch, Author: discord.User{ID: author}},
	}}
}

func TestLiveBurstGoesInOneBulk(t *testing.T) {
	m, f := liveModule(), newFake()
	now := time.Now()
	var s seq
	for range 20 {
		m.enqueue(f, guildID, textCh, msg{s.at(now.Add(-time.Second), me).ID, me}, now)
	}
	eventually(t, "one bulk of 20", func() bool {
		b, _ := f.state()
		return len(b) == 1 && len(b[0]) == 20
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queues) != 0 {
		t.Fatal("an emptied queue stayed")
	}
}

// A shorter delay queued behind a longer one is not held up by it.
func TestLiveShorterDelayJumpsTheQueue(t *testing.T) {
	m, f := liveModule(), newFake()
	now := time.Now()
	var s seq
	later, sooner := s.at(now, me).ID, s.at(now, me).ID
	m.enqueue(f, guildID, textCh, msg{later, me}, now.Add(time.Hour))
	m.enqueue(f, guildID, textCh, msg{sooner, me}, now)
	eventually(t, "the sooner message deleted alone", func() bool {
		_, singles := f.state()
		return slices.Equal(singles, []snowflake.ID{sooner})
	})
	m.mu.Lock()
	q := m.queues[textCh]
	m.mu.Unlock()
	if q == nil || len(q.items) != 1 || q.items[0].id != later {
		t.Fatal("the later message left the queue early")
	}
	q.timer.Stop()
}

func TestOnEventQueuesOnlyLiveMembers(t *testing.T) {
	m, f := liveModule(), newFake()
	m.OnEvent(posted(f, guildID, me, textCh, 900)) // nobody live yet
	m.setLive(target{guildID, me}, time.Millisecond)
	m.OnEvent(posted(f, guildID, them, textCh, 901))
	m.OnEvent(posted(f, guildID+1, me, textCh, 902)) // live in another guild only
	m.OnEvent(posted(f, guildID, me, textCh, 903))
	eventually(t, "the live member's message deleted", func() bool {
		_, singles := f.state()
		return len(singles) > 0
	})
	time.Sleep(3 * m.window)
	if _, singles := f.state(); !slices.Equal(singles, []snowflake.ID{903}) {
		t.Fatalf("deleted %v, want only 903", singles)
	}
	m.setLive(target{guildID, me}, 0)
	m.setLive(target{guildID, me}, 0)
	if m.liveN.Load() != 0 {
		t.Fatalf("live count %d after turning off twice", m.liveN.Load())
	}
}

func TestLiveRemoveStopsOnRefusal(t *testing.T) {
	m, f := liveModule(), newFake()
	f.bulkErr = refusal(403, 50013)
	m.remove(f, guildID, textCh, []msg{{1, me}, {2, me}})
	if b, s := f.state(); len(b)+len(s) != 0 {
		t.Fatal("deleted in a channel that refused")
	}
	for m.guard.Allow(guildID, guard.Purge) == nil {
	}
	f.bulkErr = nil
	m.remove(f, guildID, textCh, []msg{{1, me}, {2, me}})
	if b, s := f.state(); len(b)+len(s) != 0 {
		t.Fatal("deleted past the guard")
	}
}

// liveCmd is /purge live after:<choice>.
func liveCmd(t *testing.T, m *Module, choice string, guild snowflake.ID) string {
	return command(t, m, "live", func(p map[string]any) {
		p["guild_id"] = guild.String()
		p["data"].(map[string]any)["options"] = []any{map[string]any{
			"name": "live", "type": 1, "options": []any{map[string]any{"name": "after", "type": 3, "value": choice}},
		}}
	})
}

func TestLiveCommandWithoutADatabase(t *testing.T) {
	m := liveModule()
	if got := liveCmd(t, m, "1m", guildID); got != "✗ "+string(errNoDB) {
		t.Fatalf("got %q", got)
	}
	if got := liveCmd(t, m, "3d", guildID); !strings.Contains(got, "isn't one of the choices") {
		t.Fatalf("a choice that isn't one: %q", got)
	}
}

func TestLiveCommandRemembers(t *testing.T) {
	db := testDB(t)
	m := liveModule()
	m.useDB(db)
	g := freshGuild()
	if got := liveCmd(t, m, "1m", g); got != "**purge** · live\n✓ each new message here goes 1m after it's sent" {
		t.Fatalf("on: %q", got)
	}
	if v, ok := m.live.Load(target{g, me}); !ok || v.(time.Duration) != time.Minute {
		t.Fatal("on didn't take effect")
	}

	// A restart: a new module reads it back once the gateway is ready.
	again := liveModule()
	again.useDB(db)
	again.OnEvent(&events.Ready{GenericEvent: events.NewGenericEvent(&bot.Client{Rest: newFake()}, 0, 0)})
	eventually(t, "the live set loaded", func() bool {
		v, ok := again.live.Load(target{g, me})
		return ok && v.(time.Duration) == time.Minute
	})

	if got := liveCmd(t, m, "off", g); got != "**purge** · live\n✓ live is off; messages here stay up" {
		t.Fatalf("off: %q", got)
	}
	// Off is remembered too: the next restart doesn't bring it back.
	third := liveModule()
	third.useDB(db)
	if err := third.loadLive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := third.live.Load(target{g, me}); ok {
		t.Fatal("off didn't persist")
	}
}

// BenchmarkOnEventNotLive is the cost every message in every guild pays
// while someone somewhere is live: it must not allocate.
func BenchmarkOnEventNotLive(b *testing.B) {
	m, f := liveModule(), newFake()
	m.setLive(target{guildID, me}, time.Minute)
	e := posted(f, guildID, them, textCh, 900)
	b.ReportAllocs()
	for b.Loop() {
		m.OnEvent(e)
	}
}
