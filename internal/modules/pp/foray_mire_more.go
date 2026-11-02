package pp

// More of the mire: the odd birds who live out there, or think they do.
// Appended to the mire's deck and its book at start.
func init() {
	mire.Events = append(mire.Events, mireMore...)
	for k, p := range mirePagesMore {
		pages["mire"][k] = p
	}
}

var mireMore = []Event{
	{
		Key:  "toll",
		Text: "a plank bridge crosses a ditch in the reed that you'd step over without one, and on a stool at the near end sits an old grey heron with a cash box on his knees. the toll is one pp, he says, or a true thing about yourself. the cutters say the bridge is never in the same ditch two days running. the heron says the cutters move.",
		Options: []Option{
			{Wind: Writ, Try: "pay the toll in pp",
				Pass: Outcome{Text: "you pay. he writes a receipt in a hand older than the counting house's and gives it to you, and you cross a ditch you could have stepped over. on the far side there's a stack of cut peat with a sign on it in the same hand: toll paid, take one.", Finds: 1}},
			{Wind: Fold, Try: "pay him in a true thing", Need: 40,
				Pass: Outcome{Text: "you tell him something true about yourself that you haven't told anyone in the city. he listens with his eyes shut, nods, writes it down in a ledger with no other entries, and lets you cross. he gives you a brick from under the stool. it's the first true thing, he says, that anyone's paid in years.", Finds: 2, Mark: "true"},
				Fail: Outcome{Text: "you tell him something true and he shakes his head before you've finished. it's true, he agrees. it's not about you. he lets you cross anyway, out of pity, which is worse."}},
			{Wind: Stray, Try: "step over the ditch", Need: 45,
				Pass: Outcome{Text: "you step over the ditch a yard from the bridge. the heron doesn't look up. on the far side the reed is cut and stacked as though somebody expected you to come this way and not the other, and there's peat at the foot of the stack.", Finds: 1},
				Fail: Outcome{Text: "the ditch is wider than it was when you started across it. you go in to the knee and climb out on the near side again, and the heron holds out his cash box without a word."}},
		},
	},
	{
		Key:  "raft",
		Text: "a raft as big as a cliff house's floor is drifting through the reed with a tavern on it: a shed of tarred planks, a chimney, three tables and a bar, and a cormorant behind the bar polishing a glass. a board says the raft takes no pp. a drink costs a story, and the cormorant decides whether it was one.",
		Class: map[string]string{
			"bard": "she knows you. you sang here once, years ago, and the raft was somewhere else entirely.",
		},
		Options: []Option{
			{Wind: Vaunt, Try: "tell the best story you have", Need: 50,
				Pass: Outcome{Text: "you tell it well, and the two cutters at the corner table stop talking to listen. the cormorant pours you something brown and warm, and when you leave she puts a brick of peat on the bar beside your glass. the raft's own, she says. it burns blue.", Finds: 2, Mark: "raft"},
				Fail: Outcome{Text: "she hears your story out and pours you water. it was an anecdote, she says, not a story, and the raft has standards."}},
			{Wind: Fold, Try: "listen to the other tables", Need: 30,
				Pass: Outcome{Text: "you buy nobody a drink, because nobody can, and sit at the end of a table while an eel man tells a cutter about the year the eels came up the gut into the sump. at the end of it the cutter gives the eel man a brick of peat, and the eel man gives it to you, because you listened best.", Finds: 1},
				Fail: Outcome{Text: "the stories at the other tables are in a cutters' cant you can't follow. you sit through three of them nodding."}},
			{Wind: Lot, Try: "stay aboard while it drifts", Need: 55,
				Pass: Outcome{Text: "you stay aboard and the raft drifts, slow and sure, for two hours, through reed you've never seen and out across a stretch of open water, and grounds by an old cutting heaped with stacked peat nobody has worked in your lifetime. the cormorant lets you take what you can carry.", Finds: 3},
				Fail: Outcome{Text: "you stay aboard and the raft drifts the wrong way, deeper in, and when it grounds the cormorant says this is as far as it goes today. you walk back."}},
		},
	},
	{
		Key:  "surveyor",
		Text: "standing up to his waist in black water, a long way from any cutting, a gull in a consortium surveyor's coat is sighting through a brass instrument on a tripod at a pole nobody is holding. the coat is green with algae. he says, without looking round, that the eel men told him the run was shallow, and that was three years ago, and he still has the eastern line to finish.",
		Options: []Option{
			{Wind: Writ, Try: "hold the pole for him", Need: 35,
				Pass: Outcome{Text: "you hold the pole where he tells you and he sights it and writes a number in a book so wet it's pulp, and thanks you, and says the line is done. he hands you the consortium's surveying fee, which the consortium has paid him in peat for three years without his going back to collect it.", Finds: 2},
				Fail: Outcome{Text: "you hold the pole and he shouts that it isn't level, and it isn't, and nothing you do with it makes it level. after an hour he tells you to go and send someone from the consortium."}},
			{Wind: Lot, Try: "tell him the consortium sold the line", Need: 50,
				Pass: Outcome{Text: "you tell him the cutting was sold, and the consortium has a new surveyor, and nobody is waiting for the eastern line. he takes his eye from the instrument for the first time in three years. then he picks up his tripod and wades past you toward the hedge, and leaves you the peat stacked on his hummock.", Finds: 2, Mark: "surveyor"},
				Fail: Outcome{Text: "he says you're lying and that the consortium would have sent word. he goes back to the instrument. you leave him sighting at the pole."}},
			{Wind: Stray, Try: "look where he's sighting", Need: 45,
				Pass: Outcome{Text: "you wade out to where his instrument points. there's no pole there. there's a stake under the water with a brass cap on it, stamped with the old counting house branch's mark, and beside it an old cutting's last stack, still sound.", Finds: 1},
				Fail: Outcome{Text: "you wade out to where the instrument points and the water goes deeper and deeper and you turn back. the surveyor says that's what the eel men said, and they were wrong."}},
		},
	},
	{
		Key:  "deeds",
		Text: "on a big mat at the edge of the reed a snipe in a gentleman's coat is planting moss. she plants it in neat rows, pressing each tuft into the mat with her thumb, and every few yards she drives in a little stake with a painted plot number. she sells the plots in the bellward, she says, as land. the mat is growing, so there's always more of it.",
		Options: []Option{
			{Wind: Vaunt, Try: "buy a plot on the spot", Need: 50,
				Pass: Outcome{Text: "you buy plot forty-one for the price of a brick of peat, and she writes you a deed on good paper with a seal, and your name on it, and the plot's place on a map of the mat. it's very fine. you own land in the mire. she throws in a second brick, since buyers so rarely come to see the plot.", Finds: 1, Mark: "deed"},
				Fail: Outcome{Text: "the plot she shows you sinks while she's writing the deed. she starts again on plot forty-two without comment."}},
			{Wind: Writ, Try: "ask to see her licence",
				Pass: Outcome{Text: "she has one. it's from the tally board, for the sale of land in the city's possession, and it's perfectly genuine, and the mat is in the city's possession because it's below the hedge. she shows you the board's seal, and gives you a brick so you'll go away before you think of the next question.", Finds: 1}},
			{Wind: Fold, Try: "help her plant a row", Need: 35,
				Pass: Outcome{Text: "you plant a row with her, tuft by tuft, and she tells you the plots sell best to bellward families who want land their grandchildren can't lose at the tables. she means it kindly. she pays you in peat cut from under the oldest plots, where the mat is thickest.", Finds: 2},
				Fail: Outcome{Text: "you plant a row too close to the edge and it tears off and floats away into the reed with three plots on it. she writes them off in her book as sold."}},
		},
	},
	{
		Key:  "lamplighter",
		Text: "at dusk an old moorhen in a lamplighter's coat walks out across the reed on stilts with a long brass pole, and where she touches its tip to the water a small light comes up and drifts off against the wind. the mire's lamps. she says she's lit them every night for sixty years and that nobody has ever paid her, and she'd like a word with whoever's meant to.",
		Options: []Option{
			{Wind: Fold, Try: "walk her round", Need: 35,
				Pass: Outcome{Text: "you walk beside her stilts with her bag of wicks while she lights the round, one light at a time, and she tells you the name of every cutter's child she's lit a lamp for. at the end of the round she gives you the peat she keeps in the bag to warm her hands.", Finds: 2},
				Fail: Outcome{Text: "you can't keep up with the stilts, and she leaves you behind in the reed at the third light. you see the round go on without you, small lights coming up across the dark."}},
			{Wind: Writ, Try: "write her claim for the pay", Need: 40,
				Pass: Outcome{Text: "you write out her claim for sixty years' wages for lamplighting on the city's behalf, on the back of your out pages line, and she signs it with a thumb of ash. the warden at the gate reads it, frowns, and files it in the gate book. she pays you in advance for the trouble, in peat.", Finds: 1, Mark: "claim"},
				Fail: Outcome{Text: "you start to write the claim and realise you don't know who employs her, or whether anybody does. neither does she."}},
			{Wind: Lot, Try: "borrow the pole and light one", Need: 60,
				Pass: Outcome{Text: "she hands you the pole. you touch it to the water, and a light comes up, and goes, and you have no idea how. she says you have the knack, which is bad news, because the knack is for life. she gives you a brick for your first.", Finds: 1, Mark: "lit"},
				Fail: Outcome{Text: "you touch the pole to the water and nothing happens, and nothing happens, and then something happens a long way behind you, out in the reed. she takes the pole back quickly."}},
		},
	},
	{
		Key:  "debtor",
		Text: "something in the reed moves when you stop, and when you look it's a bird in tally mine grey with a chain round one ankle and the chain cut through. a debtor. a lapwing, thin as a reed, who came up out of the lower reaches on a night shift and walked into the mire and has been out here since. she wants a message carried to the sump. she has nothing to pay you with but peat.",
		Options: []Option{
			{Wind: Fold, Try: "carry her message", Need: 30,
				Pass: Outcome{Text: "the message is for her sister at mother tern's: that she's alive and not to pay her tally. you carry it. you don't ask. she presses two bricks on you from the stack she's been cutting to keep warm and goes back into the reed before you can say she needn't.", Finds: 2, Mark: "message"},
				Fail: Outcome{Text: "you say yes and then she won't tell you the message, because she's decided she can't trust you with it after all, and she's right not to trust anybody. she goes back into the reed."}},
			{Wind: Writ, Try: "tell her to go back and serve her tally",
				Pass: Outcome{Text: "you tell her the tally board will find her, and a debt only grows while it's run from. she knows. she listens to every word and thanks you and goes deeper into the reed. you find a brick of peat on your path afterward, cut and left for you, which is more than you'd have done."}},
			{Wind: Stray, Try: "show her where the eel men camp", Need: 45,
				Pass: Outcome{Text: "you take her to the eel men's weir, where nobody asks a bird where they came from as long as they'll carry traps. the eldest gives her a pair of stilts. she gives you the peat she's been carrying, all of it, since she won't need it now.", Finds: 3},
				Fail: Outcome{Text: "you can't find the weir again in the dusk, and she won't stay out in the open with you while you look. she goes back into the reed alone."}},
		},
	},
	{
		Key:  "inkwell",
		Text: "a coot child at the edge of the cutting has a bottle of black ink she dug out of the peat, and a scrap of paper, and the ink is writing on the paper by itself, slowly, in a clerk's hand, a line at a time. it's an account. the child can't read. she wants to know what it says before her mother sees it and throws it back.",
		Class: map[string]string{
			"sorcerer": "it's counting house ink, the old iron gall. the hand is a reckoner's. you know the hand.",
		},
		Options: []Option{
			{Wind: Writ, Try: "read it to her", Need: 40,
				Pass: Outcome{Text: "it's a peat account, cutting by cutting, a season's worth, with a line at the bottom that doesn't close. the ink stops writing when you reach it. you tell the child it's a list of who cut what and that her grandmother's name is on it twice, which is true. she gives you a brick from her mother's barrow for reading.", Finds: 1, Mark: "ink"},
				Fail: Outcome{Text: "you start to read it aloud and the ink starts to write faster than you can read, and then it's writing your name. you hand it back to the child and tell her to give it to her mother."}},
			{Wind: Lot, Try: "ask it a question", Need: 55,
				Pass: Outcome{Text: "you write a question under the last line in the ink's own bottle: where's the rest. the ink thinks for a while and then writes, in the same hand, a cutting's name and a step. you go there. under the step there's a stack of peat nobody put on any board.", Finds: 3},
				Fail: Outcome{Text: "you write a question under the last line and the ink crosses it out, neatly, with a single rule, the way a reckoner crosses out an entry that doesn't belong."}},
			{Wind: Fold, Try: "take it to her mother together", Need: 30,
				Pass: Outcome{Text: "her mother looks at the paper and doesn't throw it back. she folds it and puts it in her coat, and says it's the first time the bog has given the cutters anything in writing that was on their side. she gives you two bricks off the top of her stack.", Finds: 2},
				Fail: Outcome{Text: "her mother takes the bottle and the paper and throws them both back into the cutting before she's read a line. the child doesn't speak to either of you all afternoon."}},
		},
	},
	{
		Key:  "hermit",
		Text: "somebody out in the reed bed is singing the cutters' verse about the drowned-bell family, the one the cutters won't sing with anyone from the pot listening, loudly and very well. it's a nightingale in a choir robe gone green at the hem, sitting on a hummock with a bundle of reed for a bed. she left the choir, she says, over a verse. she won't say which.",
		Class: map[string]string{
			"bard": "you know the robe. the choir cut her name out of the monday book twenty years ago and nobody would say why.",
		},
		Options: []Option{
			{Wind: Vaunt, Try: "sing the next verse with her", Need: 55,
				Pass: Outcome{Text: "you take up the next verse and she takes the harmony without missing a beat, and for a moment the whole bed goes quiet to listen. she says you'll do. she gives you the peat she keeps under the reed bed, for when the choir comes looking and she has to move on.", Finds: 2, Mark: "hermit"},
				Fail: Outcome{Text: "you take up the next verse a tone flat. she stops singing, looks at you for a long time and says the choir must be very short of voices."}},
			{Wind: Fold, Try: "sit and listen", Need: 30,
				Pass: Outcome{Text: "you sit on the next hummock and listen to her sing all forty verses, which takes until dusk. she sings the thirtieth twice. when she's done she gives you a brick and tells you which verse she left over, and you won't repeat it here.", Finds: 1},
				Fail: Outcome{Text: "she won't sing with you sitting there. she says it isn't that kind of song. she sings it after you've gone, and you hear it all the way to the cart track."}},
			{Wind: Writ, Try: "offer to carry word to the choir",
				Pass: Outcome{Text: "she laughs for a long time. then she writes four lines on a strip of reed paper, folds it, and asks you to give it to the choir master by hand and watch his face. you will. she gives you a brick for the walk."}},
		},
	},
	{
		Key: "postman", Far: true,
		Text: "a pigeon of the penny post is wading out across the open water with a full mail bag held over his head, toward a chapel ridge with nothing else in sight. he's on the drowned villages round, he says. the post never closed it. letters still come for them, from people who don't know, or do, and the rule is the post delivers.",
		Options: []Option{
			{Wind: Writ, Try: "help him finish the round", Need: 40,
				Pass: Outcome{Text: "you carry the second bag. at each ridge he reads the address and posts the letter through the chapel's belfry slats, where it drops into the water, and ticks it off in his book. at the last ridge there's a letter addressed to the bird who carries the second bag. inside it there's a peat chit, good at any cutting, and nothing else.", Finds: 2, Mark: "letter"},
				Fail: Outcome{Text: "the second bag is heavier than his and you slow him down, and he misses the last ridge before the light goes. he says the post will be late, which it has never been, and he's very quiet on the way back."}},
			{Wind: Lot, Try: "open one of the letters", Need: 55,
				Pass: Outcome{Text: "you open one while he's at a ridge. it's to a reeve's wife, from her sister in the city, dated last week: the sister says she hopes the water's gone down by now, and sends a brick of peat for the winter, wrapped in the letter. you send it on.", Finds: 1},
				Fail: Outcome{Text: "you open one while he's at a ridge and it's blank, both sides, and when he comes back he takes it from you and looks at it and says that one was for you, and now it's been read."}},
			{Wind: Fold, Try: "walk with him and talk", Need: 30,
				Pass: Outcome{Text: "you walk the round with him and he talks the whole way, about the post, about the villages before the rising, about his father who walked the same round dry. at the end he gives you a brick he carries for the reeve who used to give him one, every winter.", Finds: 1},
				Fail: Outcome{Text: "he doesn't talk while he's on the round. it's a rule. you walk beside him in silence for three hours, which is its own kind of company."}},
		},
	},
	{
		Key: "stander", Far: true,
		Text: "far out past the causeway a heron is standing in the water to the chest, and moss has grown up her back and over one shoulder. the cutters say she's stood there since the rising, and the house says she's one of theirs, and nobody has ever seen her move. on a hummock beside her somebody has left peat, the way the cutters leave it at a shrine.",
		Class: map[string]string{
			"monk": "the house teaches her name to every novice on their first day in the water. nobody tells them she's real.",
		},
		Options: []Option{
			{Wind: Fold, Try: "stand beside her a while", Need: 50,
				Pass: Outcome{Text: "you stand beside her to the chest until the light goes, and at some point, without moving her head, she says thank you, in a voice like wet reed. she doesn't say anything else. the peat on the hummock is yours, by the custom of the place.", Finds: 2, Mark: "stander"},
				Fail: Outcome{Text: "you stand beside her for an hour and then you can't any longer. you wade back. she doesn't move. you leave a brick of your own on the hummock, though you're not sure you had one to leave."}},
			{Wind: Writ, Try: "leave peat at the hummock",
				Pass: Outcome{Text: "you leave a brick at the hummock as the cutters do, and say a word, and go. on the way back a cutter at the causeway asks if you left something for her and gives you two bricks when you say yes, because that's the custom too.", Finds: 1}},
			{Wind: Lot, Try: "touch her shoulder", Need: 65,
				Pass: Outcome{Text: "you wade up and put your hand on the moss on her shoulder. it's warm. she turns her head, very slowly, the first time anyone has seen her move, and looks at you, and turns it back. the cutters will talk about it for a year. one of them pushes a sack of peat into your arms without a word.", Finds: 3, Mark: "stander"},
				Fail: Outcome{Text: "you reach for her shoulder and the water between you is suddenly deep, very deep, and you're swimming. you don't try again."}},
		},
	},
	{
		Key: "fair", Far: true,
		Text: "on the night of the full moon the bog fair is held on a ring of rafts lashed together far out on the open mire: traders from nowhere anybody can name, selling what the bog kept. a box of butter a century old, still sweet. a bell clapper. shoes. a contract signed by both parties and neither of them born yet. nobody takes pp. everything is bought in peat.",
		Options: []Option{
			{Wind: Vaunt, Try: "buy the strangest thing on sale", Need: 55,
				Pass: Outcome{Text: "you buy a sealed jar with a label in no hand you know, for a brick, and when you open it on the walk back there's peat in it, a single perfect block, dry, stamped with the consortium's brand and a date forty years before there was a consortium. the trader threw in two more for the custom.", Finds: 3, Mark: "fair"},
				Fail: Outcome{Text: "the trader won't sell you the jar. she says it's spoken for, and points across the fair at a bird you can't quite see, who waves."}},
			{Wind: Lot, Try: "trade a find for a better one", Need: 50,
				Pass: Outcome{Text: "you trade a brick for a sack of bricks, which a trader at the edge of the fair says is the going rate tonight and won't explain. nobody seems to think it's strange. you carry the sack home and it's lighter than it should be and has more in it.", Finds: 2},
				Fail: Outcome{Text: "you trade a brick for something wrapped in sacking that turns out to be a brick. the trader says that's the fair. the fair doesn't promise a better one."}},
			{Wind: Stray, Try: "walk the whole ring", Need: 45,
				Pass: Outcome{Text: "you walk the ring of rafts end to end. at the far side, where the traders are fewest, an old cutter is selling nothing at all, sitting by a stack of peat, and gives you some of it because you came all the way round. most don't, she says.", Finds: 2},
				Fail: Outcome{Text: "the ring of rafts is longer than it looked from the water, and longer again when you're halfway round, and you come back to where you started without having passed it."}},
		},
	},
	{
		Key: "keel", Far: true,
		Text: "under the causeway's boat span, out of the wind, an albatross has made a home in the upturned whaleboat's shadow, with a hammock slung from the thwarts. he collects names, he says, not tolls: any bird crossing the span gives him theirs, and he writes it on the boat's planks with a burnt stick. the planks are nearly full.",
		Class: map[string]string{
			"wanderer": "the boat is the kittiwake's build. he says he knows, and that it's why he lives under it.",
		},
		Options: []Option{
			{Wind: Writ, Try: "give him your name",
				Pass: Outcome{Text: "you give him your name and he writes it on a plank near the keel, small, under a name you recognise from the gate book. he says that's the last space on that plank. he gives you a brick from under his hammock, which is what he gives everybody who fills a plank."}},
			{Wind: Stray, Try: "read the names on the planks", Need: 45,
				Pass: Outcome{Text: "you read the planks for an hour with his leave. there are names from before the rising, names of birds still alive in the city, and near the keel, small, eleven names you've heard before, in a different hand from his. he says they were there when the boat came. he gives you peat for the reading.", Finds: 1, Mark: "keel"},
				Fail: Outcome{Text: "the planks are written over and over, names on names, and you can't make out a single one. he says that's how it is on the outside planks. the old names are on the inside, and he doesn't show the inside."}},
			{Wind: Lot, Try: "give him somebody else's name", Need: 60,
				Pass: Outcome{Text: "you give him a name that isn't yours, and he writes it down, and looks at it, and then he laughs, the first time anybody has heard him laugh, and gives you a sack of peat. he says that bird crossed this span forty years ago and never gave it.", Finds: 3},
				Fail: Outcome{Text: "you give him a name that isn't yours and he doesn't write it. he says that bird's name is already on the boat, and that the bird is standing in front of him, and that's a thing he's only seen once."}},
		},
	},
}

var mirePagesMore = map[string]string{
	"toll":        "the toll heron has kept the bridge longer than anyone at the cuttings has been alive, and the bridge has been in a different ditch every morning of it. the cutters gave up mapping it. the hedge company once tried to charge him rent for the ditch it was in and found it in another the next day, a mile off, with the company's own survey stake driven in beside it, painted with the toll. he takes a pp or a true thing about yourself, and he keeps the true things in a ledger that has, the cutters say, only a handful of entries, though he's had a stool at that bridge for fifty years. the counting house sent a raven to audit the cash box once. the raven paid the toll in a true thing and came back to the city and resigned. nobody knows what the heron does with the pp. he has never been seen to spend one, and the box is never full.",
	"raft":        "the raft tavern has no name, no owner anyone admits to and no fixed place: it drifts through the near reaches on the wind and the rising and falling of the mire, and grounds where it grounds. a cormorant keeps the bar. she has kept it for as long as there's been a raft, and the cutters say she came with it, the way a figurehead comes with a ship. it takes no pp. a drink costs a story, and the cormorant decides whether what you told her was one. she's turned away a prior of the hearthkeepers, two reckoners and the head of the choir, and poured for an eel man's child who told her about a frog. the choir sends its new singers out to find the raft and try their songs on her, and most of them come back with water. the raft's own peat, stacked behind the bar, burns blue, and nobody has ever seen where she cuts it.",
	"surveyor":    "the consortium sent three surveyors into the mire to plan the draining of the eel runs for cutting, and the eel men said no in the eel men's way, which is that the surveyors kept walking into the deep water by mistake. two came out. the third, a gull called by the consortium's books its surveyor of the eastern line, didn't, and the consortium entered him as lost and stopped paying him. he hasn't stopped surveying. he stands where the eel men told him the run was shallow, sighting at a pole nobody holds, and the consortium has been paying his fee all along without anyone noticing: in peat, which the tallyman leaves on a hummock near him every month out of habit, and which he's never collected. his line, when he finishes it, will be very accurate. it runs to a brass-capped stake under the water with the old counting house branch's mark on it, set before the rising, and nobody at the consortium knows that stake is there.",
	"deeds":       "the snipe who sells the mats is called by the bellward a land agent and by the cutters a moss gardener, and both are true. she plants moss on the near reaches' floating mats in neat rows, a tuft at a time, so they grow faster, and sells the new ground in plots to bellward families who want land their grandchildren can't lose. the deeds are real. the tally board has licensed the sale of land below the hedge since before the rising, when there was land below the hedge, and nobody has ever revoked it, so a mat is land in law. the plots float. they drift, they join and part, and sometimes they sink, which the deed calls an act of the mire and excuses. there are bellward families who have owned the same plot for three generations without ever seeing it, and keep the deed framed on the wall. she says that's the point of land. the counting house holds a mortgage on four of her plots and hasn't been able to find them.",
	"lamplighter": "the old moorhen says she lights the mire's lamps, and the cutters don't argue with her, because nobody else has ever claimed to. she walks out at dusk on stilts with a brass pole and a bag of wicks, and where she touches the water a light comes up and drifts away against the wind, and she's done it every night for sixty years. the hedge company says the lights are a hazard and has twice sent a patrol to stop her, and both patrols came back saying she wasn't the one lighting them. she says nobody has ever paid her. she's not sure who should. she's written to the tally board, the counting house, the hearthkeepers and the choir, and the only reply she's had was from the choir, asking whether she'd let them use the lights in a play. she said yes. she was never paid for that either. she has a knack, she says, and it's for life, and whoever has it after her had better start soon.",
	"debtor":      "debtors go down into the tally mines at dawn in a line, a tally at a time, and the tally board decides when a debt is paid, and debt grows faster than tallies. some go into the mire instead. the lower reaches come out under the cliff close to the cuttings, and a debtor on a night shift who cuts a chain and walks east along the cliff foot instead of up the steps is in the reed by morning. the tally board doesn't chase debtors into the mire. it doesn't need to: the debt keeps, and it passes to whoever stood beside them, and a bird in the reed has nobody beside them. the cutters don't turn them in. the eel men take them on to carry traps and never ask their names. there are more of them out there than the board admits, living on eels and the peat they cut to keep warm, and mother tern's carries their messages up the gut for nothing, which the board knows about and has never managed to prove.",
	"inkwell":     "the bog keeps ink, and ink kept long enough remembers what it was for. a bottle of counting house iron gall dug out of a cutting will sometimes start to write, on any paper put near it, in the hand of the reckoner who last used it: accounts, mostly, and letters, and once at the cuttings a confession that the cutters burned the same afternoon. it writes slowly, a line at a time, and it stops when it reaches a sum that doesn't close. the counting house denies that its ink does anything of the kind. it also buys every bottle the cutters dig up, at a good price, through a clerk who comes out to the cuttings in a closed carriage once a season and asks for them by name. the cutters have started keeping a few back. the coot children at the cutting have learned their letters from bog ink, writing under its lines in its own bottle, and the ink corrects their spelling.",
	"hermit":      "the choir cuts a singer's name out of the monday book only when they've sung something the choir can't be seen to have heard, and it has done it four times in its history. the nightingale in the reed bed is the third. she sang a verse at dawn at the pot, twenty years ago, about where the city's hearth peat comes from and who's paid for it, and the choir master cut her name out by noon. she walked into the mire that evening and has lived on a hummock in the reed beds since, singing the cutters' songs back to them better than they sing them, and the cutters bring her eels and peat and won't tell the choir where she is. the choir knows where she is. it sends its best young voices out each spring, quietly, to learn what she knows. the verse she was cut out for has never been sung at the pot since. the cutters say it's the thirty-first, and that the choir's version stops at thirty.",
	"postman":     "the penny post never closed the drowned villages' round. the rule of the post, set by the pigeons when they opened it, is that the post delivers, and nobody has ever written to the post to say the villages aren't there any more. letters still come for them: from families in the city who don't know what happened, or do, from the tally board for debts the bog is keeping, and from people who just want to write to someone who won't write back. a pigeon walks the round every week, wading out across the open water with the bag over his head, and posts each letter through the belfry slats of the right chapel. the letters drop into the water and stay there. the post keeps a book of every one, and the wet guild has offered three times to buy it. the pigeon who walks it now is the third of his family to have the round. he says the villages are good about postage, which is due on delivery, and always paid.",
	"stander":     "every heron novice at the hearthkeepers' house is taught her name on the first day in the water, as the one who stood longest. nobody tells them she's real. she went out into the water in the rising with the other herons who stood at the four corners of the hedge, and when the water went down they came home and she didn't. she's still standing where she was, far out past the causeway, to the chest, and moss has grown up her back and over one shoulder. the house says she's one of theirs and sends novices out once a year to stand with her for a day. the cutters leave peat at a hummock beside her, the way they'd leave it at a shrine, and take some when they need it, which is also the custom. the abbot has been out to her once. he came back and wouldn't say anything for a week, and then cut the next notch in the notch stick a finger lower than the count, for one day, and never again.",
	"fair":        "the bog fair is held on the night of the full moon on a ring of rafts lashed together far out on the open mire, where the patrols don't go and the moon on the water lights it better than lamps. nobody knows who sets it up. the traders come from nowhere anybody can name and sell what the bog has kept: butter, shoes, bell clappers, a ledger in a hand nobody will admit to, a box of letters from someone to someone, a contract signed by two parties neither of whom is born yet. everything is bought and sold in peat. pp is no good at the fair. a trader who was offered it once laughed and said the bog doesn't keep it. the hedge company has tried to tax the fair and couldn't find it on the night. the counting house has tried to buy the contract and was told it wasn't for sale to either party. the cutters go every month. they say the prices are fair, which is the joke, and they come home with more than they took.",
	"keel":        "the old albatross who lives under the causeway's boat span came off the grey north twenty years ago with nothing, like every albatross, and walked south across the mire instead of to the city, and stopped at the span because the boat was the build of his own ship. he collects names. any bird crossing the span gives him theirs and he writes it on the boat's planks with a burnt stick, outside planks first, and the outside is nearly full. the inside planks are older and he doesn't show them. he says there were names on the inside when the boat came, in a different hand, and that he's kept them. the wet guild has tried to take the boat for salvage twice and both times the span it makes was the only span standing next morning. he's paid in peat by the causeway gang, who give a brick to anybody who keeps a span standing, and he's never asked them for anything else. he says the names are the boat's toll, not his.",
}
