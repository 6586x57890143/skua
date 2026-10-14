package pp

// Entry is one release of the game as members read it in /pp changelog.
// Balance is Default's fingerprint when it shipped: changelog_test fails
// while the newest entry's doesn't match, so no balance change goes out
// unannounced.
type Entry struct {
	Version, Date, Balance string
	Lines                  []string
}

// Changelog is newest first.
var Changelog = []Entry{{
	Version: "0.1.0",
	Date:    "2026-10-14",
	Balance: "75beb4",
	Lines: []string{
		"pot points arrive: time in voice and chatting both earn pp",
		"every day has a pool of vigor; earning slows as it runs out and what you leave unspent partly carries to tomorrow",
		"wear the server tag or put .gg/melted in your status for a little extra on everything but quests",
		"when the whole server has a big day the hearth lights up and everyone earns more for the rest of it and all of the next",
		"days turn at 00:00 utc",
	},
}}
