package armada

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type blossomLimitTransport func(*http.Request) (*http.Response, error)

func (f blossomLimitTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type blossomLimitBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *blossomLimitBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *blossomLimitBody) Close() error { b.closed = true; return nil }

func TestBlossomDescriptorLimits(t *testing.T) {
	const limit = 64 << 10
	const valid = `{"url":"https://cdn.example/blob"}`
	for _, tc := range []struct {
		name string
		body string
		want string
		fail bool
	}{
		{"valid", valid, "https://cdn.example/blob", false},
		{"hash fallback", `{}`, "", false},
		{"unsafe URL fallback", `{"url":"javascript:alert(1)"}`, "", false},
		{"exact byte boundary", valid + strings.Repeat(" ", limit-len(valid)), "https://cdn.example/blob", false},
		{"over byte boundary", valid + strings.Repeat(" ", limit-len(valid)+1), "", true},
		{"large unknown field", `{"url":"https://cdn.example/blob","extra":"` + strings.Repeat("x", limit) + `"}`, "", true},
		{"malformed", `{"url":`, "", true},
		{"wrong URL type", `{"url":7}`, "", true},
		{"null", `null`, "", true},
		{"empty", "", "", true},
		{"trailing value", valid + `{}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &blossomLimitBody{Reader: strings.NewReader(tc.body)}
			b := &blossom{http: &http.Client{Transport: blossomLimitTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})}}
			got, err := b.put(context.Background(), "https://blob.example", []byte("dummy"), "image/png", "dummy auth")
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("URL=%q err=%v, want URL=%q fail=%v", got, err, tc.want, tc.fail)
			}
			if body.read > limit+1 || !body.closed {
				t.Fatalf("read=%d closed=%v", body.read, body.closed)
			}
		})
	}
}

func TestBlossomDescriptorFailureTriesNextServer(t *testing.T) {
	for _, rejected := range []string{`{"url":`, `{"padding":"` + strings.Repeat("x", 64<<10) + `"}`} {
		var hosts []string
		b := &blossom{
			servers: []string{"https://bad.example", "https://good.example"},
			http: &http.Client{Transport: blossomLimitTransport(func(r *http.Request) (*http.Response, error) {
				hosts = append(hosts, r.URL.Host)
				body := `{"url":"https://cdn.example/blob"}`
				if r.URL.Host == "bad.example" {
					body = rejected
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
			})},
		}
		got, _, err := b.upload(context.Background(), []byte("dummy"), "image/png", key(t, 7).SK)
		if err != nil || got != "https://cdn.example/blob.png" || len(hosts) != 2 || hosts[1] != "good.example" {
			t.Fatalf("URL=%q hosts=%v err=%v", got, hosts, err)
		}
	}
}
