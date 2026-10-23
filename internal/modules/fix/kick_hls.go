package fix

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const mediaLimit = 64 << 20

type clipSegment struct {
	URL          string
	Offset, Size int64
}

// Only finite, unencrypted transport-stream playlists on the exact clip CDN
// directory are admitted. A playlist cannot turn this into an arbitrary proxy.
func clipSegments(base string, raw []byte) ([]clipSegment, error) {
	if len(raw) > 64<<10 || !bytes.HasPrefix(raw, []byte("#EXTM3U")) || !bytes.Contains(raw, []byte("#EXT-X-ENDLIST")) {
		return nil, errMetadata
	}
	u, _ := url.Parse(base)
	if u == nil || !kickMediaURL(base) {
		return nil, errMetadata
	}
	directory := u.Path[:strings.LastIndex(u.Path, "/")+1]
	var out []clipSegment
	var size, offset, total int64
	duration := 0.0
	lastURL := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#EXT-X-KEY:"), strings.HasPrefix(line, "#EXT-X-MAP:"), strings.HasPrefix(line, "#EXT-X-STREAM-INF:"), strings.HasPrefix(line, "#EXT-X-DISCONTINUITY"):
			return nil, errMetadata
		case strings.HasPrefix(line, "#EXTINF:"):
			n, e := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","), 64)
			if e != nil || n <= 0 || n > 20 {
				return nil, errMetadata
			}
			duration += n
		case strings.HasPrefix(line, "#EXT-X-BYTERANGE:"):
			p := strings.Split(strings.TrimPrefix(line, "#EXT-X-BYTERANGE:"), "@")
			var e error
			size, e = strconv.ParseInt(p[0], 10, 64)
			if e != nil || size <= 0 || size > mediaLimit {
				return nil, errMetadata
			}
			offset = -1
			if len(p) == 2 {
				offset, e = strconv.ParseInt(p[1], 10, 64)
				if e != nil || offset < 0 || offset > mediaLimit {
					return nil, errMetadata
				}
			} else if len(p) != 1 {
				return nil, errMetadata
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			r, e := url.Parse(line)
			if e != nil {
				return nil, errMetadata
			}
			dest := u.ResolveReference(r)
			if dest.Scheme != "https" || dest.Host != "clips.kick.com" || dest.User != nil || dest.RawQuery != "" || dest.Fragment != "" || !strings.HasPrefix(dest.Path, directory) || strings.Contains(strings.TrimPrefix(dest.Path, directory), "/") || !strings.HasSuffix(dest.Path, ".ts") {
				return nil, errMetadata
			}
			if offset == -1 {
				if dest.String() != lastURL || len(out) == 0 {
					return nil, errMetadata
				}
				prev := out[len(out)-1]
				offset = prev.Offset + prev.Size
			}
			if size > 0 {
				total += size
			}
			if total > mediaLimit || offset+size > mediaLimit || len(out) >= 128 {
				return nil, errMetadata
			}
			out = append(out, clipSegment{dest.String(), offset, size})
			lastURL = dest.String()
			size, offset = 0, 0
		}
	}
	if len(out) == 0 || duration <= 0 || duration > 181 || size != 0 {
		return nil, errMetadata
	}
	return out, nil
}

func (a *API) clipBytes(ctx context.Context, raw string, offset, size, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errMetadata
	}
	req.Header.Set("User-Agent", "skua/fix")
	if size > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+size-1))
	}
	resp, err := a.c.Do(req)
	if err != nil {
		return nil, errMetadata
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, errMetadata
	}
	if resp.StatusCode == http.StatusPartialContent {
		prefix := fmt.Sprintf("bytes %d-%d/", offset, offset+size-1)
		if size == 0 || !strings.HasPrefix(resp.Header.Get("Content-Range"), prefix) {
			return nil, errMetadata
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errMetadata
	}
	if size > 0 {
		if resp.StatusCode == http.StatusOK {
			if offset+size > int64(len(data)) {
				return nil, errMetadata
			}
			data = data[offset : offset+size]
		}
		if int64(len(data)) != size {
			return nil, errMetadata
		}
	}
	return data, nil
}

func (a *API) clipTransport(ctx context.Context, media string) (io.Reader, error) {
	raw, err := a.clipBytes(ctx, media, 0, 0, 64<<10)
	if err != nil {
		return nil, err
	}
	segments, err := clipSegments(media, raw)
	if err != nil {
		return nil, err
	}
	// Coalesce adjacent byte ranges into one request per transport-stream file.
	var readers []io.Reader
	total := 0
	for i := 0; i < len(segments); {
		s := segments[i]
		j := i + 1
		end := s.Offset + s.Size
		for s.Size > 0 && j < len(segments) && segments[j].URL == s.URL && segments[j].Offset == end && segments[j].Size > 0 {
			end += segments[j].Size
			j++
		}
		data, e := a.clipBytes(ctx, s.URL, s.Offset, end-s.Offset, int64(mediaLimit-total))
		if e != nil {
			return nil, e
		}
		total += len(data)
		readers = append(readers, bytes.NewReader(data))
		i = j
	}
	return io.MultiReader(readers...), nil
}
