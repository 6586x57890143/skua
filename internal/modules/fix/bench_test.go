package fix

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrdinaryMessageDoesNotAllocate(t *testing.T) {
	text := "a normal message with no link " + strings.Repeat("hello ", 300)
	if n := testing.AllocsPerRun(1000, func() {
		if len(Links(text)) != 0 {
			panic("unexpected URL")
		}
	}); n != 0 {
		t.Fatalf("ordinary message allocated %.0f times", n)
	}
}
func BenchmarkLinksOrdinary(b *testing.B) {
	text := "a normal message with no link " + strings.Repeat("hello ", 300)
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		_ = Links(text)
	}
}
func BenchmarkLinksThreePosts(b *testing.B) {
	text := "a post https://x.com/user/status/123 and a video https://www.reddit.com/r/test/comments/abc/title/ and an image https://bsky.app/profile/bsky.app/post/123"
	b.ReportAllocs()
	for b.Loop() {
		_ = Links(text)
	}
}
func BenchmarkParseReddit(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = Parse("https://www.reddit.com/r/test/comments/abc/title/?utm_source=share")
	}
}
func BenchmarkRenderMixedMedia(b *testing.B) {
	l, _ := Parse("https://reddit.com/r/test/comments/abc/title")
	p := Post{Author: "Creator", Title: "A post", Text: "Post text", Provider: "fixembed", Media: []Media{{URL: "https://packaged-media.redd.it/abc/video.mp4?sig=Signed"}, {URL: "https://cdn.example/image.jpg"}}}
	b.ReportAllocs()
	for b.Loop() {
		_ = card(l, p)
	}
}
func BenchmarkDecodeFxMetadata(b *testing.B) {
	raw := []byte(fxFixture)
	b.ReportAllocs()
	for b.Loop() {
		var body struct {
			Code   int
			Status fxStatus
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderQuotedPost(b *testing.B) {
	l, _ := Parse("https://x.com/author/status/123")
	p := Post{Author: "Author", Handle: "author", AuthorURL: "https://x.com/author", Text: "A post with a source https://example.com/article", Stats: "7215 likes · 737 reposts · 135 replies · 31000 views", Timestamp: "2026-10-22T10:13:00Z", Provider: "fxembed", Sections: []Section{{Kind: "quote", Author: "Another", Handle: "another", URL: "https://x.com/another/status/124", AuthorURL: "https://x.com/another", Text: "Quoted text and https://example.com/context", Media: []Media{{URL: "https://video.twimg.com/example.mp4"}}}}}
	b.ReportAllocs()
	for b.Loop() {
		_ = card(l, p)
	}
}
