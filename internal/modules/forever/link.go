package forever

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// Linking battle.net: a member signs in on Blizzard's page, Blizzard sends
// them back to skua's hooks address with a code, and skua reads their
// Forever characters in every region with it, listing each as theirs and
// verified. The code and the token it buys are used once and dropped.

// linkTTL is how long a sign-in link stays good.
const linkTTL = 10 * time.Minute

// link is the battle.net sign-in's settings: where Blizzard sends members
// back and the secret that signs who asked.
type link struct {
	hooks, secret string
	authorize     string // Blizzard's sign-in page
	token         string // where a code becomes a token

	mu   sync.Mutex
	used map[string]time.Time // states spent, until they'd expire anyway
}

func (l *link) on() bool { return l != nil && l.hooks != "" && len(l.secret) >= 16 }

func (l *link) redirect() string { return l.hooks + "/forever/callback" }

// state is who asked, signed: guild, user, when it stops being good and a
// nonce, so every link is its own and spending one leaves the next good.
func (l *link) state(guild, user snowflake.ID, until time.Time) string {
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	body := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d:%d:%x", guild, user, until.Unix(), nonce)))
	return body + "." + l.sign(body)
}

func (l *link) sign(body string) string {
	h := hmac.New(sha256.New, []byte(l.secret))
	h.Write([]byte("forever-link:" + body))
	return hex.EncodeToString(h.Sum(nil))
}

// errState is a state skua didn't sign, has seen before or that expired.
var errState = errors.New("forever: bad link state")

// check reads a state back, once.
func (l *link) check(state string, now time.Time) (guild, user snowflake.ID, err error) {
	body, sig, ok := strings.Cut(state, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(l.sign(body))) {
		return 0, 0, errState
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return 0, 0, errState
	}
	f := strings.Split(string(raw), ":")
	if len(f) != 4 {
		return 0, 0, errState
	}
	g, e1 := snowflake.Parse(f[0])
	u, e2 := snowflake.Parse(f[1])
	until, e3 := strconv.ParseInt(f[2], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || now.Unix() > until {
		return 0, 0, errState
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for s, t := range l.used {
		if now.After(t) {
			delete(l.used, s)
		}
	}
	if _, seen := l.used[state]; seen {
		return 0, 0, errState
	}
	l.used[state] = time.Unix(until, 0)
	return g, u, nil
}

// linkCard answers the link button: the sign-in, for the member's eyes.
func (m *Module) linkCard(e *events.ComponentInteractionCreate, guild snowflake.ID) error {
	if !m.link.on() {
		return core.Tell("battle.net sign-in isn't set up on this skua")
	}
	q := url.Values{
		"client_id": {m.api.id}, "redirect_uri": {m.link.redirect()}, "response_type": {"code"},
		"scope": {"wow.profile"}, "state": {m.link.state(guild, e.User().ID, m.now().Add(linkTTL))},
	}
	signIn := discord.NewLinkButton("sign in to battle.net", m.link.authorize+"?"+q.Encode())
	signIn.Emoji = brand.ComponentEmoji("fv_link")
	body := "sign in and skua adds every forever character on your battle.net account, in every region, marked as yours\n-# the link works once, for 10 minutes. skua reads your characters with it and keeps nothing else"
	return e.CreateMessage(v2(brand.Card(brand.ColorNotice, Name, "battle.net", body, discord.NewActionRow(signIn)), true))
}

// Hooks is the handler for forever's path on SKUA_HOOKS_ADDR.
func (m *Module) Hooks() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /forever/callback", m.callback)
	return mux
}

// Linking is whether battle.net sign-in is set up, so main serves Hooks.
func (m *Module) Linking() bool { return m.link.on() }

// page is the one line a member reads in their browser after signing in.
func page(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta name="viewport" content="width=device-width"><title>skua</title><body style="background:#1e2228;color:#dbdee1;font:16px system-ui;display:grid;place-items:center;min-height:90vh;margin:0"><p style="max-width:32em;padding:0 16px">%s</p>`, text)
}

// callback is where Blizzard sends a member back: the code becomes a token,
// the token their characters, and the characters theirs in the server
// that asked.
func (m *Module) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	guild, user, err := m.link.check(q.Get("state"), m.now())
	if err != nil {
		page(w, http.StatusBadRequest, "this sign-in link has expired or was already used. press link in /forever me for a new one")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		page(w, http.StatusOK, "battle.net sign-in was cancelled, so nothing changed. you can close this tab")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	tok, err := m.exchange(ctx, q.Get("code"))
	if err != nil {
		m.log.Warn("forever: battle.net sign-in", "err", err)
		page(w, http.StatusBadGateway, "battle.net didn't let skua read your characters. try the link button again in a moment")
		return
	}
	added, skipped := m.importAll(ctx, guild, user, tok)
	text := fmt.Sprintf("skua added %d of your forever characters, marked as yours. you can close this tab and run /forever me", added)
	if skipped > 0 {
		text += fmt.Sprintf(". %d more aren't in blizzard's armory yet: a character shows from level 10, once it has logged out", skipped)
	}
	page(w, http.StatusOK, text)
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
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&t); err != nil || t.Token == "" {
		return "", fmt.Errorf("forever: code exchange: %w", err)
	}
	return t.Token, nil
}

// account is the slice of a battle.net account summary skua reads.
type account struct {
	Accounts []struct {
		Characters []struct {
			Name    string `json:"name"`
			Surname string `json:"surname"`
			Level   int    `json:"level"`
		} `json:"characters"`
	} `json:"wow_accounts"`
}

// importAll lists every Forever character on the member's account, in
// every region, as theirs and verified.
func (m *Module) importAll(ctx context.Context, guild, user snowflake.ID, tok string) (added, skipped int) {
	for _, region := range Regions {
		var acc account
		if err := m.api.getAs(ctx, tok, region, "/profile/user/wow", &acc); err != nil {
			continue // no Forever account there
		}
		for _, a := range acc.Accounts {
			for _, c := range a.Characters {
				if c.Name == "" || c.Surname == "" {
					continue
				}
				if _, err := m.list(ctx, guild, user, region, c.Name, c.Surname, true); err != nil {
					skipped++
					continue
				}
				added++
			}
		}
	}
	return added, skipped
}
