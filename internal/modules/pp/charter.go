package pp

import "strconv"

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
//     TreasuryWeeks of it beside the contest fund; it pays the prizes
//   - the contest fund, in the treasury, is the one pp made beside time,
//     and only out of it: FundShare of every quest paid, until it holds
//     FundHold of the biggest prize; it starts full, once
//   - grants come out of the contest fund: PrizeMax pot days at most,
//     GrantSeason pot days a season to anyone, never to an admin, a second
//     admin above GrantSecond pot days, every one posted
//   - spending is burned; members never trade pp; nothing pp buys is
//     scarce but a livery of one's own: a server's roll has room for so
//     many, and their price rises with how full it is, by RollRise percent
//     at full and with the square of how full; prices are set in pot days
//   - a petition's price is held while the heralds decide, and given back
//     if they refuse it
//   - nothing a member holds is ever taken
//   - the backstop: if the shop burns under SinkFloor of what's minted two
//     seasons running, a purse past Line pot days keeps Keep of new earning
//     from the next season on
//   - prices are in pot days, an engaged member's day; when a mechanic
//     becomes part of that day it is measured again, never quietly
//   - every pp is in the ledger, and every roll comes from a seed committed
//     a season ahead
type Charter struct {
	Version string

	Levy          int64 // basis points of every earning
	TreasuryWeeks int64

	FundShare int64 // basis points of every quest paid
	FundHold  int64 // of the biggest prize
	PrizeMax  int64 // pot days

	GrantSeason, GrantSecond int64

	SinkFloor, Line, Keep int64

	// RollRise is how much dearer, in percent, a livery of one's own is
	// when the heralds' roll is full.
	RollRise int64

	// A pot day is what this day earns under the live Balance, unbuffed and
	// unrested: Voice minutes with the mic open, Messages at the mean pay,
	// and a quest, rerolled when plain. It is a core member's median day on
	// the melting pot (merlin's statistics, september to october 2026),
	// times Engaged percent: what a member takes home on the median day they
	// finish a quest, over that reference day, measured on the melting pot's
	// ledger. When a mechanic becomes part of an engaged day, Engaged is
	// measured again, in a new version, and every price moves with it.
	RefVoice, RefMessages int64
	Engaged               int64
}

// Rules is the charter in force.
var Rules = Charter{
	Version:       "6",
	Levy:          350,
	TreasuryWeeks: 8,
	FundShare:     300,
	FundHold:      3,
	PrizeMax:      9,
	GrantSeason:   10,
	GrantSecond:   1,
	SinkFloor:     5,
	Line:          45,
	Keep:          10,
	RollRise:      200,
	RefVoice:      4,
	RefMessages:   27,
	Engaged:       155,
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
	return (got + b.QuestEV()) * c.Engaged / 100
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

// LevyPct is the levy as members read it, like 3.5%.
func (c Charter) LevyPct() string { return strconv.FormatFloat(float64(c.Levy)/100, 'f', -1, 64) + "%" }

// Prize is the biggest grant: PrizeMax pot days under b.
func (c Charter) Prize(b *Balance) int64 { return c.PrizeMax * b.PotDay(c) }
