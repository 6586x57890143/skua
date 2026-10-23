package fix

import (
	"context"
	"encoding/json"
	"github.com/6586x57890143/skua/internal/obs"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var redditSource = regexp.MustCompile(`"source"\s*:\s*\{`)

// Reddit's fallback MP4 is often video-only. Prefer Reddit's signed packaged
// rendition, which combines the tracks without a local transcoder or relay.
// Upstream currently assumes URL precedes dimensions in JSON; Reddit serves
// the reverse order too. Decode member names instead of matching their order.
func (a *API) reddit(ctx context.Context, l Link, d *fixData, p *Post) {
	missing := d.Video == nil && len(p.Media) == 0
	if missing {
		for _, s := range p.Sections {
			if len(s.Media) > 0 {
				return
			}
		}
	} else {
		if d.Video == nil || d.Video.MediaType == "gif" {
			return
		}
		video, err := url.Parse(d.Video.URL)
		if err != nil || video.Hostname() != "v.redd.it" {
			return
		}
	}
	unavailable := func() {
		if missing {
			p.Incomplete = true
			p.Context += "\nmedia couldn't be checked; follow the author link to view the original"
		} else {
			p.Context += "\nvideo preview may be silent; follow the author link for audio"
		}
	}
	original, ok := Parse(d.SourceURL)
	if !ok || original.Platform != "reddit" {
		original = l
	}
	u, _ := url.Parse(original.URL)
	path := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(path) < 4 || path[0] != "r" || path[2] != "comments" {
		unavailable()
		return
	}
	start := time.Now()
	defer func() { obs.Default.Add("fix reddit audio", obs.HTTP, time.Since(start)) }()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	endpoint := "https://embed.reddit.com/r/" + url.PathEscape(path[1]) + "/comments/" + url.PathEscape(path[3]) + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)")
	resp, err := a.c.Do(req)
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusOK && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 512*1024+1))
			if readErr == nil && len(raw) <= 512*1024 {
				if muxed := redditMuxed(string(raw)); muxed != "" {
					if missing {
						p.Media = append(p.Media, Media{URL: muxed, Sensitive: p.Sensitive})
						return
					}
					for i := range p.Media {
						if p.Media[i].URL == d.Video.URL {
							p.Media[i].URL = muxed
						}
					}
					return
				}
				if missing {
					if strings.Contains(string(raw), "<shreddit-player") {
						unavailable()
					}
					return
				} // A successfully read text-only post needs no media.
			}
		}
	}
	unavailable()
}

func redditMuxed(raw string) string {
	decoded := html.UnescapeString(raw)
	best := ""
	bestHeight := 0
	for _, loc := range redditSource.FindAllStringIndex(decoded, -1) {
		var s struct {
			URL        string                      `json:"url"`
			Dimensions struct{ Height, Width int } `json:"dimensions"`
		}
		if json.NewDecoder(strings.NewReader(decoded[loc[1]-1:])).Decode(&s) != nil {
			continue
		}
		u, err := url.Parse(s.URL)
		if err != nil || u.Scheme != "https" || u.Host != "packaged-media.redd.it" || u.User != nil || !strings.HasSuffix(u.Path, ".mp4") || s.Dimensions.Height <= 0 || s.Dimensions.Width <= 0 {
			continue
		}
		h := s.Dimensions.Height
		if best == "" || (h <= 720 && (bestHeight > 720 || h > bestHeight)) || (h > 720 && bestHeight > 720 && h < bestHeight) {
			best = s.URL
			bestHeight = h
		}
	}
	return best
}
