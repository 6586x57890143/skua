package forever

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

func (m *Module) loop(ctx context.Context) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		rctx, cancel := context.WithTimeout(ctx, every)
		m.poll(rctx)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// fetched is one character's poll: what it is now, or why not.
type fetched struct {
	was  char
	now  char
	err  error
	news bool
}

// poll reads every listed character once, keeps what changed and posts
// each server's feed.
func (m *Module) poll(ctx context.Context) {
	m.mu.Lock()
	var todo []char
	seen := map[key]bool{}
	for _, c := range m.claims {
		if ch, ok := m.chars[c.k]; ok && !seen[c.k] {
			seen[c.k] = true
			todo = append(todo, *ch)
		}
	}
	m.mu.Unlock()

	out := make([]fetched, len(todo))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range inFlight {
		wg.Go(func() {
			for i := range jobs {
				out[i] = m.refresh(ctx, todo[i])
			}
		})
	}
	for i := range todo {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	now := m.now()
	hs := map[string]health{}
	var changed []char
	feed := map[snowflake.ID][]string{}
	m.mu.Lock()
	for _, f := range out {
		h := hs[f.was.region]
		switch {
		case errors.Is(f.err, errGone):
			h.gone++
		case f.err != nil:
			h.err = f.err
		default:
			h.ok = now
		}
		hs[f.was.region] = h
		ch, ok := m.chars[f.was.key]
		if !ok {
			continue // dropped while it was read
		}
		if f.err != nil {
			if errors.Is(f.err, errGone) {
				ch.missing++
			}
			continue
		}
		ch.missing = 0
		if !f.news {
			continue
		}
		*ch = f.now
		changed = append(changed, f.now)
		for g, lines := range m.dings(f.was, f.now) {
			feed[g] = append(feed[g], lines...)
		}
	}
	for r, h := range hs {
		if h.ok.IsZero() {
			h.ok = m.health[r].ok // a failing round keeps the last good one
		}
		m.health[r] = h
	}
	m.round = now
	p := m.rest
	setups := maps.Clone(m.guilds)
	m.mu.Unlock()

	for _, c := range changed {
		if err := saveChar(ctx, m.db, c); err != nil {
			m.log.Warn("forever: saving a character", "err", err)
		}
	}
	for _, f := range out {
		if f.news && f.now.level > f.was.level {
			if err := saveDings(ctx, m.db, f.now.key, f.was.level, f.now.level, f.now.reached); err != nil {
				m.log.Warn("forever: saving levels", "err", err)
			}
		}
	}
	if p != nil {
		m.post(ctx, p, setups, feed)
	}
}

// refresh reads c's profile, and its renders when the profile changed.
func (m *Module) refresh(ctx context.Context, c char) fetched {
	f := fetched{was: c, now: c}
	var p profile
	mod, changed, err := m.api.get(ctx, c.region, "profile", charPath(c.slug()), c.modified, &p)
	if err != nil || !changed {
		f.err = err
		return f
	}
	if p.ID != 0 && p.ID != c.id {
		// The name now belongs to another character: this one was renamed
		// or deleted, and that one isn't who was listed.
		f.err = errGone
		return f
	}
	f.news = true
	apply(&f.now, p)
	f.now.modified = mod
	if f.now.level > c.level {
		f.now.reached = m.now()
	}
	f.now.avatar, f.now.render = m.renders(ctx, f.now)
	return f
}

// renders is Blizzard's art of c: its portrait and its full render, or
// what it had when Blizzard has none yet.
func (m *Module) renders(ctx context.Context, c char) (avatar, render string) {
	var md media
	if _, _, err := m.api.get(ctx, c.region, "profile", charPath(c.slug())+"/character-media", "", &md); err != nil {
		return c.avatar, c.render
	}
	avatar, render = c.avatar, c.render
	for _, a := range md.Assets {
		if !strings.HasPrefix(a.Value, "https://") {
			continue
		}
		switch a.Key {
		case "avatar":
			avatar = a.Value
		case "main-raw":
			render = a.Value
		}
	}
	return avatar, render
}

// apply is p over c.
func apply(c *char, p profile) {
	c.id = p.ID
	c.name, c.surname = p.Name, p.Surname
	c.level, c.points, c.ilvl = p.Level, p.Points, p.ILvl
	c.class, c.classID, c.race = strings.ToLower(p.Class.Name), p.Class.ID, strings.ToLower(p.Race.Name)
	c.ruleset = strings.ToLower(p.Ruleset.Name)
	c.faction = strings.ToLower(p.Faction.Type)
}

// milestone is a level the feed tells: every ten, and Forever's Legacy
// class checkpoints, 25, 45 and 60.
func milestone(l int) bool { return l%10 == 0 || legacy(l) }

// legacy is a Legacy checkpoint: the first character of a class to 25, 45
// and 60 earns its account a Legacy point.
func legacy(l int) bool { return l == 25 || l == 45 || l == Cap }

// dings is the feed's lines for was climbing to now, per server that lists
// it: the highest milestone crossed, whether it earned its player a Legacy
// point (their first of that class there) and whether it's the server's
// first there. Called with m.mu held, after now is kept.
func (m *Module) dings(was, now char) map[snowflake.ID][]string {
	if now.level <= was.level {
		return nil
	}
	top := 0
	for l := was.level + 1; l <= now.level; l++ {
		if milestone(l) {
			top = l
		}
	}
	if top == 0 {
		return nil
	}
	out := map[snowflake.ID][]string{}
	for _, c := range m.claims {
		if c.k != now.key || m.guilds[c.guild].feed == 0 {
			continue
		}
		line := classMark(now.class) + "**" + now.name + " " + now.surname + "** reached " + itoa(top) + " · " + where(now)
		if top == Cap {
			line += " · " + core.Duration(now.reached.Sub(Launch)) + " after launch"
		}
		if legacy(top) && m.firstOfClass(c, now, top) {
			line += " · ✦ legacy point"
		}
		if m.first(c.guild, now.key, top) {
			line += " · first in the server"
		}
		out[c.guild] = append(out[c.guild], line+" · <@"+c.user.String()+">")
	}
	return out
}

// firstOfClass is whether none of the member's other characters here of
// now's class had reached level: Legacy is account wide, and skua sees a
// member's listed characters as their account.
func (m *Module) firstOfClass(cl claim, now char, level int) bool {
	for _, c := range m.claims {
		if c.guild != cl.guild || c.user != cl.user || c.k == now.key {
			continue
		}
		if ch := m.chars[c.k]; ch != nil && ch.class == now.class && ch.level >= level {
			return false
		}
	}
	return true
}

// first is whether no other character this server lists has reached level.
func (m *Module) first(guild snowflake.ID, k key, level int) bool {
	for _, c := range m.claims {
		if c.guild == guild && c.k != k {
			if ch := m.chars[c.k]; ch != nil && ch.level >= level {
				return false
			}
		}
	}
	return true
}

// feedLines is the most one feed card carries.
const feedLines = 15

// post sends each server one card with everything its round brought.
func (m *Module) post(ctx context.Context, p Poster, setups map[snowflake.ID]setup, feed map[snowflake.ID][]string) {
	for g, lines := range feed {
		to := setups[g].feed
		if to == 0 || len(lines) == 0 {
			continue
		}
		if len(lines) > feedLines {
			lines = append(lines[:feedLines], fmt.Sprintf("-# and %d more", len(lines)-feedLines))
		}
		if err := m.guard.Allow(g, guard.MessageSend); err != nil {
			continue
		}
		_, err := p.CreateMessage(to, discord.MessageCreate{
			Components:      []discord.LayoutComponent{brand.Card(brand.ColorOK, Name, "levels", strings.Join(lines, "\n"))},
			Flags:           discord.MessageFlagIsComponentsV2,
			AllowedMentions: core.NoPings(),
		}, rest.WithCtx(ctx))
		m.guard.Done(g, err)
		if err != nil {
			m.log.Warn("forever: posting the feed", "guild", g, "err", err)
		}
	}
}
