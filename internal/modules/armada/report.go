// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// tally is what one link has done since skua started, one direction each
// way, read by its server's admins on armada's /help page.
type tally struct {
	in, out, failed           atomic.Int64 // messages into Discord, out to Armada, and failures
	lastIn, lastOut, lastFail atomic.Int64 // unix ms; 0 is never
}

func (t *tally) crossedIn()  { t.in.Add(1); t.lastIn.Store(time.Now().UnixMilli()) }
func (t *tally) crossedOut() { t.out.Add(1); t.lastOut.Store(time.Now().UnixMilli()) }
func (t *tally) fail()       { t.failed.Add(1); t.lastFail.Store(time.Now().UnixMilli()) }

// Report is the bridge as guild's admins see it: the community, whether
// skua is reading it, and each link that ends in guild. Links into other
// servers are not mentioned at all.
func (m *Module) Report(guild snowflake.ID) core.Report {
	var here []*link
	for _, l := range m.links {
		if snowflake.ID(l.guild.Load()) == guild {
			here = append(here, l)
		}
	}
	if len(here) == 0 {
		return core.Report{}
	}
	m.mu.RLock()
	c, f, chans, failing := m.comm, m.folded, m.chans, m.failing
	m.mu.RUnlock()
	var r core.Report
	switch {
	case c != nil && failing == "":
		r.Rows = append(r.Rows, [2]string{"community", c.Name}, [2]string{"state", "connected"}, [2]string{"relays", strconv.Itoa(len(c.Relays))})
	case c != nil:
		r.Rows = append(r.Rows, [2]string{"community", c.Name}, [2]string{"state", "connected, invite unreadable"})
		r.Notes = append(r.Notes, "! the invite stopped reading: "+failing+"; she keeps the keys she has")
	case failing != "":
		r.Rows = append(r.Rows, [2]string{"state", "can't read the invite"})
		r.Notes = append(r.Notes, "✗ "+failing)
	default:
		r.Rows = append(r.Rows, [2]string{"state", "connecting"})
	}
	now := time.Now()
	for _, l := range here {
		name, readable := l.armada[:8], false
		if f != nil {
			if ch, ok := f.Channels[l.armada]; ok {
				name = ch.Name
			}
		}
		_, readable = chans[l.armada]
		state := "readable"
		if !readable {
			state = "! not readable: grant skua a role that sees it"
		}
		r.Notes = append(r.Notes, fmt.Sprintf("<#%s> ↔ #%s · %s", l.discord, name, state))
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
