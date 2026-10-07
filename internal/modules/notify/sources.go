package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// item is one thing an account shows: a post, or a stream while it's live.
// A stream's ID starts "live:", and it is forgotten once the stream ends,
// so the next one is news again.
type item struct {
	ID, Title, URL, Image, Author, Detail string
	// Started is when a stream began, where the platform says; zero
	// otherwise, and the card's own time stands in.
	Started time.Time
}

func (it item) live() bool { return strings.HasPrefix(it.ID, "live:") }

// source is one platform.
type source interface {
	// resolve checks an account exists and returns its key, which is what
	// is stored and polled, and the name it reads as.
	resolve(ctx context.Context, account string) (key, name string, err error)
	// check returns what each account shows now, newest first. An account
	// missing from the map failed, and the error says why.
	check(ctx context.Context, accounts []string) (map[string][]item, error)
	every() time.Duration
}

// keeper is a source that can veto an item before it is announced: one
// network call per new item, never per poll.
type keeper interface {
	keep(ctx context.Context, it item) bool
}

var errUnknown = errors.New("notify: no such account")

// statusError is a platform answering something other than 2xx or 404.
type statusError struct {
	host string
	code int
}

func (e statusError) Error() string { return fmt.Sprintf("notify: %s answered %d", e.host, e.code) }

// maxBody caps what one fetch reads: a youtube page is about 1 MB.
const maxBody = 4 << 20

// get fetches u into v (JSON), or returns the body when v is nil.
func get(ctx context.Context, c *http.Client, u string, h http.Header, v any) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range h {
		req.Header[k] = vs
	}
	return do(c, req, v)
}

func do(c *http.Client, req *http.Request, v any) ([]byte, error) {
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, errUnknown
	case res.StatusCode/100 != 2:
		return nil, statusError{req.URL.Host, res.StatusCode}
	case err != nil:
		return nil, err
	case v != nil && len(body) > 0:
		return body, json.Unmarshal(body, v)
	}
	return body, nil
}

// clean takes an account as typed: a handle with or without @, or a link
// to the profile.
func clean(account string) string {
	a := strings.TrimRight(strings.TrimSpace(account), "/")
	if i := strings.LastIndexByte(a, '/'); i >= 0 {
		a = a[i+1:]
	}
	return strings.ToLower(strings.TrimPrefix(a, "@"))
}

// handle is a cleaned account name: at most 40 of a-z, 0-9, _, . and -,
// with a letter or digit among them, so "." or ".." never become a path
// on a route skua builds from it.
var handleChars = regexp.MustCompile(`^[a-z0-9_.-]*[a-z0-9][a-z0-9_.-]*$`)

func handle(s string) bool { return len(s) <= 40 && handleChars.MatchString(s) }

// feedDoc is Atom (youtube) or RSS 2.0 (rsshub). encoding/xml matches tags
// by local name, so yt:videoId and media:group need no namespaces.
type feedDoc struct {
	Title   string  `xml:"title"`
	Entries []entry `xml:"entry"`
	Channel struct {
		Title string  `xml:"title"`
		Items []entry `xml:"item"`
	} `xml:"channel"`
}

type entry struct {
	ID      string `xml:"id"`
	GUID    string `xml:"guid"`
	VideoID string `xml:"videoId"`
	Title   string `xml:"title"`
	Links   []struct {
		Href string `xml:"href,attr"`
		Text string `xml:",chardata"`
	} `xml:"link"`
	Author string `xml:"author>name"`
	Thumb  struct {
		URL string `xml:"url,attr"`
	} `xml:"group>thumbnail"`
	Description string `xml:"description"`
}

// firstImg is the preview in an item's HTML: an image (x), or a video's
// poster (tiktok).
var firstImg = regexp.MustCompile(`<(?:img[^>]+src|video[^>]+poster)="([^"]+)"`)

// parseFeed reads a feed's name and its entries as items, newest first as
// feeds list them.
func parseFeed(body []byte) (string, []item, error) {
	var d feedDoc
	if err := xml.Unmarshal(body, &d); err != nil {
		return "", nil, fmt.Errorf("notify: reading a feed: %w", err)
	}
	name, es := d.Title, d.Entries
	if name == "" {
		name, es = d.Channel.Title, d.Channel.Items
	}
	items := make([]item, 0, len(es))
	for _, e := range es {
		it := item{ID: first(e.VideoID, e.GUID, e.ID), Title: e.Title, Author: first(e.Author, name), Image: e.Thumb.URL}
		for _, l := range e.Links {
			if it.URL = first(l.Href, strings.TrimSpace(l.Text)); it.URL != "" {
				break
			}
		}
		if it.Image == "" {
			if m := firstImg.FindStringSubmatch(e.Description); m != nil {
				it.Image = html.UnescapeString(m[1])
			}
		}
		if it.ID = first(it.ID, it.URL); it.ID != "" {
			items = append(items, it)
		}
	}
	return name, items, nil
}

func first(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// feed is x or tiktok through an RSSHub instance: path turns a handle into
// the route.
type feed struct {
	c    *http.Client
	base string
	path func(handle string) string
}

func (f feed) every() time.Duration { return 5 * time.Minute }

func (f feed) resolve(ctx context.Context, account string) (string, string, error) {
	h := clean(account)
	if !handle(h) {
		return "", "", errUnknown
	}
	if _, err := f.items(ctx, h); err != nil {
		return "", "", err
	}
	return h, "@" + h, nil
}

func (f feed) items(ctx context.Context, h string) ([]item, error) {
	body, err := get(ctx, f.c, f.base+f.path(h), nil, nil)
	if err != nil {
		return nil, err
	}
	_, items, err := parseFeed(body)
	for i := range items {
		items[i].Author = "@" + h
	}
	return items, err
}

// check fetches each feed in turn.
//
// ponytail: serial, a request per account each round; fan out if rounds
// ever outgrow every().
func (f feed) check(ctx context.Context, accounts []string) (map[string][]item, error) {
	got := map[string][]item{}
	var errs []error
	for _, a := range accounts {
		items, err := f.items(ctx, a)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a, err))
			continue
		}
		got[a] = items
	}
	return got, errors.Join(errs...)
}

// youtube reads a channel's public feed for uploads and its /live page for
// a stream. No key: both are what a logged-out browser sees.
type youtube struct {
	c    *http.Client
	base string // https://www.youtube.com
	img  string // https://i.ytimg.com
	hub  string // https://pubsubhubbub.appspot.com/subscribe

	mu     sync.Mutex
	subbed []string // the channels last asked of the hub
}

// consent skips the EU cookie wall a server's address gets instead of the
// page.
var consent = http.Header{"Cookie": {"SOCS=CAI"}, "Accept-Language": {"en"}}

var (
	channelID = regexp.MustCompile(`^UC[A-Za-z0-9_-]{22}$`)
	canonical = regexp.MustCompile(`<link rel="canonical" href="https://www\.youtube\.com/(?:channel/(UC[A-Za-z0-9_-]{22})|watch\?v=([A-Za-z0-9_-]{11}))">`)
	metaTitle = regexp.MustCompile(`<meta name="title" content="([^"]*)">`)
)

func (y *youtube) every() time.Duration { return 3 * time.Minute }

func (y *youtube) resolve(ctx context.Context, account string) (string, string, error) {
	a := strings.TrimRight(strings.TrimSpace(account), "/")
	if i := strings.LastIndexByte(a, '/'); i >= 0 {
		a = a[i+1:]
	}
	id := a
	if !channelID.MatchString(a) {
		h := strings.TrimPrefix(a, "@")
		if !handle(strings.ToLower(h)) {
			return "", "", errUnknown
		}
		page, err := get(ctx, y.c, y.base+"/@"+url.PathEscape(h), consent, nil)
		if err != nil {
			return "", "", err
		}
		m := canonical.FindSubmatch(page)
		if m == nil || len(m[1]) == 0 {
			return "", "", errUnknown
		}
		id = string(m[1])
	}
	body, err := get(ctx, y.c, y.base+"/feeds/videos.xml?channel_id="+id, nil, nil)
	if err != nil {
		return "", "", err
	}
	name, _, err := parseFeed(body)
	return id, name, err
}

func (y *youtube) check(ctx context.Context, accounts []string) (map[string][]item, error) {
	got := map[string][]item{}
	var errs []error
	for _, id := range accounts {
		items, err := y.channel(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		got[id] = items
	}
	return got, errors.Join(errs...)
}

// channel is a channel's stream, while it is live, then its uploads.
func (y *youtube) channel(ctx context.Context, id string) ([]item, error) {
	body, err := get(ctx, y.c, y.base+"/feeds/videos.xml?channel_id="+id, nil, nil)
	if err != nil {
		return nil, err
	}
	name, items, err := parseFeed(body)
	if err != nil {
		return nil, err
	}
	page, err := get(ctx, y.c, y.base+"/channel/"+id+"/live", consent, nil)
	if err != nil {
		return nil, err
	}
	// videoDetails.isLive: absent on an upcoming stream and on a channel
	// page, which is where /live lands when nothing is on.
	if !strings.Contains(string(page), `"isLive":true`) {
		return items, nil
	}
	m := canonical.FindSubmatch(page)
	if m == nil || len(m[2]) == 0 {
		return items, nil
	}
	v := string(m[2])
	live := item{ID: "live:" + v, URL: y.base + "/watch?v=" + v, Image: y.img + "/vi/" + v + "/maxresdefault_live.jpg", Author: name}
	if t := metaTitle.FindSubmatch(page); t != nil {
		live.Title = html.UnescapeString(string(t[1]))
	}
	return append([]item{live}, items...), nil
}

// keep drops a stream's own feed entry, which appears as soon as it is
// scheduled: the /live check announces it when it starts. A page that
// can't be read is kept, since an extra card beats a lost one.
func (y *youtube) keep(ctx context.Context, it item) bool {
	page, err := get(ctx, y.c, y.base+"/watch?v="+url.QueryEscape(it.ID), consent, nil)
	return err != nil || !strings.Contains(string(page), `"isLiveContent":true`)
}

// app is an OAuth client credentials token, fetched when first needed and
// again before it runs out.
type app struct {
	c               *http.Client
	url, id, secret string
	mu              sync.Mutex
	token           string
	until           time.Time
}

func (a *app) bearer(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Now().Before(a.until) {
		return a.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {a.id}, "client_secret": {a.secret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var res struct {
		Token   string `json:"access_token"`
		Expires int64  `json:"expires_in"`
	}
	if _, err := do(a.c, req, &res); err != nil {
		return "", fmt.Errorf("notify: token: %w", err)
	}
	// A minute's margin, so a token never dies mid-request.
	a.token, a.until = res.Token, time.Now().Add(time.Duration(res.Expires)*time.Second-time.Minute)
	return a.token, nil
}

// call is a GET with the app's token.
func (a *app) call(ctx context.Context, u string, h http.Header, v any) error {
	return a.send(ctx, http.MethodGet, u, h, nil, v)
}

// send is a request with the app's token, body sent as JSON when there is
// one. A 401 drops the token, so the next call fetches a fresh one.
func (a *app) send(ctx context.Context, method, u string, h http.Header, body, v any) error {
	tok, err := a.bearer(ctx)
	if err != nil {
		return err
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	for k, vs := range h {
		req.Header[k] = vs
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	_, err = do(a.c, req, v)
	if se, ok := errors.AsType[statusError](err); ok && se.code == http.StatusUnauthorized {
		a.mu.Lock()
		a.token = ""
		a.mu.Unlock()
	}
	return err
}

// batches splits accounts into runs of n, the most one call takes.
func batches(accounts []string, n int) [][]string {
	var out [][]string
	for len(accounts) > n {
		out = append(out, accounts[:n])
		accounts = accounts[n:]
	}
	return append(out, accounts)
}

// twitch is Helix with an app token: a hundred logins a call.
type twitch struct {
	app *app
	api string // https://api.twitch.tv/helix
}

func (t *twitch) every() time.Duration { return time.Minute }

func (t *twitch) header() http.Header { return http.Header{"Client-Id": {t.app.id}} }

func (t *twitch) resolve(ctx context.Context, account string) (string, string, error) {
	login := clean(account)
	if !handle(login) {
		return "", "", errUnknown
	}
	var res struct {
		Data []struct {
			Login string `json:"login"`
			Name  string `json:"display_name"`
		} `json:"data"`
	}
	if err := t.app.call(ctx, t.api+"/users?login="+url.QueryEscape(login), t.header(), &res); err != nil {
		return "", "", err
	}
	if len(res.Data) == 0 {
		return "", "", errUnknown
	}
	return res.Data[0].Login, res.Data[0].Name, nil
}

func (t *twitch) check(ctx context.Context, accounts []string) (map[string][]item, error) {
	got := map[string][]item{}
	var errs []error
	for _, b := range batches(accounts, 100) {
		q := url.Values{"user_login": b, "first": {"100"}}
		var raw struct {
			Data []struct {
				ID    string `json:"id"`
				Login string `json:"user_login"`
				Name  string `json:"user_name"`
				Game  string `json:"game_name"`
				Title string `json:"title"`
				Thumb string `json:"thumbnail_url"`
				Start string `json:"started_at"`
			} `json:"data"`
		}
		if err := t.app.call(ctx, t.api+"/streams?"+q.Encode(), t.header(), &raw); err != nil {
			errs = append(errs, err)
			continue
		}
		for _, a := range b {
			got[a] = nil
		}
		for _, s := range raw.Data {
			thumb := strings.NewReplacer("{width}", "1280", "{height}", "720").Replace(s.Thumb)
			start, _ := time.Parse(time.RFC3339, s.Start)
			got[s.Login] = []item{{
				ID: "live:" + s.ID, Title: s.Title, URL: "https://www.twitch.tv/" + s.Login,
				// The preview is cached by URL: the stream's ID makes it this stream's.
				Image: thumb + "?s=" + s.ID, Author: s.Name, Detail: s.Game, Started: start,
			}}
		}
	}
	return got, errors.Join(errs...)
}

// kick is Kick's public API with an app token: fifty slugs a call.
type kick struct {
	app *app
	api string // https://api.kick.com/public/v1
	key kickKey
}

type kickChannel struct {
	ID       int64  `json:"broadcaster_user_id"`
	Slug     string `json:"slug"`
	Title    string `json:"stream_title"`
	Category struct {
		Name string `json:"name"`
	} `json:"category"`
	Stream struct {
		Live      bool   `json:"is_live"`
		Thumbnail string `json:"thumbnail"`
		Start     string `json:"start_time"`
	} `json:"stream"`
}

func (k *kick) every() time.Duration { return time.Minute }

func (k *kick) channels(ctx context.Context, slugs []string) ([]kickChannel, error) {
	var res struct {
		Data []kickChannel `json:"data"`
	}
	err := k.app.call(ctx, k.api+"/channels?"+url.Values{"slug": slugs}.Encode(), nil, &res)
	return res.Data, err
}

func (k *kick) resolve(ctx context.Context, account string) (string, string, error) {
	slug := clean(account)
	if !handle(slug) || len(slug) > 25 {
		return "", "", errUnknown
	}
	cs, err := k.channels(ctx, []string{slug})
	if err != nil {
		return "", "", err
	}
	if len(cs) == 0 {
		return "", "", errUnknown
	}
	return cs[0].Slug, cs[0].Slug, nil
}

func (k *kick) check(ctx context.Context, accounts []string) (map[string][]item, error) {
	got := map[string][]item{}
	var errs []error
	for _, b := range batches(accounts, 50) {
		cs, err := k.channels(ctx, b)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, a := range b {
			got[a] = nil
		}
		for _, c := range cs {
			if !c.Stream.Live {
				continue
			}
			start, _ := time.Parse(time.RFC3339, c.Stream.Start)
			got[c.Slug] = []item{{
				ID: "live:" + first(c.Stream.Start, "on"), Title: c.Title, URL: "https://kick.com/" + c.Slug,
				Image: c.Stream.Thumbnail, Author: c.Slug, Detail: c.Category.Name, Started: start,
			}}
		}
	}
	return got, errors.Join(errs...)
}
