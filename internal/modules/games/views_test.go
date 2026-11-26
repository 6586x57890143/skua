package games

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/game/count"
	"github.com/6586x57890143/skua/internal/guard"
)

// command runs /name as member 5 in channel 4 of guild 3, with options,
// and returns what it said.
func command(t *testing.T, run func(context.Context, *events.ApplicationCommandInteractionCreate) error, name string, opts []any, channels map[string]any) (string, discord.MessageCreate, error) {
	t.Helper()
	e, sent := coretest.Event(t, name, func(p map[string]any) {
		d := p["data"].(map[string]any)
		d["options"] = opts
		if channels != nil {
			d["resolved"] = map[string]any{"channels": channels}
		}
	})
	err := run(context.Background(), e)
	if len(*sent) == 0 {
		return "", discord.MessageCreate{}, err
	}
	msg := (*sent)[0]
	return coretest.Text(msg.Content, msg.Components), msg, err
}

// click presses customID as member 5 and returns what the card became, or
// a new message's text.
func click(t *testing.T, m *Module, customID string) (string, error) {
	t.Helper()
	e, _ := coretest.Button(t, customID, nil)
	f := &fakeRest{}
	e.GenericEvent = events.NewGenericEvent(&bot.Client{Rest: f}, 0, 0)
	var got string
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		switch d := d.(type) {
		case discord.MessageUpdate:
			got = coretest.Text("", *d.Components)
		case discord.MessageCreate:
			got = coretest.Text(d.Content, d.Components)
			for _, f := range d.Files {
				b, _ := io.ReadAll(f.Reader)
				got += "\nfile " + f.Name + "\n" + string(b)
			}
		}
		return nil
	}
	err := m.press(context.Background(), e)
	// A deferred press answers by editing its response.
	for _, u := range f.updates {
		got = coretest.Text("", *u.Components)
		for _, file := range u.Files {
			b, _ := io.ReadAll(file.Reader)
			got += "\nfile " + file.Name + "\n" + string(b)
		}
	}
	return got, err
}

func TestCountCommand(t *testing.T) {
	m, _ := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), nil)
	if _, _, err := command(t, m.show, "count", nil, nil); !errors.Is(err, errNotCounting) {
		t.Fatalf("no counting: %v", err)
	}
	tb := &table{t: t, m: m, r: &fakeRest{now: t0}, at: t0}
	m.en.Open(guildID, chanID, count.Standard())
	m.en.Open(guildID, 40, count.Standard())
	tb.count(30)
	m.en.RestoreMember(count.Who{Guild: guildID, User: 5}, count.Member{Counts: 12, XP: 30, Saves: 1, Misses: 2})

	text, msg, err := command(t, m.show, "count", nil, nil)
	if err != nil || msg.Flags&discord.MessageFlagEphemeral == 0 {
		t.Fatalf("%v %v", err, msg.Flags)
	}
	for _, want := range []string{"<#4>", "standard rules", "next  31", "best  30", "### you", "hatchling, rank 1", "▱▱▱▱▱▱▱▱▱▱ 5 of 75 xp", "counts  12", "saves   1 of 2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lacks %q:\n%s", want, text)
		}
	}
	// Another counting channel by name, and one that doesn't count.
	opt := func(id string) []any { return []any{map[string]any{"name": "channel", "type": 7, "value": id}} }
	res := func(id string) map[string]any { return map[string]any{id: map[string]any{"id": id, "type": 0}} }
	if text, _, err := command(t, m.show, "count", opt("40"), res("40")); err != nil || !strings.Contains(text, "<#40>") || !strings.Contains(text, "next  1") {
		t.Fatalf("%v\n%s", err, text)
	}
	if _, _, err := command(t, m.show, "count", opt("77"), res("77")); !errors.Is(err, errNotCounting) {
		t.Fatal(err)
	}
	// Elsewhere in the server, /count shows its first counting channel.
	e, sent := coretest.Event(t, "count", func(p map[string]any) { p["channel"] = map[string]any{"id": "99", "type": 0} })
	if err := m.show(context.Background(), e); err != nil || !strings.Contains(coretest.Text("", (*sent)[0].Components), "<#4>") {
		t.Fatal(err)
	}
	lv, _ := count.Preset("lives")
	m.en.Open(guildID, 40, lv)
	if text, _, _ := command(t, m.show, "count", opt("40"), res("40")); !strings.Contains(text, "3 lives left this run") {
		t.Fatal(text)
	}
	hc, _ := count.Preset("hardcore")
	m.en.Open(guildID, 40, hc)
	if text, _, _ := command(t, m.show, "count", opt("40"), res("40")); !strings.Contains(text, "no saves in hardcore") {
		t.Fatal(text)
	}
}

func TestBoardWithoutADatabase(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.count(9)
	for u := range 14 {
		tb.m.en.RestoreMember(count.Who{Guild: guildID, User: uint64(200 + u)}, count.Member{Counts: int64(50 - u)})
	}
	tb.m.en.RestoreMember(count.Who{Guild: guildID, User: 5}, count.Member{Counts: 1})
	// Another server counting, better.
	tb.m.en.Open(8, 80, count.Standard())
	tb.m.en.Restore(80, count.State{Guild: 8, Best: 500, Rules: count.Standard()})

	got, err := click(t, tb.m, "games:board:4:month")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"### all time", "**1** <@200> · 50", "<@5> · 1", "◂", "number 2 of 2 servers"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n**") > boardRows {
		t.Fatalf("more than %d rows:\n%s", boardRows, got)
	}
	if got, err := click(t, tb.m, "games:view:4"); err != nil || !strings.Contains(got, "### you") {
		t.Fatal(err)
	}
	if _, err := click(t, tb.m, "games:proof:4"); !errors.Is(err, errNoLog) {
		t.Fatal(err)
	}
	if _, err := click(t, tb.m, "games:file:4"); !errors.Is(err, errNoLog) {
		t.Fatal(err)
	}
	// Another server's channel, a channel that doesn't count, junk.
	for _, bad := range []string{"games:board:80:all", "games:view:5", "games:view:x", "games:what:4"} {
		if _, err := click(t, tb.m, bad); !errors.Is(err, errNotCounting) && !errors.Is(err, errNotAdmin) {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	// An empty board.
	empty := newTable(t, count.Standard())
	if got, _ := click(t, empty.m, "games:board:4:all"); !strings.Contains(got, "nobody has counted yet") {
		t.Fatal(got)
	}
}

func TestBar(t *testing.T) {
	if got := bar(0, 0); got != "▱▱▱▱▱▱▱▱▱▱ 0 of 25 xp" {
		t.Fatal(got)
	}
	if got := bar(50, 1); got != "▰▰▰▱▱▱▱▱▱▱ 25 of 75 xp" {
		t.Fatal(got)
	}
}

func TestRecount(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.count(4)
	opts := []any{map[string]any{"name": "number", "type": 4, "value": 5000}}
	text, _, err := command(t, tb.m.recount, "recount", opts, nil)
	if err != nil || text != "✓ <#4> counts on from **5000**: the next number is **5001**" {
		t.Fatalf("%q %v", text, err)
	}
	if len(tb.m.journal.runs) != 1 || tb.m.journal.runs[0].length != 4 {
		t.Fatalf("the run it was on wasn't recorded: %+v", tb.m.journal.runs)
	}
	tb.say(7, "5001")
	if s, _ := tb.m.en.Channel(chanID); s.N != 5001 {
		t.Fatal(s.N)
	}
	// A recount is the truth: a channel that lost track has found it.
	tb.m.lost.Store(snowflake.ID(chanID), struct{}{})
	if _, _, err := command(t, tb.m.recount, "recount", opts, nil); err != nil {
		t.Fatal(err)
	}
	if _, lost := tb.m.lost.Load(snowflake.ID(chanID)); lost {
		t.Fatal("still lost after a recount")
	}
	// A fresh recount on a run with nothing in it records nothing.
	if _, _, err := command(t, tb.m.recount, "recount", opts, nil); err != nil {
		t.Fatal(err)
	}
	none, _ := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), nil)
	if _, _, err := command(t, none.recount, "recount", opts, nil); !errors.Is(err, errNotCounting) {
		t.Fatal(err)
	}
}

func TestCommandsShape(t *testing.T) {
	m, _ := New(context.Background(), slog.New(slog.DiscardHandler), guard.New(), nil)
	cmds := m.Commands()
	if len(cmds) != 2 || cmds[0].Tier == cmds[1].Tier {
		t.Fatal("tiers")
	}
}

// firstMsg is the message that made a run's first count.
func firstMsg(t *testing.T, db *pgxpool.Pool, run int64) uint64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(context.Background(), "select message_id from count_log where channel_id = 4 and run = $1 and n = 1", run).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return uint64(id)
}

// A failed read is said plainly, after the press was acknowledged.
func TestProofOnABrokenDatabase(t *testing.T) {
	tb := newTable(t, count.Standard())
	tb.count(2)
	tb.m.db = failDB{}
	got, err := click(t, tb.m, "games:proof:4")
	if err != nil || !strings.Contains(got, "couldn't read the log") {
		t.Fatalf("%v %q", err, got)
	}
}

// With a database: the month's board, a proof that checks the current
// run and the last that ended, and the file a member checks it from.
func TestBoardAndProofWithADatabase(t *testing.T) {
	db := pool(t)
	ctx := context.Background()
	m, err := New(ctx, slog.New(slog.DiscardHandler), guard.New(), db)
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return t0 }
	if err := m.pick(ctx, guildID, []snowflake.ID{chanID}); err != nil {
		t.Fatal(err)
	}
	tb := &table{t: t, m: m, r: &fakeRest{now: t0}, at: t0}
	tb.count(6)
	tb.say(7, "99") // run 1 ends at 6
	tb.count(3)

	got, err := click(t, m, "games:board:4:month")
	if err != nil || !strings.Contains(got, "### this month") || !strings.Contains(got, "**1** <@101> · 5") {
		t.Fatalf("%v\n%s", err, got)
	}
	if got, _ := click(t, m, "games:board:4:all"); !strings.Contains(got, "### all time") || !strings.Contains(got, "**1** <@101> · 5") {
		t.Fatal(got)
	}
	got, err = click(t, m, "games:proof:4")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.en.Channel(chanID)
	for _, want := range []string{"### run 2", "✓ all 3 counts check out", s.Head.String(), "### run 1, ended", "✓ all 6 counts check out"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lacks %q:\n%s", want, got)
		}
	}
	got, err = click(t, m, "games:file:4")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"file count-4-run-2.txt", "genesis " + count.Genesis(guildID, chanID, 2, 0).String(), "\n1 101 ", "\n3 101 ", "head " + s.Head.String()} {
		if !strings.Contains(got, want) {
			t.Fatalf("lacks %q:\n%s", want, got)
		}
	}
	// A run longer than the file lists its end after the link it follows.
	proofMost = 2
	got, _ = click(t, m, "games:file:4")
	proofMost = 20000
	prev := count.Next(count.Genesis(guildID, chanID, 2, 0), 2, count.Entry{N: 1, User: 101, Msg: firstMsg(t, db, 2)})
	if !strings.Contains(got, "prev "+prev.String()) || strings.Contains(got, "\n1 101 ") || !strings.Contains(got, "\n3 101 ") {
		t.Fatalf("a cut file can't be checked:\n%s", got)
	}
	// A run longer than skua checks says so rather than checking it.
	checkMost = 2
	got, _ = click(t, m, "games:proof:4")
	gotFile, _ := click(t, m, "games:file:4")
	checkMost = 200000
	if !strings.Contains(got, "longer than skua checks here") || !strings.Contains(got, "it ran 6 counts, longer") || !strings.Contains(gotFile, "longer than skua writes out") {
		t.Fatalf("%s\n%s", got, gotFile)
	}
	// A log that doesn't reach the head is said so.
	if _, err := db.Exec(ctx, "delete from count_log where channel_id = 4 and run = 2 and n = 2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := click(t, m, "games:proof:4"); !strings.Contains(got, "✗ the log doesn't reach this head") {
		t.Fatal(got)
	}
}
