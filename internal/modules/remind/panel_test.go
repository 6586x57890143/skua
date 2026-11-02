package remind

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

func pickValues(v ...string) func(map[string]any) {
	return func(p map[string]any) {
		p["data"].(map[string]any)["component_type"] = 3
		p["data"].(map[string]any)["values"] = v
	}
}

func TestAgenda(t *testing.T) {
	m, _, _, g, u := fresh(t)
	ctx := context.Background()
	msg, err := m.agendaPanel(ctx, u, "")
	if err != nil {
		t.Fatal(err)
	}
	if out := text(msg); !strings.Contains(out, "## reminders") || !strings.Contains(out, "0 waiting · utc, no zone set") || !strings.Contains(out, "nothing waiting") {
		t.Fatalf("empty agenda %q", out)
	}
	if err := m.keepZone(ctx, u, "Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	m.add(t, reminder{guild: g, owner: u, due: at(11, 2, 17, 30), text: "stretch", r: rule{mask: everyDay, at: 17*60 + 30}})
	m.add(t, reminder{guild: g, owner: u, due: at(11, 3, 9, 0), text: "standup", r: rule{mask: weekdays, at: 540}})
	m.add(t, reminder{guild: g, owner: u, due: at(11, 6, 17, 0), text: "ship it"})
	m.add(t, reminder{guild: g, owner: u, due: time.Date(2027, 1, 4, 9, 0, 0, 0, berlin), text: "taxes"})
	msg, err = m.agendaPanel(ctx, u, "✓ set")
	if err != nil {
		t.Fatal(err)
	}
	out := text(msg)
	for _, want := range []string{"4 waiting · europe/berlin", "✓ set", "**today**\n<t:", " · stretch · daily", "**tomorrow**", " · standup · weekdays", "**fri 6 nov**", "**mon 4 jan 2027**", "next one <t:"} {
		if !strings.Contains(out, want) {
			t.Errorf("agenda lacks %q:\n%s", want, out)
		}
	}
	for range shown {
		m.add(t, reminder{guild: g, owner: u, due: at(12, 1, 9, 0)})
	}
	msg, _ = m.agendaPanel(ctx, u, "")
	if out := text(msg); !strings.Contains(out, "25+ waiting") || !strings.Contains(out, "and more after these") {
		t.Fatalf("long agenda %q", out)
	}
	e, sent := coretest.Event(t, "reminders", as(u, g, nil))
	if err := m.agendaCommand().Run(ctx, e); err != nil || len(*sent) != 1 || !(*sent)[0].Flags.Has(discord.MessageFlagEphemeral) {
		t.Fatalf("/reminders: %v", err)
	}
}

func TestAgendaControls(t *testing.T) {
	m, _, _, g, u := fresh(t)
	r := m.add(t, reminder{guild: g, owner: u, due: at(11, 6, 17, 0), text: "ship it"})
	out, err := click(t, m, u, "remind:open", now, pickValues(itoa(r.id)))
	if err != nil || !strings.Contains(out, "### ship it") || !strings.Contains(out, "**remind** · set") {
		t.Fatalf("open: %v %q", err, out)
	}
	if _, err := click(t, m, u+1, "remind:open", now, pickValues(itoa(r.id))); err == nil {
		t.Fatal("opened someone else's")
	}
	if _, err := click(t, m, u, "remind:open", now, pickValues("x")); err == nil {
		t.Fatal("opened nonsense")
	}
	if out, err := click(t, m, u, "remind:agenda", now, nil); err != nil || !strings.Contains(out, "1 waiting") {
		t.Fatalf("agenda: %v %q", err, out)
	}
	if out, err := click(t, m, u, "remind:new", now, nil); err != nil || out != "modal" {
		t.Fatalf("new: %v %q", err, out)
	}
	if out, err := click(t, m, u, "remind:zone", now, nil); err != nil || out != "modal" {
		t.Fatalf("zone: %v %q", err, out)
	}
	if _, err := click(t, m, u, "remind:what", now, nil); err == nil {
		t.Fatal("unknown verb")
	}
	// the set card's cancel, on an ephemeral reply, leads back
	out, err = click(t, m, u, "remind:x:"+itoa(r.id), now, nil)
	if err != nil || !strings.Contains(out, "cancelled") {
		t.Fatalf("cancel: %v %q", err, out)
	}
}

func TestSettings(t *testing.T) {
	m, _, _, _, u := fresh(t)
	out, err := click(t, m, u, "remind:set", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"**remind** · settings", "zone      utc, not set", "delivery  your dms", "snooze    10m, 1h, tomorrow", "quiet     off"} {
		if !strings.Contains(out, want) {
			t.Errorf("settings lacks %q:\n%s", want, out)
		}
	}
	if out, err = click(t, m, u, "remind:deliver", now, pickValues("here")); err != nil || !strings.Contains(out, "delivery  where you set it") {
		t.Fatalf("deliver: %v %q", err, out)
	}
	if out, err = click(t, m, u, "remind:snooze", now, pickValues("-1", "5", "120")); err != nil || !strings.Contains(out, "snooze    5m, 2h, tomorrow") {
		t.Fatalf("snooze: %v %q", err, out)
	}
	if out, err = click(t, m, u, "remind:quiet", now, pickValues("1380-420")); err != nil || !strings.Contains(out, "quiet     23:00 → 07:00") {
		t.Fatalf("quiet: %v %q", err, out)
	}
	for _, bad := range []struct {
		id string
		v  []string
	}{{"remind:deliver", []string{"moon"}}, {"remind:snooze", []string{"7"}}, {"remind:snooze", nil}, {"remind:snooze", []string{"5", "10", "15", "30"}}, {"remind:quiet", []string{"1-2"}}, {"remind:quiet", nil}} {
		if _, err := click(t, m, u, bad.id, now, pickValues(bad.v...)); err == nil {
			t.Errorf("%s %v: no error", bad.id, bad.v)
		}
	}
	// kept across a restart
	again := New(quiet(), m.guard, m.db)
	p, err := again.person(context.Background(), u)
	if err != nil || p.deliver != "here" || len(p.snooze) != 3 || p.snooze[2] != tomorrow || p.quietWords() != "23:00 → 07:00" {
		t.Fatalf("after restart %+v %v", p, err)
	}
}

func TestNewAndZoneForms(t *testing.T) {
	m, _, _, g, u := fresh(t)
	ctx := context.Background()
	send := func(customID string, values map[string]string, selects map[string][]string, perms string, run func(context.Context, *Module, any) error) (string, error) {
		e, _ := coretest.Modal(t, customID, values, func(p map[string]any) {
			p["member"].(map[string]any)["user"].(map[string]any)["id"] = u.String()
			p["guild_id"] = g.String()
			if perms != "" {
				p["member"].(map[string]any)["permissions"] = perms
			}
			rows := p["data"].(map[string]any)["components"].([]any)
			for id, v := range selects {
				rows = append(rows, map[string]any{"type": 18, "component": map[string]any{"type": 3, "custom_id": id, "values": v}})
			}
			p["data"].(map[string]any)["components"] = rows
		})
		var got discord.MessageUpdate
		e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
			got, _ = d.(discord.MessageUpdate)
			return nil
		}
		if err := run(ctx, m, e); err != nil {
			return "", err
		}
		return deep(*got.Components), nil
	}
	out, err := send(newID, map[string]string{"what": "water plants", "when": "every weekday at 9"}, map[string][]string{"repeat": {"as"}}, "", modalRun(m.create))
	if err != nil || !strings.Contains(out, "### water plants") || !strings.Contains(out, "weekdays at 09:00") || !strings.Contains(out, "✓ set") {
		t.Fatalf("new: %v %q", err, out)
	}
	out, err = send(newID, map[string]string{"what": "x", "when": "in 1h"}, map[string][]string{"repeat": {"day"}, "to": {"dm"}}, "", modalRun(m.create))
	if err != nil || !strings.Contains(out, "every day at") || !strings.Contains(out, "your dms") {
		t.Fatalf("new daily: %v %q", err, out)
	}
	if _, err = send(newID, map[string]string{"what": "x", "when": "in 1h"}, map[string][]string{"to": {"here"}}, "", modalRun(m.create)); err == nil || !strings.Contains(err.Error(), "can't post here") {
		t.Fatalf("here without perms: %v", err)
	}
	out, err = send(newID, map[string]string{"what": "x", "when": "in 1h"}, map[string][]string{"to": {"here"}}, "2048", modalRun(m.create))
	if err != nil || !strings.Contains(out, "<#4>") {
		t.Fatalf("here: %v %q", err, out)
	}
	if _, err = send(newID, map[string]string{"what": "x", "when": "banana"}, nil, "", modalRun(m.create)); err == nil {
		t.Fatal("banana")
	}
	// a setting of here, where the member can't post, falls back to dms
	if _, err := m.change(ctx, u, func(p *person) { p.deliver = "here" }); err != nil {
		t.Fatal(err)
	}
	if out, err = send(newID, map[string]string{"what": "x", "when": "in 1h"}, nil, "", modalRun(m.create)); err != nil || !strings.Contains(out, "your dms") {
		t.Fatalf("setting fallback: %v %q", err, out)
	}
	if out, err = send(newID, map[string]string{"what": "x", "when": "in 1h"}, nil, "2048", modalRun(m.create)); err != nil || !strings.Contains(out, "<#4>") {
		t.Fatalf("setting here: %v %q", err, out)
	}

	out, err = send(zoneID, map[string]string{"zone": "new york"}, nil, "", modalRun(m.setZone))
	if err != nil || !strings.Contains(out, "✓ zone america/new_york, 04:00 there now") || !strings.Contains(out, "zone      america/new_york") {
		t.Fatalf("zone: %v %q", err, out)
	}
	if _, err = send(zoneID, map[string]string{"zone": "atlantis"}, nil, "", modalRun(m.setZone)); err == nil {
		t.Fatal("atlantis")
	}
	for q, want := range map[string]string{"Asia/Tokyo": "Asia/Tokyo", "  ": "", "kolkata": "Asia/Kolkata", "EST": "America/New_York", "budapest": "Europe/Budapest", "bucha": "Europe/Bucharest", "argentina": "America/Argentina/Buenos_Aires"} {
		if got := findZone(q); got != want {
			t.Errorf("findZone(%q) = %q, want %q", q, got, want)
		}
	}
	// outside a server
	e, _ := coretest.Modal(t, newID, map[string]string{"what": "x", "when": "in 1h"}, func(p map[string]any) { delete(p, "guild_id") })
	if err := m.create(ctx, e); err == nil {
		t.Fatal("created outside a server")
	}
}

// modalRun adapts a modal handler for send.
func modalRun[E any](f func(context.Context, E) error) func(context.Context, *Module, any) error {
	return func(ctx context.Context, _ *Module, e any) error { return f(ctx, e.(E)) }
}

func TestQuietHoursHoldADM(t *testing.T) {
	m, c, p, g, u := fresh(t)
	ctx := context.Background()
	if _, err := m.change(ctx, u, func(p *person) {
		p.tz, p.quietFrom, p.quietTo, p.snooze = "Europe/Berlin", 22*60, 7*60, []int{5, 30, tomorrow}
	}); err != nil {
		t.Fatal(err)
	}
	once := m.add(t, reminder{guild: g, owner: u, due: at(11, 2, 23, 0)})
	c.t = once.due
	m.fire(ctx, once)
	if got := m.row(t, once.id); !got.due.Equal(at(11, 3, 7, 0)) || len(p.all()) != 0 {
		t.Fatalf("held to %v, sent %d", got.due, len(p.all()))
	}
	rep := m.add(t, reminder{guild: g, owner: u, due: at(11, 3, 6, 0), r: rule{mask: everyDay, at: 360}})
	c.t = rep.due
	m.fire(ctx, rep)
	if got := m.row(t, rep.id); !got.due.Equal(at(11, 4, 6, 0)) {
		t.Fatalf("repeat moved to %v", got.due)
	}
	var copies int
	if err := m.db.QueryRow(ctx, "select count(*) from remind where owner_id = $1 and mask = 0 and due_at = $2", int64(u), at(11, 3, 7, 0)).Scan(&copies); err != nil || copies != 2 {
		t.Fatalf("copies at 07:00: %d %v", copies, err)
	}
	// a channel reminder comes anyway, and a dm outside them carries the
	// owner's snooze buttons
	m.fire(ctx, m.add(t, reminder{guild: g, owner: u, due: at(11, 3, 6, 0), target: "channel"}))
	c.t = at(11, 3, 12, 0)
	m.fire(ctx, m.add(t, reminder{guild: g, owner: u, due: c.t}))
	s := p.all()
	if len(s) != 2 {
		t.Fatalf("sent %d", len(s))
	}
	var labels []string
	for _, c := range s[1].msg.Components[0].(discord.ContainerComponent).Components {
		if row, ok := c.(discord.ActionRowComponent); ok {
			for _, b := range row.Components {
				labels = append(labels, b.(discord.ButtonComponent).Label)
			}
		}
	}
	if strings.Join(labels, ",") != "done,5m,30m,tomorrow" {
		t.Fatalf("buttons %v", labels)
	}
}

func TestPersonWords(t *testing.T) {
	for n, want := range map[int]string{5: "5m", 60: "1h", 90: "1h 30m", 480: "8h", tomorrow: "tomorrow"} {
		if got := snoozeWord(n); got != want {
			t.Errorf("%d: %q", n, got)
		}
	}
	p := person{quietFrom: 9 * 60, quietTo: 17 * 60}
	if !p.quiet(at(11, 2, 10, 0).UTC()) || p.quiet(at(11, 2, 18, 0)) {
		t.Fatal("day quiet hours in utc")
	}
	if (person{quietFrom: 60, quietTo: 60}).quietWords() != "off" || (person{quietFrom: 60, quietTo: 60}).quiet(now) {
		t.Fatal("empty quiet hours")
	}
	if got := p.quietEnd(time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2026, 11, 3, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("end %v", got)
	}
	for r, want := range map[rule]string{{}: "", {mday: 3}: "monthly", {mask: everyDay}: "daily", {mask: weekdays}: "weekdays", {mask: weekends}: "weekends", {mask: 4}: "weekly", {mask: 6}: "some days"} {
		if r.short() != want {
			t.Errorf("%+v: %q", r, r.short())
		}
	}
	if waiting(1, false) != "1 waiting" || waiting(3, false) != "3 waiting" {
		t.Fatal("waiting")
	}
	if dayWord(at(12, 25, 9, 0), now, berlin) != "fri 25 dec" {
		t.Fatal(dayWord(at(12, 25, 9, 0), now, berlin))
	}
}
