// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/concord"
	"github.com/6586x57890143/skua/internal/core"
)

// oneWay is how long a link may send out with nothing coming back before
// it is worth a warning. Only a warning: a quiet channel is normal.
const oneWay = time.Hour

// tally is what one link has done since skua started, one direction each
// way, read by its server's admins on armada's /help page.
type tally struct {
	in, out, failed           atomic.Int64 // messages into Discord, out to Armada, and failures
	lastIn, lastOut, lastFail atomic.Int64 // unix ms; 0 is never
	// outSinceIn is when the first message went out after the last one
	// came in; 0 when nothing has gone out since.
	outSinceIn atomic.Int64
}

func (t *tally) crossedIn() {
	t.in.Add(1)
	t.lastIn.Store(time.Now().UnixMilli())
	t.outSinceIn.Store(0)
}

func (t *tally) crossedOut() {
	now := time.Now().UnixMilli()
	t.out.Add(1)
	t.lastOut.Store(now)
	t.outSinceIn.CompareAndSwap(0, now)
}

func (t *tally) fail() { t.failed.Add(1); t.lastFail.Store(time.Now().UnixMilli()) }

// The verdicts, best first.
const (
	healthOK       = "ok"
	healthDegraded = "degraded"
	healthOutage   = "outage"
)

// verdict is the bridge's health in one server, and why, worst reason first.
type verdict struct {
	level string
	why   string
}

// snapshot is everything health reads, taken at once.
type snapshot struct {
	comm    *concord.Community
	folded  *concord.Folded
	chans   map[string]concord.ChatChannel
	drift   map[string]concord.Drift
	failing string
	relays  []concord.RelayHealth
}

func (m *Module) snapshot() snapshot {
	m.mu.RLock()
	s := snapshot{comm: m.comm, folded: m.folded, chans: m.chans, drift: m.drift, failing: m.failing}
	pool := m.pool
	m.mu.RUnlock()
	if pool != nil {
		s.relays = pool.Health()
	}
	return s
}

// linksIn is the links that end in guild.
func (m *Module) linksIn(guild snowflake.ID) []*link {
	var here []*link
	for _, l := range m.links {
		if snowflake.ID(l.guild.Load()) == guild {
			here = append(here, l)
		}
	}
	return here
}

// channelName is a link's Armada channel by name, else its id's start.
func (s snapshot) channelName(l *link) string {
	if s.folded != nil {
		if ch, ok := s.folded.Channels[l.armada]; ok {
			return ch.Name
		}
	}
	return l.armada[:8]
}

// health is one verdict for the links in here, from what skua already holds:
// whether the invite reads, whether any relay is up, whether each linked
// channel is readable on its current key, and whether a link only talks one
// way. An outage is something that stops messages crossing; degraded is
// something that may.
func (s snapshot) health(here []*link, now time.Time) verdict {
	var outage, degraded []string
	switch {
	case s.comm == nil && s.failing != "":
		outage = append(outage, "can't read the invite: "+s.failing)
	case s.comm == nil:
		degraded = append(degraded, "still connecting")
	case s.failing != "":
		degraded = append(degraded, "the invite stopped reading: "+s.failing)
	}
	if len(s.relays) > 0 {
		up := 0
		for _, r := range s.relays {
			if r.Up {
				up++
			}
		}
		switch {
		case up == 0:
			outage = append(outage, "no relay is reachable")
		case up < len(s.relays):
			degraded = append(degraded, fmt.Sprintf("%d of %d relays are down", len(s.relays)-up, len(s.relays)))
		}
	}
	for _, l := range here {
		name := "#" + s.channelName(l)
		if s.comm == nil {
			continue
		}
		if _, ok := s.chans[l.armada]; !ok {
			outage = append(outage, name+" isn't readable: grant skua a role that sees it")
			continue
		}
		if d, ok := s.drift[l.armada]; ok {
			if d.Stale {
				outage = append(outage, name+": "+d.Why)
			} else {
				degraded = append(degraded, name+": "+d.Why)
			}
		}
		if since := l.tally.outSinceIn.Load(); since > 0 && now.Sub(time.UnixMilli(since)) >= oneWay {
			degraded = append(degraded, fmt.Sprintf("%s: messages have gone out for %s with nothing back", name, core.Duration(now.Sub(time.UnixMilli(since)))))
		}
	}
	switch {
	case len(outage) > 0:
		return verdict{healthOutage, outage[0]}
	case len(degraded) > 0:
		return verdict{healthDegraded, degraded[0]}
	}
	return verdict{level: healthOK}
}

// Report is the bridge as guild's admins see it: one verdict first, then the
// community, the relays and each link that ends in guild. Links into other
// servers are not mentioned at all.
func (m *Module) Report(guild snowflake.ID) core.Report {
	here := m.linksIn(guild)
	if len(here) == 0 {
		return core.Report{}
	}
	s := m.snapshot()
	now := time.Now()
	v := s.health(here, now)
	r := core.Report{Rows: [][2]string{{"health", v.level}}}
	switch v.level {
	case healthOutage:
		r.Notes = append(r.Notes, "✗ outage: "+v.why)
	case healthDegraded:
		r.Notes = append(r.Notes, "! degraded: "+v.why)
	}
	if s.comm != nil {
		r.Rows = append(r.Rows, [2]string{"community", s.comm.Name})
	}
	if s.relays != nil {
		r.Rows, r.Notes = relayHealth(s.relays, now, r.Rows, r.Notes)
	}
	for _, l := range here {
		state := "readable"
		if _, ok := s.chans[l.armada]; !ok {
			state = "not readable"
		} else if k, ok := s.comm.Private[l.armada]; ok {
			state += fmt.Sprintf(" · key epoch %d", k.Epoch)
		}
		r.Notes = append(r.Notes, fmt.Sprintf("<#%s> ↔ #%s · %s", l.discord, s.channelName(l), state))
		if d, ok := s.drift[l.armada]; ok {
			mark := "!"
			if d.Stale {
				mark = "✗"
			}
			r.Notes = append(r.Notes, fmt.Sprintf("%s %s (skua holds epoch %d)", mark, d.Why, d.Epoch))
		}
		line := fmt.Sprintf("-# %d in · %d out", l.tally.in.Load(), l.tally.out.Load())
		if last := max(l.tally.lastIn.Load(), l.tally.lastOut.Load()); last > 0 {
			line += " · last " + core.Duration(now.Sub(time.UnixMilli(last))) + " ago"
		}
		if n := l.tally.failed.Load(); n > 0 {
			line += fmt.Sprintf(" · %d failed, last %s ago", n, core.Duration(now.Sub(time.UnixMilli(l.tally.lastFail.Load()))))
		}
		r.Notes = append(r.Notes, line+" · since skua started")
	}
	return r
}

// watchHealth logs the verdict for each server with a link whenever it
// changes, a warning while it isn't ok, so the log holds the evidence of an
// outage even when nobody opened the page and a restart wipes the counters.
func (m *Module) watchHealth(every time.Duration) {
	last := map[snowflake.ID]verdict{}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		}
		s := m.snapshot()
		now := time.Now()
		guilds := map[snowflake.ID]bool{}
		for _, l := range m.links {
			if g := snowflake.ID(l.guild.Load()); g != 0 {
				guilds[g] = true
			}
		}
		for g := range guilds {
			v := s.health(m.linksIn(g), now)
			prev, seen := last[g]
			if seen && prev == v || !seen && v.level == healthOK {
				last[g] = v
				continue
			}
			last[g] = v
			if v.level == healthOK {
				m.log.Info("armada: health ok again", "guild", g)
			} else {
				m.log.Warn("armada: health "+v.level, "guild", g, "why", v.why)
			}
		}
	}
}

// relayHealth is how skua's sockets to the community's relays are: how many
// are up, how long since the quietest of them was heard from (pings go every
// 30s, so much past that means trouble), how many have died under use, and
// which are down.
func relayHealth(hs []concord.RelayHealth, now time.Time, rows [][2]string, notes []string) ([][2]string, []string) {
	up, drops := 0, 0
	var quietest time.Time
	for _, h := range hs {
		drops += h.Drops
		if !h.Up {
			if h.Heard.IsZero() {
				notes = append(notes, "! "+h.URL+" is not connected")
			} else {
				notes = append(notes, fmt.Sprintf("! %s is down, last heard %s ago", h.URL, core.Duration(now.Sub(h.Heard))))
			}
			continue
		}
		up++
		if quietest.IsZero() || h.Heard.Before(quietest) {
			quietest = h.Heard
		}
	}
	rows = append(rows, [2]string{"relays", fmt.Sprintf("%d of %d up", up, len(hs))})
	if up > 0 {
		rows = append(rows, [2]string{"heard", core.Duration(now.Sub(quietest)) + " ago"})
	}
	if drops > 0 {
		rows = append(rows, [2]string{"drops", strconv.Itoa(drops)})
	}
	return rows, notes
}
