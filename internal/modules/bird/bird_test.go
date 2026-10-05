package bird

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
)

// xenoCanto serves one page of results, a restricted recording first, and
// the audio of the other. It counts what it was asked for.
func xenoCanto(t *testing.T, status int) (*httptest.Server, *[]string) {
	var asked []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RequestURI())
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		if r.URL.Path == "/audio" {
			_, _ = fmt.Fprint(w, "ID3 chirp")
			return
		}
		_, _ = fmt.Fprintf(w, `{"numPages":"7","recordings":[
			{"id":"1","en":"Restricted Owl","file":""},
			{"id":"42","en":"Eurasian Wren","rec":"Jane Doe","file":%q,"lic":"//creativecommons.org/licenses/by-nc-sa/4.0/"}]}`,
			srv.URL+"/audio")
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func module(api string) *Module {
	m := New(guard.New(), &fakePoster{}, filter.Default(), "k")
	m.api = api
	return m
}

func TestFetchPicksADownloadableRecordingAndLearnsThePages(t *testing.T) {
	srv, asked := xenoCanto(t, http.StatusOK)
	m := module(srv.URL + "/api")
	rec, audio, err := m.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "42" || string(audio) != "ID3 chirp" {
		t.Fatalf("fetched %+v %q", rec, audio)
	}
	if m.pages.Load() != 7 {
		t.Fatalf("pages = %d, want 7", m.pages.Load())
	}
	if q := (*asked)[0]; !strings.Contains(q, "page=1") || !strings.Contains(q, "key=k") || !strings.Contains(q, "grp%3Abirds") {
		t.Fatalf("first query = %s", q)
	}
}

func TestFetchFailsOnABadAnswer(t *testing.T) {
	srv, _ := xenoCanto(t, http.StatusTooManyRequests)
	if _, _, err := module(srv.URL).fetch(context.Background()); err == nil {
		t.Fatal("a 429 fetched a bird")
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"numPages":1,"recordings":[{"id":"1","file":""}]}`)
	}))
	defer empty.Close()
	if _, _, err := module(empty.URL).fetch(context.Background()); err == nil {
		t.Fatal("a page of restricted recordings fetched a bird")
	}
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(w, io.LimitReader(zeros{}, 5<<20))
	}))
	defer big.Close()
	if _, err := module(big.URL).get(context.Background(), big.URL, 4<<20); err == nil {
		t.Fatal("an oversized answer was read")
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestMarker(t *testing.T) {
	cases := map[string]string{
		"Eurasian Wren":            `-# @a\_b is a [eurasian wren](<https://xeno-canto.org/42>) for 9m`,
		"Upland Sandpiper":         `-# @a\_b is an [upland sandpiper](<https://xeno-canto.org/42>) for 9m`,
		"Ural Owl":                 `-# @a\_b is a [ural owl](<https://xeno-canto.org/42>) for 9m`,
		"Freckle-breasted Wood]pe": `-# @a\_b is a [freckle-breasted wood\]pe](<https://xeno-canto.org/42>) for 9m`,
	}
	for en, want := range cases {
		if got := marker("a_b", 9*time.Minute+20*time.Second, recording{ID: "42", En: en}); got != want {
			t.Errorf("marker =\n%s\nwant\n%s", got, want)
		}
	}
	if strings.Contains(marker("x", 10*time.Second, recording{}), "for 0m") {
		t.Fatal("the last seconds read as 0m")
	}
}

// fakeRest is the REST bird calls itself: the deferred reply's update and
// the delete of the original.
type fakeRest struct {
	rest.Rest
	updated   []discord.MessageUpdate
	deleted   []snowflake.ID
	reason    string // the delete's audit log reason
	deleteErr error
}

func (f *fakeRest) UpdateInteractionResponse(_ snowflake.ID, _ string, u discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.updated = append(f.updated, u)
	return &discord.Message{}, nil
}

func (f *fakeRest) DeleteMessage(_, id snowflake.ID, opts ...rest.RequestOpt) error {
	f.deleted = append(f.deleted, id)
	f.reason = coretest.Reason(opts...)
	return f.deleteErr
}

type fakePoster struct {
	sent []discord.WebhookMessageCreate
	err  error
}

func (p *fakePoster) Send(_ context.Context, _ rest.Rest, _, _, _ snowflake.ID, msg discord.WebhookMessageCreate) (*discord.Message, error) {
	p.sent = append(p.sent, msg)
	return nil, p.err
}

// run is /bird member:<user 7> minutes:<minutes> from an admin, through the
// router.
func run(t *testing.T, m *Module, minutes int, isBot bool, r rest.Rest) []discord.MessageCreate {
	t.Helper()
	router := core.NewRouter(0, nil, slog.New(slog.DiscardHandler))
	if err := router.Add(m); err != nil {
		t.Fatal(err)
	}
	e, sent := coretest.Event(t, "bird", func(p map[string]any) {
		p["member"].(map[string]any)["permissions"] = fmt.Sprint(int64(discord.PermissionAdministrator))
		d := p["data"].(map[string]any)
		d["options"] = []any{
			map[string]any{"name": "member", "type": 6, "value": "7"},
			map[string]any{"name": "minutes", "type": 4, "value": minutes},
		}
		d["resolved"] = map[string]any{"users": map[string]any{"7": map[string]any{"id": "7", "username": "wren_fan", "bot": isBot}}}
	})
	e.Client().Rest = r
	router.OnCommand(e)
	return *sent
}

func TestBirdRefusals(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		minutes int
		bot     bool
		want    string
	}{
		{"bot", "k", 5, true, "bots are already strange enough"},
		{"no key", "", 5, false, "SKUA_XENO_CANTO_KEY"},
		{"not a bird", "k", 0, false, "@wren_fan isn't a bird"},
	}
	for _, c := range cases {
		m := module("")
		m.key = c.key
		sent := run(t, m, c.minutes, c.bot, nil)
		if len(sent) != 1 || !strings.HasPrefix(sent[0].Content, "✗ ") || !strings.Contains(sent[0].Content, c.want) {
			t.Errorf("%s: replied %+v", c.name, sent)
		}
	}
}

func TestBirdThenBack(t *testing.T) {
	srv, _ := xenoCanto(t, http.StatusOK)
	m := module(srv.URL)
	r := &fakeRest{}
	run(t, m, 10, false, r)
	if len(r.updated) != 1 || *r.updated[0].Content != "✓ @wren_fan is a bird for 10m: eurasian wren" {
		t.Fatalf("updated %+v", r.updated)
	}
	if _, ok := m.pranks.Load(target{3, 7}); !ok {
		t.Fatal("no prank stored")
	}
	sent := run(t, m, 0, false, r)
	if len(sent) != 1 || sent[0].Content != "✓ @wren_fan is themselves again" {
		t.Fatalf("replied %+v", sent)
	}
	if _, ok := m.pranks.Load(target{3, 7}); ok {
		t.Fatal("the prank outlived minutes:0")
	}
}

func TestBirdReportsAFailedFetch(t *testing.T) {
	srv, _ := xenoCanto(t, http.StatusInternalServerError)
	r := &fakeRest{}
	m := module(srv.URL)
	run(t, m, 10, false, r)
	if _, ok := m.pranks.Load(target{3, 7}); ok {
		t.Fatal("a failed fetch stored a prank")
	}
}

// message is a guild message from author in channel 4 of guild 3.
func message(r rest.Rest, author snowflake.ID, edit func(*discord.Message)) *events.GuildMessageCreate {
	c := &bot.Client{Rest: r, ApplicationID: 2}
	msg := discord.Message{ID: 99, ChannelID: 4, Type: discord.MessageTypeDefault, Author: discord.User{ID: author, Username: "wren_fan"}}
	if edit != nil {
		edit(&msg)
	}
	return &events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{
		GenericEvent: events.NewGenericEvent(c, 0, 0), MessageID: msg.ID, Message: msg, ChannelID: 4, GuildID: 3,
	}}
}

func pranked(now time.Time) (*Module, *fakePoster) {
	p := &fakePoster{}
	m := New(guard.New(), p, filter.Default(), "k")
	m.now = func() time.Time { return now }
	m.pranks.Store(target{3, 7}, &prank{by: 8, until: now.Add(5 * time.Minute), rec: recording{ID: "42", En: "Wren"}, audio: []byte("chirp")})
	return m, p
}

func TestOnEventReplacesThenDeletes(t *testing.T) {
	m, p := pranked(time.Now())
	r := &fakeRest{}
	nick := "Discord Mod"
	e := message(r, 7, func(msg *discord.Message) { msg.Member = &discord.Member{Nick: &nick} })
	m.replace(r, 2, 3, e.Message, mustPrank(t, m))
	if len(p.sent) != 1 || len(r.deleted) != 1 || r.deleted[0] != 99 {
		t.Fatalf("sent %d, deleted %v", len(p.sent), r.deleted)
	}
	if r.reason != "bird: replaced with a recording, started by 8" {
		t.Errorf("audit log reason %q", r.reason)
	}
	s := p.sent[0]
	if s.Username != "wren_fan" || len(s.Files) != 1 || s.Files[0].Name != "xc42.mp3" || s.Content != `-# @wren\_fan is a [wren](<https://xeno-canto.org/42>) for 5m` {
		t.Fatalf("posted %+v", s)
	}
}

func TestReplaceKeepsTheMessageWhenThePostFails(t *testing.T) {
	m, p := pranked(time.Now())
	p.err = errors.New("no webhook in a thread")
	r := &fakeRest{}
	m.replace(r, 2, 3, message(r, 7, nil).Message, mustPrank(t, m))
	if len(r.deleted) != 0 {
		t.Fatal("deleted a message that was never replaced")
	}
}

func TestOnEventLeavesOthersAlone(t *testing.T) {
	now := time.Now()
	m, p := pranked(now)
	r := &fakeRest{}
	hook := snowflake.ID(1)
	m.OnEvent(message(r, 8, nil))                                                                   // someone else
	m.OnEvent(message(r, 7, func(msg *discord.Message) { msg.Author.Bot = true }))                  // a bot
	m.OnEvent(message(r, 7, func(msg *discord.Message) { msg.WebhookID = &hook }))                  // a webhook, skua's own post
	m.OnEvent(message(r, 7, func(msg *discord.Message) { msg.Type = discord.MessageTypeUserJoin })) // a system message
	m.OnEvent(&events.GuildReady{})
	if len(p.sent) != 0 {
		t.Fatalf("replaced %d messages it should have left", len(p.sent))
	}
	m.now = func() time.Time { return now.Add(time.Hour) }
	m.OnEvent(message(r, 7, nil))
	if _, ok := m.pranks.Load(target{3, 7}); ok || len(p.sent) != 0 {
		t.Fatal("an expired prank still ran")
	}
}

func TestOnEventDispatches(t *testing.T) {
	m, _ := pranked(time.Now())
	done := make(chan struct{})
	r := &fakeRest{}
	m.post = sendFunc(func() { close(done) })
	m.OnEvent(message(r, 7, nil))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a pranked member's message was not replaced")
	}
}

type sendFunc func()

func (f sendFunc) Send(context.Context, rest.Rest, snowflake.ID, snowflake.ID, snowflake.ID, discord.WebhookMessageCreate) (*discord.Message, error) {
	f()
	return nil, errors.New("stop here")
}

func TestStruggling(t *testing.T) {
	if struggling(nil) || struggling(errors.New("x")) {
		t.Fatal("not a Discord answer")
	}
	if !struggling(&rest.Error{Response: &http.Response{StatusCode: 503}}) || struggling(&rest.Error{Response: &http.Response{StatusCode: 404}}) {
		t.Fatal("misread a status")
	}
}

func mustPrank(t *testing.T, m *Module) *prank {
	t.Helper()
	v, ok := m.pranks.Load(target{3, 7})
	if !ok {
		t.Fatal("no prank")
	}
	return v.(*prank)
}
