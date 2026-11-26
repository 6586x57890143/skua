// Package game is the bird game library: the parts every channel game needs,
// with no Discord in them, so any bird can carry it whatever its Discord
// library or store. A game keeps its state per channel in a Table, writes it
// behind through Dirty, chains what happened with Link so a result can be
// checked, ranks players with Rank and Top, keys its seasons with Month and
// Week, and levels and combos with Curve and Combo.
//
// The game decides and the caller talks: nothing here sends, stores or
// sleeps, and every time is passed in.
package game

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// Hash is one link of a chain.
type Hash [32]byte

// String is the hash in hex, the form a player copies.
func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// Short is the first eight hex digits, for a card line.
func (h Hash) Short() string { return h.String()[:8] }

// ParseHash reads a hash back from its hex.
func ParseHash(s string) (Hash, error) {
	var h Hash
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(h) {
		return h, fmt.Errorf("game: %q is not a hash", s)
	}
	copy(h[:], b)
	return h, nil
}

// Link is sha256 over prev and each field: an integer as 8 bytes big
// endian, a string or bytes after its length, so no two field lists hash
// alike. A field of any other type is a programming error and panics.
func Link(prev Hash, fields ...any) Hash {
	h := sha256.New()
	h.Write(prev[:])
	var n [8]byte
	put := func(v uint64) {
		binary.BigEndian.PutUint64(n[:], v)
		h.Write(n[:])
	}
	for _, f := range fields {
		switch v := f.(type) {
		case int64:
			put(uint64(v))
		case uint64:
			put(v)
		case int:
			put(uint64(v))
		case string:
			put(uint64(len(v)))
			h.Write([]byte(v))
		case []byte:
			put(uint64(len(v)))
			h.Write(v)
		default:
			panic(fmt.Sprintf("game: Link can't hash a %T", f))
		}
	}
	var out Hash
	h.Sum(out[:0])
	return out
}

// Genesis is a chain's first link: the zero hash linked to a domain that
// names the game and whatever fields fix where the chain lives.
func Genesis(domain string, fields ...any) Hash {
	return Link(Hash{}, append([]any{domain}, fields...)...)
}

// MarshalText writes the hash as hex, so a stored state reads as a player
// copies it.
func (h Hash) MarshalText() ([]byte, error) { return []byte(h.String()), nil }

// UnmarshalText reads a hash written by MarshalText.
func (h *Hash) UnmarshalText(b []byte) error {
	v, err := ParseHash(string(b))
	*h = v
	return err
}
