package forever

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// Linking battle.net: a member presses link, signs in on Blizzard's page,
// and Blizzard sends them back to skua's hooks address with a code. skua
// reads the account's Forever characters with it and shows them on a page
// that names who and where they'll be linked to; nothing is linked until
// whoever signed in confirms there. So a sign-in link sent to someone else
// can't take their characters without them seeing it, and the latest
// sign-in with an account always wins, so an owner can take theirs back.
// The code and the token it buys are used once and dropped.

// linkTTL is how long a sign-in link, and the page it ends on, stay good.
const linkTTL = 10 * time.Minute

// ask is who pressed link, and where, by what the page names them.
type ask struct {
	guild, user     snowflake.ID
	who, where      string
	until           time.Time
	chars           []found // read from the account, on the confirm page
	failed, regions int     // regions Blizzard didn't answer, of those read
}

// found is a character on the member's account.
type found struct{ region, name, surname string }

// link is the battle.net sign-in: where Blizzard sends members back, and
// the sign-ins under way, by the one-use nonce each carries.
type link struct {
	hooks     string
	authorize string // Blizzard's sign-in page
	token     string // where a code becomes a token

	mu      sync.Mutex
	asks    map[string]ask // state -> who asked, until Blizzard sends them back
	confirm map[string]ask // confirm page -> what it offers, until pressed
}

func (l *link) on() bool { return l != nil && l.hooks != "" }

func (l *link) redirect() string { return l.hooks + "/forever/callback" }

func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// put keeps a under a fresh nonce in into, dropping what has expired.
func (l *link) put(into map[string]ask, a ask, now time.Time) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, v := range into {
		if now.After(v.until) {
			delete(into, k)
		}
	}
	n := nonce()
	into[n] = a
	return n
}

// take spends n from from, once, if it hasn't expired.
func (l *link) take(from map[string]ask, n string, now time.Time) (ask, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := from[n]
	delete(from, n)
	return a, ok && !now.After(a.until)
}

// linkCard answers the link button: the sign-in, for the member's eyes.
func (m *Module) linkCard(e *events.ComponentInteractionCreate, guild snowflake.ID) error {
	if !m.link.on() {
		return core.Tell("battle.net sign-in isn't set up on this skua")
	}
	where := "this server"
	if c := e.Client(); c != nil && c.Caches != nil {
		if g, ok := c.Caches.Guild(guild); ok && g.Name != "" {
			where = g.Name
		}
	}
	state := m.link.put(m.link.asks, ask{guild: guild, user: e.User().ID, who: e.User().EffectiveName(), where: where, until: m.now().Add(linkTTL)}, m.now())
	q := url.Values{
		"client_id": {m.api.id}, "redirect_uri": {m.link.redirect()}, "response_type": {"code"},
		"scope": {"wow.profile"}, "state": {state},
	}
	signIn := discord.NewLinkButton("sign in to battle.net", m.link.authorize+"?"+q.Encode())
	signIn.Emoji = brand.ComponentEmoji("fv_link")
	body := "sign in and skua shows the forever characters on your battle.net account, in every region, and links them to you here once you confirm\n-# the link is yours alone and works once, for 10 minutes. skua reads your characters with it and keeps nothing else"
	return e.CreateMessage(v2(brand.Card(brand.ColorNotice, Name, "battle.net", body, discord.NewActionRow(signIn)), true))
}

// Hooks is the handler for forever's paths on SKUA_HOOKS_ADDR.
func (m *Module) Hooks() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /forever/callback", m.callback)
	mux.HandleFunc("POST /forever/confirm", m.confirmed)
	return mux
}

// Linking is whether battle.net sign-in is set up, so main serves Hooks.
func (m *Module) Linking() bool { return m.link.on() }

// page is what a member reads in their browser: lines of text, and a form
// when there's something to confirm. Whatever comes from outside is
// escaped before it gets here.
func page(w http.ResponseWriter, status int, form string, lines ...string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("<p>" + l + "</p>")
	}
	_, _ = fmt.Fprintf(w, `<!doctype html><meta name="viewport" content="width=device-width"><title>skua</title><body style="background:#1e2228;color:#dbdee1;font:16px/1.5 system-ui;display:grid;place-items:center;min-height:90vh;margin:0"><main style="max-width:34em;padding:0 16px">%s%s</main>`, b.String(), form)
}

// callback is where Blizzard sends a member back: the code becomes a token,
// the token their characters, and the characters a page to confirm.
func (m *Module) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a, ok := m.link.take(m.link.asks, q.Get("state"), m.now())
	if !ok {
		page(w, http.StatusBadRequest, "", "this sign-in link has expired or was already used. press link battle.net in /forever me for a new one")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		page(w, http.StatusOK, "", "battle.net sign-in was cancelled, so nothing changed. you can close this tab")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	tok, err := m.exchange(ctx, q.Get("code"))
	if err != nil {
		m.log.Warn("forever: battle.net sign-in", "err", err)
		page(w, http.StatusBadGateway, "", "battle.net didn't let skua read your characters. press link battle.net again in a moment")
		return
	}
	a.chars, a.failed, a.regions = m.account(ctx, tok)
	if a.failed == a.regions {
		page(w, http.StatusBadGateway, "", "blizzard's armory didn't answer for any region. press link battle.net again in a moment")
		return
	}
	if len(a.chars) == 0 {
		page(w, http.StatusOK, "", "this battle.net account has no forever characters skua can see yet. you can close this tab")
		return
	}
	a.until = m.now().Add(linkTTL)
	id := m.link.put(m.link.confirm, a, m.now())
	var names []string
	for _, c := range a.chars {
		names = append(names, html.EscapeString(c.name+" "+c.surname)+" <small>"+c.region+"</small>")
	}
	lines := []string{
		fmt.Sprintf("link these %d forever characters to <b>%s</b> in <b>%s</b>?", len(a.chars), html.EscapeString(a.who), html.EscapeString(a.where)),
		strings.Join(names, "<br>"),
		"only confirm if that's you. if someone sent you this link, close this tab and nothing changes",
	}
	if a.failed > 0 {
		lines = append(lines, fmt.Sprintf("blizzard didn't answer for %d of the regions, so some characters may be missing; link again later to add them", a.failed))
	}
	form := `<form method="post" action="/forever/confirm"><input type="hidden" name="id" value="` + id + `"><button style="font:inherit;padding:8px 20px;background:#5865f2;color:#fff;border:0;border-radius:4px">link them to me</button></form>`
	page(w, http.StatusOK, form, lines...)
}

// confirmed links the characters a confirm page offered, as the member's
// and verified, gives them the forever role and says how it went.
func (m *Module) confirmed(w http.ResponseWriter, r *http.Request) {
	a, ok := m.link.take(m.link.confirm, r.PostFormValue("id"), m.now())
	if !ok {
		page(w, http.StatusBadRequest, "", "this page has expired or was already used. press link battle.net in /forever me for a new one")
		return
	}
	if !m.on(a.guild) {
		page(w, http.StatusOK, "", "forever is switched off in that server now, so nothing was linked")
		return
	}
	// The page has been pressed: the links go through even if the tab is
	// closed or the tunnel drops before they're done.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer cancel()
	added := 0
	why := map[string]int{}
	var displaced []snowflake.ID
	for _, c := range a.chars {
		if m.full(a.guild, a.user, c) {
			why["full"]++
			continue
		}
		res, err := m.list(ctx, a.guild, a.user, c.region, c.name, c.surname, true)
		if err != nil {
			why[reason(err)]++
			continue
		}
		added++
		displaced = append(displaced, res.displaced...)
	}
	m.mu.Lock()
	p := m.rest
	m.mu.Unlock()
	if p != nil && added > 0 {
		m.grant(ctx, p, a.guild, a.user)
		for _, u := range displaced {
			m.ungrant(ctx, p, a.guild, u)
		}
	}
	lines := []string{fmt.Sprintf("skua linked %d of your forever characters to you in %s. you can close this tab and run /forever me", added, html.EscapeString(a.where))}
	for _, k := range []string{"armory", "full", "other"} {
		if n := why[k]; n > 0 {
			lines = append(lines, fmt.Sprintf("%d %s", n, map[string]string{
				"armory": "aren't in blizzard's armory yet: a character shows from level 10, once it has logged out",
				"full":   "didn't fit: you or this server already list as many characters as skua keeps",
				"other":  "couldn't be read from blizzard just now; link again later to add them",
			}[k]))
		}
	}
	page(w, http.StatusOK, "", lines...)
}

// full is whether the member already lists as many characters here as
// skua keeps, and c isn't one of them: so its profile needn't be fetched.
func (m *Module) full(guild, user snowflake.ID, c found) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, cl := range m.claims {
		if cl.guild != guild || cl.user != user {
			continue
		}
		if ch := m.chars[cl.k]; ch != nil && ch.region == c.region && ch.slug() == slug(c.name, c.surname) {
			return false
		}
		n++
	}
	return n >= perMember
}

// reason is which line of the page a failed add belongs on.
func reason(err error) string {
	var t core.Tell
	switch {
	case errors.As(err, &t) && strings.Contains(string(t), "level 10"):
		return "armory"
	case errors.As(err, &t):
		return "full"
	}
	return "other"
}

// exchange trades the sign-in's code for the member's token.
func (m *Module) exchange(ctx context.Context, code string) (string, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {m.link.redirect()}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.link.token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(m.api.id, m.api.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := m.api.c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("forever: code exchange: %s", res.Status)
	}
	var t struct {
		Token string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&t); err != nil {
		return "", fmt.Errorf("forever: code exchange: %w", err)
	}
	if t.Token == "" {
		return "", errors.New("forever: code exchange: no access_token")
	}
	return t.Token, nil
}

// account is the slice of a battle.net account summary skua reads.
type account struct {
	Accounts []struct {
		Characters []struct {
			Name    string `json:"name"`
			Surname string `json:"surname"`
		} `json:"characters"`
	} `json:"wow_accounts"`
}

// account is every Forever character on the member's account, and how many
// regions Blizzard failed to answer for: a region with no Forever account
// answers 404, which is no failure.
func (m *Module) account(ctx context.Context, tok string) (chars []found, failed, regions int) {
	for _, region := range Regions {
		regions++
		var acc account
		err := m.api.getAs(ctx, tok, region, "/profile/user/wow", &acc)
		if errors.Is(err, errGone) {
			continue
		}
		if err != nil {
			m.log.Warn("forever: reading a battle.net account", "region", region, "err", err)
			failed++
			continue
		}
		for _, a := range acc.Accounts {
			for _, c := range a.Characters {
				if c.Name != "" && c.Surname != "" {
					chars = append(chars, found{region, c.Name, c.Surname})
				}
			}
		}
	}
	return chars, failed, regions
}
