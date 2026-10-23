package fix

import (
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/6586x57890143/skua/internal/brand"
)

// Only actual HTTPS URLs become links. Provider markdown stays inert; no
// provider can smuggle a masked destination, mention or timestamp into a card.
func sourceText(text string, limit int) string {
	if limit < 4 {
		return ""
	}
	var b strings.Builder
	used, last := 0, 0
	// Only this prefix can contribute to the UTF-16 budget. Extra room keeps
	// a bounded link destination intact; never link a URL cut by the window.
	scan := text[:min(len(text), limit*4+2048)]
	for _, loc := range sourceURLs.FindAllStringIndex(scan, 32) {
		prefix := clean(text[last:loc[0]], limit-used)
		b.WriteString(prefix)
		used += len(utf16.Encode([]rune(prefix)))
		if used >= limit-3 {
			return clip(b.String(), limit)
		}
		raw := strings.TrimRight(text[loc[0]:loc[1]], ".,!?;:)]}")
		u := remoteURL(raw)
		if loc[1] == len(scan) && len(scan) < len(text) {
			u = ""
		}
		visible := clean(raw, min(100, limit-used))
		link := visible
		if u != "" {
			link = "[" + visible + "](<" + u + ">)"
		}
		if len(utf16.Encode([]rune(link))) > limit-used-3 {
			b.WriteString(clean(text[loc[0]:], limit-used))
			return clip(b.String(), limit)
		}
		b.WriteString(link)
		used += len(utf16.Encode([]rune(link)))
		last = loc[0] + len(raw)
	}
	b.WriteString(clean(text[last:], limit-used))
	return clip(b.String(), limit)
}

var engagement = regexp.MustCompile(`(?i)^([0-9][0-9.,]*[kmb]?)\s+(likes?|reposts?|retweets?|replies|comments?|views?|votes?|upvotes?)$`)

func statLine(stats string) string {
	var out []string
	for _, part := range strings.SplitN(clip(stats, 500), "·", 6) {
		fields := engagement.FindStringSubmatch(strings.TrimSpace(part))
		if fields == nil {
			out = append(out, clean(part, 160))
			continue
		}
		label := strings.ToLower(fields[2])
		key, fallback := "", ""
		switch label {
		case "like", "likes":
			key, fallback = "pp_heart", "♥"
		case "repost", "reposts", "retweet", "retweets":
			key, fallback = "stat_repost", "↻"
		case "replies", "comment", "comments":
			key, fallback = "stat_reply", "↩"
		default:
			out = append(out, clean(part, 160))
			continue
		}
		mark := brand.Mention(key)
		if mark == "" {
			mark = fallback
		}
		out = append(out, mark+" **"+fields[1]+"**")
	}
	return clip(strings.Join(out, "   "), 500)
}
