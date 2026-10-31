package fix

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/6586x57890143/skua/internal/obs"
)

// One preparation at a time and one short-lived cached clip bound memory and
// merge concurrent Discord range requests. Only bot-issued signed IDs work.
type mediaRelay struct {
	api          *API
	base, secret string
	slot         chan struct{}
	serving      chan struct{}
	streaming    chan struct{}
	cachedID     string
	cached       []byte
	until        time.Time
	mu           sync.Mutex
	sources      map[string]instagramSource
}

type instagramSource struct {
	url   string
	until time.Time
}

func newMediaRelay(a *API, base, secret string) *mediaRelay {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(secret) < 16 {
		return nil
	}
	return &mediaRelay{api: a, base: strings.TrimRight(base, "/"), secret: secret, slot: make(chan struct{}, 1), serving: make(chan struct{}, 2), streaming: make(chan struct{}, 2), sources: map[string]instagramSource{}}
}

// kind is "kick" or "reddit"; each signs its IDs in its own namespace.
func (m *mediaRelay) signature(kind, id string) string {
	h := hmac.New(sha256.New, []byte(m.secret))
	_, _ = h.Write([]byte("skua/fix/" + kind + "/" + id))
	return hex.EncodeToString(h.Sum(nil))
}
func relayID(kind, id string) bool {
	return (kind == "kick" && kickClipID.MatchString(id)) || (kind == "reddit" && redditVideoID.MatchString(id))
}
func (m *mediaRelay) link(kind, id string) string {
	return m.base + "/fix/media/" + kind + "/" + id + "/" + m.signature(kind, id) + ".mp4"
}

// The link names the post, not its video: FixEmbed alternates sources
// (vxinstagram, snapsave's rapidcdn) whose URLs run past 2 KB and expire, so
// the relay asks FixEmbed again when Discord fetches it.
func (m *mediaRelay) instagramPostLink(post string) string {
	token := base64.RawURLEncoding.EncodeToString([]byte(post))
	return m.base + "/fix/media/instagram-post/" + token + "/" + m.signature("instagram-post", token) + ".mp4"
}

// ponytail: the map is cleared whole at 64 posts; an LRU if that ever churns.
func (m *mediaRelay) instagramVideo(ctx context.Context, l Link) string {
	m.mu.Lock()
	s, ok := m.sources[l.URL]
	m.mu.Unlock()
	if ok && time.Now().Before(s.until) {
		return s.url
	}
	d, err := m.api.fixMetadata(ctx, l)
	if err != nil || d.Video == nil || !trustedInstagramVideoURL(d.Video.URL, m.api.cfg.FixEmbed) {
		return ""
	}
	m.mu.Lock()
	if len(m.sources) >= 64 {
		clear(m.sources)
	}
	m.sources[l.URL] = instagramSource{d.Video.URL, time.Now().Add(10 * time.Minute)}
	m.mu.Unlock()
	return d.Video.URL
}

// skua only ever connects to FixEmbed's own video proxy; FixEmbed fetches the
// source, so which https host it picked doesn't widen what skua reaches.
func trustedInstagramVideoURL(raw, endpoint string) bool {
	video, err := url.Parse(raw)
	if err != nil || video.Scheme != "https" || video.User != nil || video.RawFragment != "" || video.RawQuery == "" {
		return false
	}
	base, err := url.Parse(endpoint)
	if err != nil || base.Scheme != "https" || video.Host != base.Host || video.Path != "/video/instagram" {
		return false
	}
	query := video.Query()
	if len(query) != 1 || len(query["url"]) != 1 {
		return false
	}
	source, err := url.Parse(query.Get("url"))
	return err == nil && source.Scheme == "https" && source.Host != "" && source.User == nil && source.Fragment == ""
}

// MediaHandler shares the already-configured public listener through main;
// no fix module imports or alters another module's handlers.
func (a *API) MediaHandler() http.Handler {
	if a.media == nil {
		return http.NotFoundHandler()
	}
	return a.media
}
func (a *API) ServesMedia() bool { return a.media != nil }
func (m *mediaRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(20 * time.Second))
	p := strings.Split(strings.TrimPrefix(r.URL.Path, "/fix/media/"), "/")
	gallery := len(p) == 3 && p[0] == "gallery" && len(p[1]) <= 1800 && hmac.Equal([]byte(p[2]), []byte(m.signature("gallery", p[1])+".png"))
	clip := len(p) == 3 && relayID(p[0], p[1]) && hmac.Equal([]byte(p[2]), []byte(m.signature(p[0], p[1])+".mp4"))
	// "instagram" carries a video URL: links posted before instagram-post.
	instagramPost := len(p) == 3 && p[0] == "instagram-post" && len(p[1]) <= 512 && hmac.Equal([]byte(p[2]), []byte(m.signature("instagram-post", p[1])+".mp4"))
	instagramVideo := instagramPost || len(p) == 3 && p[0] == "instagram" && len(p[1]) <= 1800 && hmac.Equal([]byte(p[2]), []byte(m.signature("instagram-video", p[1])+".mp4"))
	if !strings.HasPrefix(r.URL.Path, "/fix/media/") || (!gallery && !clip && !instagramVideo) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// A clip is tens of MB: 20s to fetch, remux and write it cut Discord off
	// mid-body (cloudflared logged the EOFs).
	timeout := 20 * time.Second
	if instagramVideo || clip {
		timeout = 2 * time.Minute
		_ = controller.SetWriteDeadline(time.Now().Add(timeout))
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	// Instagram streams through and never touches the clip cache, so it gets
	// its own slots: two slow Kick clips can't starve it.
	serving := m.serving
	if instagramVideo {
		serving = m.streaming
	}
	select {
	case serving <- struct{}{}:
	case <-ctx.Done():
		http.Error(w, "media busy", http.StatusServiceUnavailable)
		return
	}
	defer func() { <-serving }()
	if instagramVideo {
		m.proxyInstagramVideo(ctx, w, r, p[1], instagramPost)
		return
	}
	key := p[0] + "/" + p[1]
	prepare := func(ctx context.Context) ([]byte, error) { return m.prepare(ctx, p[1], nil) }
	contentType, filename, maxAge := "video/mp4", "clip.mp4", "public, max-age=900"
	if p[0] == "reddit" {
		prepare = func(ctx context.Context) ([]byte, error) { return m.api.redditMux(ctx, p[1]) }
	}
	if gallery {
		key = "gallery/" + p[1]
		prepare = func(ctx context.Context) ([]byte, error) { return m.prepareGallery(ctx, p[1]) }
		contentType, filename, maxAge = "image/png", "gallery.png", "public, max-age=86400"
	}
	raw, err := m.ready(ctx, key, prepare)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusBadGateway)
		return
	}
	_ = controller.SetWriteDeadline(time.Now().Add(timeout))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", maxAge)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filename, time.Time{}, bytes.NewReader(raw))
}
func (m *mediaRelay) proxyInstagramVideo(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, post bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusBadGateway)
		return
	}
	raw := string(decoded)
	if post {
		l, ok := Parse(raw)
		raw = ""
		if ok && l.Platform == "instagram" {
			raw = m.instagramVideo(ctx, l)
		}
	}
	if !trustedInstagramVideoURL(raw, m.api.cfg.FixEmbed) {
		http.Error(w, "media unavailable", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, raw, nil)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusBadGateway)
		return
	}
	for _, header := range []string{"Range", "If-Range"} {
		if value := r.Header.Get(header); len(value) > 0 && len(value) <= 256 {
			req.Header.Set(header, value)
		}
	}
	client := &http.Client{
		Transport: m.api.c.Transport,
		Timeout:   2 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	validStatus := resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent
	if !validStatus || resp.ContentLength > mediaLimit {
		http.Error(w, "media unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline; filename=video.mp4")
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if value := resp.Header.Get("Content-Range"); value != "" {
		w.Header().Set("Content-Range", value)
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, io.LimitReader(resp.Body, mediaLimit))
}

func (m *mediaRelay) ready(ctx context.Context, key string, prepare func(context.Context) ([]byte, error)) ([]byte, error) {
	select {
	case m.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-m.slot }()
	if m.cachedID == key && time.Now().Before(m.until) {
		return m.cached, nil
	}
	started := time.Now()
	raw, err := prepare(ctx)
	obs.Default.Add("fix media", obs.HTTP, time.Since(started))
	if err != nil {
		return nil, err
	}
	m.cachedID, m.cached, m.until = key, raw, time.Now().Add(15*time.Minute)
	return raw, nil
}
func (m *mediaRelay) prepare(ctx context.Context, id string, d *kickData) ([]byte, error) {
	if d == nil {
		var err error
		d, err = m.api.kickMetadata(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	for _, raw := range []string{d.PlaybackURL, d.ClipURL, d.VideoURL} {
		if !kickMediaURL(raw) {
			continue
		}
		u, _ := url.Parse(raw)
		if strings.HasSuffix(u.Path, ".mp4") {
			return m.api.clipBytes(ctx, raw, 0, 0, mediaLimit)
		}
		src, e := m.api.clipTransport(ctx, raw)
		if e != nil {
			return nil, e
		}
		return remuxClip(ctx, src)
	}
	return nil, errMetadata
}
