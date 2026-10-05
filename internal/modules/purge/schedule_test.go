package purge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// subCmd is /purge <sub> <option>:<value> in guild.
func subCmd(t *testing.T, m *Module, sub, option, value string, guild snowflake.ID) string {
	return command(t, m, sub, func(p map[string]any) {
		p["guild_id"] = guild.String()
		o := map[string]any{"name": sub, "type": 1}
		if option != "" {
			o["options"] = []any{map[string]any{"name": option, "type": 3, "value": value}}
		}
		p["data"].(map[string]any)["options"] = []any{o}
	})
}

func TestEveryAndStatusWithoutADatabase(t *testing.T) {
	m := liveModule()
	if got := subCmd(t, m, "every", "every", "1d", guildID); got != "✗ "+string(errNoDB) {
		t.Errorf("every: %q", got)
	}
	if got := subCmd(t, m, "every", "every", "2d", guildID); got != "✗ "+string(errNotAChoice) {
		t.Errorf("every 2d: %q", got)
	}
	if got := subCmd(t, m, "status", "", "", guildID); got != "✗ "+string(errNoDB) {
		t.Errorf("status: %q", got)
	}
}

// The whole schedule: /purge every queues a sweep, the scheduler claims
// it and sweeps the guild for the member, and /purge status reads back how
// it went. A second member in the same guild rides the same sweep and
// sees only their own count.
func TestScheduledSweep(t *testing.T) {
	db := testDB(t)
	m := liveModule()
	m.useDB(db)
	g := freshGuild()
	if got := subCmd(t, m, "status", "", "", g); !strings.Contains(got, "last sweep   never") || !strings.Contains(got, "every        off") {
		t.Fatalf("status before anything:\n%s", got)
	}
	if got := subCmd(t, m, "every", "every", "1d", g); !strings.HasPrefix(got, "✓ every 1d") {
		t.Fatalf("every: %q", got)
	}
	// The other member is live, which queues them for the catch-up sweep.
	if err := m.saveLive(context.Background(), target{g, them}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `update purge_subs set next_run = now() where guild_id = $1`, int64(g)); err != nil {
		t.Fatal(err)
	}

	// Other tests' due rows get claimed too, and sweep their own guilds:
	// the fake has nothing for them.
	f := server(t, time.Now())
	f.guild = g
	m.due(f)
	eventually(t, "the scheduled sweep finished", func() bool {
		_, running := m.sweeping.Load(g)
		return !running && strings.Contains(subCmd(t, m, "status", "", "", g), "ago")
	})
	for ch, msgs := range f.msgs {
		for _, msg := range msgs {
			if ch != voiceCh && !f.gone[msg.ID] {
				t.Fatalf("message %d by %d survived a sweep for both of them", msg.ID, msg.Author.ID)
			}
		}
	}

	got := subCmd(t, m, "status", "", "", g)
	for _, want := range []string{"every        1d", "deleted      129", "next sweep   in 23h", "unreachable  1", "<#12>"} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
	var through int64
	if err := db.QueryRow(context.Background(), `select read_through from purge_channels where guild_id = $1 and channel_id = $2`, int64(g), int64(textCh)).Scan(&through); err != nil || through == 0 {
		t.Fatalf("read_through %d, %v: the catch-up didn't mark the channel", through, err)
	}

	// The live-only member's row has no schedule left once claimed.
	var next *time.Time
	if err := db.QueryRow(context.Background(), `select next_run from purge_subs where guild_id = $1 and user_id = $2`, int64(g), int64(them)).Scan(&next); err != nil || next != nil {
		t.Fatalf("live-only next_run %v, %v", next, err)
	}

	if got := subCmd(t, m, "every", "every", "off", g); got != "✓ no more scheduled sweeps here" {
		t.Fatalf("every off: %q", got)
	}
	if got := subCmd(t, m, "status", "", "", g); !strings.Contains(got, "next sweep   none") {
		t.Fatalf("status after off:\n%s", got)
	}
}

// A guild whose last sweep is still going is not swept twice at once; its
// members go back in the queue.
func TestBusyGuildIsRequeued(t *testing.T) {
	db := testDB(t)
	m := liveModule()
	m.useDB(db)
	g := freshGuild()
	if got := subCmd(t, m, "every", "every", "6h", g); !strings.HasPrefix(got, "✓") {
		t.Fatal(got)
	}
	m.sweeping.Store(g, struct{}{})
	m.due(newFake())
	var due bool
	if err := db.QueryRow(context.Background(), `select next_run <= now() from purge_subs where guild_id = $1`, int64(g)).Scan(&due); err != nil || !due {
		t.Fatalf("requeued %v, %v", due, err)
	}
	if got := subCmd(t, m, "status", "", "", g); !strings.Contains(got, "a sweep is running in this server") || !strings.Contains(got, "within a minute") {
		t.Errorf("status while busy:\n%s", got)
	}
}

// A guild that turned purge off skips its scheduled run rather than
// queueing it for when purge comes back.
func TestOffGuildIsNotSwept(t *testing.T) {
	db := testDB(t)
	m := liveModule()
	m.useDB(db)
	g := freshGuild()
	if got := subCmd(t, m, "every", "every", "6h", g); !strings.HasPrefix(got, "✓") {
		t.Fatal(got)
	}
	if _, err := db.Exec(context.Background(), `update purge_subs set next_run = now() where guild_id = $1`, int64(g)); err != nil {
		t.Fatal(err)
	}
	m.Gate(func(guild snowflake.ID) bool { return guild != g })
	m.due(newFake())
	if _, swept := m.sweeping.Load(g); swept {
		t.Fatal("swept a guild with purge off")
	}
	var later bool
	if err := db.QueryRow(context.Background(), `select next_run > now() from purge_subs where guild_id = $1`, int64(g)).Scan(&later); err != nil || !later {
		t.Fatalf("next run moved on %v, %v", later, err)
	}
}

// A job that stopped part way is recorded as stopped.
func TestStoppedSweepIsRecorded(t *testing.T) {
	db := testDB(t)
	m := liveModule()
	m.useDB(db)
	g := freshGuild()
	m.record(m.newJob(newFake(), g, []snowflake.ID{me}), context.Canceled)
	if got := subCmd(t, m, "status", "", "", g); !strings.Contains(got, "ago, stopped") {
		t.Errorf("status:\n%s", got)
	}
}

func TestChoiceName(t *testing.T) {
	odd := int32(90)
	if got := choiceName(delays, &odd); got != "1m" {
		t.Errorf("90s reads %q", got)
	}
}
