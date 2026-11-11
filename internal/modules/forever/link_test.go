package forever

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// linked is a module with sign-in on, over a fake whose token exchange
// takes the code "good" and whose account (eu) holds A B, a character under
// level 10 and an unnamed one.
func linked(t *testing.T) (*Module, *armory, *fake) {
	t.Helper()
	m, a, f := newTest(t)
	m.link.hooks = "https://hooks.example"
	a.routes["/oauth/token"] = func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if id, secret, _ := r.BasicAuth(); id != "id" || secret != "s" || r.PostForm.Get("code") != "good" || r.PostForm.Get("redirect_uri") != "https://hooks.example/forever/callback" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"user"}`)
	}
	a.routes["/profile/user/wow"] = func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Authorization") != "Bearer user":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Query().Get("namespace") == "profile-forever-kr":
			w.WriteHeader(http.StatusForbidden) // no account there
		case r.URL.Query().Get("namespace") != "profile-forever-eu":
			w.WriteHeader(http.StatusNotFound)
		default:
			_, _ = io.WriteString(w, `{"wow_accounts":[{"characters":[{"name":"A","surname":"B"},{"name":"Low","surname":"Level"},{"name":"","surname":"x"}]}]}`)
		}
	}
	return m, a, f
}

// signIn is a sign-in asked for by user in guild 3, returned as its state.
func signIn(m *Module, user snowflake.ID) string {
	return m.link.put(m.link.asks, ask{guild: 3, user: user, who: "<steve>", where: "the pot", until: m.now().Add(linkTTL)}, m.now())
}

func get(m *Module, q url.Values) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	m.Hooks().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/forever/callback?"+q.Encode(), nil))
	return w
}

func post(m *Module, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/forever/confirm", strings.NewReader(url.Values{"id": {id}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	m.Hooks().ServeHTTP(w, r)
	return w
}

var confirmID = regexp.MustCompile(`name="id" value="([0-9a-f]+)"`)

func TestLinkNonces(t *testing.T) {
	m, _, _ := linked(t)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	a := m.link.put(m.link.asks, ask{user: 5, until: now.Add(time.Minute)}, now)
	b := m.link.put(m.link.asks, ask{user: 5, until: now.Add(time.Minute)}, now)
	if a == b || len(a) != 32 {
		t.Fatal("two sign-ins share a nonce")
	}
	if got, ok := m.link.take(m.link.asks, a, now); !ok || got.user != 5 {
		t.Fatal("a good nonce")
	}
	if _, ok := m.link.take(m.link.asks, a, now); ok {
		t.Fatal("a nonce worked twice")
	}
	if _, ok := m.link.take(m.link.asks, b, now.Add(2*time.Minute)); ok {
		t.Fatal("an expired nonce worked")
	}
	m.link.put(m.link.asks, ask{until: now.Add(time.Minute)}, now)
	m.link.put(m.link.asks, ask{until: now.Add(time.Hour)}, now.Add(30*time.Minute))
	if len(m.link.asks) != 1 {
		t.Fatalf("expired sign-ins kept: %d", len(m.link.asks))
	}
}

func TestLinkCard(t *testing.T) {
	m, _, _ := newTest(t)
	b, _ := coretest.Button(t, "forever:link", nil)
	if err := m.click(context.Background(), b); err == nil {
		t.Fatal("linked without hooks")
	}
	m.link.hooks = "https://hooks.example"
	if !m.Linking() || m.Hooks() == nil {
		t.Fatal("linking off")
	}
	b, sent := coretest.Button(t, "forever:link", nil)
	if err := m.click(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	box := (*sent)[0].Components[0].(discord.ContainerComponent)
	row := box.Components[len(box.Components)-1].(discord.ActionRowComponent)
	u, err := url.Parse(row.Components[0].(discord.ButtonComponent).URL)
	if err != nil || u.Query().Get("redirect_uri") != "https://hooks.example/forever/callback" || u.Query().Get("scope") != "wow.profile" {
		t.Fatalf("sign-in link: %v %v", u, err)
	}
	if a, ok := m.link.take(m.link.asks, u.Query().Get("state"), m.now()); !ok || a.user != 5 || a.guild != 3 || a.who == "" {
		t.Fatalf("the link's sign-in: %+v", a)
	}
	if !hasButton(m.hub(3, 5, ""), "forever:link") {
		t.Fatal("no link button on the card")
	}
}

func hasButton(c discord.ContainerComponent, id string) bool {
	for _, s := range c.Components {
		if row, ok := s.(discord.ActionRowComponent); ok {
			for _, x := range row.Components {
				if b, ok := x.(discord.ButtonComponent); ok && b.CustomID == id {
					return true
				}
			}
		}
	}
	return false
}

func TestLinkConfirm(t *testing.T) {
	m, a, f := linked(t)
	m.guilds[3] = setup{role: 70}
	a.set("a-b", prof(1, "A", "B", "Mage", 30), "x")
	// Someone else listed A B by name first.
	if _, err := m.list(context.Background(), 3, 9, "eu", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	if w := get(m, url.Values{"state": {"nope"}, "code": {"good"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad state: %d", w.Code)
	}
	if w := get(m, url.Values{"state": {signIn(m, 5)}, "error": {"access_denied"}}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatalf("cancelled: %d %s", w.Code, w.Body)
	}
	if w := get(m, url.Values{"state": {signIn(m, 5)}, "code": {"bad"}}); w.Code != http.StatusBadGateway {
		t.Fatalf("a refused code: %d", w.Code)
	}
	// Signing in only shows what would be linked, to whom and where, with
	// what came from outside escaped.
	w := get(m, url.Values{"state": {signIn(m, 5)}, "code": {"good"}})
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "link these 2 forever characters to <b>&lt;steve&gt;</b> in <b>the pot</b>") || !strings.Contains(body, "A B") || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("confirm page: %d %s", w.Code, body)
	}
	if len(m.claims) != 1 || m.claims[0].user != 9 {
		t.Fatal("linked before confirming")
	}
	id := confirmID.FindStringSubmatch(body)[1]
	// Confirming links what Blizzard knows, takes A B from the name claim,
	// gives the role, takes it from whoever lost their last character and
	// says why the rest didn't come.
	w = post(m, id)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "linked 1") || !strings.Contains(w.Body.String(), "1 aren't in blizzard's armory yet") {
		t.Fatalf("confirmed: %d %s", w.Code, w.Body)
	}
	if len(m.claims) != 1 || m.claims[0].user != 5 || !m.claims[0].verified {
		t.Fatalf("claims after confirming: %+v", m.claims)
	}
	if len(f.added) != 1 || len(f.removed) != 1 {
		t.Fatalf("roles: added %v, removed %v", f.added, f.removed)
	}
	if !strings.Contains(text(m.hub(3, 5, "")), "✓ linked") {
		t.Fatal("the card doesn't say linked")
	}
	if w := post(m, id); w.Code != http.StatusBadRequest {
		t.Fatal("a confirm page worked twice")
	}
	// A name claim can't take a verified character...
	if _, err := m.list(context.Background(), 3, 9, "eu", "a", "b", false); err == nil {
		t.Fatal("a name claim took a verified character")
	}
	// ...but the account's latest sign-in can: it's the owner's.
	w = get(m, url.Values{"state": {signIn(m, 9)}, "code": {"good"}})
	post(m, confirmID.FindStringSubmatch(w.Body.String())[1])
	if len(m.claims) != 1 || m.claims[0].user != 9 {
		t.Fatalf("the latest sign-in didn't win: %+v", m.claims)
	}
}

func TestLinkBlizzardDown(t *testing.T) {
	m, a, _ := linked(t)
	a.routes["/profile/user/wow"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }
	if w := get(m, url.Values{"state": {signIn(m, 5)}, "code": {"good"}}); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "didn't answer for any region") {
		t.Fatalf("down: %d %s", w.Code, w.Body)
	}
	a.routes["/profile/user/wow"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("namespace") == "profile-forever-us" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
	if w := get(m, url.Values{"state": {signIn(m, 5)}, "code": {"good"}}); !strings.Contains(w.Body.String(), "no forever characters") {
		t.Fatalf("empty account: %s", w.Body)
	}
	if reason(errNoGear) != "other" {
		t.Fatal("reasons")
	}
}
