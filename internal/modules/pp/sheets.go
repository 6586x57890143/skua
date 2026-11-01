package pp

import (
	"context"
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/pixel"
)

// The admins' money is a set of sheets, one screen each in the same frame,
// paged like the lore: the treasury first, then the house. A new fund gets
// a sheet of its own rather than squeezing into one already full.

// treasurySheets is how many sheets there are.
const treasurySheets = 2

// sheetID is the press that opens sheet n.
func sheetID(n int) string {
	// Never the menu's own pp:treasury: Discord refuses two of one ID.
	return "pp:treasury:" + strconv.Itoa(n)
}

// parseSheet is the sheet a press asks for, 0 for one it can't read.
func parseSheet(id string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "pp:treasury:"))
	if err != nil || n < 0 || n >= treasurySheets {
		return 0
	}
	return n
}

// sheetTab is a sheet's tab: its name and where it is in the set.
func sheetTab(name string, n int) string { return fmt.Sprintf("%s %d/%d", name, n+1, treasurySheets) }

// sheetMessage is admin sheet n of guild, with the buttons to turn it.
func (m *Module) sheetMessage(ctx context.Context, guild snowflake.ID, n int) (discord.MessageCreate, error) {
	var msg discord.MessageCreate
	var err error
	if n == 1 {
		msg, err = m.houseSheetMessage(ctx, guild)
	} else {
		msg, err = m.treasuryMessage(ctx, guild)
	}
	if err != nil {
		return msg, err
	}
	// Back and on, but one button when both lead to the same sheet: Discord
	// refuses a message with two of the same ID.
	prev, next := (n+treasurySheets-1)%treasurySheets, (n+1)%treasurySheets
	turn := []discord.InteractiveComponent{discord.NewSecondaryButton("▸", sheetID(next))}
	if prev != next {
		turn = append([]discord.InteractiveComponent{discord.NewSecondaryButton("◂", sheetID(prev))}, turn...)
	}
	card := msg.Components[0].(discord.ContainerComponent)
	card.Components = append(card.Components, discord.NewActionRow(turn...))
	msg.Components[0] = card
	return msg, nil
}

// paneRow sets row i of the window over r: an icon, a label and a value
// set to its right edge.
func paneRow(c *pixel.Canvas, r image.Rectangle, i int, icon, name, value string, ink uint8) {
	x, y := r.Min.X+12, paneTop(r)+i*19
	if icon != "" {
		c.Icon(icon, image.Pt(x, y-iconLift))
	}
	label(c, x+pixel.IconCells+4, y, name)
	right(c, r.Max.X-12, y, value, ink)
}

// HouseSheet is the house as its sheet shows it: the hold against its line
// and the note it started from, what this period staked, won and pumped,
// the edge the bones have kept against the one the table promises, and
// how much the tables are played.
type HouseSheet struct {
	Period              string
	Hold, Line, Note    int64
	Staked, Won, Pumped int64
	// Edge and Week are the edge kept this period and these seven days, in
	// basis points of what was staked; noEdge when nothing was.
	Edge, Week       int64
	Throws, Throwers int64
	Bells            int64
	Best             int64 // the largest win
}

// expectedEdge is what the table keeps, in basis points: 1 - 0.925.
const expectedEdge = 750

// Healthy is the house's health in one word's worth: the hold keeps at
// least half its note, and the last seven days kept an edge, or nobody
// threw. It's a fact for the admins, with nothing automatic behind it.
//
// The plan measured the hold against half its line; with the line at ten
// notes the hold sits under that for months while it fills, so it's half
// the note it started from instead.
func (h HouseSheet) Healthy() bool { return h.Hold*2 >= h.Note && (h.Week == noEdge || h.Week > 0) }

// noEdge is the edge of nothing staked: no real edge reaches it.
const noEdge = math.MinInt64

// edgeOf is staked less won over staked, in basis points, noEdge for none.
func edgeOf(staked, won int64) int64 {
	if staked == 0 {
		return noEdge
	}
	return (staked - won) * 10000 / staked
}

// edgeWord is an edge as admins read it.
func edgeWord(bp int64) string {
	if bp == noEdge {
		return "no throws"
	}
	return strconv.FormatFloat(float64(bp)/100, 'f', 1, 64) + "%"
}

// houseSheetMessage is the house's sheet for admins.
func (m *Module) houseSheetMessage(ctx context.Context, guild snowflake.ID) (discord.MessageCreate, error) {
	today := DayOf(m.now())
	b := m.st.bal()
	season, _ := b.Season(today)
	from, period := int64(0), "preseason"
	if season > 0 {
		from, period = DayOf(b.seasonStart(season)), fmt.Sprintf("season %d", season)
	}
	h := HouseSheet{Period: period, Line: houseLine(), Note: Rules.HouseNote * unit}
	// A refunded stake was never thrown, so it comes off what was staked;
	// the edge is measured on wins against a stake (a win's third input),
	// which the house's own stakes on its marker aren't.
	var wonStaked, refunds, refunded int64
	if err := one(ctx, m.db, `select coalesce(-sum(delta) filter (where reason = 'stake'), 0)::bigint,
			coalesce(sum(delta) filter (where reason = 'refund'), 0)::bigint,
			count(*) filter (where reason = 'stake'), count(*) filter (where reason = 'refund'),
			count(distinct user_id) filter (where reason = 'stake'),
			coalesce(sum(delta) filter (where reason = 'win'), 0)::bigint,
			coalesce(sum(delta) filter (where reason = 'win' and inputs[3] <> 0), 0)::bigint,
			coalesce(max(delta) filter (where reason = 'win'), 0)::bigint,
			coalesce(-sum(delta) filter (where reason = 'pump'), 0)::bigint
		from pp_ledger where guild_id = $1 and day >= $2 and reason in ('stake', 'refund', 'win', 'pump')`, []any{int64(guild), from},
		&h.Staked, &refunds, &h.Throws, &refunded, &h.Throwers, &h.Won, &wonStaked, &h.Best, &h.Pumped); err != nil {
		return discord.MessageCreate{}, err
	}
	h.Staked, h.Throws = h.Staked-refunds, h.Throws-refunded
	var weekStaked, weekWon int64
	if err := one(ctx, m.db, `select coalesce(-sum(delta) filter (where reason in ('stake', 'refund')), 0)::bigint,
			coalesce(sum(delta) filter (where reason = 'win' and inputs[3] <> 0), 0)::bigint
		from pp_ledger where guild_id = $1 and day > $2 and reason in ('stake', 'refund', 'win')`, []any{int64(guild), today - 7}, &weekStaked, &weekWon); err != nil {
		return discord.MessageCreate{}, err
	}
	if err := one(ctx, m.db, `select coalesce(sum(n), 0)::bigint from pp_stat where guild_id = $1 and what = $2 and day >= $3`,
		[]any{int64(guild), statBell, from}, &h.Bells); err != nil {
		return discord.MessageCreate{}, err
	}
	h.Edge, h.Week = edgeOf(h.Staked, wonStaked), edgeOf(weekStaked, weekWon)
	s := m.st.server(guild)
	s.mu.Lock()
	h.Hold = s.hold
	if !s.noted {
		h.Hold = h.Note
	}
	s.mu.Unlock()

	pic, err := HouseSheetScreen(h)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	health := "healthy"
	if !h.Healthy() {
		health = "watch"
	}
	alt := fmt.Sprintf("the house: the hold at %s pp of its %s pp line, %s; this %s %s pp staked, %s won, %s pumped",
		grouped(h.Hold/unit), grouped(h.Line/unit), health, h.Period, grouped(h.Staked/unit), grouped(h.Won/unit), grouped(h.Pumped/unit))
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorInfo, "pp", "the house",
			"-# for admins: the slack tide's hold, its own fund, apart from the treasury. what the tables took and paid, and what the pumps burned",
			discord.NewMediaGallery(screenItem(pic, alt)))},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// HouseSheetScreen draws the house's sheet.
func HouseSheetScreen(h HouseSheet) ([]byte, error) { return houseSheetCanvas(h).Screen(screenScale) }

func houseSheetCanvas(h HouseSheet) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	hold := image.Rect(4, 4, 190, 106)
	c.Window(hold)
	c.Tab(hold, sheetTab("the house", 1))
	paneRow(c, hold, 0, "pot", "the hold", grouped(h.Hold/unit)+" pp", pixel.Gold)
	paneRow(c, hold, 1, "", "the line", grouped(h.Line/unit)+" pp", pixel.Ink)
	tide := "tide's out"
	if h.Hold >= h.Line {
		tide = "tide's in"
	}
	paneRow(c, hold, 2, "", "the tide", tide, pixel.Ink)
	health, ink := "healthy", pixel.Green
	if !h.Healthy() {
		health, ink = "watch", pixel.Red
	}
	paneRow(c, hold, 3, "", "health", health, ink)

	in := image.Rect(194, 4, 380, 106)
	c.Window(in)
	c.Tab(in, h.Period)
	paneRow(c, in, 0, "candle", "staked", "+"+grouped(h.Staked/unit), pixel.Green)
	paneRow(c, in, 1, "lantern", "won", "-"+grouped(h.Won/unit), pixel.Red)
	paneRow(c, in, 2, "", "pumped, burned", grouped(h.Pumped/unit), pixel.Ink)
	paneRow(c, in, 3, "", "edge kept", edgeWord(h.Edge), pixel.Ink)

	tb := image.Rect(4, 110, 190, 212)
	c.Window(tb)
	c.Tab(tb, "the tables")
	paneRow(c, tb, 0, "bones", "throws", grouped(h.Throws), pixel.Ink)
	paneRow(c, tb, 1, "", "birds", grouped(h.Throwers), pixel.Ink)
	paneRow(c, tb, 2, "bell", "bells rung", grouped(h.Bells), pixel.Ink)
	paneRow(c, tb, 3, "", "biggest win", grouped(h.Best/unit)+" pp", pixel.Gold)

	wk := image.Rect(194, 110, 380, 212)
	c.Window(wk)
	c.Tab(wk, "the edge")
	paneRow(c, wk, 0, "", "these 7 days", edgeWord(h.Week), pixel.Ink)
	paneRow(c, wk, 1, "", "the table's", edgeWord(expectedEdge), pixel.Dim)
	paneRow(c, wk, 2, "envelope", "the note", grouped(h.Note/unit)+" pp", pixel.Ink)
	paneRow(c, wk, 3, "", "up to the line", fmt.Sprintf("%d%%", h.Hold*100/max(h.Line, 1)), pixel.Ink)
	return c
}

// one reads a query's single row into dst.
func one(ctx context.Context, db DB, sql string, args []any, dst ...any) error {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return fmt.Errorf("pp: no row from %q", sql)
	}
	if err := rows.Scan(dst...); err != nil {
		return err
	}
	return rows.Err()
}
