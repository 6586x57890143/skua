package fix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/6586x57890143/skua/internal/obs"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Post is independent of any provider's wire format. Source-authored text keeps
// its case; skua's labels and controls follow the suite's lowercase voice.
type Post struct {
	Title, Text, Author, Handle, Avatar, Stats, Context, Timestamp, Provider string
	AuthorURL                                                                string
	TranslatedFrom                                                           string // source language, only after a successful English translation
	Sensitive                                                                bool
	Incomplete                                                               bool // metadata could not establish whether source media is missing
	Media                                                                    []Media
	Sections                                                                 []Section
}
type Media struct {
	URL, Alt, Type string
	Width, Height  int
	Sensitive      bool
}
type Section struct {
	Kind, Author, Handle, AuthorURL string
	Title, Text, URL                string
	TranslatedFrom                  string
	Media                           []Media
	Sensitive                       bool
}

// Resolver may be shared with another module through constructor injection.
// Credentials belong to the provider, never to a card or another module.
type Resolver interface {
	Resolve(context.Context, Link) (Post, error)
}

type Config struct{ Twitter, Bluesky, FixEmbed, MediaURL, MediaSecret string }

func FromEnv() Config {
	return Config{Twitter: os.Getenv("SKUA_FIX_TWITTER_API"), Bluesky: os.Getenv("SKUA_FIX_BLUESKY_API"), FixEmbed: os.Getenv("SKUA_FIX_API"), MediaURL: os.Getenv("SKUA_HOOKS_URL"), MediaSecret: os.Getenv("SKUA_HOOKS_SECRET")}
}

// API uses maintained metadata services, not their HTML or Discord rendering.
// Endpoints can point at self-hosted services; no platform secret is required.
type API struct {
	c     *http.Client
	cfg   Config
	media *mediaRelay
}

func NewAPI(cfg Config) (*API, error) {
	targets()
	defaults := []string{"https://api.fxtwitter.com", "https://api.fxbsky.app", "https://fixembed.app"}
	fields := []*string{&cfg.Twitter, &cfg.Bluesky, &cfg.FixEmbed}
	for i, p := range fields {
		if *p == "" {
			*p = defaults[i]
		}
		*p = strings.TrimRight(*p, "/")
		u, err := url.Parse(*p)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("fix: endpoint %d must be an https base URL", i+1)
		}
	}
	a := &API{cfg: cfg, c: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	a.media = newMediaRelay(a, cfg.MediaURL, cfg.MediaSecret)
	return a, nil
}

const responseLimit = 2 << 20

var errMetadata = errors.New("fix: metadata unavailable")

var mediaOnly = map[string]bool{"instagram": true, "tiktok": true, "pixiv": true, "pinterest": true, "deviantart": true}

func (a *API) get(ctx context.Context, endpoint string, out any) error {
	start := time.Now()
	defer func() { obs.Default.Add("fix source", obs.HTTP, time.Since(start)) }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &metadataFailure{reason: "invalid endpoint"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "skua/fix (+https://github.com/6586x57890143/skua)")
	resp, err := a.c.Do(req)
	if err != nil {
		reason := "transport failed"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "request timed out"
		}
		return &metadataFailure{reason: reason, retry: transientTransport(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
		after := retryAfter(resp.Header.Get("Retry-After"))
		if resp.StatusCode == http.StatusTooManyRequests {
			after = max(after, time.Second)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &metadataFailure{reason: "request failed", status: resp.StatusCode, retry: retry, after: after}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil {
		return &metadataFailure{reason: "response read failed", retry: transientTransport(err)}
	}
	if len(raw) > responseLimit {
		return &metadataFailure{reason: "response too large"}
	}
	if json.Unmarshal(raw, out) != nil {
		return &metadataFailure{reason: "invalid json"}
	}
	return nil
}

func (a *API) Resolve(ctx context.Context, l Link) (Post, error) {
	// Revalidate at the boundary, even for callers outside the Discord module.
	checked, ok := Parse(l.URL)
	if !ok || checked != l {
		return Post{}, errMetadata
	}
	if l.Platform == "kick" {
		p, err := a.kick(ctx, l)
		return a.prepareGalleries(ctx, p, l.Platform), err
	}
	if l.Platform == "twitter" || l.Platform == "bluesky" {
		first, cancel := context.WithTimeout(ctx, 6*time.Second)
		p, err := a.fx(first, l)
		cancel()
		if err == nil {
			return a.prepareGalleries(ctx, p, l.Platform), nil
		}
		if ctx.Err() != nil {
			return Post{}, errMetadata
		}
	}
	p, err := a.fix(ctx, l)
	return a.prepareGalleries(ctx, p, l.Platform), err
}

type fixData struct {
	SourceURL    string   `json:"url"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Caption      string   `json:"caption"`
	Author       string   `json:"authorName"`
	AuthorURL    string   `json:"authorUrl"`
	Handle       string   `json:"authorHandle"`
	Avatar       string   `json:"authorAvatar"`
	Stats        string   `json:"stats"`
	Context      string   `json:"context"`
	Timestamp    string   `json:"timestamp"`
	Sensitive    bool     `json:"sensitive"`
	Sensitivity  []string `json:"sensitivityTypes"`
	Images       []string `json:"images"`
	Image        string   `json:"image"`
	Descriptions []string `json:"mediaDescriptions"`
	Video        *struct {
		URL       string `json:"url"`
		MediaType string `json:"mediaType"`
	} `json:"video"`
	Sections []struct {
		fixData
		Kind string `json:"kind"`
		Body string `json:"body"`
		URL  string `json:"url"`
	} `json:"sections"`
	MediaOrigin string `json:"mediaOrigin"`
	Direct      bool   `json:"-"` // identity or caption recovered directly from Instagram
	Translation *struct {
		Source string `json:"sourceLanguage"`
		Target string `json:"targetLanguage"`
	} `json:"translation"`
}

// Discord identifies video media from the URL extension. FxEmbed's Instagram
// video proxy URL has no extension, so it goes out as a relay link that ends
// in .mp4; without a relay, the poster image stands in for it.
func (a *API) instagramMediaPreview(l Link, d *fixData, media []Media) []Media {
	if d.Video == nil {
		return media
	}
	u, err := url.Parse(d.Video.URL)
	if err != nil {
		return media
	}
	videoPath := strings.ToLower(u.Path)
	if strings.HasSuffix(videoPath, ".mp4") || strings.HasSuffix(videoPath, ".webm") || strings.HasSuffix(videoPath, ".mov") {
		return media
	}
	if !trustedInstagramVideoURL(d.Video.URL, a.cfg.FixEmbed) {
		return media
	}
	if a.media != nil {
		relayed := a.media.instagramPostLink(l.URL)
		if len(media) > 0 {
			media[0].URL = relayed
			return media
		}
		return []Media{{URL: relayed, Type: "video"}}
	}
	if d.Image == "" {
		return media
	}
	poster := Media{URL: d.Image, Type: "photo", Sensitive: d.Sensitive || len(d.Sensitivity) > 0}
	if len(media) > 0 {
		poster.Alt = media[0].Alt
		media[0] = poster
		return media
	}
	return []Media{poster}
}

func (d fixData) media() []Media {
	var m []Media
	if d.Video != nil {
		m = append(m, Media{URL: d.Video.URL, Type: "video"})
	}
	for _, u := range d.Images {
		m = append(m, Media{URL: u, Type: "photo"})
	}
	if len(m) == 0 && d.Image != "" {
		m = append(m, Media{URL: d.Image, Type: "photo"})
	}
	for i := range m {
		if i < len(d.Descriptions) {
			m[i].Alt = d.Descriptions[i]
		}
		m[i].Sensitive = d.Sensitive || len(d.Sensitivity) > 0
	}
	return m
}
func (a *API) fix(ctx context.Context, l Link) (Post, error) {
	d, err := a.fixMetadata(ctx, l)
	if err != nil {
		return Post{}, err
	}
	text := d.Caption
	if text == "" {
		text = d.Description
	}
	media := d.media()
	if l.Platform == "instagram" {
		media = a.instagramMediaPreview(l, d, media)
	}
	p := Post{Title: d.Title, Text: text, Author: d.Author, AuthorURL: d.AuthorURL, Handle: d.Handle, Avatar: d.Avatar, Stats: d.Stats, Context: d.Context, Timestamp: d.Timestamp, Provider: "fixembed", Sensitive: d.Sensitive || len(d.Sensitivity) > 0}
	if d.Translation != nil {
		p.TranslatedFrom = translationSource(d.Translation.Source, d.Translation.Target)
	}
	if d.Direct {
		p.Provider += " + instagram"
	}
	if d.MediaOrigin != "quote" {
		p.Media = media
	}
	for i, s := range d.Sections {
		if i == 6 {
			break
		}
		title := s.Title
		if s.Kind == "quote" && s.Author != "" {
			title = "quote · " + s.Author
		}
		p.Sections = append(p.Sections, Section{Kind: s.Kind, Author: s.Author, Handle: s.Handle, AuthorURL: s.AuthorURL, Title: title, Text: s.Body, URL: s.URL, Media: s.media(), Sensitive: p.Sensitive || s.Sensitive || len(s.Sensitivity) > 0})
	}
	if p.Text == "" && p.Title == "" && len(p.Media) == 0 {
		return Post{}, errMetadata
	}
	// These platforms never post without media, so a bare answer means the
	// source's own embed knows more than the card does.
	if len(p.Media) == 0 && mediaOnly[l.Platform] {
		p.Incomplete = true
	}
	if l.Platform == "instagram" && d.partialInstagram() {
		p.Incomplete = true
		if strings.EqualFold(p.Title, "post") || strings.EqualFold(p.Title, "reel") {
			p.Title = ""
		}
		if p.Context != "" {
			p.Context += "; "
		}
		p.Context += "some instagram details are unavailable; view the original post for the full preview"
	}
	if l.Platform == "reddit" {
		if redditComment(l.URL) || redditComment(d.SourceURL) {
			p.Incomplete = true
		} else {
			a.redditLinked(ctx, d, &p)
		}
		a.reddit(ctx, l, d, &p)
	}
	return p, nil
}
