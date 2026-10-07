// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

func keyOf(t *testing.T, b byte) Key {
	t.Helper()
	var s [32]byte
	for i := range s {
		s[i] = b
	}
	k, err := KeyFromSecret(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// rotate is a rotation of channel cid from (prevEpoch, prevKey) to
// (newEpoch, newKey) by rotator, with a blob for each of to, as Armada
// publishes one: chunk idx of n.
func rotate(t *testing.T, c *Community, cid [32]byte, rotator Key, prevEpoch uint64, prevKey [32]byte, newEpoch uint64, newKey [32]byte, idx, n int, to ...Key) *nostr.Event {
	t.Helper()
	rot, _ := hex32(rotator.PK)
	var blobs []rekeyBlob
	for _, r := range to {
		rec, _ := hex32(r.PK)
		plain := append(append(cid[:0:0], cid[:]...), make([]byte, 8)...)
		binary.BigEndian.PutUint64(plain[32:], newEpoch)
		plain = append(plain, newKey[:]...)
		conv, _ := nip44.GenerateConversationKey(r.PK, rotator.SK)
		wrapped, err := nip44.Encrypt(base64.StdEncoding.EncodeToString(plain), conv)
		if err != nil {
			t.Fatal(err)
		}
		blobs = append(blobs, rekeyBlob{Locator: hexOf(recipientLocator(rot, rec, cid, newEpoch)), Wrapped: wrapped})
	}
	content, _ := json.Marshal(blobs)
	commit := epochCommitment(prevEpoch, prevKey)
	r, err := NewRumor(kindRekey, string(content), [][]string{
		{"scope", hexOf(cid)}, {"newepoch", strconv.FormatUint(newEpoch, 10)},
		{"prevepoch", strconv.FormatUint(prevEpoch, 10)}, {"prevcommit", hexOf(commit)},
		{"chunk", strconv.Itoa(idx), strconv.Itoa(n)},
	}, rotator.PK, 1_800_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	w, err := Seal(r, channelRekeyKey(c.Root, cid, newEpoch).Stream(), rotator.SK)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func fill32(b byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = b
	}
	return k
}

func TestRekeysFollowOnlyAuthorizedChainedRotations(t *testing.T) {
	u := load(t)
	inv, _ := ParseInvite(u.Invite.URL)
	c, err := openBundle(u.Invite.Event, inv.Token, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := FoldControl(c, u.Community.A.Wraps)
	id := strings.Repeat("22", 32)
	cid, _ := hex32(id)
	held := c.Private[id] // epoch 4
	me := keyOf(t, 0x50)
	owner, admin, mod, helper := keyOf(t, 1), keyOf(t, 2), keyOf(t, 3), keyOf(t, 5)
	low, high := fill32(0x11), fill32(0x99)
	q := fixedQuery{
		// Two valid rotations to 5 race: the lower key wins.
		rotate(t, c, cid, owner, 4, held.Key, 5, high, 1, 1, me),
		rotate(t, c, cid, admin, 4, held.Key, 5, low, 1, 1, me),
		// The helper holds neither MANAGE_CHANNELS nor BAN.
		rotate(t, c, cid, helper, 5, low, 6, fill32(0x01), 1, 1, me),
		// A fork: chains from a key that isn't the one held.
		rotate(t, c, cid, mod, 5, high, 6, fill32(0x02), 1, 1, me),
		// The moderator holds BAN, and chains correctly.
		rotate(t, c, cid, mod, 5, low, 6, fill32(0x66), 1, 1, me),
		// Leaves skua out, and an incomplete one: neither is followed.
		rotate(t, c, cid, admin, 6, fill32(0x66), 7, fill32(0x03), 1, 1, owner),
		rotate(t, c, cid, owner, 6, fill32(0x66), 7, fill32(0x04), 1, 2, me),
	}
	moved := Rekeys(context.Background(), q, me.SK, c, f)
	if len(moved) != 1 || moved[0] != id {
		t.Fatalf("moved %v", moved)
	}
	if got := c.Private[id]; got.Epoch != 6 || got.Key != fill32(0x66) {
		t.Fatalf("held epoch %d key %x", got.Epoch, got.Key[:2])
	}
	if again := Rekeys(context.Background(), q, me.SK, c, f); len(again) != 0 {
		t.Fatalf("moved again %v", again)
	}
	if len(RekeyAddresses(c)) != rekeyLookahead || Rekeys(context.Background(), q, me.SK, c, nil) != nil {
		t.Fatal("addresses or a nil fold")
	}
}
