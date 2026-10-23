package armada

import (
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/concord"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

func TestDeleteWaitsForQueuedMessageMapping(t *testing.T) {
	h := newHarness(t, "11="+general)
	msg := discord.Message{ID: 800, ChannelID: 11, Content: "withdrawn", Author: discord.User{ID: 42, Username: "member"}}
	h.m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{
		GuildID: 7, ChannelID: 11, MessageID: 800, Message: msg,
	}})
	h.m.OnEvent(&events.GuildMessageDelete{GenericGuildMessage: &events.GenericGuildMessage{
		GuildID: 7, ChannelID: 11, MessageID: 800,
	}})
	if n := len(h.l.toArm); n != 2 {
		t.Fatalf("queued %d operations, want create then delete", n)
	}
	done := make(chan struct{})
	go func() { defer close(done); h.m.discordWorker(h.l) }()
	t.Cleanup(func() { h.m.stop(); <-done })
	deadline := time.After(5 * time.Second)
	for {
		h.pool.mu.Lock()
		var posted, target string
		for _, ev := range h.pool.published {
			if o, err := concord.OpenChat(ev, h.chans[general]); err == nil {
				if o.Kind == concord.KindMessage {
					posted = o.RumorID
				}
				if o.Kind == concord.KindDelete {
					target = concord.Tag(o.Tags, "e")
				}
			}
		}
		h.pool.mu.Unlock()
		if target != "" {
			if target != posted {
				t.Fatalf("delete targets %s, want %s", target, posted)
			}
			return
		}
		select {
		case <-h.pool.notify:
		case <-deadline:
			t.Fatal("queued message was never tombstoned")
		}
	}
}
