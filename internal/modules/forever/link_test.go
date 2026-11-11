package forever

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

func linked(t *testing.T) (*Module, *armory) {
	t.Helper()
	m, a, _ := newTest(t)
	m.link.hooks, m.link.secret = "https://hooks.example", "0123456789abcdef0123"
	a.routes["/oauth/token"] = func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if id, secret, _ := r.BasicAuth(); id != "id" || secret != "s" || r.PostForm.Get("code") != "good" || r.PostForm.Get("redirect_uri") != "https://hooks.example/forever/callback" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"user"}`)
	}
	a.routes["/profile/user/wow"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("namespace") != "profile-forever-eu" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"wow_accounts":[{"characters":[{"name":"A","surname":"B","level":30},{"name":"Low","surname":"Level","level":4},{"name":"","surname":"x"}]}]}`)
	}
	return m, a
}

func TestLinkState(t *testing.T) {
	m, _ := linked(t)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	s := m.link.state(3, 5, now.Add(linkTTL))
	if g, u, err := m.link.check(s, now); err != nil || g != 3 || u != 5 {
		t.Fatalf("a good state: %v %v %v", g, u, err)
	}
	if _, _, err := m.link.check(s, now); err == nil {
		t.Fatal("a state worked twice")
	}
	body, _, _ := strings.Cut(m.link.state(3, 6, now.Add(linkTTL)), ".")
	forged := base64.RawURLEncoding.EncodeToString([]byte("3:7:9999999999:00"))
	for name, bad := range map[string]string{
		"forged":   forged + "." + strings.Repeat("0", 64),
		"swapped":  forged + "." + m.link.sign(body),
		"no sig":   body,
		"not b64":  "%%%." + m.link.sign("%%%"),
		"fields":   base64.RawURLEncoding.EncodeToString([]byte("3:5")) + "." + m.link.sign(base64.RawURLEncoding.EncodeToString([]byte("3:5"))),
		"expired":  m.link.state(3, 5, now.Add(-time.Second)),
		"not nums": base64.RawURLEncoding.EncodeToString([]byte("a:b:c:d")) + "." + m.link.sign(base64.RawURLEncoding.EncodeToString([]byte("a:b:c:d"))),
	} {
		if _, _, err := m.link.check(bad, now); err == nil {
			t.Errorf("%s state passed", name)
		}
	}
	// Spent states are forgotten once they'd have expired anyway.
	_, _, _ = m.link.check(m.link.state(1, 1, now.Add(time.Minute)), now)
	_, _, _ = m.link.check(m.link.state(1, 2, now.Add(time.Hour)), now.Add(30*time.Minute))
	if len(m.link.used) != 1 {
		t.Fatalf("spent states kept: %d", len(m.link.used))
	}
}

func TestLinkCard(t *testing.T) {
	m, _, _ := newTest(t)
	b, _ := coretest.Button(t, "forever:link", nil)
	if err := m.click(context.Background(), b); err == nil {
		t.Fatal("linked without hooks")
	}
	m.link.hooks, m.link.secret = "https://hooks.example", "0123456789abcdef0123"
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
	if _, _, err := m.link.check(u.Query().Get("state"), m.now()); err != nil {
		t.Fatal("the link's state doesn't check")
	}
	if !strings.Contains(text(m.hub(3, 5, "")), "") || !hasButton(m.hub(3, 5, ""), "forever:link") {
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

func TestLinkCallback(t *testing.T) {
	m, a := linked(t)
	a.set("a-b", prof(1, "A", "B", "Mage", 30), "x")
	// Someone else listed A B by name first; the owner's sign-in takes it.
	if _, err := m.list(context.Background(), 3, 9, "eu", "a", "b", false); err != nil {
		t.Fatal(err)
	}
	call := func(q url.Values) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		m.Hooks().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/forever/callback?"+q.Encode(), nil))
		return w
	}
	if w := call(url.Values{"state": {"nope"}, "code": {"good"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad state: %d", w.Code)
	}
	if w := call(url.Values{"state": {m.link.state(3, 5, m.now().Add(linkTTL))}, "error": {"access_denied"}}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatalf("cancelled: %d %s", w.Code, w.Body)
	}
	if w := call(url.Values{"state": {m.link.state(3, 5, m.now().Add(linkTTL))}, "code": {"bad"}}); w.Code != http.StatusBadGateway {
		t.Fatalf("a refused code: %d", w.Code)
	}
	w := call(url.Values{"state": {m.link.state(3, 5, m.now().Add(linkTTL))}, "code": {"good"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "added 1") || !strings.Contains(w.Body.String(), "1 more") {
		t.Fatalf("callback: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("no csp on the page")
	}
	var mine []claim
	for _, c := range m.claims {
		if c.guild == 3 && c.k == (key{"eu", 1}) {
			mine = append(mine, c)
		}
	}
	if len(mine) != 1 || mine[0].user != 5 || !mine[0].verified {
		t.Fatalf("claims after sign-in: %+v", mine)
	}
	if !strings.Contains(text(m.hub(3, 5, "")), "✓ linked") {
		t.Fatal("the card doesn't say linked")
	}
	// Someone else can't take a verified character by name.
	if _, err := m.list(context.Background(), 3, 9, "eu", "a", "b", false); err == nil {
		t.Fatal("a name claim took a verified character")
	}
	// Signing in again verifies what's already the member's.
	if w := call(url.Values{"state": {m.link.state(3, 5, m.now().Add(linkTTL))}, "code": {"good"}}); !strings.Contains(w.Body.String(), "added 1") {
		t.Fatalf("second sign-in: %s", w.Body)
	}
}
