package fix

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstagramGalleryPreparationKeepsPlayableRelay(t *testing.T) {
	a := apiFor(t, func(*http.Request) (*http.Response, error) {
		return response(`{"success":true,"platform":"instagram","data":{"authorName":"creator","caption":"caption","image":"https://scontent.cdninstagram.com/poster.jpg","video":{"url":"https://fixembed.app/video/instagram?url=https%3A%2F%2Fvxinstagram.com%2Foffload%2FAb_1%2F0.mp4"}}}`), nil
	})
	a.media = newMediaRelay(a, "https://hooks.example", "a-secret-long-enough-for-tests")
	l, _ := Parse("https://instagram.com/reel/Ab_1/")
	p, err := a.fix(context.Background(), l)
	if err != nil || len(p.Media) != 1 || !strings.Contains(p.Media[0].URL, "/fix/media/instagram-post/") {
		t.Fatalf("provider mapping discarded playable relay: %+v err=%v", p.Media, err)
	}
}
func TestQuoteOriginMediaRemainsInQuote(t *testing.T) {
	a := apiFor(t, func(*http.Request) (*http.Response, error) {
		return response(`{"success":true,"platform":"twitter","data":{"caption":"main","mediaOrigin":"quote","images":["https://pbs.twimg.com/a.jpg"],"sections":[{"kind":"quote","body":"quoted","images":["https://pbs.twimg.com/a.jpg"]}]}}`), nil
	})
	l, _ := Parse("https://x.com/creator/status/123")
	p, err := a.fix(context.Background(), l)
	if err != nil || len(p.Media) != 0 || len(p.Sections) != 1 || len(p.Sections[0].Media) != 1 {
		t.Fatalf("quote media should not be duplicated in parent: %+v err=%v", p, err)
	}
}

// FixEmbed alternates video sources; a rapidcdn one (a long, expiring URL)
// must play too. The relay link names the post and re-resolves it on fetch.
func TestInstagramRelayResolvesAnySourceOnFetch(t *testing.T) {
	video := "https://fixembed.app/video/instagram?url=https%3A%2F%2Fd.rapidcdn.app%2Fv2%3Ftoken%3D" + strings.Repeat("x", 2400) + "%26dl%3D1"
	lookups, fetches := 0, 0
	a := apiFor(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/video/instagram" {
			fetches++
			if !strings.HasPrefix(video, r.URL.String()) {
				t.Errorf("proxied %s", r.URL)
			}
			return &http.Response{StatusCode: 200, ContentLength: 3, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("mp4"))}, nil
		}
		lookups++
		return response(`{"success":true,"platform":"instagram","data":{"authorName":"creator","authorHandle":"creator","caption":"c","image":"https://scontent.cdninstagram.com/p.jpg","video":{"url":"` + video + `"}}}`), nil
	})
	a.media = newMediaRelay(a, "https://hooks.example", "a-secret-long-enough-for-tests")
	l, _ := Parse("https://www.instagram.com/share/reel/Ab_1/")
	p, err := a.fix(context.Background(), l)
	if err != nil || len(p.Media) != 1 || len(p.Media[0].URL) > 300 {
		t.Fatalf("media %+v err=%v", p.Media, err)
	}
	for range 2 {
		w := httptest.NewRecorder()
		a.MediaHandler().ServeHTTP(w, httptest.NewRequest("GET", p.Media[0].URL, nil))
		if w.Code != 200 || w.Body.String() != "mp4" {
			t.Fatalf("relay %d %q", w.Code, w.Body)
		}
	}
	if lookups != 2 || fetches != 2 { // one for the card, one for both fetches
		t.Fatalf("lookups=%d fetches=%d", lookups, fetches)
	}
	// Links posted before this carry the video URL itself and keep working.
	token := base64.RawURLEncoding.EncodeToString([]byte(video[:200]))
	w := httptest.NewRecorder()
	a.MediaHandler().ServeHTTP(w, httptest.NewRequest("GET", "https://hooks.example/fix/media/instagram/"+token+"/"+a.media.signature("instagram-video", token)+".mp4", nil))
	if w.Code != 200 {
		t.Fatalf("legacy link %d", w.Code)
	}
	for _, path := range []string{"instagram-post/" + token + "/bad.mp4", "instagram-post/" + base64.RawURLEncoding.EncodeToString([]byte("https://x.com/a/status/1")) + "/x.mp4"} {
		w := httptest.NewRecorder()
		a.MediaHandler().ServeHTTP(w, httptest.NewRequest("GET", "https://hooks.example/fix/media/"+path, nil))
		if w.Code == 200 {
			t.Fatalf("%s served", path)
		}
	}
	other := base64.RawURLEncoding.EncodeToString([]byte("https://x.com/a/status/1"))
	w = httptest.NewRecorder()
	a.MediaHandler().ServeHTTP(w, httptest.NewRequest("GET", "https://hooks.example/fix/media/instagram-post/"+other+"/"+a.media.signature("instagram-post", other)+".mp4", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("non-instagram post %d", w.Code)
	}
}
