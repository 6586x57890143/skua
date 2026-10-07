// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

// The kinds the bridge reads or writes (CORD-02 Appendix B).
const (
	KindWrap          = 1059
	KindWrapEphemeral = 21059
	KindSealEncrypted = 20013
	KindSealPlaintext = 20014
	KindMessage       = 9
	KindComment       = 1111
	KindDelete        = 5
	KindReaction      = 7
	KindEdit          = 3302
	KindJoinLeave     = 3306
	KindControl       = 3308
	KindInviteBundle  = 33301
	KindDMRelays      = 10050
)

// maxPlaintext is NIP-44's cap, enforced at every layer: a lenient publisher
// mints events a strict reader cannot decrypt.
const maxPlaintext = 65535

// A stream event is a kind 1059 wrap that reverses NIP-59: its author is the
// plane's derived key, it carries a random p tag, and it is encrypted under
// the stream's own conversation key. Inside is a seal signed by the author's
// real key, around an unsigned rumor with the functional kind:
//
//	wrap (1059/21059, signed by the stream key)
//	  seal (20013 encrypted, 20014 plaintext; signed by the author)
//	    rumor (unsigned, the functional kind)

// Rumor is an unsigned event. Its JSON has no sig, as on the wire.
type Rumor struct {
	ID        string     `json:"id"`
	PubKey    string     `json:"pubkey"`
	CreatedAt int64      `json:"created_at"`
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
}

func (r *Rumor) event() *nostr.Event {
	tags := make(nostr.Tags, len(r.Tags))
	for i, t := range r.Tags {
		tags[i] = nostr.Tag(t)
	}
	return &nostr.Event{PubKey: r.PubKey, CreatedAt: nostr.Timestamp(r.CreatedAt), Kind: r.Kind, Tags: tags, Content: r.Content}
}

// hash is the rumor's NIP-01 id.
func (r *Rumor) hash() string { return r.event().GetID() }

// NewRumor builds a rumor sent at ms (epoch milliseconds): created_at holds
// the seconds and an ms tag the 0 to 999 remainder (CORD-02 §4). A negative
// time would mint an ms tag every reader drops, so it is refused.
func NewRumor(kind int, content string, tags [][]string, pubkey string, ms int64) (*Rumor, error) {
	if ms < 0 {
		return nil, fmt.Errorf("concord: send time must be a non-negative epoch-ms, got %d", ms)
	}
	all := make([][]string, 0, len(tags)+1)
	all = append(all, tags...)
	all = append(all, []string{"ms", strconv.FormatInt(ms%1000, 10)})
	r := &Rumor{PubKey: pubkey, CreatedAt: ms / 1000, Kind: kind, Tags: all, Content: content}
	r.ID = r.hash()
	return r, nil
}

func encrypt(conv [32]byte, plaintext []byte) (string, error) {
	if len(plaintext) > maxPlaintext {
		return "", errors.New("concord: plaintext exceeds the NIP-44 65,535-byte cap")
	}
	return nip44.Encrypt(string(plaintext), conv)
}

// seal signs rumor as its author. An encrypted seal NIP-44s the rumor under
// the stream key first, so no layer can be lifted out as a standalone public
// event.
func seal(r *Rumor, kind int, s StreamKey, authorSK string) (*nostr.Event, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	content := string(raw)
	if kind == KindSealEncrypted {
		if content, err = encrypt(s.Conv, raw); err != nil {
			return nil, err
		}
	}
	ev := &nostr.Event{Kind: kind, Content: content, Tags: nostr.Tags{}, CreatedAt: nostr.Timestamp(r.CreatedAt)}
	if err := ev.Sign(authorSK); err != nil {
		return nil, err
	}
	return ev, nil
}

// wrap puts a signed seal in the outer stream event, signed by the stream
// key and tagged with a random p. created_at is not tweaked (CORD-01).
func wrap(sealed *nostr.Event, s StreamKey, kind int) (*nostr.Event, error) {
	if s.SK == "" {
		return nil, errors.New("concord: no secret for this stream address")
	}
	raw, err := json.Marshal(sealed)
	if err != nil {
		return nil, err
	}
	content, err := encrypt(s.Conv, raw)
	if err != nil {
		return nil, err
	}
	var eph [32]byte
	for !validScalar(eph) {
		_, _ = rand.Read(eph[:])
	}
	ephPK, err := nostr.GetPublicKey(hex.EncodeToString(eph[:]))
	if err != nil {
		return nil, err
	}
	ev := &nostr.Event{Kind: kind, Content: content, Tags: nostr.Tags{{"p", ephPK}}, CreatedAt: nostr.Now()}
	if err := ev.Sign(s.SK); err != nil {
		return nil, err
	}
	return ev, nil
}

// Seal seals rumor (encrypted) as its author and wraps it for s.
func Seal(r *Rumor, s StreamKey, authorSK string) (*nostr.Event, error) {
	sealed, err := seal(r, KindSealEncrypted, s, authorSK)
	if err != nil {
		return nil, err
	}
	return wrap(sealed, s, KindWrap)
}

// Opened is a fully opened and verified stream event.
type Opened struct {
	RumorID   string
	Author    string // the seal's signer, which is also the rumor's pubkey
	Kind      int
	Content   string
	Tags      [][]string
	MS        int64 // created_at*1000 + the ms tag
	CreatedAt int64
	WrapID    string
	StreamPK  string
	SealKind  int
}

var errStream = errors.New("concord: not a valid stream event")

func streamErr(why string) error { return fmt.Errorf("%w: %s", errStream, why) }

// validEvent is nostr-tools' verifyEvent: the id is the hash and the
// signature holds.
func validEvent(ev *nostr.Event) bool {
	if !ev.CheckID() {
		return false
	}
	ok, _ := ev.CheckSignature()
	return ok
}

// Open opens and verifies one stream wrap under s:
//
//  1. the wrap's author must be the stream address. On an ordinary stream
//     its signature is not checked: every reader holds the key that made
//     it, so it proves nothing. On a write-restricted one it is the write
//     gate and is checked;
//  2. the seal must be a known seal kind with a valid signature;
//  3. the rumor's id must be its hash (an id is the ordering tiebreak, so a
//     claimed one is never trusted) and its pubkey must be the seal's
//     signer, or a keyholder could re-seal another member's rumor as their
//     own.
func Open(w *nostr.Event, s StreamKey) (*Opened, error) {
	if w.Kind != KindWrap && w.Kind != KindWrapEphemeral {
		return nil, streamErr("wrap kind")
	}
	if w.PubKey != s.PK {
		return nil, streamErr("wrap author is not this stream's address")
	}
	if s.Restricted && !validEvent(w) {
		return nil, streamErr("write-restricted wrap signature")
	}
	plain, err := nip44.Decrypt(w.Content, s.Conv)
	if err != nil {
		return nil, streamErr("wrap decrypt")
	}
	var sealed nostr.Event
	if err := json.Unmarshal([]byte(plain), &sealed); err != nil {
		return nil, streamErr("seal parse")
	}
	if sealed.Kind != KindSealEncrypted && sealed.Kind != KindSealPlaintext {
		return nil, streamErr("seal kind")
	}
	if !validEvent(&sealed) {
		return nil, streamErr("seal signature")
	}
	body := sealed.Content
	if sealed.Kind == KindSealEncrypted {
		if body, err = nip44.Decrypt(sealed.Content, s.Conv); err != nil {
			return nil, streamErr("rumor decrypt")
		}
	}
	var r Rumor
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return nil, streamErr("rumor parse")
	}
	if r.Tags == nil {
		r.Tags = [][]string{}
	}
	if r.PubKey != sealed.PubKey {
		return nil, streamErr("rumor author does not match the seal's signer")
	}
	if r.ID != r.hash() {
		return nil, streamErr("rumor id is not its event hash")
	}
	ms, err := resolveMS(r.CreatedAt, r.Tags)
	if err != nil {
		return nil, err
	}
	return &Opened{
		RumorID: r.ID, Author: sealed.PubKey, Kind: r.Kind, Content: r.Content, Tags: r.Tags,
		MS: ms, CreatedAt: r.CreatedAt, WrapID: w.ID, StreamPK: w.PubKey, SealKind: sealed.Kind,
	}, nil
}

// msTag is a plain decimal 0 to 999 with no leading zeros. Number() would
// take "", "0x1f", "1e2" or " 5 ", and two clients disagreeing on that
// diverge on the ordering every comparison rides on (CORD-02 §4/§5).
var msTag = regexp.MustCompile(`^(0|[1-9][0-9]{0,2})$`)

func resolveMS(createdAt int64, tags [][]string) (int64, error) {
	for _, t := range tags {
		if len(t) == 0 || t[0] != "ms" {
			continue
		}
		if len(t) < 2 || !msTag.MatchString(t[1]) {
			return 0, streamErr("malformed ms tag")
		}
		n, _ := strconv.ParseInt(t[1], 10, 64)
		return createdAt*1000 + n, nil
	}
	return createdAt * 1000, nil
}

// Tag is the first value of the first tag called name, or "".
func Tag(tags [][]string, name string) string {
	for _, t := range tags {
		if len(t) >= 2 && t[0] == name {
			return t[1]
		}
	}
	return ""
}

// HasTag is whether any tag is called name.
func HasTag(tags [][]string, name string) bool {
	for _, t := range tags {
		if len(t) > 0 && t[0] == name {
			return true
		}
	}
	return false
}

// nowMS is a seam for tests.
var nowMS = func() int64 { return time.Now().UnixMilli() }
