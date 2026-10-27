package fix

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Wire shapes follow FxEmbed API v2 at the revision in upstreams.json.
// Only the fields used by skua are decoded; additive upstream fields are safe.
type fxStatus struct {
	ID          string         `json:"id"`
	Text        string         `json:"text"`
	Translation *fxTranslation `json:"translation"`
	Timestamp   float64        `json:"created_timestamp"`
	Sensitive   bool           `json:"possibly_sensitive"`
	Type        string         `json:"type"`
	Message     string         `json:"message"`
	URL         string         `json:"url"`
	Author      struct {
		Name   string `json:"name"`
		Handle string `json:"screen_name"`
		Avatar string `json:"avatar_url"`
		URL    string `json:"url"`
	} `json:"author"`
	Views   *int64 `json:"views"`
	Likes   int64  `json:"likes"`
	Reposts int64  `json:"reposts"`
	Replies int64  `json:"replies"`
	Media   struct {
		All    []fxMedia `json:"all"`
		Photos []fxMedia `json:"photos"`
		Videos []fxMedia `json:"videos"`
	} `json:"media"`
	Quote *fxStatus `json:"quote"`
	Poll  *struct {
		Choices []struct {
			Label      string  `json:"label"`
			Count      int64   `json:"count"`
			Percentage float64 `json:"percentage"`
		} `json:"choices"`
		Votes int64 `json:"total_votes"`
	} `json:"poll"`
	Note *struct {
		Text string `json:"text"`
	} `json:"community_note"`
}

type fxTranslation struct {
	Text   string `json:"text"`
	Source string `json:"source_lang"`
	Target string `json:"target_lang"`
}

func (s fxStatus) translatedText() (string, string) {
	if t := s.Translation; t != nil {
		source := translationSource(t.Source, t.Target)
		if source != "" && strings.TrimSpace(t.Text) != "" && strings.TrimSpace(t.Text) != strings.TrimSpace(s.Text) {
			return t.Text, source
		}
	}
	return s.Text, ""
}

type fxMedia struct {
	URL     string `json:"url"`
	Type    string `json:"type"`
	Alt     string `json:"altText"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Formats []struct {
		URL       string `json:"url"`
		Container string `json:"container"`
		Bitrate   int64  `json:"bitrate"`
	} `json:"formats"`
}

func (s fxStatus) media() []Media {
	all := s.Media.All
	if len(all) == 0 {
		all = append(append([]fxMedia{}, s.Media.Photos...), s.Media.Videos...)
	}
	var out []Media
	for _, m := range all {
		u := m.URL
		if m.Type == "video" || m.Type == "gif" {
			best := int64(-1)
			for _, f := range m.Formats {
				if f.Container == "mp4" && f.Bitrate > best {
					u = f.URL
					best = f.Bitrate
				}
			}
		}
		out = append(out, Media{URL: u, Alt: m.Alt, Type: m.Type, Width: m.Width, Height: m.Height, Sensitive: s.Sensitive})
	}
	return out
}
func (a *API) fx(ctx context.Context, l Link) (Post, error) {
	endpoint := a.cfg.Twitter + "/2/status/" + l.ID
	if l.Platform == "bluesky" {
		endpoint = a.cfg.Bluesky + "/2/status/" + url.PathEscape(l.Account) + "/" + l.ID
	}
	// API v2 uses lang=en for the same translation requested by /en on embed URLs.
	// A suffix here would select the legacy tweet envelope instead of status.
	endpoint += "?lang=en"
	var body struct {
		Code   int       `json:"code"`
		Status *fxStatus `json:"status"`
	}
	if err := a.get(ctx, endpoint, &body); err != nil {
		return Post{}, err
	}
	s := body.Status
	if body.Code != 200 || s == nil || s.ID != l.ID || (s.Text == "" && len(s.media()) == 0 && s.Poll == nil) {
		return Post{}, errMetadata
	}
	p := Post{Text: s.Text, Author: s.Author.Name, AuthorURL: s.Author.URL, Handle: s.Author.Handle, Avatar: s.Author.Avatar, Provider: "fxembed", Sensitive: s.Sensitive, Media: s.media(), Stats: fmt.Sprintf("%d likes · %d reposts · %d replies", s.Likes, s.Reposts, s.Replies)}
	p.Text, p.TranslatedFrom = s.translatedText()
	if s.Views != nil && *s.Views >= 0 {
		p.Stats += fmt.Sprintf(" · %d views", *s.Views)
	}
	if s.Timestamp > 0 {
		p.Timestamp = time.UnixMilli(int64(s.Timestamp * 1000)).UTC().Format(time.RFC3339)
	}
	if s.Quote != nil {
		q := s.Quote
		title := "quote · " + q.Author.Name
		text, translatedFrom := q.translatedText()
		if q.Type == "tombstone" {
			text, translatedFrom = q.Text, ""
			title = "quote unavailable"
			if text == "" {
				text = q.Message
				if text == "" {
					text = "the quoted post is unavailable"
				}
			}
		}
		p.Sections = append(p.Sections, Section{Kind: "quote", Author: q.Author.Name, Handle: q.Author.Handle, AuthorURL: q.Author.URL, Title: title, Text: text, TranslatedFrom: translatedFrom, URL: q.URL, Media: q.media(), Sensitive: s.Sensitive || q.Sensitive})
	}
	if s.Poll != nil {
		text := ""
		for i, c := range s.Poll.Choices {
			if i == 4 {
				break
			}
			text += fmt.Sprintf("%s · %d votes · %.1f%%\n", c.Label, c.Count, c.Percentage)
		}
		p.Sections = append(p.Sections, Section{Kind: "poll", Title: "poll", Text: text})
	}
	if s.Note != nil && s.Note.Text != "" {
		p.Sections = append(p.Sections, Section{Kind: "community-note", Title: "community note", Text: s.Note.Text})
	}
	return p, nil
}
