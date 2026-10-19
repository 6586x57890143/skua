package pp

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// The boards: levels, everything ever earned, which runs before the first
// season and alongside every one; and while a season runs, the season's
// race. Each shows its top boardRows, the member's own line taking the
// last when they're further down. Before the first season the levels board
// runs across the screen over a line on when the first season starts, and
// shows preseasonRows.

const boardRows, preseasonRows = 10, 8

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

// boardTop is a board's first rows, the member's own line in the last place
// when they're further down.
func boardTop(board []standing, user snowflake.ID, n int) []standing {
	if len(board) <= n {
		return board
	}
	out := slices.Clone(board[:n])
	if !slices.ContainsFunc(out, func(st standing) bool { return st.user == user }) {
		if i := slices.IndexFunc(board, func(st standing) bool { return st.user == user }); i >= 0 {
			out[n-1] = board[i]
		}
	}
	return out
}

// boardMessage is the boards for user in guild, as a screen.
func (m *Module) boardMessage(guild, user snowflake.ID) (discord.MessageCreate, error) {
	today := DayOf(m.now())
	season, _ := m.st.bal().Season(today)
	s := m.st.server(guild)
	s.mu.Lock()
	var users []snowflake.ID
	var totals, seasons []int64
	names, icons, classes := map[snowflake.ID]string{}, map[snowflake.ID]string{}, map[snowflake.ID]string{}
	rungs := map[snowflake.ID]int64{}
	for u, mem := range s.members {
		users = append(users, u)
		totals = append(totals, mem.total)
		pp := int64(0)
		if mem.season == season {
			pp = mem.seasonPP
		}
		seasons = append(seasons, pp)
		names[u], icons[u], classes[u] = or(mem.name, "someone"), classIcons[ClassOf(mem.class).Key], ClassOf(mem.class).Name()
		rungs[u] = mem.rung
	}
	s.mu.Unlock()

	b := m.st.bal()
	rows := func(board []standing, n int, value func(standing) string, gold bool) []BoardRow {
		var out []BoardRow
		for _, st := range boardTop(board, user, n) {
			out = append(out, BoardRow{Place: st.place, Icon: icons[st.user], Name: names[st.user], Rung: rungs[st.user], Class: classes[st.user],
				Value: value(st), You: st.user == user, ValueIsGold: gold})
		}
		return out
	}
	level := func(st standing) string { l, _, _ := b.Level(st.score); return fmt.Sprintf("lv %d", l) }
	levelBoard := rank(users, totals)

	var levels, seasonRows []BoardRow
	tab, starts := "", ""
	if season > 0 {
		tab = fmt.Sprintf("season %d", season)
		levels = rows(levelBoard, boardRows, level, false)
		seasonRows = rows(rank(users, seasons), boardRows, func(st standing) string { return grouped(st.score / unit) }, true)
	} else {
		starts = strings.ToLower(time.Unix(b.SeasonStart*86400, 0).UTC().Format("2 Jan"))
		levels = rows(levelBoard, preseasonRows, level, false)
	}
	pic, err := BoardScreen(levels, seasonRows, tab, starts)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := "the levels board"
	if len(levels) > 0 {
		alt += ", led by " + levels[0].Name
	}
	if tab != "" {
		alt += ", and the " + tab + " board"
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "board", "",
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// treasuryMessage is the server's money for its admins, as a screen: what
// the treasury holds and whether it's taking, what came in and went out
// this season (or in the preseason), what members hold, and how the shop
// and the ledger stand.
func (m *Module) treasuryMessage(ctx context.Context, guild snowflake.ID) (discord.MessageCreate, error) {
	today := DayOf(m.now())
	b := m.st.bal()
	season, _ := b.Season(today)
	from, period := int64(0), "preseason"
	if season > 0 {
		from, period = DayOf(b.seasonStart(season)), fmt.Sprintf("season %d", season)
	}
	rows, err := m.db.Query(ctx, `select reason, user_id = 0, sum(delta)::bigint from pp_ledger
		where guild_id = $1 and day >= $2 and (user_id = 0 or reason = 'quest') group by 1, 2`, int64(guild), from)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	t := Treasury{Period: period}
	for rows.Next() {
		var reason string
		var treasury bool
		var sum int64
		if err := rows.Scan(&reason, &treasury, &sum); err != nil {
			rows.Close()
			return discord.MessageCreate{}, err
		}
		switch {
		case treasury && reason == reasonLevy:
			t.Levy = sum
		case treasury && reason == reasonPrize:
			t.Prizes = -sum
		case treasury && reason == reasonGrant:
			t.Granted = -sum
		case !treasury && reason == reasonQuest:
			t.Quest = sum
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return discord.MessageCreate{}, err
	}

	s := m.st.server(guild)
	s.mu.Lock()
	t.Balance, t.Taking, t.Backstop = s.treasury, s.levying(), s.heavy
	t.Levy += s.levyOpen
	t.Grants = "open"
	if s.levySince == 0 || today-s.levySince < Rules.GrantsAfter {
		t.Grants = strings.TrimPrefix(grantsRow(s, today), "open ")
	}
	week := s.grantWeekSum
	if s.grantWeek != weekOf(today) {
		week = 0
	}
	t.GrantRoom = max(Rules.GrantShare*(s.treasury+week)/100-week, 0)
	var purses []int64
	for _, mem := range s.members {
		t.Supply += mem.purse
		if mem.dayN > today-28 {
			purses = append(purses, mem.purse)
		}
	}
	t.Burned = s.seasonBurn * 100 / max(s.seasonMint, 1)
	t.Rows, t.Head = s.seq, s.headHex()
	s.mu.Unlock()
	t.Active = len(purses)
	if len(purses) > 0 {
		slices.Sort(purses)
		var all, head int64
		cut := len(purses) - max(len(purses)/100, 1)
		for i, p := range purses {
			all += p
			if i >= cut {
				head += p
			}
		}
		t.Median = purses[len(purses)/2]
		t.Top = head * 100 / max(all, 1)
	}
	t.Head = t.Head[:min(len(t.Head), 8)]

	pic, err := TreasuryScreen(t)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := fmt.Sprintf("the treasury holds %s pp", grouped(t.Balance/unit))
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorInfo, "pp", "treasury",
			"-# for admins: the levy in, prizes and grants out, what members hold and the ledger it's all written in",
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}
