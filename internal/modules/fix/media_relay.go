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
func (m *mediaRelay) signature(id string) string {
	h := hmac.New(sha256.New, []byte(m.secret))
	_, _ = h.Write([]byte("skua/fix/kick/" + id))
	return hex.EncodeToString(h.Sum(nil))
}
func (m *mediaRelay) link(id string) string {
	return m.base + "/fix/media/kick/" + id + "/" + m.signature(id) + ".mp4"
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
	p := strings.Split(strings.TrimPrefix(r.URL.Path, "/fix/media/kick/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/fix/media/kick/") || len(p) != 2 || !kickClipID.MatchString(p[0]) || !hmac.Equal([]byte(p[1]), []byte(m.signature(p[0])+".mp4")) {
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
	raw, err := m.ready(ctx, p[0], nil)
	if err != nil {
		http.Error(w, "media unavailable", http.StatusBadGateway)
		return
	}
	_ = controller.SetWriteDeadline(time.Now().Add(20 * time.Second))
	// ServeContent handles HEAD and byte ranges with the same encoded MP4.
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "public, max-age=900")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "clip.mp4", time.Time{}, bytes.NewReader(raw))
}
func (m *mediaRelay) ready(ctx context.Context, id string, d *kickData) ([]byte, error) {
	select {
	case m.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-m.slot }()
	if m.cachedID == id && time.Now().Before(m.until) {
		return m.cached, nil
	}
	started := time.Now()
	raw, err := m.prepare(ctx, id, d)
	obs.Default.Add("fix media", obs.HTTP, time.Since(started))
	if err != nil {
		return nil, err
	}
	m.cachedID, m.cached, m.until = id, raw, time.Now().Add(15*time.Minute)
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
