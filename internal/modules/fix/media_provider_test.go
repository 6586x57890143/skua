package fix

import (
	"context"
	"net/http"
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
	if err != nil || len(p.Media) != 1 || !strings.Contains(p.Media[0].URL, "/fix/media/instagram/") {
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
