// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A Klipy send from Discord's GIF picker is a page link,
// https://klipy.com/gifs/<slug>, and nothing keyless gets from there to
// the GIF: its file names are random per format, the media CDN answers any
// missing file with a placeholder GIF, so guessing "works" every time, and
// klipy.com puts a Cloudflare challenge in front of a server. Klipy's Items
// API turns the slug into the real renditions, so with a key the link
// crosses as the GIF itself, which Armada shows inline. Without one it stays
// a link.

const klipyAPI = "https://api.klipy.com/api/v1"

var klipyPage = regexp.MustCompile(`^/(gifs|stickers)/([A-Za-z0-9_-]{1,200})/?$`)

type klipy struct {
	key  string
	api  string
	http *http.Client
}

// klipyGIF is one GIF rendition.
type klipyGIF struct {
	URL           string
	Width, Height int
}

// klipyItem is the kind and slug a Klipy page link names, or ok false for
// anything else.
func klipyItem(raw string) (kind, slug string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || (u.Host != "klipy.com" && u.Host != "www.klipy.com") {
		return "", "", false
	}
	m := klipyPage.FindStringSubmatch(u.Path)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// klipyMedia is whether raw is on Klipy's own CDN, the only place a GIF the
// API names may come from.
func klipyMedia(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && (u.Host == "klipy.com" || strings.HasSuffix(u.Host, ".klipy.com"))
}

// gif is the largest GIF rendition of the item page names that fits
// maxFile, or ok false when page isn't a Klipy item or the lookup fails:
// this is an upgrade, and the message crosses without it.
func (k *klipy) gif(ctx context.Context, page string) (klipyGIF, bool) {
	kind, slug, ok := klipyItem(page)
	if !ok {
		return klipyGIF{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("%s/%s/%s/items?slugs=%s&customer_id=skua", k.api, url.PathEscape(k.key), kind, url.QueryEscape(slug))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return klipyGIF{}, false
	}
	res, err := k.http.Do(req)
	if err != nil {
		return klipyGIF{}, false
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return klipyGIF{}, false
	}
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body) != nil {
		return klipyGIF{}, false
	}
	// data is either the items or an object holding them.
	var items []klipyEntry
	if json.Unmarshal(body.Data, &items) != nil {
		var wrapped struct {
			Data []klipyEntry `json:"data"`
		}
		_ = json.Unmarshal(body.Data, &wrapped)
		items = wrapped.Data
	}
	for _, it := range items {
		if it.Slug == slug || len(items) == 1 {
			return it.pick()
		}
	}
	return klipyGIF{}, false
}

type klipyFile struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int    `json:"size"`
}

type klipyEntry struct {
	Slug string `json:"slug"`
	File map[string]struct {
		GIF *klipyFile `json:"gif"`
	} `json:"file"`
}

// pick is the largest rendition under maxFile, else the smallest listed.
// Only a URL on Klipy's CDN is taken, so an answer can't put anything else
// in a member's message.
func (e klipyEntry) pick() (klipyGIF, bool) {
	var last *klipyFile
	for _, size := range []string{"hd", "md", "sm", "xs"} {
		f := e.File[size].GIF
		if f == nil || !klipyMedia(f.URL) {
			continue
		}
		last = f
		if f.Size == 0 || f.Size <= maxFile {
			return klipyGIF{f.URL, f.Width, f.Height}, true
		}
	}
	if last == nil {
		return klipyGIF{}, false
	}
	return klipyGIF{last.URL, last.Width, last.Height}, true
}

// tag is the NIP-92 tag for the GIF: exactly the URL that ends up in the
// content, since Armada keys inline media on the URL byte for byte.
func (g klipyGIF) tag() []string {
	t := []string{"imeta", "url " + g.URL, "m image/gif"}
	if g.Width > 0 && g.Height > 0 {
		t = append(t, "dim "+strconv.Itoa(g.Width)+"x"+strconv.Itoa(g.Height))
	}
	return t
}
