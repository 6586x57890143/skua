package fix

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/6586x57890143/skua/internal/obs"
	"golang.org/x/net/html"
)

const instagramDirectBudget = 1500 * time.Millisecond
const instagramHTMLLimit = 1 << 20

var instagramHandle = regexp.MustCompile(`^[A-Za-z0-9._]{1,30}$`)
var instagramDescription = regexp.MustCompile(`(?s)(?:^| - )([A-Za-z0-9._]{1,30}) on [^:]{1,100}: ["\x{201c}](.*)["\x{201d}]\.?$`)
var instagramTitle = regexp.MustCompile(`(?s)^(.+?) on Instagram:\s*["\x{201c}](.*)["\x{201d}]\.?$`)

func instagramCode(raw string) string {
	l, ok := Parse(raw)
	if !ok || l.Platform != "instagram" {
		return ""
	}
	u, _ := url.Parse(l.URL)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 { // share URLs must be resolved by the provider first
		return ""
	}
	return parts[1]
}

// One independent, anonymous request, inside the metadata budget. Never follow
// login redirects or fetch URLs discovered in HTML; only enrich an existing
// provider result, so an HTML poster cannot replace its playable video.
func (a *API) instagramDirect(ctx context.Context, l Link, existing *fixData) *fixData {
	code := instagramCode(l.URL)
	if code == "" || ctx.Err() != nil {
		return existing
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < metadataReserve {
		return existing
	}
	ctx, cancel := context.WithTimeout(ctx, instagramDirectBudget)
	defer cancel()
	start := time.Now()
	defer func() { obs.Default.Add("fix instagram direct", obs.HTTP, time.Since(start)) }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.instagram.com/p/"+code+"/", nil)
	if err != nil {
		return existing
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)")
	resp, err := a.c.Do(req)
	if err != nil {
		return existing
	}
	defer func() { _ = resp.Body.Close() }()
	mediaType, _, typeErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || typeErr != nil || mediaType != "text/html" || resp.ContentLength > instagramHTMLLimit {
		return existing
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, instagramHTMLLimit+1))
	if err != nil || len(raw) > instagramHTMLLimit || ctx.Err() != nil {
		return existing
	}
	details := instagramMetadata(raw, code)
	if details == nil {
		return existing
	}
	// Direct lookup contributes identity and caption only, never source media,
	// sensitivity, timestamps or availability claims from a login shell.
	result := *existing
	if strings.TrimSpace(result.Author) == "" {
		result.Author = details.Author
	}
	if strings.TrimSpace(result.Handle) == "" {
		result.Handle = details.Handle
	}
	if result.AuthorURL == "" {
		result.AuthorURL = details.AuthorURL
	}
	if result.Caption == "" && result.Description == "" {
		result.Caption = details.Caption
	}
	result.Direct = result.Author != existing.Author || result.Handle != existing.Handle || result.AuthorURL != existing.AuthorURL || result.Caption != existing.Caption
	return &result
}

// Tokenization ignores fake tags inside script strings and handles reordered
// attributes and HTML entities without building a DOM for a potentially large
// page. A matching public-post URL is required before accepting any metadata.
func instagramMetadata(raw []byte, code string) *fixData {
	z := html.NewTokenizer(bytes.NewReader(raw))
	z.SetMaxBuf(instagramHTMLLimit)
	var source, title, description, owner string
	var structured *fixData
	jsonScript := false
	scripts := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return nil
			}
			goto parsed
		case html.StartTagToken, html.SelfClosingTagToken:
			name, attrs := z.TagName()
			tag := string(name)
			if tag != "meta" && tag != "script" && tag != "form" {
				continue
			}
			var key, content, scriptType, action string
			for attrs {
				var attr, value []byte
				attr, value, attrs = z.TagAttr()
				switch string(attr) {
				case "property", "name":
					key = strings.ToLower(string(value))
				case "content":
					content = string(value)
				case "type":
					scriptType = string(value)
				case "action":
					action = string(value)
				}
			}
			if tag == "form" && (strings.Contains(action, "/accounts/login") || strings.Contains(action, "/challenge/") || strings.Contains(action, "/checkpoint/")) {
				return nil
			}
			switch tag {
			case "script":
				jsonScript = strings.EqualFold(scriptType, "application/ld+json") && scripts < 8
			case "meta":
				switch key {
				case "og:url":
					source = content
				case "og:title":
					title = content
				case "og:description":
					description = content
				case "instapp:owner_user_name":
					owner = content
				}
			}
		case html.TextToken:
			if jsonScript {
				scripts++
				if d := instagramJSON(z.Text(), code); d != nil && structured == nil {
					structured = d
				}
				jsonScript = false
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "script" {
				jsonScript = false
			}
		}
	}
parsed:
	if instagramCode(source) != code {
		// JSON-LD can independently identify the exact post, even without OG.
		if source != "" || structured == nil {
			return nil
		}
		return structured
	}
	d := &fixData{}
	if instagramHandle.MatchString(owner) {
		d.Handle = owner
	}
	if match := instagramDescription.FindStringSubmatch(description); match != nil {
		if d.Handle == "" {
			d.Handle = match[1]
		}
		d.Caption = match[2]
	}
	if match := instagramTitle.FindStringSubmatch(title); match != nil {
		d.Author = match[1]
		if d.Caption == "" {
			d.Caption = match[2]
		}
	}
	if d.Handle != "" {
		d.AuthorURL = "https://www.instagram.com/" + d.Handle + "/"
		if d.Author == "" {
			d.Author = d.Handle
		}
	}
	if structured != nil {
		d = mergeInstagram(d, structured)
	}
	if d.Author == "" && d.Handle == "" && d.Caption == "" {
		return nil
	}
	return d
}

func instagramJSON(raw []byte, code string) *fixData {
	var data struct {
		Type        string `json:"@type"`
		URL         string `json:"url"`
		Caption     string `json:"caption"`
		Description string `json:"description"`
		Author      struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"author"`
	}
	if json.Unmarshal(raw, &data) != nil || instagramCode(data.URL) != code {
		return nil
	}
	if data.Type != "SocialMediaPosting" && data.Type != "VideoObject" && data.Type != "ImageObject" {
		return nil
	}
	profile, err := url.Parse(data.Author.URL)
	if err != nil || profile.Scheme != "https" || profile.User != nil || (profile.Host != "www.instagram.com" && profile.Host != "instagram.com") || profile.RawQuery != "" || profile.Fragment != "" {
		return nil
	}
	handle := strings.Trim(profile.Path, "/")
	if !instagramHandle.MatchString(handle) || handle == "accounts" || handle == "explore" {
		return nil
	}
	caption := data.Caption
	if caption == "" {
		caption = data.Description
	}
	return &fixData{Author: data.Author.Name, Handle: handle, AuthorURL: "https://www.instagram.com/" + handle + "/", Caption: caption}
}
