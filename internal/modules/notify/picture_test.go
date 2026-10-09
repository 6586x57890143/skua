package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestImageAnswersOnlyForAnImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.webp":
			w.Header().Set("Content-Type", "image/webp")
		case "/page":
			w.Header().Set("Content-Type", "text/html")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	for u, want := range map[string]bool{
		srv.URL + "/ok.webp": true,
		srv.URL + "/page":    false,
		srv.URL + "/missing": false,
		"http://[::1]:0/x":   false,
		"::nope":             false,
	} {
		if got := answers(ctx, srv.Client(), u); got != want {
			t.Errorf("%s: %v", u, got)
		}
	}
}

func TestPictureFallsBackOrGoesWithout(t *testing.T) {
	m := module(t, newFake())
	var mu sync.Mutex
	var asked []string
	have := map[string]bool{"https://i/vi/a/hqdefault_live.jpg": true, "https://k/t.webp": true}
	m.looks = func(_ context.Context, u string) bool {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, u)
		return have[u]
	}
	ctx := context.Background()
	for in, want := range map[string]string{
		"https://i/vi/a/maxresdefault_live.jpg": "https://i/vi/a/hqdefault_live.jpg",
		"https://k/t.webp":                      "https://k/t.webp",
		"https://k/not-yet.webp":                "",
		"http://k/t.webp":                       "",
		"":                                      "",
	} {
		if got := m.picture(ctx, in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	for _, u := range asked {
		if !strings.HasPrefix(u, "https://") {
			t.Errorf("asked after a plain link: %s", u)
		}
	}
}

func TestBust(t *testing.T) {
	at := time.Unix(100, 0)
	for in, want := range map[string]string{"https://a/b.jpg": "https://a/b.jpg?t=100", "https://a/b.jpg?s=1": "https://a/b.jpg?s=1&t=100", "": ""} {
		if got := bust(in, at); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestCount(t *testing.T) {
	for n, want := range map[int]string{0: "0", 950: "950", 1000: "1k", 1234: "1.2k", 9999: "9.9k", 12345: "12k", 999_999: "999k", 1_250_000: "1.2m", 3_000_000: "3m"} {
		if got := count(n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}
