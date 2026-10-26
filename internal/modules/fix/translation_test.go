package fix

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/disgoorg/disgo/discord"
)

func TestFxEnglishTranslationKeepsMediaAndSource(t *testing.T) {
	for _, raw := range []string{"https://x.com/author/status/123", "https://bsky.app/profile/author.example/post/123"} {
		t.Run(raw, func(t *testing.T) {
			l, _ := Parse(raw)
			calls := 0
			a := apiFor(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Query().Get("lang") != "en" || strings.HasSuffix(r.URL.Path, "/en") {
					t.Fatalf("API v2 translation route: %s", r.URL)
				}
				return response(`{"code":200,"status":{"id":"123","text":"Bonjour tout le monde","translation":{"text":"Hello everyone","source_lang":"fr","target_lang":"en"},"author":{"name":"Author"},"media":{"all":[{"type":"photo","url":"https://cdn.example/photo.jpg"}]},"quote":{"id":"124","text":"こんにちは","url":"https://x.com/other/status/124","translation":{"text":"Good afternoon","source_lang":"ja","target_lang":"en"}}}}`), nil
			})
			p, err := a.Resolve(context.Background(), l)
			if err != nil || calls != 1 || p.Text != "Hello everyone" || p.TranslatedFrom != "fr" || len(p.Media) != 1 || p.Media[0].URL != "https://cdn.example/photo.jpg" || p.Sections[0].Text != "Good afternoon" || p.Sections[0].TranslatedFrom != "ja" {
				t.Fatalf("translated post: %+v, requests=%d, err=%v", p, calls, err)
			}
			box := card(l, p).Components[0].(discord.ContainerComponent)
			body := box.Components[1].(discord.TextDisplayComponent).Content
			footer := box.Components[len(box.Components)-1].(discord.TextDisplayComponent).Content
			if body != "Hello everyone" || !strings.HasPrefix(footer, "-# ") || !strings.Contains(footer, "fxembed · [tl from:](<"+l.URL+">) 🇫🇷 🇯🇵") {
				t.Fatalf("body=%q footer=%q", body, footer)
			}
			encoded, _ := json.Marshal(box)
			if strings.Contains(string(encoded), `"type":2`) || strings.Contains(string(encoded), "Bonjour") {
				t.Fatalf("translation must replace the body without adding a button: %s", encoded)
			}
		})
	}
}

func TestFxMissingOrUnusableTranslationKeepsOriginal(t *testing.T) {
	for _, translation := range []string{
		`null`, `{}`, `{"text":" ","source_lang":"fr","target_lang":"en"}`,
		`{"text":"Bonjour","source_lang":"fr","target_lang":"en"}`,
		`{"text":"Hello","source_lang":"fr","target_lang":"de"}`,
		`{"text":"Hello","source_lang":"en-US","target_lang":"en"}`,
		`{"text":"Hello","source_lang":"und","target_lang":"en"}`,
	} {
		t.Run(translation, func(t *testing.T) {
			a := apiFor(t, func(*http.Request) (*http.Response, error) {
				return response(`{"code":200,"status":{"id":"123","text":"Bonjour","translation":` + translation + `}}`), nil
			})
			l, _ := Parse("https://x.com/author/status/123")
			p, err := a.Resolve(context.Background(), l)
			if err != nil || p.Text != "Bonjour" || p.TranslatedFrom != "" || translationCredit(l, p) != "" {
				t.Fatalf("original not preserved: %+v %v", p, err)
			}
		})
	}
}

func TestEnglishPostCreditsOnlyItsTranslatedQuote(t *testing.T) {
	l, _ := Parse("https://x.com/author/status/123")
	a := apiFor(t, func(*http.Request) (*http.Response, error) {
		return response(`{"code":200,"status":{"id":"123","text":"An English response","quote":{"text":"Bonjour","translation":{"text":"Hello","source_lang":"fr","target_lang":"en"}}}}`), nil
	})
	p, err := a.Resolve(context.Background(), l)
	if err != nil || p.Text != "An English response" || p.TranslatedFrom != "" || !strings.HasSuffix(translationCredit(l, p), "🇫🇷") {
		t.Fatalf("quote-only translation: %+v %v", p, err)
	}
}

func TestFixEmbedTranslationAcrossPlatformsAndFallback(t *testing.T) {
	for _, raw := range []string{
		"https://x.com/author/status/123", "https://bsky.app/profile/author.example/post/123",
		"https://reddit.com/r/test/comments/abc/title/", "https://instagram.com/p/ABC/",
		"https://threads.net/@author/post/ABC", "https://pixiv.net/artworks/123",
		"https://youtube.com/post/UgABC", "https://bilibili.com/video/BV123",
		"https://pinterest.com/pin/123", "https://tiktok.com/@author/video/123",
		"https://tumblr.com/author/123/title", "https://twitch.tv/videos/123",
		"https://deviantart.com/author/art/title-123",
	} {
		l, _ := Parse(raw)
		t.Run(l.Platform, func(t *testing.T) {
			calls := 0
			a := apiFor(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/api/embed" {
					return response(`{"code":404}`), nil
				}
				if r.URL.Query().Get("lang") != "en" || r.URL.Query().Get("url") != l.URL {
					t.Fatalf("translation request: %s", r.URL)
				}
				return response(`{"success":true,"platform":"` + l.Platform + `","data":{"title":"A translated title","caption":"A translated caption","authorName":"Author","translation":{"sourceLanguage":"ja","targetLanguage":"en"},"image":"https://cdn.example/photo.jpg","sections":[{"kind":"quote","body":"A translated quote"}]}}`), nil
			})
			p, err := a.Resolve(context.Background(), l)
			wantCalls := 1
			if l.Platform == "twitter" || l.Platform == "bluesky" {
				wantCalls++
			}
			if err != nil || calls != wantCalls || p.Text != "A translated caption" || p.Title != "A translated title" || p.TranslatedFrom != "ja" || len(p.Media) != 1 || p.Sections[0].Text != "A translated quote" {
				t.Fatalf("translated FixEmbed post: %+v, requests=%d, err=%v", p, calls, err)
			}
		})
	}
}

func TestTranslationCreditIsCompactAndBounded(t *testing.T) {
	for _, tc := range []struct{ source, target, want string }{
		{"ja", "en", "ja"}, {"zh-CN", "en-US", "zh"}, {"jpn", "eng", "ja"},
		{"iw", "en", "he"}, {"pt_BR", "en", "pt"}, {"en", "en", ""},
		{"und", "en", ""}, {"bad language @everyone", "en", ""}, {"fr", "invalid tag!", ""},
	} {
		if got := translationSource(tc.source, tc.target); got != tc.want {
			t.Errorf("%q -> %q: %q, want %q", tc.source, tc.target, got, tc.want)
		}
	}
	l, _ := Parse("https://x.com/author/status/123")
	p := Post{Author: "Author", Text: strings.Repeat("😀 translated text\n", 2000), Stats: strings.Repeat("1 likes · ", 1000), Provider: "fxembed", Timestamp: "2026-10-22T10:13:00Z", TranslatedFrom: "ja", Sections: []Section{{TranslatedFrom: "ja"}, {TranslatedFrom: "fr"}}}
	box := card(l, p).Components[0].(discord.ContainerComponent)
	footer := box.Components[len(box.Components)-1].(discord.TextDisplayComponent).Content
	if strings.Count(footer, "🇯🇵") != 1 || !strings.Contains(footer, "🇫🇷") || !strings.HasSuffix(footer, " · <t:1792663980:R>") || len(utf16.Encode([]rune(footer))) > 500 {
		t.Fatal(footer)
	}
	units := 0
	for _, component := range box.Components {
		if text, ok := component.(discord.TextDisplayComponent); ok {
			units += len(utf16.Encode([]rune(text.Content)))
		}
	}
	if units > 4000 {
		t.Fatalf("card text exceeds Discord's budget: %d", units)
	}
	if got := languageMark("haw"); got != "haw" {
		t.Fatal(got)
	}
	l.URL += strings.Repeat("a", 1000)
	if credit := translationCredit(l, p); strings.Contains(credit, "](<") || !strings.Contains(credit, "tl from:") {
		t.Fatal(credit)
	}
	for code, country := range languageCountries {
		if len(country) != 2 || len([]rune(languageMark(code))) != 2 {
			t.Fatal("invalid language flag", code)
		}
	}
}

func TestEnglishSuffixLinksKeepTheirOriginalDestination(t *testing.T) {
	for _, raw := range []string{
		"https://fixupx.com/author/status/123/en", "https://fxtwitter.com/author/status/123/photo/2/en/",
		"https://x.com/i/web/status/123/en", "https://fxbsky.app/profile/author.example/post/123/en",
	} {
		l, ok := Parse(raw)
		if !ok || strings.HasSuffix(l.URL, "/en") {
			t.Fatalf("translation suffix: %+v %t", l, ok)
		}
		again, ok := Parse(l.URL)
		if !ok || again != l {
			t.Fatalf("non-canonical link: %+v", l)
		}
	}
}
