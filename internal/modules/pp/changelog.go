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
	Version: "0.8.1",
	Date:    "2026-10-21",
	Balance: "d68156",
	Lines: []string{
		"if the treasury can't pay every weekly prize, each place gets the same share of its own, so first still pays the most",
		"the day's pool swings about half as much from one day to the next",
	},
}, {
	Version: "0.8.0",
	Date:    "2026-10-19",
	Balance: "c65e95",
	Lines: []string{
		"contest prizes come from a contest fund in the treasury, which grows by 3% of every quest paid and starts with three of the biggest prizes",
		"a contest prize can be up to five golden quests, and anyone can win up to ten pot days of them a season",
		"the levy is now 3.5%, enough to pay every weekly prize",
	},
}, {
	Version: "0.7.7",
	Date:    "2026-10-19",
	Balance: "5931ef",
	Lines: []string{
		"days now turn at 05:30 utc, a sweet spot between europe and the americas",
		"any change to how pp is earned waits for the next day to start",
	},
}, {
	Version: "0.7.6",
	Date:    "2026-10-19",
	Balance: "4fb0db",
	Lines: []string{
		"a day holds more vigor",
	},
}, {
	Version: "0.7.5",
	Date:    "2026-10-19",
	Balance: "e21e90",
	Lines: []string{
		"if the treasury can't pay every weekly prize, it splits what it has evenly between everyone placed instead of paying from the top down",
	},
}, {
	Version: "0.7.4",
	Date:    "2026-10-18",
	Balance: "ffe472",
	Lines: []string{
		"every quest shows each thing it asks on its own line with its own gauge",
		"the screens speak for themselves: the notes over them are gone, the hearth's progress is on the buffs screen and a path's price is on its button",
	},
}, {
	Version: "0.7.3",
	Date:    "2026-10-18",
	Balance: "165648",
	Lines: []string{
		"silver, gilt and diamond frames wear their pieces set into the frame, and the diamond frame is set with cut lozenges under a crown of diamonds",
	},
}, {
	Version: "0.7.2",
	Date:    "2026-10-18",
	Balance: "154a76",
	Lines: []string{
		"the portrait frames are slimmer, wear a crest from bronze up, a crown in gilt, and the last is cut diamond under a great crown",
	},
}, {
	Version: "0.7.1",
	Date:    "2026-10-18",
	Balance: "16abe8",
	Lines: []string{
		"your portrait wears a frame that rises with your title: wood, then iron, bronze, silver, gilt and at the last gilt set with rubies",
		"the lore button works again",
	},
}, {
	Version: "0.7.0",
	Date:    "2026-10-18",
	Balance: "1a7bb6",
	Lines: []string{
		"every class is a character now, with a story set in the rookery: the lore button opens them, any time, and walking a new path starts there",
		"a reroll never gives back the quest it replaces",
	},
}, {
	Version: "0.6.1",
	Date:    "2026-10-18",
	Balance: "570115",
	Lines: []string{
		"the menu stays under every /pp screen, and paging through it keeps to one message until the chat moves on",
		"each quest has its own reroll a day",
		"a perch shows beside your name from the first rung, and climbing opens the rung you bought",
		"the top rung is the perch touched by the great nothingness",
		"the buffs button says how close the server is to lighting the hearth",
	},
}, {
	Version: "0.6.0",
	Date:    "2026-10-17",
	Balance: "69bb55",
	Lines: []string{
		"each rung of the rookery stair now changes how your name looks on every board and on your status: bold, then slate, then chitin red with your perch, then a glass plate, a bronze one and gilt",
		"the shop prices everything in pp, and each rung opens its own card with its story and what it does",
		"the buffs button explains every buff, what it adds and how you come by it",
	},
}, {
	Version: "0.5.1",
	Date:    "2026-10-17",
	Balance: "b9d09e",
	Lines: []string{
		"the preseason runs two weeks longer: the first season starts on monday the 2nd of november, and until then the levels are the board",
	},
}, {
	Version: "0.5.0",
	Date:    "2026-10-17",
	Balance: "510ac3",
	Lines: []string{
		"quests arrive: one is dealt each day you're around, up to three held, and the quests button on /pp shows them with one reroll a day",
		"a good quest asks about a day's talk or time in voice, a great one a little more and a golden one more again, though never as much more as it pays",
		"some golden quests ask for something odd, like a picture in the selfie channel that stays up a day",
		"quests come to accounts at least a week old, and what they pay counts toward your level like any earning",
	},
}, {
	Version: "0.4.0",
	Date:    "2026-10-17",
	Balance: "b926de",
	Lines: []string{
		"the shop opens with the prestige ladder: each rung costs twice the last, starting at two pot days, and everything bought is burned",
		"if the shop burns less than 5% of what everyone earns two seasons running, a purse past 45 pot days keeps a tenth of new earning until it picks up; your level still counts all of it",
	},
}, {
	Version: "0.3.1",
	Date:    "2026-10-17",
	Balance: "0f75f3",
	Lines: []string{
		"the weekly checkpoint prizes come out of the treasury, before any grant, and are posted in the ledger channel",
		"a prize goes into your purse and doesn't count toward your level or the season, the same as a grant",
	},
}, {
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
