package purge

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
)

// administrator is every permission bit a server admin can hold.
const administrator = "8"

// forCmd is /purge <sub> member:<who> from member 5 holding perms, with
// extra options for sub.
func forCmd(t *testing.T, m *Module, sub string, who snowflake.ID, perms string, extra ...map[string]any) string {
	return command(t, m, sub, func(p map[string]any) {
		p["member"].(map[string]any)["permissions"] = perms
		opts := []any{map[string]any{"name": "member", "type": 6, "value": who.String()}}
		for _, o := range extra {
			opts = append(opts, o)
		}
		p["data"].(map[string]any)["options"] = []any{map[string]any{"name": sub, "type": 1, "options": opts}}
	})
}

// Nobody but the break-glass admin acts for someone else: not a server
// admin, not with no break-glass admin set, on any subcommand.
func TestOnlyBreakGlassActsForOthers(t *testing.T) {
	refusal := "✗ " + string(errNotYours)
	after := map[string]any{"name": "after", "type": 3, "value": "1m"}
	every := map[string]any{"name": "every", "type": 3, "value": "1d"}
	for _, bootstrap := range []snowflake.ID{0, 99} {
		m := newModule()
		m.bootstrap = core.Admins{bootstrap}
		for _, c := range []struct {
			sub   string
			extra []map[string]any
		}{{"now", nil}, {"stop", nil}, {"status", nil}, {"live", []map[string]any{after}}, {"every", []map[string]any{every}}} {
			if got := forCmd(t, m, c.sub, them, administrator, c.extra...); got != refusal {
				t.Errorf("bootstrap %d, /purge %s for someone else as a server admin: %q", bootstrap, c.sub, got)
			}
		}
		if m.liveN.Load() != 0 {
			t.Error("a refused live took effect")
		}
	}
}

// Naming yourself is the same as naming nobody.
func TestMemberYourselfIsYourself(t *testing.T) {
	m := newModule()
	if got := forCmd(t, m, "now", me, "0"); got != "modal" {
		t.Fatalf("/purge now member:yourself answered %q", got)
	}
}

// The break-glass admin opens the box for the member, and confirming it
// deletes the member's messages, not the admin's.
func TestBreakGlassPurgesTheMember(t *testing.T) {
	m := newModule()
	m.bootstrap = core.Admins{me}
	m.tick = time.Hour
	var modal discord.ModalCreate
	e, _ := coretest.Event(t, "purge", func(p map[string]any) {
		p["data"].(map[string]any)["options"] = []any{map[string]any{"name": "now", "type": 1, "options": []any{
			map[string]any{"name": "member", "type": 6, "value": them.String()},
		}}}
	})
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		modal, _ = d.(discord.ModalCreate)
		return nil
	}
	router(t, m).OnCommand(e)
	if modal.CustomID != fmt.Sprintf("%s:%d", confirmModal, them) {
		t.Fatalf("the box is %q, want it to carry the member", modal.CustomID)
	}

	f := server(t, time.Now())
	me2, sent := coretest.Modal(t, modal.CustomID, map[string]string{"confirm": "delete"}, nil)
	me2.Client().Rest = f
	router(t, m).OnModal(me2)
	if len(*sent) != 1 {
		t.Fatalf("confirm answered %+v", *sent)
	}
	if got := final(t, f); !strings.Contains(got, "✓ done") {
		t.Fatalf("readout:\n%s", got)
	}
	for _, msg := range f.msgs[textCh] {
		switch {
		case msg.Author.ID == them && !f.gone[msg.ID]:
			t.Fatal("the member's message survived")
		case msg.Author.ID == me && f.gone[msg.ID]:
			t.Fatal("the admin's own message went")
		}
	}
	// Server admins read the audit log: the operator isn't named in it.
	f.reasonsAre(t, "purge now by skua's break-glass admin, for 6")
}

// The box's custom ID comes back from the client, so a member who isn't the
// break-glass admin can't forge one for someone else, and garbage is
// refused.
func TestForgedBoxIsRefused(t *testing.T) {
	m := newModule()
	m.bootstrap = core.Admins{99}
	for id, want := range map[string]string{
		fmt.Sprintf("%s:%d", confirmModal, them): string(errNotYours),
		confirmModal + ":nonsense":               "that box is out of date; run /purge now again",
	} {
		e, sent := coretest.Modal(t, id, map[string]string{"confirm": "delete"}, nil)
		e.Client().Rest = newFake()
		router(t, m).OnModal(e)
		if len(*sent) != 1 || (*sent)[0].Content != "✗ "+want {
			t.Errorf("%s: %+v", id, *sent)
		}
	}
	if _, ok := m.running.Load(target{guildID, them}); ok {
		t.Fatal("a forged box started a sweep")
	}
}

// The break-glass admin's live, every and status act on the member's row.
func TestBreakGlassSetsTheMembersRow(t *testing.T) {
	db := testDB(t)
	m := liveModule()
	m.db, m.bootstrap = db, core.Admins{me}
	g := freshGuild()
	cmd := func(sub string, extra ...map[string]any) string {
		return command(t, m, sub, func(p map[string]any) {
			p["guild_id"] = g.String()
			opts := []any{map[string]any{"name": "member", "type": 6, "value": them.String()}}
			for _, o := range extra {
				opts = append(opts, o)
			}
			p["data"].(map[string]any)["options"] = []any{map[string]any{"name": sub, "type": 1, "options": opts}}
		})
	}
	if got := cmd("live", map[string]any{"name": "after", "type": 3, "value": "10m"}); !strings.HasPrefix(got, "**purge** · live\n✓ each new message") {
		t.Fatalf("live: %q", got)
	}
	if _, ok := m.live.Load(target{g, them}); !ok {
		t.Fatal("live went on for someone other than the member")
	}
	if _, ok := m.live.Load(target{g, me}); ok {
		t.Fatal("live went on for the admin")
	}
	if got := cmd("every", map[string]any{"name": "every", "type": 3, "value": "7d"}); !strings.HasPrefix(got, "**purge** · every\n✓ every 7d") {
		t.Fatalf("every: %q", got)
	}
	if got := cmd("status"); !strings.Contains(oneSpace(got), "live 10m") || !strings.Contains(oneSpace(got), "every 7d") {
		t.Fatalf("status:\n%s", got)
	}
}

// id reaches who member can't: it's held to break-glass, opens the box for
// that ID, and refuses what isn't one.
func TestBreakGlassByID(t *testing.T) {
	byID := func(m *Module, v string) string {
		return command(t, m, "now", func(p map[string]any) {
			p["member"].(map[string]any)["permissions"] = administrator
			p["data"].(map[string]any)["options"] = []any{map[string]any{"name": "now", "type": 1, "options": []any{
				map[string]any{"name": "id", "type": 3, "value": v},
			}}}
		})
	}
	m := newModule()
	if got := byID(m, them.String()); got != "✗ "+string(errNotYours) {
		t.Errorf("a server admin by id: %q", got)
	}
	m.bootstrap = core.Admins{me}
	if got := byID(m, " "+them.String()+" "); got != "modal" {
		t.Errorf("break-glass by id: %q", got)
	}
	if got := byID(m, "someone"); !strings.Contains(got, "isn't an ID") {
		t.Errorf("not an id: %q", got)
	}
}

// A post from an app a member installed on their own account is theirs; a
// server bot's reply to their command is not.
func TestRanBy(t *testing.T) {
	app := snowflake.ID(77)
	post := func(owners map[discord.ApplicationIntegrationType]snowflake.ID) discord.Message {
		return discord.Message{Author: discord.User{ID: app}, InteractionMetadata: &discord.InteractionMetadata{
			User: discord.User{ID: them}, AuthorizingIntegrationOwners: owners,
		}}
	}
	user := map[discord.ApplicationIntegrationType]snowflake.ID{discord.ApplicationIntegrationTypeUserInstall: them}
	both := map[discord.ApplicationIntegrationType]snowflake.ID{discord.ApplicationIntegrationTypeUserInstall: them, discord.ApplicationIntegrationTypeGuildInstall: 1}
	guild := map[discord.ApplicationIntegrationType]snowflake.ID{discord.ApplicationIntegrationTypeGuildInstall: 1}
	for name, c := range map[string]struct {
		m    discord.Message
		want snowflake.ID
	}{
		"user app": {post(user), them}, "installed both ways": {post(both), 0}, "server bot": {post(guild), 0},
		"typed": {discord.Message{Author: discord.User{ID: them}}, 0},
	} {
		if got := ranBy(c.m); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
}
