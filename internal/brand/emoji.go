package brand

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

//go:embed emoji/*.png
var emojiArt embed.FS

// Emoji is one of skua's application emoji. Key is what code asks for
// ("skua_ok", "mod_purge"); Name is what it is called on Discord, the key
// and the start of its PNG's hash, so a redrawn icon is a new emoji and an
// unchanged one is never uploaded twice.
type Emoji struct {
	Key, Name string
	PNG       []byte
}

// Emojis is every emoji skua uploads, read from the embedded art.
func Emojis() []Emoji {
	entries, _ := emojiArt.ReadDir("emoji") // embedded; the pattern matched at build
	out := make([]Emoji, 0, len(entries))
	for _, e := range entries {
		png, _ := emojiArt.ReadFile("emoji/" + e.Name())
		sum := sha256.Sum256(png)
		key := strings.TrimSuffix(e.Name(), ".png")
		out = append(out, Emoji{Key: key, Name: key + "_" + hex.EncodeToString(sum[:3]), PNG: png})
	}
	return out
}

// emojiIDs is key -> ID for every emoji the last sync found or made. Nil
// until a sync has run: then every icon goes as an attachment, as in tests.
// Sync builds a new map and swaps it in whole, so a reply never sees one
// half built and reads take no lock.
var emojiIDs atomic.Pointer[map[string]emojiRef]

type emojiRef struct {
	id   snowflake.ID
	name string
}

// emoji is the emoji for key and whether the sync has one.
func emoji(key string) (emojiRef, bool) {
	ids := emojiIDs.Load()
	if ids == nil {
		return emojiRef{}, false
	}
	r, ok := (*ids)[key]
	return r, ok
}

// Mention is key's emoji written inline, <:name:id>, or "" when there is
// none to show.
func Mention(key string) string {
	r, ok := emoji(key)
	if !ok {
		return ""
	}
	return "<:" + r.name + ":" + r.id.String() + ">"
}

// emojiURL is the CDN address of key's emoji, for a thumbnail. PNG, not
// WebP: Discord does not take WebP in every image slot.
func emojiURL(key string) (string, bool) {
	r, ok := emoji(key)
	if !ok {
		return "", false
	}
	return "https://cdn.discordapp.com/emojis/" + r.id.String() + ".png", true
}

// EmojiRest is the REST Sync calls.
type EmojiRest interface {
	GetApplicationEmojis(app snowflake.ID, opts ...rest.RequestOpt) ([]discord.Emoji, error)
	CreateApplicationEmoji(app snowflake.ID, c discord.EmojiCreate, opts ...rest.RequestOpt) (*discord.Emoji, error)
}

// Sync makes sure every emoji is uploaded to app, creating those missing
// by name and leaving every other emoji alone, then serves icons from
// them. It is safe to run on every boot: with nothing changed it is one
// list call. An emoji it could not create still goes as an attachment.
//
// ponytail: a redrawn icon leaves its old emoji behind; an app holds 2000,
// so pruning the stale ones waits until that is near.
func Sync(ctx context.Context, r EmojiRest, app snowflake.ID, g *guard.Guard) error {
	have, err := r.GetApplicationEmojis(app, rest.WithCtx(ctx))
	if err != nil {
		emojiIDs.Store(nil) // nothing known: every icon goes as an attachment
		return fmt.Errorf("listing application emoji: %w", err)
	}
	byName := make(map[string]snowflake.ID, len(have))
	for _, e := range have {
		byName[e.Name] = e.ID
	}
	ids := map[string]emojiRef{}
	var errs []error
	for _, e := range Emojis() {
		if id, ok := byName[e.Name]; ok {
			ids[e.Key] = emojiRef{id, e.Name}
			continue
		}
		// Application emoji belong to no guild: they share guild 0's budget.
		if err := g.Allow(0, guard.EmojiCreate); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name, err))
			continue
		}
		made, err := r.CreateApplicationEmoji(app, discord.EmojiCreate{
			Name: e.Name, Image: *discord.NewIconRaw(discord.IconTypePNG, e.PNG),
		}, rest.WithCtx(ctx))
		g.Report(0, struggling(err))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name, err))
			continue
		}
		ids[e.Key] = emojiRef{made.ID, e.Name}
	}
	emojiIDs.Store(&ids)
	return errors.Join(errs...)
}

// struggling is true only for answers that say Discord is, not the request.
func struggling(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	if !ok || re.Response == nil {
		return false
	}
	s := re.Response.StatusCode
	return s == 429 || s >= 500
}
