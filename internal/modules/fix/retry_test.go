package fix

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

const instagramPartial = `{"success":true,"platform":"instagram","source":"fallback","data":{"title":"Post","video":{"url":"https://fixembed.app/video/instagram?url=video"}}}`
const instagramFull = `{"success":true,"platform":"instagram","data":{"authorName":"Creator","caption":"Original caption","video":{"url":"https://cdn.example/video.mp4"}}}`

func TestFixMetadataRetryClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body, after string
		status            int
		transportErr      error
		readErr           bool
		want              int
	}{
		{name: "healthy", body: instagramFull, want: 1},
		{name: "request timeout", status: 408, want: 2},
		{name: "rate limit", status: 429, after: "1", want: 2},
		{name: "server error", status: 500, want: 2},
		{name: "gateway error", status: 502, want: 2},
		{name: "unavailable", status: 503, want: 2},
		{name: "gateway timeout", status: 504, want: 2},
		{name: "timeout transport", transportErr: context.DeadlineExceeded, want: 2},
		{name: "connection reset", transportErr: &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}, want: 2},
		{name: "truncated body", readErr: true, want: 2},
		{name: "provider failure", body: `{"success":false}`, want: 2},
		{name: "empty data", body: `{"success":true,"platform":"instagram","data":{}}`, want: 2},
		{name: "missing data", body: `{"success":true,"platform":"instagram"}`, want: 2},
		{name: "unauthorized", status: 401, want: 1},
		{name: "forbidden", status: 403, want: 1},
		{name: "not found", status: 404, want: 1},
		{name: "redirect", status: 302, want: 1},
		{name: "not implemented", status: 501, want: 1},
		{name: "malformed json", body: "not json", want: 1},
		{name: "oversized", body: strings.Repeat("a", responseLimit+1), want: 1},
		{name: "wrong platform", body: `{"success":true,"platform":"reddit","data":{"title":"wrong"}}`, want: 1},
		{name: "missing platform", body: `{"success":true,"data":{"title":"wrong"}}`, want: 1},
		{name: "tls certificate", transportErr: x509.UnknownAuthorityError{}, want: 1},
		{name: "unknown host", transportErr: &net.DNSError{IsNotFound: true}, want: 1},
		{name: "cancelled transport", transportErr: context.Canceled, want: 1},
		{name: "long retry after", status: 429, after: "3600", want: 1},
		{name: "far future retry after", status: 503, after: "Fri, 31 Dec 9999 23:59:59 GMT", want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				started := time.Now()
				a := apiFor(t, func(req *http.Request) (*http.Response, error) {
					calls++
					if calls == 2 {
						if req.URL.Query().Get("_conformance") != "1" {
							t.Fatal("recovery read the same provider cache")
						}
						return response(instagramFull), nil
					}
					if req.URL.Query().Has("_conformance") {
						t.Fatal("healthy path bypassed cache")
					}
					if tc.transportErr != nil {
						return nil, tc.transportErr
					}
					r := response(tc.body)
					if tc.status != 0 {
						r.StatusCode = tc.status
					}
					r.Header.Set("Retry-After", tc.after)
					if tc.readErr {
						r.Body = brokenReader{}
					}
					return r, nil
				})
				l, _ := Parse("https://www.instagram.com/reels/De5kQ-IJ60z/")
				p, err := a.Resolve(context.Background(), l)
				if calls != tc.want {
					t.Fatalf("calls %d want %d, err %v", calls, tc.want, err)
				}
				if tc.want == 2 || tc.name == "healthy" {
					if err != nil || p.Author != "Creator" || p.Incomplete {
						t.Fatalf("%+v %v", p, err)
					}
				} else if !errors.Is(err, errMetadata) {
					t.Fatalf("missing safe metadata error: %v", err)
				}
				if tc.status == 429 && tc.want == 2 && time.Since(started) < time.Second {
					t.Fatal("ignored retry-after")
				}
			})
		})
	}
}

func TestInstagramRecoveryPreservesBestMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, author, text string
		status                            int
		partial                           bool
	}{
		{name: "recovered", second: instagramFull, author: "Creator", text: "Original caption"},
		{name: "still partial", second: instagramPartial, partial: true},
		{name: "failed recovery", status: 503, partial: true},
		{name: "refused recovery", status: 403, partial: true},
		{name: "wrong platform recovery", second: `{"success":true,"platform":"twitter","data":{"title":"wrong"}}`, partial: true},
		{name: "missing platform recovery", second: `{"success":true,"data":{"title":"wrong"}}`, partial: true},
		{name: "complementary metadata", second: `{"success":true,"platform":"instagram","data":{"authorName":"Creator","caption":"Original caption","image":"https://cdn.example/poster.jpg"}}`, author: "Creator", text: "Original caption"},
		{name: "preserve sensitivity and caption", first: `{"success":true,"platform":"instagram","data":{"title":"Real title","caption":"Keep caption","sensitivityTypes":["spoiler"],"video":{"url":"https://cdn.example/video.mp4"}}}`, second: `{"success":true,"platform":"instagram","data":{"title":"Post","authorHandle":"creator"}}`, text: "Keep caption"},
		{name: "captionless post is complete", first: `{"success":true,"platform":"instagram","data":{"authorName":"Creator","image":"https://cdn.example/image.jpg"}}`, author: "Creator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				a := apiFor(t, func(req *http.Request) (*http.Response, error) {
					if req.URL.Host == "www.instagram.com" {
						r := response("")
						r.StatusCode = 403
						return r, nil
					}
					calls++
					if calls == 1 {
						if tc.first != "" {
							return response(tc.first), nil
						}
						return response(instagramPartial), nil
					}
					r := response(tc.second)
					if tc.status != 0 {
						r.StatusCode = tc.status
					}
					return r, nil
				})
				l, _ := Parse("https://instagram.com/reel/De5kQ-IJ60z/")
				p, err := a.Resolve(context.Background(), l)
				if err != nil || len(p.Media) == 0 || p.Incomplete != tc.partial || p.Author != tc.author || p.Text != tc.text {
					t.Fatalf("calls %d: %+v %v", calls, p, err)
				}
				want := 2
				if tc.name == "captionless post is complete" {
					want = 1
				}
				if calls != want {
					t.Fatalf("calls %d want %d", calls, want)
				}
				if tc.partial && (p.Title != "" || !strings.Contains(p.Context, "some instagram details are unavailable")) {
					t.Fatalf("missing partial preview explanation: %+v", p)
				}
				if tc.name == "complementary metadata" && !strings.Contains(p.Media[0].URL, "/video/instagram") {
					t.Fatal("poster replaced playable video")
				}
				if tc.name == "preserve sensitivity and caption" && (!p.Sensitive || !p.Media[0].Sensitive || p.Title != "Real title") {
					t.Fatal("lost source metadata", p)
				}
				m := module()
				m.api = resolverFunc(func(context.Context, Link) (Post, error) { return p, nil })
				_, complete := m.previewResult(context.Background(), 3, l)
				if complete == tc.partial {
					t.Fatal("incomplete preview would suppress the source")
				}
			})
		})
	}
}

func TestMetadataRetryDeadlinesAndCancellation(t *testing.T) {
	for _, which := range []string{"attempt timeouts", "short deadline", "cancel backoff", "cancel before request", "partial then timeout", "retry-after date", "custom provider"} {
		t.Run(which, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if which == "short deadline" {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, 200*time.Millisecond)
					defer stop()
				}
				if which == "cancel before request" {
					cancel()
				}
				start := time.Now()
				a := apiFor(t, func(req *http.Request) (*http.Response, error) {
					if req.URL.Host == "www.instagram.com" {
						r := response("")
						r.StatusCode = 403
						return r, nil
					}
					calls++
					if which == "custom provider" && req.URL.Query().Has("_conformance") {
						t.Fatal("changed custom provider query contract")
					}
					if which == "partial then timeout" && calls == 1 {
						return response(instagramPartial), nil
					}
					if which == "attempt timeouts" || which == "partial then timeout" {
						<-req.Context().Done()
						return nil, req.Context().Err()
					}
					if which == "cancel backoff" {
						go func() { time.Sleep(50 * time.Millisecond); cancel() }()
					}
					if calls == 2 {
						return response(instagramFull), nil
					}
					r := response(`{}`)
					r.StatusCode = 503
					if which == "retry-after date" {
						r.Header.Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))
					}
					return r, nil
				})
				if which == "custom provider" {
					a.cfg.FixEmbed = "https://custom.example"
				}
				l, _ := Parse("https://instagram.com/reel/De5kQ-IJ60z/")
				p, err := a.Resolve(ctx, l)
				elapsed := time.Since(start)
				want := 2
				switch which {
				case "short deadline", "cancel backoff":
					want = 1
				case "cancel before request":
					want = 0
				}
				if calls != want || elapsed > metadataBudget {
					t.Fatalf("calls %d want %d elapsed %s", calls, want, elapsed)
				}
				if which == "partial then timeout" {
					if err != nil || !p.Incomplete || len(p.Media) != 1 {
						t.Fatalf("lost usable partial: %+v %v", p, err)
					}
				} else if which == "retry-after date" || which == "custom provider" {
					if err != nil || p.Incomplete {
						t.Fatalf("%+v %v", p, err)
					}
					if which == "retry-after date" && elapsed < 2*time.Second {
						t.Fatal("retried before provider deadline")
					}
				} else if err == nil {
					t.Fatal("expected bounded failure")
				}
			})
		})
	}
}

func TestMetadataFailuresContainNoSourceContent(t *testing.T) {
	a := apiFor(t, func(*http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: "https://secret.example/private-caption", Err: context.DeadlineExceeded}
	})
	var decoded any
	err := a.get(context.Background(), "https://example.com", &decoded)
	if !errors.Is(err, errMetadata) || !strings.Contains(err.Error(), "timed out") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "caption") {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "invalid", "-1", "18446744073709551615", "3600", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)} {
		if got := retryAfter(raw); got < 0 {
			t.Fatalf("negative retry after %q: %s", raw, got)
		}
	}
}
