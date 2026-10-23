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
	Sensitive                                                                bool
	Media                                                                    []Media
	Sections                                                                 []Section
}
type Media struct {
	URL, Alt  string
	Sensitive bool
}
type Section struct {
	Title, Text, URL string
	Media            []Media
	Sensitive        bool
}

// Resolver may be shared with another module through constructor injection.
// Credentials belong to the provider, never to a card or another module.
type Resolver interface {
	Resolve(context.Context, Link) (Post, error)
}

type Config struct{ Twitter, Bluesky, FixEmbed string }

func FromEnv() Config {
	return Config{Twitter: os.Getenv("SKUA_FIX_TWITTER_API"), Bluesky: os.Getenv("SKUA_FIX_BLUESKY_API"), FixEmbed: os.Getenv("SKUA_FIX_API")}
}

// API uses maintained metadata services, not their HTML or Discord rendering.
// Endpoints can point at self-hosted services; no platform secret is required.
type API struct {
	c   *http.Client
	cfg Config
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
	return &API{cfg: cfg, c: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

const responseLimit = 2 << 20

var errMetadata = errors.New("fix: metadata unavailable")

func (a *API) get(ctx context.Context, endpoint string, out any) error {
	start := time.Now()
	defer func() { obs.Default.Add("fix source", obs.HTTP, time.Since(start)) }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errMetadata
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "skua/fix (+https://github.com/6586x57890143/skua)")
	resp, err := a.c.Do(req)
	if err != nil {
		return errMetadata
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errMetadata
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil || len(raw) > responseLimit {
		return errMetadata
	}
	if json.Unmarshal(raw, out) != nil {
		return errMetadata
	}
	return nil
}

func (a *API) Resolve(ctx context.Context, l Link) (Post, error) {
	// Revalidate at the boundary, even for callers outside the Discord module.
	checked, ok := Parse(l.URL)
	if !ok || checked != l {
		return Post{}, errMetadata
	}
	if l.Platform == "twitter" || l.Platform == "bluesky" {
		first, cancel := context.WithTimeout(ctx, 6*time.Second)
		p, err := a.fx(first, l)
		cancel()
		if err == nil {
			return p, nil
		}
		if ctx.Err() != nil {
			return Post{}, errMetadata
		}
	}
	return a.fix(ctx, l)
}

type fixData struct {
	SourceURL    string   `json:"url"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Caption      string   `json:"caption"`
	Author       string   `json:"authorName"`
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
}

func (d fixData) media() []Media {
	var m []Media
	if d.Video != nil {
		m = append(m, Media{URL: d.Video.URL})
	}
	for _, u := range d.Images {
		m = append(m, Media{URL: u})
	}
	if len(m) == 0 && d.Image != "" {
		m = append(m, Media{URL: d.Image})
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
	var body struct {
		Success  bool     `json:"success"`
		Platform string   `json:"platform"`
		Data     *fixData `json:"data"`
	}
	endpoint := a.cfg.FixEmbed + "/api/embed?" + url.Values{"url": {l.URL}, "renderer": {"components-v2"}}.Encode()
	if err := a.get(ctx, endpoint, &body); err != nil {
		return Post{}, err
	}
	if !body.Success || body.Platform != l.Platform || body.Data == nil {
		return Post{}, errMetadata
	}
	d := body.Data
	text := d.Caption
	if text == "" {
		text = d.Description
	}
	p := Post{Title: d.Title, Text: text, Author: d.Author, Handle: d.Handle, Avatar: d.Avatar, Stats: d.Stats, Context: d.Context, Timestamp: d.Timestamp, Provider: "fixembed", Sensitive: d.Sensitive || len(d.Sensitivity) > 0}
	if d.MediaOrigin != "quote" {
		p.Media = d.media()
	}
	for i, s := range d.Sections {
		if i == 6 {
			break
		}
		title := s.Title
		if s.Kind == "quote" && s.Author != "" {
			title = "quote · " + s.Author
		}
		p.Sections = append(p.Sections, Section{Title: title, Text: s.Body, URL: s.URL, Media: s.media(), Sensitive: p.Sensitive || s.Sensitive || len(s.Sensitivity) > 0})
	}
	if p.Text == "" && p.Title == "" && len(p.Media) == 0 {
		return Post{}, errMetadata
	}
	if l.Platform == "reddit" {
		a.reddit(ctx, l, d, &p)
	}
	return p, nil
}
