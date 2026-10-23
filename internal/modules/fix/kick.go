package fix

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

type kickData struct {
	ID, Title, Privacy string
	ClipURL            string `json:"clip_url"`
	VideoURL           string `json:"video_url"`
	PlaybackURL        string `json:"playback_url"`
	Timestamp          string `json:"created_at"`
	Sensitive          bool   `json:"is_mature"`
	Views, Likes       int64
	Channel            struct{ Username, Slug string }
}

func (a *API) kickMetadata(ctx context.Context, id string) (*kickData, error) {
	var body struct{ Clip *kickData }
	if err := a.get(ctx, "https://kick.com/api/v2/clips/"+id, &body); err != nil {
		return nil, err
	}
	if body.Clip == nil || body.Clip.ID != id || body.Clip.Privacy != "public" {
		return nil, errMetadata
	}
	return body.Clip, nil
}
func (a *API) kick(ctx context.Context, l Link) (Post, error) {
	d, err := a.kickMetadata(ctx, l.ID)
	if err != nil {
		return Post{}, err
	}
	var media string
	for _, candidate := range []string{d.PlaybackURL, d.ClipURL, d.VideoURL} {
		if kickMediaURL(candidate) {
			media = candidate
			break
		}
	}
	if media == "" {
		return Post{}, errMetadata
	}
	u, _ := url.Parse(media)
	if strings.HasSuffix(u.Path, ".m3u8") {
		if a.media == nil {
			return Post{}, errMetadata
		}
		media = a.media.link(l.ID)
	}
	author := d.Channel.Username
	if author == "" {
		author = l.Account
	}
	return Post{Title: d.Title, Author: author, Handle: d.Channel.Slug, AuthorURL: "https://kick.com/" + l.Account, Timestamp: d.Timestamp, Stats: strconv.FormatInt(max(0, d.Likes), 10) + " likes · " + strconv.FormatInt(max(0, d.Views), 10) + " views", Provider: "kick", Sensitive: d.Sensitive, Media: []Media{{URL: media, Sensitive: d.Sensitive}}}, nil
}

// Only Kick's own clip CDN is fetched by the media relay. Redirects remain off.
func kickMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "clips.kick.com" && u.User == nil && u.Fragment == "" && strings.HasPrefix(u.Path, "/clips/") && remoteURL(raw) != "" && (strings.HasSuffix(u.Path, ".mp4") || strings.HasSuffix(u.Path, ".m3u8"))
}
