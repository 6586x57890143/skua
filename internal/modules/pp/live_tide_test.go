package pp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

// TestLiveTide runs the slack tide's posts for real, on the test bot
// (flightless.skua) in its bench channel: a table opened, sat at and
// thrown, a bell through every stage and an all hands line. Each message
// is read back from Discord, so a component or picture it refuses fails
// here rather than in front of the server. It needs the bench bot's token
// and channel, from bench.env:
//
//	set -a; . ../bench.env; set +a
//	SKUA_LIVE=1 go test ./internal/modules/pp -run TestLiveTide -v
//
// Never point it at skua's own token.
func TestLiveTide(t *testing.T) {
	token, chs := os.Getenv("DISCORD_BOT_TOKEN"), os.Getenv("SKUA_BENCH_CHANNEL")
	if os.Getenv("SKUA_LIVE") == "" || token == "" || chs == "" {
		t.Skip("SKUA_LIVE, DISCORD_BOT_TOKEN and SKUA_BENCH_CHANNEL from bench.env")
	}
	realScreens(t)
	ch, err := snowflake.Parse(chs)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := rest.New(rest.NewClient(token))
	c, err := r.GetChannel(ch)
	if err != nil {
		t.Fatal(err)
	}
	gc, ok := c.(interface{ GuildID() snowflake.ID })
	if !ok {
		t.Fatal("the bench channel isn't in a server")
	}
	g := gc.GuildID()

	m := testModule(&fakeDB{})
	m.rest, m.Guard = r, guard.New()
	m.now = time.Now
	s := m.st.server(g)
	s.mu.Lock()
	s.houseCh = ch
	names := []string{"heavywinds", "steve", "tester", "a bird with a very long name"}
	for i := range names {
		mem := m.st.member(s, g, old(i+1), DayOf(m.now()))
		mem.purse, mem.total, mem.class, mem.classSeason = 1000*unit, 1000*unit, Classes[(i*3)%len(Classes)].Key, 1
	}
	s.mu.Unlock()

	// Read back is the proof: Discord kept the card, its picture and every
	// button.
	check := func(id snowflake.ID, want ...string) {
		t.Helper()
		got, err := r.GetMessage(ch, id)
		if err != nil {
			t.Fatalf("reading %d back: %v", id, err)
		}
		body := js(got.Components)
		// The picture: Discord swaps attachment:// for its own CDN once it
		// has the file, and names what it sniffed.
		if i := strings.Index(body, `"media":{"url":"`); i >= 0 {
			url := body[i+len(`"media":{"url":"`):]
			url = url[:strings.IndexByte(url, '"')]
			if strings.HasPrefix(url, "attachment://") {
				t.Errorf("message %d's picture never arrived: %s", id, url)
			}
			end := strings.Index(body[i:], "}")
			t.Logf("message %d's picture: %s", id, body[i:i+end+1])
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("message %d hasn't %q: %s", id, w, body)
			}
		}
		t.Logf("message %d: %d attachments, %s", id, len(got.Attachments), func() string {
			var kinds []string
			for _, a := range got.Attachments {
				kinds = append(kinds, a.Filename)
			}
			return strings.Join(kinds, ",")
		}())
	}

	for _, bell := range []bool{false, true} {
		s.mu.Lock()
		s.table = nil
		tb, err := m.st.open(s, ch, m.now(), bell)
		if err != nil {
			t.Fatal(err)
		}
		msg, err := m.tableMessage(s, tb)
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		posted, err := r.CreateMessage(ch, msg, rest.WithCtx(ctx))
		if err != nil {
			t.Fatalf("posting a table: %v", err)
		}
		tb.msg = posted.ID
		check(posted.ID, "pp:sit:", tb.slate()[:8])

		s.mu.Lock()
		for i, name := range names {
			if err := m.st.sitDown(s, tb, g, old(i+1), name, stakes[i%2].pp, m.now()); err != nil {
				t.Fatal(err)
			}
		}
		if bell {
			// A slate whose first bird throws a hundred, to call all hands.
			for n := 0; ; n++ {
				tb.seed = fmt.Appendf(nil, "live slate %d", n)
				if throwOf(tb.seed, tb.id, old(1)) == 100 {
					break
				}
			}
		}
		s.mu.Unlock()
		m.settleTable(ctx, g, tb)
		check(posted.ID, fmt.Sprintf("%x", tb.seed)[:8])
	}

	// The slack tide's own card, the sump moving on it, posted as it would
	// show (in the channel, since a bot can't post an ephemeral one).
	s.mu.Lock()
	s.table = nil
	s.mu.Unlock()
	card, err := m.tideMessage(g, old(1))
	if err != nil {
		t.Fatal(err)
	}
	card.Flags &^= discord.MessageFlagEphemeral
	posted, err := r.CreateMessage(ch, card, rest.WithCtx(ctx))
	if err != nil {
		t.Fatalf("posting the slack tide: %v", err)
	}
	check(posted.ID, "pp:rail", "pp:table", "deckhead")

	// A call-out at the rail, both birds standing, thrown.
	s.mu.Lock()
	tb, err := m.st.callOut(s, ch, m.now(), false, old(1), old(2), names[1])
	if err != nil {
		t.Fatal(err)
	}
	msg, err := m.tableMessage(s, tb)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	posted, err = r.CreateMessage(ch, msg, rest.WithCtx(ctx))
	if err != nil {
		t.Fatalf("posting a call-out: %v", err)
	}
	tb.msg = posted.ID
	check(posted.ID, "a duel at the rail", "pp:sit:")
	s.mu.Lock()
	for i := range 2 {
		if err := m.st.sitDown(s, tb, g, old(i+1), names[i], stakes[1].pp, m.now()); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Unlock()
	m.settleTable(ctx, g, tb)
	check(posted.ID, fmt.Sprintf("%x", tb.seed)[:8])

	// The bell, every stage, as the minute's work moves it.
	at := time.Now().Add(time.Hour)
	for _, d := range []time.Duration{-50 * time.Minute, -10 * time.Minute, 5 * time.Minute, 70 * time.Minute} {
		m.now = func() time.Time { return at.Add(d) }
		m.bellAt(ctx, at)
		time.Sleep(time.Second)
	}
	s.mu.Lock()
	bellMsg := s.bell.msg
	s.mu.Unlock()
	check(bellMsg, "the bell")
}
