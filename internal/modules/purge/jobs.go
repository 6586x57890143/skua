package purge

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
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
	if !m.bootstrap.Has(by) {
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
		key   string
		text  string
		user  snowflake.ID
		guild snowflake.ID
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
		running = append(running, line{key: name(t.guild) + t.user.String(), text: fmt.Sprintf("<@%d> in %s · %s · %s · %s",
			t.user, name(t.guild), how, span(now.Sub(r.job.began)), state), user: t.user, guild: t.guild})
		return true
	})
	m.catching.Range(func(k, v any) bool {
		g, c := k.(snowflake.ID), v.(*catchup)
		progress := "listing channels"
		if c.scan.listed.Load() {
			progress = fmt.Sprintf("channels %s · %d scanned", c.scan.progress(), c.scan.scanned.Load())
		}
		catching = append(catching, line{key: name(g), text: fmt.Sprintf("reading %s · %s", name(g), progress)})
		return true
	})
	for _, l := range [][]line{running, catching} {
		slices.SortFunc(l, func(a, b line) int { return strings.Compare(a.key, b.key) })
	}

	var upcoming []line
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
			upcoming = append(upcoming, line{key: name(snowflake.ID(g)) + snowflake.ID(u).String(), text: fmt.Sprintf("<@%d> in %s · %s", u, name(snowflake.ID(g)), strings.Join(facts, " · ")), user: snowflake.ID(u), guild: snowflake.ID(g)})
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
	var lines []line
	lines = append(lines, append(running, catching...)...)
	lines = append(lines, upcoming...)
	users := make([]snowflake.ID, 0, 25)
	userGuilds := map[snowflake.ID][]snowflake.ID{}
	for i, l := range lines {
		if len(text)+len(l.text)+4 > replyMax {
			text += fmt.Sprintf("\n-# and %d more", len(lines)-i)
			break
		}
		text += "\n-# " + l.text
		if l.user != 0 {
			if _, seen := userGuilds[l.user]; !seen {
				users = append(users, l.user)
			}
			userGuilds[l.user] = append(userGuilds[l.user], l.guild)
		}
	}
	var extra []discord.ContainerSubComponent
	if len(users) > 25 {
		extra = append(extra, discord.NewTextDisplay("-# dropdown shows the first 25 members listed"))
		users = users[:25]
	}
	if len(users) > 0 {
		options := make([]discord.StringSelectMenuOption, 0, len(users))
		labels := make([]string, len(users))
		var wg sync.WaitGroup
		for i, user := range users {
			wg.Add(1)
			go func() {
				defer wg.Done()
				labels[i] = memberOptionName(e, user, userGuilds[user])
			}()
		}
		wg.Wait()
		for i, user := range users {
			options = append(options, discord.NewStringSelectMenuOption(labels[i], user.String()))
		}
		menu := discord.NewStringSelectMenu(jobsDropMenu, "drop a member's purge jobs", options...).WithMaxValues(1)
		extra = append(extra, discord.NewActionRow(menu))
	}
	return reply(e, brand.ColorInfo, "jobs", text, extra...)
}

// dropJob withdraws every queued purge for one member, across servers.
// Historical sweep results remain available to /purge status.
func (m *Module) dropJob(ctx context.Context, e *events.ComponentInteractionCreate) error {
	by := e.User().ID
	if !m.bootstrap.Has(by) {
		return errJobsNotYours
	}
	selected := e.StringSelectMenuInteractionData().Values
	if len(selected) != 1 {
		return core.Tell("pick one member; run /purge jobs again if that card is out of date")
	}
	user, err := snowflake.Parse(selected[0])
	if err != nil || user == 0 {
		return core.Tell("that jobs card is out of date; run /purge jobs again")
	}
	m.log.Warn("purge: break-glass", "sub", "jobs drop", "by", by, "for", user)
	dropped, err := m.dropJobs(ctx, user)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("✓ dropped <@%d>'s purge jobs across servers · %d saved settings cleared, %d live timers removed, %d running purges stopped; messages already deleted stay deleted", user, dropped.settings, dropped.live, dropped.running)
	components := []discord.LayoutComponent{brand.Card(brand.ColorWarn, "purge", "jobs", text)}
	return e.UpdateMessage(core.Edit(discord.MessageUpdate{Components: &components, AllowedMentions: core.NoPings()}))
}

type dropCount struct {
	settings      int64
	live, running int
}

func (m *Module) dropJobs(ctx context.Context, user snowflake.ID) (dropCount, error) {
	m.settings.Lock()
	defer m.settings.Unlock()

	var dropped dropCount
	if m.db != nil {
		tag, err := m.db.Exec(ctx, `update purge_subs set live_delay_s = null, every_s = null,
			next_run = null, claimed = false, now_since = null,
			live_channels = '{}', every_channels = '{}'
			where user_id = $1 and (live_delay_s is not null or every_s is not null or next_run is not null or claimed or now_since is not null)`, int64(user))
		if err != nil {
			return dropCount{}, err
		}
		dropped.settings = tag.RowsAffected()
	}

	m.live.Range(func(key, _ any) bool {
		k := key.(target)
		if k.user == user {
			m.setLive(k, 0, nil)
			dropped.live++
		}
		return true
	})
	m.running.Range(func(key, value any) bool {
		k := key.(target)
		if k.user == user {
			value.(*run).cancel()
			dropped.running++
		}
		return true
	})

	return dropped, nil
}

func memberOptionName(e *events.ApplicationCommandInteractionCreate, user snowflake.ID, guilds []snowflake.ID) string {
	name := "unknown user"
	if caches := e.Client().Caches; caches != nil {
		for _, guild := range guilds {
			if member, ok := caches.Member(guild, user); ok && member.User.Username != "" {
				name = member.User.Username
				break
			}
		}
	}
	if name == "unknown user" && e.Client().Rest != nil {
		if fetched, err := e.Client().Rest.GetUser(user); err == nil && fetched != nil && fetched.Username != "" {
			name = fetched.Username
		}
	}
	id := user.String()
	if len(id) > 6 {
		id = id[:3] + "..." + id[len(id)-3:]
	}
	return name + " · " + id
}

