package pp

// The broadsheet's lines, from LORE.md's "The broadsheet": write them there
// first.

// worldLines are the world's news by season, a line a day from its first.
var worldLines = map[int64][]string{
	1: {
		"the tally board opens the gannet case for the new season. the north sea company's clerk asks for the hull again, and the wet guild opens the purser's tables in it while he talks",
		"company notice: the north sea company pays a finder's fee for any bell metal brought up from under the gannet's bow. the wet guild says there isn't any",
		"the gate book took more names this week than any week since the summer fair. the wardens put it down to the slack tide",
		"the ferrymen counted one step fewer on the fish market stair this morning and pulled their boats up early",
		"debt court: a chandler from the row is called before the tally board for staking a lantern he didn't own. the purser has sent his slate up as evidence",
		"the counting house has sent a second raven to watch the pumps. the first one wrote the eels a finger above the line last night",
		"the bell rang under the gannet's keel again after the hour was over, with nobody at the tables. the purser slept in the hold",
	},
}

// rumours are flavour, any day.
var rumours = []string{
	"they say mother tern boils her coffee with water drawn from below the drowned market, and that's why it keeps you up",
	"a gull on the fish market steps swears the herring came in already salted this morning",
	"the standing book in the gin lanes has a new favourite winter for the gannet to break her back, and it's this one",
	"a jay from the college of heralds was seen in the sump buying dyers' seconds by the armful",
	"the kiln keepers pay sump children to scrape the grey bloom off the cliff houses, and nobody knows what for",
	"someone chalked a name on the beam that nobody at the slack tide remembers throwing",
	"the hedge company is hiring again and paying in advance, which it has never done",
	"a raven was seen at the pot's rim before dawn counting the newcomers asleep against the warm side, twice",
	"the choir is learning a new song for monday and won't say whose names are in it",
	"the north sea company's clerk drinks at the slack tide every night and never sits at a table",
}
