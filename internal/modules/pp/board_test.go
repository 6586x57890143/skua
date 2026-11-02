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
	board := boardTop(rank(users, scores), me, boardRows)
	if len(board) != boardRows || board[0].user != old(1) || board[boardRows-1].user != me || board[boardRows-1].place != 13 {
		t.Errorf("the top of the levels board: %+v", board)
	}
	if got := boardTop(rank(users[:3], scores[:3]), me, boardRows); len(got) != 3 {
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

	// In a season its race leads, ten a page; levels is a press away, and
	// a page past the last, or one that's no number, is the last or first.
	m.now = func() time.Time { return t0 }
	for _, c := range []struct{ id, want, not string }{
		{"pp:board", "the season 1 board, led by birdl, page 1 of 2", ""},
		{"pp:board:season:1", "page 2 of 2", "led by"},
		{"pp:board:season:9", "page 2 of 2", ""},
		{"pp:board:levels:0", "the levels board, led by birda, page 1 of 2", "season 1 board"},
		{"pp:board:levels:x", "the levels board, led by birda", ""},
	} {
		msg, err := m.boardMessage(3, me, c.id)
		card := js(msg.Components)
		if err != nil || !strings.Contains(card, c.want) || c.not != "" && strings.Contains(card, c.not) {
			t.Errorf("%s: %s, %v", c.id, card, err)
		}
	}
	if got, _, _ := boardPage(nil, me, boardRows, 3); len(got) != 0 {
		t.Errorf("an empty board has %d rows", len(got))
	}
}
