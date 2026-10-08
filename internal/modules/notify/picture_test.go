package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/disgoorg/snowflake/v2"
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
		if got := image(ctx, srv.Client(), u); got != want {
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

// kick makes a stream's preview a while after it starts: the card goes up
// without one, and gets it, unpinged, once kick has it.
func TestALiveCardGetsItsPreviewLate(t *testing.T) {
	src := newFake()
	m := module(t, src)
	m.follows = []follow{{guild: 1, channel: 10, platform: "fake", account: "bird", name: "Bird", role: 5}}
	ready := false
	m.looks = func(context.Context, string) bool { return ready }
	p := &poster{}
	ctx := context.Background()
	stream := item{ID: "live:1", Title: "on air", URL: "https://p/live1", Author: "Bird", Image: "https://k/t.webp"}

	src.show("bird")
	m.poll(ctx, p, "fake", src)
	src.show("bird", stream)
	m.poll(ctx, p, "fake", src)
	if b, _ := json.Marshal(p.sent[0].msg); strings.Contains(string(b), "https://k/t.webp") {
		t.Fatalf("a preview that isn't there yet went out: %s", b)
	}
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 0 {
		t.Fatal("an edit before the preview exists")
	}
	ready = true
	m.poll(ctx, p, "fake", src)
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 1 || p.edits[0].message != 1 {
		t.Fatalf("one edit, of the card: %+v", p.edits)
	}
	b, _ := json.Marshal(p.edits[0].msg)
	for _, want := range []string{"https://k/t.webp", "live on fake", "on air", `"parse":[]`, `"roles":null`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the filled card has no %q: %s", want, b)
		}
	}

	// A card with its preview from the start is never edited for it.
	src.show("bird")
	m.poll(ctx, p, "fake", src)
	src.show("bird", item{ID: "live:2", URL: "https://p/live2", Image: "https://k/2.webp"})
	m.poll(ctx, p, "fake", src)
	edits := len(p.edits)
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != edits {
		t.Fatal("a card that had its preview was edited again")
	}

	// A server with notify off gets no edit, and a refused edit is
	// logged: the preview is then taken as given either way.
	ready = false
	src.show("bird")
	m.poll(ctx, p, "fake", src) // live:2 ends: its card becomes the vod
	edits = len(p.edits)
	src.show("bird", item{ID: "live:3", URL: "https://p/live3", Image: "https://k/3.webp"})
	m.poll(ctx, p, "fake", src)
	ready = true
	m.on = func(snowflake.ID) bool { return false }
	m.poll(ctx, p, "fake", src)
	m.on = func(snowflake.ID) bool { return true }
	p.editErr = errors.New("gone")
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != edits {
		t.Fatalf("edits: %+v", p.edits[edits:])
	}
}
