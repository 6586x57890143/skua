package core

import (
	"slices"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
)

// Install is what adding skua to a server asks for: the bot and its slash
// commands, with exactly the permissions the running modules declare. A
// module skipped for a missing intent asks for nothing.
func Install(running []Module) discord.InstallParams {
	var p discord.Permissions
	for _, m := range running {
		p |= m.Perms()
	}
	return discord.InstallParams{
		Scopes:      []discord.OAuth2Scope{discord.OAuth2ScopeApplicationsCommands, discord.OAuth2ScopeBot},
		Permissions: p,
	}
}

// SyncInstall makes want the app's Default Install Settings, which is what
// Discord's bare install link (and skua.melting.lol, which redirects to it)
// asks a server for. Guild install only: user install is switched off,
// since every command is guild-only. It writes only when Discord's copy
// differs, so a restart costs one comparison, and reports whether it wrote.
func SyncInstall(r rest.Applications, app *discord.Application, want discord.InstallParams) (bool, error) {
	guild := discord.ApplicationIntegrationTypeGuildInstall
	if have, ok := app.IntegrationTypesConfig[guild]; ok && len(app.IntegrationTypesConfig) == 1 &&
		have.OAuth2InstallParams != nil && sameInstall(*have.OAuth2InstallParams, want) {
		return false, nil
	}
	_, err := r.UpdateCurrentApplication(discord.ApplicationUpdate{
		IntegrationTypesConfig: &discord.ApplicationIntegrationTypesConfig{guild: {OAuth2InstallParams: &want}},
	})
	return err == nil, err
}

func sameInstall(a, b discord.InstallParams) bool {
	if a.Permissions != b.Permissions || len(a.Scopes) != len(b.Scopes) {
		return false
	}
	x, y := slices.Clone(a.Scopes), slices.Clone(b.Scopes)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
