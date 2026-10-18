package pp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
)

// fakeDB records what /pp writes and fails when told to.
type fakeDB struct {
	execs []string
	fail  error
}

func (f *fakeDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.execs = append(f.execs, sql)
	return pgconn.CommandTag{}, f.fail
}

func (f *fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("fakeDB: no reads")
}

func testModule(db DB) *Module {
	return &Module{
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), db: db, st: newState(&Default, 42),
		info: func(snowflake.ID) (snowflake.ID, string) { return afkCh, "melted" },
		now:  func() time.Time { return t0 },
		// The tests play as an admin; TestBetaGate plays as everyone else.
		Admin: func(discord.Interaction) bool { return true },
	}
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

// Before a first pick /pp is the title screen with the button to pick;
// the wanderer counts as a pick though it's the class they start as.
func TestTitleUntilAPick(t *testing.T) {
	m := testModule(&fakeDB{})
	e, sent := coretest.Event(t, "pp", nil)
	if err := m.Commands()[0].Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	got := js((*sent)[0])
	for _, want := range []string{"melting pot", "pick a class", "pp:class", "attachment://pp.jpg"} {
		if !strings.Contains(got, want) {
			t.Errorf("the title lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "pp:show") {
		t.Error("the title offers to show the channel")
	}
	if err := m.press(context.Background(), firstOf(pick(t, "wanderer"))); err != nil {
		t.Fatal(err)
	}
	if m.st.server(3).members[5].classSeason == -1 {
		t.Error("picking the wanderer first didn't count")
	}
}

func TestStatusCommand(t *testing.T) {
	m := testModule(&fakeDB{})
	if err := m.pick(3, 5, "thief"); err != nil {
		t.Fatal(err)
	}
	e, sent := coretest.Event(t, "pp", nil)
	if err := m.Commands()[0].Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 {
		t.Fatalf("%d replies", len(*sent))
	}
	msg := (*sent)[0]
	got := js(msg)
	for _, want := range []string{"attachment://pp.jpg", "pp:class", "pp:show", `"parse":[]`} {
		if !strings.Contains(got, want) {
			t.Errorf("the reply lacks %q: %s", want, got)
		}
	}
	if len(msg.Files) != 1 || msg.Files[0].Name != screenName {
		t.Errorf("files %v", msg.Files)
	}
	if !msg.Flags.Has(discord.MessageFlagEphemeral) || !msg.Flags.Has(discord.MessageFlagIsComponentsV2) {
		t.Errorf("flags %v", msg.Flags)
	}
}

func TestStatusOutsideAServer(t *testing.T) {
	m := testModule(&fakeDB{})
	r := core.NewRouter(0, func(snowflake.ID) (snowflake.ID, bool) { return 0, false }, m.log)
	if err := r.Add(m); err != nil {
		t.Fatal(err)
	}
	e, sent := coretest.Event(t, "pp", func(p map[string]any) {
		delete(p, "guild_id")
		p["user"] = p["member"].(map[string]any)["user"]
		delete(p, "member")
	})
	r.OnCommand(e)
	if len(*sent) != 1 || !strings.Contains((*sent)[0].Content, "works in a server") {
		t.Errorf("replies %v", *sent)
	}
}

// pick is the class select, as Discord sends it.
func pick(t *testing.T, value string) (*events.ComponentInteractionCreate, *[]discord.MessageCreate) {
	return coretest.Button(t, "pp:pick:"+value, nil)
}

// A pick turns the picker into the status screen, and the edit lists the
// screen's picture once, as disgo sends it: unlisted Discord can't find
// attachment://pp.jpg, listed twice it refuses the edit.
func TestPickListsTheScreen(t *testing.T) {
	m := testModule(&fakeDB{})
	// The chat hasn't moved past the picker, so it's edited in place.
	e, _ := coretest.Button(t, "pp:pick:thief", func(p map[string]any) {
		p["channel"] = map[string]any{"id": "4", "type": 0, "last_message_id": "9"}
	})
	var got discord.MessageUpdate
	e.Respond = func(typ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		got, _ = d.(discord.MessageUpdate)
		_, err := got.ToResponseBody(discord.InteractionResponse{Type: typ, Data: got})
		return err
	}
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].Name != screenName || got.Attachments == nil || len(*got.Attachments) != 1 {
		t.Fatalf("the edit brings %d files and lists %v", len(got.Files), got.Attachments)
	}
	if a, ok := (*got.Attachments)[0].(discord.AttachmentCreate); !ok || a.ID != 0 {
		t.Errorf("the screen is listed as %#v", (*got.Attachments)[0])
	}
}

func TestClassChanges(t *testing.T) {
	m := testModule(&fakeDB{})
	press := func(e *events.ComponentInteractionCreate, sent *[]discord.MessageCreate) (string, error) {
		err := m.press(context.Background(), e)
		if len(*sent) == 0 {
			return "", err
		}
		return js((*sent)[len(*sent)-1]), err
	}
	got, err := press(coretest.Button(t, "pp:class", nil))
	if err != nil || !strings.Contains(got, "pp:pick:wanderer") || !strings.Contains(got, "albatross wanderer") || !strings.Contains(got, `"label":"walk this path"`) {
		t.Fatalf("the picker: %v %s", err, got)
	}
	if _, err := press(pick(t, "thief")); err != nil {
		t.Fatal(err)
	}
	mem := m.st.server(3).members[5]
	if mem.class != "thief" || mem.purse != 0 {
		t.Fatalf("the free pick: class %q purse %d", mem.class, mem.purse)
	}

	// A second change this season costs, and they can't pay.
	if _, err := press(pick(t, "bard")); err == nil || !strings.Contains(err.Error(), "costs 200 pp and you have 0") {
		t.Fatalf("an unaffordable change: %v", err)
	}
	mem.purse, mem.total = 5000*unit, 2000*unit
	l, _, _ := Default.Level(mem.total)
	first := Default.ClassCost(l, 0)
	if got, _ := press(coretest.Button(t, loreID(classIndex("bard"), 0), nil)); !strings.Contains(got, "walk this path · "+grouped(first/unit)+" pp") {
		t.Errorf("the picker doesn't say the price: %s", got)
	}
	if _, err := press(pick(t, "bard")); err != nil || mem.class != "bard" || mem.purse != 5000*unit-first || mem.classChanges != 1 {
		t.Fatalf("a paid change: %v, %q, purse %d", err, mem.class, mem.purse)
	}
	if _, err := press(pick(t, "knight")); err != nil || mem.purse != 5000*unit-3*first || mem.total != 2000*unit {
		t.Errorf("the next change didn't double, or spending touched the total: purse %d total %d", mem.purse, mem.total)
	}
	before := mem.purse
	if _, err := press(pick(t, "knight")); err != nil || mem.purse != before {
		t.Error("picking the class they have cost something")
	}

	// A new season's first pick is free again.
	m.now = func() time.Time { return t0.Add(28 * 24 * time.Hour) }
	if _, err := press(pick(t, "monk")); err != nil || mem.class != "monk" || mem.purse != before || mem.classChanges != 0 {
		t.Errorf("a new season's pick: %v, %q, purse %d, changes %d", err, mem.class, mem.purse, mem.classChanges)
	}
	for _, bad := range []func() (*events.ComponentInteractionCreate, *[]discord.MessageCreate){
		func() (*events.ComponentInteractionCreate, *[]discord.MessageCreate) { return pick(t, "necromancer") },
		func() (*events.ComponentInteractionCreate, *[]discord.MessageCreate) {
			return coretest.Button(t, "pp:pick", nil)
		},
		func() (*events.ComponentInteractionCreate, *[]discord.MessageCreate) {
			return coretest.Button(t, "pp:nothing", nil)
		},
	} {
		if _, err := press(bad()); !errors.Is(err, errNotAClass) {
			t.Errorf("a bad pick: %v", err)
		}
	}
}

func TestShowPostsForTheChannel(t *testing.T) {
	m := testModule(&fakeDB{})
	e, sent := coretest.Button(t, "pp:show", nil)
	if err := m.press(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	msg := (*sent)[0]
	if msg.Flags.Has(discord.MessageFlagEphemeral) || strings.Contains(js(msg), "pp:show") {
		t.Errorf("the shown copy is ephemeral or has buttons: %s", js(msg))
	}
}

func generic() *events.GenericEvent { return events.NewGenericEvent(&bot.Client{}, 0, 0) }

func TestEvents(t *testing.T) {
	m := testModule(&fakeDB{})
	a, b := old(1), old(2)
	message := func(author discord.User) *events.GuildMessageCreate {
		return &events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{
			GenericEvent: generic(), GuildID: guild,
			Message: discord.Message{Author: author, Content: "a real thing to say"},
		}}
	}
	m.OnEvent(message(discord.User{ID: a}))
	m.OnEvent(message(discord.User{ID: old(9), Bot: true}))
	s := m.st.server(guild)
	if s.members[a] == nil || s.members[a].total == 0 || s.members[old(9)] != nil {
		t.Fatal("a message didn't pay, or a bot's did")
	}
	voice := func(u snowflake.ID, vs discord.VoiceState) *events.GuildVoiceStateUpdate {
		vs.GuildID, vs.UserID = guild, u
		return &events.GuildVoiceStateUpdate{GenericGuildVoiceState: &events.GenericGuildVoiceState{
			GenericEvent: generic(), VoiceState: vs, Member: discord.Member{User: discord.User{ID: u}},
		}}
	}
	m.OnEvent(voice(a, in(room)))
	m.OnEvent(voice(b, in(room)))
	if len(s.seats) != 2 {
		t.Fatalf("%d seated", len(s.seats))
	}
	state := "come by .gg/melted"
	m.OnEvent(&events.PresenceUpdate{GenericEvent: generic(), EventPresenceUpdate: gateway.EventPresenceUpdate{Presence: discord.Presence{
		PresenceUser: discord.PresenceUser{ID: a}, GuildID: guild,
		Activities: []discord.Activity{{Type: discord.ActivityTypeCustom, State: &state}},
	}}})
	if !s.members[a].wear.Banner {
		t.Error("the banner isn't worn")
	}
	// A status put up before someone's first earning waits for them, and
	// the help page counts it and the presence that brought it.
	newcomer := old(42)
	m.OnEvent(&events.PresenceUpdate{GenericEvent: generic(), EventPresenceUpdate: gateway.EventPresenceUpdate{Presence: discord.Presence{
		PresenceUser: discord.PresenceUser{ID: newcomer}, GuildID: guild,
		Activities: []discord.Activity{{Type: discord.ActivityTypeCustom, State: &state}},
	}}})
	if js(m.Report(guild).Rows) == "" || !strings.Contains(js(m.Report(guild).Rows), `["banners","2"]`) {
		t.Errorf("the help page's banners: %s", js(m.Report(guild).Rows))
	}
	if !m.st.member(s, guild, newcomer, DayOf(t0)).wear.Banner || len(s.banners) != 0 {
		t.Error("a banner put up before the first earning was lost")
	}
	m.now = func() time.Time { return t0.Add(time.Minute) }
	m.minute()
	if v, _ := m.earning.Load(guild); v != 2 {
		t.Errorf("%v earned in voice", v)
	}
	m.Gate(func(snowflake.ID) bool { return false })
	m.minute()
	if v, _ := m.earning.Load(guild); v != 2 {
		t.Error("a server with /pp off was paid")
	}

	// A reconnect seats who's there now, and reads their statuses.
	m.OnEvent(&events.GuildReady{GenericGuild: &events.GenericGuild{GenericEvent: generic(), GuildID: guild}, Guild: discord.GatewayGuild{
		VoiceStates: []discord.VoiceState{{UserID: b, ChannelID: &room}},
		Presences:   []discord.Presence{{PresenceUser: discord.PresenceUser{ID: a}}},
	}})
	if len(s.seats) != 1 || s.members[a].wear.Banner {
		t.Errorf("after ready: %d seated, banner %v", len(s.seats), s.members[a].wear.Banner)
	}
}

func TestSaveAndReport(t *testing.T) {
	db := &fakeDB{}
	m := testModule(db)
	s := m.st.server(guild)
	s.mu.Lock()
	m.st.chat(s, guild, old(1), "hello there", t0)
	s.mu.Unlock()
	if r := m.Report(guild); r.Rows[3][1] != "not yet" {
		t.Errorf("before a save: %v", r.Rows)
	}

	db.fail = errors.New("down")
	m.Save()
	if !s.members[old(1)].dirty || !s.dirty {
		t.Fatal("a failed save forgot what it owed")
	}
	db.fail = nil
	m.Save()
	if s.members[old(1)].dirty || s.dirty || len(db.execs) != 3 {
		t.Fatalf("after a save: %d statements", len(db.execs))
	}
	m.Save()
	if len(db.execs) != 3 {
		t.Error("an idle save wrote")
	}
	m.now = func() time.Time { return t0.Add(90 * time.Second) }
	r := m.Report(guild)
	if r.Rows[2][1] != "1" || r.Rows[3][1] != "1m ago" || r.Rows[1][1] != "unlit" {
		t.Errorf("report %v", r.Rows)
	}
}

func TestModuleContract(t *testing.T) {
	m := testModule(&fakeDB{})
	if m.Name() != Name || m.Perms() != discord.PermissionViewChannel|discord.PermissionSendMessages || m.Want().Required&gateway.IntentGuildVoiceStates == 0 {
		t.Error("the module's contract")
	}
	if h := m.Help(); h.Line == "" || strings.Count(h.About, ";") > 1 {
		t.Errorf("help %+v", h)
	}
	if m.Components()[0].ID != "pp" {
		t.Error("components")
	}
}
