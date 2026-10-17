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
	Version: "0.3.0",
	Date:    "2026-10-17",
	Balance: "a51f0f",
	Lines: []string{
		"admins can reward contest winners with pp from the treasury, never pp made from nothing, and every grant is posted in the ledger channel",
		"a grant is at most a quarter of the treasury a week and ten pot days a season to anyone; anything over a pot day needs a second admin, and admins can't grant to admins",
		"grants open four weeks after the treasury first takes its levy",
	},
}, {
	Version: "0.2.0",
	Date:    "2026-10-17",
	Balance: "f580f8",
	Lines: []string{
		"2% of what you earn goes to the server's treasury, which pays the weekly prizes and contest grants, and it stops taking whenever the treasury already holds eight weeks of it",
		"admins can't make pp: a contest prize comes out of the treasury and is posted where everyone can see it",
		"every pp goes into a ledger chained by hash and its head is posted every day; the ledger button on /pp shows your own rows",
		"every roll, from a day's pool to what a message pays, comes from a seed posted before the season starts and revealed when it ends",
		"nothing you hold is ever taken and there's no cap on what you can save",
	},
}, {
	Version: "0.1.0",
	Date:    "2026-10-14",
	Balance: "ad0f8b",
	Lines: []string{
		"pot points arrive: time in voice and chatting both earn pp, and a day of real talk earns as well as a day in voice",
		"everyone starts a wanderer; pick a path in /pp, from shrike fighter to magpie thief, and earn its titles as you level",
		"the first pick of a season is free and every change after it costs pp, more the higher your level and more again each time",
		"spending comes out of your purse: your level counts everything you've earned and never goes down",
		"voice pays most with your mic open; muted or deafened still earns but slowly fades the longer you go quiet, and a message brings it back",
		"every day has a pool of vigor; earning slows as it runs out and what you leave unspent partly carries to tomorrow",
		"wear the server tag or put .gg/melted in your status for a little extra on everything but quests, and boosting the server adds a little more for as long as you boost",
		"a daily quest is dealt every day and you can hold three, so a missed day isn't lost; most pay a good chunk, some great and now and then one is golden, and you get one reroll a day",
		"when the whole server beats its usual day the hearth lights up and everyone earns more for the rest of it and all of the next",
		"seasons last four weeks: the board starts over but your level and pp stay yours",
		"every monday the week's five most active in voice and the five most active in chat each get pp and a laurel; time past the daily cap counts, idling doesn't, and the prize never counts toward the season",
		"days turn at 00:00 utc and weeks on monday",
	},
}}
