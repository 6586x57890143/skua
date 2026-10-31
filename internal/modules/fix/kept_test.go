package fix

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/store"
)

// hideRefused posts cards but answers every preview-hiding write with err.
type hideRefused struct {
	recordingRest
	hideErr error
}

func (r *hideRefused) UpdateMessage(c, m snowflake.ID, msg discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error) {
	if msg.Flags != nil && msg.Components == nil {
		return nil, r.hideErr
	}
	return r.recordingRest.UpdateMessage(c, m, msg, opts...)
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func keptModule() (*Module, *syncBuffer) {
	m := module()
	logs := &syncBuffer{}
	m.log = slog.New(slog.NewTextHandler(logs, nil))
	return m, logs
}

func reportRow(m *Module, g snowflake.ID, label string) string {
	for _, row := range m.Report(g).Rows {
		if row[0] == label {
			return row[1]
		}
	}
	return ""
}

// A card beside a preview skua couldn't hide is logged with who posted it,
// once per version of the message, and counted on the report.
func TestKeptPreviewIsLoggedAndCounted(t *testing.T) {
	m, logs := keptModule()
	r := &recordingRest{}
	key := source{3, 4, 10}
	m.queueBy(r, key, 77, "https://x.com/user/status/123 and https://example.com/other", false)
	idle(t, m)
	line := logs.String()
	for _, want := range []string{"source preview kept", "author=77", "message=10", `reason="other links in message"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("log %q lacks %q", line, want)
		}
	}
	if got := reportRow(m, 3, "previews kept, other links in message"); got != "1" {
		t.Fatalf("report row %q", got)
	}
	// An edit that changes nothing doesn't count it again.
	m.queue(r, key, "https://x.com/user/status/123 and https://example.com/other", true)
	idle(t, m)
	if n := strings.Count(logs.String(), "source preview kept"); n != 1 {
		t.Fatalf("logged %d times", n)
	}
}

func TestFailedHideNamesItsReason(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
	}{{discordError(403), "no manage messages"}, {discordError(500), "discord error"}} {
		m, logs := keptModule()
		r := &hideRefused{hideErr: tc.err}
		m.queueBy(r, source{3, 4, 10}, 77, "https://x.com/user/status/123", false)
		idle(t, m)
		if got := reportRow(m, 3, "previews kept, "+tc.reason); got != "1" {
			t.Fatalf("%s: report row %q, log %s", tc.reason, got, logs.String())
		}
	}
	// After a 403 the channel isn't asked again, and that is counted too.
	m, _ := keptModule()
	r := &hideRefused{hideErr: discordError(403)}
	m.queueBy(r, source{3, 4, 10}, 77, "https://x.com/user/status/123", false)
	idle(t, m)
	m.queueBy(r, source{3, 4, 11}, 78, "https://x.com/user/status/124", false)
	idle(t, m)
	if got := reportRow(m, 3, "previews kept, no manage messages"); got != "2" {
		t.Fatalf("report row %q", got)
	}
	if keptReason(true, nil) != "hourly cap" {
		t.Fatal("a guard denial is the hourly cap")
	}
}

// The counts survive a restart for keptFor days, against Postgres from
// SKUA_TEST_DATABASE_URL as CI runs it.
func TestKeptCountsOutliveRestart(t *testing.T) {
	dsn := os.Getenv("SKUA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKUA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	if _, err := pool.Exec(ctx, "insert into fix_kept (guild_id, day, reason, n) values ($1, $2, 'discord error', 5)", int64(g), today()-keptFor-1); err != nil {
		t.Fatal(err)
	}
	first, _ := keptModule()
	if err := first.Keep(ctx, pool); err != nil {
		t.Fatal(err)
	}
	first.noteKept(source{g, 4, 10}, 77, "incomplete preview", nil)
	first.noteKept(source{g, 4, 11}, 77, "incomplete preview", nil)
	restarted, _ := keptModule()
	if err := restarted.Keep(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if got := reportRow(restarted, g, "previews kept, incomplete preview"); got != "2" {
		t.Fatalf("after restart %q", got)
	}
	if got := reportRow(restarted, g, "previews kept, discord error"); got != "" {
		t.Fatalf("a row past %d days survived: %q", keptFor, got)
	}
	if err := restarted.Keep(ctx, nil); err != nil {
		t.Fatal("no database is not an error")
	}
}
