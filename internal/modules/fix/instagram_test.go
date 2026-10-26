package fix

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

const instagramHTML = `<html><head>
<meta content='https://www.instagram.com/reel/De5kQ-IJ60z/' property='og:url'>
<meta property="og:title" content="Creator on Instagram: &quot;Original caption &amp; more&quot;">
<meta content='20 likes, 3 comments - creator_name on October 25, 2026: &quot;Original caption &amp; more&quot;' property='og:description'>
</head><body>post</body></html>`

func instagramPage(body string) *http.Response {
	r := response(body)
	r.Header.Set("Content-Type", "text/html; charset=utf-8")
	return r
}

func TestInstagramMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, page, author, handle, caption string
		valid                               bool
	}{
		{name: "open graph", page: instagramHTML, author: "Creator", handle: "creator_name", caption: "Original caption & more", valid: true},
		{name: "owner metadata", page: `<META PROPERTY='og:url' CONTENT='https://instagram.com/p/De5kQ-IJ60z/'><meta property='instapp:owner_user_name' content='creator'>`, author: "creator", handle: "creator", valid: true},
		{name: "title only", page: `<meta property='og:url' content='https://instagram.com/p/De5kQ-IJ60z/'><meta property='og:title' content='Creator on Instagram: &quot;Caption&quot;'>`, author: "Creator", caption: "Caption", valid: true},
		{name: "json ld", page: `<script type="application/ld+json">{"@type":"VideoObject","url":"https://www.instagram.com/reels/De5kQ-IJ60z/","description":"JSON caption","author":{"name":"JSON creator","url":"https://www.instagram.com/json_creator/"}}</script>`, author: "JSON creator", handle: "json_creator", caption: "JSON caption", valid: true},
		{name: "combined", page: instagramHTML + `<script type="application/ld+json">{"@type":"SocialMediaPosting","url":"https://www.instagram.com/p/De5kQ-IJ60z/","caption":"Structured caption","author":{"name":"Structured creator","url":"https://instagram.com/creator_name/"}}</script>`, author: "Structured creator", handle: "creator_name", caption: "Structured caption", valid: true},
		{name: "login shell", page: `<title>Instagram</title><meta property='og:description' content='Log in to Instagram to see more posts.'>`},
		{name: "login form with stale tags", page: instagramHTML + `<form action='/accounts/login/'></form>`},
		{name: "challenge", page: instagramHTML + `<form action='/challenge/123/'></form>`},
		{name: "checkpoint", page: instagramHTML + `<form action='https://www.instagram.com/checkpoint/123/'></form>`},
		{name: "wrong post", page: strings.ReplaceAll(instagramHTML, "De5kQ-IJ60z", "another")},
		{name: "missing source", page: `<meta property='og:title' content='Creator on Instagram: &quot;Caption&quot;'>`},
		{name: "generic metadata", page: `<meta property='og:url' content='https://instagram.com/p/De5kQ-IJ60z/'><meta property='og:title' content='Instagram'>`},
		{name: "tags inside script", page: `<script>var markup = ` + "`" + instagramHTML + "`" + `;</script>`},
		{name: "commented tags", page: `<!-- ` + instagramHTML + ` -->`},
		{name: "invalid owner", page: `<meta property='og:url' content='https://instagram.com/p/De5kQ-IJ60z/'><meta property='instapp:owner_user_name' content='../accounts/login'>`},
		{name: "unrelated json ld", page: `<script type="application/ld+json">{"@type":"VideoObject","url":"https://www.instagram.com/p/another/","author":{"name":"Wrong","url":"https://www.instagram.com/wrong/"}}</script>`},
		{name: "profile from another host", page: `<script type="application/ld+json">{"@type":"VideoObject","url":"https://instagram.com/p/De5kQ-IJ60z/","author":{"name":"Wrong","url":"https://instagram.com.evil.example/wrong/"}}</script>`},
		{name: "invalid profile", page: `<script type="application/ld+json">{"@type":"VideoObject","url":"https://instagram.com/p/De5kQ-IJ60z/","author":{"url":"https://instagram.com/accounts/login/"}}</script>`},
		{name: "reserved profile", page: `<script type="application/ld+json">{"@type":"VideoObject","url":"https://instagram.com/p/De5kQ-IJ60z/","author":{"url":"https://instagram.com/accounts/"}}</script>`},
		{name: "wrong json type", page: `<script type="application/ld+json">{"@type":"WebSite","url":"https://instagram.com/p/De5kQ-IJ60z/","author":{"url":"https://instagram.com/creator/"}}</script>`},
		{name: "malformed json", page: `<script type="application/ld+json">{broken}</script>`},
		{name: "oversized token", page: `<script>` + strings.Repeat("x", instagramHTMLLimit+1) + `</script>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := instagramMetadata([]byte(tc.page), "De5kQ-IJ60z")
			if (d != nil) != tc.valid {
				t.Fatalf("valid=%t: %+v", tc.valid, d)
			}
			if d != nil && (d.Author != tc.author || d.Handle != tc.handle || d.Caption != tc.caption) {
				t.Fatalf("unexpected metadata: %+v", d)
			}
		})
	}
}

func TestInstagramDirectRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, page                                      string
		status                                          int
		contentLength                                   int64
		badType, readErr, transportErr, slow, recovered bool
	}{
		{name: "recovered", page: instagramHTML, recovered: true},
		{name: "generic shell", page: `<title>Instagram</title>`},
		{name: "login redirect", status: 302},
		{name: "denied", status: 403},
		{name: "rate limited", status: 429},
		{name: "non html", page: instagramHTML, badType: true},
		{name: "declared too large", page: instagramHTML, contentLength: instagramHTMLLimit + 1},
		{name: "actual too large", page: instagramHTML + strings.Repeat("x", instagramHTMLLimit)},
		{name: "read failed", readErr: true},
		{name: "transport failed", transportErr: true},
		{name: "direct timeout", slow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				providerCalls, directCalls := 0, 0
				a := apiFor(t, func(req *http.Request) (*http.Response, error) {
					if req.URL.Host == "fixembed.app" {
						providerCalls++
						return response(instagramPartial), nil
					}
					directCalls++
					if req.URL.String() != "https://www.instagram.com/p/De5kQ-IJ60z/" || req.Header.Get("Accept") != "text/html" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
						t.Fatal("unexpected direct request", req.URL)
					}
					deadline, ok := req.Context().Deadline()
					if !ok || time.Until(deadline) > instagramDirectBudget {
						t.Fatal("direct request is unbounded")
					}
					if tc.slow {
						<-req.Context().Done()
						return nil, req.Context().Err()
					}
					if tc.transportErr {
						return nil, errors.New("connection failed")
					}
					r := instagramPage(tc.page)
					r.ContentLength = tc.contentLength
					if tc.status != 0 {
						r.StatusCode = tc.status
						r.Header.Set("Location", "https://evil.example/collect")
					}
					if tc.badType {
						r.Header.Set("Content-Type", "application/json")
					}
					if tc.readErr {
						r.Body = brokenReader{}
					}
					return r, nil
				})
				l, _ := Parse("https://www.instagram.com/reels/De5kQ-IJ60z/")
				start := time.Now()
				p, err := a.Resolve(context.Background(), l)
				if err != nil || providerCalls != 2 || directCalls != 1 || time.Since(start) > metadataBudget {
					t.Fatalf("provider=%d direct=%d err=%v", providerCalls, directCalls, err)
				}
				if len(p.Media) != 1 || p.Media[0].URL != "https://fixembed.app/video/instagram?url=video" {
					t.Fatal("lost video", p.Media)
				}
				if p.Incomplete == tc.recovered {
					t.Fatalf("incorrect completeness: %+v", p)
				}
				if tc.recovered && (p.Author != "Creator" || p.Text != "Original caption & more" || p.Provider != "fixembed + instagram") {
					t.Fatalf("missing direct metadata: %+v", p)
				}
			})
		})
	}
}

func TestInstagramDirectSkipsUnsafeAndExpiredRequests(t *testing.T) {
	a := apiFor(t, func(r *http.Request) (*http.Response, error) { t.Fatal("unexpected lookup", r.URL); return nil, nil })
	existing := &fixData{Title: "Post"}
	for _, raw := range []string{"https://evil.example/p/De5kQ-IJ60z/", "https://instagram.com/share/reel/abc/", "https://instagram.com/creator/"} {
		if got := a.instagramDirect(context.Background(), Link{URL: raw}, existing); got != existing {
			t.Fatal("replaced existing data")
		}
	}
	l, _ := Parse("https://instagram.com/reel/De5kQ-IJ60z/")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if a.instagramDirect(ctx, l, existing) != existing {
		t.Fatal("cancelled request mutated data")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if a.instagramDirect(ctx, l, existing) != existing {
		t.Fatal("short request mutated data")
	}
}

func TestInstagramDirectOnlyFillsMissingFields(t *testing.T) {
	a := apiFor(t, func(*http.Request) (*http.Response, error) { return instagramPage(instagramHTML), nil })
	l, _ := Parse("https://instagram.com/reel/De5kQ-IJ60z/")
	existing := &fixData{Author: "Known creator", Caption: "Known caption", Sensitive: true}
	recovered := a.instagramDirect(context.Background(), l, existing)
	if recovered.Author != existing.Author || recovered.Caption != existing.Caption || !recovered.Sensitive || recovered.Handle != "creator_name" || existing.Handle != "" {
		t.Fatalf("overwrote source metadata: %+v", recovered)
	}
}

func TestInstagramDirectSharesMetadataDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		a := apiFor(t, func(req *http.Request) (*http.Response, error) {
			calls++
			if req.URL.Host == "fixembed.app" {
				time.Sleep(3900 * time.Millisecond)
				return response(instagramPartial), nil
			}
			<-req.Context().Done()
			return nil, req.Context().Err()
		})
		l, _ := Parse("https://instagram.com/reel/De5kQ-IJ60z/")
		start := time.Now()
		p, err := a.Resolve(context.Background(), l)
		if err != nil || calls != 3 || time.Since(start) != metadataBudget || len(p.Media) != 1 || !p.Incomplete {
			t.Fatalf("requests=%d elapsed=%s incomplete=%t err=%v", calls, time.Since(start), p.Incomplete, err)
		}
	})
}

func BenchmarkInstagramMetadata(b *testing.B) {
	raw := []byte(instagramHTML + `<script>` + strings.Repeat(" ", 500*1024) + `</script>`)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		if instagramMetadata(raw, "De5kQ-IJ60z") == nil {
			b.Fatal("missing metadata")
		}
	}
}
