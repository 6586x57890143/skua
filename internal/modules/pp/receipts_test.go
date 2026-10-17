package pp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// A member's ledger button shows their own rows, newest first, the day
// still open, what went to the treasury and the head they're chained
// under; the help page shows the treasury and savings.
func TestReceipts(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	m := testModule(db)
	g := snowflake.New(time.Now()) + snowflake.ID(time.Now().UnixNano()%4096)
	s := m.st.server(g)
	a := old(1)

	empty, err := m.receipts(ctx, g, a)
	if err != nil || !strings.Contains(js(empty.Components), "nothing yet") {
		t.Fatalf("a member with no rows saw %s, %v", js(empty.Components), err)
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
	s.append(a, DayOf(t0)+1, reasonClass, -2000, nil) // chained, not yet written
	s.mu.Unlock()

	msg, err := m.receipts(ctx, g, a)
	if err != nil {
		t.Fatal(err)
	}
	card := js(msg.Components)
	for _, want := range []string{"today", "open", "class", "-20", "a day", dayDate(DayOf(t0)), "to the treasury", s.headHex()[:16]} {
		if !strings.Contains(card, want) {
			t.Errorf("the ledger card is missing %q: %s", want, card)
		}
	}
	if strings.Index(card, "class") > strings.Index(card, "a day") {
		t.Error("the ledger isn't newest first")
	}

	r := js(m.Report(g).Rows)
	if !strings.Contains(r, "taking its levy") || !strings.Contains(r, "median") {
		t.Errorf("the help page shows %s", r)
	}
	if got := m.savings(newServer(1), DayOf(t0)); got != "nobody active yet" {
		t.Errorf("an empty server's savings read %q", got)
	}

	// The button routes to it.
	e, sent := coretest.Button(t, "pp:ledger", nil)
	if err := m.press(ctx, e); err != nil || len(*sent) != 1 {
		t.Errorf("the ledger button: %v, %d replies", err, len(*sent))
	}
}
