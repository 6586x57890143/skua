package pp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// The launch. An admin arms it on /pp's help page, and at the next turn
// /pp opens to every member with everything new for the season: the slack
// tide, sealed letters, the morning sheet, the painted stair and yard, the
// warm side and the standing order. Until then admins see it all and
// nobody else does, as the yard was before it opened. Something new that
// waits for it asks seesNew or released, and its presses go in
// launchFronts. It's kept as the
// pp day it goes live, so nothing has to run at the turn, and a restart
// over it changes nothing.

var (
	errNotYet    = core.Tell("that opens with the season, at the turn")
	errLaunched  = core.Tell("/pp is open here already, with everything in it")
	launchFronts = []string{"pp:tide", "pp:table", "pp:rail", "pp:letter", "pp:seal", "pp:yardrenew"}
)

// released is whether s's launch has come: armed for today or before, or
// opened outright. s.mu held.
func (s *server) released(today int64) bool { return s.open || s.launch != 0 && today >= s.launch }

// released is whether guild's launch has come.
func (m *Module) released(guild snowflake.ID) bool {
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.released(DayOf(m.now()))
}

// seesNew is whether i sees what's new in guild: everyone once it's
// launched, its admins before.
func (m *Module) seesNew(i discord.Interaction, guild snowflake.ID) bool {
	return m.released(guild) || m.Admin != nil && m.Admin(i)
}

// launchOnly is whether a press with id is one of the launch's, refused
// to members before it.
func launchOnly(id string) bool {
	return strings.HasPrefix(id, "pp:sit:") || slices.Contains(launchFronts, id)
}

// launchButton arms the launch, or disarms one armed.
func launchButton(armed bool) discord.ButtonComponent {
	if armed {
		return withIcon(discord.NewSuccessButton("armed for the turn", "pp:arm"), "bell")
	}
	return withIcon(discord.NewPrimaryButton("arm the launch", "pp:arm"), "bell")
}

// launchWord is the launch as the help page tells it, armed or not.
func launchWord(launch int64) string {
	if launch == 0 {
		return "arming the launch opens /pp to every member at the next turn, with everything new for the season. until then admins see it all and nobody else does"
	}
	return fmt.Sprintf("**armed** /pp opens to every member <t:%d:R>, at the turn, with everything new for the season. press again to disarm", dayStart(launch).Unix())
}

// armLaunch arms guild's launch for the next turn, or disarms it.
func (m *Module) armLaunch(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	if s.released(today) {
		s.mu.Unlock()
		return errLaunched
	}
	launch := today + 1
	if s.launch != 0 {
		launch = 0
	}
	s.mu.Unlock()
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, launch) values ($1, $2)
		on conflict (guild_id) do update set launch = excluded.launch`, int64(guild), launch); err != nil {
		return err
	}
	s.mu.Lock()
	s.launch = launch
	s.mu.Unlock()
	said := "disarmed: /pp stays as it is"
	if launch != 0 {
		said = fmt.Sprintf("armed: /pp opens to every member <t:%d:R>, at the turn, with everything new for the season", dayStart(launch).Unix())
	}
	m.log.Info("pp: the launch", "guild", guild, "by", e.User().ID, "day", launch)
	return e.CreateMessage(discord.MessageCreate{Content: said, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}
