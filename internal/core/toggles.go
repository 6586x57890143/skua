package core

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

// Switch is one module in one guild.
type Switch struct {
	Guild  snowflake.ID
	Module string
}

// Toggles is which modules a guild's admins have flipped from where they
// start. A module starts on unless it is opt-in, and a nil *Toggles is all
// on. On is one map load, cheap enough for every event.
type Toggles struct {
	flipped sync.Map // Switch -> struct{}
	// optIn is the modules that start off, written while wiring, before
	// anything reads it.
	optIn map[string]bool
	// n is how many switches are flipped anywhere: while none are, Listen
	// passes every event of a module that starts on straight through.
	n atomic.Int32
	// ponytail: one lock for every guild, held across the save; fine at
	// admin click rates, a lock per Switch if Set ever gets hot.
	mu   sync.Mutex // one Set at a time, so the store and memory agree
	save func(ctx context.Context, s Switch, flipped bool) error
	// OnChange runs after a successful Set, so the guild's command list can
	// follow it.
	OnChange func(guild snowflake.ID)
}

// NewToggles starts from the switches that are flipped and persists every
// Set through save, told whether the switch is now flipped. A nil save
// keeps them in memory only.
func NewToggles(flipped []Switch, save func(ctx context.Context, s Switch, flipped bool) error) *Toggles {
	t := &Toggles{save: save, optIn: map[string]bool{}}
	for _, s := range flipped {
		if _, dup := t.flipped.Swap(s, struct{}{}); !dup {
			t.n.Add(1)
		}
	}
	return t
}

// OptIn makes module start off in every guild until its admins switch it
// on. Call it before the gateway, the link or Listen can read it.
func (t *Toggles) OptIn(module string) { t.optIn[module] = true }

func (t *Toggles) On(guild snowflake.ID, module string) bool {
	if t == nil {
		return true
	}
	_, flipped := t.flipped.Load(Switch{guild, module})
	return flipped == t.optIn[module]
}

// Set saves first, so memory never claims a switch the store lost.
func (t *Toggles) Set(ctx context.Context, guild snowflake.ID, module string, on bool) error {
	s := Switch{guild, module}
	flip := on == t.optIn[module]
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.save != nil {
		if err := t.save(ctx, s, flip); err != nil {
			return err
		}
	}
	if !flip {
		if _, was := t.flipped.LoadAndDelete(s); was {
			t.n.Add(-1)
		}
	} else if _, was := t.flipped.Swap(s, struct{}{}); !was {
		t.n.Add(1)
	}
	if t.OnChange != nil {
		go t.OnChange(guild) // never under the lock
	}
	return nil
}

// Gated is a Module with work of its own beyond commands and events, a
// schedule say, that has to stop in a guild that turned it off. Optional.
type Gated interface {
	Gate(on func(guild snowflake.ID) bool)
}

// Listen drops every event of a guild that has module off. Any event with a
// GuildID field counts, found once per event type, so a new kind of event
// cannot slip past. Leaving a guild still gets through, so a module can
// forget what it kept there.
func (t *Toggles) Listen(module string, l bot.EventListener) bot.EventListener {
	optIn := t.optIn[module]
	return bot.NewListenerFunc(func(ev bot.Event) {
		if t.n.Load() == 0 && !optIn {
			l.OnEvent(ev)
			return
		}
		if _, leave := ev.(*events.GuildLeave); leave {
			l.OnEvent(ev)
			return
		}
		if g, ok := guildOf(ev); !ok || t.On(g, module) {
			l.OnEvent(ev)
		}
	})
}

var guildField sync.Map // reflect.Type -> []int, nil for none

var idType = reflect.TypeFor[snowflake.ID]()

// guildOf is ev's guild, from its GuildID field (or *snowflake.ID).
func guildOf(ev bot.Event) (snowflake.ID, bool) {
	v := reflect.ValueOf(ev)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return 0, false
	}
	v = v.Elem()
	idx, ok := guildField.Load(v.Type())
	if !ok {
		var found []int
		if f, ok := v.Type().FieldByName("GuildID"); ok && (f.Type == idType || f.Type == reflect.PointerTo(idType)) {
			found = f.Index
		}
		idx, _ = guildField.LoadOrStore(v.Type(), found)
	}
	if idx.([]int) == nil {
		return 0, false
	}
	f, err := v.FieldByIndexErr(idx.([]int))
	if err != nil { // a nil embedded struct
		return 0, false
	}
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return 0, false
		}
		f = f.Elem()
	}
	return snowflake.ID(f.Uint()), true
}

// Coalesce runs f for a guild on its own goroutine, one at a time per
// guild: calls that land while one is running fold into one more run
// after it, so a flapping switch costs two runs, not one per flip, and the
// last run always sees the last state.
func Coalesce(f func(guild snowflake.ID)) func(guild snowflake.ID) {
	var pending sync.Map // snowflake.ID -> *atomic.Int32
	return func(guild snowflake.ID) {
		v, _ := pending.LoadOrStore(guild, new(atomic.Int32))
		n := v.(*atomic.Int32)
		if n.Add(1) > 1 {
			return
		}
		go func() {
			for {
				seen := n.Load()
				f(guild)
				if n.Add(-seen) == 0 {
					return
				}
			}
		}()
	}
}
