package fix

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in canaries exercise live contracts, not just a frozen JSON example.
// No Discord token is used and no message is posted. Ordinary CI stays offline.
func TestLiveProviders(t *testing.T) {
	if os.Getenv("SKUA_FIX_LIVE") != "1" {
		t.Skip("set SKUA_FIX_LIVE=1 for public API canaries")
	}
	a, err := NewAPI(FromEnv())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, raw string
		audio     bool
	}{
		{"x-v2", "https://x.com/jack/status/20", false},
		{"x-video-v2", "https://x.com/IndieGameJoe/status/2076971764257575262", false},
		{"x-gif-v2", "https://x.com/RoRoFli/status/2031318660011188567", false},
		{"bluesky-v2", "https://bsky.app/profile/bsky.app/post/3mqafridzgk2e", false},
		{"reddit-video-audio", "https://www.reddit.com/r/marvelrivals/comments/1vclj7w/avengers_vs_ultron_animation_by_sarpsarter/", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			l, ok := Parse(tc.raw)
			if !ok {
				t.Fatal("invalid canary URL")
			}
			started := time.Now()
			var p Post
			var err error
			if tc.audio {
				p, err = a.Resolve(ctx, l)
			} else {
				p, err = a.fx(ctx, l)
			}
			if err != nil {
				t.Fatalf("live metadata contract: %v", err)
			}
			if p.Text == "" && p.Title == "" {
				t.Fatal("missing post text")
			}
			elapsed := time.Since(started)
			t.Logf("metadata %s", elapsed.Round(time.Millisecond))
			if elapsed > 4*time.Second {
				t.Errorf("metadata exceeded 4s target: %s", elapsed)
			}
			buildStart := time.Now()
			msg := card(l, p)
			if elapsed := time.Since(buildStart); elapsed > 5*time.Millisecond {
				t.Errorf("local card build exceeded 5ms target: %s", elapsed)
			}
			if tc.name == "x-video-v2" || tc.name == "x-gif-v2" {
				if len(p.Media) == 0 {
					t.Fatal("video/GIF media missing")
				}
				u, _ := url.Parse(p.Media[0].URL)
				if u == nil || u.Host != "video.twimg.com" || !strings.HasSuffix(u.Path, ".mp4") {
					t.Fatal("video/GIF must use a playable MP4")
				}
			}
			if len(msg.Components) != 1 {
				t.Fatal("missing V2 card")
			}
			if !tc.audio {
				return
			}
			if len(p.Media) == 0 {
				t.Fatal("Reddit video missing")
			}
			u, err := url.Parse(p.Media[0].URL)
			if err != nil || u.Scheme != "https" || u.Host != "packaged-media.redd.it" || u.User != nil {
				t.Fatal("Reddit did not provide a combined audio/video rendition")
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Range", "bytes=0-524287")
			req.Header.Set("User-Agent", "skua/fix")
			resp, err := a.c.Do(req)
			if err != nil {
				t.Fatal("combined MP4 couldn't be fetched")
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != 200 && resp.StatusCode != 206 {
				t.Fatalf("combined MP4: HTTP %d", resp.StatusCode)
			}
			raw, err := io.ReadAll(io.LimitReader(resp.Body, 524288))
			if err != nil {
				t.Fatal("combined MP4 couldn't be read")
			}
			if !hasTrack(raw, "soun") || !hasTrack(raw, "vide") {
				t.Fatal("combined MP4 must contain both audio and video tracks in its initialization")
			}
		})
	}
}

// hdlr is a FullBox: version/flags, pre_defined, then handler_type. Check the
// declared size too so arbitrary byte matches in media payloads aren't enough.
func hasTrack(raw []byte, kind string) bool {
	for from := 0; from < len(raw); {
		i := bytes.Index(raw[from:], []byte("hdlr"))
		if i < 0 {
			return false
		}
		i += from
		if i >= 4 && i+16 <= len(raw) {
			size := int(binary.BigEndian.Uint32(raw[i-4 : i]))
			if size >= 24 && size <= len(raw)-(i-4) && string(raw[i+12:i+16]) == kind {
				return true
			}
		}
		from = i + 4
	}
	return false
}
