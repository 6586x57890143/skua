package pace

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/6586x57890143/cygnet/link"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

// clock starts on the hour, so the hours seen are easy to count.
var start = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

func module(on bool) (*Module, *time.Time) {
	now := start
	m := New(func(snowflake.ID) bool { return on })
	m.now = func() time.Time { return now }
	return m, &now
}

func msg(guild snowflake.ID, edit func(*discord.Message)) *events.GuildMessageCreate {
	gen := events.NewGenericEvent(nil, 0, 0)
	ev := &events.GuildMessageCreate{GenericGuildMessage: &events.GenericGuildMessage{GenericEvent: gen, GuildID: guild}}
	if edit != nil {
		edit(&ev.Message)
	}
	return ev
}

func ask(m *Module, from, body string) (Answer, error) {
	out, err := m.Answer(context.Background(), link.Call{From: from, Method: Method, Body: []byte(body)})
	if err != nil {
		return Answer{}, err
	}
	return out.(Answer), nil
}

func code(err error) link.Code {
	if le, ok := errors.AsType[*link.Error](err); ok {
		return le.Code
	}
	return ""
}

func TestPaceNowAndUsual(t *testing.T) {
	m, now := module(true)
	// Two hours at 1 a minute, then a burst of 50 in the last minute.
	for range 120 {
		m.OnEvent(msg(3, nil))
		*now = now.Add(time.Minute)
	}
	if _, err := ask(m, "cygnet", `{"guild":"3"}`); err != nil {
		t.Fatalf("after two hours: %v", err)
	}
	for range 50 {
		m.OnEvent(msg(3, nil))
	}
	a, err := ask(m, "cygnet", `{"guild":"3"}`)
	if err != nil {
		t.Fatal(err)
	}
	// The first message lands on the hour, so both hours are whole: 120
	// over 120 minutes. Now is the 50, plus the four minutes before at 1.
	if a.Usual != 1 || a.Now != (50+4)/5.0 {
		t.Fatalf("answered %+v", a)
	}
}

func TestPaceWarmsUpAndForgets(t *testing.T) {
	m, now := module(true)
	if _, err := ask(m, "cygnet", `{"guild":"3"}`); code(err) != link.Busy {
		t.Fatalf("a server never heard answered %v", err)
	}
	*now = now.Add(time.Second) // the first whole hour starts at 10:00
	m.OnEvent(msg(3, nil))
	*now = now.Add(time.Hour)
	if _, err := ask(m, "cygnet", `{"guild":"3"}`); code(err) != link.Busy {
		t.Fatalf("under a whole hour answered %v", err)
	}
	*now = now.Add(time.Hour)
	if _, err := ask(m, "cygnet", `{"guild":"3"}`); err != nil {
		t.Fatalf("after a whole hour: %v", err)
	}
	m.OnEvent(&events.GuildLeave{GenericGuild: &events.GenericGuild{GenericEvent: events.NewGenericEvent(nil, 0, 0), GuildID: 3}})
	if _, err := ask(m, "cygnet", `{"guild":"3"}`); code(err) != link.Busy {
		t.Fatal("a server left is still counted")
	}
}

func TestPaceCountsOnlyMembers(t *testing.T) {
	m, _ := module(true)
	hook := snowflake.ID(1)
	m.OnEvent(msg(3, func(d *discord.Message) { d.Author.Bot = true }))
	m.OnEvent(msg(3, func(d *discord.Message) { d.WebhookID = &hook }))
	m.OnEvent(&events.Ready{})
	if _, ok := m.guilds.Load(snowflake.ID(3)); ok {
		t.Fatal("counted a bot or a webhook")
	}
}

func TestPaceRefusals(t *testing.T) {
	m, _ := module(true)
	for _, c := range []struct {
		from, body string
		want       link.Code
	}{
		{"merlin", `{"guild":"3"}`, link.Denied},
		{"cygnet", `{"guild":"three"}`, link.Invalid},
		{"cygnet", `{`, link.Invalid},
	} {
		if _, err := ask(m, c.from, c.body); code(err) != c.want {
			t.Errorf("%s %s: %v, want %s", c.from, c.body, err, c.want)
		}
	}
	off, _ := module(false)
	off.OnEvent(msg(3, nil)) // as if counted before the switch went off
	if _, err := ask(off, "cygnet", `{"guild":"3"}`); code(err) != link.Denied {
		t.Fatalf("a server that hasn't opted in answered %v", err)
	}
	if _, ok := off.guilds.Load(snowflake.ID(3)); ok {
		t.Fatal("switched off, still holding its counts")
	}
}

func TestPaceReportAndHelp(t *testing.T) {
	m, now := module(true)
	if r := m.Report(3); len(r.Notes) != 1 || len(r.Rows) != 0 {
		t.Fatalf("cold report %+v", r)
	}
	for range 90 {
		m.OnEvent(msg(3, nil))
		*now = now.Add(time.Minute)
	}
	r := m.Report(3)
	if len(r.Rows) != 2 || r.Rows[0] != [2]string{"now", "0.8 a minute"} || r.Rows[1] != [2]string{"usual", "1.0 a minute"} {
		t.Fatalf("report %+v", r)
	}
	if off, _ := module(false); len(off.Report(3).Notes) != 0 {
		t.Fatal("a server that hasn't switched pace on is told she's listening")
	}
	h := m.Help()
	if h.Line == "" || strings.ToLower(h.About) != h.About {
		t.Fatalf("help %+v", h)
	}
	if m.Name() != Name || m.Perms() != 0 || m.Commands() != nil || m.Want().Required == 0 {
		t.Fatal("module surface changed")
	}
}
