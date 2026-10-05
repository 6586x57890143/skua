package core

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

func TestTogglesSetSavesThenSwitches(t *testing.T) {
	var nilT *Toggles
	if !nilT.On(3, "test") {
		t.Fatal("a nil Toggles turned something off")
	}
	var saved []bool
	changed := make(chan snowflake.ID, 2)
	tg := NewToggles([]Switch{{3, "bird"}}, func(_ context.Context, _ Switch, on bool) error {
		saved = append(saved, on)
		return nil
	})
	tg.OnChange = func(g snowflake.ID) { changed <- g }
	if tg.On(3, "bird") || !tg.On(4, "bird") || !tg.On(3, "test") {
		t.Fatal("the boot switches were not applied to just their guild")
	}
	if err := tg.Set(context.Background(), 3, "test", false); err != nil || tg.On(3, "test") {
		t.Fatalf("turning off: %v", err)
	}
	if err := tg.Set(context.Background(), 3, "test", true); err != nil || !tg.On(3, "test") {
		t.Fatalf("turning on: %v", err)
	}
	if len(saved) != 2 || saved[0] || !saved[1] || <-changed != 3 || <-changed != 3 {
		t.Fatalf("saved %v", saved)
	}

	broken := NewToggles(nil, func(context.Context, Switch, bool) error { return errors.New("db down") })
	if err := broken.Set(context.Background(), 3, "test", false); err == nil || !broken.On(3, "test") {
		t.Fatal("a failed save still switched")
	}
}

func TestToggledOffModuleIsRefusedAndUnregistered(t *testing.T) {
	ran := 0
	r := NewRouter(0, nil, slog.New(slog.DiscardHandler))
	if err := r.Add(compMod{mod: mod{[]Command{{Create: discord.SlashCommandCreate{Name: "a"}, Tier: Public,
		Run: func(context.Context, *events.ApplicationCommandInteractionCreate) error { ran++; return nil }}}},
		comps: []Component{{ID: "b", Run: func(context.Context, *events.ComponentInteractionCreate) error { ran++; return nil }}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(modalMod{modals: []Modal{{ID: "c", Run: func(context.Context, *events.ModalSubmitInteractionCreate) error { ran++; return nil }}}}); err != nil {
		t.Fatal(err)
	}
	tg := NewToggles([]Switch{{3, "test"}}, nil)
	r.Gate(tg)
	if len(r.CreatesFor(3)) != 0 || len(r.CreatesFor(4)) != 1 {
		t.Fatalf("creates: %d in the off guild, %d elsewhere", len(r.CreatesFor(3)), len(r.CreatesFor(4)))
	}
	want := "✗ test is off in this server"
	e, sent := coretest.Event(t, "a", nil)
	r.OnCommand(e)
	b, bsent := coretest.Button(t, "b:1", nil)
	r.OnComponent(b)
	m, msent := coretest.Modal(t, "c", nil, nil)
	r.OnModal(m)
	if ran != 0 {
		t.Fatalf("%d handlers ran for a module that is off", ran)
	}
	for _, s := range []*[]discord.MessageCreate{sent, bsent, msent} {
		if len(*s) != 1 || (*s)[0].Content != want {
			t.Errorf("reply %+v, want %q", *s, want)
		}
	}
	_ = tg.Set(context.Background(), 3, "test", true)
	e, _ = coretest.Event(t, "a", nil)
	r.OnCommand(e)
	if ran != 1 {
		t.Fatal("turned back on, still refused")
	}
}

func TestListenDropsEventsOfAnOffModule(t *testing.T) {
	tg := NewToggles([]Switch{{3, "test"}}, nil)
	got := 0
	l := tg.Listen("test", bot.NewListenerFunc(func(bot.Event) { got++ }))
	gen := events.NewGenericEvent(nil, 0, 0)
	l.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: gen, GuildID: 3}})
	l.OnEvent(&events.GuildMessageReactionAdd{GenericGuildMessageReaction: &events.GenericGuildMessageReaction{GenericEvent: gen, GuildID: 3}})
	if got != 0 {
		t.Fatalf("%d events reached a module that is off", got)
	}
	// Kinds no module listens for yet are caught the same way.
	l.OnEvent(&events.GuildMessageUpdate{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: gen, GuildID: 3}})
	l.OnEvent(&events.GuildMemberJoin{GenericGuildMember: &events.GenericGuildMember{GenericEvent: gen, GuildID: 3}})
	l.OnEvent(&events.GuildMessageCreate{}) // a nil embedded struct has no guild
	if got != 1 {
		t.Fatalf("%d events reached a module that is off, want only the guildless one", got)
	}
	l.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: gen, GuildID: 4}})
	l.OnEvent(&events.Ready{})
	l.OnEvent(&events.GuildLeave{GenericGuild: &events.GenericGuild{GenericEvent: gen, GuildID: 3}})
	if got != 4 {
		t.Fatalf("%d of 4 events got through", got)
	}
	// A *GuildID: nil (a DM) passes, set is checked. No field passes, from
	// the cache the second time.
	g3 := snowflake.ID(3)
	l.OnEvent(&events.MessageCreate{GenericMessage: &events.GenericMessage{GenericEvent: gen}})
	l.OnEvent(&events.MessageCreate{GenericMessage: &events.GenericMessage{GenericEvent: gen, GuildID: &g3}})
	l.OnEvent(&events.Ready{})
	if got != 6 {
		t.Fatalf("%d of 6 events got through", got)
	}
	if idx, ok := guildField.Load(reflect.TypeFor[events.Ready]()); !ok || idx.([]int) != nil {
		t.Fatal("no field was not cached")
	}
}

func TestCoalesceFoldsCallsThatLandMidRun(t *testing.T) {
	var runs, state, seen atomic.Int32
	started, gate, done := make(chan struct{}, 10), make(chan struct{}), make(chan struct{}, 10)
	f := Coalesce(func(snowflake.ID) {
		started <- struct{}{}
		<-gate
		seen.Store(state.Load())
		runs.Add(1)
		done <- struct{}{}
	})
	f(3)
	<-started // the first run is under way, held at the gate
	for i := range 5 {
		state.Store(int32(i + 1)) // a Set landing mid-register
		f(3)                      // all fold into one more
	}
	close(gate)
	<-done
	<-done
	time.Sleep(20 * time.Millisecond)
	if n := runs.Load(); n != 2 {
		t.Fatalf("%d runs for 6 calls landing mid-run, want 2", n)
	}
	if seen.Load() != 5 {
		t.Fatalf("the follow-up run saw state %d, want the last, 5", seen.Load())
	}
	f(3)
	<-done
	if n := runs.Load(); n != 3 {
		t.Fatalf("a call after the runs finished made %d runs, want 3", n)
	}
}

func TestListenCountsWhatIsOff(t *testing.T) {
	tg := NewToggles([]Switch{{3, "a"}, {3, "a"}}, nil)
	ctx := context.Background()
	steps := []struct {
		module string
		on     bool
		want   int32
	}{{"a", false, 1}, {"b", false, 2}, {"b", false, 2}, {"a", true, 1}, {"a", true, 1}, {"b", true, 0}}
	if tg.n.Load() != 1 {
		t.Fatalf("a duplicate boot switch counted twice: %d", tg.n.Load())
	}
	for _, s := range steps {
		_ = tg.Set(ctx, 3, s.module, s.on)
		if got := tg.n.Load(); got != s.want {
			t.Fatalf("after %s on=%v: %d off, want %d", s.module, s.on, got, s.want)
		}
	}
}

// Listen sits on every gateway event once per listening module: no
// allocation either way.
func TestListenDoesNotAllocate(t *testing.T) {
	ev := &events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: events.NewGenericEvent(nil, 0, 0), GuildID: 4}}
	for name, tg := range map[string]*Toggles{"none off": NewToggles(nil, nil), "some off": NewToggles([]Switch{{3, "test"}}, nil)} {
		l := tg.Listen("test", bot.NewListenerFunc(func(bot.Event) {}))
		l.OnEvent(ev) // fills the field cache
		if n := testing.AllocsPerRun(100, func() { l.OnEvent(ev) }); n != 0 {
			t.Errorf("%s: %v allocs per event", name, n)
		}
	}
}

func BenchmarkListen(b *testing.B) {
	ev := &events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: events.NewGenericEvent(nil, 0, 0), GuildID: 4}}
	for name, tg := range map[string]*Toggles{"none-off": NewToggles(nil, nil), "some-off": NewToggles([]Switch{{3, "test"}}, nil)} {
		l := tg.Listen("test", bot.NewListenerFunc(func(bot.Event) {}))
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				l.OnEvent(ev)
			}
		})
	}
}
