package pp

import (
	"context"
	"slices"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// plays reports whether i may play /pp while it is in closed beta: its
// server's admins, skua's break-glass admin, and members holding the
// server's beta role.
func (m *Module) plays(i discord.Interaction) bool {
	if m.Admin != nil && m.Admin(i) {
		return true
	}
	guild, member := i.GuildID(), i.Member()
	if guild == nil || member == nil {
		return false
	}
	s := m.st.server(*guild)
	s.mu.Lock()
	beta := s.beta
	s.mu.Unlock()
	return beta != 0 && slices.Contains(member.RoleIDs, beta)
}

// betaControls is the beta role picker for /pp's help page, which shows it
// to admins only.
func (m *Module) betaControls(guild snowflake.ID) []discord.ContainerSubComponent {
	s := m.st.server(guild)
	s.mu.Lock()
	beta := s.beta
	s.mu.Unlock()
	pick := discord.NewRoleSelectMenu("pp:beta", "pick the beta testers' role").WithMinValues(0)
	if beta != 0 {
		pick = pick.AddDefaultValue(beta)
	}
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay("### beta testers\n-# /pp is in closed beta: admins play, and so do members with this role"),
		discord.NewActionRow(pick),
	}
}

// setBeta keeps the role an admin picked, or none when they cleared it,
// and says so.
func (m *Module) setBeta(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	data, ok := e.Data.(discord.RoleSelectMenuInteractionData)
	if !ok {
		return errNotAdmin
	}
	var role snowflake.ID
	if len(data.Values) > 0 {
		role = data.Values[0]
	}
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, beta_role) values ($1, $2)
		on conflict (guild_id) do update set beta_role = excluded.beta_role`, int64(guild), int64(role)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	s.beta = role
	s.mu.Unlock()
	said := "no beta role now: only admins play /pp here"
	if role != 0 {
		said = "members with <@&" + role.String() + "> play /pp here now, beside admins"
	}
	return e.CreateMessage(discord.MessageCreate{Content: said, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}
