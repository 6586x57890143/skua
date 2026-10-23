package fix

import (
	"context"
	"strings"
	"time"
)

// Resolve one known external post through the maintained adapters. Never follow
// arbitrary article URLs or recurse through Reddit crossposts. The existing
// overall fetch deadline still bounds this additional four-second request.
func (a *API) redditLinked(ctx context.Context, d *fixData, p *Post) {
	if d.Video != nil || len(d.Images) > 0 || d.MediaOrigin == "quote" || redditComment(d.SourceURL) {
		return
	}
	for i, s := range p.Sections {
		if s.Kind != "link-card" {
			continue
		}
		l, ok := Parse(s.URL)
		if !ok || l.Platform == "reddit" {
			continue
		}
		childCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		child, err := a.Resolve(childCtx, l)
		cancel()
		if err != nil || child.Incomplete || len(child.Media) == 0 {
			p.Incomplete = true
			p.Sections[i].Title = "view linked post"
			return
		}
		media := make([]Media, 0, len(child.Media))
		for _, m := range child.Media {
			if remoteURL(m.URL) == "" {
				continue
			}
			m.Sensitive = m.Sensitive || p.Sensitive || s.Sensitive || child.Sensitive
			media = append(media, m)
		}
		if len(media) == 0 {
			p.Incomplete = true
			return
		}
		// The Reddit image here is the link thumbnail, superseded by resolved media.
		p.Media = media
		p.Sensitive = p.Sensitive || s.Sensitive || child.Sensitive
		p.Sections[i] = Section{Kind: "linked-post", Title: "clip on " + platformName(l), URL: l.URL}
		if l.Platform != "twitch" && l.Platform != "kick" {
			p.Sections[i].Title = "post on " + platformName(l)
		}
		if child.Provider != "" && !strings.Contains(p.Provider, child.Provider) {
			p.Provider += " + " + child.Provider
		}
		return
	}
}
