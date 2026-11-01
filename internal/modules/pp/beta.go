package pp

import (
	"context"
	"slices"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// plays reports whether i may play /pp: anyone once an admin has opened
// it, and while it is in closed beta its server's admins, skua's
// break-glass admin, and members holding the server's beta role.
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
	beta, open := s.beta, s.open
	s.mu.Unlock()
	return open || beta != 0 && slices.Contains(member.RoleIDs, beta)
}

// betaControls is the beta role picker for /pp's help page, which shows it
// to admins only, with the switch that opens /pp to everyone and the
// button that issues beta keys until it's opened, and the sealed letters
// always. They share a section because the page holds at most 40
// components.
func (m *Module) betaControls(guild snowflake.ID) []discord.ContainerSubComponent {
	s := m.st.server(guild)
	s.mu.Lock()
	beta, open := s.beta, s.open
	s.mu.Unlock()
	letters := m.letterText(guild)
	if open {
		return []discord.ContainerSubComponent{
			discord.NewTextDisplay("### open\n-# /pp is open to everyone here\n" + letters),
			discord.NewActionRow(lettersButton()),
		}
	}
	pick := discord.NewRoleSelectMenu("pp:beta", "pick the beta testers' role").WithMinValues(0)
	if beta != 0 {
		pick = pick.AddDefaultValue(beta)
	}
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay("### beta testers\n-# /pp is in closed beta: admins play, and so do members with this role. a beta key lets one member in: they redeem it on /pp and get the role. a key works once and skua keeps only its hash, so copy them when they're shown. opening /pp lets every member play, once, and can't be undone\n" + letters),
		discord.NewActionRow(pick),
		discord.NewActionRow(discord.NewPrimaryButton("open /pp to everyone", "pp:open"), keysButton(), lettersButton()),
	}
}

// openPP opens /pp to everyone in guild, for good.
func (m *Module) openPP(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, open) values ($1, true)
		on conflict (guild_id) do update set open = true`, int64(guild)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	s.open = true
	s.mu.Unlock()
	m.log.Info("pp: opened to everyone", "guild", guild)
	return e.CreateMessage(discord.MessageCreate{Content: "/pp is open: every member here can play now",
		Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
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

// enlist gives member the beta role on their first /pp once /pp is open
// here, so everyone who played from the start carries it. It's asked of
// merlin in the background and never holds the reply up; a refusal is
// logged and asked again on a later /pp, within redeemTries an hour.
func (m *Module) enlist(guild snowflake.ID, member *discord.ResolvedMember) {
	s := m.st.server(guild)
	s.mu.Lock()
	role, open := s.beta, s.open
	s.mu.Unlock()
	if !open || role == 0 || m.Grant == nil || member == nil || slices.Contains(member.RoleIDs, role) {
		return
	}
	user := member.User.ID
	if !m.asks.take(user, m.now()) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.Grant(ctx, guild, user, role); err != nil {
			m.log.Warn("pp: the beta role didn't come through", "guild", guild, "user", user, "err", err)
			return
		}
		m.log.Info("pp: enlisted in the beta", "guild", guild, "user", user)
	}()
}
