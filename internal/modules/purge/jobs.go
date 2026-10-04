package purge

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// replyMax is Discord's 2000 characters, less room for the line that
// counts what didn't fit.
const replyMax = 1950

var errJobsNotYours = core.Tell("only skua's break-glass admin can see every purge")

// jobsCmd is the break-glass admin's view of every purge in every server:
// what is deleting now, which indexes are catching up, and what is
// scheduled or live. Every use is logged.
func (m *Module) jobsCmd(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	by := e.User().ID
	if m.bootstrap == 0 || by != m.bootstrap {
		return errJobsNotYours
	}
	m.log.Warn("purge: break-glass", "sub", "jobs", "by", by)
	name := func(g snowflake.ID) string {
		if c := e.Client().Caches; c != nil {
			if guild, ok := c.Guild(g); ok {
				return guild.Name
			}
		}
		return g.String()
	}
	now := m.now()

	type line struct {
		key  string
		text string
	}
	var running, catching []line
	m.running.Range(func(k, v any) bool {
		t, r := k.(target), v.(*run)
		how := "now"
		if r.scheduled {
			how = "scheduled"
		}
		// A scheduled job's total covers every member in it, so only a
		// single member's purge shows one.
		state := "reading what's new"
		if s := r.job.sweep; r.job.deleting.Load() {
			var n int64
			if c := s.authors[t.user]; c != nil {
				n = c.Load()
			}
			state = fmt.Sprintf("%d deleted", n)
			if len(s.authors) == 1 {
				state = fmt.Sprintf("%d of %d deleted %s", n, s.total.Load(), bar(s.handled.Load(), s.total.Load()))
			}
		}
		running = append(running, line{name(t.guild) + t.user.String(), fmt.Sprintf("<@%d> in %s · %s · %s · %s",
			t.user, name(t.guild), how, span(now.Sub(r.job.began)), state)})
		return true
	})
	m.catching.Range(func(k, v any) bool {
		g, c := k.(snowflake.ID), v.(*catchup)
		progress := "listing channels"
		if c.scan.listed.Load() {
			progress = fmt.Sprintf("channels %s · %d scanned", c.scan.progress(), c.scan.scanned.Load())
		}
		catching = append(catching, line{name(g), fmt.Sprintf("reading %s · %s", name(g), progress)})
		return true
	})
	for _, l := range [][]line{running, catching} {
		slices.SortFunc(l, func(a, b line) int { return strings.Compare(a.key, b.key) })
	}

	var upcoming []string
	var scheduled, live int
	if m.db != nil {
		rows, err := m.db.Query(ctx, `select guild_id, user_id, every_s, next_run, live_delay_s from purge_subs
			where every_s is not null or live_delay_s is not null
			order by next_run nulls last, guild_id, user_id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var g, u int64
			var every, liveS *int32
			var next *time.Time
			if err := rows.Scan(&g, &u, &every, &next, &liveS); err != nil {
				rows.Close()
				return err
			}
			var facts []string
			if every != nil {
				scheduled++
				when := "due now"
				if next != nil && next.After(now) {
					when = "next in " + span(next.Sub(now))
				}
				facts = append(facts, "every "+choiceName(everyChoices, every)+", "+when)
			}
			if liveS != nil {
				live++
				facts = append(facts, "live "+choiceName(delays, liveS))
			}
			upcoming = append(upcoming, fmt.Sprintf("<@%d> in %s · %s", u, name(snowflake.ID(g)), strings.Join(facts, " · ")))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}

	text := grid([][2]string{
		{"running", fmt.Sprint(len(running))},
		{"catching up", fmt.Sprint(len(catching))},
		{"scheduled", fmt.Sprint(scheduled)},
		{"live", fmt.Sprint(live)},
	})
	if m.db == nil {
		text += "\n-# no database here, so nothing is scheduled or live"
	}
	var lines []string
	for _, l := range append(running, catching...) {
		lines = append(lines, l.text)
	}
	lines = append(lines, upcoming...)
	for i, l := range lines {
		if len(text)+len(l)+4 > replyMax {
			text += fmt.Sprintf("\n-# and %d more", len(lines)-i)
			break
		}
		text += "\n-# " + l
	}
	return reply(e, text)
}
