package pp

import (
	"context"
	"slices"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// The journal is a member's own lines in the ledger, newest first: each
// day's earning, quests, prizes, grants and what they bought, and the day
// still open. The ledger itself, chain and head, is the treasury's
// business, on the admins' card.

// journalRows is how many of their ledger lines a member sees.
const journalRows = 8

// journalLine is how a ledger line reads in the journal: its icon and
// words.
func journalLine(reason string, inputs []int64) (icon, what string) {
	switch reason {
	case reasonLabour:
		return "pot", "a day's earning"
	case reasonQuest:
		if len(inputs) > 0 && inputs[0] >= 0 && int(inputs[0]) < len(QuestTiers) {
			return "compass", QuestTiers[inputs[0]] + " quest"
		}
		return "compass", "a quest"
	case reasonJoinder:
		if len(inputs) > 0 && inputs[0] > 2 {
			return "beads", "a round robin"
		}
		return "envelope", "a chirograph"
	case reasonClass:
		return "shield", "a new path"
	case reasonShop:
		if len(inputs) > 0 && inputs[0] > 0 {
			name, icon := perch(inputs[0])
			return icon, name
		}
		return "pot", "the shop"
	case reasonLease:
		if len(inputs) > 0 && inputs[0] >= 0 && int(inputs[0]) < len(dyes) {
			return "scales", dyes[inputs[0]].name
		}
		return "scales", "a livery"
	case reasonHold:
		return "quill", "a petition to the heralds"
	case reasonRelease:
		return "quill", "a petition's price returned"
	case reasonPrize:
		return "laurel", "a weekly prize"
	case reasonGrant:
		return "envelope", "a grant"
	case reasonCorrect:
		return "scales", "a correction"
	}
	return "lantern", "purse carried over"
}

// journal is user's journal as a screen.
func (m *Module) journal(ctx context.Context, guild snowflake.ID, who *discord.ResolvedMember) (discord.MessageCreate, error) {
	user := who.User.ID
	type line struct {
		day, delta int64
		reason     string
		inputs     []int64
	}
	var lines []line
	rows, err := m.db.Query(ctx, `select day, reason, delta, inputs from pp_ledger
		where guild_id = $1 and user_id = $2 order by seq desc limit $3`, int64(guild), int64(user), journalRows)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.day, &l.reason, &l.delta, &l.inputs); err != nil {
			rows.Close()
			return discord.MessageCreate{}, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return discord.MessageCreate{}, err
	}

	s := m.st.server(guild)
	s.mu.Lock()
	// Lines chained but not yet written come first, newest first.
	for _, e := range slices.Backward(s.pending) {
		if e.user == user {
			lines = append([]line{{e.day, e.delta, e.reason, e.inputs}}, lines...)
		}
	}
	var open int64
	if mem, ok := s.members[user]; ok {
		open = mem.dayGot
	}
	s.mu.Unlock()
	lines = lines[:min(len(lines), journalRows)]

	out := make([]JournalRow, len(lines))
	for i, l := range lines {
		icon, what := journalLine(l.reason, l.inputs)
		out[i] = JournalRow{Day: dayDate(l.day), Icon: icon, What: what, PP: l.delta}
	}
	pic, err := JournalScreen(pixelName(who), out, open)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	const alt = "your journal: what came in and went out, newest first"
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorInfo, "pp", "journal", "",
			discord.NewMediaGallery(screenItem(pic, alt)))},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// dayDate is a game day as its date.
func dayDate(day int64) string { return time.Unix(day*86400, 0).UTC().Format("2006-01-02") }
