package help

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/core/coretest"
	"github.com/6586x57890143/skua/internal/intents"
	"github.com/6586x57890143/skua/internal/modules/bird"
	"github.com/6586x57890143/skua/internal/modules/perf"
	"github.com/6586x57890143/skua/internal/modules/preen"
	"github.com/6586x57890143/skua/internal/modules/purge"
	"github.com/6586x57890143/skua/internal/modules/status"
	"github.com/6586x57890143/skua/internal/modules/whisper"
	"github.com/6586x57890143/skua/internal/obs"
)

// fake is a module without a page; paged is one with.
type fake struct {
	name string
	cmds []core.Command
	help core.Help
}

func (f fake) Name() string             { return f.name }
func (fake) Want() intents.Want         { return intents.Want{Required: gateway.IntentGuilds} }
func (fake) Perms() discord.Permissions { return 0 }
func (f fake) Commands() []core.Command { return f.cmds }

type paged struct{ fake }

func (p paged) Help() core.Help { return p.help }

func run(context.Context, *events.ApplicationCommandInteractionCreate) error { return nil }

var (
	nest = paged{fake{"nest", []core.Command{
		{Create: discord.SlashCommandCreate{Name: "nest", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{Name: "build", Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{Name: "twig"}, discord.ApplicationCommandOptionInt{Name: "count"},
			}},
			discord.ApplicationCommandOptionSubCommand{Name: "leave"},
		}}, Tier: core.Public, Run: run},
		{Create: discord.MessageCommandCreate{Name: "Steal egg"}, Tier: core.Admin, Run: run},
	}, core.Help{Color: brand.ColorOK, Line: "where she sleeps", About: "twigs mostly"}}}
	gull   = paged{fake{"gull", []core.Command{{Create: discord.SlashCommandCreate{Name: "gull"}, Tier: core.Admin, Run: run}}, core.Help{Color: brand.ColorOK, Line: "loud", About: "very loud"}}}
	stat   = paged{fake{"status", nil, core.Help{Color: brand.ColorIdle, Line: "fine", About: "fine"}}}
	hidden = fake{name: "hidden"}
)

// toggles is an in-memory switchboard; fail makes Set fail.
type toggles struct {
	off  map[string]bool
	fail bool
}

func (t *toggles) On(_ snowflake.ID, module string) bool { return !t.off[module] }
func (t *toggles) Set(_ context.Context, _ snowflake.ID, module string, on bool) error {
	if t.fail {
		return errors.New("db down")
	}
	t.off[module] = !on
	return nil
}

// newRouter is help wired as main wires it, over the modules given.
func newRouter(t *testing.T, sw Toggles, mods ...core.Module) *core.Router {
	t.Helper()
	var r *core.Router
	h := New(func() []core.Module { return mods }, func(i discord.Interaction) bool { return r.Admin(i) }, sw)
	r = core.NewRouter(0, func(snowflake.ID) (snowflake.ID, bool) { return 0, false }, slog.New(slog.DiscardHandler))
	if err := r.Add(h); err != nil {
		t.Fatal(err)
	}
	return r
}

// container is the one container a guide is.
func container(t *testing.T, comps []discord.LayoutComponent, flags discord.MessageFlags) discord.ContainerComponent {
	t.Helper()
	if !flags.Has(discord.MessageFlagIsComponentsV2) || len(comps) != 1 {
		t.Fatalf("not one components v2 container: %+v", comps)
	}
	c, ok := comps[0].(discord.ContainerComponent)
	if !ok {
		t.Fatalf("%T, want a container", comps[0])
	}
	return c
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// checkFiles: every attachment:// URL a guide points at is uploaded with
// it, once, and nothing else is.
func checkFiles(t *testing.T, comps []discord.LayoutComponent, files []*discord.File) {
	t.Helper()
	want := map[string]bool{}
	for _, u := range regexp.MustCompile(`attachment://([a-z_]+\.png)`).FindAllStringSubmatch(mustJSON(t, comps), -1) {
		want[u[1]] = true
	}
	if len(files) != len(want) {
		t.Fatalf("%d files for urls %v", len(files), want)
	}
	for _, f := range files {
		if !want[f.Name] {
			t.Fatalf("%s uploaded but nothing points at it", f.Name)
		}
	}
}

func contains(t *testing.T, what, all string, want ...string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(all, s) {
			t.Errorf("%s is missing %q", what, s)
		}
	}
}

func TestIndexListsTheRunningModulesWithAPage(t *testing.T) {
	r := newRouter(t, &toggles{off: map[string]bool{"gull": true}}, nest, hidden, gull)
	e, sent := coretest.Event(t, "help", nil)
	r.OnCommand(e)
	if len(*sent) != 1 {
		t.Fatalf("sent %d messages", len(*sent))
	}
	m := (*sent)[0]
	if !m.Flags.Has(discord.MessageFlagEphemeral) || m.AllowedMentions == nil || m.AllowedMentions.Parse == nil {
		t.Fatalf("not an ephemeral reply with no pings: %+v", m)
	}
	if c := container(t, m.Components, m.Flags); c.AccentColor != brand.ColorPrimary {
		t.Fatalf("accent %#06x", c.AccentColor)
	}
	all := mustJSON(t, m.Components)
	contains(t, "index", all, "`├` **nest** · where she sleeps\\n", "`└` **gull** · loud · `off here`", "2 modules · 3 commands · build `"+core.Revision()+"`", `"help:pick"`, invite, source)
	if strings.Contains(all, "hidden") {
		t.Error("a module without a page is listed")
	}
	checkFiles(t, m.Components, m.Files)
	if len(m.Files) != 1 || m.Files[0].Name != "skua_avatar.png" {
		t.Errorf("files %v; the index is compact: one avatar and no more", m.Files)
	}
}

func post(p map[string]any) {
	p["data"].(map[string]any)["options"] = []any{map[string]any{"name": "post", "type": 5, "value": true}}
}

func admin(p map[string]any) { p["member"].(map[string]any)["permissions"] = "8" }

func TestPostIsForAdmins(t *testing.T) {
	r := newRouter(t, nil, nest)
	e, sent := coretest.Event(t, "help", post)
	r.OnCommand(e)
	if len(*sent) != 1 || !strings.HasPrefix((*sent)[0].Content, "✗ only this server's admins can post the guide") {
		t.Fatalf("a member posting got %+v", *sent)
	}
	e, sent = coretest.Event(t, "help", func(p map[string]any) { post(p); admin(p) })
	r.OnCommand(e)
	if len(*sent) != 1 || (*sent)[0].Flags.Has(discord.MessageFlagEphemeral) {
		t.Fatalf("an admin's post is not public: %+v", *sent)
	}
}

// response is the one response a component interaction got.
type response struct {
	create *discord.MessageCreate
	update *discord.MessageUpdate
}

// click presses customID, or picks values from it when there are any.
func click(t *testing.T, r *core.Router, customID string, ephemeral, asAdmin bool, values ...string) response {
	t.Helper()
	e, _ := coretest.Button(t, customID, func(p map[string]any) {
		if !ephemeral {
			p["message"].(map[string]any)["flags"] = 0
		}
		if asAdmin {
			admin(p)
		}
		if values != nil {
			p["data"] = map[string]any{"custom_id": customID, "component_type": 3, "values": values}
		}
	})
	var got []response
	e.Respond = func(_ discord.InteractionResponseType, d discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		switch m := d.(type) {
		case discord.MessageCreate:
			got = append(got, response{create: &m})
		case discord.MessageUpdate:
			got = append(got, response{update: &m})
		}
		return nil
	}
	r.OnComponent(e)
	if len(got) != 1 {
		t.Fatalf("%s: %d responses", customID, len(got))
	}
	return got[0]
}

// page is the update a click turned the guide into.
func page(t *testing.T, res response) (discord.ContainerComponent, string) {
	t.Helper()
	u := res.update
	if u == nil {
		t.Fatalf("got %+v, want an update", res)
	}
	if u.Attachments == nil || len(*u.Attachments) != 0 {
		t.Error("the last page's attachments are not cleared")
	}
	checkFiles(t, *u.Components, u.Files)
	return container(t, *u.Components, discord.MessageFlagIsComponentsV2), mustJSON(t, *u.Components)
}

func TestPickingTurnsAnEphemeralGuideInPlace(t *testing.T) {
	r := newRouter(t, &toggles{off: map[string]bool{}}, nest, gull)
	c, all := page(t, click(t, r, "help:pick", true, false, "nest"))
	if c.AccentColor != brand.ColorOK {
		t.Errorf("page accent %#06x, want the module's colour", c.AccentColor)
	}
	contains(t, "page", all, "## nest", "twigs mostly", "/nest build", "twig, count", "/nest leave", "Steal egg", "apps menu · admin", `"default":true`, `"help:index"`)
	if strings.Contains(all, "help:off") {
		t.Error("a member is shown the switch")
	}
	if _, all := page(t, click(t, r, "help:index", true, false)); !strings.Contains(all, "**gull**") {
		t.Error("back did not return to the index")
	}
}

func TestPickingOnAPostedGuideAnswersOnlyTheClicker(t *testing.T) {
	r := newRouter(t, nil, nest)
	res := click(t, r, "help:pick", false, false, "nest")
	if res.create == nil || !res.create.Flags.Has(discord.MessageFlagEphemeral) {
		t.Fatalf("got %+v, want an ephemeral reply", res)
	}
}

func TestPickingAModuleThatStoppedRunning(t *testing.T) {
	r := newRouter(t, nil, nest)
	if res := click(t, r, "help:pick", true, false, "gone"); res.create == nil || !strings.HasPrefix(res.create.Content, "✗ that module isn't running") {
		t.Fatalf("got %+v", res)
	}
}

func TestAdminsSwitchModulesFromTheGuide(t *testing.T) {
	sw := &toggles{off: map[string]bool{}}
	r := newRouter(t, sw, nest, stat)

	_, all := page(t, click(t, r, "help:pick", true, true, "nest"))
	contains(t, "admin page", all, "**on** in this server", `"help:off:nest"`)

	_, all = page(t, click(t, r, "help:off:nest", true, true))
	if !sw.off["nest"] {
		t.Fatal("switch off did not switch it off")
	}
	contains(t, "switched page", all, "**off** in this server", `"help:on:nest"`)

	page(t, click(t, r, "help:on:nest", true, true))
	if sw.off["nest"] {
		t.Fatal("switch on did not switch it on")
	}

	if _, all := page(t, click(t, r, "help:pick", true, true, "status")); strings.Contains(all, "help:off") {
		t.Error("status offers a switch")
	}
}

func TestSwitchesRefuseAnyoneElse(t *testing.T) {
	sw := &toggles{off: map[string]bool{}}
	for _, c := range []struct {
		name, id string
		admin    bool
		sw       Toggles
	}{
		{"a member", "help:off:nest", false, sw},
		{"a fixed module", "help:off:status", true, sw},
		{"help itself", "help:off:help", true, sw},
		{"no switchboard", "help:off:nest", true, nil},
	} {
		res := click(t, newRouter(t, c.sw, nest, stat), c.id, true, c.admin)
		if res.create == nil || res.create.Content != "✗ only this server's admins can switch modules" {
			t.Errorf("%s: got %+v", c.name, res)
		}
	}
	res := click(t, newRouter(t, sw, nest), "help:off:forged", true, true)
	if res.create == nil || !strings.HasPrefix(res.create.Content, "✗ that module isn't running") {
		t.Errorf("a forged module name: got %+v", res)
	}
	if len(sw.off) != 0 {
		t.Errorf("a refused switch changed something: %v", sw.off)
	}
	res = click(t, newRouter(t, &toggles{off: map[string]bool{}, fail: true}, nest), "help:off:nest", true, true)
	if res.create == nil || !strings.HasPrefix(res.create.Content, "✗ something went wrong on skua's side") {
		t.Errorf("a failed Set: got %+v", res)
	}
}

// Every module's real page keeps the voice (UX.md) and fits the grid. This
// test reads the modules; the package itself never does.
func TestEveryPageKeepsTheVoice(t *testing.T) {
	generated := string([]rune{0x2014, 0x2013, 0x2026, 0x2018, 0x2019, 0x201c, 0x201d})
	oxford := regexp.MustCompile(`, [^,]+, (and|or) `)
	mods := []core.Module{
		status.New(nil, nil, nil),
		whisper.New(nil, nil, nil),
		bird.New(nil, nil, nil, ""),
		purge.New(nil, nil, slog.New(slog.DiscardHandler), snowflake.ID(0)),
		preen.New(nil),
		perf.New(obs.New()),
	}
	for _, mod := range mods {
		h, ok := mod.(core.Helper)
		if !ok {
			t.Errorf("%s has no page", mod.Name())
			continue
		}
		for _, s := range []string{h.Help().Line, h.Help().About} {
			switch {
			case s == "":
				t.Errorf("%s: empty", mod.Name())
			case s != strings.ToLower(s):
				t.Errorf("%s: %q is not lowercase", mod.Name(), s)
			case strings.ContainsAny(s, generated+"!"):
				t.Errorf("%s: %q has a generated mark or an exclamation", mod.Name(), s)
			case oxford.MatchString(s):
				t.Errorf("%s: %q has an oxford comma", mod.Name(), s)
			case strings.HasSuffix(s, "."):
				t.Errorf("%s: %q ends on a full stop", mod.Name(), s)
			case strings.Count(s, ";") > 1:
				t.Errorf("%s: %q chains clauses with semicolons; say it in sentences", mod.Name(), s)
			}
		}
		// The index line is one plain phrase: a tag after a comma ("takes
		// back what you said, all of it") reads as written, not said.
		if strings.ContainsAny(h.Help().Line, ",;:") {
			t.Errorf("%s: line %q has a comma, semicolon or colon", mod.Name(), h.Help().Line)
		}
		// A select option's description stops at 100.
		if utf8.RuneCountInString(h.Help().Line) > 100 {
			t.Errorf("%s: line over 100", mod.Name())
		}
		for _, line := range strings.Split(grid(mod.Commands()), "\n") {
			if utf8.RuneCountInString(line) > 40 {
				t.Errorf("%s: grid line %q is over 40 columns", mod.Name(), line)
			}
		}
	}
}
