package pp

// SigningLore is what each class learns signing in company (LORE.md,
// chirographs and round robins), in the order it's learned: at the first
// chirograph, at the tenth signature and at the first round robin closed
// with all four places filled. signing_lore_test holds it to LORE.md;
// write it there first.
var SigningLore = map[string][3]string{
	"wanderer": {
		"the board's clerk is an albatross too, and asks the same question at the rim that everyone from the grey north asks.",
		"the north sea company buys up chirographs signed by albatrosses, because a bird who signs in company can be found again.",
		"the kittiwake sailed with a round robin pinned under her mast. nobody at the company will say what it asked for.",
	},
	"fighter": {
		"the clay is from the mire's edge, and it only sets on a shrike's thumb.",
		"the hedge company pays a widow's portion on every thumb it can match to a squad roll, and the kiln keepers pay more for the same rolls.",
		"the squad that held the eastern hedge in the rising signed a round robin the night before. four thumbs. one came back.",
	},
	"ranger": {
		"the wardens number birds from the north gate inward, and the rookery has passed nine thousand.",
		"the last wood has one more tree in the count than in the wood, and every warden knows which.",
		"wardens on a cold trail sign in fours, one at each point of the compass, so whatever comes can't come from behind.",
	},
	"sorcerer": {
		"the brand is the house's eye over a ledger line, and the line is a real entry in a real book.",
		"the ravens who fit the halves together are the reckoners who struck them off. they say nothing. they pay.",
		"the counting house was founded on a round robin, and the founding ledger has four signatures and five hands.",
	},
	"thief": {
		"the rings are the drowned's, and the wet guild returns any a family asks for, for a fee.",
		"one ring has come back from the board three times with a different magpie's name on it.",
		"the chapel bell was brought up under a round robin. the fourth signature was the bell ringer's, and he went down with the low town.",
	},
	"bard": {
		"the bar is from the song the choir sang through the first winter, a note longer for every name.",
		"the tally board has fined a bard for a bar of music signed under someone else's notice. the bard paid in verse.",
		"four nightingales signing together make a chord, and the choir won't say which.",
	},
	"monk": {
		"the ash is from the hearth, and the hearthkeepers know it on sight.",
		"the herons sign nothing in their order. they sign the board's notices because the board feeds the sump.",
		"in the rising, four herons stood in the water at the four corners of the hedge and didn't sign anything. the notice they were answering is still on the board.",
	},
	"knight": {
		"every writ the order serves is dated by the bell, and so is every knight's vigil.",
		"a knight who signs in company has to enter it at the chapter house, and the chapter house reads every entry.",
		"the order's founding oath was a round robin of four cranes. three of the names are on the chapter house wall, among those who didn't come down.",
	},
}

// signingWhen is when each piece is learned, as the lore card says it.
var signingWhen = [3]string{
	"the first time you sign a chirograph",
	"at your tenth signature",
	"the first time you close a round robin with all four places filled",
}

// learned is which pieces of their class's signing lore s has taught.
func learned(s signs) [3]bool {
	return [3]bool{s.chirographs >= 1, s.signatures >= 10, s.rings >= 1}
}
