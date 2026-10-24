package pp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
)

// Discord refuses a message whose components share a custom ID, so every
// shop screen, with the menu under it, carries each once.
func TestScreensKeepIDsApart(t *testing.T) {
	m, _ := yardServer(5, 100000)
	s := m.st.server(3)
	today := DayOf(m.now())
	mem := m.st.member(s, 3, 5, today)
	mem.classSeason = 0
	tallow, _ := dyeOf("tallow")
	screens := map[string]func() (discord.MessageCreate, error){
		"shop":     func() (discord.MessageCreate, error) { return m.st.shopMessage(s, mem, true) },
		"trades":   func() (discord.MessageCreate, error) { return m.st.yardMessage(s, 5, mem, tradesPage, today) },
		"factions": func() (discord.MessageCreate, error) { return m.st.yardMessage(s, 5, mem, factionsPage, today) },
		"card":     func() (discord.MessageCreate, error) { return m.st.dyeMessage(mem, tallow, today) },
		"own": func() (discord.MessageCreate, error) {
			return m.st.ownMessage(mem, &draft{name: "x", style: ownStyles[0], colors: []uint32{1}}, ownState{drafted: true, wearing: true, days: 7, ahead: 28})
		},
	}
	for name, screen := range screens {
		msg, err := screen()
		if err != nil {
			t.Fatal(name, err)
		}
		msg = m.withMenu(3, 5, true, msg, "pp:shop")
		seen := map[string]bool{}
		var walk func(any)
		walk = func(x any) {
			switch x := x.(type) {
			case map[string]any:
				if id, ok := x["custom_id"].(string); ok {
					if seen[id] {
						t.Errorf("%s carries %s twice", name, id)
					}
					seen[id] = true
				}
				for _, v := range x {
					walk(v)
				}
			case []any:
				for _, v := range x {
					walk(v)
				}
			}
		}
		var x any
		_ = json.Unmarshal([]byte(js(msg)), &x)
		walk(x)
	}
}

// Work after a deferred answer runs off the caller's goroutine, outlives
// the press's three seconds and survives a panic.
func TestLater(t *testing.T) {
	m := testModule(&fakeDB{})
	m.inline = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 2)
	m.later(ctx, func(ctx context.Context) { done <- ctx.Err() })
	m.later(ctx, func(context.Context) { defer func() { done <- nil }(); panic("boom") })
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("the work's context came cancelled: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the work never ran")
		}
	}
}
