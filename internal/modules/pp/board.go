package pp

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// The boards: levels, everything ever earned, which runs before the first
// season and alongside every one; and while a season runs, the season's
// race. Each shows its top boardRows and where the member stands.

const boardRows = 10

// standing is one member on a board.
type standing struct {
	user  snowflake.ID
	score int64
	place int
}

// rank is every member with a score, best first, with standard places.
func rank(users []snowflake.ID, scores []int64) []standing {
	places := Places(scores)
	var out []standing
	for i, u := range users {
		if places[i] > 0 {
			out = append(out, standing{u, scores[i], places[i]})
		}
	}
	slices.SortFunc(out, func(a, b standing) int { return cmp.Or(cmp.Compare(a.place, b.place), cmp.Compare(a.user, b.user)) })
	return out
}

// boardLines is a board's top rows and, below them if they aren't among
// them, the member's own line.
func boardLines(board []standing, user snowflake.ID, line func(standing) string) string {
	var b strings.Builder
	mine := false
	for i, st := range board {
		if i == boardRows {
			break
		}
		mine = mine || st.user == user
		b.WriteString(line(st) + "\n")
	}
	if !mine {
		for _, st := range board {
			if st.user == user {
				b.WriteString("-# further down\n" + line(st) + "\n")
			}
		}
	}
	if len(board) == 0 {
		b.WriteString("-# nobody yet\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// boardMessage is the boards for user in guild.
func (m *Module) boardMessage(guild, user snowflake.ID) discord.MessageCreate {
	today := DayOf(m.now())
	season, _ := m.st.b.Season(today)
	s := m.st.server(guild)
	s.mu.Lock()
	var users []snowflake.ID
	var totals, seasons []int64
	for u, mem := range s.members {
		users = append(users, u)
		totals = append(totals, mem.total)
		pp := int64(0)
		if mem.season == season {
			pp = mem.seasonPP
		}
		seasons = append(seasons, pp)
	}
	s.mu.Unlock()

	b := m.st.b
	you := func(st standing) string {
		if st.user == user {
			return " ← you"
		}
		return ""
	}
	levels := boardLines(rank(users, totals), user, func(st standing) string {
		l, _, _ := b.Level(st.score)
		return fmt.Sprintf("%d · <@%s> · level %d%s", st.place, st.user, l, you(st))
	})
	body := "### levels\n" + levels
	if season > 0 {
		body += fmt.Sprintf("\n### season %d\n", season) + boardLines(rank(users, seasons), user, func(st standing) string {
			return fmt.Sprintf("%d · <@%s> · %s pp%s", st.place, st.user, grouped(st.score/unit), you(st))
		})
	} else {
		start := m.st.b.seasonStart(1)
		body += "\n-# the first season starts " + dayDate(DayOf(start)) + "; until then the levels are the board"
	}
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "board", body)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}
}
