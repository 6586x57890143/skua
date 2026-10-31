package fix

import (
	"context"
	"regexp"
	"strings"
	"time"

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

// Reconcile only suppression owned by skua. A source that was already hidden
// by its author is never unsuppressed. Every successful write is remembered,
// even if an edit arrives while Discord is completing that write.
func (m *Module) suppressOriginal(ctx context.Context, r rest.Rest, key source, t *tracking, version uint64, links []Link) {
	m.mu.Lock()
	current := m.current(m.ctx, key, t, version)
	done := len(links) > 0 && t.completed == len(links)
	desired := done && t.suppressible && !t.fallback
	change := current && ((desired && !t.suppressed) || (!desired && t.ownsSuppression))
	// Every card is up but the source's preview stays: note it once a version.
	kept := ""
	if current && done && !t.suppressed && t.keptVersion != version+1 {
		switch {
		case t.fallback:
			kept = "incomplete preview"
		case !t.suppressible:
			kept = "other links in message"
		case m.refused(refusal{key.channel, true}):
			kept = "no manage messages"
		}
	}
	author := t.author
	if kept != "" {
		t.keptVersion = version + 1
	}
	flags := t.sourceFlags
	if desired {
		flags |= discord.MessageFlagSuppressEmbeds
	} else {
		flags &^= discord.MessageFlagSuppressEmbeds
	}
	m.mu.Unlock()
	if kept != "" {
		m.noteKept(key, author, kept, nil)
	}
	if !change || m.refused(refusal{key.channel, true}) {
		return
	}
	err := ctx.Err()
	if err == nil {
		err = m.guard.Allow(key.guild, guard.FixCard)
	}
	denied := err != nil
	if !denied {
		_, err = r.UpdateMessage(key.channel, key.message, discord.MessageUpdate{Flags: &flags}, rest.WithCtx(ctx))
		m.guard.Report(key.guild, guard.Struggling(err))
		m.noteRefusal(refusal{key.channel, true}, err)
	}
	m.mu.Lock()
	if err == nil {
		t.suppressed = desired
		t.ownsSuppression = desired
		// Preserve the latest other flags if a concurrent edit superseded us.
		if desired {
			t.sourceFlags |= discord.MessageFlagSuppressEmbeds
		} else {
			t.sourceFlags &^= discord.MessageFlagSuppressEmbeds
		}
	} else if !desired && (denied || retryDelete(err)) && m.current(m.ctx, key, t, version) {
		// Restore retries use the same bounded queue and timer. Completed cards
		// are not re-fetched; only a new content revision resets their progress.
		t.pending, t.dirty = links, true
		t.nextFetch = time.Now().Add(30 * time.Second)
	}
	failedHide := err != nil && desired && t.keptVersion != version+1
	if failedHide {
		t.keptVersion = version + 1
	}
	m.mu.Unlock()
	if failedHide {
		m.noteKept(key, author, keptReason(denied, err), err)
	} else if err != nil {
		m.log.Debug("fix: source preview flag unchanged", "guild", key.guild)
	}
}
