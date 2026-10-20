package pp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// Grants are how admins reward contest winners: always out of the contest
// fund, which grows with every quest paid, under the charter's limits, and
// every one posted in the ledger channel. A grant over GrantSecond pot days waits there for
// a second admin to approve it.

const grantForm = "ppgrant"

var (
	errNoLedger   = core.Tell("pick a ledger channel on this page first: every grant is posted there")
	errGrantAdmin = core.Tell("grants never go to an admin, the one granting included")
	errGrantBot   = core.Tell("grants go to members, not bots")
	errGrantWho   = core.Tell("pick who the grant is for")
	errGrantSize  = core.Tell("say how many pot days, like 2 or 0.5")
	errGrantGone  = core.Tell("that grant was already settled")
	errGrantSelf  = core.Tell("a second admin has to approve this one, not the one who asked for it")
)

// grantControls is the grant button for /pp's help page.
func (m *Module) grantControls() []discord.ContainerSubComponent {
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay(fmt.Sprintf("### grants\n-# pay a contest winner from the contest fund, which grows with every quest: at most %s pp a prize and %d pot days a season to anyone. a second admin approves anything over %d pot day, and every grant is posted in the ledger channel",
			grouped(Rules.Prize(m.st.bal())/unit), Rules.GrantSeason, Rules.GrantSecond)),
		discord.NewActionRow(withIcon(discord.NewSecondaryButton("grant pp", "pp:grant"), "envelope")),
	}
}

// grantModal asks who, how much and why.
func grantModal() discord.ModalCreate {
	return discord.ModalCreate{
		CustomID: grantForm,
		Title:    "grant pp from the contest fund",
		Components: []discord.LayoutComponent{
			discord.NewLabel("who", discord.UserSelectMenuComponent{CustomID: "to", MaxValues: 1}),
			discord.NewLabel("pot days", discord.TextInputComponent{
				CustomID: "amount", Style: discord.TextInputStyleShort, Required: true, MaxLength: 6, Placeholder: "2",
			}),
			discord.NewLabel("what for", discord.TextInputComponent{
				CustomID: "reason", Style: discord.TextInputStyleShort, Required: true, MaxLength: 120,
				Placeholder: "won the october screenshot contest",
			}),
		},
	}
}

// grant is one grant: who asked, for whom, how much and why.
type grant struct {
	id               int64
	from, to, second snowflake.ID
	amount           int64
	reason           string
}

// potDays reads an amount of pot days as pp hundredths.
func potDays(s string) (int64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(s, ",", ".")), 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || f > 1000 {
		return 0, false
	}
	return int64(f * float64(Default.PotDay(Rules))), true
}

// submitGrant takes the form: under GrantSecond pot days it pays at once,
// over it it waits for a second admin in the ledger channel.
func (m *Module) submitGrant(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil || m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	users := e.Data.Users("to")
	if len(users) != 1 {
		return errGrantWho
	}
	to := users[0]
	amount, ok := potDays(e.Data.Text("amount"))
	if !ok {
		return errGrantSize
	}
	switch rm, ok := e.Data.Resolved.Members[to.ID]; {
	case to.Bot:
		return errGrantBot
	case to.ID == e.User().ID, ok && rm.Permissions.Has(discord.PermissionAdministrator):
		return errGrantAdmin
	}
	g := grant{from: e.User().ID, to: to.ID, amount: amount, reason: strings.TrimSpace(e.Data.Text("reason"))}
	if err := m.canGrant(*guild, g); err != nil {
		return err
	}
	if amount <= Rules.GrantSecond*m.st.pot() {
		if err := m.pay(*guild, g); err != nil {
			return err
		}
		return e.CreateMessage(said(fmt.Sprintf("✓ <@%s> got %s pp from the contest fund", g.to, grouped(amount/unit))))
	}
	if err := queryOne(ctx, m.db, `insert into pp_grant (guild_id, from_id, to_id, amount, reason) values ($1, $2, $3, $4, $5) returning id`,
		[]any{int64(*guild), int64(g.from), int64(g.to), g.amount, g.reason}, &g.id); err != nil {
		return err
	}
	if err := m.postGrant(*guild, g, true); err != nil {
		return err
	}
	return e.CreateMessage(said(fmt.Sprintf("! %s pp for <@%s> is waiting in the ledger channel for a second admin to approve", grouped(amount/unit), g.to)))
}

// canGrant checks g against the charter: a ledger channel to post in, a
// prize no bigger than the biggest, room in the contest fund and in the
// member's season. It changes nothing.
func (m *Module) canGrant(guild snowflake.ID, g grant) error {
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	return m.checkGrant(s, g)
}

// checkGrant is canGrant with s.mu held.
func (m *Module) checkGrant(s *server, g grant) error {
	season, _ := m.st.bal().Season(DayOf(m.now()))
	if s.logChannel == 0 {
		return errNoLedger
	}
	if most := Rules.Prize(m.st.bal()); g.amount > most {
		return core.Tell(fmt.Sprintf("a prize is at most %s pp, %d golden quests", grouped(most/unit), Rules.PrizeMax))
	}
	if g.amount > s.fund {
		return core.Tell(fmt.Sprintf("the contest fund holds %s pp: it grows with every quest paid", grouped(s.fund/unit)))
	}
	var had int64
	if mem, ok := s.members[g.to]; ok && mem.grantSeason == season {
		had = mem.granted
	}
	if room := Rules.GrantSeason*m.st.pot() - had; g.amount > room {
		return core.Tell(fmt.Sprintf("<@%s> can get %s pp more in grants this season", g.to, grouped(max(room, 0)/unit)))
	}
	return nil
}

// pay moves g from the contest fund into the member's purse, both into the
// ledger, and posts it. The level doesn't count it: levels are time. It
// checks g under the lock it pays under, so two grants can't both fit in
// room for one, and saves at once: a waiting grant is marked paid in the
// same transaction as its ledger rows.
func (m *Module) pay(guild snowflake.ID, g grant) error {
	today := DayOf(m.now())
	season, _ := m.st.bal().Season(today)
	s := m.st.server(guild)
	s.mu.Lock()
	if err := m.checkGrant(s, g); err != nil {
		s.mu.Unlock()
		return err
	}
	mem := m.st.member(s, guild, g.to, today)
	if mem.grantSeason != season {
		mem.grantSeason, mem.granted = season, 0
	}
	s.treasury -= g.amount
	s.fund -= g.amount
	mem.purse += g.amount
	mem.granted += g.amount
	s.append(0, today, reasonGrant, -g.amount, []int64{int64(g.to), int64(g.from), int64(g.second)})
	s.append(g.to, today, reasonGrant, g.amount, []int64{int64(g.from), int64(g.second)})
	mem.dirty, s.dirty = true, true
	if g.id != 0 {
		s.paid = append(s.paid, g.id)
	}
	s.mu.Unlock()
	m.Save()
	// Paid is paid; a post that fails is logged, and the ledger holds it.
	if err := m.postGrant(guild, g, false); err != nil {
		m.log.Warn("pp: posting a grant", "guild", guild, "err", err)
	}
	return nil
}

// grantCard is a grant as the ledger channel shows it: waiting, with the
// buttons to settle it, or done.
func grantCard(g grant, waiting bool, note string) discord.ContainerComponent {
	head := fmt.Sprintf("<@%s> got %s pp from the contest fund", g.to, grouped(g.amount/unit))
	color := brand.ColorOK
	who := fmt.Sprintf("granted by <@%s>", g.from)
	if g.second != 0 {
		who += fmt.Sprintf(", approved by <@%s>", g.second)
	}
	var extra []discord.ContainerSubComponent
	if waiting {
		head = fmt.Sprintf("<@%s> asks to grant <@%s> %s pp from the contest fund", g.from, g.to, grouped(g.amount/unit))
		color, who = brand.ColorWarn, "a second admin approves or refuses it"
		id := strconv.FormatInt(g.id, 10)
		extra = append(extra, discord.NewActionRow(discord.NewSuccessButton("approve", "pp:approve:"+id), discord.NewDangerButton("refuse", "pp:refuse:"+id)))
	}
	if note != "" {
		head = note
	}
	return brand.Card(color, Name, "grant", head+"\n-# "+line(g.reason)+" · "+who, extra...)
}

// line keeps a member's text to one line.
func line(s string) string { return strings.Join(strings.Fields(s), " ") }

// postGrant posts g in the ledger channel through the guard.
func (m *Module) postGrant(guild snowflake.ID, g grant, waiting bool) error {
	s := m.st.server(guild)
	s.mu.Lock()
	ch := s.logChannel
	s.mu.Unlock()
	if m.rest == nil {
		return nil
	}
	if err := m.Guard.Allow(guild, guard.MessageSend); err != nil {
		return err
	}
	_, err := m.rest.CreateMessage(ch, discord.MessageCreate{
		Components:      []discord.LayoutComponent{grantCard(g, waiting, "")},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
	m.Guard.Report(guild, struggling(err))
	return err
}

// settle is a second admin approving or refusing a waiting grant on its
// card: approving pays it, either way the card says how it ended.
func (m *Module) settle(ctx context.Context, guild snowflake.ID, e *events.ComponentInteractionCreate, approve bool) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	id, err := strconv.ParseInt(e.Data.CustomID()[strings.LastIndex(e.Data.CustomID(), ":")+1:], 10, 64)
	if err != nil {
		return errGrantGone
	}
	var g grant
	var from, to int64
	err = queryOne(ctx, m.db, `select from_id, to_id, amount, reason from pp_grant where id = $1 and guild_id = $2 and state = 'waiting'`,
		[]any{id, int64(guild)}, &from, &to, &g.amount, &g.reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return errGrantGone
	}
	if err != nil {
		return err
	}
	g.id, g.from, g.to = id, snowflake.ID(from), snowflake.ID(to)
	state, note := "refused", fmt.Sprintf("✗ <@%s> refused %s pp for <@%s>", e.User().ID, grouped(g.amount/unit), g.to)
	if approve {
		if e.User().ID == g.from {
			return errGrantSelf
		}
		g.second = e.User().ID
		if err := m.canGrant(guild, g); err != nil {
			return err
		}
		state, note = "paying", ""
	}
	// Claim the row first, so a double press can't pay twice. A paying row
	// turns paid in the flush that writes its ledger rows; one a crash
	// left paying waits again at the next boot.
	tag, err := m.db.Exec(ctx, `update pp_grant set state = $3, second_id = $4 where id = $1 and guild_id = $2 and state = 'waiting'`,
		id, int64(guild), state, int64(e.User().ID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errGrantGone
	}
	if approve {
		if err := m.pay(guild, g); err != nil {
			// Unpaid, so it waits again.
			_, _ = m.db.Exec(ctx, `update pp_grant set state = 'waiting', second_id = 0 where id = $1 and state = 'paying'`, id)
			return err
		}
		note = fmt.Sprintf("✓ <@%s> got %s pp from the contest fund", g.to, grouped(g.amount/unit))
	}
	return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(grantCard(g, false, note))))
}

// grantsRow is the contest fund for the help page. s.mu held.
func grantsRow(s *server) string {
	return grouped(s.fund/unit) + " pp in the contest fund"
}

// queryOne scans the one row sql returns into dest, pgx.ErrNoRows if none.
func queryOne(ctx context.Context, db DB, sql string, args []any, dest ...any) error {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return pgx.ErrNoRows
	}
	return rows.Scan(dest...)
}

// said is an ephemeral line for the admin who acted.
func said(s string) discord.MessageCreate {
	return discord.MessageCreate{Content: s, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()}
}
