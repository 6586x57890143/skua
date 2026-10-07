// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

// Direct Invites (CORD-05 §6) are how a member is handed channel keys
// after joining: granting someone a private channel, or keeping them
// through a key rotation, sends them a NIP-59 gift wrap holding a bundle.
// The wrap is from a throwaway key, the seal inside from the sender, and
// the rumor inside that is the bundle.
const (
	kindDirectInvite = 3313
	kindNIP59Seal    = 13
	// intakeScan bounds how many wraps one scan reads.
	//
	// ponytail: anyone can address wraps to skua, so a flood of junk can push
	// a real grant out of the newest intakeScan. Filtering by the authorized
	// senders, if the protocol ever lets a reader tell them from a wrap, or
	// paging back with until, is the way past it.
	intakeScan = 200
)

// StockRelays is where a Direct Invite goes to a member who has not said
// where their inbox is.
var StockRelays = stockRelays

// DMRelays is the kind 10050 that says where skua's inbox is, so the next
// Direct Invite lands on the community's relays.
func DMRelays(relays []string, sk string) (*nostr.Event, error) {
	ev := &nostr.Event{Kind: KindDMRelays, CreatedAt: nostr.Now(), Tags: nostr.Tags{}}
	for _, r := range relays {
		ev.Tags = append(ev.Tags, nostr.Tag{"relay", r})
	}
	return ev, ev.Sign(sk)
}

// Intake reads the Direct Invites addressed to sk and adds to c the channel
// keys in them, returning the ids of the channels it learned or that moved
// to a newer epoch. A bundle counts only when:
//
//   - its seal opens under the pairwise key with its sender, and the
//     rumor's author is that sender, which proves who sent it;
//   - the sender may hand out channels: the owner or a MANAGE_CHANNELS
//     holder per f. Proven who is not authorized to, and the bundle is
//     otherwise unsigned key material skua is about to adopt;
//   - it is for this community, whose owner reproduces its id. Channel ids
//     say nothing about which community minted them.
//
// Keys are only ever added, never replaced by an older or equal epoch, so a
// grant of one channel can't displace the others.
func Intake(ctx context.Context, q querier, sk string, c *Community, f *Folded) []string {
	pk, err := nostr.GetPublicKey(sk)
	if err != nil || f == nil {
		return nil
	}
	wraps := q.Query(ctx, nostr.Filter{Kinds: []int{KindWrap}, Tags: nostr.TagMap{"p": {pk}, "k": {"3313"}}, Limit: intakeScan})
	var learned []string
	for _, w := range wraps {
		sender, b, ok := unwrapInvite(w, sk)
		if !ok || !f.Roster.authorized(sender, c.Owner, PermManageChannels) {
			continue
		}
		bc, err := community(b)
		if err != nil || bc.IDHex != c.IDHex || len(b.Channels) > maxBundleChans {
			continue
		}
		for id, k := range bc.Private {
			if held, ok := c.Private[id]; ok && k.Epoch <= held.Epoch {
				continue
			}
			c.Private[id] = k
			learned = append(learned, id)
		}
	}
	return learned
}

func unwrapInvite(w *nostr.Event, sk string) (string, bundle, bool) {
	var b bundle
	if w.Kind != KindWrap {
		return "", b, false
	}
	conv, err := nip44.GenerateConversationKey(w.PubKey, sk)
	if err != nil {
		return "", b, false
	}
	plain, err := nip44.Decrypt(w.Content, conv)
	if err != nil {
		return "", b, false
	}
	var seal nostr.Event
	if json.Unmarshal([]byte(plain), &seal) != nil || seal.Kind != kindNIP59Seal || !validEvent(&seal) {
		return "", b, false
	}
	conv, err = nip44.GenerateConversationKey(seal.PubKey, sk)
	if err != nil {
		return "", b, false
	}
	inner, err := nip44.Decrypt(seal.Content, conv)
	if err != nil {
		return "", b, false
	}
	var r Rumor
	if json.Unmarshal([]byte(inner), &r) != nil || r.Kind != kindDirectInvite || r.PubKey != seal.PubKey {
		return "", b, false
	}
	if json.Unmarshal([]byte(r.Content), &b) != nil || b.CommunityID == "" {
		return "", b, false
	}
	return strings.ToLower(seal.PubKey), b, true
}
