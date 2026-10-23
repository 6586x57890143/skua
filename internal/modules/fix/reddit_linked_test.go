package fix

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestRedditLinkedMedia(t *testing.T) {
	for _, tc := range []struct {
		name, link, child string
		fail, incomplete  bool
		calls             int
	}{
		{name: "twitch clip", link: "https://www.twitch.tv/kacey/clip/abc", child: `{"success":true,"platform":"twitch","data":{"title":"clip","video":{"url":"https://cdn.example/clip.mp4"},"sensitive":true}}`, calls: 1},
		{name: "supported photo", link: "https://instagram.com/p/abc", child: `{"success":true,"platform":"instagram","data":{"title":"post","image":"https://cdn.example/image.jpg"}}`, calls: 1},
		{name: "failed extraction", link: "https://clips.twitch.tv/abc", fail: true, incomplete: true, calls: 1},
		{name: "no media", link: "https://clips.twitch.tv/abc", child: `{"success":true,"platform":"twitch","data":{"title":"clip"}}`, incomplete: true, calls: 1},
		{name: "invalid media", link: "https://clips.twitch.tv/abc", child: `{"success":true,"platform":"twitch","data":{"title":"clip","video":{"url":"http://localhost/video.mp4"}}}`, incomplete: true, calls: 1},
		{name: "arbitrary article", link: "https://example.com/post"},
		{name: "reddit cycle", link: "https://reddit.com/r/test/comments/abc/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := apiFor(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "fixembed.app" {
					t.Fatal(r.URL)
				}
				if tc.fail {
					return nil, errors.New("offline")
				}
				return response(tc.child), nil
			})
			p := Post{Provider: "fixembed", Media: []Media{{URL: "https://cdn.example/thumbnail.jpg"}}, Sections: []Section{{Kind: "link-card", Title: "Open linked article", URL: tc.link}}}
			a.redditLinked(context.Background(), &fixData{}, &p)
			if calls != tc.calls || p.Incomplete != tc.incomplete {
				t.Fatalf("calls=%d post=%+v", calls, p)
			}
			if tc.calls > 0 && !tc.incomplete {
				if p.Media[0].URL == "https://cdn.example/thumbnail.jpg" || p.Sections[0].Kind != "linked-post" {
					t.Fatalf("%+v", p)
				}
				if tc.name == "twitch clip" && (!p.Sensitive || !p.Media[0].Sensitive) {
					t.Fatal("sensitivity lost")
				}
			}
		})
	}
}

func TestRedditLinkedThroughResolver(t *testing.T) {
	calls := 0
	a := apiFor(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(r.URL.Query().Get("url"), "reddit.com") {
			return response(`{"success":true,"platform":"reddit","data":{"title":"Reddit title","authorName":"u/US_FENTCOM","image":"https://cdn.example/thumb.jpg","sections":[{"kind":"link-card","url":"https://clips.twitch.tv/abc"}]}}`), nil
		}
		return response(`{"success":true,"platform":"twitch","data":{"title":"clip","video":{"url":"https://cdn.example/clip.mp4"}}}`), nil
	})
	l, _ := Parse("https://reddit.com/r/LivestreamFail/s/abc")
	p, err := a.Resolve(context.Background(), l)
	if err != nil || calls != 2 || p.Title != "Reddit title" || p.Author != "u/US_FENTCOM" || p.Media[0].URL != "https://cdn.example/clip.mp4" || p.Incomplete {
		t.Fatalf("%+v %v calls=%d", p, err, calls)
	}
	header := identity(l, p)
	if strings.Contains(header, `US\_FENTCOM`) || !strings.Contains(header, "[u/US_FENTCOM](<https://www.reddit.com/user/US_FENTCOM/>)") {
		t.Fatal(header)
	}
}

func TestRedditLinkedSkipsCommentAndExistingVideo(t *testing.T) {
	a := apiFor(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected fetch"); return nil, nil })
	for _, raw := range []string{`{"url":"https://reddit.com/r/test/comments/abc/title/comment"}`, `{"video":{"url":"https://cdn.example/video.mp4"}}`, `{"images":["https://cdn.example/native.jpg"]}`, `{"mediaOrigin":"quote"}`} {
		var d fixData
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		p := Post{Sections: []Section{{Kind: "link-card", URL: "https://clips.twitch.tv/abc"}}}
		a.redditLinked(context.Background(), &d, &p)
	}
}

func TestRedditCommentNeverResolvesParentLinkedMedia(t *testing.T) {
	for _, source := range []string{"", "https://reddit.com/r/test/comments/abc/title/"} {
		calls := 0
		a := apiFor(t, func(r *http.Request) (*http.Response, error) {
			calls++
			return response(`{"success":true,"platform":"reddit","data":{"url":"` + source + `","title":"parent","image":"https://cdn.example/thumb.jpg","sections":[{"kind":"link-card","url":"https://clips.twitch.tv/abc"}]}}`), nil
		})
		l, _ := Parse("https://reddit.com/r/test/comments/abc/title/comment")
		p, err := a.Resolve(context.Background(), l)
		if err != nil || calls != 1 || !p.Incomplete {
			t.Fatalf("calls=%d p=%+v err=%v", calls, p, err)
		}
	}
}
