// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import "github.com/nbd-wtf/go-nostr"

// ValidControlWrap checks the same stream and edition syntax used by the
// control fold before a caller retains an event. Roster authority still
// belongs to FoldControl over the complete history.
func ValidControlWrap(w *nostr.Event, s StreamKey) bool {
	if w == nil || w.Kind != KindWrap {
		return false
	}
	o, err := Open(w, s)
	if err != nil {
		return false
	}
	_, ok := parseEdition(o)
	return ok
}
