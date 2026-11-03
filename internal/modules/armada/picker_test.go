// SPDX-License-Identifier: AGPL-3.0-only

package armada

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/6586x57890143/skua/internal/concord"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/store"
)

// seed links pairs "<discord>=<armada>,..." as if an admin had picked them.
func seed(m *Module, pairs string) {
	for _, pair := range strings.Split(pairs, ",") {
		d, a, _ := strings.Cut(pair, "=")
		l := newLink(snowflake.MustParse(d), a)
		m.links = append(m.links, l)
		m.byDisc[l.discord] = l
	}
}

// other is a readable armada channel the harness's first link isn't on.
func (h *harness) other(t *testing.T) string {
	t.Helper()
	for id := range h.chans {
		if id != general {
			return id
		}
	}
	t.Fatal("the vectors have one readable channel")
	return ""
}

// choose is an admin in guild 3 picking values on the select id.
func (h *harness) choose(t *testing.T, id string, kind int, values ...string) (*[]discord.MessageCreate, error) {
	t.Helper()
	e, sent := coretest.Button(t, id, func(p map[string]any) {
		p["data"] = map[string]any{"custom_id": id, "component_type": kind, "values": values}
		if kind == 8 {
			res := map[string]any{}
			for _, v := range values {
				res[v] = map[string]any{"id": v, "type": 0, "name": "here", "permissions": "0"}
			}
			p["data"].(map[string]any)["resolved"] = map[string]any{"channels": res}
		}
	})
	h.m.Admin = func(discord.Interaction) bool { return true }
	return sent, h.m.press(context.Background(), e)
}

func TestPickerLinksMovesAndStops(t *testing.T) {
	h := newHarness(t, "100="+general)
	h.m.mu.Lock()
	h.m.ready = true
	h.m.mu.Unlock()
	arm := h.other(t)

	sent, err := h.choose(t, "armada:pick", 3, arm)
	if err != nil || len(*sent) != 1 || !strings.Contains(fmt.Sprint((*sent)[0].Components), "armada:to:"+arm) {
		t.Fatalf("pick answered %+v (%v)", sent, err)
	}
	subs := len(h.pool.subs)
	if sent, err = h.choose(t, "armada:to:"+arm, 8, "50"); err != nil || !strings.Contains((*sent)[0].Content, "<#50> ↔ #") {
		t.Fatalf("link answered %+v (%v)", sent, err)
	}
	first, ok := h.m.linkOn(50)
	if !ok || snowflake.ID(first.guild.Load()) != 3 || len(h.pool.subs) != subs+1 || h.m.subs[arm] == nil {
		t.Fatal("the link isn't live")
	}
	if !strings.Contains(fmt.Sprint(h.m.Report(3).Controls), "crosses into this server") {
		t.Error("the picker doesn't mark what crosses here")
	}

	if _, err := h.choose(t, "armada:to:"+arm, 8, "51"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.m.linkOn(50); ok {
		t.Error("the old channel still crosses")
	}
	if _, ok := h.m.linkOn(51); !ok {
		t.Error("the new channel doesn't cross")
	}
	select {
	case <-first.done:
	default:
		t.Error("the old link's workers live on")
	}
	if err := h.m.link(context.Background(), 8, 60, arm); !errors.Is(err, errTaken) {
		t.Errorf("another server took a carried channel: %v", err)
	}

	if sent, err = h.choose(t, "armada:to:"+arm, 8); err != nil || !strings.Contains((*sent)[0].Content, "crosses nowhere") {
		t.Fatalf("unlink answered %+v (%v)", sent, err)
	}
	if _, ok := h.m.linkOn(51); ok || h.m.subs[arm] != nil {
		t.Error("the link outlived its unpicking")
	}
	if len(h.m.linkList()) != 1 {
		t.Error("the other link went too")
	}
}

func TestPickerRefuses(t *testing.T) {
	h := newHarness(t, "100="+general)
	arm := h.other(t)
	if _, err := h.choose(t, "armada:to:"+strings.Repeat("ab", 32), 8, "50"); !errors.Is(err, errGone) {
		t.Errorf("an unreadable channel was picked: %v", err)
	}
	if _, err := h.choose(t, "armada:pick", 3, strings.Repeat("ab", 32)); !errors.Is(err, errGone) {
		t.Errorf("an unreadable channel was offered: %v", err)
	}
	if _, err := h.choose(t, "armada:to:"+arm, 3, "50"); !errors.Is(err, errNotAdmin) {
		t.Errorf("a string select linked: %v", err)
	}
	if err := h.m.unlink(context.Background(), 3, arm); err != nil {
		t.Errorf("unpicking nothing: %v", err)
	}
	e, _ := coretest.Button(t, "armada:pick", nil)
	h.m.Admin = func(discord.Interaction) bool { return false }
	if err := h.m.press(context.Background(), e); !errors.Is(err, errNotAdmin) {
		t.Errorf("a member picked: %v", err)
	}
}

func TestPickerWithNothingReadable(t *testing.T) {
	h := newHarness(t, "100="+general)
	h.m.mu.Lock()
	h.m.chans = nil
	h.m.mu.Unlock()
	if r := h.m.Report(3); !strings.Contains(fmt.Sprint(r.Controls), "nothing to pick") {
		t.Errorf("controls %+v", r.Controls)
	}
}

// linkDB serves picked links and records writes.
type linkDB struct {
	rows  [][3]any
	execs []string
	err   error
}

func (d *linkDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.execs = append(d.execs, fmt.Sprint(sql, args))
	return pgconn.CommandTag{}, d.err
}

func (d *linkDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return &linkRows{rows: d.rows, i: -1}, nil
}

type linkRows struct {
	pgx.Rows
	rows [][3]any
	i    int
}

func (r *linkRows) Next() bool { r.i++; return r.i < len(r.rows) }
func (r *linkRows) Scan(dest ...any) error {
	*dest[0].(*int64), *dest[1].(*int64), *dest[2].(*string) = r.rows[r.i][0].(int64), r.rows[r.i][1].(int64), r.rows[r.i][2].(string)
	return nil
}
func (*linkRows) Close()                        {}
func (*linkRows) Err() error                    { return nil }
func (*linkRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func TestPickedLinksOutliveARestart(t *testing.T) {
	v := loadVectors(t)
	db := &linkDB{rows: [][3]any{{int64(50), int64(3), strings.Repeat("22", 32)}, {int64(100), int64(3), strings.Repeat("33", 32)}}}
	m, err := New(slog.New(slog.DiscardHandler), guard.New(), &fakePoster{}, filter.Default(), db, Config{
		Invite: v.Invite.URL, Master: v.Puppet.Master, Primary: v.Direct.Recipient,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	l, ok := m.linkOn(50)
	if !ok || snowflake.ID(l.guild.Load()) != 3 || len(m.linkList()) != 2 {
		t.Fatalf("loaded %v", m.linkList())
	}
	db.err = errors.New("down")
	m.chans = map[string]concord.ChatChannel{strings.Repeat("44", 32): {}}
	if m.link(context.Background(), 3, 70, strings.Repeat("44", 32)) == nil {
		t.Error("the store refused and the link went through")
	}
	if _, ok := m.linkOn(70); ok {
		t.Error("memory took a link the store refused")
	}
	if m.unlink(context.Background(), 3, strings.Repeat("22", 32)) == nil {
		t.Error("the store refused and the unlink went through")
	}
}

func TestPostgresLinks(t *testing.T) {
	dsn := os.Getenv("SKUA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKUA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	arm := strings.Repeat("5e", 32)
	m := &Module{db: pool, byDisc: map[snowflake.ID]*link{}, subs: map[string]context.CancelFunc{}, subKeys: map[string]string{}, missing: map[string]bool{}}
	for _, ch := range []snowflake.ID{9001, 9002} {
		if err := m.link(ctx, 3, ch, arm); err != nil {
			t.Fatal(err)
		}
	}
	got, err := loadLinks(pool)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(got, func(l *link) bool { return l.armada == arm }); i < 0 || got[i].discord != 9002 {
		t.Fatalf("stored %v", got)
	}
	if err := m.unlink(ctx, 3, arm); err != nil {
		t.Fatal(err)
	}
	got, _ = loadLinks(pool)
	if slices.ContainsFunc(got, func(l *link) bool { return l.armada == arm }) {
		t.Error("the unlink wasn't stored")
	}
}

// The page shows both pickers, and picking an armada channel swaps them in
// place, pointing the channel select at it.
func TestPickerSwapsInPlace(t *testing.T) {
	h := newHarness(t, "100="+general)
	arm := h.other(t)
	page := []discord.LayoutComponent{discord.NewContainer(append([]discord.ContainerSubComponent{discord.NewTextDisplay("top")},
		append(h.m.Report(3).Controls, discord.NewTextDisplay("bottom"))...)...)}
	if s := fmt.Sprint(page); !strings.Contains(s, "armada:pick") || !strings.Contains(s, "armada:to:") {
		t.Fatalf("the page lacks a picker: %s", s)
	}
	got, ok := swapControls(page, h.m.controls(3, arm))
	box := got[0].(discord.ContainerComponent)
	if !ok || len(box.Components) != 6 || !strings.Contains(fmt.Sprint(box.Components[4]), "armada:to:"+arm) ||
		fmt.Sprint(box.Components[0]) != fmt.Sprint(discord.NewTextDisplay("top")) || fmt.Sprint(box.Components[5]) != fmt.Sprint(discord.NewTextDisplay("bottom")) {
		t.Fatalf("swapped %v (%v)", box.Components, ok)
	}
	if _, ok := swapControls([]discord.LayoutComponent{discord.NewContainer(discord.NewTextDisplay("other"))}, nil); ok {
		t.Error("swapped a page with no picker")
	}
}
