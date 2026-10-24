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
	// when the member's own mic is open and Deafened percent when they can't
	// hear. Companions count toward the two humans a channel needs only once
	// their account is CompanionAge old.
	VoiceBase    int64
	Unmuted      int64
	Deafened     int64
	CompanionAge time.Duration

	// Idling: muted or deafened with no sign of life (a message that could
	// pay, a command or button, camera or stream; never a mute toggle, which
	// a macro can do) for longer than IdleGrace, give or take IdleJitter
	// percent per member and day, voice fades over IdleFade to IdleFloor
	// percent, or DeafFloor deafened. Deafened, the grace is halved. The fade
	// is a slope, not a step, so there is no minute to learn and dodge.
	//
	// skua can't hear voice, so an open mic is taken at its word for
	// OpenGrace (jittered alike), then treated as muted: a hardware mute
	// looks open to Discord, and it shouldn't farm the bonus all day.
	IdleGrace, IdleFade  time.Duration
	OpenGrace            time.Duration
	IdleJitter           int64
	IdleFloor, DeafFloor int64

	// Chat: one message per ChatCooldown pays ChatMin to ChatMax, and one
	// shorter than ChatMinRunes or repeating the last pays nothing.
	ChatMin, ChatMax int64
	ChatCooldown     time.Duration
	ChatMinRunes     int
	// Activity (quests, stats) counts any such message once per ActivityGap,
	// so a lively conversation shows for what it is while pay stays paced.
	ActivityGap time.Duration

	// The daily pool: Pool, give or take Jitter percent per member and day,
	// one pool for voice and chat alike, so a server's best talker and its
	// best voice regular top out level. Past the last band's edge voice pays
	// VoiceTail percent and chat ChatTail. Unused pool carries RestedCarry percent into
	// the next day, up to RestedMax percent of a pool.
	Pool                int64
	Jitter              int64
	Taper               [2]Band
	VoiceTail, ChatTail int64
	RestedCarry         int64
	RestedMax           int64

	// Buffs, in percent on everything but quests: the server tag worn,
	// .gg/melted in the status, and boosting the server.
	Crest, Banner, Plume int64
	// Hearth tiers, reached at HearthAt percent of the server's usual day
	// (HearthFloor until it has one).
	Hearth, HearthAt [3]int64
	HearthFloor      int64

	// Levels: level L to L+1 costs CurveA*L² + CurveB*L + CurveC pp.
	CurveA, CurveB, CurveC int64

	// Quests: one dealt a day, QuestHold held, one reroll a day. A dealt
	// quest's tier is drawn by QuestOdds (out of their sum) and pays that
	// tier's QuestPP: most a good chunk, a few great, now and then golden.
	QuestPP   [3]int64
	QuestOdds [3]uint64
	QuestHold int
	// QuestKinds are what each tier can deal, drawn evenly: a task or a
	// few, each counted from when the quest was dealt. A good quest asks
	// about a core member's day; a rarer one asks more, but less than it
	// pays more, so drawing one is luck. Some golden ones ask for
	// something odd instead.
	QuestKinds [3][]QuestKind

	// Chirographs and round robins: PactGoal minutes together finish one,
	// it lapses PactLife days after it was posted, and Joinder is what each
	// signer paid gets when two, three or four were.
	PactGoal, PactLife int64
	Joinder            [3]int64

	// Classes: the first pick of a season is free, and so is changing it
	// within ClassGrace days. After that a change costs ClassFee pot days
	// and the next can't come for ClassWait days.
	ClassGrace, ClassWait, ClassFee int64

	// Seasons: SeasonWeeks long, the first starting on day SeasonStart (a
	// Monday). Each week ends in a checkpoint paying the top places on each
	// board (voice and chat) Prizes, which never count toward the season.
	SeasonStart int64
	SeasonWeeks int64
	Prizes      [5]int64
}

// Default is the live balance.
var Default = Balance{
	Version: "0.10.0",

	VoiceBase:    2 * unit,
	Unmuted:      150,
	Deafened:     50,
	CompanionAge: 7 * 24 * time.Hour,

	IdleGrace:  30 * time.Minute,
	IdleFade:   90 * time.Minute,
	OpenGrace:  3 * time.Hour,
	IdleJitter: 25,
	IdleFloor:  5,
	DeafFloor:  0,

	ChatMin:      2 * unit,
	ChatMax:      4 * unit,
	ChatCooldown: 20 * time.Second,
	ChatMinRunes: 3,
	ActivityGap:  10 * time.Second,

	Pool:        700 * unit,
	Jitter:      8,
	Taper:       [2]Band{{Upto: 60, Rate: 100}, {Upto: 100, Rate: 50}},
	VoiceTail:   5,
	ChatTail:    0,
	RestedCarry: 50,
	RestedMax:   50,

	Crest:       10,
	Banner:      10,
	Plume:       5,
	Hearth:      [3]int64{5, 10, 15},
	HearthAt:    [3]int64{100, 115, 130},
	HearthFloor: 3000 * unit,

	CurveA: 25,
	CurveB: 150,
	CurveC: 300,

	QuestPP:   [3]int64{275 * unit, 400 * unit, 750 * unit},
	QuestOdds: [3]uint64{36, 3, 1},
	QuestHold: 3,
	QuestKinds: [3][]QuestKind{
		{
			{Icon: "campfire", Tasks: []Task{{taskVoice, 30}}},
			{Icon: "quill", Tasks: []Task{{taskTalk, 25}}},
			{Icon: "drum", Tasks: []Task{{taskCrowd, 20}}},
			{Icon: "envelope", Tasks: []Task{{taskReply, 12}}},
		},
		{
			{Icon: "compass", Tasks: []Task{{taskVoice, 20}, {taskTalk, 17}}},
			{Icon: "campfire", Tasks: []Task{{taskVoice, 40}}},
			{Icon: "quill", Tasks: []Task{{taskTalk, 33}}},
			{Icon: "bell", Tasks: []Task{{taskCrowd, 25}, {taskReply, 10}}},
			{Icon: "lute", Tasks: []Task{{taskStream, 35}}},
		},
		{
			{Icon: "lantern", Tasks: []Task{{taskVoice, 18}, {taskTalk, 15}, {taskHearth, 10}}},
			{Icon: "quill", Tasks: []Task{{taskTalk, 45}}},
			{Icon: "mirror", Tasks: []Task{{taskSelfie, 1}}},
			{Icon: "beads", Tasks: []Task{{taskCrowd, 45}, {taskReply, 15}}},
			{Icon: "orb", Tasks: []Task{{taskCamera, 20}}},
		},
	},

	PactGoal: 30,
	PactLife: 2,
	Joinder:  [3]int64{60 * unit, 90 * unit, 120 * unit},

	ClassGrace: 2,
	ClassWait:  7,
	ClassFee:   1,

	SeasonStart: DayOf(time.Date(2026, 11, 2, 5, 30, 0, 0, time.UTC)),
	SeasonWeeks: 4,
	Prizes:      [5]int64{1500 * unit, 1000 * unit, 750 * unit, 500 * unit, 250 * unit},
}
