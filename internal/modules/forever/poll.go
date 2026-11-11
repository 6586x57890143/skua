package forever

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
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
		if !m.on(c.guild) {
			continue
		}
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
	feed := map[snowflake.ID][]moment{}
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
			ch.polled = now
			continue
		}
		stale := !ch.polled.IsZero() && now.Sub(ch.polled) > staleAfter
		*ch = f.now
		ch.missing, ch.polled = 0, now
		changed = append(changed, f.now)
		if stale {
			// Unread for a while (its server was switched off): what it did
			// meanwhile is old news, so this round is a quiet baseline.
			continue
		}
		for g, ms := range m.dings(f.was, f.now) {
			feed[g] = append(feed[g], ms...)
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
	charts := m.turnDay(now)
	m.mu.Unlock()

	for g, places := range charts {
		if err := saveDay(ctx, m.db, g, now.UTC().Format(time.DateOnly), places); err != nil {
			m.log.Warn("forever: keeping the day's chart", "err", err)
		}
	}

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
	if p.ID != c.id {
		// The name now belongs to another character: this one was renamed
		// or deleted, and that one isn't who was listed.
		f.err = errGone
		return f
	}
	f.news = true
	apply(&f.now, p)
	f.now.modified = mod
	if f.now.level > c.level {
		f.now.reached = reachedAt(p, m.now())
	}
	f.now.avatar, f.now.render = m.renders(ctx, f.now)
	f.now.crafts = m.crafts(ctx, f.now)
	return f
}

// crafts is c's crafting professions and their skill, or what it had when
// Blizzard doesn't answer.
func (m *Module) crafts(ctx context.Context, c char) map[string]int {
	var pr professions
	if _, _, err := m.api.get(ctx, c.region, "profile", charPath(c.slug())+"/professions", "", &pr); err != nil {
		return c.crafts
	}
	out := map[string]int{}
	for _, x := range pr.Primaries {
		name := strings.ToLower(x.Profession.Name)
		if !slices.Contains(crafting, name) {
			continue
		}
		for _, t := range x.Tiers {
			out[name] = max(out[name], t.Skill)
		}
	}
	return out
}

// reachedAt is when a character that climbed got there: its logout, which
// is when Blizzard saved the level, or now when that's missing or ahead of
// skua's clock.
func reachedAt(p profile, now time.Time) time.Time {
	if t := time.UnixMilli(p.Logout); p.Logout > 0 && !t.After(now) {
		return t
	}
	return now
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
	c.level, c.xp, c.ilvl = p.Level, p.XP, p.ILvl
	c.class, c.race = strings.ToLower(p.Class.Name), strings.ToLower(p.Race.Name)
	c.ruleset = strings.ToLower(p.Ruleset.Name)
}

// milestone is a level the feed tells: every ten, and Forever's Legacy
// class checkpoints, 25, 45 and 60.
func milestone(l int) bool { return l%10 == 0 || slices.Contains(classPoints, l) }

// moment is one thing the feed tells. A big one (a 60, or a server's first
// to a milestone) gets a card of its own with the character's portrait.
type moment struct {
	line string
	big  bool
	c    char
}

// dings is what was becoming now tells each server that lists it: the
// highest level milestone crossed, each crafting checkpoint crossed, a ✦
// where it earned its player a Legacy point (their first there) and
// whether it's the server's first. Called with m.mu held, after now is kept.
func (m *Module) dings(was, now char) map[snowflake.ID][]moment {
	top := 0
	for l := was.level + 1; l <= now.level; l++ {
		if milestone(l) {
			top = l
		}
	}
	out := map[snowflake.ID][]moment{}
	for _, c := range m.claims {
		if c.k != now.key || m.guilds[c.guild].feed == 0 || !m.on(c.guild) {
			continue
		}
		who := classMark(now.class) + "**" + fullName(now) + "**"
		tail := " · <@" + c.user.String() + ">"
		if top > 0 {
			line := who + " reached " + itoa(top) + " · " + where(now)
			if top == Cap && !now.reached.IsZero() {
				line += " · " + core.Duration(now.reached.Sub(Launch)) + " after launch"
			}
			for l := was.level + 1; l <= now.level; l++ {
				if slices.Contains(classPoints, l) && m.firstOfClass(c, now, l) {
					line += " · ✦ legacy point"
					break
				}
			}
			first := m.first(c.guild, now.key, top)
			if first {
				line += " · first in the server"
			}
			out[c.guild] = append(out[c.guild], moment{line: line + tail, big: top == Cap || first && top%10 == 0 && top >= 20, c: now})
		}
		for _, p := range crafting {
			for _, l := range craftPoints {
				if was.crafts[p] < l && now.crafts[p] >= l {
					line := who + " reached " + p + " " + itoa(l)
					if m.firstCraft(c, now, p, l) {
						line += " · ✦ legacy point"
					}
					out[c.guild] = append(out[c.guild], moment{line: line + tail, c: now})
				}
			}
		}
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

// firstCraft is firstOfClass for a profession's checkpoint.
func (m *Module) firstCraft(cl claim, now char, craft string, skill int) bool {
	for _, c := range m.claims {
		if c.guild != cl.guild || c.user != cl.user || c.k == now.key {
			continue
		}
		if ch := m.chars[c.k]; ch != nil && ch.crafts[craft] >= skill {
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

// turnDay keeps each server's ladder as the day starts (utc), the chart its
// moves are read against, and returns the ones it kept. m.mu held.
func (m *Module) turnDay(now time.Time) map[snowflake.ID]map[key]int {
	today := now.UTC().Format(time.DateOnly)
	out := map[snowflake.ID]map[key]int{}
	guilds := map[snowflake.ID]bool{}
	for _, c := range m.claims {
		guilds[c.guild] = true
	}
	for g := range guilds {
		if m.dayOf[g] == today {
			continue
		}
		places := map[key]int{}
		for i, c := range m.board(g) {
			places[c.key] = i + 1
		}
		m.days[g], m.dayOf[g] = places, today
		out[g] = places
	}
	return out
}

const (
	feedLines = 15 // the most one merged card carries
	bigCards  = 3  // the most cards of their own one round posts a server
)

// post sends each server its round: a card of its own for each big moment,
// with the character's portrait and its class's colour, and one card for
// the rest.
func (m *Module) post(ctx context.Context, p Poster, setups map[snowflake.ID]setup, feed map[snowflake.ID][]moment) {
	for g, ms := range feed {
		to := setups[g].feed
		if to == 0 || len(ms) == 0 {
			continue
		}
		var cards []discord.ContainerComponent
		var lines []string
		for _, x := range ms {
			if x.big && len(cards) < bigCards {
				cards = append(cards, bigCard(x))
				continue
			}
			lines = append(lines, x.line)
		}
		if len(lines) > feedLines {
			lines = append(lines[:feedLines], fmt.Sprintf("-# and %d more", len(lines)-feedLines))
		}
		if len(lines) > 0 {
			cards = append(cards, brand.Card(brand.ColorOK, Name, "levels", strings.Join(lines, "\n")))
		}
		for _, c := range cards {
			if err := m.guard.Allow(g, guard.MessageSend); err != nil {
				break
			}
			_, err := p.CreateMessage(to, discord.MessageCreate{
				Components:      []discord.LayoutComponent{c},
				Flags:           discord.MessageFlagIsComponentsV2,
				AllowedMentions: core.NoPings(),
			}, rest.WithCtx(ctx))
			m.guard.Done(g, err)
			if err != nil {
				m.log.Warn("forever: posting the feed", "guild", g, "err", err)
				break
			}
		}
	}
}

// bigCard is a moment standing alone: the head and the moment beside the
// character's portrait, in its class's colour.
func bigCard(x moment) discord.ContainerComponent {
	what := "levels"
	if x.c.level >= Cap {
		what = "level " + itoa(Cap)
	}
	head := discord.NewTextDisplay(headLine(what) + "\n" + x.line)
	return discord.NewContainer(section(head, x.c.avatar)).WithAccentColor(accent(x.c.class))
}
