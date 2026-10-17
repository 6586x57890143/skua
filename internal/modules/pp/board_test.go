package pp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// Before the first season the board is levels alone; once a season runs
// its race shows beside them. Each shows its top ten and, below, where the
// member stands if they aren't among them.
func TestBoard(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(3)
	me := snowflake.ID(5)
	for i := range 12 {
		mem := m.st.member(s, 3, old(i+1), DayOf(t0))
		mem.total = int64(100-i) * 1000 * unit
		mem.season, mem.seasonPP = 1, int64(i+1)*unit
	}
	m.st.member(s, 3, me, DayOf(t0)).total = 1 * unit

	pre := time.Unix(Default.SeasonStart*86400, 0).Add(-48 * time.Hour)
	m.now = func() time.Time { return pre }
	e, sent := coretest.Button(t, "pp:board", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	text := coretest.Text("", (*sent)[0].Components)
	if !strings.Contains(text, "### levels") || strings.Contains(text, "### season") || !strings.Contains(text, "first season starts") {
		t.Errorf("the pre-season board: %s", text)
	}
	if !strings.Contains(text, "1 · <@"+old(1).String()+">") || !strings.Contains(text, "further down\n13 · <@5> · level 0 ← you") || strings.Contains(text, "11 · ") {
		t.Errorf("the levels board's rows: %s", text)
	}

	m.now = func() time.Time { return t0 }
	text = coretest.Text("", m.boardMessage(3, me).Components)
	if !strings.Contains(text, "### season 1") || !strings.Contains(text, "1 · <@"+old(12).String()+"> · 12 pp") {
		t.Errorf("the season board: %s", text)
	}
	if got := boardLines(nil, me, nil); !strings.Contains(got, "nobody yet") {
		t.Errorf("an empty board reads %q", got)
	}
}
