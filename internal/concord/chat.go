// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"errors"
	"strconv"

	"github.com/nbd-wtf/go-nostr"
)

// ChatChannel is a channel skua can read: the streams it reads (every key it
// holds for it) and the one it writes to.
type ChatChannel struct {
	ID      [32]byte
	IDHex   string
	Name    string
	Private bool
	Streams []Stream
	Current Stream
}

// Stream is one key's view of a channel at one epoch.
type Stream struct {
	Epoch uint64
	Key   Key
}

// Channels is every channel skua can read, per the fold. A public channel
// writes to the root's stream; a private one to its own key's, and is left
// out when skua holds no key for it rather than shown and never readable.
// Deleted channels are dropped (CORD-03 §2).
func Channels(c *Community, f *Folded) map[string]ChatChannel {
	out := map[string]ChatChannel{}
	if f == nil {
		return out
	}
	for id, def := range f.Channels {
		if def.Deleted {
			continue
		}
		cid, err := hex32(id)
		if err != nil {
			continue
		}
		root := Stream{c.RootEpoch, ChannelKey(c.Root, cid, c.RootEpoch)}
		ch := ChatChannel{ID: cid, IDHex: id, Name: def.Name, Private: def.Private}
		held, ok := c.Private[id]
		var own Stream
		if ok {
			own = Stream{held.Epoch, ChannelKey(held.Key, cid, held.Epoch)}
		}
		switch {
		case !def.Private && ok:
			ch.Streams, ch.Current = []Stream{root, own}, root
		case !def.Private:
			ch.Streams, ch.Current = []Stream{root}, root
		case ok:
			// Writes go to the channel key; its public-era history stays readable.
			ch.Streams, ch.Current = []Stream{own, root}, own
		default:
			continue
		}
		out[id] = ch
	}
	return out
}

// Authors is every stream address the channel is read at.
func (ch ChatChannel) Authors() []string {
	out := make([]string, len(ch.Streams))
	for i, s := range ch.Streams {
		out[i] = s.Key.PK
	}
	return out
}

// OpenChat opens a wrap read at one of the channel's streams. Chat seals
// must be encrypted (CORD-02 §5): a plaintext one would make the message a
// standalone signed artifact any relay could show. The rumor must commit to
// exactly this channel and the epoch whose key opened it, or a keyholder
// could splice one author's rumor into a context they never chose.
func OpenChat(w *nostr.Event, ch ChatChannel) (*Opened, error) {
	for _, s := range ch.Streams {
		if s.Key.PK != w.PubKey {
			continue
		}
		o, err := Open(w, s.Key.Stream())
		if err != nil {
			return nil, err
		}
		if o.SealKind != KindSealEncrypted {
			return nil, streamErr("chat seal must be encrypted")
		}
		c, err1 := uniqueTag(o.Tags, "channel")
		e, err2 := uniqueTag(o.Tags, "epoch")
		if err := errors.Join(err1, err2); err != nil {
			return nil, err
		}
		if c != ch.IDHex || e != strconv.FormatUint(s.Epoch, 10) {
			return nil, streamErr("binding mismatch (splice)")
		}
		return o, nil
	}
	return nil, streamErr("no held key for this stream")
}

// uniqueTag is the value of a tag that must appear at most once, so the
// binding is never ambiguous.
func uniqueTag(tags [][]string, name string) (string, error) {
	val, found := "", false
	for _, t := range tags {
		if len(t) == 0 || t[0] != name {
			continue
		}
		if found {
			return "", streamErr("duplicate binding tag: " + name)
		}
		found = true
		if len(t) > 1 {
			val = t[1]
		}
	}
	return val, nil
}

// bindingTags are what a chat rumor must commit to (CORD-03 §3).
func bindingTags(ch ChatChannel) [][]string {
	return [][]string{{"channel", ch.IDHex}, {"epoch", strconv.FormatUint(ch.Current.Epoch, 10)}}
}

// NewChat builds a rumor for ch with its binding tags first.
func NewChat(ch ChatChannel, kind int, content string, tags [][]string, author string, ms int64) (*Rumor, error) {
	return NewRumor(kind, content, append(bindingTags(ch), tags...), author, ms)
}

// SealChat seals and wraps a chat rumor for ch's current stream.
func SealChat(r *Rumor, ch ChatChannel, authorSK string) (*nostr.Event, error) {
	return Seal(r, ch.Current.Key.Stream(), authorSK)
}

// ControlStream is the Control Plane's read view at the current root. A
// split epoch (with a control pk) is addressed and signed by a key only
// staff hold and encrypted under the root-derived read key; a legacy one
// is the read key whole.
func ControlStream(c *Community) StreamKey {
	read := controlKey(c.Root, c.ID, c.RootEpoch)
	if c.ControlPK == "" {
		return read.Stream()
	}
	return StreamKey{PK: c.ControlPK, Conv: read.Conv, Restricted: true}
}

// GuestbookKey is where joins are published.
func GuestbookKey(c *Community) StreamKey {
	return guestbookKey(c.Root, c.ID, c.RootEpoch).Stream()
}

// FoldControl opens the Control Plane's wraps and folds them.
func FoldControl(c *Community, wraps []*nostr.Event) *Folded {
	return Fold(openControl(wraps, ControlStream(c)), c.ID, c.Owner)
}

// Join is a guestbook join for author at ms, sealed and wrapped.
func Join(c *Community, author, authorSK string, ms int64) (*nostr.Event, error) {
	r, err := NewRumor(KindJoinLeave, "join", nil, author, ms)
	if err != nil {
		return nil, err
	}
	return Seal(r, GuestbookKey(c), authorSK)
}
