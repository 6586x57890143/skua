package pp

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/6586x57890143/skua/internal/pixel"
)

// tideRest records the house's posts and edits.
type tideRest struct {
	rest.Rest
	next   snowflake.ID
	posts  []string
	edits  []string
	to     []snowflake.ID
	failed bool
}

func (f *tideRest) CreateMessage(ch snowflake.ID, c discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	if f.failed {
		return nil, errors.New("down")
	}
	f.next++
	f.to = append(f.to, ch)
	f.posts = append(f.posts, js(c.Components)+c.Content)
	return &discord.Message{ID: 1000 + f.next}, nil
}

func (f *tideRest) UpdateMessage(_ snowflake.ID, msg snowflake.ID, u discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.edits = append(f.edits, js(u.Components))
	return &discord.Message{ID: msg}, nil
}

// houseModule is a module with the house open in channel 4, the
// channel coretest's presses come from, and member 5 with pp to stake.
func houseModule(t *testing.T) (*Module, *tideRest) {
	t.Helper()
	m := testModule(&fakeDB{})
	r := &tideRest{}
	m.rest, m.Guard = r, guard.New()
	s := m.st.server(guild)
	s.mu.Lock()
	s.houseCh = 4
	mem := m.st.member(s, guild, 5, DayOf(t0))
	mem.purse, mem.total = 1000*unit, 1000*unit
	s.mu.Unlock()
	return m, r
}

func TestHouseChannelPick(t *testing.T) {
	m := testModule(&fakeDB{})
	admin := false
	m.Admin = func(discord.Interaction) bool { return admin }
	pick := func() *events.ComponentInteractionCreate {
		e, _ := coretest.Button(t, "pp:house", func(p map[string]any) {
			p["data"] = map[string]any{"custom_id": "pp:house", "component_type": 8, "values": []any{"77"},
				"resolved": map[string]any{"channels": map[string]any{"77": map[string]any{"id": "77", "type": 0, "name": "tide", "permissions": "0"}}}}
		})
		return e
	}
	if err := m.press(context.Background(), pick()); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member picked the house channel: %v", err)
	}
	admin = true
	if err := m.press(context.Background(), pick()); err != nil {
		t.Fatal(err)
	}
	if s := m.st.server(guild); s.houseCh != 77 {
		t.Errorf("the house channel is %d", s.houseCh)
	}
	if !strings.Contains(js(m.Report(guild).Controls), "pp:house") {
		t.Error("the help page has no house picker")
	}
}

// A bird opens a table from the slack tide's screen, sits at it, and the
// bones are thrown and shown on its message.
func TestTableFlow(t *testing.T) {
	m, r := houseModule(t)
	e, sent := coretest.Button(t, "pp:tide", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := js(*sent); !strings.Contains(got, "the slack tide") || !strings.Contains(got, "pp:table") {
		t.Fatalf("the slack tide's screen: %s", got)
	}

	e, _ = coretest.Button(t, "pp:table", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(r.posts) != 1 || r.to[0] != 4 || !strings.Contains(r.posts[0], "pp:sit:") {
		t.Fatalf("the table posted %v", r.posts)
	}
	s := m.st.server(guild)
	tb := s.table
	if tb.msg == 0 || !strings.Contains(r.posts[0], tb.slate()) {
		t.Error("the table went out without its slate")
	}
	if e, _ := coretest.Button(t, "pp:table", nil); !errors.Is(m.press(context.Background(), e), errTableOpen) {
		t.Error("a second table opened beside the first")
	}

	// The tide screen now points at the table.
	e, sent = coretest.Button(t, "pp:tide", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js(*sent), "go to the table") {
		t.Error("the tide screen doesn't point at the open table")
	}

	sit := fmt.Sprintf("pp:sit:%d:1", tb.id)
	e, _ = coretest.Button(t, sit, nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(tb.seats) != 1 || s.members[5].seat != stakes[1].pp {
		t.Fatalf("seats %+v", tb.seats)
	}
	s.mu.Lock()
	msg, _ := m.tableMessage(s, tb)
	s.mu.Unlock()
	if !strings.Contains(js(msg), "the glass runs out") {
		t.Error("the table doesn't say when the glass runs out")
	}
	for _, bad := range []string{"pp:sit:1:1", fmt.Sprintf("pp:sit:%d:9", tb.id), "pp:sit:x"} {
		if e, _ := coretest.Button(t, bad, nil); !errors.Is(m.press(context.Background(), e), errTableShut) {
			t.Errorf("%s seated someone", bad)
		}
	}

	m.settleTable(context.Background(), guild, tb)
	if !tb.settled || s.members[5].seat != 0 {
		t.Fatal("the table never threw")
	}
	if len(r.edits) != 1 || !strings.Contains(r.edits[0], fmt.Sprintf("%x", tb.seed)) {
		t.Errorf("the thrown table didn't show its slate turned round: %v", r.edits)
	}
	m.settleTable(context.Background(), guild, tb)
	if len(r.edits) != 1 {
		t.Error("a table threw twice")
	}
}

// A full table throws at once, and a hundred in the drowning hour calls
// all hands.
func TestTableFullAndAllHands(t *testing.T) {
	m, r := houseModule(t)
	s := m.st.server(guild)
	s.mu.Lock()
	tb, err := m.st.open(s, 4, t0, true)
	tb.msg = 900
	// A slate whose first bird throws a hundred.
	for i := 0; ; i++ {
		tb.seed = []byte(fmt.Sprint("slate", i))
		if throwOf(tb.seed, tb.id, old(1)) == 100 {
			break
		}
	}
	for i := 1; i <= tableSeats-1; i++ {
		mem := m.st.member(s, guild, old(i), DayOf(t0))
		mem.purse, mem.total = 100*unit, 100*unit
		if err := m.st.sitDown(s, tb, guild, old(i), "bird", stakes[0].pp, t0); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e, _ := coretest.Button(t, fmt.Sprintf("pp:sit:%d:0", tb.id), nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if !tb.settled {
		t.Fatal("a full table didn't throw")
	}
	hands := 0
	for _, p := range r.posts {
		if strings.Contains(p, "all hands") {
			hands++
		}
	}
	if hands != 1 || s.bell.hands != 1 || len(s.bell.throwers) != tableSeats {
		t.Errorf("all hands called %d times, the bell tallied %d", hands, s.bell.hands)
	}
}

// A table whose timer never fired is thrown by the minute's work.
func TestStaleTable(t *testing.T) {
	m, _ := houseModule(t)
	s := m.st.server(guild)
	s.mu.Lock()
	tb, _ := m.st.open(s, 4, t0, false)
	if err := m.st.sitDown(s, tb, guild, 5, "x", stakes[0].pp, t0); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	m.now = func() time.Time { return t0.Add(tableStale) }
	m.staleTables(context.Background())
	if !tb.settled {
		t.Error("a stale table stayed open")
	}
}

// The bell's one message goes up an hour before, is edited through the
// hour and ends with the tally.
func TestBellMessage(t *testing.T) {
	m, r := houseModule(t)
	at := Bells(m.st.seedOf(DayOf(t0)+1), DayOf(t0)+1)[0]
	for _, d := range []time.Duration{-50 * time.Minute, -49 * time.Minute, -10 * time.Minute, 5 * time.Minute, 70 * time.Minute, 3 * time.Hour} {
		m.now = func() time.Time { return at.Add(d) }
		m.bells(context.Background())
	}
	if len(r.posts) != 1 || !strings.Contains(r.posts[0], "drawing back") {
		t.Fatalf("the bell posted %v", r.posts)
	}
	if len(r.edits) != 3 || !strings.Contains(r.edits[0], "shingle") || !strings.Contains(r.edits[1], "pp:table") || !strings.Contains(r.edits[2], "the bell's stopped") {
		t.Errorf("the bell's edits: %v", r.edits)
	}
	if s := m.st.server(guild); s.stats[statKey{DayOf(at), statBell, 0, 0}] != 1 {
		t.Error("the bell wasn't counted once")
	}

	// A table opened in the hour pays the bell's fifteen.
	m.now = func() time.Time { return at.Add(time.Minute) }
	e, _ := coretest.Button(t, "pp:table", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if s := m.st.server(guild); !s.table.bell {
		t.Error("a table in the drowning hour isn't the bell's")
	}
}

// A table that can't be posted is taken down again, so the next can open.
func TestTablePostFails(t *testing.T) {
	m, r := houseModule(t)
	r.failed = true
	e, _ := coretest.Button(t, "pp:table", nil)
	if err := m.press(context.Background(), e); err == nil {
		t.Fatal("a table that never posted opened")
	}
	if m.st.server(guild).table != nil {
		t.Error("the unposted table is still open")
	}
}

// The screens draw for real.
func TestHouseScreens(t *testing.T) {
	realScreens(t)
	seats := []TableSeat{{Name: "steve", Class: Classes[2], Stake: stakes[3].pp, Throw: 100, Won: stakes[3].pp * 10}, {Name: "tester", Class: Classes[0], Stake: stakes[0].pp, Throw: 3}}
	for _, tb := range []Table{{}, {Seats: seats}, {Seats: seats, Thrown: true, Bell: true}} {
		pic, err := TableScreen(tb)
		if err != nil || !strings.HasPrefix(string(pic), "RIFF") {
			t.Errorf("a table screen isn't moving: %v", err)
		}
	}
	for _, h := range []House{{Hold: 100, Line: 1000}, {Hold: 1000, Line: 1000, Bell: true}} {
		pic, err := HouseScreen(h)
		if err != nil || !strings.HasPrefix(string(pic), "RIFF") {
			t.Errorf("the hold screen isn't moving: %v", err)
		}
	}
}

// The throws' slates sit over the candles behind them: nothing on a slate
// moves while the flames flicker.
func TestSlatesOverCandles(t *testing.T) {
	var seats []TableSeat
	for i := range tableSeats {
		seats = append(seats, TableSeat{Name: "x", Class: Classes[i], Stake: stakes[0].pp, Throw: 100})
	}
	frames, _ := tableFrames(Table{Seats: seats, Thrown: true})
	landed := frames[len(tumble):]
	for i := range seats {
		r := slate(i, pixel.Bold.Width("100"))
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				want := landed[0].Palette[landed[0].ColorIndexAt(x, y)]
				for f, c := range landed[1:] {
					if c.Palette[c.ColorIndexAt(x, y)] != want {
						t.Fatalf("bench %d's slate changes at %d,%d in frame %d", i, x, y, f+1)
					}
				}
			}
		}
	}
}

// A name too long for its column is cut with a dot; one that fits isn't.
func TestFitName(t *testing.T) {
	if got := fitName(pixel.Bold, "steve", 58); got != "steve" {
		t.Errorf("a short name became %q", got)
	}
	got := fitName(pixel.Bold, "a very long name indeed", 58)
	if !strings.HasSuffix(got, ".") || pixel.Bold.Width(got) > 58 || !strings.HasPrefix("a very long name indeed", strings.TrimSuffix(got, ".")) {
		t.Errorf("a long name became %q, %d wide", got, pixel.Bold.Width(got))
	}
}

// The house is healthy while the hold keeps half its note and the week
// kept an edge, or nobody threw; a losing week or a drained hold is watch.
func TestHouseHealth(t *testing.T) {
	note := Rules.HouseNote * unit
	for _, c := range []struct {
		hold, week int64
		want       bool
	}{
		{note, noEdge, true}, {note, 700, true}, {note, -300, false}, {note, -1, false}, {note, 0, false},
		{note/2 - 1, 700, false}, {note / 2, 700, true},
	} {
		if got := (HouseSheet{Hold: c.hold, Note: note, Week: c.week}).Healthy(); got != c.want {
			t.Errorf("hold %d week %d: healthy %v", c.hold, c.week, got)
		}
	}
	if edgeOf(0, 0) != noEdge || edgeOf(1000, 925) != 750 || edgeWord(noEdge) != "no throws" || edgeWord(750) != "7.5%" {
		t.Error("the edge reads wrong")
	}
	for id, want := range map[string]int{"pp:treasury": 0, "pp:treasury:1": 1, "pp:treasury:9": 0, "pp:treasury:x": 0} {
		if got := parseSheet(id); got != want {
			t.Errorf("%s opens sheet %d", id, got)
		}
	}
}

// The house's sheet reads what the tables did out of the ledger, and the
// admins turn to it from the treasury.
func TestHouseSheet(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	m := testModule(db)
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	s := m.st.server(g)
	s.mu.Lock()
	s.houseCh = 9
	for i := 1; i <= 3; i++ {
		mem := m.st.member(s, g, old(i), DayOf(t0))
		mem.purse, mem.total = 100*unit, 100*unit
		// Opened in the ledger, as a real purse is: the database is shared,
		// and other tests audit every server in it.
		s.append(old(i), DayOf(t0), reasonOpen, mem.purse, nil)
	}
	tb, _ := m.st.open(s, 9, t0, false)
	for i := 1; i <= 3; i++ {
		if err := m.st.sitDown(s, tb, g, old(i), "x", stakes[1].pp, t0); err != nil {
			t.Fatal(err)
		}
	}
	m.st.throw(s, tb, t0)
	s.count(DayOf(t0), statBell, 0, 0)
	s.mu.Unlock()
	if _, err := m.st.flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	msg, err := m.sheetMessage(ctx, g, 1)
	if err != nil {
		t.Fatal(err)
	}
	var won int64
	for _, p := range tb.seats {
		won += p.won
	}
	got := js(msg.Components)
	for _, want := range []string{"the house", "75 pp staked", fmt.Sprintf("%s won", grouped(won/unit)), "pp:treasury\""} {
		if !strings.Contains(got, want) {
			t.Errorf("the house sheet hasn't %q: %s", want, got)
		}
	}
	first, err := m.sheetMessage(ctx, g, 0)
	if err != nil || !strings.Contains(js(first.Components), "the treasury holds") || !strings.Contains(js(first.Components), "pp:treasury:1") {
		t.Errorf("sheet one isn't the treasury turning to the house: %v", err)
	}
	// No two buttons on a sheet share an ID, which Discord would refuse.
	for _, sheet := range []discord.MessageCreate{msg, first} {
		ids := map[string]bool{}
		for _, part := range strings.Split(js(sheet.Components), `"custom_id":"`)[1:] {
			id := part[:strings.IndexByte(part, '"')]
			if ids[id] {
				t.Errorf("two buttons are %s", id)
			}
			ids[id] = true
		}
	}
	if e, _ := coretest.Button(t, "pp:treasury:1", func(p map[string]any) { p["guild_id"] = g.String() }); m.press(ctx, e) != nil {
		t.Error("an admin couldn't turn to the house")
	}
}

// The status screen tells the bell's coming in the house channel's words,
// only where the house is open and only from the hour before it rings
// until it stops.
func TestBellLine(t *testing.T) {
	m, _ := houseModule(t)
	at := Bells(m.st.seedOf(DayOf(t0)+1), DayOf(t0)+1)[0]
	for _, c := range []struct {
		d    time.Duration
		want string
	}{{-2 * time.Hour, ""}, {-30 * time.Minute, "drawing back"}, {-5 * time.Minute, "shingle"}, {30 * time.Minute, "ringing"}, {90 * time.Minute, ""}} {
		m.now = func() time.Time { return at.Add(c.d) }
		got := m.bellLine(guild)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("at %v the status says %q", c.d, got)
		}
	}
	// On the title screen a newcomer sees, and on the status screen once
	// they've picked a class.
	m.now = func() time.Time { return at.Add(-30 * time.Minute) }
	for _, picked := range []bool{false, true} {
		if picked {
			if err := m.pick(guild, 5, Classes[1].Key); err != nil {
				t.Fatal(err)
			}
		}
		msg, err := m.status(guild, who(5, "steve"), true)
		if err != nil || !strings.Contains(js(msg.Components), "drawing back") {
			t.Errorf("picked %v: the screen doesn't carry the bell: %v %s", picked, err, js(msg.Components))
		}
	}
	m.st.server(guild).houseCh = 0
	if got := m.bellLine(guild); got != "" {
		t.Errorf("a shut house tells the bell: %q", got)
	}
}

// The dice read every throw: the tens die and the ones die add up to it,
// 00 and 0 being a hundred.
func TestDiceFaces(t *testing.T) {
	for throw := 1; throw <= 100; throw++ {
		tens, ones := faces(throw)
		tn, _ := strconv.Atoi(tens)
		on, _ := strconv.Atoi(ones)
		sum := tn + on
		if sum == 0 {
			sum = 100
		}
		if sum != throw || len(tens) != 2 || len(ones) != 1 || tens[1] != '0' {
			t.Errorf("%d lands as %s and %s", throw, tens, ones)
		}
	}
	if len(dieTipped) != len(dieFace[0]) || len(dieTipped[0]) != len(dieFace) {
		t.Error("the tipped die isn't the face turned a quarter")
	}
}

// A table nobody sits at is cleared after a while, its message saying so
// and its buttons gone, so the next can open; one with a bird at it is
// left to its glass.
func TestIdleTable(t *testing.T) {
	m, r := houseModule(t)
	s := m.st.server(guild)
	s.mu.Lock()
	tb, _ := m.st.open(s, 4, t0, false)
	tb.msg = 77
	s.mu.Unlock()
	m.now = func() time.Time { return t0.Add(tableIdle - time.Second) }
	m.staleTables(context.Background())
	if s.table != tb || tb.settled {
		t.Fatal("cleared before its time")
	}
	m.now = func() time.Time { return t0.Add(tableIdle) }
	m.staleTables(context.Background())
	if s.table != nil || !tb.settled {
		t.Fatal("an empty table kept the house")
	}
	if len(r.edits) != 1 || !strings.Contains(r.edits[0], "cleared the table") || strings.Contains(r.edits[0], "pp:sit:") {
		t.Errorf("the cleared table read %v", r.edits)
	}
	if e, _ := coretest.Button(t, fmt.Sprintf("pp:sit:%d:0", tb.id), nil); !errors.Is(m.press(context.Background(), e), errTableShut) {
		t.Error("sat at a cleared table")
	}
	s.mu.Lock()
	next, err := m.st.open(s, 4, t0.Add(tableIdle), false)
	if err != nil {
		t.Fatalf("the next table: %v", err)
	}
	if err := m.st.sitDown(s, next, guild, 5, "x", stakes[0].pp, t0.Add(tableIdle)); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	m.now = func() time.Time { return t0.Add(2 * tableIdle) }
	m.staleTables(context.Background())
	if s.table != next || next.settled {
		t.Error("a table with a bird at it was cleared")
	}
}
