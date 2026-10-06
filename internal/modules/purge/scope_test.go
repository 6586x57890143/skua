package purge

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// category is the one server's text channel sits in.
const category = snowflake.ID(13)

// GetChannel answers from the fake's channels and active threads.
func (f *fake) GetChannel(id snowflake.ID, _ ...rest.RequestOpt) (discord.Channel, error) {
	for _, c := range f.chans {
		if c.ID() == id {
			return c, nil
		}
	}
	for _, t := range f.active {
		if t.ID() == id {
			return t, nil
		}
	}
	return nil, refusal(404, rest.JSONErrorCode(10003))
}

// scoped is server with its text channel in the category.
func scoped(t *testing.T, now time.Time) *fake {
	f := server(t, now)
	var u discord.UnmarshalChannel
	decode(t, `{"id":"10","type":0,"guild_id":"3","parent_id":"13"}`, &u)
	f.chans[0] = u.Channel.(discord.GuildChannel)
	return f
}

func TestScopeCovers(t *testing.T) {
	up := func(ch snowflake.ID) snowflake.ID {
		return map[snowflake.ID]snowflake.ID{activeTh: textCh, textCh: category}[ch]
	}
	for _, c := range []struct {
		in   scope
		ch   snowflake.ID
		want bool
	}{
		{nil, voiceCh, true},
		{scope{textCh}, textCh, true},
		{scope{textCh}, activeTh, true},
		{scope{category}, activeTh, true},
		{scope{category}, voiceCh, false},
		{scope{activeTh}, textCh, false},
		{scope{voiceCh}, 0, false},
	} {
		if got := c.in.covers(c.ch, up); got != c.want {
			t.Errorf("%v covers %d = %v, want %v", c.in, c.ch, got, c.want)
		}
	}
	if got := (scope{textCh}).union(scope{voiceCh, textCh}); !slices.Equal(got, scope{textCh, voiceCh}) {
		t.Errorf("union: %v", got)
	}
	if got := (scope{textCh}).union(nil); got != nil {
		t.Errorf("union with everywhere: %v", got)
	}
}

func TestSweepScope(t *testing.T) {
	on := new(int32)
	a, b := scope{textCh}, scope{voiceCh}
	for _, c := range []struct {
		live, every *int32
		want        scope
		ok          bool
	}{
		{on, on, scope{textCh, voiceCh}, true},
		{on, nil, a, true},
		{nil, on, b, true},
		{nil, nil, nil, false},
	} {
		got, ok := sweepScope(c.live, c.every, a, b)
		if ok != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("live %v every %v: %v %v", c.live != nil, c.every != nil, got, ok)
		}
	}
}

// A scoped sweep deletes the member's messages in what they picked, with
// its threads, and nowhere else.
func TestScopedSweep(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name string
		in   scope
		gone []snowflake.ID // channels left with none of the member's messages
		kept []snowflake.ID
	}{
		{"everywhere", nil, []snowflake.ID{textCh, activeTh, archivedTh}, nil},
		{"channel", scope{textCh}, []snowflake.ID{textCh, activeTh}, []snowflake.ID{archivedTh}},
		{"forum", scope{forumCh}, []snowflake.ID{archivedTh}, []snowflake.ID{textCh, activeTh}},
		{"category", scope{category}, []snowflake.ID{textCh, activeTh}, []snowflake.ID{archivedTh}},
		{"elsewhere", scope{voiceCh}, nil, []snowflake.ID{textCh, activeTh, archivedTh}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := scoped(t, now)
			p := newPass(f, now, newMemIndex())
			p.sw.in = map[snowflake.ID]scope{me: c.in}
			if err := p.run(context.Background()); err != nil {
				t.Fatal(err)
			}
			left := func(ch snowflake.ID) bool {
				return slices.ContainsFunc(f.msgs[ch], func(m discord.Message) bool { return m.Author.ID == me && !f.gone[m.ID] })
			}
			for _, ch := range c.gone {
				if left(ch) {
					t.Errorf("the member's messages are still in %d", ch)
				}
			}
			for _, ch := range c.kept {
				if !left(ch) {
					t.Errorf("the member's messages in %d were deleted", ch)
				}
			}
			for ch, ms := range f.msgs {
				for _, m := range ms {
					if m.Author.ID != me && f.gone[m.ID] {
						t.Fatalf("someone else's message in %d was deleted", ch)
					}
				}
			}
		})
	}
}

// Live in a channel takes what the member posts there and in its threads,
// and leaves the rest.
func TestLiveScoped(t *testing.T) {
	m, f := liveModule(), scoped(t, time.Now())
	m.setLive(target{guildID, me}, time.Millisecond, scope{category})
	var s seq
	in, th, out := s.at(time.Now(), me), s.at(time.Now(), me), s.at(time.Now(), me)
	m.OnEvent(posted(f, guildID, me, textCh, in.ID))
	m.OnEvent(posted(f, guildID, me, activeTh, th.ID))
	m.OnEvent(posted(f, guildID, me, voiceCh, out.ID))
	eventually(t, "the channel's and thread's messages deleted", func() bool {
		bulks, singles := f.state()
		got := slices.Concat(append(bulks, singles)...)
		return slices.Contains(got, in.ID) && slices.Contains(got, th.ID)
	})
	time.Sleep(50 * time.Millisecond)
	bulks, singles := f.state()
	if slices.Contains(slices.Concat(append(bulks, singles)...), out.ID) {
		t.Fatal("live deleted a message outside the member's category")
	}
	if _, ok := m.parents.Load(activeTh); !ok {
		t.Error("the thread's parent wasn't remembered")
	}
}

// /purge now in a channel carries it through the box, and the purge keeps
// to it.
func TestNowInChannel(t *testing.T) {
	m := newModule()
	m.tick = time.Millisecond
	e, _ := coretest.Event(t, "purge", func(p map[string]any) {
		d := p["data"].(map[string]any)
		d["options"] = []any{map[string]any{"name": "now", "type": 1, "options": []any{
			map[string]any{"name": "in", "type": 7, "value": "10"},
		}}}
		d["resolved"] = map[string]any{"channels": map[string]any{"10": map[string]any{"id": "10", "type": 0, "name": "general", "permissions": "0"}}}
	})
	var box discord.ModalCreate
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		box, _ = d.(discord.ModalCreate)
		return nil
	}
	router(t, m).OnCommand(e)
	if box.CustomID != "purge-now:5:10" || !strings.Contains(box.Components[0].(discord.LabelComponent).Description, "#general") {
		t.Fatalf("the box is %q: %+v", box.CustomID, box.Components)
	}

	f := scoped(t, time.Now())
	sent, _ := coretest.Modal(t, box.CustomID, map[string]string{"confirm": "delete"}, nil)
	sent.Client().Rest = f
	router(t, m).OnModal(sent)
	final(t, f)
	for _, m := range f.msgs[archivedTh] {
		if f.gone[m.ID] {
			t.Fatal("a purge in one channel deleted in another")
		}
	}
	for _, m := range f.msgs[textCh] {
		if m.Author.ID == 5 && !f.gone[m.ID] {
			t.Fatal("a purge in a channel left the member's messages there")
		}
	}
	if got := subCmd(t, m, "now", "", "", guildID); got != "modal" {
		t.Errorf("/purge now without in: %q", got)
	}
}

// Scopes and parents survive Postgres: a scoped schedule sweeps only its
// channel, live comes back scoped after a restart, and status says where.
func TestScopeThroughPostgres(t *testing.T) {
	db := testDB(t)
	m := liveModule()
	m.useDB(db)
	g := freshGuild()
	every := func(p map[string]any) {
		p["guild_id"] = g.String()
		p["data"].(map[string]any)["options"] = []any{map[string]any{"name": "every", "type": 1, "options": []any{
			map[string]any{"name": "every", "type": 3, "value": "1d"},
			map[string]any{"name": "in", "type": 7, "value": "13"},
		}}}
	}
	if got := command(t, m, "every", every); !strings.Contains(got, "✓ every 1d in <#13>") {
		t.Fatalf("every in a category: %q", got)
	}
	if err := m.saveLive(context.Background(), target{g, them}, time.Minute, scope{forumCh}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `update purge_subs set next_run = now() where guild_id = $1`, int64(g)); err != nil {
		t.Fatal(err)
	}
	f := scoped(t, time.Now())
	f.guild = g
	m.due(f)
	eventually(t, "the scoped sweep finished", func() bool {
		_, running := m.sweeping.Load(g)
		return !running && strings.Contains(subCmd(t, m, "status", "", "", g), "ago")
	})
	for ch, msgs := range f.msgs {
		for _, msg := range msgs {
			wanted := (msg.Author.ID == me && (ch == textCh || ch == activeTh)) || (msg.Author.ID == them && ch == archivedTh)
			if wanted == !f.gone[msg.ID] && ch != voiceCh {
				t.Fatalf("message %d by %d in %d: gone %v", msg.ID, msg.Author.ID, ch, f.gone[msg.ID])
			}
		}
	}
	if got := subCmd(t, m, "status", "", "", g); !strings.Contains(got, "-# every only in <#13>") {
		t.Errorf("status lacks the scope:\n%s", got)
	}
	var parent int64
	if err := db.QueryRow(context.Background(), `select parent_id from purge_channels where guild_id = $1 and channel_id = $2`,
		int64(g), int64(activeTh)).Scan(&parent); err != nil || parent != int64(textCh) {
		t.Fatalf("the thread's parent is %d, %v", parent, err)
	}

	back := liveModule()
	back.useDB(db)
	if err := back.loadLive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v, ok := back.live.Load(target{g, them}); !ok || !slices.Equal(v.(liveSet).in, scope{forumCh}) {
		t.Fatalf("live after a restart: %+v", v)
	}
}
