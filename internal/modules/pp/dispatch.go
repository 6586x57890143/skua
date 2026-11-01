package pp

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// morning is user's dispatch for today, "" once they've had it.
func (m *Module) morning(guild, user snowflake.ID) string {
	today := DayOf(m.now())
	seed := m.st.seedOf(today)
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	return m.dispatch(s, m.st.member(s, guild, user, today), seed, uint64(guild), uint64(user), today)
}

// A dispatch is one line of the morning's news above the first /pp a member
// opens each day: the world's (the same for everyone that day), their own,
// or a rumour. Which one is drawn from the season's seed, the day and the
// member, so nobody can reroll it. The lines are LORE.md's "The broadsheet".

// slotDispatch is the roll a day's dispatch draws from, apart from quests'.
const slotDispatch = 1 << 42

// dispatch is mem's line for today, "" once they've had it. s.mu held.
func (m *Module) dispatch(s *server, mem *member, seed, guild, user uint64, today int64) string {
	if mem.dispatchDay == today {
		return ""
	}
	mem.dispatchDay = today
	r := Roll(seed, guild, user, today, slotDispatch)
	world := worldLine(m.st.bal(), today)
	own := m.ownLine(s, mem, r>>32, today)
	rumour := rumours[(r>>8)%uint64(len(rumours))]
	// Half the days the world's news, a quarter their own, a quarter a
	// rumour, each falling through to the next when it has nothing.
	order := []string{world, own, rumour}
	switch r % 4 {
	case 2:
		order = []string{own, world, rumour}
	case 3:
		order = []string{rumour}
	}
	for _, l := range order {
		if l != "" {
			return l
		}
	}
	return ""
}

// worldLine is the world's news on today, "" past what's written.
func worldLine(b *Balance, today int64) string {
	season, _ := b.Season(today)
	lines := worldLines[season]
	if d := today - b.SeasonStart - (season-1)*7*b.SeasonWeeks; season > 0 && d < int64(len(lines)) {
		return lines[d]
	}
	return ""
}

// ownLine is news about mem, picked by r among what's true of them today.
// s.mu held.
func (m *Module) ownLine(s *server, mem *member, r uint64, today int64) string {
	var lines []string
	if mem.livery != "" && mem.liveryUntil == today {
		lines = append(lines, "the dyers' yard sends word that your livery fades tonight unless the lease is paid")
	}
	if l, into, need := m.st.bal().Level(mem.total); need-into <= need/10 {
		lines = append(lines, fmt.Sprintf("a raven at the rim has you %s pp short of level %d", grouped((need-into+unit-1)/unit), l+1))
	}
	if s.hearth() > 0 {
		lines = append(lines, "the herons lit the hearth before dawn, and what you pour in today goes further")
	}
	if len(lines) == 0 {
		return ""
	}
	return lines[r%uint64(len(lines))]
}

// withDispatch is msg with line as subtext under its card's heading.
func withDispatch(msg discord.MessageCreate, line string) discord.MessageCreate {
	if line == "" || len(msg.Components) == 0 {
		return msg
	}
	card, ok := msg.Components[0].(discord.ContainerComponent)
	if !ok || len(card.Components) == 0 {
		return msg
	}
	head, ok := card.Components[0].(discord.TextDisplayComponent)
	if !ok {
		return msg
	}
	head.Content = strings.TrimRight(head.Content, "\n") + "\n-# " + line
	card.Components[0] = head
	msg.Components[0] = card
	return msg
}
