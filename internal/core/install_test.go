package core

import (
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"

	"github.com/6586x57890143/cygnet/link"
)

type permsMod struct {
	mod
	p discord.Permissions
}

func (m permsMod) Perms() discord.Permissions { return m.p }

func TestInstallIsTheUnionOfRunningModules(t *testing.T) {
	got := Install([]Module{permsMod{p: discord.PermissionViewChannel}, permsMod{p: discord.PermissionManageWebhooks}, permsMod{}})
	if got.Permissions != discord.PermissionViewChannel|discord.PermissionManageWebhooks {
		t.Errorf("permissions = %d", got.Permissions)
	}
	if !sameInstall(got, discord.InstallParams{Scopes: []discord.OAuth2Scope{discord.OAuth2ScopeBot, discord.OAuth2ScopeApplicationsCommands}, Permissions: got.Permissions}) {
		t.Errorf("scopes = %v, want bot and applications.commands", got.Scopes)
	}
}

func TestManifestIsWhatIsRunning(t *testing.T) {
	cmds := []Command{
		{Create: discord.SlashCommandCreate{Name: "ping"}, Tier: Public},
		{Create: discord.SlashCommandCreate{Name: "status"}, Tier: Admin},
		{Create: discord.MessageCommandCreate{Name: "purge"}, Tier: BreakGlass},
	}
	got := Manifest([]Module{permsMod{mod{cmds}, discord.PermissionManageWebhooks}, permsMod{p: discord.PermissionViewChannel}}, gateway.IntentGuilds|gateway.IntentGuildMessages)
	if got.Perms != strconv.FormatUint(uint64(discord.PermissionManageWebhooks|discord.PermissionViewChannel), 10) {
		t.Errorf("perms = %s", got.Perms)
	}
	if got.Intents != uint64(gateway.IntentGuilds|gateway.IntentGuildMessages) || got.Build != Revision() {
		t.Errorf("intents %d, build %q", got.Intents, got.Build)
	}
	want := []link.Command{{Name: "ping", Tier: link.Public}, {Name: "status", Tier: link.Admin}, {Name: "purge", Tier: link.BreakGlass}}
	if !slices.Equal(got.Commands, want) {
		t.Errorf("commands = %v", got.Commands)
	}
	if empty := Manifest(nil, 0); empty.Commands == nil || empty.Perms != "0" {
		t.Errorf("no modules: %+v, want an empty list and perms 0", empty)
	}
}

type appRest struct {
	rest.Rest
	updates []discord.ApplicationUpdate
	err     error
}

func (a *appRest) UpdateCurrentApplication(u discord.ApplicationUpdate, _ ...rest.RequestOpt) (*discord.Application, error) {
	a.updates = append(a.updates, u)
	return &discord.Application{}, a.err
}

func TestSyncInstallWritesOnlyWhenDiscordDiffers(t *testing.T) {
	want := Install([]Module{permsMod{p: discord.PermissionManageWebhooks}})
	guild, user := discord.ApplicationIntegrationTypeGuildInstall, discord.ApplicationIntegrationTypeUserInstall
	in := func(p discord.InstallParams) discord.ApplicationIntegrationTypeConfiguration {
		return discord.ApplicationIntegrationTypeConfiguration{OAuth2InstallParams: &p}
	}
	reordered := want
	reordered.Scopes = []discord.OAuth2Scope{discord.OAuth2ScopeBot, discord.OAuth2ScopeApplicationsCommands}
	fewer := want
	fewer.Permissions = 0

	cases := []struct {
		name  string
		have  discord.ApplicationIntegrationTypesConfig
		write bool
	}{
		{"nothing set (a new app)", nil, true},
		{"guild entry without params", discord.ApplicationIntegrationTypesConfig{guild: {}}, true},
		{"stale permissions", discord.ApplicationIntegrationTypesConfig{guild: in(fewer)}, true},
		{"user install still on", discord.ApplicationIntegrationTypesConfig{guild: in(want), user: {}}, true},
		{"already right", discord.ApplicationIntegrationTypesConfig{guild: in(want)}, false},
		{"already right, scopes in another order", discord.ApplicationIntegrationTypesConfig{guild: in(reordered)}, false},
	}
	for _, c := range cases {
		r := &appRest{}
		wrote, err := SyncInstall(r, &discord.Application{IntegrationTypesConfig: c.have}, want)
		if err != nil || wrote != c.write || len(r.updates) != map[bool]int{false: 0, true: 1}[c.write] {
			t.Errorf("%s: wrote=%v err=%v updates=%d", c.name, wrote, err, len(r.updates))
			continue
		}
		if c.write {
			cfg := *r.updates[0].IntegrationTypesConfig
			if len(cfg) != 1 || cfg[guild].OAuth2InstallParams == nil || !sameInstall(*cfg[guild].OAuth2InstallParams, want) {
				t.Errorf("%s: sent %+v, want guild only with %+v", c.name, cfg, want)
			}
		}
	}

	r := &appRest{err: errors.New("503")}
	if wrote, err := SyncInstall(r, &discord.Application{}, want); err == nil || wrote {
		t.Errorf("a failed write: wrote=%v err=%v", wrote, err)
	}
}
