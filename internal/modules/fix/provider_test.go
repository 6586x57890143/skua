package fix

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func apiFor(t *testing.T, fn transport) *API {
	t.Helper()
	a, err := NewAPI(Config{})
	if err != nil {
		t.Fatal(err)
	}
	a.c.Transport = fn
	return a
}

const fxFixture = `{"code":200,"status":{"id":"123","text":"Source Text","created_timestamp":1792756800,"author":{"name":"Creator","screen_name":"creator","avatar_url":"https://cdn.example/a.png"},"likes":3,"reposts":2,"replies":1,"possibly_sensitive":true,"media":{"all":[{"type":"photo","url":"https://cdn.example/photo.jpg","altText":"Photo alt"},{"type":"video","url":"https://cdn.example/playlist.m3u8","formats":[{"container":"m3u8","url":"https://cdn.example/hls"},{"container":"mp4","url":"https://cdn.example/low.mp4","bitrate":100},{"container":"mp4","url":"https://cdn.example/high.mp4","bitrate":200}]}]},"quote":{"id":"100","type":"tombstone"},"poll":{"choices":[{"label":"One","count":3,"percentage":100}],"total_votes":3},"community_note":{"text":"Note context"}}}`
const fixFixture = `{"success":true,"platform":"reddit","data":{"url":"https://reddit.com/r/test/comments/abc/title","title":"A title","caption":"Complete Caption","description":"Short","authorName":"Creator","authorHandle":"u/creator","authorAvatar":"https://cdn.example/a.png","stats":"3 votes","timestamp":"2026-10-23T12:00:00Z","sensitivityTypes":["spoiler"],"video":{"url":"https://packaged-media.redd.it/abc/pb/m2-res_720p.mp4?sig=Signed&foo=bar","mediaType":"video"},"images":["https://cdn.example/photo.jpg"],"mediaDescriptions":["Video alt","Photo alt"],"sections":[{"kind":"quote","authorName":"Quoted Creator","body":"Quote Text","image":"https://cdn.example/quote.jpg"}]}}`

func TestAPIConfig(t *testing.T) {
	a, err := NewAPI(Config{})
	if err != nil || a.cfg.Twitter != "https://api.fxtwitter.com" {
		t.Fatalf("%+v %v", a, err)
	}
	if err := a.c.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatal(err)
	}
	for _, base := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?q=x", "https://example.com/#fragment", "https://%zz"} {
		if _, err := NewAPI(Config{FixEmbed: base}); err == nil {
			t.Fatal(base)
		}
	}
	t.Setenv("SKUA_FIX_API", "https://fix.example/")
	t.Setenv("SKUA_FIX_TWITTER_API", "https://x.example")
	t.Setenv("SKUA_FIX_BLUESKY_API", "https://b.example")
	a, err = NewAPI(FromEnv())
	if err != nil || a.cfg.FixEmbed != "https://fix.example" {
		t.Fatalf("%+v %v", a, err)
	}
}
func TestFxV2AndFallback(t *testing.T) {
	l, _ := Parse("https://x.com/foo/status/123")
	a := apiFor(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/2/status/123" || req.Header.Get("Accept") != "application/json" || req.Header.Get("User-Agent") == "" {
			t.Fatal(req)
		}
		return response(fxFixture), nil
	})
	p, err := a.Resolve(context.Background(), l)
	if err != nil || p.Provider != "fxembed" || p.Text != "Source Text" || len(p.Sections) != 3 || len(p.Media) != 2 || p.Media[1].URL != "https://cdn.example/high.mp4" || p.Media[0].Alt != "Photo alt" || !p.Sensitive {
		t.Fatalf("%+v %v", p, err)
	}
	l, _ = Parse("https://bsky.app/profile/bsky.app/post/3mqafridzgk2e")
	bluesky, readErr := os.ReadFile("testdata/bluesky-v2.json")
	if readErr != nil {
		t.Fatal(readErr)
	}
	a.c.Transport = transport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.fxbsky.app" || req.URL.Path != "/2/status/bsky.app/3mqafridzgk2e" {
			t.Fatal(req.URL)
		}
		return response(string(bluesky)), nil
	})
	p, err = a.Resolve(context.Background(), l)
	if err != nil || p.Timestamp != "2026-07-09T19:50:16Z" || len(p.Media) != 1 || p.Media[0].Alt == "" {
		t.Fatalf("%+v %v", p, err)
	}

	calls := 0
	a.c.Transport = transport(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(`{"code":404,"status":null}`), nil
		}
		if req.URL.Query().Get("url") != l.URL || req.URL.Query().Get("renderer") != "components-v2" {
			t.Fatal(req.URL)
		}
		return response(strings.Replace(fixFixture, `"platform":"reddit"`, `"platform":"bluesky"`, 1)), nil
	})
	p, err = a.Resolve(context.Background(), l)
	if err != nil || p.Provider != "fixembed" || calls != 2 {
		t.Fatalf("%+v %v calls %d", p, err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	a.c.Transport = transport(func(req *http.Request) (*http.Response, error) { calls++; return nil, context.Canceled })
	if _, err = a.Resolve(ctx, l); err == nil || calls != 1 {
		t.Fatalf("%v %d", err, calls)
	}
	l.ID = "forged"
	if _, err = a.Resolve(context.Background(), l); err == nil {
		t.Fatal("forged fields admitted")
	}
}
func TestFixMetadataAndValidation(t *testing.T) {
	l, _ := Parse("https://reddit.com/r/test/comments/abc/title")
	a := apiFor(t, func(req *http.Request) (*http.Response, error) { return response(fixFixture), nil })
	p, err := a.Resolve(context.Background(), l)
	if err != nil || p.Text != "Complete Caption" || len(p.Media) != 2 || p.Media[1].Alt != "Photo alt" || len(p.Sections) != 1 || p.Sections[0].Title != "quote · Quoted Creator" || !p.Sensitive || !p.Media[0].Sensitive {
		t.Fatalf("%+v %v", p, err)
	}
	for _, body := range []string{`{}`, `{"success":true,"platform":"reddit"}`, `{"success":true,"platform":"reddit","data":{}}`, `{"success":true,"platform":"twitter","data":{"title":"wrong platform"}}`, `not-json`} {
		a.c.Transport = transport(func(*http.Request) (*http.Response, error) { return response(body), nil })
		if _, err = a.Resolve(context.Background(), l); err == nil {
			t.Fatal(body)
		}
	}
	body := `{"success":true,"platform":"reddit","data":{"title":"text","description":"Description","mediaOrigin":"quote","sections":[{"kind":"quote","body":"quote","image":"https://cdn.example/quote.png"}]}}`
	a.c.Transport = transport(func(*http.Request) (*http.Response, error) { return response(body), nil })
	p, err = a.Resolve(context.Background(), l)
	if err != nil || p.Text != "Description" || len(p.Media) != 0 || len(p.Sections[0].Media) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	var d fixData
	d.Image = "https://cdn.example/a.png"
	if len(d.media()) != 1 {
		t.Fatal(d.media())
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenReader) Close() error             { return nil }
func TestBoundedHTTPFailures(t *testing.T) {
	a := apiFor(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("network") })
	if err := a.get(context.Background(), "https://example.com", &struct{}{}); err == nil {
		t.Fatal("network")
	}
	for _, status := range []int{302, 401, 403, 429, 500} {
		a.c.Transport = transport(func(*http.Request) (*http.Response, error) { r := response(`{}`); r.StatusCode = status; return r, nil })
		if err := a.get(context.Background(), "https://example.com", &struct{}{}); err == nil {
			t.Fatal(status)
		}
	}
	a.c.Transport = transport(func(*http.Request) (*http.Response, error) {
		return response(strings.Repeat("a", responseLimit+1)), nil
	})
	if err := a.get(context.Background(), "https://example.com", &struct{}{}); err == nil {
		t.Fatal("unbounded")
	}
	a.c.Transport = transport(func(*http.Request) (*http.Response, error) { r := response(""); r.Body = brokenReader{}; return r, nil })
	if err := a.get(context.Background(), "https://example.com", &struct{}{}); err == nil {
		t.Fatal("body error")
	}
	if err := a.get(context.Background(), "https://%zz", &struct{}{}); err == nil {
		t.Fatal("bad request URL")
	}
}
func TestFxLegacyMediaQuoteAndPoll(t *testing.T) {
	body := `{"code":200,"status":{"id":"123","text":"text","media":{"photos":[{"url":"https://cdn.example/image.png"}],"videos":[{"type":"gif","url":"https://cdn.example/gif.mp4"}]},"quote":{"text":"Quote Text","author":{"name":"Another"},"possibly_sensitive":true},"poll":{"choices":[{},{},{},{},{},{}]}}}`
	a := apiFor(t, func(*http.Request) (*http.Response, error) { return response(body), nil })
	l, _ := Parse("https://x.com/a/status/123")
	p, err := a.fx(context.Background(), l)
	if err != nil || len(p.Media) != 2 || !p.Sections[0].Sensitive {
		t.Fatalf("%+v %v", p, err)
	}
	for _, body := range []string{`{"code":200,"status":{"id":"wrong","text":"text"}}`, `{"code":200,"status":{"id":"123"}}`} {
		a.c.Transport = transport(func(*http.Request) (*http.Response, error) { return response(body), nil })
		if _, err := a.fx(context.Background(), l); err == nil {
			t.Fatal(body)
		}
	}
	a.c.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if _, err := a.fx(context.Background(), l); err == nil {
		t.Fatal("offline")
	}
}
