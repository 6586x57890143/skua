package forever

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Regions is every region Forever runs in, in the order skua shows them.
var Regions = []string{"us", "eu", "kr", "tw"}

func validRegion(r string) bool {
	for _, x := range Regions {
		if x == r {
			return true
		}
	}
	return false
}

// errGone is a character Blizzard doesn't answer for: under level 10, never
// logged out, renamed, or a region whose armory is down.
var errGone = errors.New("forever: character not found")

// bnet is Blizzard's API with skua's app credentials: one token for every
// region (oauth.battle.net issues them all) and one pace for every call.
type bnet struct {
	c          *http.Client
	oauth      string                     // token endpoint
	api        func(region string) string // https://<region>.api.blizzard.com
	id, secret string
	pace       <-chan time.Time // a tick a call, 100 a second

	mu    sync.Mutex
	token string
	until time.Time
}

func newBnet(c *http.Client, id, secret string) *bnet {
	return &bnet{
		c: c, id: id, secret: secret,
		oauth: "https://oauth.battle.net/token",
		api:   func(r string) string { return "https://" + r + ".api.blizzard.com" },
		// ponytail: a fixed 100/s pace, under Blizzard's 100/s and 36k/h for
		// the polls we make; a token bucket if bursts ever need more.
		pace: time.Tick(10 * time.Millisecond),
	}
}

func (b *bnet) bearer(ctx context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.token != "" && time.Now().Before(b.until) {
		return b.token, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.oauth, strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(b.id, b.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := b.c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("forever: token: %s", res.Status)
	}
	var t struct {
		Token   string `json:"access_token"`
		Expires int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&t); err != nil {
		return "", fmt.Errorf("forever: token: %w", err)
	}
	// A minute's margin, so a token never dies mid-request.
	b.token, b.until = t.Token, time.Now().Add(time.Duration(t.Expires)*time.Second-time.Minute)
	return b.token, nil
}

// get reads path in region's namespace kind (profile, dynamic) into v.
// since is the last Last-Modified seen; a 304 returns it unchanged with
// changed false and v untouched.
func (b *bnet) get(ctx context.Context, region, kind, path, since string, v any) (modified string, changed bool, err error) {
	tok, err := b.bearer(ctx)
	if err != nil {
		return since, false, err
	}
	select {
	case <-b.pace:
	case <-ctx.Done():
		return since, false, ctx.Err()
	}
	u := b.api(region) + path + "?namespace=" + kind + "-forever-" + region + "&locale=en_US"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return since, false, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if since != "" {
		req.Header.Set("If-Modified-Since", since)
	}
	res, err := b.c.Do(req)
	if err != nil {
		return since, false, err
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		return since, false, nil
	case http.StatusNotFound:
		return since, false, errGone
	case http.StatusUnauthorized:
		b.mu.Lock()
		b.token = ""
		b.mu.Unlock()
		return since, false, fmt.Errorf("forever: %s %s", region, res.Status)
	default:
		return since, false, fmt.Errorf("forever: %s %s", region, res.Status)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(v); err != nil {
		return since, false, fmt.Errorf("forever: %s %s: %w", region, path, err)
	}
	return res.Header.Get("Last-Modified"), true, nil
}

// named is a reference Blizzard hands out with its name in en_US.
type named struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// profile is the slice of a character's profile skua keeps.
type profile struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Surname string `json:"surname"`
	Level   int    `json:"level"`
	XP      int    `json:"experience"`
	Points  int    `json:"achievement_points"`
	Logout  int64  `json:"last_login_timestamp"` // ms; when its profile was last saved
	ILvl    int    `json:"equipped_item_level"`
	Class   named  `json:"character_class"`
	Race    named  `json:"race"`
	Ruleset named  `json:"ruleset"`
	Faction struct {
		Type string `json:"type"`
	} `json:"faction"`
}

// professions is a character's professions and their skill.
type professions struct {
	Primaries []struct {
		Profession named `json:"profession"`
		Tiers      []struct {
			Skill int `json:"skill_points"`
		} `json:"tiers"`
	} `json:"primaries"`
}

// media is a character's renders.
type media struct {
	Assets []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"assets"`
}

// slug is name and surname as Blizzard's path takes them.
func slug(name, surname string) string {
	return strings.ToLower(name + "-" + surname)
}

// charPath is a character's profile path, realmless as Forever is.
func charPath(slug string) string { return "/profile/wow/character/" + url.PathEscape(slug) }

// parseName reads "thrall earthcaller" or "Thrall-Earthcaller" as name and
// surname: Forever names are always two words.
func parseName(s string) (name, surname string, ok bool) {
	f := strings.FieldsFunc(strings.TrimSpace(s), func(r rune) bool { return r == ' ' || r == '-' })
	if len(f) != 2 || len(f[0]) > 24 || len(f[1]) > 24 {
		return "", "", false
	}
	for _, w := range f {
		for _, r := range w {
			if r == ':' || r == '/' || r == '?' || r == '#' || r == '%' {
				return "", "", false
			}
		}
	}
	return f[0], f[1], true
}
