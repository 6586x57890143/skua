package pp

// The dyers' yard leases liveries by the week: a sump trade's colours from
// the tallow perch and any faction's from the shale perch (LORE.md). Each
// is one role a server, shared by everyone who wears it and made the first
// time someone leases it.
//
// Every colour sits in one band of brightness, so a name in it reads on
// Discord's light themes and its dark ones alike (at least 3.3 to 1
// against white and against the darkest grey a member list is drawn on),
// and the hues are the portraits' own, kept muted.

// dye is one livery the yard sells.
type dye struct {
	key, name string // name is the role's, as the member list shows it
	color     uint32
	rung      int64 // the perch it's worn from
	// price is a week's lease in hundredths of a pot day.
	price               int64
	what, story, effect string
}

// The pages of the yard: the sump's trades, then the factions.
const (
	tradesPage, factionsPage = 0, 1
)

var dyes = []dye{
	{"tallow", "chandlers' tallow", 0xa2803e, 1, 50, "a smock dyed the yellow of poured fat",
		"the chandlers render what the yards send them and pour it into candles, lamps and, once a winter, a perch. the dye is boiled from the same vats and never quite loses the smell. a bird in tallow yellow is welcome at any back door in the sump, because everyone owes the chandlers for light",
		"your name wears tallow yellow in the server for a week"},
	{"hide", "tanners' hide", 0xbe715d, 1, 50, "an apron the red of a fresh hide",
		"the tanning pits sit behind the rendering yards where the wind carries their smell up the stair to the gilt, and the gilt pays the tanners to keep the lids on. the red comes from the bark they steep the hides in. it doesn't wash out, which the tanners consider a recommendation",
		"your name wears hide red in the server for a week"},
	{"bruise", "the pits' bruise", 0x9d78a9, 1, 50, "a sash in the colour of a three day bruise",
		"the fighting rings under the gin cellars have no guild and no hall, only a colour, and the bookmakers wear it so the winners know whom to find. fighters are never given the sash. they earn the colour on their own",
		"your name wears bruise in the server for a week"},
	{"juniper", "gin lane juniper", 0x5c916a, 1, 50, "a waistcoat dyed with spent berries",
		"the stills in the gin lanes burn through juniper by the cartload, and the dyers buy what comes out the other end for a copper a barrel. the green is cheap, the smell is not, and half the sump wears it on a saturday night",
		"your name wears juniper in the server for a week"},
	{"tidewater", "ferrymen's tidewater", 0x5f8aa6, 1, 50, "an oilskin the grey blue of the sump at slack water",
		"the ferrymen row the flooded streets of the sump and, at low tide, the roofs of the low town. they charge by the street and double past the drowning line. the colour is the water's, and they claim the water is theirs too",
		"your name wears tidewater in the server for a week"},
	{"pewter", "fish market pewter", 0x81868b, 1, 50, "a coat the grey of herring scales",
		"every board in the fish market is crusted with scales, and every coin that crosses one comes away grey. the fishwives took the colour as their own a century ago and the counting house has been trying to buy it back since",
		"your name wears pewter in the server for a week"},

	{"ink", "counting house ink", 0x887fb2, 2, 100, "a sleeve cuff stained with the house's ink",
		"the ravens mix their ink from oak gall and something they keep in a locked room, and it never fades from a ledger. a clerk's cuffs go this colour within a year. wearing it unearned is not a crime, but the house keeps a list",
		"your name wears counting house ink in the server for a week"},
	{"stair", "stair blue", 0x6286c3, 2, 100, "a tabard in the blue of the order's mantles",
		"the order of the stair dyes its mantles from woad grown on one terrace of the gilt, and the dye is counted out by the ounce. the blue says a bird has business on the stair. the knights don't stop anyone in it, but they remember them",
		"your name wears stair blue in the server for a week"},
	{"thorn", "hedge company thorn", 0x708e5b, 2, 100, "a field coat in the green grey of the hedge",
		"the shrikes dye their coats to vanish against the thorn, and a company coat has been on the hedge long enough to smell of it. the company sells the colour to anyone, and has a standing offer of a contract for anyone who wears it well",
		"your name wears thorn in the server for a week"},
	{"sea", "wet guild sea green", 0x4a9183, 2, 100, "a diver's collar the green of shallow water over stone",
		"the magpies wear it on the boats so the divers below can find them through the water. ashore it means a bird knows where to sell something without questions. half the bellward's furniture came up under a sea green collar",
		"your name wears sea green in the server for a week"},
	{"ember", "hearth ember", 0xc66e34, 2, 100, "a scarf the colour of a banked fire",
		"the herons tie an ember scarf on every bird they feed through a hard winter, and the sump remembers who wore one. the dyers sell the colour on the herons' terms: the lease pays for a bowl at the hearth on a cold night",
		"your name wears ember in the server for a week"},
	{"rose", "choir rose", 0xb1738a, 2, 100, "a ribbon in the rose of the playhouse curtains",
		"the nightingales sing the week's names every monday from behind a rose curtain, and the colour has meant the theatre ever since. it is worn by actors, broadsheet singers and anyone who wants to be mistaken for one",
		"your name wears choir rose in the server for a week"},
	{"moss", "wardens' moss", 0x818b42, 2, 100, "a hood dyed with moss from the last wood",
		"the goshawks dye their hoods with moss scraped from trees they have counted, and every hood is entered in the tally of the wood. the city finds the colour unwelcome, which the wardens take as proof it works",
		"your name wears moss in the server for a week"},
	{"glass", "kiln glass", 0x618c92, 2, 100, "a mantle with the pale cast of mire glass",
		"the kiln keepers blow their glass from peat ash and lamp smoke, and the dye is the same ash steeped in lye. the colour is the one the gilt sees through its windows every morning, and the kiln keepers make sure the gilt knows who to thank",
		"your name wears pale glass in the server for a week"},
}

// dyeOf is the livery with key, and whether there is one.
func dyeOf(key string) (dye, bool) {
	for _, d := range dyes {
		if d.key == key {
			return d, true
		}
	}
	return dye{}, false
}

// page is the yard page d is sold on.
func (d dye) page() int {
	if d.rung > 1 {
		return factionsPage
	}
	return tradesPage
}
