package filter

import (
	"regexp"
	"strings"
)

// Rules are the words Discord's guidelines treat as a violation on sight:
// there is no framing in which one is the word somebody reached for by
// accident, so no sentence needs reading around it. The subs are
// deliberately silly: a member who tries again gets the same treatment and
// nothing to argue with.
//
// Known false positives, accepted because a wrong hit costs one daft word:
// Troon (a town in Scotland) and "tranny" for a car's transmission.
var Rules = []Rule{
	{
		Name:       "n-word",
		Key:        "ngr",
		Spec:       "nigger[sz5$]?",
		CrossWords: true,
		Subs: []Sub{
			{"ninja", "ninjas"},
			{"ninjago", "ninjagos"},
			{"nice person", "nice people"},
			{"night owl", "night owls"},
			{"nintendo enjoyer", "nintendo enjoyers"},
		},
	},
	{
		Name:       "f-slur",
		Key:        "fg",
		Spec:       "faggot[sz5$]?",
		CrossWords: true,
		Subs: []Sub{
			{"frog", "frogs"},
			{"fog", "fogs"},
			{"thot", "thots"},
			{"fine gentleman", "fine gentlemen"},
			{"forklift certified individual", "forklift certified individuals"},
		},
	},
	{
		Name: "tranny",
		Key:  "rn",
		Spec: "trann(ies|iez|ys|ie|y)",
		Subs: []Sub{
			{"person", "people"},
			{"nice person", "nice people"},
			{"epic person", "epic people"},
			{"transformer", "transformers"},
			{"trombone player", "trombone players"},
		},
	},
	{
		Name: "troon",
		Key:  "rn",
		Spec: "troon[sz5$]?",
		Subs: []Sub{
			{"person", "people"},
			{"nice person", "nice people"},
			{"epic person", "epic people"},
			{"cartoon", "cartoons"},
			{"trooper", "troopers"},
		},
	},
	{
		Name: "gook",
		Key:  "gc",
		Spec: "gook[sz5$]?",
		Subs: []Sub{
			{"goose", "geese"},
			{"gnome", "gnomes"},
			{"goofball", "goofballs"},
			{"guy with a metal detector", "guys with metal detectors"},
			{"gourmet mayonnaise reviewer", "gourmet mayonnaise reviewers"},
		},
	},
	{
		Name: "chink",
		Key:  "chnc",
		Spec: "chink[sz5$]?",
		// The one spelling here that is also an ordinary word: "a chink in
		// the armour", "a chink of light". Anchored to the matched word.
		NotIf: regexp.MustCompile(`(?i)^chinks?\s+(in|of|between)\b`),
		Subs: []Sub{
			{"chinchilla", "chinchillas"},
			{"chinstrap penguin", "chinstrap penguins"},
			{"chimney sweep", "chimney sweeps"},
			{"chess club treasurer", "chess club treasurers"},
			{"chap who irons his socks", "chaps who iron their socks"},
		},
	},
	{
		// "cute and funny", coded language for the rule below, written every
		// way a filter-dodger thinks of: joined up, vowels dropped, "and"
		// worn down to an n, a k for the c. The loosest rule here on purpose.
		Name:       "cute and funny",
		Key:        "fny",
		Spec:       "[ck]u?te?a?nd?fu?nn?y",
		CrossWords: true,
		Subs: []Sub{
			{"beige", "beige"},
			{"tall and boring", "tall and boring"},
			{"damp and mildly inconvenient", "damp and mildly inconvenient"},
			{"crunchy and well ventilated", "crunchy and well ventilated"},
			{"beige and structurally sound", "beige and structurally sound"},
			{"tepid and vaguely municipal", "tepid and vaguely municipal"},
			{"sturdy and tax compliant", "sturdy and tax compliant"},
		},
	},
	{
		// The doubled n is required and the u is not: "cny" and "cuny" are
		// Chinese New Year and a New York university far more often.
		Name: "cunny",
		Key:  "cn",
		Spec: "[ck]u?nn(ies|iez|ys|ie|y|i)",
		Subs: []Sub{
			{"cumin", "cumins"},
			{"coriander", "corianders"},
			{"cinnamon", "cinnamons"},
			{"cannoli", "cannoli"},
			{"crouton", "croutons"},
			{"cheeky nutmeg", "cheeky nutmegs"},
			{"curry leaf swiped from a shared jar", "curry leaves swiped from a shared jar"},
		},
	},
	{
		Name: "kike",
		Key:  "c",
		Spec: "kike[sz5$]?",
		Subs: []Sub{
			{"kite", "kites"},
			{"koala", "koalas"},
			{"kazoo virtuoso", "kazoo virtuosos"},
			{"karaoke legend", "karaoke legends"},
			{"keen amateur beekeeper", "keen amateur beekeepers"},
		},
	},
}

// innocent are whole words that carry a rule's letters and are not the
// rule, matched against the entire word a hit landed in. That spares
// "sniggered" without sparing "sniggernation", which an anchor could not.
// Short and closed on purpose: a miss costs a daft substitution.
var innocent = regexp.MustCompile(`(?i)^(?:snigger|niggard|gobbledygook|gobbledegook|chinkapin|hangook|cunning|cunningham|cunnilingus)(?:s|es|ed|er|ing|ly|liness)?$`)

// Blocks are shapes with no publishable form. Each is near zero on ordinary
// conversation, which is the bar for being here.
var Blocks = []Block{
	{
		// Whoever reads a bot token owns the bot.
		Reason:  "a discord bot token",
		Pattern: regexp.MustCompile(`\b[A-Za-z0-9_-]{24,28}\.[A-Za-z0-9_-]{6}\.[A-Za-z0-9_-]{27,}\b`),
		Gate:    tokenShaped,
	},
	{
		// ponytail: a hand list of lookalike names and throwaway TLDs, which
		// goes stale as campaigns move. A maintained phishing-domain feed,
		// loaded at boot, is the upgrade.
		Reason:  "a known discord phishing link",
		Pattern: regexp.MustCompile(`(?i)https?://[^\s/]*\b(discord|dlscord|discrod|discocrd|steamcommunlty|discordgift|dicsord)[a-z0-9-]*\.(ru|cf|gq|tk|ml|ga|xyz|top|click|link|monster|shop)\b`),
		Gate:    hasLink,
	},
	{
		// These services exist for one purpose and are named after it.
		Reason:  "an ip grabber link",
		Pattern: regexp.MustCompile(`(?i)https?://(?:[a-z0-9-]+\.)?(grabify\.link|iplogger\.(org|com|ru)|blasze\.com|yip\.su|2no\.co|iplis\.ru|ps3cfw\.com)\b`),
		Gate:    hasLink,
	},
}

func hasLink(s string) bool { return strings.Contains(s, "://") }

// tokenShaped reports two dots exactly seven bytes apart with no space
// between them, which a token has around its six-character middle part and
// ordinary prose almost never does.
func tokenShaped(s string) bool {
	for i := strings.IndexByte(s, '.'); i >= 0 && i+7 < len(s); {
		if s[i+7] == '.' && !strings.ContainsAny(s[i+1:i+7], " \t\n") {
			return true
		}
		j := strings.IndexByte(s[i+1:], '.')
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return false
}
