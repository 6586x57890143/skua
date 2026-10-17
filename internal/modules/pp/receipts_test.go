package pp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// who is a resolved member, as an interaction carries one.
func who(user snowflake.ID, name string) *discord.ResolvedMember {
	return &discord.ResolvedMember{Member: discord.Member{User: discord.User{ID: user, Username: name}}}
}

// A member's journal is their own ledger lines as a screen, newest first,
// with the day still open and what went to the treasury today; admins get
// the treasury's card, which nobody else can open.
func TestJournalAndTreasury(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	m := testModule(db)
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	s := m.st.server(g)
	a := old(1)

	empty, err := m.journal(ctx, g, who(a, "steve"))
	if err != nil || len(empty.Files) != 1 || strings.Contains(js(empty.Components), "went to the treasury") {
		t.Fatalf("a member with no lines: %s, %v", js(empty.Components), err)
	}

	s.mu.Lock()
	for i := range 30 {
		m.st.chat(s, g, a, "a real thing to say", t0.Add(time.Duration(i)*time.Minute))
	}
	m.st.chat(s, g, a, "and the next day", t0.Add(24*time.Hour))
	s.mu.Unlock()
	if _, err := m.st.flush(ctx, db); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.append(a, DayOf(t0)+1, reasonShop, -2000, []int64{1}) // chained, not yet written
	s.mu.Unlock()

	msg, err := m.journal(ctx, g, who(a, "steve"))
	if err != nil || len(msg.Files) != 1 || !strings.Contains(js(msg.Components), "went to the treasury") {
		t.Fatalf("the journal: %s, %v", js(msg.Components), err)
	}
	for reason, want := range map[string]string{
		reasonLabour: "a day's earning", reasonClass: "a new path", reasonPrize: "a weekly prize",
		reasonGrant: "a grant", reasonOpen: "purse carried over",
	} {
		if _, what := journalLine(reason, nil); what != want {
			t.Errorf("%s reads %q", reason, what)
		}
	}
	if _, what := journalLine(reasonQuest, []int64{2}); what != "golden quest" {
		t.Errorf("a golden quest reads %q", what)
	}
	if _, what := journalLine(reasonShop, []int64{1}); what != "tallow perch" {
		t.Errorf("the first rung reads %q", what)
	}

	// The treasury card, for admins.
	tr, err := m.treasuryMessage(ctx, g)
	if err != nil || len(tr.Files) != 1 || !strings.Contains(js(tr.Components), "the treasury holds") {
		t.Fatalf("the treasury: %s, %v", js(tr.Components), err)
	}
	admin := false
	m.Admin = func(discord.Interaction) bool { return admin }
	if err := m.press(ctx, firstOf(coretest.Button(t, "pp:treasury", nil))); err == nil {
		t.Error("a member opened the treasury")
	}
	admin = true
	if err := m.press(ctx, firstOf(coretest.Button(t, "pp:treasury", nil))); err != nil {
		t.Errorf("an admin couldn't open the treasury: %v", err)
	}

	r := js(m.Report(g).Rows)
	if !strings.Contains(r, "taking its levy") || !strings.Contains(r, "median") {
		t.Errorf("the help page shows %s", r)
	}
	if got := m.savings(newServer(1), DayOf(t0)); got != "nobody active yet" {
		t.Errorf("an empty server's savings read %q", got)
	}

	// The journal button routes to it.
	e, sent := coretest.Button(t, "pp:ledger", nil)
	if err := m.press(ctx, e); err != nil || len(*sent) != 1 {
		t.Errorf("the journal button: %v, %d replies", err, len(*sent))
	}
}
