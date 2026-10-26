package fix

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/6586x57890143/skua/internal/brand"
)

var statCount = regexp.MustCompile(`(?i)^[0-9][0-9.,]*[kmb]?$`)

// Providers mix emoji-first, count-first and word-first metrics, sometimes
// separated only by spaces. Normalize only the stats field, never post text.
var statSymbols = strings.NewReplacer(
	"\ufe0f", "", "\ufe0e", "",
	"❤", " likes ", "♥", " likes ", "♡", " likes ", "👍", " likes ",
	"💬", " replies ", "🗨", " replies ", "↩", " replies ",
	"🔁", " reposts ", "🔄", " reposts ", "↻", " reposts ", "📤", " reposts ",
	"👁", " views ", "👀", " views ", "📺", " views ",
	"🔖", " bookmarks ", "⭐", " favorites ", "🪙", " coins ",
	"⬇", " downloads ", "📝", " notes ",
)

type statKind struct {
	key, label string
	order      int
	qualify    bool
}

func metric(word string) statKind {
	switch word = strings.ToLower(word); word {
	case "reply", "replies", "comment", "comments", "convo", "convos", "conversation", "conversations":
		return statKind{"stat_reply", "replies", 0, false}
	case "repost", "reposts", "retweet", "retweets", "share", "shares", "reblog", "reblogs":
		return statKind{"stat_repost", "reposts", 1, false}
	case "quote", "quotes":
		return statKind{"stat_repost", "quotes", 2, true}
	case "like", "likes", "heart", "hearts", "<3", "favorite", "favorites", "favourite", "favourites", "fave", "faves":
		return statKind{"stat_likes", "likes", 3, false}
	case "vote", "votes", "upvote", "upvotes":
		return statKind{"stat_likes", word, 4, true}
	case "view", "views", "viewer", "viewers", "watching", "play", "plays":
		return statKind{"stat_views", "views", 5, false}
	case "bookmark", "bookmarks", "save", "saves":
		return statKind{"stat_bookmarks", "bookmarks", 6, false}
	case "coin", "coins":
		return statKind{"stat_coins", "coins", 7, false}
	case "download", "downloads":
		return statKind{"stat_downloads", "downloads", 8, false}
	case "note", "notes":
		return statKind{"stat_notes", "notes", 9, false}
	default:
		return statKind{}
	}
}

func statLine(stats string) string { return statLineLimit(stats, 500) }

func statLineLimit(stats string, limit int) string {
	if stats == "" || limit <= 0 {
		return ""
	}
	words := strings.FieldsFunc(statSymbols.Replace(clip(stats, 500)), func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("·•|", r)
	})
	var ordered [10]string
	var extra []string
	for i := 0; i < len(words); {
		kind, count := statKind{}, ""
		if i+1 < len(words) {
			if statCount.MatchString(words[i]) {
				kind, count = metric(words[i+1]), words[i]
			} else if statCount.MatchString(words[i+1]) {
				kind, count = metric(words[i]), words[i+1]
			}
		}
		if kind.key == "" {
			extra = append(extra, clean(words[i], 100))
			i++
			continue
		}
		i += 2
		// Twitch supplies both an eye and a trailing views/watching label.
		// Skip that redundant label, but never consume the next metric's name.
		if i < len(words) && metric(words[i]).key == kind.key && (i+1 == len(words) || !statCount.MatchString(words[i+1])) {
			i++
		}
		value := "**" + compactCount(count) + "**"
		if mark := brand.Mention(kind.key); mark != "" {
			value = mark + " " + value
			if kind.qualify {
				value += " " + kind.label
			}
		} else {
			value += " " + kind.label // readable until application emoji finish syncing
		}
		ordered[kind.order] = value
	}
	out, used := "", 0
	for _, entry := range append(ordered[:], extra...) {
		if entry == "" {
			continue
		}
		separator := ""
		if out != "" {
			separator = "  "
		}
		n := len(utf16.Encode([]rune(separator + entry)))
		if used+n > limit {
			break // never cut a custom emoji mention or a formatted count in half
		}
		out += separator + entry
		used += n
	}
	return out
}
