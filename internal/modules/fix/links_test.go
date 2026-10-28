package fix

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/disgoorg/disgo/discord"
)

func TestSupportedPosts(t *testing.T) {
	cases := map[string]string{
		"https://www.twitter.com/Test/status/123/photo/2?s=20#x": "twitter",
		"https://x.com/i/web/status/123":                         "twitter",
		"https://bsky.app/profile/did:plc:abc/post/xyz":          "bluesky",
		"https://www.instagram.com/reel/Ab_1/":                   "instagram",
		"https://instagram.com/share/p/abc/":                     "instagram",
		"https://old.reddit.com/r/test/comments/abc/title/":      "reddit",
		"https://reddit.com/r/test/s/abc":                        "reddit",
		"https://redd.it/abc":                                    "reddit",
		"https://threads.com/@foo/post/ABC":                      "threads",
		"https://threads.net/share/abc":                          "threads",
		"https://pixiv.net/en/artworks/123":                      "pixiv",
		"https://youtube.com/post/UgABC":                         "youtube",
		"https://bilibili.com/video/BV123":                       "bilibili",
		"https://b23.tv/abc":                                     "bilibili",
		"https://pinterest.com/pin/123":                          "pinterest",
		"https://pin.it/abc":                                     "pinterest",
		"https://www.tiktok.com/t/ZPLDJqwBp/":                    "tiktok",
		"https://tiktok.com/t/ZPLDJqwBp?share=1#x":              "tiktok",
		"https://tiktok.com/@user/video/123":                     "tiktok",
		"https://vm.tiktok.com/abc":                              "tiktok",
		"https://vt.tiktok.com/abc":                              "tiktok",
		"https://tumblr.com/user/123/title":                      "tumblr",
		"https://user.tumblr.com/post/123/title":                 "tumblr",
		"https://twitch.tv/videos/123":                           "twitch",
		"https://twitch.tv/foo/clip/ABC":                         "twitch",
		"https://clips.twitch.tv/ABC":                            "twitch",
		"https://deviantart.com/foo/art/title-123":               "deviantart",
		"https://sta.sh/abc":                                     "deviantart",
	}
	for raw, platform := range cases {
		l, ok := Parse(raw)
		if !ok || l.Platform != platform {
			t.Errorf("%s: %+v %v", raw, l, ok)
		}
		if strings.ContainsAny(l.URL, "?#") {
			t.Errorf("tracking retained: %s", l.URL)
		}
	}
	for _, raw := range []string{"http://x.com/foo/status/123", "https://x.com.evil.example/foo/status/123", "https://x.com@evil.example/foo/status/123", "https://x.com:8080/foo/status/123", "https://x.com:bad/foo/status/123", "https://x.com/foo/status/abc", "https://x.com/foo", "https://x.com/a%2Fb/status/123", "https://bsky.app/profile/foo", "https://twitch.tv/directory", "https://instagram.com/profile/abc", "https://vm.tiktok.com/a/b", "https://tiktok.com/t/", "https://tiktok.com/t/a/b", "https://tiktok.com/t/a%2Fb", "https://tiktok.com.evil.example/t/ZPLDJqwBp/", "https://example.com/post/123", "https://x.com/foo/status/123\n", strings.Repeat("a", 2049)} {
		if l, ok := Parse(raw); ok {
			t.Errorf("admitted %s: %+v", raw, l)
		}
	}
}
func TestLinkSuppressionAndCap(t *testing.T) {
	link := "https://x.com/user/status/123"
	for _, text := range []string{"<" + link + ">", "`" + link + "`", "```go\n" + link + "\n```", "||" + link + "||", "``" + link + "`still code``"} {
		if got := Links(text); len(got) != 0 {
			t.Errorf("hidden %q -> %+v", text, got)
		}
	}
	text := "<https://example.com> (`code`) " + link + "?s=20. " + link + " https://x.com/user/status/124 https://x.com/user/status/125 https://x.com/user/status/126"
	if got := Links(text); len(got) != 3 || got[0].ID != "123" || got[2].ID != "125" {
		t.Fatalf("%+v", got)
	}
	if got := Links("`hidden` " + link); len(got) != 1 {
		t.Fatal(got)
	}
}
func TestMediaURLAndTextSafety(t *testing.T) {
	for _, s := range []string{"http://cdn.example/v.mp4", "https://127.0.0.1/a", "https://[::1]/a", "https://localhost/a", "https://foo.local/a", "https://foo.localhost/a", "https://user:pass@cdn.example/a", "https://cdn.example:80/a", "https://cdn.example/a\r\nx: x", "https://cdn.example/\\a", strings.Repeat("a", 2049), "https://%zz"} {
		if remoteURL(s) != "" {
			t.Errorf("admitted %q", s)
		}
	}
	if got := remoteURL("https://cdn.example/a(b).mp4?sig=AbC&foo=bar"); got != "https://cdn.example/a%28b%29.mp4?sig=AbC&foo=bar" {
		t.Fatal(got)
	}
	got := clean("Hello @everyone <@123> **bold** [x](https://example.com)\r\x00\x01", 500)
	if strings.Contains(got, "<@") || strings.Contains(got, "@everyone") || strings.Contains(got, "**bold**") {
		t.Fatal(got)
	}
	for _, s := range []string{strings.Repeat("😀", 100), strings.Repeat("*", 100), strings.Repeat("a", 100)} {
		if got := clean(s, 30); len(utf16.Encode([]rune(got))) > 30 || !strings.HasSuffix(got, "...") {
			t.Fatal(got)
		}
	}
}
func TestMultiImageGalleryKeepsPortraitsTogether(t *testing.T) {
	l, _ := Parse("https://x.com/creator/status/123")
	p := Post{Media: []Media{
		{URL: "https://cdn.example/landscape-before.jpg", Width: 1280, Height: 720},
		{URL: "https://cdn.example/portrait-one.jpg", Width: 720, Height: 1280},
		{URL: "https://cdn.example/portrait-two.jpg", Width: 720, Height: 1280},
		{URL: "https://cdn.example/landscape-after.jpg", Width: 1280, Height: 720},
	}}
	box := card(l, p).Components[0].(discord.ContainerComponent)
	var galleries []discord.MediaGalleryComponent
	for _, c := range box.Components {
		if g, ok := c.(discord.MediaGalleryComponent); ok {
			galleries = append(galleries, g)
		}
	}
	if len(galleries) != 1 {
		t.Fatalf("a multi-image post should stay in one gallery; got %d galleries", len(galleries))
	}
	if len(galleries[0].Items) != 4 {
		t.Fatalf("expected all four images in one gallery, got %d", len(galleries[0].Items))
	}
}

func TestCardIsBoundedV2WithMixedMediaAndSpoilers(t *testing.T) {
	l, _ := Parse("https://reddit.com/r/test/comments/abc/title")
	p := Post{Title: strings.Repeat("😀", 300), Text: strings.Repeat("Text **bold**\n", 500), Author: "Creator", Handle: "User", Avatar: "https://cdn.example/avatar.png", Stats: strings.Repeat("stats", 100), Context: strings.Repeat("context", 100), Provider: "fixembed", Timestamp: "2026-10-23T12:00:00Z", Sensitive: true}
	for i := range 25 {
		p.Media = append(p.Media, Media{URL: "https://cdn.example/" + strings.Repeat("a", i) + ".mp4", Alt: strings.Repeat("alt", 500)})
	}
	for range 8 {
		p.Sections = append(p.Sections, Section{Title: strings.Repeat("title", 100), Text: strings.Repeat("😀", 1000), Media: []Media{{URL: "https://cdn.example/other.png"}}})
	}
	msg := card(l, p)
	raw, _ := json.Marshal(msg)
	if msg.Flags != discord.MessageFlagIsComponentsV2 || msg.Content != "" || len(msg.Embeds) != 0 || msg.AllowedMentions == nil {
		t.Fatal(string(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	total, count, media := 0, 0, 0
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case []any:
			for _, x := range v {
				walk(x)
			}
		case map[string]any:
			if _, ok := v["type"]; ok {
				count++
			}
			if v["type"] == float64(11) {
				t.Error("sensitive avatar should be hidden")
			}
			if s, ok := v["content"].(string); ok {
				total += len(utf16.Encode([]rune(s)))
			}
			if v["type"] == float64(12) {
				items := v["items"].([]any)
				if len(items) > 10 {
					t.Fatal(len(items))
				}
				for _, item := range items {
					media++
					if item.(map[string]any)["spoiler"] != true {
						t.Error("unspoilered media")
					}
				}
			}
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(payload["components"])
	if total > 4000 || count > 40 || media != 20 {
		t.Fatalf("text=%d components=%d media=%d", total, count, media)
	}
	if !strings.Contains(string(raw), "Creator") || !strings.Contains(string(raw), l.URL) {
		t.Fatal(string(raw))
	}
	p.Sensitive = false
	p.Avatar = "https://cdn.example/avatar.png"
	p.Text = "source Text"
	p.Title = "source Text"
	p.Media = []Media{{URL: "http://evil.test/x"}, {URL: "https://cdn.example/a.png"}, {URL: "https://cdn.example/a.png"}}
	p.Sections = nil
	raw, _ = json.Marshal(card(l, p))
	if strings.Contains(string(raw), `"type":9`) || strings.Contains(string(raw), "avatar.png") || strings.Contains(string(raw), "evil.test") || !strings.Contains(string(raw), "https://cdn.example/a.png") {
		t.Fatal(string(raw))
	}
	p.Author = ""
	p.Handle = ""
	p.Avatar = ""
	p.Stats = ""
	p.Context = ""
	p.Timestamp = "<t:1:f> bad"
	p.Text = ""
	p.Title = "title"
	raw, _ = json.Marshal(card(l, p))
	if strings.Contains(string(raw), "bad") {
		t.Fatal(string(raw))
	}
}

func TestFormattedQuoteBudgetIncludesEveryNewline(t *testing.T) {
	l, _ := Parse("https://reddit.com/r/test/comments/abc/title")
	for _, text := range []string{strings.Repeat("\n", 2000), strings.Repeat("😀\n", 2000), strings.Repeat("*\n", 2000)} {
		p := Post{Text: text, Title: strings.Repeat("😀", 300), Author: strings.Repeat("😀", 100), Handle: strings.Repeat("😀", 100), Provider: "test", Stats: strings.Repeat("stats", 100), Context: strings.Repeat("context", 100)}
		for range 6 {
			p.Sections = append(p.Sections, Section{Title: strings.Repeat("😀", 100), Text: text})
		}
		msg := card(l, p)
		box := msg.Components[0].(discord.ContainerComponent)
		units := 0
		for _, c := range box.Components {
			if display, ok := c.(discord.TextDisplayComponent); ok {
				units += len(utf16.Encode([]rune(display.Content)))
			}
		}
		if units > 4000 {
			t.Fatalf("formatted text used %d UTF-16 units", units)
		}
	}
	if got := clip("😀😀😀😀", 7); len(utf16.Encode([]rune(got))) > 7 {
		t.Fatal(got)
	}
}
func TestRedditCommentQueryKeepsMeaning(t *testing.T) {
	l, ok := Parse("https://reddit.com/r/test/comments/abc/title/?utm_source=x&comment=def123#x")
	if !ok || l.URL != "https://reddit.com/r/test/comments/abc/title/?comment=def123" {
		t.Fatalf("%+v %v", l, ok)
	}
	if again, ok := Parse(l.URL); !ok || again != l {
		t.Fatal(again, ok)
	}
	l, _ = Parse("https://reddit.com/r/test/comments/abc/title/?comment=bad%26evil")
	if strings.Contains(l.URL, "?") {
		t.Fatal(l)
	}
}

func TestTikTokShortLinkReachesResolver(t *testing.T) {
	links := Links("watch https://www.tiktok.com/t/ZPLDJqwBp/?share=1")
	if len(links) != 1 || links[0].Platform != "tiktok" || links[0].URL != "https://tiktok.com/t/ZPLDJqwBp/" {
		t.Fatalf("short link was skipped or changed: %+v", links)
	}
	if parsed, ok := Parse(links[0].URL); !ok || parsed != links[0] {
		t.Fatalf("resolver boundary rejected the normalized link: %+v, %t", parsed, ok)
	}
}
