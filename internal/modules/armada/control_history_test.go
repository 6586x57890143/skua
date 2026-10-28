// SPDX-License-Identifier: AGPL-3.0-only
package armada

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/nbd-wtf/go-nostr"

	"github.com/6586x57890143/skua/internal/concord"
	"github.com/6586x57890143/skua/internal/filter"
)

func historyFixture(t *testing.T) (concord.StreamKey, []*nostr.Event) {
	t.Helper()
	v := loadVectors(t)
	invite, err := concord.ParseInvite(v.Invite.URL)
	if err != nil {
		t.Fatal(err)
	}
	c, err := concord.Resolve(context.Background(), invite, &fakeRelays{stored: []*nostr.Event{v.Invite.Event}})
	if err != nil {
		t.Fatal(err)
	}
	stream := concord.ControlStream(c)
	var valid []*nostr.Event
	for _, w := range v.Community.A.Wraps {
		if concord.ValidControlWrap(w, stream) {
			valid = append(valid, w)
		}
	}
	if len(valid) < 2 {
		t.Fatal("fixture has fewer than two valid control editions")
	}
	return stream, valid
}

func TestControlHistoryRejectsUnrelatedAndMalformedWraps(t *testing.T) {
	stream, valid := historyFixture(t)
	h := newControlHistory(2, maxControlBytes)
	unrelated := *valid[0]
	unrelated.PubKey = key(t, 99).PK
	malformed := *valid[0]
	malformed.Content = "not an encrypted control edition"
	for _, w := range []*nostr.Event{nil, &unrelated, &malformed} {
		if added, err := h.add(w, stream); added || err != nil {
			t.Fatalf("retained invalid control: %v %v", added, err)
		}
	}
	if len(h.wraps) != 0 || h.bytes != 0 {
		t.Fatal("invalid events consumed retained history")
	}
	if added, err := h.add(valid[0], stream); !added || err != nil {
		t.Fatalf("valid control: %v %v", added, err)
	}
	if added, err := h.add(valid[0], stream); added || err != nil {
		t.Fatalf("duplicate control: %v %v", added, err)
	}
}

func TestControlHistoryRequiresEditionSyntax(t *testing.T) {
	author, stream := key(t, 17), key(t, 18).Stream()
	r, err := concord.NewRumor(concord.KindControl, "{}", nil, author.PK, 1)
	if err != nil {
		t.Fatal(err)
	}
	w, err := concord.Seal(r, stream, author.SK)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := concord.Open(w, stream); err != nil {
		t.Fatal("fixture must be a valid stream event", err)
	}
	h := newControlHistory(2, maxControlBytes)
	if added, err := h.add(w, stream); added || err != nil {
		t.Fatalf("retained non-edition: %v %v", added, err)
	}
}

func TestControlHistoryOverflowPreservesPriorAuthority(t *testing.T) {
	stream, valid := historyFixture(t)
	raw, _ := json.Marshal(valid[0])
	for _, h := range []*controlHistory{newControlHistory(1, maxControlBytes), newControlHistory(10, len(raw))} {
		if added, err := h.add(valid[0], stream); !added || err != nil {
			t.Fatal(err)
		}
		if added, err := h.add(valid[1], stream); added || !errors.Is(err, errControlHistoryLimit) {
			t.Fatalf("overflow accepted: %v %v", added, err)
		}
		if len(h.wraps) != 1 || h.wraps[valid[0].ID] != valid[0] || h.bytes != len(raw) {
			t.Fatal("overflow evicted prior authority history")
		}
	}
}

func TestControlOverflowSuspendsBridging(t *testing.T) {
	pool, newer := &fakeRelays{}, &fakeRelays{}
	cancelled := false
	m := &Module{log: slog.New(slog.DiscardHandler), pool: pool, pub: pool,
		chans:   map[string]concord.ChatChannel{general: {}},
		subs:    map[string]context.CancelFunc{general: func() { cancelled = true }},
		subKeys: map[string]string{general: "old-authority"}}
	m.suspendControl(newer, errControlHistoryLimit)
	if cancelled || len(m.chans) != 1 {
		t.Fatal("old session stopped a newer session")
	}
	m.suspendControl(pool, errControlHistoryLimit)
	if !cancelled || m.chans != nil || len(m.subs) != 0 || len(m.subKeys) != 0 || !m.controlBlocked || m.failing == "" {
		t.Fatal("overflow left bridging active")
	}
}

func TestControlSuspensionRejectsQueuedDiscord(t *testing.T) {
	h := newHarness(t, "100="+general)
	w, _ := h.chat(t, 42, concord.KindMessage, "queued before suspension")
	h.m.onWrap(h.l, w)
	var job discordJob
	select {
	case job = <-h.l.toDisc:
	default:
		t.Fatal("message was not queued")
	}
	h.m.suspendControl(h.pool, errControlHistoryLimit)
	h.m.toDiscordBound(h.l, job.opened, job.binding)
	if len(h.post.sent) != 0 {
		t.Fatal("suspended queue wrote to Discord")
	}
	// A new session using the same relay object and keys must not revive
	// jobs that were authenticated by the old session.
	h.m.mu.Lock()
	h.m.controlEpoch++
	h.m.controlBlocked = false
	h.m.chans = h.chans
	h.m.mu.Unlock()
	h.m.toDiscordBound(h.l, job.opened, job.binding)
	if len(h.post.sent) != 0 {
		t.Fatal("new session revived old queued delivery")
	}
	// A channel removed and later restored under another fold also cannot
	// revive work prepared under the previous authority revision.
	h.m.mu.Lock()
	job.binding.epoch = h.m.controlEpoch
	h.m.controlRevision++
	h.m.mu.Unlock()
	h.m.toDiscordBound(h.l, job.opened, job.binding)
	if len(h.post.sent) != 0 {
		t.Fatal("new fold revived old queued delivery")
	}
}

type suspendingScreen struct{ stop func() }

func (s suspendingScreen) Check(text string) filter.Verdict {
	s.stop()
	return filter.Verdict{Text: text}
}

func TestControlSuspensionDuringDiscordPreparation(t *testing.T) {
	h := newHarness(t, "100="+general)
	w, _ := h.chat(t, 42, concord.KindMessage, "prepared before suspension")
	h.m.onWrap(h.l, w)
	job := <-h.l.toDisc
	h.m.screen = suspendingScreen{stop: func() { h.m.suspendControl(h.pool, errControlHistoryLimit) }}
	h.m.toDiscordBound(h.l, job.opened, job.binding)
	if len(h.post.sent) != 0 {
		t.Fatal("suspension during preparation did not stop the final write")
	}
}
