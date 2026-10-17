package pp

import (
	"context"
	"errors"
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

// postRest records what the herald posts; down makes posting fail.
type postRest struct {
	rest.Rest
	down  bool
	posts []string
	to    []snowflake.ID
}

func (f *postRest) CreateMessage(ch snowflake.ID, c discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	if f.down {
		return nil, errors.New("down")
	}
	f.to = append(f.to, ch)
	f.posts = append(f.posts, js(c.Components))
	return &discord.Message{}, nil
}

// Only admins pick where the ledger goes, on the help page, and the herald
// posts there and nowhere without one.
func TestLogChannel(t *testing.T) {
	db := &fakeDB{}
	m := testModule(db)
	r := &postRest{}
	m.rest, m.Guard = r, guard.New()
	admin := false
	m.Admin = func(discord.Interaction) bool { return admin }

	m.post(3, "ledger", "nowhere yet")
	if len(r.posts) != 0 {
		t.Fatal("the herald posted with no channel picked")
	}
	if !strings.Contains(js(m.Report(3).Controls), "pp:log") {
		t.Fatal("the help page has no ledger channel picker")
	}
	pick := func(id string) (*events.ComponentInteractionCreate, *[]discord.MessageCreate) {
		return coretest.Button(t, "pp:log", func(p map[string]any) {
			p["data"] = map[string]any{"custom_id": "pp:log", "component_type": 8, "values": []any{id},
				"resolved": map[string]any{"channels": map[string]any{id: map[string]any{"id": id, "type": 0, "name": "ledger", "permissions": "0"}}}}
		})
	}
	if err := m.press(context.Background(), firstOf(pick("88"))); !errors.Is(err, errNotAdmin) {
		t.Fatalf("a member set the ledger channel: %v", err)
	}
	admin = true
	e, sent := pick("88")
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if m.st.server(3).logChannel != 88 || !strings.Contains(db.execs[len(db.execs)-1], "log_channel") {
		t.Fatalf("the channel wasn't kept: %d", m.st.server(3).logChannel)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Content, "<#88>") {
		t.Errorf("the admin was told %v", *sent)
	}
	if !strings.Contains(js(m.Report(3).Controls), `"88"`) {
		t.Error("the picker doesn't show the channel picked")
	}

	m.post(3, "ledger", "hello")
	if len(r.to) != 1 || r.to[0] != 88 || !strings.Contains(r.posts[0], "hello") {
		t.Fatalf("posted %v to %v", r.posts, r.to)
	}
	r.down = true
	m.post(3, "ledger", "lost")
	if len(r.posts) != 1 {
		t.Error("a failed post was counted")
	}

	e, _ = pick("88")
	e.Data = discord.ChannelSelectMenuInteractionData{}
	if err := m.setLog(context.Background(), 3, e); err != nil || m.st.server(3).logChannel != 0 {
		t.Errorf("clearing the channel: %v, %d", err, m.st.server(3).logChannel)
	}
	e, _ = pick("88")
	e.Data = discord.ButtonInteractionData{}
	if err := m.setLog(context.Background(), 3, e); !errors.Is(err, errNotAdmin) {
		t.Errorf("a button set the channel: %v", err)
	}
	db.fail = errors.New("down")
	if err := m.setLog(context.Background(), 3, firstOf(pick("88"))); err == nil {
		t.Error("a channel that couldn't be saved was kept quietly")
	}
}

// A minute of the loop pays and saves; the first after midnight audits and
// posts every server's head, once its rows are written, and a boot only
// audits. The seeds move along every minute and their news is posted.
func TestTickPostsHeads(t *testing.T) {
	db := &fakeDB{}
	m := testModule(db)
	r := &postRest{}
	m.rest, m.Guard = r, guard.New()
	m.beacon = func(context.Context, int64) ([]byte, error) { return []byte{9}, nil }
	s := m.st.server(guild)
	s.logChannel = 88
	s.mu.Lock()
	m.st.chat(s, guild, old(1), "a real thing to say", t0)
	s.mu.Unlock()

	var last int64
	m.tick(context.Background(), &last)
	if last != DayOf(t0) || strings.Contains(strings.Join(r.posts, " "), "is closed") {
		t.Fatalf("a boot posted a head: %v", r.posts)
	}
	m.now = func() time.Time { return t0.Add(24 * time.Hour) }
	m.tick(context.Background(), &last)
	if s.seq == 0 || len(s.pending) != 0 {
		t.Fatalf("the day closed into %d rows, %d unwritten", s.seq, len(s.pending))
	}
	if all := strings.Join(r.posts, " "); !strings.Contains(all, "is closed") || !strings.Contains(all, s.headHex()) {
		t.Errorf("posted %v", r.posts)
	}

	// A head with rows still unwritten waits for them.
	r.posts = nil
	s.append(old(1), 1, reasonOpen, 1, nil)
	m.heads(DayOf(t0) + 2)
	if len(r.posts) != 0 {
		t.Error("a head went out ahead of its rows")
	}

	// A season committed and one revealed both reach the channel.
	r.posts = nil
	m.st.seeds[1] = &seasonSeed{season: 1, secret: []byte("old"), round: 1}
	m.seasonTick(context.Background(), Default.seasonStart(2).Add(time.Hour))
	all := strings.Join(r.posts, " ")
	if !strings.Contains(all, "season 1 is over") || !strings.Contains(all, "every roll in season 3") {
		t.Errorf("season news posted %v", r.posts)
	}
	if !strings.Contains(js(m.Report(guild).Rows), "s2 · ") {
		t.Errorf("the report doesn't show the next seed: %v", m.Report(guild).Rows)
	}
}
