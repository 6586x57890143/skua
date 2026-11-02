package pp

// More of the last wood: the odd birds who live in it, or have stayed too
// long. Appended to the wood's deck and its book at start.
func init() {
	wood.Events = append(wood.Events, woodMore...)
	for k, p := range woodPagesMore {
		pages["wood"][k] = p
	}
}

var woodMore = []Event{
	{
		Key:  "leaves",
		Text: "a jay in a herald's tabard, faded to grey, sits on a low branch of a great oak with a slate and a stub of chalk, counting its leaves. she has a wager with the college of heralds, she says, that she can count every leaf on one tree in the last wood before the autumn takes them. the wardens think it's very funny. she's on her ninth year and the oak keeps growing more.",
		Options: []Option{
			{Wind: Writ, Try: "keep her tally while she counts", Need: 35,
				Pass: Outcome{Text: "you hold the slate and mark the fives while she calls the leaves from the branch above you, one by one, all afternoon. at dusk she rubs the slate out and writes the day's number in a book, and thanks you, and gives you a bead she found in the litter under the oak on her first year and has carried since.", Finds: 1},
				Fail: Outcome{Text: "you lose count at four hundred and twelve and she has to start the branch again. she isn't angry. she says the oak always gets one helper a season who does that."}},
			{Wind: Lot, Try: "bet her she'll lose", Need: 50,
				Pass: Outcome{Text: "you bet her a bead she won't finish this year either. she takes the bet and, because she's a herald and the college keeps every wager in writing, writes it down. a leaf falls into the book while she's writing. she says that settles it, and pays you, and goes back to counting.", Finds: 2},
				Fail: Outcome{Text: "she takes the bet and a leaf falls onto the slate as she's writing it down, and she says that settles it, in her favour, and you can't argue with a herald about a wager."}},
			{Wind: Stray, Try: "count the tree next to hers", Need: 45,
				Pass: Outcome{Text: "you climb the beech beside her oak and start counting its leaves. after an hour she climbs over and checks your branch and says you're out by three, and that it's the best first count she's ever seen, and gives you amber from her pocket for your trouble.", Finds: 1, Mark: "leaves"},
				Fail: Outcome{Text: "you get to the end of the first branch and realise you've been counting the same twig over and over. the jay is watching. she says that happens to everybody, and that she's still not sure it hasn't been happening to her for nine years."}},
		},
	},
	{
		Key:  "burner",
		Text: "in a clearing a crow black with soot from beak to boots is tending a charcoal clamp, a low mound of earth with smoke coming out of it in a dozen places. the wardens let him burn wood, he says, and only one kind: what lightning has struck. he has a licence on bark to prove it. he spends the storms out here, waiting.",
		Options: []Option{
			{Wind: Fold, Try: "help him tend the clamp", Need: 35,
				Pass: Outcome{Text: "you tend the clamp with him all afternoon, stopping the smoke holes with turf whenever the fire inside gets too bright. he tells you about the storm last autumn that took the top off an oak in the deep wood and gave him a month's work. when you go he gives you a bead of amber the lightning baked out of the bark.", Finds: 1},
				Fail: Outcome{Text: "you stop the wrong hole and the clamp breathes out a cloud of white smoke and he has to rebuild a side. he doesn't shout. he says the clamp has a temper, like any fire."}},
			{Wind: Writ, Try: "read his licence",
				Pass: Outcome{Text: "the licence is cut into a strip of bark, sealed with resin, and signed with three wardens' numbers, and it says exactly what he said: what lightning has struck, and nothing else, and the burner to answer to the count for every stick. it's the oldest licence the wardens have issued, he says. he gives you a bead for reading it properly, which nobody does.", Finds: 1}},
			{Wind: Lot, Try: "wait for the next storm with him", Need: 60,
				Pass: Outcome{Text: "a storm comes over the wood at dusk, and you sit in his shelter with him until the lightning hits a beech a quarter of a mile off, and then you run there together. the strike has split the trunk and the resin inside has run and set into amber in the heat, and there's more of it than either of you can carry.", Finds: 3, Mark: "storm"},
				Fail: Outcome{Text: "the storm comes, and goes, and the lightning strikes nothing in the last wood. he says the wood is careful about storms. you go home wet."}},
		},
	},
	{
		Key:  "stayed",
		Text: "at a table under a bark awning that's grown into the trunk behind it sits a raven in the rags of a north sea company clerk's coat, the brass buttons green, a scale on the table and nothing on the scale. he came out to buy amber twenty years ago and got lost in the deep wood for a season, and when he found the edge again he decided he preferred it out here. he still keeps the company's books. nobody collects them.",
		Class: map[string]string{
			"wanderer": "his books go back to the year the kittiwake was lost. he's offered to let you read them before.",
		},
		Options: []Option{
			{Wind: Writ, Try: "ask to see his books", Need: 40,
				Pass: Outcome{Text: "his books are perfect, twenty years of them, every bead bought and every cask shipped, with no buyer and no ship. he still enters the company's price every day from memory. he lets you read whatever you like and gives you a bead for the company's account, and enters it.", Finds: 1, Mark: "books"},
				Fail: Outcome{Text: "he says the books are company property and you'll need a letter from the bellward. then he asks for news of the bellward, and forgets about the books."}},
			{Wind: Fold, Try: "bring him the news", Need: 30,
				Pass: Outcome{Text: "you tell him everything that's happened in the city that you can think of: prices, who's climbed, what the broadsheets sang, the slack tide. he listens to all of it as though it were a cargo manifest and pays you for it in amber at the company's rate for news, which he says is very good.", Finds: 2},
				Fail: Outcome{Text: "you tell him the news and he gets upset at the price of pale glass, which has fallen, and won't hear anything after it."}},
			{Wind: Vaunt, Try: "offer to buy his whole stock", Need: 55,
				Pass: Outcome{Text: "you make him an offer for everything in his strongbox and he's so delighted to have a buyer that he sells it to you for nothing, on credit, and writes you into the company's books as a customer in good standing. there's not much in the box. it's all yours.", Finds: 2},
				Fail: Outcome{Text: "he says the company never sells, it only buys, and that you should know that if you work for it. you've never said you did. he's decided you do."}},
		},
	},
	{
		Key:  "mushrooms",
		Text: "on the stump of one of the stolen oaks, where the wardens hung a black cloth for the hole in the count, an old owl is growing mushrooms, pale brown caps in tiers up the cut, and picking them into a basket. she sells them in the sump, she says, as the gilt's oak, for anyone who wants to eat what the gilt burns. they sell very well.",
		Options: []Option{
			{Wind: Vaunt, Try: "help her sell them in the sump", Need: 50,
				Pass: Outcome{Text: "you carry a basket down the gut with her and cry the gilt's oak at the fish market steps, and the sump buys every cap by noon and laughs about it all week. she pays you in the amber she takes in trade from pickers who've nothing else.", Finds: 2, Mark: "mushrooms"},
				Fail: Outcome{Text: "you carry a basket down the gut and a constable of the order stops you at chandlers' row and asks what the gilt's oak is, and you can't think of an answer that isn't a verse. you sell nothing."}},
			{Wind: Stray, Try: "find the other stumps for her", Need: 45,
				Pass: Outcome{Text: "she only knows four of the stolen oaks' stumps, and you find her two more in the deep wood, each with a black cloth on it. she says it's better than a deed. she gives you a bead from the litter at the second stump, where the cut oak dropped its last.", Finds: 1},
				Fail: Outcome{Text: "the stumps you find all have saplings growing out of them now, with the old numbers on, and no room for mushrooms. she seems pleased about that, on the whole."}},
			{Wind: Fold, Try: "pick with her and ask nothing", Need: 30,
				Pass: Outcome{Text: "you pick with her all morning without asking her anything, which she says is the right way to pick. at the end she gives you a basket of caps to carry and a bead she's been keeping in the bottom of the basket for somebody who did it the right way.", Finds: 1},
				Fail: Outcome{Text: "you pick a cap from the wrong tier and she takes it gently out of your hand and puts it back. it stays on. she doesn't explain, and you don't pick any more."}},
		},
	},
	{
		Key:  "thrush",
		Text: "somewhere off the path a thrush is singing the count song, the wardens' song of every number in the wood, and getting every number wrong. not by much. by one, each time, upward. the wardens in earshot are pretending not to hear. when you find her she's sitting on a stump with her eyes shut, and she says she's singing the count as it will be.",
		Class: map[string]string{
			"bard": "you know the count song now, and she's wrong by one on every line, in a key the wardens don't use.",
		},
		Options: []Option{
			{Wind: Lot, Try: "check one of her numbers", Need: 55,
				Pass: Outcome{Text: "you walk to the tree she just sang, a hundred paces off, and its number is one less than her song. beside it, in the leaf litter, there's a sapling as tall as your hand that wasn't counted last season, with a bead of amber caught in its roots.", Finds: 2, Mark: "thrush"},
				Fail: Outcome{Text: "you walk to the tree she sang and its number is exactly what she sang, not one less. she's stopped singing when you get back, and she's gone."}},
			{Wind: Fold, Try: "sit with her till she stops", Need: 35,
				Pass: Outcome{Text: "you sit by her stump while she sings the whole count, one more at every line, into the evening. when she stops the wood is very quiet. she says you're the first bird in years who didn't tell her she had it wrong, and gives you amber from under the stump.", Finds: 1},
				Fail: Outcome{Text: "she sings for an hour and you fall asleep, and when you wake she's gone and the wood is singing the count back, quietly, in the right numbers."}},
			{Wind: Writ, Try: "tell the wardens",
				Pass: Outcome{Text: "you tell the nearest warden that someone's singing the count wrong. she says she knows, and that they let her sing, because the thrush has never once been wrong about next season. she gives you a bead for asking, and tells you not to ask again."}},
		},
	},
	{
		Key:  "duel",
		Text: "in a clearing two young cranes from the gilt in shirtsleeves are facing each other with smallswords, and two goshawk wardens stand at the edge as their seconds with their arms folded. duelling is a hanging offence on the stair, so the gilt brings its quarrels to the last wood, where the wardens let them fight on condition that nothing is cut, including the duellists.",
		Options: []Option{
			{Wind: Fold, Try: "talk them out of it", Need: 50,
				Pass: Outcome{Text: "you ask what it's about. it's about a verse sung in a gin cellar about one of their sisters. you point out that a duel will put them both in the next verse. after a long silence they put the swords away, and the wardens, very relieved, give you a bead each from the seconds' jar.", Finds: 2, Mark: "duel"},
				Fail: Outcome{Text: "they hear you out and then fight anyway, and nobody is cut, and both claim to have won, and the wardens make them both pay for the moss they trampled."}},
			{Wind: Vaunt, Try: "offer to second one of them", Need: 55,
				Pass: Outcome{Text: "the warden seconding the taller crane hands you his place with enormous gratitude. the duel lasts four passes and ends when your crane's sword lodges in a root. under the wardens' rule that's a draw, and under the gilt's it's a story you'll be told at the pot. the warden pays you in amber for the afternoon off.", Finds: 1},
				Fail: Outcome{Text: "the warden refuses to give up his place. he says a second from the city would get ideas."}},
			{Wind: Lot, Try: "bet on the shorter one", Need: 50,
				Pass: Outcome{Text: "the other second takes your bet. the shorter crane wins in two passes, pricking the other's sleeve without cutting it, which the wardens accept as no cut. the second pays you in amber, scowling.", Finds: 2},
				Fail: Outcome{Text: "you bet on the shorter one and the taller one wins in a single pass. the second takes your bead and gives it to the winner, who doesn't know what to do with it."}},
		},
	},
	{
		Key:  "bees",
		Text: "a wren in a veil of netting is working a row of skeps hung in the hollow of a dead ash, smoking the bees out of each with a smouldering roll of bark. the honey from the last wood comes out dark gold, the colour of amber, and she sells it in the bellward as the only honey the gilt can't buy in bulk, because she only takes what the bees can spare.",
		Options: []Option{
			{Wind: Fold, Try: "hold the smoker for her", Need: 35,
				Pass: Outcome{Text: "you hold the smoker while she lifts the combs, and get stung four times and say nothing about it, which she says is the price of the job. she pays you a comb in a leaf and a bead of amber from inside an old skep, where the bees had walled it up in wax.", Finds: 1},
				Fail: Outcome{Text: "you let the smoker go out and the bees take an interest in you. you spend the rest of the afternoon in the brook."}},
			{Wind: Stray, Try: "follow a bee home", Need: 50,
				Pass: Outcome{Text: "you follow a bee a long way into the trees to a wild nest in a split beech that she didn't know about, the comb hanging out of the split in sheets. she won't take it. she says wild bees are the wood's. under the split, in the litter, there's resin the bees have been gathering, set hard into beads.", Finds: 2},
				Fail: Outcome{Text: "the bee is faster than you are and goes up, and you go into a holly bush."}},
			{Wind: Writ, Try: "buy a jar at her price",
				Pass: Outcome{Text: "you buy a jar at the price she names, which is honest, and she writes you a receipt on a scrap of bark. she says almost nobody pays the first price. she throws a bead of amber into the jar, for luck, and it sinks into the honey and stays there.", Finds: 1}},
		},
	},
	{
		Key:  "scribe",
		Text: "at a desk built into the roots of an oak sits a warden copying the out pages from the gate onto strips of bark, one line for every bird in the wood. she's writing faster than the gate could send them. when you reach her desk she's already writing your line, and she hasn't looked up.",
		Options: []Option{
			{Wind: Writ, Try: "read your own line", Need: 40,
				Pass: Outcome{Text: "she turns the strip round for you. your line is there in her hand: your name, your number, where you're going and which way you lean, and under it, already written, what you're carrying back. it matches what you're carrying. she gives you the bead it says you'll find, from a jar on the desk.", Finds: 1, Mark: "scribe"},
				Fail: Outcome{Text: "she turns the strip round and your line is in a hand you can't read, not the gate's and not a warden's. she turns it back. she says it reads better later."}},
			{Wind: Lot, Try: "ask her to write a different line", Need: 60,
				Pass: Outcome{Text: "you ask her to write you something else. she thinks about it, crosses out the second half of your line and writes a new one, and you go out into the wood and it comes true, every word, including the stand of old ash with amber under it.", Finds: 3},
				Fail: Outcome{Text: "she says she only copies. she says it kindly, and doesn't cross anything out."}},
			{Wind: Stray, Try: "look at someone else's strip", Need: 45,
				Pass: Outcome{Text: "while she's writing you read the strip beside hers, a bird you don't know, out in the deep wood today. it says they'll come back with a bead and give it away at the edge. on the way out you meet them at the edge and they give it to you.", Finds: 1},
				Fail: Outcome{Text: "the strip beside hers is blank, and while you're looking at it she says, without looking up, that it's for the next bird, and that you're standing on their way."}},
		},
	},
	{
		Key: "hollow", Far: true,
		Text: "deep in the wood an oak as wide as a cliff house is hollow, and alive, and lived in: a door is set into the bark with a knocker and a number above it, and smoke comes from a crack in the trunk high up. a crane answers the door in a dressing gown. she's lived in the tree for thirty years, she says, on the wardens' leave, because the tree asked.",
		Class: map[string]string{
			"knight": "she wears the order's ring. she did her vigil at the top of the spire, and came down, and walked out here.",
		},
		Options: []Option{
			{Wind: Fold, Try: "accept her tea", Need: 30,
				Pass: Outcome{Text: "the inside of the tree is a room with a stove and shelves and a staircase going up into the dark of the trunk. she makes you tea from leaves the tree drops. she asks about the city and listens. when you leave she gives you a bead from a bowl on the stove, which the tree weeps into through a crack in its wall.", Finds: 1},
				Fail: Outcome{Text: "she offers tea and the stove has gone out, and she can't get it lit, and she says the tree doesn't want it lit today. you stand in the doorway and talk instead."}},
			{Wind: Stray, Try: "climb the stair inside", Need: 55,
				Pass: Outcome{Text: "she lets you climb the stair in the trunk, up and up past rooms she doesn't use, to a window in the bark near the top where the tree has split. you can see the spire from there, and the top of it, which you can't see from anywhere in the city. on the sill there are beads of amber, a good many.", Finds: 3, Mark: "hollow"},
				Fail: Outcome{Text: "the stair goes up further than the tree is tall. you come down when your legs give out and she's waiting at the bottom with the tea."}},
			{Wind: Writ, Try: "ask how the tree asked",
				Pass: Outcome{Text: "she says it asked the way the wardens' count is asked: by being there every season and needing someone. she says it plainly, as she would say the rent was due. she gives you a bead from the stove bowl and doesn't elaborate."}},
		},
	},
	{
		Key: "sledges", Far: true,
		Text: "in a hollow full of shavings a carpenter, a magpie with a pencil behind one ear, is building sledges from dropped wood: runners of fallen ash, beds of beech the wind brought down. he builds for anyone who pays, he says. the wardens buy them for hauling the dropped wood out. so do the oak thieves. he doesn't ask what a sledge is for. the wardens know he doesn't.",
		Class: map[string]string{
			"ranger": "his mark is on the runners in the sledgers' camp in the mire. you've counted them.",
		},
		Options: []Option{
			{Wind: Writ, Try: "ask who his other buyers are", Need: 45,
				Pass: Outcome{Text: "he keeps a book. he shows it to you because nobody has ever asked properly. four sledges went to a raven in a good coat last autumn, paid in pp from a bellward bank. he gives you the bead the raven left as a tip, because he says he doesn't want it in the shop.", Finds: 1, Mark: "buyer"},
				Fail: Outcome{Text: "he says his buyers are his business, the way their sledges are theirs, and goes back to planing a runner."}},
			{Wind: Vaunt, Try: "commission a sledge of your own", Need: 50,
				Pass: Outcome{Text: "you commission one, for a deposit, and he builds it while you wait, a small light sledge of dropped ash. it's very fine. you've nothing to haul. he says that's the best reason to own one, and loads it with the beads he keeps for buyers' children.", Finds: 2},
				Fail: Outcome{Text: "he says he's booked through the winter, mostly by people who haul at night, and that you can wait."}},
			{Wind: Lot, Try: "ride a finished sledge down the hollow", Need: 60,
				Pass: Outcome{Text: "you take a finished sledge to the top of the hollow and ride it down the shavings, and it runs true all the way to the bottom and out into the litter, where it stops against a fallen beech whose split is full of amber. he says that's the test. he gives you what the beech had.", Finds: 3},
				Fail: Outcome{Text: "the sledge goes down the hollow without you, very straight. he says that's also the test, and that it passed."}},
		},
	},
	{
		Key: "sleeper", Far: true,
		Text: "under a beech in the deep wood a gull in a city coat is asleep, curled in the roots, with moss on his boots. the wardens say he's been asleep since the spring. they count him every season with the trees, he has a number painted on a strip of bark beside him, and they move him a little each time the count moves, so the trees don't grow round him.",
		Options: []Option{
			{Wind: Fold, Try: "sit with him a while", Need: 35,
				Pass: Outcome{Text: "you sit by him for an hour. he talks in his sleep, about prices in the gut, a quarrel with his landlord, a girl at mother tern's, and once, clearly, where he put the amber he picked before he lay down. it's under the next root. you leave him a bead of it.", Finds: 1},
				Fail: Outcome{Text: "you sit by him until you're nearly asleep yourself, and a warden comes and moves you along, politely, and says one is enough."}},
			{Wind: Lot, Try: "try to wake him", Need: 65,
				Pass: Outcome{Text: "you shake his shoulder and he opens his eyes and says, very clearly, that it's not time, and shuts them again. the warden who saw it writes it down on bark, the first word he's said since spring. the wardens give you a jar of the beads they've picked round him.", Finds: 3, Mark: "sleeper"},
				Fail: Outcome{Text: "you shake his shoulder and he doesn't wake, and the moss on his boots is warm, and you stop."}},
			{Wind: Writ, Try: "read his number",
				Pass: Outcome{Text: "his number on the bark strip is four thousand and three, which is the number after the tree he's lying under. the wardens have counted him as a tree. it's a mark of respect, one of them tells you, and gives you a bead from the jar they keep beside him for the birds who come to look."}},
		},
	},
	{
		Key: "owl", Far: true,
		Text: "in the oldest part of the wood, at a table set out in the leaf litter with a cloth and two chairs, an owl in spectacles is playing chess against nobody. the other side's pieces move when she isn't looking at them. she says she's been playing the same game for six years and is two moves from losing, and she'd like a second opinion.",
		Options: []Option{
			{Wind: Vaunt, Try: "take over her side", Need: 60,
				Pass: Outcome{Text: "you sit down and play her side. the other side's pieces move while you're thinking, slowly, when you look at the trees. after an hour it's a draw by repetition, the first draw anyone has made, and the other side's king lies down. she gives you the amber she's been using as pawns.", Finds: 3, Mark: "chess"},
				Fail: Outcome{Text: "you move a knight and the other side mates you in one, and she says, kindly, that it's been doing that for six years."}},
			{Wind: Stray, Try: "watch the other side move", Need: 45,
				Pass: Outcome{Text: "you watch the empty chair instead of the board. after a long time a branch moves overhead, and a shadow crosses the board, and a bishop moves two squares. when you tell her she laughs and says the wood plays the long game, and gives you a bead.", Finds: 1},
				Fail: Outcome{Text: "you watch the empty chair until your eyes water and nothing moves, and when you look down at the board, it has."}},
			{Wind: Writ, Try: "write down the game",
				Pass: Outcome{Text: "you write the game down move by move from her score sheet, six years of it, so she has a fair copy. the other side's moves are all good. she says the copy will go to the choir's library, which keeps games, and pays you in amber for the copying."}},
		},
	},
}

var woodPagesMore = map[string]string{
	"leaves":    "the college of heralds keeps every wager made by one of its jays in writing, and pays or collects on every one, however long it takes, because a herald's word is a herald's trade. nine years ago a junior herald wagered the college that she could count every leaf on a single tree in the last wood before the autumn took them. the college took the bet at long odds and she walked out to the wood the same week with a slate and a book. she hasn't won yet. the oak she chose keeps growing leaves faster than she can count them, which the wardens say is because it knows. her tabard has faded to grey. the college still sends her pay every quarter, which arrives by pigeon and which she spends on chalk. she's kept the count for nine summers in a book that's now three books, and the wardens have asked to copy it into the counting trees' hollow, because nobody has ever counted leaves before and they'd like to know.",
	"burner":    "nothing may be cut in the last wood and nothing may be burned but what has fallen, and even dropped wood is the wardens' to give. the charcoal burner holds the oldest licence the wardens have ever issued: to burn what lightning has struck, and nothing else. he spends the storms in the wood and the fine weather in his clamps, low mounds of turf over a stack of struck wood, smoking for days, and sells the charcoal in the sump to the blacksmiths, who say it burns hotter than any other because the sky lit it first. he's black with soot from beak to boots and has been for forty years. the wardens trust him more than any bird from the city because he has never once burned a stick he couldn't account for. the kiln consortium offered him a fortune to burn for them. he asked whether their kilns had been struck by lightning, and when they said no, he said he was very sorry.",
	"stayed":    "the north sea company sends two clerks to the wood's edge each autumn to buy amber, and twenty years ago one of them, a raven, went into the deep wood to find a better price and was lost there for a season. when he found the edge again he looked at the causeway back to the city for a long time and then built an awning against a trunk and set up his table and stayed. he still keeps the company's books, perfectly, every day: the price from memory, every bead offered and every cask he would have shipped. there's no buyer and no ship. the company wrote him off as lost and paid his widow his insurance. she knows where he is and visits in the spring. his books are the only complete record anybody has of the amber trade for twenty years, and the counting house would very much like to read them, and he's told the counting house it'll need a letter from the bellward.",
	"mushrooms": "the stolen oaks' stumps are holes in the count, each hung with a black cloth by the wardens, and the old owl who grows mushrooms on them has the wardens' leave to do it, because a mushroom isn't cut and isn't timber. she grows them in tiers up the sawn faces, pale brown caps that taste of smoke, and carries them down the gut to the sump's markets in a basket and sells them as the gilt's oak. the sump buys every one. it eats what the gilt burns, it says, and some weeks it's the only oak the sump will see. the order of the stair has twice tried to stop her selling them, on the grounds that the name is an insult to a family, and twice failed to say which family without naming the oak's buyer. she's started selling dried caps in paper twists with the stump's number written on them, and they've become something the sump gives at weddings.",
	"thrush":    "the thrush sings the count song, the wardens' song of every number in the last wood in order, and gets every number wrong by one, upward. the wardens don't stop her. they used to, when she started, and then the next season's count came in and every number she'd sung was right: one more tree at every one, saplings nobody planted. now the wardens listen to her each autumn with their arms folded and their faces carefully blank, and set out their paint pots where she sings. she says she isn't predicting anything. she says she's singing the count as it is, and that everybody else is a season behind. she won't sing in the city, though the choir has asked her three times. she says the city's count is a long way behind, and she'd only be singing it numbers that would make it unhappy.",
	"duel":      "duelling is a hanging offence on the stair and has been since a gilt family lost four sons in one spring, so the gilt brings its quarrels to the last wood, where the order of the stair has no writ and the wardens let them fight on one condition: nothing is cut, including the duellists. a warden stands second to each side and watches. a duel ends at first blood, first cut moss or first damaged bark, whichever comes first, and the side that does the damage loses, whoever it was cut. so the duels are very careful and very long and almost nobody is ever hurt, and the wardens charge each side for any moss trampled. the gilt considers this deeply humiliating and keeps coming. the wardens say the gilt's quarrels have kept the edge of the wood mown for a hundred years, and that the clearing where they fight has the best moss in the wood, because it's paid for.",
	"bees":      "the wren who keeps bees in the last wood keeps them in skeps hung in dead trees, where nothing needs to be cut, and takes only what the bees can spare. the honey comes out dark gold, the colour of the amber in the litter, and tastes of resin and smoke. she sells it in the bellward by the jar at a price she sets once a year and never moves, and it's the only honey in the city the gilt can't buy in bulk, because she won't sell more than she has and she won't have more than the bees can spare. the gilt has tried to buy her hives outright, and to buy her, and to buy the dead trees, and the wardens have declined on the trees' behalf. the bees, she says, take resin from the split trunks to wall their combs, and sometimes wall a bead of amber into the wax, and when she finds one in a skep she keeps it for a customer who paid the first price without arguing.",
	"scribe":    "every evening the wardens copy the gate's out pages onto bark, so the wood keeps its own count of who's in it, and the warden who does the copying sits at a desk in the roots of an oak at the edge with a stack of bark strips and a pot of resin ink. she copies faster than the gate writes. the wardens say that's because she's been doing it a long time. the birds who've been to her desk say she writes their line before they reach it, and writes what they'll carry back before they've found it, and is never wrong. she says she only copies. she's never said what she copies from. the strips go into the counting trees' hollow at the end of each season, bundled by the day, and the wardens have a strip for every bird who ever went out by the north gate, which is more strips than the gate book has lines, because some birds she writes were never written at the gate.",
	"hollow":    "the hollow oak in the deep wood is the only tree in the last wood that's lived in, and the wardens gave the leave thirty years ago, because, they say, the tree asked. the crane who lives in it did her vigil at the top of the spire as a knight of the order, stood in the nothing until the drowned bell rang, came down the stair and walked straight out of the north gate and across the causeway without stopping at the chapter house. the order wrote her name on the wall of those who didn't come down, which is the custom for a knight who doesn't report back, and she's never asked them to take it off. she lives in the trunk with a stove and shelves and a staircase that goes up further than the tree is tall, and burns only what the tree drops, and the tree weeps amber into a bowl on her stove through a crack in its wall. from the window at the top you can see the spire's top, which you can't see from anywhere in the city.",
	"sledges":   "the sledge maker in the deep wood builds from dropped wood only, with the wardens' leave: runners of fallen ash, beds of wind-felled beech, pegged and never nailed. his sledges are the best in the city and he builds for anyone who pays. the wardens buy them to haul dropped wood out to the shelters. the oak thieves buy them too, and he's never refused a buyer, and never asked what a sledge was for, and the wardens know he doesn't ask, and still buy from him. he keeps a book of every sale, in a clear hand, with the buyer and the price, and he'll show it to anybody who asks properly, which nobody from the city had until lately. he says a sledge maker who refuses buyers is a sledge maker who decides what sledges are for, and he isn't one. he says that's the wardens' work, and that he trusts them to do it.",
	"sleeper":   "the gull asleep in the deep wood lay down under a beech one spring evening with a bag of picked amber and a city coat and hasn't woken since. the wardens checked on him for a week, then decided he was a matter for the count. they painted him a number on a strip of bark beside him, the one after the tree he's under, and count him every season with the trees, and move him a little each time the count moves so the roots don't grow round him. moss has grown on his boots. he talks in his sleep, about ordinary things: rents, prices in the gut, a quarrel with his landlord, a girl at mother tern's. his landlord has re-let his room. the girl at mother tern's comes out once a month and sits with him, and says he's never looked better. the wardens keep a jar beside him for the beads they pick up nearby, for the birds who come to look at him, because they say a sleeper should pay his way.",
	"owl":       "in the oldest part of the last wood an owl has been playing the same game of chess for six years, at a table set out in the leaf litter with a cloth and two chairs, against an opponent nobody can see. the other side's pieces move when nobody is looking at them, and the moves are good, slow and patient, and the owl has been losing by degrees for six years and says she's never enjoyed anything more. she was the choir's chess master before she came out, and the choir's library keeps games, and she sends it a fair copy of the game each spring, and the library has started a shelf. the wardens say the wood plays and won't say more. the owl says she's two moves from losing and has been for a year, because her opponent seems to be in no hurry to win, and she's started to suspect it doesn't want the game to end either.",
}
