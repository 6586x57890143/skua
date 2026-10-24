// Package fix turns supported social links into skua's Components V2 cards.
package fix

import (
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// Link is a validated post URL. Tracking queries and fragments are removed; Reddit comment IDs keep their meaning.
type Link struct{ Platform, URL, ID, Account string }

var (
	kickAccount    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	kickClipID     = regexp.MustCompile(`^clip_[A-Za-z0-9_-]{1,96}$`)
	platformHosts  = map[string]string{"instagram.com": "instagram", "reddit.com": "reddit", "old.reddit.com": "reddit", "threads.net": "threads", "threads.com": "threads", "pixiv.net": "pixiv", "youtube.com": "youtube", "bilibili.com": "bilibili", "pinterest.com": "pinterest", "tiktok.com": "tiktok", "tumblr.com": "tumblr", "twitch.tv": "twitch", "kick.com": "kick", "deviantart.com": "deviantart"}
	shortHosts     = map[string]string{"redd.it": "reddit", "b23.tv": "bilibili", "pin.it": "pinterest", "vm.tiktok.com": "tiktok", "vt.tiktok.com": "tiktok", "clips.twitch.tv": "twitch", "sta.sh": "deviantart"}
	twitchReserved = map[string]bool{"directory": true, "downloads": true, "inventory": true, "jobs": true, "p": true, "search": true, "settings": true, "subscriptions": true, "videos": true, "wallet": true}
	shortPath      = regexp.MustCompile(`^/[A-Za-z0-9_-]+/?$`)
	commentID      = regexp.MustCompile(`^[A-Za-z0-9]+$`)
	xPost          = regexp.MustCompile(`^/(?:([A-Za-z0-9_]{1,15})/status|i/(?:web/)?status)/([0-9]+)(?:/(?:photo|video)/[0-9]+)?/?$`)
	bskyPost       = regexp.MustCompile(`^/profile/([A-Za-z0-9.:-]+)/post/([A-Za-z0-9]+)/?$`)
	postPaths      = map[string]*regexp.Regexp{
		"instagram":  regexp.MustCompile(`^/(?:p|reel|reels)/[A-Za-z0-9_-]+/?$|^/share/(?:p|reel)/[A-Za-z0-9_-]+/?$`),
		"reddit":     regexp.MustCompile(`^/(?:r/[^/]+/(?:comments|s)/[^/]+|comments/[^/]+)(?:/.*)?$`),
		"threads":    regexp.MustCompile(`^/@[^/]+/post/[A-Za-z0-9_-]+/?$|^/share/[A-Za-z0-9_-]+/?$`),
		"pixiv":      regexp.MustCompile(`^/(?:en/)?artworks/[0-9]+/?$`),
		"youtube":    regexp.MustCompile(`^/post/[A-Za-z0-9_-]+/?$`),
		"bilibili":   regexp.MustCompile(`^/video/[A-Za-z0-9]+/?$`),
		"pinterest":  regexp.MustCompile(`^/pin/[A-Za-z0-9_-]+/?$`),
		"tiktok":     regexp.MustCompile(`^/@[^/]+/(?:video|photo)/[0-9]+/?$`),
		"tumblr":     regexp.MustCompile(`^/(?:[^/]+/[0-9]+|blog/view/[^/]+/[0-9]+|post/[0-9]+)(?:/.*)?$`),
		"twitch":     regexp.MustCompile(`^/(?:videos/[0-9]+|[^/]+/clip/[A-Za-z0-9_-]+|[A-Za-z0-9_]+)/?$`),
		"deviantart": regexp.MustCompile(`^/[^/]+/art/[^/]+/?$`),
	}
)

// Parse admits only known platform hosts and post shapes, never arbitrary URLs.
func Parse(raw string) (Link, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || len(raw) > 2048 {
		return Link{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if u.RawPath != "" || strings.ContainsAny(u.Path, "\\<>[]()\n\r\t ") {
		return Link{}, false
	}
	comment := u.Query().Get("comment")
	kickClip := u.Query().Get("clip")
	u.Host, u.RawQuery, u.Fragment, u.ForceQuery = host, "", "", false
	l := Link{}
	switch host {
	case "kick.com":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 3 && parts[1] == "clips" {
			kickClip = parts[2]
		} else if len(parts) != 1 {
			return Link{}, false
		}
		if !kickAccount.MatchString(parts[0]) || !kickClipID.MatchString(kickClip) {
			return Link{}, false
		}
		l.Platform, l.ID, l.Account = "kick", kickClip, parts[0]
		u.Path = "/" + parts[0] + "/clips/" + kickClip
	case "x.com", "twitter.com", "fxtwitter.com", "fixupx.com", "vxtwitter.com":
		p := xPost.FindStringSubmatch(u.Path)
		if p == nil {
			return l, false
		}
		l.Platform, l.Account, l.ID = "twitter", p[1], p[2]
		if l.Account == "" {
			l.Account = "i"
		}
		u.Host = "x.com"
		u.Path = "/" + l.Account + "/status/" + l.ID
	case "bsky.app", "fxbsky.app", "bskyx.app":
		p := bskyPost.FindStringSubmatch(u.Path)
		if p == nil {
			return l, false
		}
		l.Platform, l.Account, l.ID = "bluesky", p[1], p[2]
		u.Host = "bsky.app"
		u.Path = "/profile/" + p[1] + "/post/" + p[2]
	default:

		l.Platform = platformHosts[host]

		if p := shortHosts[host]; p != "" {
			if !shortPath.MatchString(u.Path) {
				return Link{}, false
			}
			l.Platform = p
		} else {
			if strings.HasSuffix(host, ".tumblr.com") {
				l.Platform = "tumblr"
			}
			if l.Platform == "" || !postPaths[l.Platform].MatchString(u.Path) {
				return Link{}, false
			}
			if l.Platform == "twitch" {

				if twitchReserved[strings.Trim(u.Path, "/")] {
					return Link{}, false
				}
			}
		}
	}
	if l.Platform == "reddit" && comment != "" && commentID.MatchString(comment) {
		u.RawQuery = url.Values{"comment": {comment}}.Encode()
	}
	l.URL = u.String()
	return l, true
}

// Links skips code, angle-suppressed links and spoilers. The cap is per message,
// not per platform, and canonical duplicates cost only one upstream request.
func Links(text string) []Link {
	if !strings.Contains(text, "https://") {
		return nil
	}
	var out []Link
	seen := map[string]bool{}
	code := 0
	hidden := false
	spoiler := false
	for i := 0; i < len(text); {
		if text[i] == '`' {
			end := i
			for end < len(text) && text[end] == '`' {
				end++
			}
			n := end - i
			switch code {
			case 0:
				code = n
			case n:
				code = 0
			}
			i = end
			continue
		}
		if code != 0 {
			i++
			continue
		}
		if strings.HasPrefix(text[i:], "||") {
			spoiler = !spoiler
			i += 2
			continue
		}
		if text[i] == '<' {
			hidden = true
			i++
			continue
		}
		if text[i] == '>' {
			hidden = false
			i++
			continue
		}
		if !strings.HasPrefix(text[i:], "https://") {
			i++
			continue
		}
		end := i
		for end < len(text) && !unicode.IsSpace(rune(text[end])) && !strings.ContainsRune("<>`|", rune(text[end])) {
			end++
		}
		raw := strings.TrimRight(text[i:end], ".,!?;:)]}")
		if !hidden && !spoiler {
			if l, ok := Parse(raw); ok && !seen[l.URL] {
				out = append(out, l)
				seen[l.URL] = true
				if len(out) == 3 {
					break
				}
			}
		}
		i = end
	}
	return out
}

// remoteURL is for URLs handed to Discord, never for server-side fetching.
// Markdown delimiters are encoded so metadata cannot break a masked link.
func remoteURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || len(raw) > 2048 || (u.Port() != "" && u.Port() != "443") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") || host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".localhost") || strings.ContainsAny(raw, "\r\n\t\\") {
		return ""
	}
	return strings.NewReplacer("(", "%28", ")", "%29", "[", "%5B", "]", "%5D", "<", "%3C", ">", "%3E").Replace(u.String())
}
