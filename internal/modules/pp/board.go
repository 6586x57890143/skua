package pp

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// The boards: levels, everything ever earned, which runs before the first
// season and alongside every one; and while a season runs, the season's
// race, which leads. One board fills a screen, boardRows a page, and the
// first page gives its last line to the member when they're further down.
// Before the first season the levels board stands over a line on when the
// first season starts, preseasonRows a page.

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

// boardPage is page p of a board n rows a page, the first as boardTop has
// it; p past the last is the last.
func boardPage(board []standing, user snowflake.ID, n, p int) (rows []standing, page, pages int) {
	pages = max((len(board)+n-1)/n, 1)
	page = min(max(p, 0), pages-1)
	if page == 0 {
		return boardTop(board, user, n), 0, pages
	}
	return board[page*n : min((page+1)*n, len(board))], page, pages
}

// boardID is the press that shows page p of the levels board, or of the
// season's.
func boardID(levels bool, p int) string {
	if levels {
		return fmt.Sprintf("pp:board:levels:%d", p)
	}
	return fmt.Sprintf("pp:board:season:%d", p)
}

// parseBoard reads a press on the boards: plain pp:board is the first page
// of the one that leads. The page comes from the client, so boardPage
// clamps it.
func parseBoard(id string) (levels, plain bool, page int) {
	f := strings.Split(id, ":")
	if len(f) != 4 {
		return false, true, 0
	}
	page, _ = strconv.Atoi(f[3])
	return f[2] == "levels", false, page
}

// boardMessage is one page of the boards for user in guild, as a screen,
// with the turns between pages and boards under it.
func (m *Module) boardMessage(guild, user snowflake.ID, id string) (discord.MessageCreate, error) {
	today := DayOf(m.now())
	season, _ := m.st.bal().Season(today)
	levels, plain, p := parseBoard(id)
	if season == 0 || plain {
		levels = season == 0
	}
	s := m.st.server(guild)
	s.mu.Lock()
	var users []snowflake.ID
	var scores []int64
	names, icons, classes := map[snowflake.ID]string{}, map[snowflake.ID]string{}, map[snowflake.ID]string{}
	rungs := map[snowflake.ID]int64{}
	for u, mem := range s.members {
		users = append(users, u)
		switch {
		case levels:
			scores = append(scores, mem.total)
		case mem.season == season:
			scores = append(scores, mem.seasonPP)
		default:
			scores = append(scores, 0)
		}
		names[u], icons[u], classes[u] = or(mem.name, "someone"), classIcons[ClassOf(mem.class).Key], ClassOf(mem.class).Name()
		rungs[u] = mem.rung
	}
	s.mu.Unlock()

	b := m.st.bal()
	value := func(st standing) string { return grouped(st.score / unit) }
	if levels {
		value = func(st standing) string { l, _, _ := b.Level(st.score); return fmt.Sprintf("lv %d", l) }
	}
	n, tab, starts := boardRows, fmt.Sprintf("season %d", season), ""
	if levels {
		tab = "levels"
	}
	if season == 0 {
		n, starts = preseasonRows, strings.ToLower(time.Unix(b.SeasonStart*86400, 0).UTC().Format("2 Jan"))
	}
	page, at, pages := boardPage(rank(users, scores), user, n, p)
	var rows []BoardRow
	for _, st := range page {
		rows = append(rows, BoardRow{Place: st.place, Icon: icons[st.user], Name: names[st.user], Rung: rungs[st.user], Class: classes[st.user],
			Value: value(st), You: st.user == user, ValueIsGold: !levels})
	}
	pic, err := BoardScreen(rows, tab, starts)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := "the " + tab + " board"
	if len(rows) > 0 && at == 0 {
		alt += ", led by " + rows[0].Name
	}
	if pages > 1 {
		alt += fmt.Sprintf(", page %d of %d", at+1, pages)
	}

	var turns []discord.InteractiveComponent
	if season > 0 {
		turns = append(turns,
			withIcon(discord.NewSecondaryButton(fmt.Sprintf("season %d", season), boardID(false, 0)), "laurel").WithDisabled(!levels),
			discord.NewSecondaryButton("levels", boardID(true, 0)).WithDisabled(levels))
	}
	if pages > 1 {
		turns = append(turns,
			discord.NewSecondaryButton("◂", boardID(levels, at-1)).WithDisabled(at == 0),
			discord.NewSecondaryButton(fmt.Sprintf("%d / %d", at+1, pages), "pp:board:page").AsDisabled(),
			discord.NewSecondaryButton("▸", boardID(levels, at+1)).WithDisabled(at == pages-1))
	}
	extra := []discord.ContainerSubComponent{discord.NewMediaGallery(screenItem(pic, alt))}
	if len(turns) > 0 {
		extra = append(extra, discord.NewActionRow(turns...))
	}
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "board", "", extra...)},
		Files:           []*discord.File{screenAttachment(pic, alt)},
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
		where guild_id = $1 and day >= $2 and (user_id = 0 or reason in ('quest', 'joinder')) group by 1, 2`, int64(guild), from)
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
		case !treasury && reason == reasonJoinder:
			t.Joinder = sum
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
	t.Fund = s.fund
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
	alt := fmt.Sprintf("the treasury holds %s pp; this %s quests paid %s pp and joinders %s pp",
		grouped(t.Balance/unit), t.Period, grouped(t.Quest/unit), grouped(t.Joinder/unit))
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorInfo, "pp", "treasury",
			"-# for admins: the levy in, prizes and grants out, what members hold and the ledger it's all written in",
			discord.NewMediaGallery(screenItem(pic, alt)))},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}
