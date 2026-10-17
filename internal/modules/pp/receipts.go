package pp

import (
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

// receiptRows is how many of their ledger rows a member sees.
const receiptRows = 8

// reasonWords are the ledger's reasons as members read them.
var reasonWords = map[string]string{
	reasonOpen:   "opening",
	reasonLabour: "a day",
	reasonClass:  "class",
	reasonLevy:   "levy",
}

// receipts is user's own ledger, newest first: their last rows, the day
// still open, and the server's head their rows are chained under.
func (m *Module) receipts(ctx context.Context, guild, user snowflake.ID) (discord.MessageCreate, error) {
	type row struct {
		day, delta int64
		reason     string
		hash       []byte
	}
	var out []row
	rows, err := m.db.Query(ctx, `select day, reason, delta, hash from pp_ledger
		where guild_id = $1 and user_id = $2 order by seq desc limit $3`, int64(guild), int64(user), receiptRows)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.day, &r.reason, &r.delta, &r.hash); err != nil {
			rows.Close()
			return discord.MessageCreate{}, err
		}
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return discord.MessageCreate{}, err
	}

	s := m.st.server(guild)
	s.mu.Lock()
	// Rows chained but not yet written come first, newest first.
	for _, e := range slices.Backward(s.pending) {
		if e.user == user {
			out = append([]row{{e.day, e.delta, e.reason, e.hash}}, out...)
		}
	}
	var open, levy int64
	if mem, ok := s.members[user]; ok {
		open, levy = mem.dayGot, mem.dayLevy
	}
	seq, head := s.seq, s.headHex()
	s.mu.Unlock()
	out = out[:min(len(out), receiptRows)]

	var b strings.Builder
	b.WriteString("```\n")
	fmt.Fprintf(&b, "%-10s  %-7s  %8s  %s\n", "day", "what", "pp", "row")
	if open != 0 {
		fmt.Fprintf(&b, "%-10s  %-7s  %8s  %s\n", "today", "open", signed(open), "")
	}
	for _, r := range out {
		fmt.Fprintf(&b, "%-10s  %-7s  %8s  %x\n", dayDate(r.day), reasonWords[r.reason], signed(r.delta), r.hash[:4])
	}
	b.WriteString("```")
	if len(out) == 0 && open == 0 {
		b.Reset()
		b.WriteString("nothing yet: your first day of earning goes in when the day turns")
	}
	foot := fmt.Sprintf("\n-# each row is chained into the server's ledger by sha256. it is %s rows long and its head is %s",
		strconv.FormatInt(seq, 10), head[:min(len(head), 16)])
	if levy != 0 {
		foot = fmt.Sprintf("\n-# today %s pp of yours went to the treasury", grouped(levy/unit)) + foot
	}
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorInfo, "pp", "ledger", b.String()+foot)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// signed is pp with its sign, for a ledger line.
func signed(v int64) string {
	if v < 0 {
		return "-" + grouped(-v/unit)
	}
	return "+" + grouped(v/unit)
}

// dayDate is a game day as its date.
func dayDate(day int64) string { return time.Unix(day*86400, 0).UTC().Format("2006-01-02") }
