package fix

import (
	"strconv"
	"strings"
	"unicode/utf16"
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
			// Bare, not masked: Discord shows a masked link whose label
			// is a URL as raw markdown, and escaping changes the label.
			link = "<" + u + ">"
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

func compactCount(raw string) string {
	// Preserve upstream abbreviations; only format exact nonnegative integers.
	n, err := strconv.ParseUint(strings.ReplaceAll(raw, ",", ""), 10, 64)
	if err != nil {
		return clean(raw, 30)
	}
	switch {
	case n < 1000:
		return strconv.FormatUint(n, 10)
	case n < 10_000:
		return strings.TrimSuffix(strconv.FormatFloat(float64(n/100)/10, 'f', 1, 64), ".0") + "k"
	case n < 1_000_000:
		return strconv.FormatUint(n/1000, 10) + "k"
	case n < 1_000_000_000:
		return strings.TrimSuffix(strconv.FormatFloat(float64(n/100_000)/10, 'f', 1, 64), ".0") + "m"
	default:
		return strings.TrimSuffix(strconv.FormatFloat(float64(n/100_000_000)/10, 'f', 1, 64), ".0") + "b"
	}
}
