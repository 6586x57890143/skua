package pp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// The herald posts what makes /pp checkable from outside it: the ledger's
// head each day and each season's seed, committed before and revealed
// after. A post is only as good as where it lands, so it goes to a channel
// the server's admins pick, where members can read and quote it.

// logChannels are where the herald can post.
var logChannels = []discord.ChannelType{discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews}

// logControls is the log channel picker for /pp's help page.
func (m *Module) logControls(guild snowflake.ID) []discord.ContainerSubComponent {
	s := m.st.server(guild)
	s.mu.Lock()
	ch := s.logChannel
	s.mu.Unlock()
	pick := discord.ChannelSelectMenuComponent{CustomID: "pp:log", Placeholder: "pick where the ledger is posted", ChannelTypes: logChannels, MinValues: new(0), MaxValues: 1}
	if ch != 0 {
		pick.DefaultValues = []discord.SelectMenuDefaultValue{discord.NewSelectMenuDefaultChannel(ch)}
	}
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay("### ledger\n-# every day she posts the ledger's head here, and each season's seed before and after, so anyone can check the points"),
		discord.NewActionRow(pick),
	}
}

// setLog keeps the channel an admin picked, or none when they cleared it.
func (m *Module) setLog(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	data, ok := e.Data.(discord.ChannelSelectMenuInteractionData)
	if !ok {
		return errNotAdmin
	}
	var ch snowflake.ID
	if len(data.Values) > 0 {
		ch = data.Values[0]
	}
	if _, err := m.db.Exec(ctx, `insert into pp_guild (guild_id, log_channel) values ($1, $2)
		on conflict (guild_id) do update set log_channel = excluded.log_channel`, int64(guild), int64(ch)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	s.logChannel = ch
	s.mu.Unlock()
	said := "the ledger isn't posted anywhere now"
	if ch != 0 {
		said = "the ledger's head and the season seeds go to <#" + ch.String() + "> now"
	}
	return e.CreateMessage(discord.MessageCreate{Content: said, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}

// post sends a herald's card to guild's log channel, if it has one,
// through the guard.
func (m *Module) post(guild snowflake.ID, what, body string) {
	s := m.st.server(guild)
	s.mu.Lock()
	ch := s.logChannel
	s.mu.Unlock()
	if ch == 0 || m.rest == nil {
		return
	}
	err := m.Guard.Allow(guild, guard.MessageSend)
	if err == nil {
		_, err = m.rest.CreateMessage(ch, discord.MessageCreate{
			Components:      []discord.LayoutComponent{brand.Card(brand.ColorInfo, Name, what, body)},
			Flags:           discord.MessageFlagIsComponentsV2,
			AllowedMentions: core.NoPings(),
		})
		m.Guard.Report(guild, struggling(err))
	}
	if err != nil {
		m.log.Warn("pp: posting to the ledger channel", "guild", guild, "channel", ch, "err", err)
	}
}

// struggling is whether err is Discord being busy rather than refusing.
func struggling(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	return ok && re.Response != nil && (re.Response.StatusCode == http.StatusTooManyRequests || re.Response.StatusCode >= 500)
}

// audit holds every purse to its ledger, loudly when any disagree. It runs
// from the loop, after a save, so no rows land mid-audit.
func (m *Module) audit(ctx context.Context) {
	bad, err := m.st.audit(ctx, m.db)
	switch {
	case err != nil:
		m.log.Warn("pp: auditing the ledger", "err", err)
	case bad > 0:
		m.log.Error("pp: purses disagree with the ledger", "members", bad)
	}
}

// heads posts each server's ledger head once day has turned and the rows
// of the day before are written.
func (m *Module) heads(day int64) {
	for g, s := range m.st.servers() {
		s.mu.Lock()
		// A head is posted only once every row under it is written.
		seq, head, ready := s.seq, s.headHex(), len(s.pending) == 0
		s.mu.Unlock()
		if !ready || seq == 0 || !m.enabled(g) {
			continue
		}
		closed := dayDate(day - 1)
		m.post(g, "ledger", fmt.Sprintf("%s is closed\n```\nrows  %s\nhead  %s\n```", closed, grouped(seq), head))
	}
}

// seasonTick moves the season seeds along and posts what it commits and
// reveals to every server.
func (m *Module) seasonTick(ctx context.Context, now time.Time) {
	news, err := m.st.tickSeeds(ctx, m.db, m.beacon, now)
	if err != nil {
		m.log.Warn("pp: season seeds", "err", err)
	}
	for g := range m.st.servers() {
		if !m.enabled(g) {
			continue
		}
		for _, ss := range news.committed {
			m.post(g, fmt.Sprintf("season %d seed", ss.season), fmt.Sprintf(
				"every roll in season %d comes from a secret she has just picked; here is its sha256, and the secret itself comes out when the season ends\n```\n%s\n```\n-# drand round %d, a day before the season starts, is mixed in too",
				ss.season, ss.commitment(), ss.round))
		}
		for _, ss := range news.revealed {
			m.post(g, fmt.Sprintf("season %d seed", ss.season), fmt.Sprintf(
				"season %d is over, so here is the secret behind its rolls; its sha256 is what she posted before it began\n```\nsecret  %x\nbeacon  %x\n```",
				ss.season, ss.secret, ss.randomness))
		}
	}
}
