package notify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const atom = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015" xmlns:media="http://search.yahoo.com/mrss/" xmlns="http://www.w3.org/2005/Atom">
 <title>Bird Channel</title>
 <entry>
  <id>yt:video:vid00000002</id>
  <yt:videoId>vid00000002</yt:videoId>
  <title>second &amp; newest</title>
  <link rel="alternate" href="https://www.youtube.com/watch?v=vid00000002"/>
  <author><name>Bird Channel</name></author>
  <media:group><media:thumbnail url="https://i.ytimg.com/vi/vid00000002/hqdefault.jpg" width="480" height="360"/></media:group>
 </entry>
 <entry>
  <id>yt:video:vid00000001</id>
  <yt:videoId>vid00000001</yt:videoId>
  <title>first</title>
  <link rel="alternate" href="https://www.youtube.com/watch?v=vid00000001"/>
 </entry>
</feed>`

const rss = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
 <title>Twitter @bird</title>
 <item>
  <title>a post</title>
  <description>&lt;p&gt;a post&lt;/p&gt;&lt;img src=&quot;https://pbs.twimg.com/media/a.jpg?x=1&amp;amp;y=2&quot;&gt;</description>
  <link>https://x.com/bird/status/2</link>
  <guid isPermaLink="false">https://x.com/bird/status/2</guid>
 </item>
 <item>
  <title>no guid</title>
  <description>&lt;video controls="" poster=&quot;https://p16/poster.jpg&quot;&gt;</description>
  <link>https://x.com/bird/status/1</link>
 </item>
</channel></rss>`

func TestParseFeedReadsAtomAndRSS(t *testing.T) {
	name, items, err := parseFeed([]byte(atom))
	if err != nil || name != "Bird Channel" || len(items) != 2 {
		t.Fatalf("atom: %q %v %v", name, items, err)
	}
	want := item{ID: "vid00000002", Title: "second & newest", URL: "https://www.youtube.com/watch?v=vid00000002", Image: "https://i.ytimg.com/vi/vid00000002/hqdefault.jpg", Author: "Bird Channel"}
	if items[0] != want {
		t.Fatalf("atom entry:\n got %+v\nwant %+v", items[0], want)
	}
	if items[1].Author != "Bird Channel" {
		t.Fatalf("an entry without an author takes the feed's: %+v", items[1])
	}

	name, items, err = parseFeed([]byte(rss))
	if err != nil || name != "Twitter @bird" || len(items) != 2 {
		t.Fatalf("rss: %q %v %v", name, items, err)
	}
	if items[0].ID != "https://x.com/bird/status/2" || items[0].URL != "https://x.com/bird/status/2" || items[0].Image != "https://pbs.twimg.com/media/a.jpg?x=1&y=2" {
		t.Fatalf("rss item: %+v", items[0])
	}
	if items[1].ID != "https://x.com/bird/status/1" || items[1].Image != "https://p16/poster.jpg" {
		t.Fatalf("an item without a guid is its link: %+v", items[1])
	}

	if _, _, err := parseFeed([]byte("<html")); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"@Bird":                       "bird",
		" bird ":                      "bird",
		"https://x.com/Bird/":         "bird",
		"https://www.tiktok.com/@a.b": "a.b",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandle(t *testing.T) {
	for _, ok := range []string{"bird", "a.b", "_x_", "a-1", strings.Repeat("a", 40)} {
		if !handle(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "._-", "a/b", "a b", "A", strings.Repeat("a", 41)} {
		if handle(bad) {
			t.Errorf("%q taken", bad)
		}
	}
}

func TestBatches(t *testing.T) {
	got := batches([]string{"a", "b", "c", "d", "e"}, 2)
	if len(got) != 3 || len(got[2]) != 1 {
		t.Fatalf("%v", got)
	}
	if got := batches([]string{"a"}, 2); len(got) != 1 {
		t.Fatalf("%v", got)
	}
}

func TestFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/twitter/user/bird":
			_, _ = w.Write([]byte(rss))
		case "/twitter/user/broken":
			w.WriteHeader(http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := feed{c: srv.Client(), base: srv.URL, path: func(h string) string { return "/twitter/user/" + h }}
	ctx := context.Background()

	key, name, err := f.resolve(ctx, "https://x.com/Bird")
	if err != nil || key != "bird" || name != "@bird" {
		t.Fatalf("resolve: %q %q %v", key, name, err)
	}
	if _, _, err := f.resolve(ctx, "nobody"); !errors.Is(err, errUnknown) {
		t.Fatalf("a missing account: %v", err)
	}
	if _, _, err := f.resolve(ctx, "no spaces"); !errors.Is(err, errUnknown) {
		t.Fatalf("a bad handle: %v", err)
	}

	got, err := f.check(ctx, []string{"bird", "broken"})
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("a broken account is named: %v", err)
	}
	if _, ok := got["broken"]; ok {
		t.Fatal("a failed account is left out")
	}
	if len(got["bird"]) != 2 || got["bird"][0].Author != "@bird" {
		t.Fatalf("%+v", got["bird"])
	}
	if f.every() <= 0 {
		t.Fatal("every")
	}
}

const ytID = "UCaaaaaaaaaaaaaaaaaaaaaa"

func ytServer(t *testing.T, live *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "" && r.URL.Path != "/feeds/videos.xml" {
			t.Errorf("%s without the consent cookie", r.URL)
		}
		switch {
		case r.URL.Path == "/@bird":
			_, _ = w.Write([]byte(`<html><link rel="canonical" href="https://www.youtube.com/channel/` + ytID + `">`))
		case r.URL.Path == "/@nocanon":
			_, _ = w.Write([]byte(`<html>`))
		case r.URL.Path == "/feeds/videos.xml" && r.URL.Query().Get("channel_id") == ytID:
			_, _ = w.Write([]byte(atom))
		case r.URL.Path == "/channel/"+ytID+"/live":
			if live.Load() {
				_, _ = w.Write([]byte(`<link rel="canonical" href="https://www.youtube.com/watch?v=livevideo01"><meta name="title" content="on air &amp; loud">"isLive":true`))
				return
			}
			_, _ = w.Write([]byte(`<link rel="canonical" href="https://www.youtube.com/channel/` + ytID + `">`))
		case r.URL.Path == "/watch":
			if r.URL.Query().Get("v") == "streamvideo" {
				_, _ = w.Write([]byte(`"isLiveContent":true`))
				return
			}
			_, _ = w.Write([]byte(`"isLiveContent":false`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestYoutube(t *testing.T) {
	var live atomic.Bool
	srv := ytServer(t, &live)
	y := youtube{c: srv.Client(), base: srv.URL, img: "https://img"}
	ctx := context.Background()

	for _, in := range []string{"@bird", srv.URL + "/@bird", ytID} {
		id, name, err := y.resolve(ctx, in)
		if err != nil || id != ytID || name != "Bird Channel" {
			t.Fatalf("resolve(%q): %q %q %v", in, id, name, err)
		}
	}
	for _, in := range []string{"@nobody", "@nocanon", "bad handle!"} {
		if _, _, err := y.resolve(ctx, in); !errors.Is(err, errUnknown) {
			t.Fatalf("resolve(%q): %v", in, err)
		}
	}

	got, err := y.check(ctx, []string{ytID})
	if err != nil || len(got[ytID]) != 2 {
		t.Fatalf("offline: %v %v", got, err)
	}
	live.Store(true)
	got, err = y.check(ctx, []string{ytID, "UCbbbbbbbbbbbbbbbbbbbbbb"})
	if err == nil {
		t.Fatal("a missing channel is an error")
	}
	want := item{ID: "live:livevideo01", Title: "on air & loud", URL: srv.URL + "/watch?v=livevideo01", Image: "https://img/vi/livevideo01/maxresdefault_live.jpg", Author: "Bird Channel"}
	if len(got[ytID]) != 3 || got[ytID][0] != want {
		t.Fatalf("live:\n got %+v\nwant %+v", got[ytID], want)
	}

	if y.keep(ctx, item{ID: "streamvideo"}) {
		t.Fatal("a stream's own entry is kept")
	}
	if !y.keep(ctx, item{ID: "vid00000002"}) {
		t.Fatal("an upload is dropped")
	}
	if y.every() <= 0 {
		t.Fatal("every")
	}
}

// fakeAPI is twitch's and kick's token endpoint and API in one, counting
// tokens handed out.
func fakeAPI(t *testing.T, tokens *atomic.Int32, api http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.Method != http.MethodPost || r.FormValue("grant_type") != "client_credentials" || r.FormValue("client_secret") != "s" {
				http.Error(w, "bad", http.StatusBadRequest)
				return
			}
			tokens.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		api(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTwitch(t *testing.T) {
	var tokens atomic.Int32
	var reject atomic.Bool
	srv := fakeAPI(t, &tokens, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") != "id" {
			t.Errorf("no Client-Id on %s", r.URL)
		}
		if reject.Swap(false) {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/users":
			if r.URL.Query().Get("login") == "bird" {
				_, _ = w.Write([]byte(`{"data":[{"login":"bird","display_name":"Bird"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/streams":
			if got := r.URL.Query()["user_login"]; len(got) != 2 {
				t.Errorf("logins %v", got)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"9","user_login":"bird","user_name":"Bird","game_name":"Just Chatting","title":"hi","thumbnail_url":"https://t/{width}x{height}.jpg","started_at":"2026-10-07T09:00:00Z"}]}`))
		}
	})
	tw := &twitch{app: &app{c: srv.Client(), url: srv.URL + "/token", id: "id", secret: "s"}, api: srv.URL}
	ctx := context.Background()

	if key, name, err := tw.resolve(ctx, "@Bird"); err != nil || key != "bird" || name != "Bird" {
		t.Fatalf("resolve: %q %q %v", key, name, err)
	}
	if _, _, err := tw.resolve(ctx, "nobody"); !errors.Is(err, errUnknown) {
		t.Fatalf("nobody: %v", err)
	}
	if _, _, err := tw.resolve(ctx, "no way"); !errors.Is(err, errUnknown) {
		t.Fatalf("bad login: %v", err)
	}
	got, err := tw.check(ctx, []string{"bird", "quiet"})
	if err != nil {
		t.Fatal(err)
	}
	want := item{ID: "live:9", Title: "hi", URL: "https://www.twitch.tv/bird", Image: "https://t/1280x720.jpg?s=9", Author: "Bird", Detail: "Just Chatting", Started: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}
	if len(got["bird"]) != 1 || got["bird"][0] != want {
		t.Fatalf("live:\n got %+v\nwant %+v", got["bird"], want)
	}
	if items, ok := got["quiet"]; !ok || items != nil {
		t.Fatalf("an offline account is present and empty: %v %v", items, ok)
	}
	if tokens.Load() != 1 {
		t.Fatalf("the token is reused: %d fetched", tokens.Load())
	}

	// A 401 drops the token, and the next call fetches another.
	reject.Store(true)
	if _, err := tw.check(ctx, []string{"bird", "quiet"}); err == nil {
		t.Fatal("a 401 is an error")
	}
	if _, err := tw.check(ctx, []string{"bird", "quiet"}); err != nil || tokens.Load() != 2 {
		t.Fatalf("after a 401: %v, %d tokens", err, tokens.Load())
	}
	if tw.every() <= 0 {
		t.Fatal("every")
	}

	bad := &twitch{app: &app{c: srv.Client(), url: srv.URL + "/token", id: "id", secret: "wrong"}, api: srv.URL}
	if _, err := bad.check(ctx, []string{"bird"}); err == nil {
		t.Fatal("a refused token is an error")
	}
}

func TestKick(t *testing.T) {
	var tokens atomic.Int32
	srv := fakeAPI(t, &tokens, func(w http.ResponseWriter, r *http.Request) {
		slugs := r.URL.Query()["slug"]
		if slugs[0] == "nobody" {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[
			{"slug":"bird","stream_title":"flying","category":{"name":"IRL"},"stream":{"is_live":true,"thumbnail":"https://k/t.jpg","start_time":"2026-10-07T10:00:00Z"}},
			{"slug":"quiet","stream":{"is_live":false}}]}`))
	})
	k := &kick{app: &app{c: srv.Client(), url: srv.URL + "/token", id: "id", secret: "s"}, api: srv.URL}
	ctx := context.Background()

	if key, name, err := k.resolve(ctx, "https://kick.com/Bird"); err != nil || key != "bird" || name != "bird" {
		t.Fatalf("resolve: %q %q %v", key, name, err)
	}
	if _, _, err := k.resolve(ctx, "nobody"); !errors.Is(err, errUnknown) {
		t.Fatalf("nobody: %v", err)
	}
	if _, _, err := k.resolve(ctx, strings.Repeat("a", 26)); !errors.Is(err, errUnknown) {
		t.Fatalf("too long: %v", err)
	}
	got, err := k.check(ctx, []string{"bird", "quiet"})
	if err != nil {
		t.Fatal(err)
	}
	want := item{ID: "live:2026-10-07T10:00:00Z", Title: "flying", URL: "https://kick.com/bird", Image: "https://k/t.jpg", Author: "bird", Detail: "IRL", Started: time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)}
	if len(got["bird"]) != 1 || got["bird"][0] != want {
		t.Fatalf("live:\n got %+v\nwant %+v", got["bird"], want)
	}
	if items, ok := got["quiet"]; !ok || items != nil {
		t.Fatalf("offline: %v %v", items, ok)
	}
	if k.every() != time.Minute {
		t.Fatal("every")
	}
}

func TestSourcesFollowConfig(t *testing.T) {
	s := sources(Config{}, http.DefaultClient)
	if len(s) != 1 || s["youtube"] == nil {
		t.Fatalf("youtube alone without settings: %v", s)
	}
	s = sources(Config{RSSHub: "https://hub", TwitchID: "a", TwitchSecret: "b", KickID: "c", KickSecret: "d"}, http.DefaultClient)
	if len(s) != 5 {
		t.Fatalf("all five: %v", s)
	}
	if p := s["tiktok"].(feed).path("bird"); p != "/tiktok/user/@bird" {
		t.Fatal(p)
	}
	if p := s["x"].(feed).path("bird"); p != "/twitter/user/bird" {
		t.Fatal(p)
	}
	t.Setenv("SKUA_NOTIFY_RSSHUB", "https://hub/")
	if c := FromEnv(); c.RSSHub != "https://hub" {
		t.Fatal(c.RSSHub)
	}
}
