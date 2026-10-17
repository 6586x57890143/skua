package pp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// Before the first season the board is levels alone, with where the member
// stands; once a season runs its race shows beside them. Each shows its top
// ten, the member's own line taking the last place when they're further
// down.
func TestBoard(t *testing.T) {
	m := testModule(&fakeDB{})
	s := m.st.server(3)
	me := snowflake.ID(5)
	for i := range 12 {
		mem := m.st.member(s, 3, old(i+1), DayOf(t0))
		mem.total = int64(100-i) * 1000 * unit
		mem.season, mem.seasonPP = 1, int64(i+1)*unit
		mem.name = "bird" + string(rune('a'+i))
	}
	m.st.member(s, 3, me, DayOf(t0)).total = 1 * unit

	var users []snowflake.ID
	var scores []int64
	for u, mem := range s.members {
		users, scores = append(users, u), append(scores, mem.total)
	}
	board := boardTop(rank(users, scores), me)
	if len(board) != boardRows || board[0].user != old(1) || board[boardRows-1].user != me || board[boardRows-1].place != 13 {
		t.Errorf("the top of the levels board: %+v", board)
	}
	if got := boardTop(rank(users[:3], scores[:3]), me); len(got) != 3 {
		t.Errorf("a short board is cut to %d", len(got))
	}

	pre := time.Unix(Default.SeasonStart*86400, 0).Add(-48 * time.Hour)
	m.now = func() time.Time { return pre }
	e, sent := coretest.Button(t, "pp:board", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if card := js((*sent)[0].Components); !strings.Contains(card, "led by birda") || strings.Contains(card, "season 1") || len((*sent)[0].Files) != 1 {
		t.Errorf("the preseason board: %s", card)
	}

	m.now = func() time.Time { return t0 }
	msg, err := m.boardMessage(3, me)
	if err != nil || !strings.Contains(js(msg.Components), "season 1 board") {
		t.Errorf("the season board: %s, %v", js(msg.Components), err)
	}
}
