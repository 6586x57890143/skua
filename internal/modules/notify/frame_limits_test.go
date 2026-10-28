package notify

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"testing"
)

type frameLimitTransport func(*http.Request) (*http.Response, error)

func (f frameLimitTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type frameLimitBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *frameLimitBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *frameLimitBody) Close() error { b.closed = true; return nil }

type frameZeroes struct{}

func (frameZeroes) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func frameLimitClient(body *frameLimitBody) *http.Client {
	return &http.Client{Transport: frameLimitTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}}, Body: body}, nil
	})}
}

// The PNGs are compact grayscale fixtures. Their encoded sizes are far
// below the transfer limit even when their decoded dimensions are too big.
func TestFetchImageSourceLimits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		width     int
		height    int
		wantError bool
	}{
		{"normal", 4, 3, false},
		{"pixel boundary", 4096, 2160, false},
		{"over pixel boundary", 4096, 2161, true},
		{"over side boundary", 4097, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw bytes.Buffer
			if err := png.Encode(&raw, image.NewGray(image.Rect(0, 0, tc.width, tc.height))); err != nil {
				t.Fatal(err)
			}
			body := &frameLimitBody{Reader: bytes.NewReader(raw.Bytes())}
			got, err := fetchImage(context.Background(), frameLimitClient(body), "https://frame.example/image.png")
			if tc.wantError {
				if err == nil || got != nil {
					t.Fatalf("accepted %dx%d source: image=%v err=%v", tc.width, tc.height, got != nil, err)
				}
			} else if err != nil || got == nil || got.Bounds().Dx() != tc.width || got.Bounds().Dy() != tc.height {
				t.Fatalf("normal source failed: image=%v err=%v", got != nil, err)
			}
			if !body.closed {
				t.Fatal("response body stayed open")
			}
		})
	}
}

// Trailing bytes count toward the transfer limit even when an image
// decoder could finish before reading them.
func TestFetchImageTransferLimit(t *testing.T) {
	const limit = 8 << 20
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{limit, limit + 1, limit + 4096} {
		body := &frameLimitBody{Reader: io.MultiReader(bytes.NewReader(raw.Bytes()), io.LimitReader(frameZeroes{}, int64(size-raw.Len())))}
		got, err := fetchImage(context.Background(), frameLimitClient(body), "https://frame.example/image.png")
		if size == limit && (err != nil || got == nil) {
			t.Fatalf("exact transfer boundary refused: %v", err)
		}
		if size > limit && (err == nil || got != nil) {
			t.Fatalf("accepted %d-byte response", size)
		}
		if body.read > limit+1 || !body.closed {
			t.Fatalf("read=%d closed=%v", body.read, body.closed)
		}
	}
}
