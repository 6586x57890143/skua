// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

// A private channel's key rotates (CORD-06), after a ban say, so whoever lost
// access can't read on. The rotator publishes kind 3303 rumors at an address
// every member can derive from the community root and the channel id. Each
// carries the new epoch, a commitment to the key it replaces, and one blob per
// member who keeps access, holding the new key encrypted with NIP-44 under the
// rotator<->member pairwise key and filed under a locator only those two can
// compute. Following that is how skua stays in a channel through a rotation;
// without it she reads and writes a retired key, and nothing on either side
// says so.

const (
	kindRekey            = 3303
	labelRekeyPseudonym  = "concord/rekey-pseudonym"
	labelRecipient       = "concord/recipient-pseudonym"
	labelEpochCommitment = "concord/epoch-key-commitment"
	// rekeyLookahead is how many epochs past the held one are looked for at
	// once; a channel rotated more often than that between two reads is
	// caught up on the next.
	rekeyLookahead = 8
)

// channelRekeyKey is where rotations of channel to epoch are published.
func channelRekeyKey(root, channel [32]byte, epoch uint64) Key {
	return groupKey(labelRekeyPseudonym, root, channel, &epoch)
}

// recipientLocator files one member's blob in a rotation: only the rotator
// and that member can compute it.
func recipientLocator(rotator, recipient, scope [32]byte, epoch uint64) [32]byte {
	ikm := append(append(make([]byte, 0, 64), rotator[:]...), recipient[:]...)
	return hkdf32(ikm, info(labelRecipient, scope, &epoch))
}

// epochCommitment binds a rotation to the key it replaces, so only someone
// who held that key can chain the next.
func epochCommitment(epoch uint64, key [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(labelEpochCommitment))
	_ = binary.Write(h, binary.BigEndian, epoch)
	h.Write(key[:])
	return [32]byte(h.Sum(nil))
}

type rekeyBlob struct {
	Locator string `json:"locator"`
	Wrapped string `json:"wrapped"`
}

// rotation is one rotator's move of one channel to newEpoch.
type rotation struct {
	rotator    string
	newEpoch   uint64
	prevEpoch  uint64
	prevCommit string
	chunks     int
	have       map[int][]rekeyBlob
}

// Rekeys follows the rotations of every private channel skua holds a key
// for, returning the ids of those that moved on. A rotation is taken only
// when:
//
//   - its rotator is the owner or holds MANAGE_CHANNELS or BAN per f: the
//     roles that remove people, and so rotate;
//   - it commits to the exact key skua holds, so it chains from her epoch,
//     not from a fork or a key she never had;
//   - it is complete and carries a blob for skua, which opens under her
//     pairwise key with the rotator and names this channel and epoch.
//
// Two valid rotations to one epoch converge on the lower new key (CORD-06
// §3), as every member does.
func Rekeys(ctx context.Context, q querier, sk string, c *Community, f *Folded) []string {
	me, err := nostr.GetPublicKey(sk)
	if err != nil || f == nil {
		return nil
	}
	meB, _ := hex32(me)
	var moved []string
	for id, held := range c.Private {
		cid, err := hex32(id)
		if err != nil {
			continue
		}
		start := held.Epoch
		for {
			next, ok := rekeyOnce(ctx, q, sk, meB, c, f, cid, held)
			if !ok {
				break
			}
			held = next
			if held.Epoch >= start+rekeyLookahead*4 {
				break // a runaway chain is not followed further in one read
			}
		}
		if held.Epoch > start {
			c.Private[id] = held
			moved = append(moved, id)
		}
	}
	return moved
}

// rekeyOnce finds the rotation that follows held, if one has been published.
func rekeyOnce(ctx context.Context, q querier, sk string, me [32]byte, c *Community, f *Folded, cid [32]byte, held PrivateKey) (PrivateKey, bool) {
	addrs := map[string]uint64{}
	keys := map[string]Key{}
	var authors []string
	for e := held.Epoch + 1; e <= held.Epoch+rekeyLookahead; e++ {
		k := channelRekeyKey(c.Root, cid, e)
		addrs[k.PK], keys[k.PK] = e, k
		authors = append(authors, k.PK)
	}
	commit := hex.EncodeToString(func() []byte { b := epochCommitment(held.Epoch, held.Key); return b[:] }())
	sets := map[string]*rotation{}
	for _, w := range q.Query(ctx, nostr.Filter{Kinds: []int{KindWrap}, Authors: authors}) {
		k, ok := keys[w.PubKey]
		if !ok {
			continue
		}
		o, err := Open(w, k.Stream())
		if err != nil || o.Kind != kindRekey || o.SealKind != KindSealEncrypted {
			continue
		}
		r, ok := parseRekey(o)
		if !ok || r.newEpoch != addrs[w.PubKey] || Tag(o.Tags, "scope") != hexOf(cid) {
			continue
		}
		// Chains from exactly the key skua holds, by someone who may rotate.
		if r.prevEpoch != held.Epoch || r.prevCommit != commit {
			continue
		}
		if !f.Roster.authorized(r.rotator, c.Owner, PermManageChannels) && !f.Roster.authorized(r.rotator, c.Owner, PermBan) {
			continue
		}
		key := r.rotator + ":" + strconv.FormatUint(r.newEpoch, 10)
		s := sets[key]
		if s == nil {
			s = r
			sets[key] = s
		}
		if r.chunks == s.chunks {
			for i, b := range r.have {
				s.have[i] = b
			}
		}
	}
	var best *PrivateKey
	for _, s := range sets {
		if len(s.have) < s.chunks || s.newEpoch != held.Epoch+1 {
			continue // incomplete, or a later epoch that needs this one first
		}
		key, ok := openBlob(s, sk, me, cid)
		if !ok {
			continue
		}
		if best == nil || bytes.Compare(key[:], best.Key[:]) < 0 {
			best = &PrivateKey{Key: key, Epoch: s.newEpoch, Name: held.Name}
		}
	}
	if best == nil {
		return held, false
	}
	return *best, true
}

func parseRekey(o *Opened) (*rotation, bool) {
	newE, ok1 := parseDecimal(Tag(o.Tags, "newepoch"))
	prevE, ok2 := parseDecimal(Tag(o.Tags, "prevepoch"))
	commit := strings.ToLower(Tag(o.Tags, "prevcommit"))
	if !ok1 || !ok2 || !isHex64(commit) || !isHex64(Tag(o.Tags, "scope")) {
		return nil, false
	}
	idx, count := 1, 1
	for _, t := range o.Tags {
		if len(t) >= 3 && t[0] == "chunk" {
			a, okA := parseDecimal(t[1])
			b, okB := parseDecimal(t[2])
			if !okA || !okB || a < 1 || b < 1 || a > b || b > 1000 {
				return nil, false
			}
			idx, count = int(a), int(b)
			break
		}
	}
	var blobs []rekeyBlob
	if json.Unmarshal([]byte(o.Content), &blobs) != nil {
		return nil, false
	}
	return &rotation{rotator: o.Author, newEpoch: newE, prevEpoch: prevE, prevCommit: commit, chunks: count, have: map[int][]rekeyBlob{idx: blobs}}, true
}

// openBlob is skua's new key in a rotation: her blob, decrypted, naming
// exactly this channel and epoch.
func openBlob(s *rotation, sk string, me, cid [32]byte) ([32]byte, bool) {
	rot, err := hex32(s.rotator)
	if err != nil {
		return [32]byte{}, false
	}
	loc := hexOf(recipientLocator(rot, me, cid, s.newEpoch))
	for _, blobs := range s.have {
		for _, b := range blobs {
			if b.Locator != loc {
				continue
			}
			conv, err := nip44.GenerateConversationKey(s.rotator, sk)
			if err != nil {
				return [32]byte{}, false
			}
			plain, err := nip44.Decrypt(b.Wrapped, conv)
			if err != nil {
				return [32]byte{}, false
			}
			raw, err := base64.StdEncoding.DecodeString(plain)
			if err != nil || len(raw) != 72 || !bytes.Equal(raw[:32], cid[:]) || binary.BigEndian.Uint64(raw[32:40]) != s.newEpoch {
				return [32]byte{}, false
			}
			return [32]byte(raw[40:72]), true
		}
	}
	return [32]byte{}, false
}

// RekeyAddresses is where the next rotations of every private channel skua
// holds would be published, to listen on.
func RekeyAddresses(c *Community) []string {
	var out []string
	for id, held := range c.Private {
		cid, err := hex32(id)
		if err != nil {
			continue
		}
		for e := held.Epoch + 1; e <= held.Epoch+rekeyLookahead; e++ {
			out = append(out, channelRekeyKey(c.Root, cid, e).PK)
		}
	}
	return out
}
