package pp

// ClassLore is every class's character, by class key. The world is
// LORE.md's: check a new fact against it, and write it there first.
var ClassLore = map[string]Lore{
	"wanderer": {
		Epithet: "come in off the grey north",
		Story: []string{
			"they were navigator on the whaler kittiwake out of the grey north, until the ice closed round her in a single night and opened again with half the crew gone and no sign of what took them. they walked south across the floes for a season, dragging the ship's boat in a harness that is still across their chest, and came down into the rookery with nothing to their name but the strap and a debt for the boat.",
			"an albatross dragged the pot up out of the surf, the old story goes, so the city has always let albatrosses sleep against its warm side. they slept there their first month, among the other newcomers, and woke every morning to the steam and the notices on the board. everyone who comes to the rookery starts the way they did, as a wanderer with no perch and no trade. some find one. some never stop looking.",
			"they take any work the board offers. the tally board has a ledger with the kittiwake's name in it, and the owners want their boat paid for. the counting house will lend against a voyage north, and a voyage north is what they want more than anything: to go back and find what the ice was hiding.",
			"the owners are the north sea company, a whaling concern in the bellward, and their books say the kittiwake was lost with all hands, which pays out its insurance at the counting house. a navigator who walked home can say what really happened up there. so the company bills them for the boat, and has twice sent a clerk to offer them a bellward perch if they sign the loss book as it stands.",
			"they haven't signed. they keep the kittiwake's last log in sailcloth under the boat strap, with the eleven names the ice took inside the cover. every albatross that comes in off the grey north gets asked the same thing at the pot's rim, whether anyone has seen a whaler held in the ice with her lamps still lit. last spring one of them said yes.",
		},
		Quote: "the city's just another ship. keep your head down and learn the rigging.",
	},
	"fighter": {
		Epithet: "late of the hedge company",
		Story: []string{
			"they served nine years in the hedge company, holding the thorn against the mire with a pike and a face of pale clay, the company's old trick so the things that come up out of the water can't tell one shrike from another. they rose to corporal of the eastern hedge and buried more of their squad than they can count.",
			"then the company sold the eastern hedge. a kiln consortium wanted the peat beyond it and paid the company to thin the thorn and look the other way. the squad was told to stand down. on the third night something came through the gap, and in the morning they were the only one of the squad still standing. the company called it a regrettable loss and docked their pay for the pikes.",
			"the consortium is three kiln families and a partner from the counting house, and the peat beyond the old eastern hedge feeds their furnaces day and night. the pale glass that comes out of them sits in the bellward's windows and under the gilt's feet as perches. the shrike has walked past those windows. they know whose glass was paid for with their squad.",
			"what drives them now is a law of the tally board: a debt does not die with the debtor, it passes to the one who stood beside them. every one of the squad died owing, and every one of those tallies is theirs. so they fight for coin in the sump's pits and hire out as muscle in the bellward, and they keep the clay on their face. the hedge is still hung with what they caught. they want the consortium's names hung beside it.",
			"the pits pay by the bout and more by the crowd, and a shrike in hedge clay draws a crowd. they've won forty-one bouts and lost two, and every purse goes straight to the tally board against the squad's debts, a name at a time. there are nine names left. the tenth was paid off last month, and that night they walked out to the mire's edge and hung its tally stick on the thorn with the rest.",
		},
		Quote: "i don't talk about the hedge. i point at the thorns.",
	},
	"ranger": {
		Epithet: "a warden on a cold trail",
		Story: []string{
			"the goshawk wardens have counted every tree in the last wood since the first winter, when they refused the flock its timber and the flock poured tallow instead. the city has never forgiven them and the wardens have never apologised. they keep the count and they keep to the wood, and in the rookery a warden's headband gets you served last.",
			"this one came in on a warrant of their own making. three autumns running the count came up short, a handful of old oaks at a time, cut at night and dragged out through the mire on sledges. they followed the drag marks to the hedge, and through it, and up the stair to the gilt, where the fireplaces burn oak all winter and nobody asks where it grew.",
			"the warrant is a warden's own, cut into a strip of bark and sealed with resin, and no court in the city will hear it. the order of the stair laughed at it at the stair's lowest gate and the counting house filed it under curiosities. so they keep their own count instead: every chimney in the gilt that smokes oak, and every sledge that comes through the hedge after dark.",
			"they sleep on rooftops because the rooming houses won't take a goshawk, and they hunt the sump's rats for the fur trade to pay for powder. they know the mire better than anyone in the city, and the wet guild and the hedge company both pay well for a guide. they take the work and they keep their eyes on the gilt's chimneys, counting the smoke.",
			"the count is getting somewhere. one family's chimneys smoke oak from the first frost to the thaw, more than any household could burn, and their kitchen boy sells the ash in the sump as soap. the ash is pale and fine and it is the last wood's. the wardens have told them to come home and let the wood keep its own count. they have told the wardens that a count with a hole in it isn't a count.",
		},
		Quote: "every tree has a number. so does every chimney up there.",
	},
	"sorcerer": {
		Epithet: "struck off the counting house",
		Story: []string{
			"they were a reckoner of the counting house, one of the ravens who sit at the pot's rim reading the steam and writing down what goes in and what comes out. it's a trade of patience and arithmetic, and they were good at it. good enough to notice that the sums don't close. the pot keeps its levy and the treasury records it, but every season a little more goes into the pot than comes out of it again, and nowhere in the ledger does it say where the rest goes.",
			"they said so, in writing, to the wrong senior reckoner. a week later their name was struck off the house's roll and their quill hand was branded with the house's mark, so no honest office would take them on again.",
			"the house's mark is a raven's eye over a ledger line, burned into the back of the hand. in the bellward it closes every door. in the sump it opens a few, because a branded reckoner can still do sums, and gamblers, fences and widows with tally trouble all need someone who can read figures and won't carry them to the house.",
			"now they work the sump as a hedge sorcerer, wrapped in old diver's net from the wet guild. they read steam off tavern kettles to tell gamblers their odds, find lost rings by the weight of their absence, and for a fee they'll unwrite a debt from a tally stick, which is very illegal and very popular. they still keep a ledger of their own. one day it will show where the missing pp go, and they'll take it up the stair to someone who has to listen.",
			"the ledger holds three seasons now, kept in a cipher of their own on the backs of betting slips. the missing share isn't large and it isn't taken evenly. it comes out of the heaviest purses on the stair, and only in the seasons when the shop sits quiet and nobody climbs. somebody is skimming the pot by a rule. they mean to find out whose rule it is.",
		},
		Quote: "the pot keeps better books than the counting house. i'm going to read them.",
	},
	"thief": {
		Epithet: "a diver of the low town",
		Story: []string{
			"born in the sump to a family that worked the wet guild's boats, they were diving the low town before they could fly properly. the drowned streets are still full of everything nobody had time to carry out: silver, ledgers, strongboxes, whole shops of glass. the wet guild brings it up and sells it in the bellward, and the bellward calls it salvage when it's buying and theft when it isn't.",
			"the red coat is the chapel's altar cloth. they cut it on the dive that brought up the last piece of the bell and have worn it since, to the hearthkeepers' disgust. the bell still rings under the water at the drowning hour, and the old divers say it rings for whoever is down there when it does.",
			"the wet guild pays its divers by the weight of what they bring up, less the guild's half, less the boat, less the lamp oil. a good diver clears a few pp a tide. a great one is the diver the bellward asks for by name, and that was them, the year they brought up a drowned jeweller's whole shop window in one piece. the guild still tells the story. it leaves out that the guild kept the window.",
			"they were caught once, fencing a gilt family's silver back to the gilt family, and did two years in the tally mines. they came up owing more than they went down with, which is how the mines work. now they have one plan left: under the low town lies the old counting house branch, and in its vault the tallies of half the city. find it, raise it and burn it, and the whole sump comes up out of debt in one night. the wet guild says it's a story. they've started counting the vault's windows.",
			"they have a crew now: a kingfisher who can stay under longer than anyone in the guild, a gull who knows every tally clerk's drinking hours and a raven with a branded hand who won't say where they learned to read vault plans. the low town's tide runs lowest at the spring turn. the bell rings for whoever is down there when it does, and this time they intend to be.",
		},
		Quote: "the sea took it first. i'm just taking it second.",
	},
	"bard": {
		Epithet: "a song with a price on it",
		Story: []string{
			"they sang the news at the pot at dawn for the choir: who's climbed, who's fallen, what the kiln keepers are charging, which ship came in. every monday the choir sings the week's names, and the birds named wear the laurel. it's the oldest honour in the city. the nightingales sang the names of everyone who stayed through the first winter, and they have added to the song every night since.",
			"then they wrote a verse of their own, about a gilt family that holds the drowned-bell perch and the debts the family's sons run up in the sump that somehow always end in someone else's tally. it was a good verse. by evening it was being sung in every gin cellar on the seaward side, and by morning the order of the stair had a writ out for the singer.",
			"the order of the stair doesn't hang singers often. it doesn't need to. a writ for a verse is a debt to the tally board for the cost of the insult, set by the family insulted, and the family set it at the price of a drowned-bell perch. it was meant to be a sum nobody could ever pay. it has made the verse the most famous thing in the city.",
			"the feathered crown was a stage piece from the bellward playhouse that burned that same week, nobody says how. they sing in the sump's taverns now, under another name, and the verse keeps travelling. it has grown three new stanzas they didn't write. they want the whole city singing it before the order catches up with them, and they want to be on the pot's steps, with a laurel, the day it's sung there.",
			"the choir has stayed out of it, officially. the choir master still sends a boy down to the sump every week with the monday names, so the singer can learn them before the pot does. last monday the boy brought a second slip. it had one name on it, in the choir master's own hand, and a laurel pressed flat between the folds.",
		},
		Quote: "you can hang a singer. you can't hang a song.",
	},
	"monk": {
		Epithet: "a hearthkeeper who walked out",
		Story: []string{
			"they were raised in the hearthkeepers' house on the mire's edge, where heron novices learn to stand. in their novice year the mire came up again, not the great rising, a small one, and the house sent its novices out into the water as the old herons did. they stood to the knee, then the chest, then the throat, and the water went no higher. the abbot said they held it. they believe they only stood, and that the water stopped by itself.",
			"the hearthkeepers tend the city's hearth by the pot, and they light it only on a day the whole city has earned it. that's the rule, older than the order. every morning before dawn a novice cuts the day just gone into the notch stick and shaves the oldest notch away, and keepers learn to read that stick before they learn to pray. but the gilt's hearths burn every night of the year, and the sump freezes on the days the city falls short. one winter they asked the abbot who the rule was for, and the abbot told them to come back when they understood.",
			"a lean week brings the stick's mark down and the fire comes easier, and a fat week drives it out of reach, so the hearth lights most readily when the city climbs back out of hard times. the abbot calls that mercy. the heron wonders whether it's mercy or bookkeeping, and who in the gilt reads the stick.",
			"they never went back. they walked into the sump with the orange ember cloth every keeper wears still round their neck, and keep small fires there now for anyone who needs one. when they have to fight, it's over before anyone sees them move. what they want is the truth about the hearth: whether the rule is holy, or only bought.",
			"the heron's own fires in the sump burn every night, on coal they buy themselves. the abbot has sent word twice that a fire lit without the count is no hearth. nobody in the sump has asked what to call it.",
		},
		Quote: "stand still long enough and the water shows you the bottom.",
	},
	"knight": {
		Epithet: "a squire of the stair",
		Story: []string{
			"their family held a pale-glass perch for four generations, high enough that the whole bellward could see them. then their grandfather borrowed against it from the counting house, and their mother borrowed against the loan, and the year they hatched, the order of the stair served the writ and the family went down the stair with what it could carry. they grew up in a rented room in the bellward, looking up.",
			"the order of the stair is sworn to hold every rung. in practice the knights guard the gilt, collect perch rents and serve writs on families like theirs. they joined anyway, as the only road up open to a crane with no money, and they wear the order's blue hood. every knight keeps a vigil at the top of the spire once, alone, in the great nothingness. they kept theirs. they came back down and haven't said a word about it, as the order expects.",
			"the vigil is the one thing a knight owns outright: the climb from the lowest gate to where the stair runs out, alone, to stand in the nothing until the drowned bell rings the hour below. most knights come down quieter. a few don't come down, and the order writes their names on the chapter house wall and says nothing more.",
			"last winter they were sent to serve a writ on a shale perch, an old widower who owed the counting house for a roof. they tore up the writ in front of him. the order hasn't decided what to do about them yet. they mean to climb the stair the honest way and win back the family perch, and to find out who it is the order really holds the stair for.",
			"the widower's roof is still his. the counting house has written to the order twice to ask why, and the order has answered neither letter. the crane still collects rents on the shale perches in the blue hood, and keeps every receipt in a box under their bed. one day the box will show who pays the order to hold the stair, and how much, rung by rung.",
		},
		Quote: "i swore to hold every rung. nobody said which way up.",
	},
}
