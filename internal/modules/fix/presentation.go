package fix

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/disgoorg/disgo/discord"
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

func platformMark(l Link) string {
	mark := brand.Mention("pf_" + platformName(l))
	if mark == "" {
		mark = platformName(l)
	}
	return mark
}

func identity(l Link, p Post) string {
	platform := platformName(l)
	mark := platformMark(l)
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
			author = linkedText(p.Author, dest, 100, "profile")
		}
		return mark + " **" + linkedText(title, l.URL, 200, "post") + "**\n-# " + author
	}
	head := mark + " **" + linkedText(authorName(p.Author, platform), l.URL, 100, "post") + "**"
	if p.Handle != "" {
		handle := "@" + strings.TrimPrefix(p.Handle, "@")
		shown := clean(handle, 100)
		if dest := profile(l, p.AuthorURL, p.Handle); dest != "" {
			shown = linkedText(handle, dest, 100, "profile")
		}
		head += "\n-# " + shown
	}
	return head
}

func cardIdentity(l Link, p Post) string {
	text := identity(l, p)
	if t, err := time.Parse(time.RFC3339, p.Timestamp); err == nil {
		stamp := fmt.Sprintf("<t:%d:R>", t.Unix())
		if strings.Contains(text, "\n") {
			text += " · " + stamp
		} else {
			text += "\n-# " + stamp
		}
	}
	return text
}

func cardHeader(content string) discord.ContainerSubComponent {
	return discord.NewTextDisplay(content)
}

// Keep generated links atomic: a tight budget drops optional identity pieces
// instead of cutting a URL into visible Markdown.
func sectionText(l Link, s Section, limit int) string {
	kind := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s.Kind)), "_", "-")
	quoted := kind == "quote" || kind == "repost" || kind == "quoted-post" || strings.HasPrefix(strings.ToLower(s.Title), "quote · ")
	icon := ""
	switch kind {
	case "community-note", "note":
		icon = sectionIcon("stat_notes", "📝")
	case "reply", "comment":
		icon = sectionIcon("stat_reply", "↩️")
	case "poll":
		return iconSource(sectionIcon("stat_poll", "▥"), s.Text, limit, false)
	case "link-card", "linked-post":
		return linkedSectionText(s, kind, limit)
	}
	if icon != "" {
		return iconSource(icon, s.Text, limit, true)
	}
	prefix := ""
	title := s.Title
	if quoted {
		title = s.Author
		if title == "" {
			title = strings.TrimPrefix(s.Title, "quote · ")
		}
	}
	reserve := 0
	if quoted {
		reserve = len(utf16.Encode([]rune(sectionIcon("stat_repost", "🔁") + " ")))
	}
	titleBudget := limit - len(prefix) - reserve - 4
	if titleBudget < 4 {
		return ""
	}
	label := clean(title, min(100, titleBudget))
	if dest := remoteURL(s.URL); dest != "" {
		linked := linkedText(title, dest, min(100, titleBudget), "post")
		if len(utf16.Encode([]rune(linked))) <= titleBudget {
			label = linked
		}
	}
	heading := prefix + "**" + label + "**"
	if quoted && s.Handle != "" {
		shown := clean("@"+strings.TrimPrefix(s.Handle, "@"), 70)
		if dest := profile(l, s.AuthorURL, s.Handle); dest != "" {
			shown = linkedText("@"+strings.TrimPrefix(s.Handle, "@"), dest, 70, "profile")
		}
		if len(utf16.Encode([]rune(heading+" · "+shown))) <= limit-reserve {
			heading += " · " + shown
		}
	}
	if quoted {
		heading = sectionIcon("stat_repost", "🔁") + " " + heading
	}
	left := limit - len(utf16.Encode([]rune(heading))) - 1
	for budget := min(300, left); budget >= 4; {
		body := sourceText(s.Text, budget)
		if quoted {
			body = muted(body)
		}
		units := len(utf16.Encode([]rune(body)))
		if units <= left {
			return heading + "\n" + body
		}
		budget -= max(1, units-left)
	}
	return heading
}

func muted(text string) string {
	if text == "" {
		return ""
	}
	return "-# " + strings.ReplaceAll(text, "\n", "\n-# ")
}

func iconSource(icon, source string, limit int, small bool) string {
	prefix := icon + " "
	if small {
		prefix = "-# " + prefix
	}
	for budget := limit - len(utf16.Encode([]rune(prefix))); budget >= 1; {
		body := sourceText(source, budget)
		if small {
			body = muted(body)
			body = strings.TrimPrefix(body, "-# ")
		}
		text := prefix + body
		units := len(utf16.Encode([]rune(text)))
		if units <= limit {
			return text
		}
		budget -= max(1, units-limit)
	}
	if len(utf16.Encode([]rune(prefix))) <= limit {
		return strings.TrimSpace(prefix)
	}
	return ""
}

func linkedSectionText(s Section, kind string, limit int) string {
	dest := remoteURL(s.URL)
	if dest == "" {
		return ""
	}
	icon := sectionIcon("stat_link", "↗")
	label := strings.TrimSpace(s.Title)
	if kind == "linked-post" {
		icon = sectionIcon("stat_link", "↗")
		if target, ok := Parse(s.URL); ok {
			icon = platformMark(target)
		}
		label = "post"
		if strings.HasPrefix(strings.ToLower(s.Title), "clip on ") {
			label = "clip"
		}
	} else {
		lower := strings.ToLower(label)
		if strings.HasPrefix(lower, "open linked ") || strings.HasPrefix(lower, "view linked ") || lower == "open link" || lower == "view link" {
			u, _ := url.Parse(dest)
			label = u.Hostname()
		}
	}
	if label == "" {
		u, _ := url.Parse(dest)
		label = u.Hostname()
	}
	budget := limit - len(utf16.Encode([]rune(icon+" ")))
	if budget < 4 {
		return ""
	}
	link := linkedText(label, dest, min(160, budget-4), "link")
	if len(utf16.Encode([]rune(icon+" "+link))) > limit {
		return ""
	}
	text := icon + " " + link
	if s.Text == "" {
		return text
	}
	for bodyBudget := max(0, limit-len(utf16.Encode([]rune(text)))-5); bodyBudget > 0; {
		body := muted(sourceText(s.Text, bodyBudget))
		result := text + "\n" + body
		units := len(utf16.Encode([]rune(result)))
		if units <= limit {
			return result
		}
		bodyBudget -= max(1, units-limit)
	}
	return text
}

func sectionIcon(key, fallback string) string {
	if mark := brand.Mention(key); mark != "" {
		return mark
	}
	return fallback
}

// Discord displays escaped underscores literally inside masked-link labels.
// Other metadata escaping remains intact, including brackets and mentions.
func linkLabel(s string, limit int) string {
	return strings.ReplaceAll(clean(s, limit), `\_`, `_`)
}

// Discord renders masked links containing pictographs as literal Markdown.
// Keep decorated names intact outside the link and use a short, safe label for
// their destination. Apply the same rule to post titles and quoted identities.
func linkedText(text, dest string, limit int, fallback string) string {
	text = strings.Join(strings.Fields(text), " ")
	label := linkLabel(text, limit)
	unsafe := label == "" || strings.ContainsAny(text, "[]\\`") || strings.Contains(text, "://") || strings.ContainsFunc(text, func(r rune) bool {
		return r >= 0x1f000 || unicode.Is(unicode.So, r) || r == '\u200d' || r == '\ufe0f' || r == '\u20e3'
	})
	if unsafe {
		return clean(text, limit) + " · [" + fallback + "](<" + dest + ">)"
	}
	return "[" + label + "](<" + dest + ">)"
}
func authorName(author, platform string) string {
	if author == "" {
		return "post on " + platform
	}
	return author
}
