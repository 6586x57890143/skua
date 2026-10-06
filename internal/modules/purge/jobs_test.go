package purge

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

func TestJobsIsBreakGlassOnly(t *testing.T) {
	m := liveModule()
	if got := subCmd(t, m, "jobs", "", "", guildID); got != "✗ "+string(errJobsNotYours) {
		t.Errorf("no admin set: %q", got)
	}
	m.bootstrap = them
	if got := subCmd(t, m, "jobs", "", "", guildID); got != "✗ "+string(errJobsNotYours) {
		t.Errorf("someone else is the admin: %q", got)
	}
}

// Every running purge and catch-up, in every guild, without a database.
func TestJobsListsWhatIsRunning(t *testing.T) {
	m := liveModule()
	m.bootstrap = me
	f := newFake()
	j := m.newJob(f, 40, []snowflake.ID{them}, "test purge for")
	j.sweep.authors[them].Store(12)
	j.sweep.total.Store(48)
	j.sweep.handled.Store(24)
	j.deleting.Store(true)
	m.running.Store(target{40, them}, &run{func() {}, j, true})
	m.running.Store(target{40, me}, &run{func() {}, m.newJob(f, 40, []snowflake.ID{me}, "test purge for"), false})
	m.catching.Store(snowflake.ID(41), &catchup{scan: &scan{}})
	read := &scan{}
	read.channels.Store(4)
	read.done.Store(1)
	read.scanned.Store(900)
	read.listed.Store(true)
	m.catching.Store(snowflake.ID(42), &catchup{scan: read})

	got := subCmd(t, m, "jobs", "", "", guildID)
	for _, want := range []string{
		"running      2", "catching up  2", "scheduled    0",
		"<@6> in 40 · scheduled · 0s · 12 of 48 deleted ▰▰▰▰▱▱▱▱",
		"<@5> in 40 · now · 0s · reading what's new",
		"reading 41 · listing channels",
		"reading 42 · channels 1 of 4 ▰▰▱▱▱▱▱▱ · 900 scanned",
		"no database here",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("jobs lacks %q:\n%s", want, got)
		}
	}
}

// A reply never passes Discord's limit: what doesn't fit is counted.
func TestJobsFitsOneReply(t *testing.T) {
	m := liveModule()
	m.bootstrap = me
	f := newFake()
	for i := range 200 {
		g := snowflake.ID(1000 + i)
		m.running.Store(target{g, them}, &run{func() {}, m.newJob(f, g, []snowflake.ID{them}, "test purge for"), false})
	}
	got := subCmd(t, m, "jobs", "", "", guildID)
	if len(got) > 2000 || !strings.Contains(got, " more") {
		t.Errorf("%d characters:\n%s", len(got), got)
	}
}

// What is scheduled or live comes from the database.
func TestJobsListsWhatIsComingUp(t *testing.T) {
	db := testDB(t)
	m := liveModule()
	m.useDB(db)
	m.bootstrap = me
	g := freshGuild()
	if got := subCmd(t, m, "every", "every", "1d", g); !strings.HasPrefix(got, "**purge** · every\n✓") {
		t.Fatal(got)
	}
	if _, err := db.Exec(context.Background(), `update purge_subs set next_run = now() + interval '3 hours' where guild_id = $1`, int64(g)); err != nil {
		t.Fatal(err)
	}
	if err := m.saveLive(context.Background(), target{g, them}, time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	got := subCmd(t, m, "jobs", "", "", guildID)
	for _, want := range []string{
		fmt.Sprintf("<@%d> in %d · every 1d, next in 2h 59m", me, g),
		fmt.Sprintf("<@%d> in %d · live 1m", them, g),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("jobs lacks %q:\n%s", want, got)
		}
	}
}
