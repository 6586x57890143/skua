package fix

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	tscodecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	"github.com/disgoorg/disgo/discord"
)

const testClip = "clip_test"
const testCDN = "https://clips.kick.com/clips/ab/clip_test/"
const testPlaylist = "#EXTM3U\n#EXTINF:2,\n#EXT-X-BYTERANGE:20@0\na.ts\n#EXTINF:2,\n#EXT-X-BYTERANGE:20\na.ts\n#EXT-X-ENDLIST\n"

func kickFixture(media string) string {
	raw, _ := json.Marshal(map[string]any{"clip": map[string]any{"id": testClip, "privacy": "public", "title": "Clip title", "clip_url": media, "channel": map[string]string{"username": "Streamer", "slug": "streamer"}, "is_mature": true, "views": 1234, "likes": 10}})
	return string(raw)
}
func mediaAPI(t *testing.T, fn transport) *API {
	a := apiFor(t, fn)
	a.media = newMediaRelay(a, "https://hooks.example", "test-secret-at-least-sixteen")
	return a
}
func TestKickLinksAndMetadata(t *testing.T) {
	for _, raw := range []string{"https://kick.com/streamer?clip=clip_test&utm_source=x", "https://www.kick.com/streamer/clips/clip_test/"} {
		l, ok := Parse(raw)
		if !ok || l.ID != testClip || l.URL != "https://kick.com/streamer/clips/clip_test" {
			t.Fatalf("%+v %v", l, ok)
		}
		for _, ext := range []string{"video.mp4", "playlist.m3u8"} {
			a := mediaAPI(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "clips.kick.com" {
					if strings.HasSuffix(r.URL.Path, ".m3u8") {
						return response("#EXTM3U\n#EXTINF:2,\na.ts\n#EXT-X-ENDLIST\n"), nil
					}
					return response(string(tinyClip(t))), nil
				}
				if r.URL.String() != "https://kick.com/api/v2/clips/clip_test" {
					t.Fatal(r.URL)
				}
				return response(kickFixture(testCDN + ext)), nil
			})
			p, e := a.Resolve(context.Background(), l)
			if e != nil || !p.Sensitive || p.Author != "Streamer" || len(p.Media) != 1 {
				t.Fatalf("%+v %v", p, e)
			}
			if ext == "playlist.m3u8" && !strings.HasPrefix(p.Media[0].URL, "https://hooks.example/fix/media/kick/") {
				t.Fatal(p)
			}
			if ext == "video.mp4" && p.Media[0].URL != testCDN+ext {
				t.Fatal(p)
			}
		}
	}
	for _, raw := range []string{"https://kick.com/streamer", "https://kick.com/streamer?clip=bad", "https://kick.com/a/b/clips/clip_test", "https://kick.com/a/clips/clip_a?clip=clip_b", "https://kick.com/a/clips/clip_test/other"} {
		l, ok := Parse(raw)
		if strings.Contains(raw, "clip_a?clip") {
			if !ok || l.ID != "clip_a" {
				t.Fatal(l)
			}
			continue
		}
		if ok {
			t.Fatal(raw)
		}
	}
	for _, body := range []string{`{}`, `{"clip":{"id":"clip_other","privacy":"public"}}`, `{"clip":{"id":"clip_test","privacy":"private"}}`, kickFixture("https://evil.example/video.mp4"), kickFixture(testCDN + "playlist.m3u8")} {
		a := apiFor(t, func(*http.Request) (*http.Response, error) { return response(body), nil })
		l, _ := Parse("https://kick.com/a/clips/clip_test")
		if _, e := a.Resolve(context.Background(), l); e == nil {
			t.Fatal(body)
		}
	}
	for _, raw := range []string{"http://clips.kick.com/clips/a.mp4", "https://evil.example/clips/a.mp4", "https://clips.kick.com/other/a.mp4", "https://clips.kick.com/clips/a.m3u8#x"} {
		if kickMediaURL(raw) {
			t.Fatal(raw)
		}
	}
	a := mediaAPI(t, func(*http.Request) (*http.Response, error) { return nil, io.EOF })
	l, _ := Parse("https://kick.com/a/clips/clip_test")
	if _, e := a.Resolve(context.Background(), l); e == nil {
		t.Fatal("network error accepted")
	}
}

// The synthetic TS uses standard H.264 SPS/PPS test parameters (mediacommon,
// MIT). Payload bytes are deliberately tiny; live canaries verify real media.
func tinyClip(t *testing.T) []byte { return tinyClipAt(t, 0) }
func tinyClipAt(t *testing.T, origin int64) []byte {
	t.Helper()
	var b bytes.Buffer
	v := &mpegts.Track{Codec: &tscodecs.H264{}}
	a := &mpegts.Track{Codec: &tscodecs.MPEG4Audio{Config: mpeg4audio.AudioSpecificConfig{Type: mpeg4audio.ObjectTypeAACLC, SampleRate: 48000, ChannelConfig: 2}}}
	w := &mpegts.Writer{W: &b, Tracks: []*mpegts.Track{v, a}}
	if e := w.Initialize(); e != nil {
		t.Fatal(e)
	}
	sps := []byte{0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02, 0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9, 0x20}
	for i := range 5 {
		au := [][]byte{{0x65, 1, 2, 3}}
		if i == 0 {
			au = append([][]byte{sps, {0x68, 0xce, 0x06, 0xe2}}, au...)
		}
		if e := w.WriteH264(v, origin+int64(i)*3000, origin+int64(i)*3000, au); e != nil {
			t.Fatal(e)
		}
		if e := w.WriteMPEG4Audio(a, origin+int64(i)*3000, [][]byte{{0x21, 0x10, 0x04, 0x60}}); e != nil {
			t.Fatal(e)
		}
	}
	return b.Bytes()
}
func TestRemuxKeepsAudioVideo(t *testing.T) {
	raw, e := remuxClip(context.Background(), bytes.NewReader(tinyClip(t)))
	if e != nil || !hasTrack(raw, "soun") || !hasTrack(raw, "vide") {
		t.Fatalf("bytes=%d err=%v", len(raw), e)
	}
	if _, e := remuxClip(context.Background(), strings.NewReader("bad")); e == nil {
		t.Fatal("bad stream accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := remuxClip(ctx, bytes.NewReader(tinyClip(t))); e == nil {
		t.Fatal("canceled stream accepted")
	}
}
func TestKickRelayRangesAndCache(t *testing.T) {
	ts := tinyClip(t)
	calls := 0
	a := mediaAPI(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "kick.com" {
			return response(kickFixture(testCDN + "playlist.m3u8")), nil
		}
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			return response("#EXTM3U\n#EXTINF:2,\na.ts\n#EXT-X-ENDLIST\n"), nil
		}
		return response(string(ts)), nil
	})
	for i, method := range []string{"GET", "HEAD", "GET"} {
		req := httptest.NewRequest(method, a.media.link("kick", testClip), nil)
		req.Header.Set("Range", "bytes=0-31")
		rec := httptest.NewRecorder()
		a.MediaHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Type") != "video/mp4" || rec.Header().Get("Accept-Ranges") != "bytes" {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if method == "GET" && rec.Body.Len() != 32 {
			t.Fatal(rec.Body.Len())
		}
		if i > 0 && calls != 3 {
			t.Fatal("cache missed", calls)
		}
	}
	for _, raw := range []string{"https://hooks.example/fix/media/kick/clip_test/bad.mp4", "https://hooks.example/other"} {
		r := httptest.NewRecorder()
		a.MediaHandler().ServeHTTP(r, httptest.NewRequest("GET", raw, nil))
		if r.Code != 404 {
			t.Fatal(r.Code)
		}
	}
	r := httptest.NewRecorder()
	a.MediaHandler().ServeHTTP(r, httptest.NewRequest("POST", a.media.link("kick", testClip), nil))
	if r.Code != 405 {
		t.Fatal(r.Code)
	}
	a.media.until = time.Time{}
	a.c.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, io.EOF })
	r = httptest.NewRecorder()
	a.MediaHandler().ServeHTTP(r, httptest.NewRequest("GET", a.media.link("kick", testClip), nil))
	if r.Code != 502 {
		t.Fatal(r.Code)
	}
	for _, base := range []string{"http://hooks.example", "https://user:pass@hooks.example", "https://hooks.example/?x=y", "https://hooks.example/#x", "::bad"} {
		if newMediaRelay(a, base, "long-enough-test-secret") != nil {
			t.Fatal(base)
		}
	}
	if newMediaRelay(a, "https://hooks.example", "short") != nil {
		t.Fatal("short secret")
	}
	a.media = nil
	r = httptest.NewRecorder()
	a.MediaHandler().ServeHTTP(r, httptest.NewRequest("GET", "https://hooks.example/fix/media/", nil))
	if r.Code != 404 || a.ServesMedia() {
		t.Fatal(r.Code)
	}
}
func TestLiveKickClip(t *testing.T) {
	if os.Getenv("SKUA_FIX_LIVE") != "1" {
		t.Skip("set SKUA_FIX_LIVE=1")
	}
	cfg := FromEnv()
	cfg.MediaURL = "https://hooks.example"
	cfg.MediaSecret = "live-canary-not-a-production-secret"
	a, e := NewAPI(cfg)
	if e != nil {
		t.Fatal(e)
	}
	l, _ := Parse("https://www.reddit.com/r/LivestreamFail/comments/1xcsm77/asmongold_reacts_to_hasan_saying_it_well_well_well/")
	start := time.Now()
	p, e := a.Resolve(context.Background(), l)
	t.Logf("metadata %s", time.Since(start))
	if e != nil || p.Incomplete || len(p.Media) == 0 {
		t.Fatalf("%+v %v", p, e)
	}
	u, e := url.Parse(p.Media[0].URL)
	if e != nil || u.Host != "hooks.example" {
		t.Fatal("missing MP4 relay", p.Media[0].URL)
	}
	start = time.Now()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", p.Media[0].URL, nil)
	req.Header.Set("Range", "bytes=0-524287")
	a.MediaHandler().ServeHTTP(rec, req)
	t.Logf("prepare %s, MP4 %d bytes", time.Since(start), len(a.media.cached))
	if sample := os.Getenv("SKUA_FIX_MEDIA_SAMPLE"); sample != "" {
		if err := os.WriteFile(sample, a.media.cached, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if rec.Code != 206 || !hasTrack(rec.Body.Bytes(), "soun") || !hasTrack(rec.Body.Bytes(), "vide") {
		t.Fatalf("playback code=%d body=%s", rec.Code, rec.Body.String()[:min(100, rec.Body.Len())])
	}
}

func TestTimestampWrapAndFailedPreparation(t *testing.T) {
	raw, e := remuxClip(context.Background(), bytes.NewReader(tinyClipAt(t, (1<<33)-6000)))
	if e != nil || !hasTrack(raw, "soun") || !hasTrack(raw, "vide") {
		t.Fatalf("wrap: %v", e)
	}
	if timestampDelta(100, (1<<33)-100) != 200 || timestampDelta((1<<33)-100, 100) != -200 {
		t.Fatal("wrap delta")
	}
	a := mediaAPI(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "fixembed.app" {
			return response(`{"success":true,"platform":"reddit","data":{"title":"parent","image":"https://cdn.example/thumb.jpg","sections":[{"kind":"link-card","url":"https://kick.com/streamer/clips/clip_test"}]}}`), nil
		}
		if r.URL.Host == "kick.com" {
			return response(kickFixture(testCDN + "playlist.m3u8")), nil
		}
		return response("not a playlist"), nil
	})
	l, _ := Parse("https://reddit.com/r/test/s/abc")
	p, e := a.Resolve(context.Background(), l)
	if e != nil || !p.Incomplete {
		t.Fatalf("failed media must retain native: %+v %v", p, e)
	}
}

func TestClipPlaylistBoundaries(t *testing.T) {
	segments, e := clipSegments(testCDN+"playlist.m3u8", []byte(testPlaylist))
	if e != nil || len(segments) != 2 || segments[1].Offset != 20 {
		t.Fatalf("%+v %v", segments, e)
	}
	for _, bad := range []string{
		"not playlist", strings.ReplaceAll(testPlaylist, "#EXT-X-ENDLIST", ""), strings.ReplaceAll(testPlaylist, "a.ts", "https://evil.example/a.ts"), strings.ReplaceAll(testPlaylist, "a.ts", "../other/a.ts"), strings.ReplaceAll(testPlaylist, "a.ts", "nested/a.ts"), strings.ReplaceAll(testPlaylist, "a.ts", "a.ts?x=y"), strings.ReplaceAll(testPlaylist, "a.ts", "a.mp4"),
		strings.Replace(testPlaylist, "#EXTINF:2,", "#EXT-X-KEY:METHOD=AES-128\n#EXTINF:2,", 1), strings.Replace(testPlaylist, "#EXTINF:2,", "#EXT-X-MAP:URI=init.mp4\n#EXTINF:2,", 1), strings.Replace(testPlaylist, "#EXTINF:2,", "#EXT-X-DISCONTINUITY\n#EXTINF:2,", 1),
		strings.ReplaceAll(testPlaylist, "#EXTINF:2,", "#EXTINF:-1,"), strings.ReplaceAll(testPlaylist, "#EXTINF:2,", "#EXTINF:bad,"), strings.ReplaceAll(testPlaylist, "20@0", "-20@0"), strings.ReplaceAll(testPlaylist, "20@0", "20@-1"), strings.ReplaceAll(testPlaylist, "20@0", "20@bad"), strings.ReplaceAll(testPlaylist, "20@0", "20@0@0"), strings.ReplaceAll(testPlaylist, "20@0", "20"), strings.Replace(testPlaylist, "20\na.ts", "20\nb.ts", 1),
		"#EXTM3U\n#EXTINF:1,\n#EXT-X-ENDLIST", strings.Repeat("x", (64<<10)+1),
	} {
		if _, e := clipSegments(testCDN+"playlist.m3u8", []byte(bad)); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, e := clipSegments("bad", []byte(testPlaylist)); e == nil {
		t.Fatal("bad base")
	}
}

func TestClipByteRangeValidation(t *testing.T) {
	for _, tc := range []struct {
		status             int
		contentRange, body string
		want               string
	}{
		{200, "", "0123456789", "2345"}, {206, "bytes 2-5/10", "2345", "2345"}, {206, "bytes 0-3/10", "0123", ""}, {206, "bytes 2-5/10", "234", ""}, {404, "", "gone", ""}, {200, "", "01", ""}, {200, "", strings.Repeat("x", 101), ""},
	} {
		a := apiFor(t, func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Range") != "bytes=2-5" {
				t.Fatal(r.Header)
			}
			v := response(tc.body)
			v.StatusCode = tc.status
			v.Header.Set("Content-Range", tc.contentRange)
			return v, nil
		})
		got, e := a.clipBytes(context.Background(), testCDN+"a.ts", 2, 4, 100)
		if tc.want == "" && e == nil || tc.want != "" && (e != nil || string(got) != tc.want) {
			t.Fatalf("%+v got=%q err=%v", tc, got, e)
		}
	}
	a := apiFor(t, func(*http.Request) (*http.Response, error) { return nil, io.EOF })
	if _, e := a.clipBytes(context.Background(), testCDN+"a.ts", 0, 0, 10); e == nil {
		t.Fatal("network")
	}
	if _, e := a.clipBytes(context.Background(), "https://%zz", 0, 0, 10); e == nil {
		t.Fatal("request")
	}
	if _, e := a.clipTransport(context.Background(), testCDN+"playlist.m3u8"); e == nil {
		t.Fatal("transport")
	}
}

func TestSingleLineFooter(t *testing.T) {
	for _, p := range []Post{{Provider: "fixembed", Stats: "10 likes · 3 replies", Timestamp: "2026-10-22T10:13:00Z"}, {Provider: "fixembed"}, {Provider: "kick", Stats: "1 likes"}} {
		box := card(Link{Platform: "kick", URL: "https://kick.com/a/clips/clip_test"}, p).Components[0].(discord.ContainerComponent)
		footer := box.Components[len(box.Components)-1].(discord.TextDisplayComponent).Content
		if strings.ContainsAny(footer, "\n\r") || !strings.HasPrefix(footer, "-# ") || !strings.Contains(footer, p.Provider) {
			t.Fatal(footer)
		}
	}
}
