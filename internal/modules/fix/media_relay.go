package fix

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
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
	cachedID     string
	cached       []byte
	until        time.Time
}

func newMediaRelay(a *API, base, secret string) *mediaRelay {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(secret) < 16 {
		return nil
	}
	return &mediaRelay{api: a, base: strings.TrimRight(base, "/"), secret: secret, slot: make(chan struct{}, 1), serving: make(chan struct{}, 2)}
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
	gallery := len(p) == 3 && p[0] == "gallery" && len(p[1]) <= 1800 && hmac.Equal([]byte(p[2]), []byte(m.signature("gallery", p[1])+".jpg"))
	clip := len(p) == 3 && relayID(p[0], p[1]) && hmac.Equal([]byte(p[2]), []byte(m.signature(p[0], p[1])+".mp4"))
	if !strings.HasPrefix(r.URL.Path, "/fix/media/") || (!gallery && !clip) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	select {
	case m.serving <- struct{}{}:
	case <-ctx.Done():
		http.Error(w, "media busy", http.StatusServiceUnavailable)
		return
	}
	defer func() { <-m.serving }()
	key := p[0] + "/" + p[1]
	prepare := func(ctx context.Context) ([]byte, error) { return m.prepare(ctx, p[1], nil) }
	contentType, filename, maxAge := "video/mp4", "clip.mp4", "public, max-age=900"
	if p[0] == "reddit" {
		prepare = func(ctx context.Context) ([]byte, error) { return m.api.redditMux(ctx, p[1]) }
	}
	if gallery {
		key = "gallery/" + p[1]
		prepare = func(ctx context.Context) ([]byte, error) { return m.prepareGallery(ctx, p[1]) }
		contentType, filename, maxAge = "image/jpeg", "gallery.jpg", "public, max-age=86400"
	}
	raw, err := m.ready(ctx, key, prepare)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusBadGateway)
		return
	}
	_ = controller.SetWriteDeadline(time.Now().Add(20 * time.Second))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", maxAge)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filename, time.Time{}, bytes.NewReader(raw))
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
