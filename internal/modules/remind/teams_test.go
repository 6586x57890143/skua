package remind

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// restOf is the REST an interaction posts a team's card with.
type restOf struct {
	rest.Rest
	p *poster
}

func (r restOf) CreateMessage(ch snowflake.ID, m discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error) {
	return r.p.CreateMessage(ch, m, opts...)
}

func admins(m *Module) { m.admin = func(discord.Interaction) bool { return true } }

func makeTeam(t *testing.T, m *Module, p *poster, g, u snowflake.ID, name string, ch string) (string, error) {
	t.Helper()
	e, _ := coretest.Modal(t, teamID, map[string]string{"name": name}, func(pl map[string]any) {
		pl["guild_id"] = g.String()
		pl["member"].(map[string]any)["user"].(map[string]any)["id"] = u.String()
		d := pl["data"].(map[string]any)
		if ch != "" {
			d["components"] = append(d["components"].([]any), map[string]any{"type": 18, "component": map[string]any{"type": 8, "custom_id": "channel", "values": []string{ch}}})
			d["resolved"] = map[string]any{"channels": map[string]any{ch: map[string]any{"id": ch, "name": "dev", "type": 0, "permissions": "3072"}}}
		}
	})
	e.Client().Rest = restOf{p: p}
	var got discord.MessageUpdate
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		got, _ = d.(discord.MessageUpdate)
		return nil
	}
	if err := m.makeTeam(context.Background(), e); err != nil {
		return "", err
	}
	return deep(*got.Components), nil
}

// teamPress presses customID in guild g, on an ephemeral reply or a
// channel's card.
func teamPress(t *testing.T, m *Module, p *poster, g, u snowflake.ID, customID string, ephemeral bool, edit func(map[string]any)) (string, error) {
	t.Helper()
	e, _ := coretest.Button(t, customID, func(pl map[string]any) {
		pl["guild_id"] = g.String()
		pl["member"].(map[string]any)["user"].(map[string]any)["id"] = u.String()
		if !ephemeral {
			pl["message"].(map[string]any)["flags"] = 32768
		}
		if edit != nil {
			edit(pl)
		}
	})
	e.Client().Rest = restOf{p: p}
	var got discord.MessageUpdate
	modal := false
	e.Respond = func(typ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		if typ == discord.InteractionResponseTypeModal {
			modal = true
			return nil
		}
		got, _ = d.(discord.MessageUpdate)
		return nil
	}
	if err := m.click(context.Background(), e); err != nil {
		return "", err
	}
	if modal {
		return "modal", nil
	}
	return deep(*got.Components), nil
}

func teamIDOf(t *testing.T, m *Module, g snowflake.ID, name string) int64 {
	t.Helper()
	var id int64
	if err := m.db.QueryRow(context.Background(), "select id from remind_team where guild_id = $1 and name = $2", int64(g), name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTeams(t *testing.T) {
	m, _, p, g, u := fresh(t)
	ctx := context.Background()
	if out, err := teamPress(t, m, p, g, u, "remind:teams", true, nil); err != nil || !strings.Contains(out, "no teams yet") || strings.Contains(out, "make one") {
		t.Fatalf("teams: %v %q", err, out)
	}
	if _, err := teamPress(t, m, p, g, u, "remind:mkteam", true, nil); err == nil {
		t.Fatal("a member made a team")
	}
	if _, err := makeTeam(t, m, p, g, u, "standup crew", "60"); err == nil {
		t.Fatal("a member's form made a team")
	}
	admins(m)
	if out, err := teamPress(t, m, p, g, u, "remind:mkteam", true, nil); err != nil || out != "modal" {
		t.Fatalf("mkteam: %v %q", err, out)
	}
	for _, c := range [][2]string{{" ", "60"}, {"crew", ""}} {
		if _, err := makeTeam(t, m, p, g, u, c[0], c[1]); err == nil {
			t.Errorf("%v made a team", c)
		}
	}
	out, err := makeTeam(t, m, p, g, u, "standup crew", "60")
	if err != nil || !strings.Contains(out, "✓ made standup crew and posted its card in <#60>") || !strings.Contains(out, "**standup crew** · 0 on it · <#60>") {
		t.Fatalf("make: %v %q", err, out)
	}
	s := p.all()
	if len(s) != 1 || s[0].ch != 60 || !strings.Contains(text(s[0].msg), "**remind** · team") || !strings.Contains(text(s[0].msg), "nothing set yet") {
		t.Fatalf("card %+v", s)
	}
	if _, err := makeTeam(t, m, p, g, u, "Standup Crew", "60"); err == nil || !strings.Contains(err.Error(), "already has a team") {
		t.Fatalf("duplicate: %v", err)
	}
	tid := teamIDOf(t, m, g, "standup crew")
	sid := itoa(tid)

	// a member opens it from the agenda and joins
	m.admin = func(discord.Interaction) bool { return false }
	out, err = teamPress(t, m, p, g, u, "remind:team", true, pickValues(sid))
	if err != nil || !strings.Contains(out, "### standup crew") || strings.Contains(out, "you're on it") {
		t.Fatalf("open: %v %q", err, out)
	}
	if out, err = teamPress(t, m, p, g, u, "remind:join:"+sid, true, nil); err != nil || !strings.Contains(out, "✓ joined standup crew") || !strings.Contains(out, "you're on it") {
		t.Fatalf("join: %v %q", err, out)
	}
	// on the channel's card it redraws the card for everyone
	if out, err = teamPress(t, m, p, g, u+1, "remind:join:"+sid, false, nil); err != nil || !strings.Contains(out, "members  2") || strings.Contains(out, "you're on it") {
		t.Fatalf("card join: %v %q", err, out)
	}
	if out, err = teamPress(t, m, p, g, u+1, "remind:leave:"+sid, true, nil); err != nil || !strings.Contains(out, "✓ left") {
		t.Fatalf("leave: %v %q", err, out)
	}
	for i := range perTeam - 1 {
		if err := enlist(ctx, m.db, tid, u+snowflake.ID(100+i), true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := teamPress(t, m, p, g, u+1, "remind:join:"+sid, true, nil); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("full: %v", err)
	}
	if err := enlist(ctx, m.db, tid, u, true); err != nil {
		t.Fatal("joining again when full and on it")
	}
	// admins only: post and delete
	for _, v := range []string{"post", "rmteam"} {
		if _, err := teamPress(t, m, p, g, u, "remind:"+v+":"+sid, true, nil); err == nil {
			t.Errorf("a member could %s", v)
		}
	}
	admins(m)
	if out, err = teamPress(t, m, p, g, u, "remind:post:"+sid, true, nil); err != nil || !strings.Contains(out, "✓ card posted in <#60>") {
		t.Fatalf("post: %v %q", err, out)
	}
	p.err = errors.New("missing access")
	if _, err = teamPress(t, m, p, g, u, "remind:post:"+sid, true, nil); err == nil || !strings.Contains(err.Error(), "couldn't post") {
		t.Fatalf("post failing: %v", err)
	}
	if out, err = makeTeam(t, m, p, g, u, "night owls", "61"); err != nil || !strings.Contains(out, "its card couldn't be posted") {
		t.Fatalf("make failing: %v %q", err, out)
	}
	p.err = nil
	// a team elsewhere, gone or out of date
	for _, bad := range []string{"remind:join:" + sid + "0", "remind:join:x", "remind:team", "remind:hm:" + sid} {
		if _, err := teamPress(t, m, p, g, u, bad, true, nil); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
	if _, err := teamPress(t, m, p, g+1, u, "remind:join:"+sid, true, nil); err == nil {
		t.Fatal("joined from another server")
	}
	if _, err := teamPress(t, m, p, g, u, "remind:join:"+sid, true, func(pl map[string]any) { delete(pl, "guild_id") }); err == nil {
		t.Fatal("joined outside a server")
	}
	// the list and the autocomplete
	if cs, err := m.teamChoices(ctx, g, "STAND"); err != nil || len(cs) != 1 || cs[0].(discord.AutocompleteChoiceString).Value != sid {
		t.Fatalf("choices %+v %v", cs, err)
	}
	if got := autocomplete(t, m, u, "team", "", ""); len(got) != 0 {
		t.Fatalf("choices in guild 3: %+v", got)
	}
	r := m.add(t, reminder{guild: g, owner: u, due: now.Add(time.Hour), target: "team", targetID: snowflake.ID(tid)})
	if out, err = teamPress(t, m, p, g, u, "remind:team", true, pickValues(sid)); err != nil || !strings.Contains(out, "next") || !strings.Contains(out, "<t:") {
		t.Fatalf("next: %v %q", err, out)
	}
	if out, err = teamPress(t, m, p, g, u, "remind:rmteam:"+sid, true, nil); err != nil || !strings.Contains(out, "✓ deleted standup crew and its reminders") {
		t.Fatalf("delete: %v %q", err, out)
	}
	if _, ok, _ := get(ctx, m.db, r.id); ok {
		t.Fatal("kept a deleted team's reminder")
	}
}

func TestTeamCap(t *testing.T) {
	m, _, p, g, u := fresh(t)
	admins(m)
	for i := range teamsPer {
		if _, err := addTeam(context.Background(), m.db, team{guild: g, channel: 60, by: u, name: "t" + itoa(int64(i))}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := makeTeam(t, m, p, g, u, "one more", "60"); err == nil || !strings.Contains(err.Error(), "most skua keeps") {
		t.Fatalf("cap: %v", err)
	}
	if out, err := teamPress(t, m, p, g, u, "remind:teams", true, nil); err != nil || strings.Contains(out, "new team") {
		t.Fatalf("teams at the cap: %v", err)
	}
}

// group runs /remind naming a team or role.
func group(t *testing.T, m *Module, g, u snowflake.ID, perms string, edit func(d map[string]any)) (string, error) {
	t.Helper()
	e, sent := coretest.Event(t, "remind", as(u, g, func(pl map[string]any) {
		opts("when", "in 1h", "what", "standup")(pl)
		if perms != "" {
			pl["member"].(map[string]any)["permissions"] = perms
		}
		edit(pl["data"].(map[string]any))
	}))
	if err := m.remind(context.Background(), e); err != nil {
		return "", err
	}
	return text((*sent)[0]), nil
}

func withOpt(d map[string]any, name string, typ int, value string) {
	d["options"] = append(d["options"].([]any), map[string]any{"name": name, "type": typ, "value": value})
}

func withRole(d map[string]any, id string, mentionable bool) {
	withOpt(d, "role", 8, id)
	d["resolved"] = map[string]any{"roles": map[string]any{id: map[string]any{"id": id, "name": "devs", "mentionable": mentionable, "color": 0, "position": 1, "permissions": "0"}}}
}

func TestGroupReminders(t *testing.T) {
	m, c, p, g, u := fresh(t)
	ctx := context.Background()
	tid, err := addTeam(ctx, m.db, team{guild: g, channel: 60, by: u, name: "crew"})
	if err != nil {
		t.Fatal(err)
	}
	sid := itoa(tid)
	if _, err := group(t, m, g, u, "", func(d map[string]any) { withOpt(d, "team", 3, sid) }); err == nil || !strings.Contains(err.Error(), "join crew") {
		t.Fatalf("not on it: %v", err)
	}
	for _, v := range []string{"x", sid + "9"} {
		if _, err := group(t, m, g, u, "", func(d map[string]any) { withOpt(d, "team", 3, v) }); err == nil {
			t.Errorf("team %s", v)
		}
	}
	for _, who := range []snowflake.ID{u, u + 1} {
		if err := enlist(ctx, m.db, tid, who, true); err != nil {
			t.Fatal(err)
		}
	}
	out, err := group(t, m, g, u, "", func(d map[string]any) { withOpt(d, "team", 3, sid) })
	if err != nil || !strings.Contains(out, "**to** the team in <#60>") {
		t.Fatalf("team: %v %q", err, out)
	}
	if _, err := group(t, m, g, u, "", func(d map[string]any) { withOpt(d, "team", 3, sid); withRole(d, "77", true) }); err == nil {
		t.Fatal("team and role")
	}
	// roles
	if _, err := group(t, m, g, u, "2048", func(d map[string]any) { withRole(d, g.String(), true) }); err == nil || !strings.Contains(err.Error(), "never pings everyone") {
		t.Fatalf("everyone: %v", err)
	}
	if _, err := group(t, m, g, u, "", func(d map[string]any) { withRole(d, "77", true) }); err == nil || !strings.Contains(err.Error(), "can't post") {
		t.Fatalf("can't post: %v", err)
	}
	if _, err := group(t, m, g, u, "2048", func(d map[string]any) { withRole(d, "77", false) }); err == nil || !strings.Contains(err.Error(), "can't ping @devs") {
		t.Fatalf("not mentionable: %v", err)
	}
	if out, err := group(t, m, g, u, "133120", func(d map[string]any) { withRole(d, "77", false) }); err != nil || !strings.Contains(out, "<@&77> in <#4>") {
		t.Fatalf("mention everyone: %v %q", err, out)
	}
	out, err = group(t, m, g, u, "", func(d map[string]any) {
		withRole(d, "77", true)
		withOpt(d, "channel", 7, "62")
		d["resolved"].(map[string]any)["channels"] = map[string]any{"62": map[string]any{"id": "62", "name": "ops", "type": 0, "permissions": "3072"}}
	})
	if err != nil || !strings.Contains(out, "<@&77> in <#62>") {
		t.Fatalf("role in a channel: %v %q", err, out)
	}

	// firing: a team pings exactly who's on it, a role only that role
	tr := m.add(t, reminder{guild: g, owner: u, due: now, target: "team", targetID: snowflake.ID(tid)})
	m.fire(ctx, tr)
	rr := m.add(t, reminder{guild: g, owner: u, due: now, target: "role", targetID: 77})
	m.fire(ctx, rr)
	s := p.all()
	if len(s) != 2 || s[0].ch != 60 || s[1].ch != 4 {
		t.Fatalf("sent %+v", s)
	}
	if am := s[0].msg.AllowedMentions; len(am.Users) != 2 || len(am.Roles) != 0 || len(am.Parse) != 0 || !strings.Contains(text(s[0].msg), "<@"+u.String()+"> <@"+(u+1).String()+">") {
		t.Fatalf("team pings %+v %q", am, text(s[0].msg))
	}
	if am := s[1].msg.AllowedMentions; len(am.Roles) != 1 || am.Roles[0] != 77 || len(am.Users) != 0 {
		t.Fatalf("role pings %+v", am)
	}
	// nobody answered yet; then two do, one twice
	key := itoa(tr.id) + ":" + itoa(now.UnixMilli())
	for _, who := range []snowflake.ID{u, u, u + 5} {
		out, err = teamPress(t, m, p, g, who, "remind:here:"+key, false, nil)
	}
	if err != nil || !strings.Contains(out, "✓ 2 here") || !strings.Contains(out, "<@"+u.String()+">") {
		t.Fatalf("here: %v %q", err, out)
	}
	for _, bad := range []string{"remind:here:" + itoa(tr.id), "remind:here:x:1", "remind:here:0:1"} {
		if _, err := teamPress(t, m, p, g, u, bad, false, nil); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
	if _, err := teamPress(t, m, p, g+1, u, "remind:here:"+key, false, nil); err == nil {
		t.Fatal("answered from another server")
	}
	// an empty team still gets its reminder; a gone one is retried, then dropped
	if err := enlist(ctx, m.db, tid, u, false); err != nil {
		t.Fatal(err)
	}
	if err := enlist(ctx, m.db, tid, u+1, false); err != nil {
		t.Fatal(err)
	}
	m.fire(ctx, m.add(t, reminder{guild: g, owner: u, due: now, target: "team", targetID: snowflake.ID(tid), r: rule{mask: everyDay, at: 600}}))
	if s := p.all(); !strings.Contains(text(s[2].msg), "nobody's on this team yet") || !strings.Contains(text(s[2].msg), "every day at 10:00") {
		t.Fatalf("empty team %q", text(s[2].msg))
	}
	gone := m.add(t, reminder{guild: g, owner: u, due: now, target: "team", targetID: 1 << 50})
	m.fire(ctx, gone)
	if got := m.row(t, gone.id); got.state != "pending" {
		t.Fatalf("a gone team's reminder is %s", got.state)
	}
	c.add(lateBy)
	if out := text(called(rr, lateBy, nil, 0)); !strings.Contains(out, "late by 5s") {
		t.Fatalf("late %q", out)
	}
}

func TestLeavingDropsTeams(t *testing.T) {
	m, _, _, g, u := fresh(t)
	if _, err := addTeam(context.Background(), m.db, team{guild: g, channel: 60, by: u, name: "crew"}); err != nil {
		t.Fatal(err)
	}
	if err := dropGuild(context.Background(), m.db, g); err != nil {
		t.Fatal(err)
	}
	if ts, _, err := teamsOf(context.Background(), m.db, g); err != nil || len(ts) != 0 {
		t.Fatalf("teams after leaving %d %v", len(ts), err)
	}
}
