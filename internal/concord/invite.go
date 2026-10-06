// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/nbd-wtf/go-nostr/nip44"
)

// An invite (CORD-05) is a URL in two parts: a public locator in the path,
// a bare naddr naming the addressable bundle (kind 33301, the link signer,
// d=""), and a secret in the fragment: a 16-byte token and up to 3
// bootstrap relays. The fragment never reaches a server. The token derives
// the bundle's decrypt key, and the bundle carries the membership keys.

const (
	tokenBytes       = 16
	maxBootstrap     = 3
	fragmentVersion  = 4
	flagStockSet     = 0x01
	maxBundleChans   = 256
	maxRelays        = 5
	vskInviteLive    = "6"
	vskInviteRevoked = "9"
)

// relayDictionary is generation 4 of the stock relays, each named by one
// byte in a fragment. Vector and Soapbox ship it identically.
var relayDictionary = map[byte]string{
	1: "wss://jskitty.com/nostr",
	2: "wss://asia.vectorapp.io/nostr",
	3: "wss://relay.ditto.pub",
	4: "wss://relay.dreamith.to",
}

var stockRelays = []string{relayDictionary[1], relayDictionary[2], relayDictionary[3], relayDictionary[4]}

// Invite is a parsed invite link.
type Invite struct {
	Signer    string // the link signer, the bundle's author
	Token     []byte
	Bootstrap []string
}

var errInvite = errors.New("concord: not a valid armada invite")

// ParseInvite reads a full invite URL (.../invite/<naddr>#<fragment>) or the
// bare form (<naddr>#<fragment>).
func ParseInvite(link string) (Invite, error) {
	link = strings.TrimSpace(link)
	var naddr, fragment string
	if strings.HasPrefix(strings.ToLower(link), "naddr1") {
		naddr, fragment, _ = strings.Cut(link, "#")
	} else {
		u, err := url.Parse(link)
		if err != nil || !strings.HasPrefix(u.Path, "/invite/") {
			return Invite{}, errInvite
		}
		naddr = strings.TrimSuffix(strings.TrimPrefix(u.Path, "/invite/"), "/")
		fragment = u.Fragment
	}
	if naddr == "" || fragment == "" {
		return Invite{}, errInvite
	}
	signer, ok := bundleSigner(naddr)
	if !ok {
		return Invite{}, errInvite
	}
	token, relays, err := decodeFragment(fragment)
	if err != nil {
		return Invite{}, err
	}
	return Invite{Signer: signer, Token: token, Bootstrap: relays}, nil
}

// bundleSigner is the author of an invite bundle coordinate. An invite's
// identifier is empty by design, which go-nostr reports as an incomplete
// naddr while still handing back what it read, so that one error is
// accepted when everything else names a bundle.
func bundleSigner(naddr string) (string, bool) {
	prefix, v, _ := nip19.Decode(naddr)
	p, ok := v.(nostr.EntityPointer)
	if prefix != "naddr" || !ok || p.Kind != KindInviteBundle || p.Identifier != "" || !isHex64(p.PublicKey) {
		return "", false
	}
	return p.PublicKey, true
}

// decodeFragment reads [version][flags][relays?][token:16], base64url with
// no padding. Each relay is a dictionary byte, a wss host (0, len, host) or
// a verbatim URL (255, len, url).
func decodeFragment(fragment string) ([]byte, []string, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(fragment), "="))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: fragment is not base64url", errInvite)
	}
	o := 0
	need := func(n int) error {
		if o+n > len(b) {
			return fmt.Errorf("%w: fragment truncated", errInvite)
		}
		return nil
	}
	if err := need(2); err != nil {
		return nil, nil, err
	}
	if v := b[o]; v != fragmentVersion {
		return nil, nil, fmt.Errorf("%w: invite format %d", errInvite, v)
	}
	flags := b[o+1]
	o += 2
	var relays []string
	if flags&flagStockSet != 0 {
		relays = append(relays, stockRelays...)
	} else {
		if err := need(1); err != nil {
			return nil, nil, err
		}
		count := int(b[o])
		o++
		if count > maxBootstrap {
			return nil, nil, fmt.Errorf("%w: too many bootstrap relays", errInvite)
		}
		for range count {
			if err := need(1); err != nil {
				return nil, nil, err
			}
			lead := b[o]
			o++
			if lead >= 1 && lead <= 254 {
				// An unknown id is skipped, not fatal: the dictionary grows.
				if u, ok := relayDictionary[lead]; ok {
					relays = append(relays, u)
				}
				continue
			}
			if err := need(1); err != nil {
				return nil, nil, err
			}
			n := int(b[o])
			o++
			if err := need(n); err != nil {
				return nil, nil, err
			}
			text := string(b[o : o+n])
			o += n
			if lead == 255 {
				relays = append(relays, text)
			} else {
				relays = append(relays, "wss://"+text)
			}
		}
	}
	if err := need(tokenBytes); err != nil {
		return nil, nil, err
	}
	token := b[o : o+tokenBytes]
	if o+tokenBytes != len(b) {
		return nil, nil, fmt.Errorf("%w: trailing bytes in fragment", errInvite)
	}
	return token, relays, nil
}

// bundle is the decrypted invite bundle. Only what joining needs.
type bundle struct {
	CommunityID string `json:"community_id"`
	Owner       string `json:"owner"`
	OwnerSalt   string `json:"owner_salt"`
	Root        string `json:"community_root"`
	RootEpoch   uint64 `json:"root_epoch"`
	ControlPK   string `json:"control_pk"`
	Channels    []struct {
		ID    string `json:"id"`
		Key   string `json:"key"`
		Epoch uint64 `json:"epoch"`
		Name  string `json:"name"`
	} `json:"channels"`
	Relays    []string `json:"relays"`
	Name      string   `json:"name"`
	ExpiresAt *float64 `json:"expires_at"`
}

// Community is what membership is read from: its id, its owner, the root
// every member holds at the current epoch, and the private channel keys the
// invite granted. Only the current root is held: a bridge reads live
// traffic, and a root rotation is picked up by resolving the invite again,
// which is also how upstream's bridge learned one.
type Community struct {
	ID        [32]byte
	IDHex     string
	Owner     string
	Root      [32]byte
	RootEpoch uint64
	ControlPK string // empty for a legacy, pre-split Control Plane
	Private   map[string]PrivateKey
	Relays    []string
	Name      string
}

// PrivateKey is a held private channel's key at its epoch.
type PrivateKey struct {
	Key   [32]byte
	Epoch uint64
	Name  string
}

// Resolve fetches the invite's bundle from its bootstrap relays, decrypts
// it with the token, and checks it: the link signer signed it, it is live
// and not expired, and the owner reproduces the community id, so even a
// compromised creator can't smuggle in a false owner.
func Resolve(ctx context.Context, inv Invite, q querier) (*Community, error) {
	events := q.Query(ctx, nostr.Filter{Kinds: []int{KindInviteBundle}, Authors: []string{inv.Signer}, Limit: 8})
	var newest *nostr.Event
	for _, ev := range events {
		if ev.Kind == KindInviteBundle && ev.PubKey == inv.Signer && validEvent(ev) && (newest == nil || ev.CreatedAt > newest.CreatedAt) {
			newest = ev
		}
	}
	if newest == nil {
		return nil, errors.New("concord: invite bundle not found on its relays")
	}
	return openBundle(newest, inv.Token, nowMS())
}

func openBundle(ev *nostr.Event, token []byte, now int64) (*Community, error) {
	switch Tag(tagsOf(ev), "vsk") {
	case vskInviteLive:
	case vskInviteRevoked:
		return nil, errors.New("concord: this invite link has been revoked")
	default:
		return nil, errors.New("concord: unknown bundle marker")
	}
	plain, err := nip44.Decrypt(ev.Content, inviteBundleKey(token))
	if err != nil {
		return nil, fmt.Errorf("concord: bundle decrypt: %w", err)
	}
	var b bundle
	if err := json.Unmarshal([]byte(plain), &b); err != nil {
		return nil, fmt.Errorf("concord: bundle parse: %w", err)
	}
	if len(b.Channels) > maxBundleChans {
		return nil, fmt.Errorf("concord: bundle carries %d channels (cap %d)", len(b.Channels), maxBundleChans)
	}
	if b.ExpiresAt != nil && float64(now) > *b.ExpiresAt {
		return nil, errors.New("concord: this invite link has expired")
	}
	return community(b)
}

func community(b bundle) (*Community, error) {
	id, err1 := hex32(b.CommunityID)
	owner, err2 := hex32(b.Owner)
	salt, err3 := hex32(b.OwnerSalt)
	root, err4 := hex32(b.Root)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return nil, fmt.Errorf("concord: bundle: %w", err)
	}
	if communityID(owner, salt) != id {
		return nil, errors.New("concord: bundle's owner does not reproduce its community_id")
	}
	c := &Community{
		ID: id, IDHex: strings.ToLower(b.CommunityID), Owner: strings.ToLower(b.Owner),
		Root: root, RootEpoch: b.RootEpoch, Private: map[string]PrivateKey{},
		Relays: capRelays(b.Relays), Name: b.Name,
	}
	if isHex64(b.ControlPK) {
		c.ControlPK = strings.ToLower(b.ControlPK)
	}
	for _, ch := range b.Channels {
		cid, err1 := hex32(ch.ID)
		key, err2 := hex32(ch.Key)
		if err1 != nil || err2 != nil {
			continue // one bad entry costs that channel, not the community
		}
		c.Private[hexOf(cid)] = PrivateKey{Key: key, Epoch: ch.Epoch, Name: ch.Name}
	}
	return c, nil
}

// capRelays keeps the first five distinct non-empty relays.
func capRelays(relays []string) []string {
	out := []string{}
	for _, r := range relays {
		if len(out) == maxRelays {
			break
		}
		if r != "" && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func tagsOf(ev *nostr.Event) [][]string {
	out := make([][]string, len(ev.Tags))
	for i, t := range ev.Tags {
		out[i] = t
	}
	return out
}
