package remind

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/store"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// pool is Postgres from SKUA_TEST_DATABASE_URL, migrated, as CI runs it.
func pool(t *testing.T) DB {
	t.Helper()
	dsn := os.Getenv("SKUA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKUA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	p, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if err := store.Migrate(ctx, p); err != nil {
		t.Fatal(err)
	}
	return p
}

// clk is a clock a test moves by hand.
type clk struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clk) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clk) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// poster records what was sent, its message IDs made at the clock's time
// plus trip, and refuses as told.
type poster struct {
	mu     sync.Mutex
	c      *clk
	trip   time.Duration
	sent   []sent
	dmErr  error
	err    error
	dms    int
	dmOpen error
}

type sent struct {
	ch  snowflake.ID
	msg discord.MessageCreate
}

const dmChannel snowflake.ID = 777

func (p *poster) CreateMessage(ch snowflake.ID, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ch == dmChannel && p.dmErr != nil {
		return nil, p.dmErr
	}
	if p.err != nil {
		return nil, p.err
	}
	p.sent = append(p.sent, sent{ch, m})
	return &discord.Message{ID: snowflake.New(p.c.now().Add(p.trip)), ChannelID: ch}, nil
}

func (p *poster) CreateDMChannel(snowflake.ID, ...rest.RequestOpt) (*discord.DMChannel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dms++
	if p.dmOpen != nil {
		return nil, p.dmOpen
	}
	var ch discord.DMChannel
	_ = json.Unmarshal([]byte(`{"id":"777","type":1}`), &ch)
	return &ch, nil
}

func (p *poster) all() []sent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sent(nil), p.sent...)
}

// fresh is a module on the test database at a fixed time, with ids no
// other test shares.
func fresh(t *testing.T) (*Module, *clk, *poster, snowflake.ID, snowflake.ID) {
	t.Helper()
	db := pool(t)
	c := &clk{t: now}
	p := &poster{c: c, trip: 80 * time.Millisecond}
	m := New(quiet(), guard.New(), db)
	m.now = c.now
	m.after = func(time.Duration, func()) *time.Timer { return time.NewTimer(time.Hour) }
	m.rest = p
	guild := snowflake.ID(1_000_000 + rand.Uint64N(1<<40))
	user := snowflake.ID(2_000_000 + rand.Uint64N(1<<40))
	return m, c, p, guild, user
}

func (m *Module) add(t *testing.T, r reminder) reminder {
	t.Helper()
	if r.text == "" {
		r.text = "ship the release"
	}
	if r.target == "" {
		r.target = "dm"
	}
	if r.tz == "" {
		r.tz = "Europe/Berlin"
	}
	if r.created.IsZero() {
		r.created = m.now().Add(-4 * 24 * time.Hour)
	}
	r.channel, r.state = 4, "pending"
	var err error
	if r.id, err = insert(context.Background(), m.db, r); err != nil {
		t.Fatal(err)
	}
	return r
}

func (m *Module) row(t *testing.T, id int64) reminder {
	t.Helper()
	r, ok, err := get(context.Background(), m.db, id)
	if err != nil || !ok {
		t.Fatalf("row %d: %v %v", id, ok, err)
	}
	return r
}

func text(m discord.MessageCreate) string { return coretest.Text(m.Content, m.Components) }

func TestFireSendsToDMsAndLearns(t *testing.T) {
	m, c, p, g, u := fresh(t)
	r := m.add(t, reminder{guild: g, owner: u, due: now})
	m.fire(context.Background(), r)
	s := p.all()
	if len(s) != 1 || s[0].ch != dmChannel {
		t.Fatalf("sent %+v", s)
	}
	body := text(s[0].msg)
	if !strings.Contains(body, "### ship the release") || !strings.Contains(body, "set 4d 0h ago") || strings.Contains(body, "<@") {
		t.Fatalf("card %q", body)
	}
	if got := m.row(t, r.id).state; got != "done" {
		t.Fatalf("state %s", got)
	}
	if m.lead() == firstLead {
		t.Fatal("lead didn't move")
	}
	if p50, _, n := m.offsets(g); n != 1 || p50 != 80*time.Millisecond {
		t.Fatalf("offsets %v %d", p50, n)
	}
	// the dm channel is kept: a second fire opens none
	r2 := m.add(t, reminder{guild: g, owner: u, due: now})
	m.dms = map[snowflake.ID]snowflake.ID{}
	m.fire(context.Background(), r2)
	if p.dms != 1 {
		t.Fatalf("opened %d dm channels", p.dms)
	}
	// an already-claimed row doesn't go twice
	m.fire(context.Background(), r2)
	if len(p.all()) != 2 {
		t.Fatalf("sent %d", len(p.all()))
	}
	_ = c
}

func TestLeadConverges(t *testing.T) {
	m, _, _, g, _ := fresh(t)
	// a send that takes 200 ms from the timer to Discord lands 200 ms
	// minus the lead late; the lead closes the gap
	for range 80 {
		m.learn(g, 200*time.Millisecond-m.lead())
	}
	if l := m.lead(); l < 195*time.Millisecond || l > 205*time.Millisecond {
		t.Fatalf("lead %v", l)
	}
	for range 200 {
		m.learn(g, 10*time.Second)
	}
	if m.lead() != maxLead {
		t.Fatalf("lead past max: %v", m.lead())
	}
	if _, _, n := m.offsets(g); n != offsKept {
		t.Fatalf("kept %d", n)
	}
}

func TestClosedDMsGoToTheChannel(t *testing.T) {
	m, _, p, g, u := fresh(t)
	p.dmErr = &rest.Error{Code: rest.JSONErrorCodeCannotSendMessagesToThisUser}
	r := m.add(t, reminder{guild: g, owner: u, due: now})
	m.fire(context.Background(), r)
	s := p.all()
	if len(s) != 1 || s[0].ch != 4 {
		t.Fatalf("sent %+v", s)
	}
	if !strings.Contains(text(s[0].msg), "for <@"+u.String()+">") || len(s[0].msg.AllowedMentions.Users) != 1 || s[0].msg.AllowedMentions.Users[0] != u {
		t.Fatalf("ping %q %+v", text(s[0].msg), s[0].msg.AllowedMentions)
	}
	if len(s[0].msg.AllowedMentions.Parse) != 0 || len(s[0].msg.AllowedMentions.Roles) != 0 {
		t.Fatal("pings more than its owner")
	}
	if got := m.Report(g); !strings.Contains(strings.Join(got.Notes, ""), "1 went to the channel") {
		t.Fatalf("report %+v", got)
	}
}

func TestChannelTarget(t *testing.T) {
	m, _, p, g, u := fresh(t)
	m.fire(context.Background(), m.add(t, reminder{guild: g, owner: u, due: now, target: "channel", targetID: 99}))
	m.fire(context.Background(), m.add(t, reminder{guild: g, owner: u, due: now, target: "channel"}))
	s := p.all()
	if len(s) != 2 || s[0].ch != 99 || s[1].ch != 4 {
		t.Fatalf("sent %+v", s)
	}
}

func TestFailedSendRetriesThenGivesUp(t *testing.T) {
	m, c, p, g, u := fresh(t)
	p.err = errors.New("discord is down")
	r := m.add(t, reminder{guild: g, owner: u, due: now, target: "channel"})
	for i := range retries {
		m.fire(context.Background(), r)
		got := m.row(t, r.id)
		if i < retries-1 {
			if got.state != "pending" || !got.due.Equal(c.now().Add(retryIn)) {
				t.Fatalf("try %d: %s %v", i, got.state, got.due)
			}
			r = got
		} else if got.state != "done" {
			t.Fatalf("gave up as %s, want done so a restart doesn't send it", got.state)
		}
	}
	// a failed dm channel open is a failed send too
	p.err = nil
	p.dmOpen = errors.New("no")
	r2 := m.add(t, reminder{guild: g, owner: u, due: now})
	m.fire(context.Background(), r2)
	if got := m.row(t, r2.id); got.state != "pending" {
		t.Fatalf("dm open failure left it %s", got.state)
	}
}

func TestRepeatMovesOnAndSkipsWhatItMissed(t *testing.T) {
	m, c, p, g, u := fresh(t)
	r := m.add(t, reminder{guild: g, owner: u, due: at(11, 2, 9, 0), r: rule{mask: everyDay, at: 540}, target: "channel"})
	c.t = at(11, 2, 9, 0)
	m.fire(context.Background(), r)
	if got := m.row(t, r.id); got.state != "pending" || !got.due.Equal(at(11, 3, 9, 0)) {
		t.Fatalf("moved to %v %s", got.due, got.state)
	}
	if !strings.Contains(text(p.all()[0].msg), "every day at 09:00") {
		t.Fatal("card doesn't say it repeats")
	}
	// down a day: the turn is skipped, not sent, and the next one stands
	r = m.row(t, r.id)
	c.t = at(11, 3, 15, 0)
	m.fire(context.Background(), r)
	if got := m.row(t, r.id); !got.due.Equal(at(11, 4, 9, 0)) || len(p.all()) != 1 {
		t.Fatalf("missed turn: %v, sent %d", got.due, len(p.all()))
	}
	// a little late still sends, and says so
	r = m.row(t, r.id)
	c.t = at(11, 4, 9, 3)
	m.fire(context.Background(), r)
	if s := p.all(); len(s) != 2 || !strings.Contains(text(s[1].msg), "late by 3m") {
		t.Fatalf("late send %+v", s)
	}
	// a failed repeat isn't retried: its next turn stands
	p.err = errors.New("down")
	r = m.row(t, r.id)
	c.t = r.due
	m.fire(context.Background(), r)
	if got := m.row(t, r.id); !got.due.Equal(at(11, 6, 9, 0)) {
		t.Fatalf("after a failed turn %v", got.due)
	}
}

func TestHeapWakesInOrderAndDropsStale(t *testing.T) {
	m, c, p, g, u := fresh(t)
	ctx := context.Background()
	later := m.add(t, reminder{guild: g, owner: u, due: now.Add(2 * time.Minute), target: "channel", text: "later"})
	sooner := m.add(t, reminder{guild: g, owner: u, due: now.Add(time.Minute), target: "channel", text: "sooner"})
	far := m.add(t, reminder{guild: g, owner: u, due: now.Add(time.Hour), target: "channel", text: "far"})
	// the shared test table holds other tests' rows, so these are held by
	// hand rather than loaded
	for _, r := range []reminder{later, sooner, far} {
		m.hold(r)
	}
	m.mu.Lock()
	top, n := m.q[0].text, m.q.Len()
	m.mu.Unlock()
	if top != "sooner" || n < 2 {
		t.Fatalf("top %s of %d", top, n)
	}
	if _, ok := m.held[far.id]; ok {
		t.Fatal("held a reminder past the horizon")
	}
	// an edit moves sooner past later; its old entry goes stale
	sooner.due = now.Add(3 * time.Minute)
	if _, err := reschedule(ctx, m.db, sooner); err != nil {
		t.Fatal(err)
	}
	m.hold(sooner)
	m.hold(sooner) // twice is once
	c.add(2*time.Minute - m.lead())
	m.wake()
	waitSent(t, p, 1)
	if s := p.all(); !strings.Contains(text(s[0].msg), "later") {
		t.Fatalf("woke %q", text(s[0].msg))
	}
	c.t = now.Add(3 * time.Minute)
	m.wake()
	waitSent(t, p, 2)
	if s := p.all(); !strings.Contains(text(s[1].msg), "sooner") {
		t.Fatalf("then %q", text(s[1].msg))
	}
}

func waitSent(t *testing.T, p *poster, n int) {
	t.Helper()
	for range 200 {
		if len(p.all()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("sent %d, want %d", len(p.all()), n)
}

// The armed timer is real: a reminder a moment away goes out by itself.
func TestTimerFires(t *testing.T) {
	m, _, p, g, u := fresh(t)
	m.now, m.after = time.Now, time.AfterFunc
	m.hold(m.add(t, reminder{guild: g, owner: u, due: time.Now().Add(50 * time.Millisecond), target: "channel"}))
	waitSent(t, p, 1)
}

func TestResumeAndRun(t *testing.T) {
	m, c, p, g, u := fresh(t)
	ctx := context.Background()
	r := m.add(t, reminder{guild: g, owner: u, due: now.Add(-time.Minute), target: "channel"})
	if _, err := claim(ctx, m.db, r, time.Time{}); err != nil {
		t.Fatal(err)
	}
	// a claim this fresh belongs to a process still sending it; one this
	// old was left by a process that died
	fresh := m.add(t, reminder{guild: g, owner: u, due: now.Add(-time.Minute), target: "channel", text: "still sending"})
	if _, err := claim(ctx, m.db, fresh, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(ctx, "update remind set sent_at = now() - interval '5 minutes' where id = $1", r.id); err != nil {
		t.Fatal(err)
	}
	old := m.add(t, reminder{guild: g, owner: u, due: now.Add(-8 * 24 * time.Hour)})
	if _, err := m.db.Exec(ctx, "update remind set state = 'done' where id = $1", old.id); err != nil {
		t.Fatal(err)
	}
	m.now, m.after = time.Now, time.AfterFunc
	c.t = time.Now()
	run, stop := context.WithCancel(ctx)
	go m.run(run)
	waitSent(t, p, 1)
	stop()
	if !strings.Contains(text(p.all()[0].msg), "skua was away") {
		t.Fatalf("catch up %q", text(p.all()[0].msg))
	}
	if _, ok, _ := get(ctx, m.db, old.id); ok {
		t.Fatal("kept a done reminder past a week")
	}
	if got := m.row(t, fresh.id); got.state != "sending" {
		t.Fatalf("took back a fresh claim: %s", got.state)
	}
}

// opts is a slash command's options as Discord sends them.
func opts(kv ...string) func(map[string]any) {
	return func(p map[string]any) {
		var o []any
		for i := 0; i < len(kv); i += 2 {
			o = append(o, map[string]any{"name": kv[i], "type": 3, "value": kv[i+1]})
		}
		p["data"].(map[string]any)["options"] = o
	}
}

func as(u snowflake.ID, guild snowflake.ID, edit func(map[string]any)) func(map[string]any) {
	return func(p map[string]any) {
		p["member"].(map[string]any)["user"].(map[string]any)["id"] = u.String()
		p["guild_id"] = guild.String()
		if edit != nil {
			edit(p)
		}
	}
}

func command(t *testing.T, m *Module, u, g snowflake.ID, kv ...string) (string, error) {
	t.Helper()
	e, sent := coretest.Event(t, "remind", as(u, g, opts(kv...)))
	err := m.remind(context.Background(), e)
	if err != nil || len(*sent) == 0 {
		return "", err
	}
	return text((*sent)[0]), nil
}

func TestRemindCommand(t *testing.T) {
	m, _, _, g, u := fresh(t)
	out, err := command(t, m, u, g, "when", "fri 5pm", "what", "  ship\nthe release ", "tz", "europe/berlin")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"**remind** · set", "### ship the release", "<t:" + itoa(at(11, 6, 17, 0).Unix()) + ":f>", "repeat  never", "to      your dms", "kept in europe/berlin"} {
		if !strings.Contains(out, want) {
			t.Errorf("reply lacks %q:\n%s", want, out)
		}
	}
	if m.zoneOf(context.Background(), u) != "Europe/Berlin" {
		t.Fatal("zone not kept")
	}
	// the zone is kept: the next one reads in it without saying
	out, err = command(t, m, u, g, "when", "every weekday at 9", "what", "standup", "repeat", "weekdays")
	if err != nil || !strings.Contains(out, "weekdays at 09:00") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = command(t, m, u, g, "when", "t:"+itoa(now.Add(time.Hour).Unix()), "what", "x", "repeat", "month")
	if err != nil || !strings.Contains(out, "the 2nd of each month at 11:00") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = command(t, m, u, g, "when", "in 1h", "what", "x", "repeat", "day")
	if err != nil || !strings.Contains(out, "every day at 11:00") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = command(t, m, u, g, "when", "in 1h", "what", "x", "repeat", "week")
	if err != nil || !strings.Contains(out, "every mon at 11:00") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = command(t, m, u, g, "when", "every day 9am", "what", "x", "repeat", "never")
	if err != nil || !strings.Contains(out, "repeat  never") {
		t.Fatalf("%v\n%s", err, out)
	}
	// no zone yet says so
	other := u + 1
	out, err = command(t, m, other, g, "when", "in 5m", "what", "x")
	if err != nil || !strings.Contains(out, "times read as utc") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestRemindRefuses(t *testing.T) {
	m, _, _, g, u := fresh(t)
	for _, c := range []struct {
		kv   []string
		tell string
	}{
		{[]string{"when", "banana", "what", "x"}, "can't read banana"},
		{[]string{"when", "t:1", "what", "x"}, "between now and a year"},
		{[]string{"when", "in 5m", "what", "  "}, "say what"},
		{[]string{"when", "in 5m", "what", "x", "tz", "mars/olympus"}, "doesn't know the timezone"},
		{[]string{"when", "in 5m", "what", "x", "to", "here"}, "can't post here"},
	} {
		_, err := command(t, m, u, g, c.kv...)
		if tell, ok := errors.AsType[core.Tell](err); !ok || !strings.Contains(string(tell), c.tell) {
			t.Errorf("%v: %v, want %q", c.kv, err, c.tell)
		}
	}
	// a channel the member can't post in
	e, _ := coretest.Event(t, "remind", as(u, g, func(p map[string]any) {
		opts("when", "in 5m", "what", "x")(p)
		d := p["data"].(map[string]any)
		d["options"] = append(d["options"].([]any), map[string]any{"name": "channel", "type": 7, "value": "50"})
		d["resolved"] = map[string]any{"channels": map[string]any{"50": map[string]any{"id": "50", "name": "news", "type": 0, "permissions": "1024"}}}
	}))
	if err := m.remind(context.Background(), e); err == nil || !strings.Contains(err.Error(), "can't post in <#50>") {
		t.Fatalf("channel: %v", err)
	}
	// with permission it goes there, and "here" too
	e, sent := coretest.Event(t, "remind", as(u, g, func(p map[string]any) {
		opts("when", "in 5m", "what", "x")(p)
		d := p["data"].(map[string]any)
		d["options"] = append(d["options"].([]any), map[string]any{"name": "channel", "type": 7, "value": "50"})
		d["resolved"] = map[string]any{"channels": map[string]any{"50": map[string]any{"id": "50", "name": "news", "type": 0, "permissions": "3072"}}}
	}))
	if err := m.remind(context.Background(), e); err != nil || !strings.Contains(text((*sent)[0]), "<#50>") {
		t.Fatalf("channel ok: %v", err)
	}
	out, err := command(t, m, u, g, "when", "in 5m", "what", "x", "to", "here")
	_ = out
	if err == nil {
		t.Fatal("here without permission")
	}
	e, sent = coretest.Event(t, "remind", as(u, g, func(p map[string]any) {
		opts("when", "in 5m", "what", "x", "to", "here")(p)
		p["member"].(map[string]any)["permissions"] = "2048"
	}))
	if err := m.remind(context.Background(), e); err != nil || !strings.Contains(text((*sent)[0]), "<#4>") {
		t.Fatalf("here ok: %v", err)
	}
	// the cap
	for range perMember {
		m.add(t, reminder{guild: g, owner: u, due: now.Add(time.Hour)})
	}
	if _, err := command(t, m, u, g, "when", "in 5m", "what", "x"); err == nil || !strings.Contains(err.Error(), "most skua keeps") {
		t.Fatalf("cap: %v", err)
	}
	// dms only in a server
	e, _ = coretest.Event(t, "remind", func(p map[string]any) { delete(p, "guild_id") })
	if err := m.remind(context.Background(), e); err == nil {
		t.Fatal("ran outside a server")
	}
}

// press presses customID on a card made at made.
func press(t *testing.T, m *Module, u snowflake.ID, customID string, made time.Time, edit func(map[string]any)) (*events.ComponentInteractionCreate, *discord.MessageUpdate, error) {
	t.Helper()
	e, _ := coretest.Button(t, customID, func(p map[string]any) {
		p["member"].(map[string]any)["user"].(map[string]any)["id"] = u.String()
		p["message"].(map[string]any)["id"] = snowflake.New(made).String()
		if edit != nil {
			edit(p)
		}
	})
	var got *discord.MessageUpdate
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		if u, ok := d.(discord.MessageUpdate); ok {
			got = &u
		}
		return nil
	}
	return e, got, nil
}

func click(t *testing.T, m *Module, u snowflake.ID, customID string, made time.Time, edit func(map[string]any)) (string, error) {
	t.Helper()
	e, _, _ := press(t, m, u, customID, made, edit)
	var got discord.MessageUpdate
	var modal bool
	e.Respond = func(typ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		if typ == discord.InteractionResponseTypeModal {
			modal = true
			return nil
		}
		got = d.(discord.MessageUpdate)
		return nil
	}
	if err := m.click(context.Background(), e); err != nil {
		return "", err
	}
	if modal {
		return "modal", nil
	}
	return coretest.Text("", *got.Components), nil
}

func TestCardButtons(t *testing.T) {
	m, c, _, g, u := fresh(t)
	r := m.add(t, reminder{guild: g, owner: u, due: now})
	key := itoa(r.id) + ":" + itoa(now.UnixMilli())
	made := now.Add(3 * time.Millisecond)

	out, err := click(t, m, u, "remind:done:"+key, made, nil)
	if err != nil || !strings.Contains(out, "✓ done · landed 3.0 ms late") {
		t.Fatalf("done: %v %q", err, out)
	}
	out, err = click(t, m, u, "remind:done:"+key, now.Add(-2*time.Millisecond), nil)
	if err != nil || !strings.Contains(out, "landed 2.0 ms early") {
		t.Fatalf("early: %v %q", err, out)
	}
	out, err = click(t, m, u, "remind:done:"+key, now.Add(time.Minute), nil)
	if err != nil || strings.Contains(out, "landed") {
		t.Fatalf("late card says landed: %q", out)
	}
	if _, err := click(t, m, u+1, "remind:done:"+key, made, nil); err == nil || !strings.Contains(err.Error(), "someone else's") {
		t.Fatalf("someone else: %v", err)
	}

	out, err = click(t, m, u, "remind:snz:"+key+":10", made, nil)
	if err != nil || !strings.Contains(out, "snoozed") || !strings.Contains(out, "<t:"+itoa(now.Add(10*time.Minute).Unix())+":R>") {
		t.Fatalf("snooze 10: %v %q", err, out)
	}
	if got := m.row(t, r.id); !got.due.Equal(now.Add(10*time.Minute)) || got.state != "pending" {
		t.Fatalf("snoozed row %v %s", got.due, got.state)
	}
	if _, err = click(t, m, u, "remind:snz:"+key+":60", made, nil); err != nil || !m.row(t, r.id).due.Equal(now.Add(time.Hour)) {
		t.Fatalf("snooze 60: %v", err)
	}
	if _, err = click(t, m, u, "remind:snz:"+key+":tmr", made, nil); err != nil || !m.row(t, r.id).due.Equal(at(11, 3, 10, 0)) {
		t.Fatalf("snooze tomorrow: %v %v", err, m.row(t, r.id).due)
	}
	if _, err = click(t, m, u, "remind:snz:"+key+":99", made, nil); err == nil {
		t.Fatal("snooze by nonsense")
	}
	if _, err = click(t, m, u, "remind:snz:"+key, made, nil); err == nil {
		t.Fatal("snooze by nothing")
	}

	// a repeat's snooze is a one-off copy; the repeat keeps its turn
	rep := m.add(t, reminder{guild: g, owner: u, due: now, r: rule{mask: everyDay, at: 600}})
	out, err = click(t, m, u, "remind:snz:"+itoa(rep.id)+":"+itoa(now.UnixMilli())+":10", made, nil)
	if err != nil || !m.row(t, rep.id).due.Equal(now) {
		t.Fatalf("repeat snooze moved it: %v", err)
	}
	var copyID int64
	if err := m.db.QueryRow(context.Background(), "select id from remind where owner_id = $1 and mask = 0 and due_at = $2", int64(u), now.Add(10*time.Minute)).Scan(&copyID); err != nil {
		t.Fatalf("no copy: %v", err)
	}
	if !strings.Contains(out, "remind:x:"+itoa(copyID)) && !strings.Contains(out, "snoozed") {
		t.Fatalf("snoozed card %q", out)
	}

	// cancel
	out, err = click(t, m, u, "remind:x:"+itoa(copyID), made, nil)
	if err != nil || !strings.Contains(out, "cancelled") {
		t.Fatalf("cancel: %v %q", err, out)
	}
	if _, err = click(t, m, u, "remind:x:"+itoa(copyID), made, nil); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("cancel twice: %v", err)
	}
	for _, bad := range []string{"remind", "remind:done:x", "remind:what:" + itoa(r.id)} {
		if _, err := click(t, m, u, bad, made, nil); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
	_ = c
}

func TestSetCardControls(t *testing.T) {
	m, _, _, g, u := fresh(t)
	r := m.add(t, reminder{guild: g, owner: u, due: at(11, 6, 17, 0)})
	sid := itoa(r.id)
	if out, err := click(t, m, u, "remind:edit:"+sid, now, nil); err != nil || out != "modal" {
		t.Fatalf("edit: %v %q", err, out)
	}
	pick := func(v string) func(map[string]any) {
		return func(p map[string]any) {
			p["data"].(map[string]any)["component_type"] = 3
			p["data"].(map[string]any)["values"] = []string{v}
		}
	}
	out, err := click(t, m, u, "remind:rep:"+sid, now, pick("weekdays"))
	if err != nil || !strings.Contains(out, "✓ repeat weekdays at 17:00") {
		t.Fatalf("rep: %v %q", err, out)
	}
	if got := m.row(t, r.id); got.r != (rule{mask: weekdays, at: 17 * 60}) {
		t.Fatalf("rule %+v", got.r)
	}
	out, err = click(t, m, u, "remind:rep:"+sid, now, pick("never"))
	if err != nil || !strings.Contains(out, "repeat never") || !m.row(t, r.id).due.Equal(at(11, 6, 17, 0)) {
		t.Fatalf("rep never: %v %q", err, out)
	}
	if _, err := click(t, m, u, "remind:rep:"+sid, now, func(p map[string]any) {
		p["data"].(map[string]any)["component_type"] = 3
		p["data"].(map[string]any)["values"] = []string{}
	}); err == nil {
		t.Fatal("rep with nothing picked")
	}
	if _, err := m.db.Exec(context.Background(), "update remind set state = 'done' where id = $1", r.id); err != nil {
		t.Fatal(err)
	}
	if _, err := click(t, m, u, "remind:edit:"+sid, now, nil); err == nil || !strings.Contains(err.Error(), "already come") {
		t.Fatalf("edit a done one: %v", err)
	}
}

func TestEditForm(t *testing.T) {
	m, _, _, g, u := fresh(t)
	r := m.add(t, reminder{guild: g, owner: u, due: at(11, 6, 17, 0)})
	f := form(r)
	if f.CustomID != "remind-edit:"+itoa(r.id) {
		t.Fatalf("form id %s", f.CustomID)
	}
	submit := func(who snowflake.ID, id, what, when string) (string, error) {
		e, _ := coretest.Modal(t, id, map[string]string{"what": what, "when": when}, func(p map[string]any) {
			p["member"].(map[string]any)["user"].(map[string]any)["id"] = who.String()
		})
		var got discord.MessageUpdate
		e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			got, _ = d.(discord.MessageUpdate)
			return nil
		}
		if err := m.submit(context.Background(), e); err != nil {
			return "", err
		}
		return coretest.Text("", *got.Components), nil
	}
	out, err := submit(u, f.CustomID, "call the bank", "every weekday at 9")
	if err != nil || !strings.Contains(out, "### call the bank") || !strings.Contains(out, "✓ changed") {
		t.Fatalf("submit: %v %q", err, out)
	}
	if got := m.row(t, r.id); got.r != (rule{mask: weekdays, at: 540}) || got.text != "call the bank" {
		t.Fatalf("row %+v", got)
	}
	for _, c := range [][3]string{{f.CustomID, "x", "banana"}, {f.CustomID, "x", "t:1"}, {f.CustomID, " ", "in 5m"}, {"remind-edit:x", "x", "in 5m"}, {"remind-edit:1", "x", "in 5m"}} {
		if _, err := submit(u, c[0], c[1], c[2]); err == nil {
			t.Errorf("%v: no error", c)
		}
	}
	if _, err := submit(u+1, f.CustomID, "x", "in 5m"); err == nil {
		t.Fatal("someone else edited it")
	}
}

// autocomplete is the when or tz list for typed, focused on name.
func autocomplete(t *testing.T, m *Module, u snowflake.ID, focus, typed, tz string) []discord.AutocompleteChoice {
	t.Helper()
	o := []any{map[string]any{"name": focus, "type": 3, "value": typed, "focused": true}}
	if tz != "" {
		o = append(o, map[string]any{"name": "tz", "type": 3, "value": tz})
	}
	raw, _ := json.Marshal(map[string]any{
		"id": snowflake.New(time.Now()).String(), "application_id": "2", "type": 4, "token": "t", "version": 1, "guild_id": "3",
		"channel": map[string]any{"id": "4", "type": 0},
		"member":  map[string]any{"user": map[string]any{"id": u.String(), "username": "m"}, "permissions": "0", "roles": []any{}, "joined_at": "2026-01-01T00:00:00Z"},
		"data":    map[string]any{"id": "6", "name": "remind", "type": 1, "options": o},
	})
	var i discord.AutocompleteInteraction
	if err := json.Unmarshal(raw, &i); err != nil {
		t.Fatal(err)
	}
	var got []discord.AutocompleteChoice
	e := &events.AutocompleteInteractionCreate{
		GenericEvent: events.NewGenericEvent(&bot.Client{}, 0, 0), AutocompleteInteraction: i,
		Respond: func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			got = d.(discord.AutocompleteResult).Choices
			return nil
		},
	}
	m.OnEvent(e)
	return got
}

func TestAutocomplete(t *testing.T) {
	m, _, _, _, u := fresh(t)
	got := autocomplete(t, m, u, "when", "fri 5pm", "Europe/Berlin")
	if len(got) != 1 || got[0].(discord.AutocompleteChoiceString).Name != "fri 6 nov 17:00 cet · in 4d 7h" || got[0].(discord.AutocompleteChoiceString).Value != "t:"+itoa(at(11, 6, 17, 0).Unix()) {
		t.Fatalf("when %+v", got)
	}
	if got := autocomplete(t, m, u, "when", "", ""); len(got) < 4 || !strings.Contains(got[0].(discord.AutocompleteChoiceString).Name, " · set tz") {
		t.Fatalf("empty %+v", got)
	}
	if got := autocomplete(t, m, u, "when", "banana", ""); len(got) != 1 || !strings.Contains(got[0].(discord.AutocompleteChoiceString).Name, "can't read that") {
		t.Fatalf("banana %+v", got)
	}
	if got := autocomplete(t, m, u, "tz", "new york", ""); len(got) != 1 || got[0].(discord.AutocompleteChoiceString).Value != "America/New_York" || !strings.HasSuffix(got[0].(discord.AutocompleteChoiceString).Name, " now") {
		t.Fatalf("tz %+v", got)
	}
	if got := autocomplete(t, m, u, "tz", "", ""); len(got) != len(common) {
		t.Fatalf("tz empty %d", len(got))
	}
	if got := autocomplete(t, m, u, "tz", "a", ""); len(got) != 25 {
		t.Fatalf("tz a %d", len(got))
	}
}

func TestZones(t *testing.T) {
	if len(zoneNames) < 400 {
		t.Fatalf("only %d zones", len(zoneNames))
	}
	for _, z := range zoneNames {
		if _, err := time.LoadLocation(z); err != nil {
			t.Errorf("%s: %v", z, err)
		}
	}
	if zone("mars/olympus") != time.UTC || zone("") != time.UTC || zone("Local") != time.UTC {
		t.Fatal("a bad zone isn't utc")
	}
	if known("asia/TOKYO") != "Asia/Tokyo" || known("Local") != "" {
		t.Fatal("known")
	}
	if clip("abcdef", 3) != "abc" || clip("ab", 3) != "ab" {
		t.Fatal("clip")
	}
	if oneLine("## hi\nthere") != "hi there" {
		t.Fatal(oneLine("## hi\nthere"))
	}
}

func TestModuleFacts(t *testing.T) {
	m, _, _, g, _ := fresh(t)
	if m.Name() != "remind" || m.Perms() == 0 || m.Want().Required == 0 || m.Help().About == "" {
		t.Fatal("facts")
	}
	if len(m.Commands()) != 1 || len(m.Components()) != 1 || len(m.Modals()) != 1 {
		t.Fatal("handlers")
	}
	if r := m.Report(g); len(r.Rows) != 0 {
		t.Fatalf("empty report %+v", r)
	}
	m.pending[g] = 2
	if r := m.Report(g); r.Rows[1][1] != "not measured yet" {
		t.Fatalf("report %+v", r)
	}
	m.learn(g, 12*time.Millisecond)
	if r := m.Report(g); r.Rows[1][1] != "p50 12 ms · p99 12 ms" {
		t.Fatalf("report %+v", r)
	}
	if ms(1500*time.Microsecond) != "1.5 ms" {
		t.Fatal(ms(1500 * time.Microsecond))
	}
	if !closed(&rest.Error{Code: rest.JSONErrorCodeCannotSendMessagesToThisUserDueToHavingNoMutualGuilds}) || closed(errors.New("x")) {
		t.Fatal("closed")
	}
}

func TestLeavingAServerForgetsIt(t *testing.T) {
	m, _, _, g, u := fresh(t)
	r := m.add(t, reminder{guild: g, owner: u, due: now.Add(time.Hour)})
	m.OnEvent(&events.GuildLeave{GenericGuild: &events.GenericGuild{GuildID: g}})
	for range 200 {
		if _, ok, _ := get(context.Background(), m.db, r.id); !ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("still kept")
}

func TestNotConnected(t *testing.T) {
	m, _, _, g, u := fresh(t)
	m.rest = nil
	m.dms = map[snowflake.ID]snowflake.ID{}
	if _, err := m.send(context.Background(), reminder{guild: g, owner: u, target: "dm"}, 0); err == nil {
		t.Fatal("sent without a connection")
	}
	if _, err := m.warm(u); err == nil {
		t.Fatal("opened a dm without a connection")
	}
}

func TestRefusedSendIsNotRetried(t *testing.T) {
	m, _, p, g, u := fresh(t)
	p.err = &rest.Error{Code: rest.JSONErrorCodeUnknownChannel, Response: &http.Response{StatusCode: 404}}
	r := m.add(t, reminder{guild: g, owner: u, due: now, target: "channel"})
	m.fire(context.Background(), r)
	if got := m.row(t, r.id); got.state != "done" {
		t.Fatalf("a 404 left it %s", got.state)
	}
	if refused(&rest.Error{Response: &http.Response{StatusCode: 429}}) || refused(&rest.Error{Response: &http.Response{StatusCode: 502}}) || refused(errors.New("x")) || refused(&rest.Error{}) {
		t.Fatal("refused too much")
	}
}

func TestNewZoneMovesRepeats(t *testing.T) {
	m, _, _, g, u := fresh(t)
	ctx := context.Background()
	rep := m.add(t, reminder{guild: g, owner: u, due: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC), r: rule{mask: weekdays, at: 540}, tz: "UTC"})
	one := m.add(t, reminder{guild: g, owner: u, due: now.Add(time.Hour), tz: "UTC"})
	if err := m.keepZone(ctx, u, "Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	if got := m.row(t, rep.id); got.tz != "Europe/Berlin" || !got.due.Equal(at(11, 3, 9, 0)) {
		t.Fatalf("repeat %s %v", got.tz, got.due)
	}
	if got := m.row(t, one.id); got.tz != "UTC" || !got.due.Equal(now.Add(time.Hour)) {
		t.Fatalf("one-off moved: %s %v", got.tz, got.due)
	}
}

func TestEditKeepsSeconds(t *testing.T) {
	m, _, _, g, u := fresh(t)
	due := now.Add(17*time.Second + 20*time.Minute)
	r := m.add(t, reminder{guild: g, owner: u, due: due})
	e, _ := coretest.Modal(t, form(r).CustomID, map[string]string{"what": "fixed typo", "when": typed(r)}, func(p map[string]any) {
		p["member"].(map[string]any)["user"].(map[string]any)["id"] = u.String()
	})
	e.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
		return nil
	}
	if err := m.submit(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := m.row(t, r.id); !got.due.Equal(due) || got.text != "fixed typo" {
		t.Fatalf("edit moved it to %v", got.due)
	}
	e, _ = coretest.Modal(t, form(r).CustomID, map[string]string{"what": " ", "when": typed(r)}, func(p map[string]any) {
		p["member"].(map[string]any)["user"].(map[string]any)["id"] = u.String()
	})
	if err := m.submit(context.Background(), e); err == nil {
		t.Fatal("emptied the text")
	}
}
