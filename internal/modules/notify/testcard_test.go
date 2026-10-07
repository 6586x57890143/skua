package notify

import (
	"errors"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/guard"
)

func TestTestCard(t *testing.T) {
	f := follow{guild: 3, platform: "kick", account: "munkiki", name: "munkiki", role: 7}
	got := js(testCard(f, true))
	for _, want := range []string{"test card", "just a test", "press ping me to hear when munkiki goes live", "https://kick.com/munkiki", "notify-role:7", `"parse":[]`} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q: %s", want, got)
		}
	}
	if strings.Contains(got, "<@&7>") || strings.Contains(got, `"roles":["7"]`) {
		t.Fatalf("a test pings no one: %s", got)
	}
	if got := js(testCard(f, false)); strings.Contains(got, "notify-role") || strings.Contains(got, "ping me") {
		t.Fatalf("no button skua couldn't honour, and no line about it: %s", got)
	}
	for p, want := range map[string]string{
		"youtube": "https://www.youtube.com/channel/UCx", "twitch": "https://www.twitch.tv/UCx", "kick": "https://kick.com/UCx",
		"x": "https://x.com/UCx", "tiktok": "https://www.tiktok.com/@UCx", "fake": "",
	} {
		if got := profile(p, "UCx"); got != want {
			t.Errorf("%s: %q", p, got)
		}
	}
}

func TestPanelPostsATestCard(t *testing.T) {
	src := newFake()
	pt := newPanel(t, src)
	m := pt.m
	m.self = 2
	m.bound[3] = 9
	m.follows = []follow{{guild: 3, platform: "fake", account: "bird", name: "Bird", role: 7}}

	if got := pt.open(); !strings.Contains(got, "notify:test") {
		t.Fatalf("the panel offers a test card: %s", got)
	}
	e, _ := coretest.Button(t, "notify:test", func(p map[string]any) { p["data"] = pick("notify:test", "fake:bird:0") })
	var got []string
	e.Respond = record(&got)
	rr := &restRec{}
	e.Client().Rest = rr
	pt.r.OnComponent(e)
	if len(rr.posted) != 1 || rr.posted[0].channel != 9 {
		t.Fatalf("posted into the server's channel: %+v", rr.posted)
	}
	if card := js(rr.posted[0].msg); !strings.Contains(card, "notify-role:7") {
		t.Fatalf("with ping me: %s", card)
	}
	if note := read(got, rr); !strings.Contains(note, "✓ test card for Bird posted in <#9>") {
		t.Fatal(note)
	}

	for name, c := range map[string]struct {
		setup func(*restRec)
		value string
		want  string
	}{
		"gone":       {func(*restRec) {}, "fake:nobody:0", "that follow is gone"},
		"none":       {func(*restRec) {}, "", "pick one follow"},
		"post fails": {func(r *restRec) { r.postErr = errors.New("boom") }, "fake:bird:0", "discord refused the post"},
		"refused":    {func(r *restRec) { r.postErr = &rest.Error{} }, "fake:bird:0", "discord refused the post"},
	} {
		e, _ := coretest.Button(t, "notify:test", func(p map[string]any) {
			vals := []string{}
			if c.value != "" {
				vals = append(vals, c.value)
			}
			p["data"] = pick("notify:test", vals...)
		})
		var got []string
		e.Respond = record(&got)
		rr := &restRec{}
		c.setup(rr)
		e.Client().Rest = rr
		pt.r.OnComponent(e)
		if out := read(got, rr); !strings.Contains(out, c.want) {
			t.Errorf("%s: %s", name, out)
		}
	}

	m.bound = map[snowflake.ID]snowflake.ID{}
	if out := pt.click(pick("notify:test", "fake:bird:0")); !strings.Contains(out, "pick where cards go first") {
		t.Fatal(out)
	}
	m.bound[3] = 9
	g := guard.New()
	for g.Allow(3, guard.MessageSend) == nil {
	}
	m.guard = g
	e, _ = coretest.Button(t, "notify:test", func(p map[string]any) { p["data"] = pick("notify:test", "fake:bird:0") })
	got = nil
	e.Respond = record(&got)
	rr = &restRec{}
	e.Client().Rest = rr
	pt.r.OnComponent(e)
	if out := read(got, rr); !strings.Contains(out, "too many cards here this hour") {
		t.Fatal(out)
	}
}
