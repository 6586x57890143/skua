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
	Balance: "ca01af",
	Lines: []string{
		"pot points arrive: time in voice and chatting both earn pp",
		"everyone starts a wanderer; pick a path in /pp, from shrike fighter to magpie thief, earn its titles as you level and change it once a season",
		"voice pays most with your mic open; muted or deafened still earns but slowly fades the longer you go quiet, and a message brings it back",
		"every day has a pool of vigor; earning slows as it runs out and what you leave unspent partly carries to tomorrow",
		"wear the server tag or put .gg/melted in your status for a little extra on everything but quests, and boosting the server adds a little more for as long as you boost",
		"a daily quest is dealt every day and you can hold three, so a missed day isn't lost; most pay a good chunk, some great and now and then one is golden, and you get one reroll a day",
		"when the whole server beats its usual day the hearth lights up and everyone earns more for the rest of it and all of the next",
		"seasons last four weeks: the board starts over but your level and pp stay yours",
		"every monday the week's five most active get pp and a laurel: time in voice and talk count past the daily cap, idling doesn't, and the prize never counts toward the season",
		"days turn at 00:00 utc and weeks on monday",
	},
}}
