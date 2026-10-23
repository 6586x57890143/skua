package fix

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/6586x57890143/skua/internal/brand"
)

var twitterHandle = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)
var blueskyHandle = regexp.MustCompile(`^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)

func platformName(l Link) string {
	if l.Platform == "twitter" {
		return "x"
	}
	return l.Platform
}

// State changes use the suite's mood colours; ready cards use muted platform
// accents. No remote image sampling or additional fetch is needed.
func accent(l Link, p Post) int {
	if p.Provider == "unavailable" {
		return brand.ColorError
	}
	if p.Incomplete {
		return brand.ColorWarn
	}
	if p.Sensitive {
		return brand.ColorIdle
	}
	switch l.Platform {
	case "reddit":
		return 0xB87555
	case "bluesky":
		return 0x688FAF
	case "instagram":
		return 0xAB7993
	case "threads":
		return 0x88939F
	case "pixiv":
		return 0x6498B3
	case "bilibili":
		return 0x8AA9BD
	case "pinterest":
		return 0xA85A65
	case "tumblr":
		return 0x657C95
	case "deviantart":
		return 0x719C78
	default:
		return brand.PlatformColor(platformName(l))
	}
}

func profile(l Link, authorURL, handle string) string {
	u, err := url.Parse(authorURL)
	if err == nil && u.Scheme == "https" && u.User == nil && u.RawQuery == "" && u.Fragment == "" {
		valid := false
		switch l.Platform {
		case "twitter":
			valid = (u.Host == "x.com" || u.Host == "twitter.com") && twitterHandle.MatchString(strings.Trim(u.Path, "/"))
		case "bluesky":
			valid = len(u.Path) <= 256 && u.Host == "bsky.app" && strings.HasPrefix(u.Path, "/profile/") && len(strings.Split(strings.Trim(u.Path, "/"), "/")) == 2
		}
		if valid {
			return remoteURL(authorURL)
		}
	}
	handle = strings.TrimPrefix(handle, "@")
	if l.Platform == "twitter" && twitterHandle.MatchString(handle) {
		return "https://x.com/" + handle
	}
	if l.Platform == "bluesky" && blueskyHandle.MatchString(handle) {
		return "https://bsky.app/profile/" + handle
	}
	return ""
}

func identity(l Link, p Post) string {
	platform := platformName(l)
	mark := brand.Mention("pf_" + platform)
	if mark == "" {
		mark = platform
	}
	author := clean(p.Author, 100)
	if author == "" {
		author = "post on " + platform
	}
	if l.Platform == "reddit" {
		title := p.Title
		if title == "" {
			title = "reddit post"
		}
		if dest := redditProfile(p); dest != "" {
			author = "[" + linkLabel(p.Author, 100) + "](<" + dest + ">)"
		}
		return mark + " **[" + linkLabel(title, 200) + "](<" + l.URL + ">)**\n-# " + author
	}
	head := mark + " **[" + linkLabel(authorName(p.Author, platform), 100) + "](<" + l.URL + ">)**"
	if p.Handle != "" {
		handle := "@" + strings.TrimPrefix(p.Handle, "@")
		shown := clean(handle, 100)
		if dest := profile(l, p.AuthorURL, p.Handle); dest != "" {
			shown = "[" + linkLabel(handle, 100) + "](<" + dest + ">)"
		}
		head += "\n-# " + shown
	}
	return head
}

// Keep generated links atomic: a tight budget drops optional identity pieces
// instead of cutting a URL into visible Markdown.
func sectionText(l Link, s Section, limit int) string {
	quoted := s.Kind == "quote" || strings.HasPrefix(s.Title, "quote · ")
	prefix := ""
	title := s.Title
	if quoted {
		prefix = "-# quoted post\n"
		title = s.Author
		if title == "" {
			title = strings.TrimPrefix(s.Title, "quote · ")
		}
	}
	titleBudget := limit - len(prefix) - 4
	if titleBudget < 4 {
		return ""
	}
	label := clean(title, min(100, titleBudget))
	if dest := remoteURL(s.URL); dest != "" {
		linked := "[" + linkLabel(title, min(100, titleBudget)) + "](<" + dest + ">)"
		if len(utf16.Encode([]rune(linked))) <= titleBudget {
			label = linked
		}
	}
	heading := prefix + "**" + label + "**"
	if quoted && s.Handle != "" {
		shown := clean("@"+strings.TrimPrefix(s.Handle, "@"), 70)
		if dest := profile(l, s.AuthorURL, s.Handle); dest != "" {
			shown = "[" + linkLabel("@"+strings.TrimPrefix(s.Handle, "@"), 70) + "](<" + dest + ">)"
		}
		if len(utf16.Encode([]rune(heading+" · "+shown))) <= limit-20 {
			heading += " · " + shown
		}
	}
	left := limit - len(utf16.Encode([]rune(heading))) - 1
	for budget := min(300, left); budget >= 4; {
		body := sourceText(s.Text, budget)
		if quoted {
			body = quote(body)
		}
		units := len(utf16.Encode([]rune(body)))
		if units <= left {
			return heading + "\n" + body
		}
		budget -= max(1, units-left)
	}
	return heading
}

// Discord displays escaped underscores literally inside masked-link labels.
// Other metadata escaping remains intact, including brackets and mentions.
func linkLabel(s string, limit int) string {
	return strings.ReplaceAll(clean(s, limit), `\_`, `_`)
}
func authorName(author, platform string) string {
	if author == "" {
		return "post on " + platform
	}
	return author
}
