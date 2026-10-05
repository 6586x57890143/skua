package whisper

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
)

// coretest's application and member: app 2, member 5 "member".
const ctApp = "2"

// fetched is what GetWebhookMessage returns, and records reads, edits and
// deletes of whispers, on top of the posting fake.
func (f *fake) GetWebhookMessage(_ snowflake.ID, _ string, id snowflake.ID, _ ...rest.RequestOpt) (*discord.Message, error) {
	if f.fetched == nil {
		return nil, errors.New("no such message")
	}
	m := *f.fetched
	m.ID = id
	return &m, nil
}

func (f *fake) UpdateWebhookMessage(_ snowflake.ID, _ string, _ snowflake.ID, u discord.WebhookMessageUpdate, _ rest.UpdateWebhookMessageParams, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.updates = append(f.updates, u)
	return nil, nil
}

func (f *fake) DeleteWebhookMessage(_ snowflake.ID, _ string, id, _ snowflake.ID, _ ...rest.RequestOpt) error {
	f.removed = append(f.removed, id)
	return nil
}

// target is a message-command interaction on a message reading content,
// posted through webhook (none for 0) stamped with application app.
func target(t *testing.T, name, content string, webhook int, app string) (*events.ApplicationCommandInteractionCreate, *[]discord.MessageCreate) {
	t.Helper()
	return coretest.Event(t, name, func(p map[string]any) {
		msg := map[string]any{"id": "8", "channel_id": "4", "content": content, "author": map[string]any{"id": "77", "username": "Nick"}}
		if webhook != 0 {
			msg["webhook_id"] = snowflake.ID(webhook).String()
			msg["application_id"] = app
		}
		d := p["data"].(map[string]any)
		d["type"], d["target_id"] = 3, "8"
		d["resolved"] = map[string]any{"messages": map[string]any{"8": msg}}
	})
}

func TestDeleteWhisperRemovesOnlyTheMembersOwn(t *testing.T) {
	mine := "hello" + marker("member")
	f := &fake{owned: []discord.Webhook{incoming("701", ctApp)}}
	e, _ := target(t, "Delete whisper", mine, 701, ctApp)
	e.Client().Rest = f
	if err := newWhisper().deleteWhisper(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 1 || f.removed[0] != 8 || f.deletes != 1 {
		t.Fatalf("removed %v, deferred replies deleted %d: want message 8 gone, silently", f.removed, f.deletes)
	}

	// Posted before the rename: the old marker still says whose it is, and
	// only for that member.
	legacy := func(user string) string { return "hello\n-# echoed through skua by @" + user }
	if text, ok := byMember(discord.Message{Content: legacy("member")}, "member"); !ok || text != "hello" {
		t.Errorf("a pre-rename post: %q, %v", text, ok)
	}
	if _, ok := byMember(discord.Message{Content: legacy("other")}, "member"); ok {
		t.Error("someone else's pre-rename post counted as the member's")
	}

	for name, c := range map[string]struct {
		content string
		webhook int
		app     string
	}{
		"someone else's whisper":   {"hello" + marker("other"), 701, ctApp},
		"a member's own message":   {"hello" + marker("member"), 0, ""},
		"another app's webhook":    {"hello" + marker("member"), 701, "999"},
		"a marker in the middle":   {"x" + marker("member") + " and more", 701, ctApp},
		"another member, prefixed": {"hello" + marker("amember"), 701, ctApp},
	} {
		f := &fake{owned: []discord.Webhook{incoming("701", ctApp)}}
		e, _ := target(t, "Delete whisper", c.content, c.webhook, c.app)
		e.Client().Rest = f
		if err := newWhisper().deleteWhisper(t.Context(), e); !errors.Is(err, errNotYours) || len(f.removed) != 0 {
			t.Errorf("%s: err %v, removed %v", name, err, f.removed)
		}
	}
}

func TestEditWhisperOpensAPrefilledBox(t *testing.T) {
	e, _ := target(t, "Edit whisper", `-\# my words`+marker("member"), 701, ctApp)
	var modal discord.ModalCreate
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		modal = d.(discord.ModalCreate)
		return nil
	}
	if err := newWhisper().editWhisper(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if modal.CustomID != "whisper-edit:701:8" {
		t.Errorf("custom ID %q", modal.CustomID)
	}
	in := modal.Components[0].(discord.LabelComponent).Component.(discord.TextInputComponent)
	if in.Value != `-\# my words` || in.MaxLength != maxText || in.Style != discord.TextInputStyleShort {
		t.Errorf("text input %+v: want the whisper minus its marker, one line, capped", in)
	}

	e, _ = target(t, "Edit whisper", "hi"+marker("other"), 701, ctApp)
	if err := newWhisper().editWhisper(t.Context(), e); !errors.Is(err, errNotYours) {
		t.Errorf("someone else's whisper: %v", err)
	}
}

func TestSubmitEditRewritesThroughTheSameScreen(t *testing.T) {
	sendAll := func(p map[string]any) { p["member"].(map[string]any)["permissions"] = "2048" }
	f := &fake{
		owned:   []discord.Webhook{incoming("701", ctApp)},
		fetched: &discord.Message{Content: "old" + marker("member")},
	}
	e, _ := coretest.Modal(t, "whisper-edit:701:8", map[string]string{"message": "you f4ggot\nlook -# here"}, sendAll)
	e.Client().Rest = f
	if err := newWhisper().submitEdit(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if len(f.updates) != 1 || f.deletes != 1 {
		t.Fatalf("updates %d, deferred replies deleted %d", len(f.updates), f.deletes)
	}
	u := f.updates[0]
	got := *u.Content
	if !strings.HasPrefix(got, "you ") || !strings.HasSuffix(got, " look -# here"+marker("member")) || strings.Contains(got, "\n-# here") {
		t.Errorf("edited to %q: want one line, slur rewritten, marker last", got)
	}
	if u.Flags == nil || *u.Flags != discord.MessageFlagSuppressEmbeds || u.AllowedMentions == nil {
		t.Errorf("update %+v: want no pings and no unfurls for a member without Embed Links", u)
	}

	cases := map[string]struct {
		id, text string
		fetched  *discord.Message
		edit     func(map[string]any)
		want     string
	}{
		"a forged custom ID":     {"whisper-edit:nope", "x", nil, sendAll, "out of date"},
		"a foreign webhook":      {"whisper-edit:999:8", "x", nil, sendAll, "your own"},
		"someone else's whisper": {"whisper-edit:701:8", "x", &discord.Message{Content: "old" + marker("other")}, sendAll, "your own"},
		"a vanished whisper":     {"whisper-edit:701:8", "x", nil, sendAll, "couldn't be found"},
		"no Send Messages":       {"whisper-edit:701:8", "x", nil, nil, "can't send"},
		"a grabber link":         {"whisper-edit:701:8", "https://grabify.link/x", nil, sendAll, "ip grabber"},
		"nothing left to post":   {"whisper-edit:701:8", " \n ", nil, sendAll, "nothing to send"},
		"a timeout": {"whisper-edit:701:8", "x", nil, func(p map[string]any) {
			sendAll(p)
			p["member"].(map[string]any)["communication_disabled_until"] = "2999-01-01T00:00:00Z"
		}, "timed out"},
	}
	for name, c := range cases {
		f := &fake{owned: []discord.Webhook{incoming("701", ctApp)}, fetched: c.fetched}
		e, _ := coretest.Modal(t, c.id, map[string]string{"message": c.text}, c.edit)
		e.Client().Rest = f
		err := newWhisper().submitEdit(t.Context(), e)
		if err == nil || !strings.Contains(err.Error(), c.want) || len(f.updates) != 0 {
			t.Errorf("%s: err %v, updates %d, want %q and nothing changed", name, err, len(f.updates), c.want)
		}
	}
}

func TestWhisperOffersItsOwnershipCommandsAndModal(t *testing.T) {
	m := newWhisper()
	var names []string
	for _, c := range m.Commands() {
		names = append(names, c.Create.CommandName())
	}
	if strings.Join(names, ",") != "whisper,Edit whisper,Delete whisper" {
		t.Errorf("commands %v", names)
	}
	if md := m.Modals(); len(md) != 1 || md[0].ID != editModal {
		t.Errorf("modals %+v", md)
	}
}

// Edit refusals go through core.Router like command refusals, so they must
// be Tells: checked as the reply the member gets, not as a returned error.
func TestEditRefusalsReachTheMemberThroughTheRouter(t *testing.T) {
	r := core.NewRouter(0, nil, slog.New(slog.DiscardHandler))
	if err := r.Add(newWhisper()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"whisper-edit:not-a-snowflake": "✗ that edit box is out of date; open it again",
		"whisper-edit:701:8":           "✗ you can't send messages in this channel",
	} {
		e, sent := coretest.Modal(t, id, map[string]string{"message": " "}, nil)
		r.OnModal(e)
		if len(*sent) != 1 || (*sent)[0].Content != want {
			t.Errorf("%s: the member got %+v, want %q", id, *sent, want)
		}
	}
}

// Wrote answers from what skua noted when it sent each whisper: the writer,
// and only the writer, of a whisper it remembers, and no REST call for it.
func TestWroteKnowsTheWhispersWriter(t *testing.T) {
	m := newWhisper()
	m.writers.add(9, 5)
	if !m.Wrote(9, 5) || m.Wrote(9, 6) || m.Wrote(10, 5) || m.Wrote(10, 0) {
		t.Fatal("Wrote is not exactly the noted writer")
	}
}

func TestWritersForgetsTheOldest(t *testing.T) {
	w := writers{by: map[snowflake.ID]snowflake.ID{}, ring: make([]snowflake.ID, 2)}
	w.add(1, 5)
	w.add(2, 5)
	w.add(1, 6) // already noted: the first writer stands
	w.add(3, 5)
	if w.of(1) != 0 || w.of(2) != 5 || w.of(3) != 5 {
		t.Fatalf("writers %v", w.by)
	}
}
