package pp

// Charter is the economy's ground rules, apart from Balance because they
// are meant to hold for years: a Balance tunes how play pays, the Charter
// says where pp may come from and go. charter_test pins every version's
// hash, so a change can't go out quietly: it needs a new Version and a
// changelog line. The operator still controls the code, so this makes a
// change visible, not impossible.
//
// The rules, in short:
//   - only time creates pp: voice, chat and quests, under the daily pool;
//     no admin mints, and there is no bank, debt or borrowing
//   - Levy of every earning goes to the server's treasury, until it holds
//     TreasuryWeeks of it; the treasury pays prizes and grants
//   - grants: GrantShare of the treasury a week at most, GrantSeason pot
//     days a season to anyone, never to an admin, a second admin above
//     GrantSecond pot days, every one posted, none before the treasury has
//     taken its levy for GrantsAfter days
//   - spending is burned; members never trade pp; nothing pp buys is
//     scarce; prices are set in pot days
//   - nothing a member holds is ever taken
//   - the backstop: if the shop burns under SinkFloor of what's minted two
//     seasons running, a purse past Line pot days keeps Keep of new earning
//     from the next season on
//   - every pp is in the ledger, and every roll comes from a seed committed
//     a season ahead
type Charter struct {
	Version string

	Levy          int64 // basis points of every earning
	TreasuryWeeks int64

	GrantShare, GrantSeason, GrantSecond int64
	GrantsAfter                          int64 // days after the treasury's first levy

	SinkFloor, Line, Keep int64

	// A pot day is what this day earns under the live Balance, unbuffed and
	// unrested: Voice minutes with the mic open, Messages at the mean pay,
	// and a quest, rerolled when plain. It is a core member's median day on
	// the melting pot (merlin's statistics, september to october 2026).
	RefVoice, RefMessages int64
}

// Rules is the charter in force.
var Rules = Charter{
	Version:       "2",
	Levy:          200,
	TreasuryWeeks: 8,
	GrantShare:    25,
	GrantSeason:   10,
	GrantSecond:   1,
	GrantsAfter:   28,
	SinkFloor:     5,
	Line:          45,
	Keep:          10,
	RefVoice:      4,
	RefMessages:   27,
}

// PotDay is the unit prices are set in, in hundredths of a pp.
func (b *Balance) PotDay(c Charter) int64 {
	d, p := Day{}, Pools{Pool: b.Pool, Pace: 100}
	var got int64
	for range c.RefVoice {
		got += b.Earn(&d, p, false, b.VoiceBase*b.Unmuted/100, 0)
	}
	for range c.RefMessages {
		got += b.Earn(&d, p, true, (b.ChatMin+b.ChatMax)/2, 0)
	}
	return got + b.QuestEV()
}

// QuestEV is what a quest pays on average when a plain one is rerolled.
func (b *Balance) QuestEV() int64 {
	var sum, mean int64
	for i, o := range b.QuestOdds {
		sum += int64(o)
		mean += int64(o) * b.QuestPP[i]
	}
	mean /= sum
	return (int64(b.QuestOdds[0])*mean + int64(b.QuestOdds[1])*b.QuestPP[1] + int64(b.QuestOdds[2])*b.QuestPP[2]) / sum
}

// levy is what the treasury takes of got while it's taking.
func (c Charter) levy(got int64) int64 { return got * c.Levy / 10000 }
