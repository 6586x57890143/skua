// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

// Package concord is the slice of Armada's Concord protocol (CORD-01 to
// CORD-05) that skua's armada bridge needs: keys, the wrap and seal layers,
// invites, the Control Plane fold and a relay pool. It is a port of
// concord-core from armada-discord-bridge, which in turn is extracted from
// the Armada client. Changing which events are admissible here does not
// harden anything: it makes skua and Armada disagree about who may do what.
package concord

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

// The derivation labels (CORD-01). Only the ones the bridge uses.
const (
	labelChannel   = "concord/channel"
	labelControl   = "concord/control"
	labelGuestbook = "concord/guestbook"
	labelGrant     = "concord/grant"
	labelBanlist   = "concord/banlist"
	labelInviteKey = "concord/invite-key"
	labelCommunity = "concord/community"
)

var zero32 [32]byte

// Key is a keypair a plane is addressed by: the secret, its x-only public
// key, and the NIP-44 conversation key it holds with itself, which is what
// every layer of a stream is encrypted under.
type Key struct {
	SK   string // hex
	PK   string // hex, x-only
	Conv [32]byte
}

// StreamKey is how a stream is read: its address and conversation key. SK is
// empty when skua can read but not sign at the address (a write-restricted
// Control Plane). Restricted streams have their wrap signature checked,
// because there the signer set is narrower than the readership.
type StreamKey struct {
	PK         string
	SK         string
	Conv       [32]byte
	Restricted bool
}

func (k Key) Stream() StreamKey { return StreamKey{PK: k.PK, SK: k.SK, Conv: k.Conv} }

// info is label ‖ 0x00 ‖ id[32] ‖ epoch as u64 big-endian, the epoch only
// when the derivation has one.
func info(label string, id [32]byte, epoch *uint64) []byte {
	out := make([]byte, 0, len(label)+1+32+8)
	out = append(out, label...)
	out = append(out, 0)
	out = append(out, id[:]...)
	if epoch != nil {
		out = binary.BigEndian.AppendUint64(out, *epoch)
	}
	return out
}

func hkdf32(ikm, info []byte) [32]byte {
	b, err := hkdf.Key(sha256.New, ikm, nil, string(info), 32)
	if err != nil {
		panic(err) // only for a length past 255*32
	}
	return [32]byte(b)
}

// validScalar is 0 < k < n, what secp256k1 accepts as a secret key.
func validScalar(b [32]byte) bool {
	var s btcec.ModNScalar
	overflow := s.SetBytes(&b)
	return overflow == 0 && !s.IsZero()
}

// scalar derives a secret key, retrying with a counter byte appended to the
// info in the ~2^-128 case the output is not a valid scalar. Deterministic,
// because the first valid counter always wins.
func scalar(ikm, base []byte) [32]byte {
	if k := hkdf32(ikm, base); validScalar(k) {
		return k
	}
	for c := 0; c <= 0xff; c++ {
		if k := hkdf32(ikm, append(append([]byte{}, base...), byte(c))); validScalar(k) {
			return k
		}
	}
	panic("concord: scalar rejection 257 times running is impossible")
}

// KeyFromSecret is the keypair of a 32-byte secret.
func KeyFromSecret(sk [32]byte) (Key, error) {
	skHex := hex.EncodeToString(sk[:])
	pk, err := nostr.GetPublicKey(skHex)
	if err != nil {
		return Key{}, err
	}
	conv, err := nip44.GenerateConversationKey(pk, skHex)
	if err != nil {
		return Key{}, err
	}
	return Key{SK: skHex, PK: pk, Conv: conv}, nil
}

func groupKey(label string, secret, id [32]byte, epoch *uint64) Key {
	k, err := KeyFromSecret(scalar(secret[:], info(label, id, epoch)))
	if err != nil {
		panic(err) // the scalar is valid by construction
	}
	return k
}

// ChannelKey addresses a channel's Chat Plane under secret (the community
// root for a public channel, the channel key for a private one) at epoch.
func ChannelKey(secret, channel [32]byte, epoch uint64) Key {
	return groupKey(labelChannel, secret, channel, &epoch)
}

func controlKey(root, community [32]byte, epoch uint64) Key {
	return groupKey(labelControl, root, community, &epoch)
}

func guestbookKey(root, community [32]byte, epoch uint64) Key {
	return groupKey(labelGuestbook, root, community, &epoch)
}

// grantLocator is the entity id of a member's Grant.
func grantLocator(community, member [32]byte) [32]byte {
	return hkdf32(community[:], info(labelGrant, member, nil))
}

func banlistLocator(community [32]byte) [32]byte {
	return hkdf32(community[:], info(labelBanlist, zero32, nil))
}

// inviteBundleKey is the NIP-44 conversation key an invite's token opens
// its bundle with.
func inviteBundleKey(token []byte) [32]byte {
	return hkdf32(token, info(labelInviteKey, zero32, nil))
}

// communityID is the self-certifying id: sha256(label ‖ owner ‖ salt), so
// nothing can name a false owner for a community.
func communityID(owner, salt [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(labelCommunity))
	h.Write(owner[:])
	h.Write(salt[:])
	return [32]byte(h.Sum(nil))
}

var errHex32 = errors.New("concord: not 64 hex characters")

// hex32 decodes exactly 64 hex characters, either case.
func hex32(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 64 {
		return out, errHex32
	}
	if _, err := hex.Decode(out[:], []byte(s)); err != nil {
		return out, fmt.Errorf("%w: %v", errHex32, err)
	}
	return out, nil
}

func isHex64(s string) bool {
	_, err := hex32(s)
	return err == nil
}
