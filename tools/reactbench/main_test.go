package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func answer(path string, code int, h map[string]string) *http.Response {
	rs := &http.Response{StatusCode: code, Header: http.Header{}, Request: &http.Request{URL: &url.URL{Path: path}}}
	for k, v := range h {
		rs.Header.Set(k, v)
	}
	return rs
}

// Only the reaction route is counted, by limit and scope, 429s apart.
func TestSeenCountsTheReactionRoute(t *testing.T) {
	s := &seen{buckets: map[string]int{}}
	react := "/api/v10/channels/4/messages/9/reactions/x/@me"
	s.note(answer(react, 204, map[string]string{"X-RateLimit-Limit": "1"}))
	s.note(answer(react, 204, map[string]string{"X-RateLimit-Limit": "1"}))
	limited := answer(react, 429, map[string]string{"X-RateLimit-Limit": "1", "X-RateLimit-Scope": "user", "X-RateLimit-Reset-After": "0.03"})
	limited.Body = io.NopCloser(strings.NewReader(`{"retry_after": 0.2}`))
	s.note(limited)
	if got, _ := io.ReadAll(limited.Body); string(got) != `{"retry_after": 0.2}` {
		t.Fatalf("the body disgo reads next is %q", got)
	}
	s.note(answer("/api/v10/channels/4/messages", 429, nil))
	if len(s.retryAfter) != 1 || s.retryAfter[0] != 200*time.Millisecond || s.resetAfter[0] != 30*time.Millisecond {
		t.Fatalf("waits %v %v", s.retryAfter, s.resetAfter)
	}
	if s.rateLimits() != 1 || s.buckets["limit 1 · route"] != 2 || s.buckets["limit 1 · user 429"] != 1 || len(s.buckets) != 2 {
		t.Fatalf("seen %v, %d 429s", s.buckets, s.rateLimits())
	}
}

func TestMedian(t *testing.T) {
	if m := median([]time.Duration{3, 1, 2}); m != 2 {
		t.Fatalf("median %v", m)
	}
}
