package fix

import (
	"context"
	"errors"
	"html"
	"net/http"
	"strings"
	"testing"
)

const muxURL = "https://packaged-media.redd.it/abc/pb/m2-res_720p.mp4?m=DASHPlaylist.mpd&e=9999999999&s=SignedAbC"

func TestRedditMuxedIsIndependentOfJSONFieldOrder(t *testing.T) {
	first := `"source":{"url":"https://packaged-media.redd.it/abc/pb/m2-res_360p.mp4","dimensions":{"height":360,"width":640}}`
	reordered := `"source":{"dimensions":{"height":720,"width":1280},"url":"` + muxURL + `"}`
	if got := redditMuxed("<div data='" + html.EscapeString(first+","+reordered) + "'>"); got != muxURL {
		t.Fatal(got)
	}
	for _, candidate := range []string{`"source":{"url":"http://packaged-media.redd.it/a.mp4","dimensions":{"height":720,"width":1280}}`, `"source":{"url":"https://packaged-media.redd.it.evil.example/a.mp4","dimensions":{"height":720,"width":1280}}`, `"source":{"url":"https://user@packaged-media.redd.it/a.mp4","dimensions":{"height":720,"width":1280}}`, `"source":{"url":"https://packaged-media.redd.it/a.m3u8","dimensions":{"height":720,"width":1280}}`, `"source":{"url":"https://packaged-media.redd.it/a.mp4","dimensions":{"height":0,"width":1280}}`, `"source":{bad json}`, `"source":{"url":"https://%zz"}`} {
		if got := redditMuxed(candidate); got != "" {
			t.Fatal(got)
		}
	}
	only := `"source":{"dimensions":{"height":1080,"width":1920},"url":"https://packaged-media.redd.it/1080.mp4"},"source":{"dimensions":{"height":2160,"width":3840},"url":"https://packaged-media.redd.it/2160.mp4"}`
	if got := redditMuxed(only); got != "https://packaged-media.redd.it/1080.mp4" {
		t.Fatal(got)
	}
}
func TestRedditVideoReplacesSilentStreamWithoutLosingSignature(t *testing.T) {
	l, _ := Parse("https://reddit.com/r/test/comments/abc/title")
	calls := 0
	a := apiFor(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(strings.Replace(fixFixture, "https://packaged-media.redd.it/abc/pb/m2-res_720p.mp4?sig=Signed&foo=bar", "https://v.redd.it/abc/CMAF_720.mp4", 1)), nil
		}
		if req.URL.String() != "https://embed.reddit.com/r/test/comments/abc/" {
			t.Fatal(req.URL)
		}
		r := response(`"source":{"dimensions":{"height":720,"width":1280},"url":"` + muxURL + `"}`)
		r.Header.Set("Content-Type", "text/html; charset=utf-8")
		return r, nil
	})
	p, err := a.Resolve(context.Background(), l)
	if err != nil || p.Media[0].URL != muxURL || calls != 2 {
		t.Fatalf("%+v %v calls=%d", p, err, calls)
	}
	d := &fixData{}
	d.SourceURL = l.URL
	// Fetch the normal metadata into the wire type so every fallback is tested.

	d.Video = &struct {
		URL       string `json:"url"`
		MediaType string `json:"mediaType"`
	}{URL: "https://v.redd.it/abc/DASH_720.mp4"}
	failures := []transport{
		func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") },
		func(*http.Request) (*http.Response, error) { r := response(""); r.StatusCode = 403; return r, nil },
		func(*http.Request) (*http.Response, error) { return response(`{}`), nil },
		func(*http.Request) (*http.Response, error) {
			r := response("no candidates")
			r.Header.Set("Content-Type", "text/html")
			return r, nil
		},
		func(*http.Request) (*http.Response, error) {
			r := response(strings.Repeat("x", 512*1024+1))
			r.Header.Set("Content-Type", "text/html")
			return r, nil
		},
		func(*http.Request) (*http.Response, error) {
			r := response("")
			r.Header.Set("Content-Type", "text/html")
			r.Body = brokenReader{}
			return r, nil
		},
	}
	for i, fn := range failures {
		a.c.Transport = fn
		p := Post{}
		a.reddit(context.Background(), l, d, &p)
		if !strings.Contains(p.Context, "may be silent") {
			t.Fatalf("case %d: %+v", i, p)
		}
	}
	d.SourceURL = ""
	short, _ := Parse("https://redd.it/abc")
	p = Post{}
	a.reddit(context.Background(), short, d, &p)
	if !strings.Contains(p.Context, "may be silent") {
		t.Fatal(p)
	}
	d.Video = nil
	a.reddit(context.Background(), l, d, &p)
	d.Video = &struct {
		URL       string `json:"url"`
		MediaType string `json:"mediaType"`
	}{URL: "https://cdn.example/video.mp4"}
	a.reddit(context.Background(), l, d, &p)
	d.Video.MediaType = "gif"
	a.reddit(context.Background(), l, d, &p)

}
