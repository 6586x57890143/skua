// Package pp is the melting pot's rpg engine: members earn pot points (pp)
// for time in voice and for chatting, under a daily cap they feel but never
// see, sped up by buffs they can wear and one the whole server builds. It
// lives on skua until peregrine has modules, so it leans on nothing but core,
// brand and the store.
//
// This file is every number the game runs on. Changing one is a balance
// change: it has to pass the simulation in balance_test.go and ship with a
// changelog entry, which changelog_test.go enforces.
package pp

import "time"

// Amounts are in hundredths of a pp, so a buff on a 2 pp minute stays exact.
// Members only ever see whole pp.
const unit = 100

// Band is one stretch of the daily pool: up to Upto percent full, earning
// pays Rate percent.
type Band struct{ Upto, Rate int64 }

// Balance is every tunable. Default is the one live.
type Balance struct {
	Version string

	// Voice: a whole eligible minute pays VoiceBase, times Unmuted percent
	// when the member's own mic is open. Companions count toward the two
	// humans a channel needs only once their account is CompanionAge old.
	VoiceBase    int64
	Unmuted      int64
	CompanionAge time.Duration

	// Chat: one message per ChatCooldown pays ChatMin to ChatMax, and one
	// shorter than ChatMinRunes or repeating the last pays nothing.
	ChatMin, ChatMax int64
	ChatCooldown     time.Duration
	ChatMinRunes     int

	// The daily pool: Pool, give or take Jitter percent per member and day.
	// Chat fills at most ChatShare percent of it (give or take ChatJitter),
	// past which chat pays ChatTail percent. VoiceTail is what voice pays
	// past the last band's edge. Unused pool carries RestedCarry percent into
	// the next day, up to RestedMax percent of a pool.
	Pool                  int64
	Jitter                int64
	ChatShare, ChatJitter int64
	Taper                 [2]Band
	VoiceTail, ChatTail   int64
	RestedCarry           int64
	RestedMax             int64

	// Buffs, in percent on everything but quests.
	Crest, Banner int64
	// Hearth tiers, reached at HearthAt percent of the server's usual day
	// (HearthFloor until it has one).
	Hearth, HearthAt [3]int64
	HearthFloor      int64

	// Levels: level L to L+1 costs CurveA*L² + CurveB*L + CurveC pp.
	CurveA, CurveB, CurveC int64

	// Quests: one dealt a day, QuestHold held, one in RareOneIn rare.
	QuestCommon, QuestRare int64
	RareOneIn              uint64
	QuestHold              int
}

// Default is the live balance.
var Default = Balance{
	Version: "0.1.0",

	VoiceBase:    2 * unit,
	Unmuted:      150,
	CompanionAge: 7 * 24 * time.Hour,

	ChatMin:      4 * unit,
	ChatMax:      7 * unit,
	ChatCooldown: time.Minute,
	ChatMinRunes: 3,

	Pool:        600 * unit,
	Jitter:      15,
	ChatShare:   40,
	ChatJitter:  10,
	Taper:       [2]Band{{Upto: 60, Rate: 100}, {Upto: 100, Rate: 50}},
	VoiceTail:   5,
	ChatTail:    1,
	RestedCarry: 50,
	RestedMax:   50,

	Crest:       10,
	Banner:      10,
	Hearth:      [3]int64{5, 10, 15},
	HearthAt:    [3]int64{50, 100, 150},
	HearthFloor: 3000 * unit,

	CurveA: 25,
	CurveB: 150,
	CurveC: 300,

	QuestCommon: 150 * unit,
	QuestRare:   250 * unit,
	RareOneIn:   4,
	QuestHold:   3,
}
