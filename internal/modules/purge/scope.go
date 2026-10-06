package purge

import (
	"context"
	"slices"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// scope is where a purge deletes: the channels a member picked, each with
// its threads, and for a category every channel in it and their threads.
// Empty is everywhere, as before scopes existed.
type scope []snowflake.ID

// covers reports whether ch is in s, climbing from a thread to its channel
// to its category with up (0 for no parent). A parent up can't tell is 0,
// so a channel skua can't place is left alone: a scope only ever narrows
// what is deleted.
func (s scope) covers(ch snowflake.ID, up func(snowflake.ID) snowflake.ID) bool {
	if len(s) == 0 {
		return true
	}
	for range 3 { // thread, channel, category
		if ch == 0 {
			return false
		}
		if slices.Contains(s, ch) {
			return true
		}
		ch = up(ch)
	}
	return false
}

// union is everywhere either covers.
func (s scope) union(o scope) scope {
	if len(s) == 0 || len(o) == 0 {
		return nil
	}
	out := slices.Concat(s, o)
	slices.Sort(out)
	return slices.Compact(out)
}

// int64s is s as Postgres stores it; never nil, so it writes '{}'.
func (s scope) int64s() []int64 {
	out := make([]int64, len(s))
	for i, id := range s {
		out[i] = int64(id)
	}
	return out
}

func scopeOf(ids []int64) scope {
	var s scope
	for _, id := range ids {
		s = append(s, snowflake.ID(id))
	}
	return s
}

// mentions is s as channel mentions, for a reply.
func (s scope) mentions() string {
	out := ""
	for i, id := range s {
		if i > 0 {
			out += " "
		}
		out += "<#" + id.String() + ">"
	}
	return out
}

// parentTTL is how long live remembers a channel's parent: a channel moved
// to another category is seen within it.
const parentTTL = 10 * time.Minute

type parentAt struct {
	id snowflake.ID
	at time.Time
}

// parent is ch's parent channel from Discord, 0 for none or when Discord
// won't say. Live asks it only for a scoped member posting outside the
// channels they picked, and remembers the answer.
func (m *Module) parent(r rest.Rest, ch snowflake.ID) snowflake.ID {
	if v, ok := m.parents.Load(ch); ok && m.now().Sub(v.(parentAt).at) < parentTTL {
		return v.(parentAt).id
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := r.GetChannel(ch, rest.WithCtx(ctx))
	if err != nil {
		m.log.Warn("purge: placing a channel", "channel", ch, "err", err)
		return 0
	}
	var p snowflake.ID
	if gc, ok := c.(discord.GuildChannel); ok && gc.ParentID() != nil {
		p = *gc.ParentID()
	}
	m.parents.Store(ch, parentAt{p, m.now()})
	return p
}
