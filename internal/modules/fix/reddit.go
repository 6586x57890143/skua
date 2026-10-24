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
		if redditComment(l.URL) || redditComment(d.SourceURL) {
			p.Incomplete = true
			return
		}
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
			p.Context += "\nmedia couldn't be checked; follow the post title to view the original"
		} else {
			p.Context += "\nvideo preview may be silent; follow the post title for audio"
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
	place := func(media string) {
		if missing {
			p.Media = append(p.Media, Media{URL: media, Sensitive: p.Sensitive})
			return
		}
		for i := range p.Media {
			if p.Media[i].URL == d.Video.URL {
				p.Media[i].URL = media
			}
		}
	}
	page := a.redditEmbed(ctx, path[1], path[3])
	if muxed := redditMuxed(page); muxed != "" {
		place(muxed)
		return
	}
	if id := redditVideoIn(d, page); id != "" {
		if media, silent, err := a.redditVideo(ctx, id); err == nil {
			place(media)
			if silent {
				p.Context += "\nvideo preview may be silent; follow the post title for audio"
			}
			return
		}
	}
	if missing && page != "" {
		p.Incomplete = true // HTML alone cannot prove a text-only source.
		return
	}
	unavailable()
}

// redditEmbed is Reddit's own embed page, or empty when it can't be read.
func (a *API) redditEmbed(ctx context.Context, subreddit, post string) string {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	endpoint := "https://embed.reddit.com/r/" + url.PathEscape(subreddit) + "/comments/" + url.PathEscape(post) + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)")
	resp, err := a.c.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024+1))
	if err != nil || len(raw) > 512*1024 {
		return ""
	}
	return string(raw)
}

var redditPlaylist = regexp.MustCompile(`https://v\.redd\.it/([a-z0-9]{6,20})/HLSPlaylist\.m3u8`)

func redditVideoIn(d *fixData, page string) string {
	if d.Video != nil {
		if u, err := url.Parse(d.Video.URL); err == nil && u.Host == "v.redd.it" {
			if id, _, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/"); redditVideoID.MatchString(id) {
				return id
			}
		}
	}
	if m := redditPlaylist.FindStringSubmatch(page); m != nil {
		return m[1]
	}
	return ""
}

// redditVideo returns a playable URL for a CMAF upload: the relay's muxed
// file, or the video track alone (silent) when the relay can't serve it.
func (a *API) redditVideo(ctx context.Context, id string) (media string, silent bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	video, audio, err := a.redditRenditions(ctx, id)
	if err != nil || audio == "" {
		return video, false, err
	}
	if a.media == nil {
		return video, true, nil
	}
	if _, err := a.media.ready(ctx, "reddit/"+id, func(ctx context.Context) ([]byte, error) { return a.redditMux(ctx, id) }); err != nil {
		return video, true, nil
	}
	return a.media.link("reddit", id), false, nil
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

// A comment is not its parent post. Never recover the parent video for a
// comment URL whose own metadata intentionally has no media.
func redditComment(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Query().Get("comment") != "" {
		return true
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	return (len(parts) > 5 && parts[0] == "r" && parts[2] == "comments") || (len(parts) > 3 && parts[0] == "comments")
}

var redditUserPath = regexp.MustCompile(`^/user/[A-Za-z0-9_-]+/?$`)
var redditUserName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func redditProfile(p Post) string {
	if u, err := url.Parse(p.AuthorURL); err == nil && u.Scheme == "https" && u.User == nil && (u.Host == "reddit.com" || u.Host == "www.reddit.com") && len(u.Path) <= 128 && redditUserPath.MatchString(u.Path) {
		return "https://www.reddit.com" + u.Path
	}
	for _, name := range []string{p.Handle, p.Author} {
		if len(name) <= 66 && strings.HasPrefix(name, "u/") && redditUserName.MatchString(name[2:]) {
			return "https://www.reddit.com/user/" + name[2:] + "/"
		}
	}
	return ""
}
