package pp

import (
	"context"
	"errors"
	"fmt"
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
