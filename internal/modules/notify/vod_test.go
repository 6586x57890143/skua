package notify

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// vodFake is a platform that also finds VODs.
type vodFake struct {
	*fake
	found vod
	ok    bool
}

func (v *vodFake) vod(context.Context, string, item) (vod, bool) { return v.found, v.ok }

func TestAStreamsEndTurnsItsCardIntoTheVOD(t *testing.T) {
	src := newFake()
	m := module(t, src)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.follows = []follow{
		{guild: 1, channel: 10, platform: "fake", account: "bird", name: "Bird", role: 7},
		{guild: 2, channel: 20, platform: "fake", account: "bird", name: "Bird"},
	}
	p := &poster{}
	ctx := context.Background()
	stream := item{ID: "live:1", Title: "on air", URL: "https://p/live1", Author: "Bird", Detail: "IRL", Started: now.Add(-3*time.Hour - 12*time.Minute)}

	src.show("bird")
	m.poll(ctx, p, "fake", src)
	src.show("bird", stream)
	m.poll(ctx, p, "fake", src)
	if len(p.sent) != 2 || len(m.live[stream_("bird", "live:1")]) != 2 {
		t.Fatalf("two live cards kept: %d sent, %+v", len(p.sent), m.live)
	}
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 0 {
		t.Fatal("still live: nothing to edit")
	}

	src.show("bird")
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 2 || len(m.live) != 0 {
		t.Fatalf("both cards edited and forgotten: %+v %+v", p.edits, m.live)
	}
	e := p.edits[0]
	if e.channel != 10 || e.message != 1 {
		t.Fatalf("edited the card it posted: %+v", e)
	}
	got := js(e.msg)
	for _, want := range []string{"was live on fake", "IRL · 3h 12m", `"label":"vod"`, "https://p/live1", "<@&7>", `"parse":[]`} {
		if !strings.Contains(got, want) {
			t.Errorf("the ended card has no %q: %s", want, got)
		}
	}
	if strings.Contains(got, `"roles":["7"]`) {
		t.Error("an edit never pings")
	}

	// Ending again finds nothing more to do.
	m.poll(ctx, p, "fake", src)
	if len(p.edits) != 2 {
		t.Fatal("edited twice")
	}
}

func stream_(account, id string) stream { return stream{key{"fake", account}, id} }

func TestTheVODComesFromThePlatformWhenItHasOne(t *testing.T) {
	vf := &vodFake{fake: newFake(), found: vod{url: "https://v/1", image: "https://v/1.jpg", length: 90 * time.Minute}, ok: true}
	m := module(t, vf.fake)
	m.sources = map[string]source{"fake": vf}
	m.follows = []follow{{guild: 1, channel: 10, platform: "fake", account: "bird", name: "Bird"}}
	p := &poster{}
	ctx := context.Background()
	m.keepLive(ctx, posted{k: key{"fake", "bird"}, guild: 1, channel: 10, message: 5, it: item{ID: "live:9", Title: "t", URL: "https://p/9"}, at: m.now()})
	m.end(ctx, p, key{"fake", "bird"}, "live:9", vf)
	got := js(p.edits[0].msg)
	if !strings.Contains(got, "https://v/1") || !strings.Contains(got, "https://v/1.jpg") || !strings.Contains(got, "1h 30m") {
		t.Fatal(got)
	}

	// A server with notify off keeps its card as it was; a failed edit is
	// logged and forgotten.
	m.keepLive(ctx, posted{k: key{"fake", "bird"}, guild: 1, channel: 10, message: 6, it: item{ID: "live:10"}, at: m.now()})
	m.Gate(func(snowflake.ID) bool { return false })
	m.end(ctx, p, key{"fake", "bird"}, "live:10", vf)
	m.Gate(func(snowflake.ID) bool { return true })
	m.keepLive(ctx, posted{k: key{"fake", "bird"}, guild: 1, channel: 10, message: 7, it: item{ID: "live:11"}, at: m.now()})
	p.editErr = errors.New("gone")
	m.end(ctx, p, key{"fake", "bird"}, "live:11", vf)
	if len(p.edits) != 1 || len(m.live) != 0 {
		t.Fatalf("%d edits, %v", len(p.edits), m.live)
	}
}

func TestFallback(t *testing.T) {
	m := module(t, newFake())
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	posted := now.Add(-45 * time.Minute)
	for _, c := range []struct {
		platform   string
		it         item
		url, image string
		length     time.Duration
	}{
		{"twitch", item{URL: "https://www.twitch.tv/bird", Started: now.Add(-2 * time.Hour)}, "https://www.twitch.tv/bird/videos", "", 2 * time.Hour},
		{"kick", item{URL: "https://kick.com/bird", Image: "https://k/t.jpg"}, "https://kick.com/bird/videos", "", 45 * time.Minute},
		{"youtube", item{URL: "https://y/watch?v=a", Image: "https://i/vi/a/maxresdefault_live.jpg"}, "https://y/watch?v=a", "https://i/vi/a/maxresdefault.jpg", 45 * time.Minute},
	} {
		v := m.fallback(key{c.platform, "bird"}, c.it, posted)
		if v.url != c.url || v.image != c.image || v.length != c.length {
			t.Errorf("%s: %+v", c.platform, v)
		}
	}
	if got := js(ended(key{"kick", "bird"}, item{Title: "t"}, vod{url: "https://kick.com/bird/videos", length: time.Hour}, 0, false)); !strings.Contains(got, `"label":"videos"`) || !strings.Contains(got, "1h 0m") {
		t.Fatal(got)
	}
}

func TestTwitchVOD(t *testing.T) {
	var tokens atomic.Int32
	var noUser atomic.Bool
	srv := fakeAPI(t, &tokens, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users":
			if noUser.Load() {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"42"}]}`))
		case "/videos":
			if r.URL.Query().Get("user_id") != "42" || r.URL.Query().Get("type") != "archive" {
				t.Errorf("videos %v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"data":[
				{"stream_id":"8","url":"https://www.twitch.tv/videos/old","duration":"1h"},
				{"stream_id":"9","url":"https://www.twitch.tv/videos/1","duration":"3h8m33s","thumbnail_url":"https://t/%{width}x%{height}.jpg"},
				{"stream_id":"10","url":"https://www.twitch.tv/videos/bad","duration":"long"}]}`))
		}
	})
	tw := &twitch{app: &app{c: srv.Client(), url: srv.URL + "/token", id: "id", secret: "s"}, api: srv.URL}
	ctx := context.Background()
	v, ok := tw.vod(ctx, "bird", item{ID: "live:9"})
	if !ok || v.url != "https://www.twitch.tv/videos/1" || v.image != "https://t/1280x720.jpg" || v.length != 3*time.Hour+8*time.Minute+33*time.Second {
		t.Fatalf("%+v %v", v, ok)
	}
	for _, id := range []string{"live:7", "live:10"} {
		if _, ok := tw.vod(ctx, "bird", item{ID: id}); ok {
			t.Errorf("%s: no archive, or one with no length, is none", id)
		}
	}
	noUser.Store(true)
	if _, ok := tw.vod(ctx, "bird", item{ID: "live:9"}); ok {
		t.Error("no user, no vod")
	}
	broken := &twitch{app: &app{c: srv.Client(), url: srv.URL + "/token", id: "id", secret: "wrong"}, api: srv.URL}
	if _, ok := broken.vod(ctx, "bird", item{ID: "live:9"}); ok {
		t.Error("a refused token, no vod")
	}
}

func TestForgettingDropsLiveCards(t *testing.T) {
	m := module(t, newFake())
	ctx := context.Background()
	m.follows = []follow{{guild: 1, platform: "fake", account: "a"}, {guild: 2, platform: "fake", account: "b"}}
	m.keepLive(ctx, posted{k: key{"fake", "a"}, guild: 1, message: 1, it: item{ID: "live:1"}})
	m.keepLive(ctx, posted{k: key{"fake", "b"}, guild: 2, message: 2, it: item{ID: "live:2"}})
	m.keepLive(ctx, posted{k: key{"fake", "b"}, guild: 3, message: 3, it: item{ID: "live:2"}})
	m.leave(1)
	if _, ok := m.live[stream_("a", "live:1")]; ok {
		t.Fatal("an account nobody follows keeps no live cards")
	}
	m.leave(2)
	if cards := m.live[stream_("b", "live:2")]; len(cards) != 0 {
		t.Fatalf("leaving a server drops its cards: %+v", cards)
	}
}
