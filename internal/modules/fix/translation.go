package fix

import (
	"slices"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/language"
)

func languageCode(raw string) string {
	tag, err := language.Parse(strings.ReplaceAll(strings.TrimSpace(raw), "_", "-"))
	if err != nil {
		return ""
	}
	base, _ := tag.Base()
	if base.String() == "und" {
		return ""
	}
	return base.String()
}

func translationSource(source, target string) string {
	if source == "" {
		return ""
	}
	source = languageCode(source)
	if source == "en" || languageCode(target) != "en" {
		return ""
	}
	return source
}

// Flags are compact language markers, never the author's inferred nationality.
// Keep unfamiliar languages readable as their code instead of inventing a flag.
var languageCountries = map[string]string{
	"af": "ZA", "ar": "SA", "az": "AZ", "be": "BY", "bg": "BG", "bn": "BD",
	"bs": "BA", "ca": "ES", "cs": "CZ", "cy": "GB", "da": "DK", "de": "DE",
	"el": "GR", "es": "ES", "et": "EE", "eu": "ES", "fa": "IR", "fi": "FI",
	"fil": "PH", "fr": "FR", "ga": "IE", "gl": "ES", "gu": "IN", "he": "IL",
	"hi": "IN", "hr": "HR", "hu": "HU", "hy": "AM", "id": "ID", "is": "IS",
	"it": "IT", "ja": "JP", "ka": "GE", "kk": "KZ", "km": "KH", "kn": "IN",
	"ko": "KR", "lo": "LA", "lt": "LT", "lv": "LV", "mk": "MK", "ml": "IN",
	"mn": "MN", "mr": "IN", "ms": "MY", "my": "MM", "nb": "NO", "ne": "NP",
	"nl": "NL", "nn": "NO", "no": "NO", "pa": "IN", "pl": "PL", "pt": "PT",
	"ro": "RO", "ru": "RU", "si": "LK", "sk": "SK", "sl": "SI", "sq": "AL",
	"sr": "RS", "sv": "SE", "sw": "TZ", "ta": "IN", "te": "IN", "th": "TH",
	"tr": "TR", "uk": "UA", "ur": "PK", "uz": "UZ", "vi": "VN", "zh": "CN",
}

func languageMark(code string) string {
	if country := languageCountries[code]; country != "" {
		return string([]rune{0x1f1e6 + rune(country[0]-'A'), 0x1f1e6 + rune(country[1]-'A')})
	}
	return code
}

func translationCredit(l Link, p Post) string {
	var sources []string
	add := func(source string) {
		if code := translationSource(source, "en"); code != "" && !slices.Contains(sources, code) {
			sources = append(sources, code)
		}
	}
	add(p.TranslatedFrom)
	for _, section := range p.Sections {
		add(section.TranslatedFrom)
	}
	if len(sources) == 0 {
		return ""
	}
	marks := make([]string, 0, len(sources))
	for _, source := range sources[:min(len(sources), 6)] {
		marks = append(marks, languageMark(source))
	}
	label := "tl from:"
	// Emoji remain outside the masked link: Discord can render pictographs in
	// link labels as literal Markdown. Very long URLs keep the footer bounded;
	// the card's author/title still links to the original source.
	if remoteURL(l.URL) != "" && len(utf16.Encode([]rune(l.URL))) <= 240 {
		label = "[" + label + "](<" + l.URL + ">)"
	}
	return label + " " + strings.Join(marks, " ")
}
