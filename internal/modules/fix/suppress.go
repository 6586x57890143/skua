package fix

import (
	"context"
	"regexp"
	"strings"

	"github.com/6586x57890143/skua/internal/guard"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
)

var sourceURLs = regexp.MustCompile(`(?i)https?://[^\s<>` + "`" + `|]+`)

// Discord suppresses all embeds on a message, not individual previews. Keep
// originals when any URL lacks a replacement, including links beyond our cap.
func canSuppress(text string, links []Link) bool {
	if len(links) == 0 {
		return false
	}
	urls := sourceURLs.FindAllString(text, -1)
	if len(urls) == 0 {
		return false
	}
	for _, raw := range urls {
		l, ok := Parse(strings.TrimRight(raw, ".,!?;:)]}"))
		if !ok {
			return false
		}
		found := false
		for _, replacement := range links {
			if l.URL == replacement.URL {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (m *Module) suppressOriginal(ctx context.Context, r rest.Rest, key source, t *tracking, version uint64, count int) {
	m.mu.Lock()
	ready := m.current(ctx, key, t, version) && count > 0 && t.completed == count && t.suppressible && !t.fallback && !t.suppressed
	flags := t.sourceFlags | discord.MessageFlagSuppressEmbeds
	m.mu.Unlock()
	if !ready {
		return
	}
	// A separate guarded write. Never delete the member's message or retry a
	// permission refusal. Private previews and failed metadata never get here.
	if m.guard.Allow(key.guild, guard.MessageSend) != nil {
		return
	}
	_, err := r.UpdateMessage(key.channel, key.message, discord.MessageUpdate{Flags: &flags}, rest.WithCtx(ctx))
	m.guard.Report(key.guild, guard.Struggling(err))
	m.mu.Lock()
	if err == nil && t.version == version {
		t.suppressed = true
		t.sourceFlags = flags
	}
	m.mu.Unlock()
	if err != nil {
		m.log.Debug("fix: original preview retained", "guild", key.guild)
	}
}
