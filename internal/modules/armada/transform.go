// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/disgoorg/snowflake/v2"
)

const (
	// armadaMaxChars keeps well under NIP-44's 65,535-byte cap once the
	// layers around the rumor are counted.
	armadaMaxChars = 60_000
	discordMax     = 2_000
	// maxParts is how many Discord messages one rumor may become. Each part
	// is a webhook send and a mapping row, so splitting is the cheapest
	// amplifier there is; ten parts is 20,000 characters.
	maxParts = 10
	// maxNameChars bounds a display name placed in bridge-authored text.
	maxNameChars = 80
)

var (
	userMention = regexp.MustCompile(`<@!?(\d+)>`)
	customEmoji = regexp.MustCompile(`<(a?):(\w+):(\d+)>`)
	// channelMention is <#id>; commandMention is </name:id>, where a
	// subcommand's name has spaces in it.
	channelMention = regexp.MustCompile(`<#(\d+)>`)
	commandMention = regexp.MustCompile(`</([^:<>\n]{1,96}):\d+>`)
	nostrProfile   = regexp.MustCompile(`nostr:(npub1|nprofile1)([02-9ac-hj-np-z]+)`)
	emojiName      = regexp.MustCompile(`^\w{2,32}$`)
	emojiPath      = regexp.MustCompile(`^/emojis/(\d{1,25})\.(\w+)$`)
	markdown       = regexp.MustCompile("[\\\\`*_~|\\[\\]()>#-]")
	lineTail       = regexp.MustCompile(`[^\S\n]+\n`)
	blankRun       = regexp.MustCompile(`\n{3,}`)
)

// toArmada is Discord markup turned into Armada's plain text: user mentions
// become @name, a channel mention a link to that channel in guild (Armada
// can't name a Discord channel, and the raw <#id> reads as noise), a slash
// command mention /name, custom emoji :name: (with NIP-30 tags alongside),
// and attachment URLs follow one per line.
func toArmada(content string, guild snowflake.ID, names map[string]string, urls []string) string {
	text := userMention.ReplaceAllStringFunc(content, func(raw string) string {
		if n, ok := names[userMention.FindStringSubmatch(raw)[1]]; ok {
			return "@" + n
		}
		return raw
	})
	text = channelMention.ReplaceAllString(text, "https://discord.com/channels/"+guild.String()+"/$1")
	text = commandMention.ReplaceAllString(text, "/$1")
	text = customEmoji.ReplaceAllString(text, ":$2:")
	if len(urls) > 0 {
		lines := []string{strings.TrimRight(text, " \t\n")}
		lines = append(lines, urls...)
		text = strings.TrimLeft(strings.Join(lines, "\n"), "\n")
	}
	if utf8.RuneCountInString(text) > armadaMaxChars {
		text = string([]rune(text)[:armadaMaxChars-3]) + "..."
	}
	return text
}

// emojiTags are NIP-30 tags for the custom emoji in Discord content, one per
// name, so Armada can draw the image the text names.
func emojiTags(content string) [][]string {
	var tags [][]string
	seen := map[string]bool{}
	for _, m := range customEmoji.FindAllStringSubmatch(content, -1) {
		if seen[m[2]] {
			continue
		}
		seen[m[2]] = true
		tags = append(tags, []string{"emoji", m[2], emojiURL(m[3], m[1] == "a")})
	}
	return tags
}

func emojiURL(id string, animated bool) string {
	ext := "png"
	if animated {
		ext = "gif"
	}
	return "https://cdn.discordapp.com/emojis/" + id + "." + ext
}

// discordEmoji is the id in a Discord emoji CDN URL. Parsed rather than
// matched as a substring, or https://evil.example/?x=cdn.discordapp.com/emojis/1.
// would get to name the emoji.
func discordEmoji(raw string) (id string, animated, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "cdn.discordapp.com" {
		return "", false, false
	}
	m := emojiPath.FindStringSubmatch(u.Path)
	if m == nil {
		return "", false, false
	}
	return m[1], m[2] == "gif", true
}

// maxReactionRunes bounds an emoji handed to Discord's reaction routes. A
// real ZWJ sequence is about ten code points.
const maxReactionRunes = 16

// reactionEmoji is a kind 7's content as a unicode emoji Discord can react
// with: "+" and "" are a like and "-" a dislike (NIP-25). It has to be emoji
// and nothing else, because disgo puts it in the request path unescaped.
func reactionEmoji(content string) (string, bool) {
	c := strings.TrimSpace(content)
	switch c {
	case "", "+":
		return "👍", true
	case "-":
		return "👎", true
	}
	if utf8.RuneCountInString(c) > maxReactionRunes {
		return "", false
	}
	shown := false
	for _, r := range c {
		switch {
		case pictograph(r) || r == 0x20E3: // 0x20E3 makes a keycap of a digit
			shown = true
		case r == 0x200D || r == 0xFE0F || r == '#' || r == '*' || (r >= '0' && r <= '9') || (r >= 0xE0020 && r <= 0xE007F):
			// joiners, presentation, keycap bases and subdivision-flag tags
		default:
			return "", false
		}
	}
	return c, shown
}

// pictographs is a loose superset of Unicode's Extended_Pictographic, which
// Go's unicode tables don't carry, along with the flag letters and skin
// tones. Loose is safe: Discord refuses what isn't an emoji, and nothing in
// these ranges can shape a path.
var pictographs = [][2]rune{
	{0xA9, 0xA9}, {0xAE, 0xAE}, {0x203C, 0x203C}, {0x2049, 0x2049}, {0x2122, 0x2122},
	{0x2139, 0x2139}, {0x2194, 0x21AA}, {0x231A, 0x23FF}, {0x24C2, 0x24C2}, {0x25AA, 0x27BF},
	{0x2934, 0x2935}, {0x2B05, 0x2B55}, {0x3030, 0x3030}, {0x303D, 0x303D}, {0x3297, 0x3299},
	{0x1F000, 0x1FAFF}, {0x1FC00, 0x1FFFD},
}

func pictograph(r rune) bool {
	for _, p := range pictographs {
		if r >= p[0] && r <= p[1] {
			return true
		}
	}
	return false
}

// armadaEmoji is a kind 7's custom emoji as Discord's name:id, when its
// NIP-30 tag names the content and points at Discord's emoji CDN. Any other
// image has nothing on Discord to react with, so it stays in Armada.
func armadaEmoji(content string, tags [][]string) (string, bool) {
	c := strings.TrimSpace(content)
	if len(c) < 2 || c[0] != ':' || c[len(c)-1] != ':' {
		return "", false
	}
	name := c[1 : len(c)-1]
	if !emojiName.MatchString(name) {
		return "", false
	}
	for _, t := range tags {
		if len(t) < 3 || t[0] != "emoji" || t[1] != name {
			continue
		}
		if id, _, ok := discordEmoji(t[2]); ok {
			return name + ":" + id, true
		}
	}
	return "", false
}

// toDiscord is Armada text made ready for Discord: bare npub and nprofile
// URIs shortened (Discord shows them as dead links), and :name: turned back
// into native emoji when its NIP-30 tag points at Discord's emoji CDN. Event
// URIs stay whole so they can still be copied.
func toDiscord(content string, tags [][]string) string {
	out := nostrProfile.ReplaceAllStringFunc(content, func(raw string) string {
		m := nostrProfile.FindStringSubmatch(raw)
		return "@" + m[1] + m[2][:min(8, len(m[2]))] + "..."
	})
	for _, t := range tags {
		// The name goes into Discord markup, so it is held to Discord's rule.
		if len(t) < 3 || t[0] != "emoji" || !emojiName.MatchString(t[1]) {
			continue
		}
		id, animated, ok := discordEmoji(t[2])
		if !ok {
			continue
		}
		a := ""
		if animated {
			a = "a"
		}
		out = strings.ReplaceAll(out, ":"+t[1]+":", fmt.Sprintf("<%s:%s:%s>", a, t[1], id))
	}
	return out
}

// split cuts text for Discord's 2,000 characters, at a newline or else a
// space when one falls in the second half of the window.
func split(text string) []string {
	r := []rune(text)
	var parts []string
	for len(r) > discordMax {
		w := r[:discordMax]
		cut := lastIndex(w, '\n')
		if cut < discordMax/2 {
			cut = lastIndex(w, ' ')
		}
		if cut < discordMax/2 {
			cut = discordMax
		}
		parts = append(parts, string(r[:cut]))
		r = r[cut:]
		if len(r) > 0 && (r[0] == '\n' || r[0] == ' ') {
			r = r[1:]
		}
	}
	if len(r) > 0 {
		parts = append(parts, string(r))
	}
	return parts
}

func lastIndex(r []rune, c rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == c {
			return i
		}
	}
	return -1
}

// capParts keeps the first maxParts and says in the last one how many
// more there were, rather than losing the tail without a word.
func capParts(parts []string) []string {
	if len(parts) <= maxParts {
		return parts
	}
	dropped := len(parts) - maxParts
	parts = parts[:maxParts]
	s := "s"
	if dropped == 1 {
		s = ""
	}
	notice := fmt.Sprintf("\n%d more part%s not bridged", dropped, s)
	last := []rune(parts[maxParts-1])
	keep := min(len(last), discordMax-utf8.RuneCountInString(notice))
	parts[maxParts-1] = string(last[:keep]) + notice
	return parts
}

// escape neutralises Discord markdown in a name someone else chose, for the
// one place it lands in text the bridge wrote: the reply line. Unescaped, a
// name of [#general](https://phishing.example) made a link that borrowed
// the bridge's authority.
func escape(name string) string {
	if r := []rune(name); len(r) > maxNameChars {
		name = string(r[:maxNameChars]) + "..."
	}
	return markdown.ReplaceAllString(name, `\$0`)
}

// stripURLs takes out of the text the URLs that ride as uploads, or Discord
// shows the media twice.
func stripURLs(text string, urls []string) string {
	if len(urls) == 0 {
		return text
	}
	for _, u := range urls {
		text = strings.ReplaceAll(text, u, "")
	}
	text = lineTail.ReplaceAllString(text, "\n")
	text = blankRun.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
