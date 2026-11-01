package pp

import (
	"context"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// lateRest records the answers edited in after a press was deferred.
type lateRest struct {
	rest.Rest
	edits, notes []string
}

func (f *lateRest) UpdateInteractionResponse(_ snowflake.ID, _ string, u discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	text := js(u.Components)
	if u.Content != nil {
		text += *u.Content
	}
	f.edits = append(f.edits, text)
	return &discord.Message{}, nil
}

func (f *lateRest) CreateFollowupMessage(_ snowflake.ID, _ string, c discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.notes = append(f.notes, js(c.Components)+c.Content)
	return &discord.Message{}, nil
}

// A screen that moves takes longer to draw on skua's host than a press
// waits, so the press is answered at once and the screen edited in after:
// the slack tide, the stair, the yard, the morning sheet and a new table.
func TestMovingScreensAnswerFirst(t *testing.T) {
	m, tr := houseModule(t)
	s := m.st.server(guild)
	s.mu.Lock()
	s.yardOpen = true
	s.mu.Unlock()
	if err := m.pick(guild, 5, "thief"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"pp:tide": "the slack tide", "pp:shop": "the ladder", "pp:yard": "the factions"} {
		late := &lateRest{}
		e, sent := coretest.Button(t, id, nil)
		e.Client().Rest = late
		if err := m.press(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		if !deferred(*sent) {
			t.Errorf("%s answered with a whole screen, not a deferral", id)
		}
		if len(late.edits) != 1 || !strings.Contains(late.edits[0], want) || !strings.Contains(late.edits[0], "pp:status") {
			t.Errorf("%s edited in %v", id, late.edits)
		}
	}

	// The morning sheet, on the first /pp of the day.
	late := &lateRest{}
	e, sent := coretest.Event(t, "pp", nil)
	e.Client().Rest = late
	if err := m.Commands()[0].Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if !deferred(*sent) || len(late.edits) != 1 || !strings.Contains(late.edits[0], "the morning sheet") {
		t.Errorf("the morning sheet: %d sent, edited %v", len(*sent), late.edits)
	}

	// A new table: posted after the press is answered, and the press told
	// where it is.
	late = &lateRest{}
	e2, sent2 := coretest.Button(t, "pp:table", nil)
	e2.Client().Rest = late
	if err := m.press(context.Background(), e2); err != nil {
		t.Fatal(err)
	}
	if !deferred(*sent2) || len(tr.posts) != 1 || len(late.edits) != 1 || !strings.Contains(late.edits[0], "set a table") {
		t.Errorf("a table: %d sent, %d posted, edited %v", len(*sent2), len(tr.posts), late.edits)
	}

	// A refusal still comes back at once, nothing deferred.
	late = &lateRest{}
	e3, _ := coretest.Button(t, "pp:table", nil)
	e3.Client().Rest = late
	if err := m.press(context.Background(), e3); err == nil || len(late.edits) != 0 {
		t.Errorf("a second table: %v, edited %v", err, late.edits)
	}
}

// deferred is whether a press was answered only by a deferral, which
// coretest records as one empty reply.
func deferred(sent []discord.MessageCreate) bool {
	return len(sent) == 1 && len(sent[0].Components) == 0 && sent[0].Content == ""
}
