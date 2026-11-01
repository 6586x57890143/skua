package pp

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
)

// A rarer quest asks more, but less than it pays more: every kind of a
// tier asks under its pay's multiple of the good tier's ask. A minute in a
// crowd or on stream is a voice minute too, and a reply is a message, so
// tasks of one kind overlap: the work is the biggest of them. Each kind is
// measured against the good quest of its own kind, since a message comes
// every 20 seconds and pays about what a voice minute does: work, not pay,
// is what a quest asks. A quest of both asks the sum.
func TestQuestsAskLessThanTheyPay(t *testing.T) {
	goals := func(k QuestKind) (voice, talk int64) {
		for _, t := range k.Tasks {
			switch t.Metric {
			case taskVoice, taskHearth, taskCrowd, taskStream, taskCamera:
				voice = max(voice, t.Goal)
			case taskTalk, taskReply:
				talk = max(talk, t.Goal)
			}
		}
		return voice, talk
	}
	baseVoice, _ := goals(Default.QuestKinds[0][0])
	_, baseTalk := goals(Default.QuestKinds[0][1])
	for tier, kinds := range Default.QuestKinds {
		for _, k := range kinds {
			if k.Tasks[0].Metric == taskSelfie || k.Tasks[0].Metric == taskCamera {
				continue
			}
			pays := float64(Default.QuestPP[tier]) / float64(Default.QuestPP[0])
			voice, talk := goals(k)
			asks := float64(voice)/float64(baseVoice) + float64(talk)/float64(baseTalk)
			if tier > 0 && (asks >= pays || asks <= 1) {
				t.Errorf("a %s quest asks %.2f times a good one and pays %.2f times", QuestTiers[tier], asks, pays)
			}
		}
	}
}

// Quests are dealt one a day to old enough accounts, up to three held, a
// missed day kept; a young account gets none. A first deal brings one more,
// a good quest to talk.
func TestQuestDeal(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	a := old(1)
	st.chat(s, guild, a, "here", t0)
	if qs := s.members[a].quests; len(qs) != 2 || qs[1].tier != 0 || qs[1].tasks(&Default)[0].Metric != taskTalk {
		t.Fatalf("a first day dealt %+v", qs)
	}
	st.chat(s, guild, a, "here again", t0.Add(time.Hour))
	if n := len(s.members[a].quests); n != 2 {
		t.Errorf("a second visit the same day dealt more: %d", n)
	}
	st.chat(s, guild, a, "back after a while", t0.Add(5*24*time.Hour))
	if n := len(s.members[a].quests); n != Default.QuestHold {
		t.Errorf("days away dealt up to %d; want %d", n, Default.QuestHold)
	}

	young := snowflake.New(t0.Add(-time.Hour))
	st.chat(s, guild, young, "new here", t0)
	if n := len(s.members[young].quests); n != 0 {
		t.Errorf("a day old account was dealt %d", n)
	}

	// Selfies come up only where there's a channel for them.
	for slot := range uint64(400) {
		if q := Default.draw(1, 2, 3, 4, slot, false, nil); q.tasks(&Default)[0].Metric == taskSelfie {
			t.Fatal("a selfie quest without a selfie channel")
		}
	}
	saw := false
	for slot := range uint64(4000) {
		if q := Default.draw(1, 2, 3, 4, slot, true, nil); q.tasks(&Default)[0].Metric == taskSelfie {
			saw = true
		}
	}
	if !saw {
		t.Error("no selfie quest ever came up with a channel")
	}
}

// A quest counts its tasks and, once they're all met, pays its tier as
// labour: the levy's share to the treasury, the rest to the purse, all of
// it to the level, a ledger row of its own.
func TestQuestPays(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	a := old(1)
	mem := st.member(s, guild, a, DayOf(t0))
	mem.quests = []heldQuest{{tier: 0, kind: 1}, {tier: 1, kind: 0}} // messages; minutes and messages
	talk, greatTalk := Default.QuestKinds[0][1].Tasks[0].Goal, Default.QuestKinds[1][0].Tasks[1].Goal
	purse, total, treasury := mem.purse, mem.total, s.treasury
	for i := range talk {
		st.chat(s, guild, a, "a thing worth saying "+strconv.Itoa(int(i)), t0.Add(time.Duration(i)*30*time.Second))
	}
	if len(mem.quests) != 1 || mem.questsDone != 1 {
		t.Fatalf("after %d messages: %d held, %d done", talk, len(mem.quests), mem.questsDone)
	}
	var row *entry
	for i := range s.pending {
		if s.pending[i].reason == reasonQuest {
			row = &s.pending[i]
		}
	}
	if row == nil || row.inputs[2] != Default.QuestPP[0] {
		t.Fatalf("the quest's row: %+v", row)
	}
	levy := Rules.levy(Default.QuestPP[0])
	if got := mem.total - total; got < Default.QuestPP[0] {
		t.Errorf("the level counted %d of the quest", got)
	}
	if s.treasury-treasury < levy || row.delta != Default.QuestPP[0]-levy || mem.purse-purse < row.delta {
		t.Errorf("treasury +%d, row %d, purse +%d", s.treasury-treasury, row.delta, mem.purse-purse)
	}
	if mem.quests[0].prog[1] != greatTalk {
		t.Errorf("the great quest's talk stands at %d", mem.quests[0].prog[1])
	}

	// Round trip through the save format, dropping what the balance lost.
	mem.quests[0].rerolled = 20000
	saved := encodeQuests(mem.quests) + ";9,9,0,0,0,0,0;junk"
	if back := decodeQuests(&Default, saved); len(back) != 1 || back[0] != mem.quests[0] {
		t.Errorf("came back as %+v", back)
	}
	// Saved before rerolls were each quest's.
	if back := decodeQuests(&Default, "0,1,5,0,0,0,0"); len(back) != 1 || back[0].prog[0] != 5 || back[0].rerolled != 0 {
		t.Errorf("an old save came back as %+v", back)
	}
}

// Each quest held rerolls once a day, on its own.
func TestQuestReroll(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	a := old(1)
	today := DayOf(t0)
	mem := st.member(s, guild, a, today)
	mem.quests = []heldQuest{{tier: 0, kind: 0}, {tier: 0, kind: 0}}
	if err := st.reroll(s, mem, guild, a, 3, today); err == nil {
		t.Error("rerolled a quest not held")
	}
	if err := st.reroll(s, mem, guild, a, 0, today); err != nil || mem.quests[0].rerolled != today {
		t.Fatalf("reroll: %v", err)
	}
	if err := st.reroll(s, mem, guild, a, 0, today); err == nil {
		t.Error("rerolled one quest twice in a day")
	}
	if err := st.reroll(s, mem, guild, a, 1, today); err != nil || mem.quests[1].rerolled != today {
		t.Fatalf("the second quest's reroll: %v", err)
	}
	if err := st.reroll(s, mem, guild, a, 0, today+1); err != nil {
		t.Errorf("a new day's reroll: %v", err)
	}
}

// A reroll never lands on the quest it replaces, whatever the day, the
// tier or the selfie channel.
func TestRerollNeverRepeats(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	a := old(1)
	for day := DayOf(t0); day < DayOf(t0)+300; day++ {
		for tier, kinds := range Default.QuestKinds {
			for kind := range kinds {
				for _, selfies := range []snowflake.ID{0, 77} {
					s.selfies = selfies
					mem := st.member(s, guild, a, day)
					mem.quests = []heldQuest{{tier: tier, kind: kind}}
					if err := st.reroll(s, mem, guild, a, 0, day); err != nil {
						t.Fatal(err)
					}
					if q := mem.quests[0]; q.tier == tier && q.kind == kind {
						t.Fatalf("day %d rerolled %d/%d into itself", day, tier, kind)
					}
				}
			}
		}
	}
}

// selfieRest answers GetMessage for the messages it still has.
type selfieRest struct {
	rest.Rest
	there map[snowflake.ID]bool
}

func (f *selfieRest) GetMessage(_, msg snowflake.ID, _ ...rest.RequestOpt) (*discord.Message, error) {
	if f.there[msg] {
		return &discord.Message{ID: msg}, nil
	}
	return nil, &rest.Error{Response: &http.Response{StatusCode: http.StatusNotFound}}
}

// A selfie quest takes a picture in the selfie channel, and finishes once
// the picture has stayed a day; one taken down frees the quest to try
// again.
func TestSelfieQuest(t *testing.T) {
	m := testModule(&fakeDB{})
	f := &selfieRest{there: map[snowflake.ID]bool{}}
	m.rest, m.Guard = f, guard.New()
	s := m.st.server(guild)
	s.selfies = 77
	a := old(1)
	selfie := 0
	for k, kind := range Default.QuestKinds[2] {
		if kind.Tasks[0].Metric == taskSelfie {
			selfie = k
		}
	}
	mem := m.st.member(s, guild, a, DayOf(t0))
	mem.quests = []heldQuest{{tier: 2, kind: selfie}}
	png := "image/png"
	post := func(id snowflake.ID, ch snowflake.ID, at time.Time) {
		m.st.selfie(s, mem, discord.Message{ID: id, ChannelID: ch, Attachments: []discord.Attachment{{ContentType: &png}}}, at)
	}
	post(900, 4, t0) // the wrong channel
	m.st.selfie(s, mem, discord.Message{ID: 901, ChannelID: 77}, t0)
	if mem.quests[0].ref != 0 {
		t.Fatal("a message off the channel, or without a picture, counted")
	}
	post(902, 77, t0)
	if mem.quests[0].ref != 902 || !strings.Contains(mem.quests[0].text(&Default), "stayed a day") {
		t.Fatalf("the selfie wasn't kept: %+v", mem.quests[0])
	}

	// Taken down before the day was out: the quest waits for another.
	m.now = func() time.Time { return t0.Add(25 * time.Hour) }
	m.checkSelfies(context.Background())
	if len(mem.quests) != 1 || mem.quests[0].ref != 0 {
		t.Fatalf("a deleted selfie: %+v", mem.quests)
	}
	post(903, 77, t0.Add(26*time.Hour))
	f.there[903] = true
	m.now = func() time.Time { return t0.Add(40 * time.Hour) }
	m.checkSelfies(context.Background())
	if len(mem.quests) != 1 {
		t.Fatal("a selfie up only fourteen hours finished")
	}
	m.now = func() time.Time { return t0.Add(51 * time.Hour) }
	m.checkSelfies(context.Background())
	if len(mem.quests) != 0 || mem.questsDone != 1 {
		t.Errorf("a selfie up a day didn't finish: %+v", mem.quests)
	}
}

// The quests button shows the screen, every quest as its description and
// no text over it; a golden one turns the card gold; each quest's reroll
// button swaps it, under the menu.
func TestQuestsButton(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(3)
	user := snowflake.ID(5)
	mem := m.st.member(s, 3, user, DayOf(t0))
	mem.quests = []heldQuest{{tier: 2, kind: 0}, {tier: 0, kind: 1}}
	mem.classSeason = 0
	e, sent := coretest.Button(t, "pp:quests", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	card := js((*sent)[0].Components)
	if strings.Contains(card, "-# ") || !strings.Contains(card, "your quests: golden") || !strings.Contains(card, "while the hearth burns") || !strings.Contains(card, "pp:reroll:0") || !strings.Contains(card, "pp:reroll:1") || !strings.Contains(card, "pp:status") {
		t.Errorf("the quests card: %s", card)
	}

	r, _ := coretest.Button(t, "pp:reroll:1", nil)
	r.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
		return nil
	}
	if err := m.press(context.Background(), r); err != nil || mem.quests[1].rerolled != DayOf(t0) || mem.quests[0].rerolled != 0 {
		t.Errorf("rerolling: %v", err)
	}

	// The selfie channel picker is on the help page, for admins.
	if !strings.Contains(js(m.Report(3).Controls), "pp:selfies") {
		t.Error("the help page has no selfie channel picker")
	}
}

// kindOf is the first kind in tier whose first task counts metric.
func kindOf(t *testing.T, tier int, metric string) int {
	t.Helper()
	for k, kind := range Default.QuestKinds[tier] {
		if kind.Tasks[0].Metric == metric {
			return k
		}
	}
	t.Fatalf("no %s quest in tier %d", metric, tier)
	return 0
}

// A crowd counts a voice minute with three or more in the room, a stream or a
// camera counts only its own, and only a bird earning is counted.
func TestVoiceQuests(t *testing.T) {
	st := newState(&Default, 42)
	s := st.server(guild)
	a, b, c := old(1), old(2), old(3)
	crowd, camera := kindOf(t, 0, taskCrowd), kindOf(t, 2, taskCamera)
	stream := kindOf(t, 1, taskStream)
	for _, u := range []snowflake.ID{a, b, c} {
		st.member(s, guild, u, DayOf(t0)).quests = []heldQuest{{tier: 0, kind: crowd}, {tier: 2, kind: camera}, {tier: 1, kind: stream}}
	}
	cam, live := in(room), in(room)
	cam.SelfVideo, live.SelfStream = true, true
	st.sit(s, guild, a, cam, false, t0)
	st.sit(s, guild, b, live, false, t0)
	st.minute(s, guild, 0, t0.Add(time.Minute))
	qa, qb := s.members[a].quests, s.members[b].quests
	if qa[0].prog[0] != 0 || qa[1].prog[0] != 1 || qa[2].prog[0] != 0 || qb[2].prog[0] != 1 || qb[1].prog[0] != 0 {
		t.Fatalf("two in a room: %+v %+v", qa, qb)
	}
	// A fresh alt makes three seats but not a crowd.
	alt := snowflake.New(t0)
	st.sit(s, guild, alt, in(room), false, t0)
	st.minute(s, guild, 0, t0.Add(2*time.Minute))
	if s.members[a].quests[0].prog[0] != 0 {
		t.Fatal("a fresh account counted toward a crowd")
	}
	st.sit(s, guild, alt, discord.VoiceState{}, false, t0)
	st.sit(s, guild, c, in(room), false, t0)
	st.minute(s, guild, 0, t0.Add(3*time.Minute))
	if s.members[c].quests[0].prog[0] != 1 || s.members[a].quests[0].prog[0] != 1 {
		t.Errorf("three in a room isn't a crowd: %+v", s.members[c].quests)
	}
}

// A reply counts when it answers another bird and pays: never one to
// yourself or to a bot.
func TestReplyQuest(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(guild)
	a := old(1)
	mem := m.st.member(s, guild, a, DayOf(t0))
	mem.quests = []heldQuest{{tier: 0, kind: kindOf(t, 0, taskReply)}}
	say := func(at time.Duration, to discord.User, text string) {
		m.now = func() time.Time { return t0.Add(at) }
		m.OnEvent(&events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{
			GenericEvent: generic(), GuildID: guild,
			Message: discord.Message{Author: discord.User{ID: a}, Content: text, ReferencedMessage: &discord.Message{Author: to}},
		}})
	}
	say(0, discord.User{ID: a}, "talking to myself")
	say(time.Minute, discord.User{ID: old(9), Bot: true}, "thanks, bot")
	say(2*time.Minute, discord.User{ID: snowflake.New(t0)}, "hi, alt")
	if mem.quests[0].prog[0] != 0 {
		t.Fatal("a reply to yourself, a bot or a fresh account counted")
	}
	say(3*time.Minute, discord.User{ID: old(2)}, "good point")
	say(3*time.Minute+time.Second, discord.User{ID: old(2)}, "and another")
	if mem.quests[0].prog[0] != 1 {
		t.Errorf("replies counted %d, want the one that paid", mem.quests[0].prog[0])
	}
}

// A selfie that's up is set aside: with three held the day's deal still
// comes and the selfie goes under the screen, and it can't be rerolled
// while it's up, though one not yet posted can. One taken down may be
// posted again once; a second taken down voids the quest.
func TestSelfieAside(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(3)
	s.selfies = 77
	user, day := snowflake.ID(5), DayOf(t0)
	selfie := kindOf(t, 2, taskSelfie)
	mem := m.st.member(s, 3, user, day)
	mem.quests = []heldQuest{{tier: 0, kind: 0}, {tier: 0, kind: 1}, {tier: 2, kind: selfie}}

	other := m.st.member(s, 3, old(2), day)
	other.quests = []heldQuest{{tier: 2, kind: selfie}}
	if err := m.st.reroll(s, other, 3, old(2), 0, day); err != nil {
		t.Errorf("a selfie not yet posted can't be rerolled: %v", err)
	}

	png := "image/png"
	post := func(id snowflake.ID) {
		m.st.selfie(s, mem, discord.Message{ID: id, ChannelID: 77, Attachments: []discord.Attachment{{ContentType: &png}}}, t0)
	}
	post(900)
	if m.st.reroll(s, mem, 3, user, 2, day) == nil {
		t.Error("a selfie that's up was rerolled")
	}
	m.now = func() time.Time { return t0.Add(24 * time.Hour) }
	m.st.member(s, 3, user, day+1)
	if len(mem.quests) != 4 || held(mem.quests) != 3 {
		t.Fatalf("the day's deal behind a selfie that's up: %+v", mem.quests)
	}
	if got := decodeQuests(&Default, encodeQuests(mem.quests)); !slices.Equal(got, mem.quests) {
		t.Errorf("saved quests came back %+v", got)
	}
	card := func() string {
		e, sent := coretest.Button(t, "pp:quests", nil)
		if err := m.press(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		return js((*sent)[0].Components)
	}
	if c := card(); !strings.Contains(c, "your selfie is up") || strings.Contains(c, "pp:reroll:2") || !strings.Contains(c, "pp:reroll:3") {
		t.Errorf("the quests card with a selfie aside: %s", c)
	}

	s.unpost(900)
	if len(mem.quests) != 4 || mem.quests[2].ref != 0 || !strings.Contains(card(), "came down") {
		t.Fatalf("a selfie taken down once: %+v", mem.quests)
	}
	post(901)
	if mem.quests[2].ref != 901 {
		t.Fatalf("the repost wasn't kept: %+v", mem.quests)
	}
	s.unpost(901)
	if len(mem.quests) != 3 || held(mem.quests) != 3 {
		t.Errorf("a selfie taken down twice: %+v", mem.quests)
	}
}
