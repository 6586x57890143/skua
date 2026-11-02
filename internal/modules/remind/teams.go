package remind

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// A team is a list of members a reminder can ping, kept by skua so no one
// needs a role for it: an admin makes one for a channel, skua posts its
// card there, and members join or leave from the card. A reminder for a
// team posts in its channel and pings exactly who is on it.

const (
	teamID = "remind-team"
	// perTeam is the most members a team holds: its pings fit one line.
	perTeam = 50
	// teamsPer is the most teams a server keeps: one select's worth.
	teamsPer = 25
)

type team struct {
	id                 int64
	guild, channel, by snowflake.ID
	name               string
	card               snowflake.ID
	members            []snowflake.ID
}

// scanTeam reads teamCols, then into extra whatever the query adds.
func scanTeam(row pgx.Row, extra ...any) (team, error) {
	var t team
	var guild, ch, by, card int64
	err := row.Scan(append([]any{&t.id, &guild, &t.name, &ch, &by, &card}, extra...)...)
	t.guild, t.channel, t.by, t.card = snowflake.ID(guild), snowflake.ID(ch), snowflake.ID(by), snowflake.ID(card)
	return t, err
}

const teamCols = "id, guild_id, name, channel_id, created_by, card_id"

// teamOf is team id with its members, or false when it is gone.
func teamOf(ctx context.Context, db DB, id int64) (team, bool, error) {
	t, err := scanTeam(db.QueryRow(ctx, "select "+teamCols+" from remind_team where id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, false, nil
	}
	if err != nil {
		return t, false, err
	}
	rows, err := db.Query(ctx, "select user_id from remind_team_member where team_id = $1 order by user_id", id)
	if err != nil {
		return t, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var u int64
		if err := rows.Scan(&u); err != nil {
			return t, false, err
		}
		t.members = append(t.members, snowflake.ID(u))
	}
	return t, true, rows.Err()
}

// teamsOf is guild's teams by name, each with how many are on it.
func teamsOf(ctx context.Context, db DB, guild snowflake.ID) ([]team, []int, error) {
	rows, err := db.Query(ctx, `select t.id, t.guild_id, t.name, t.channel_id, t.created_by, t.card_id, count(m.user_id)
		from remind_team t left join remind_team_member m on m.team_id = t.id
		where t.guild_id = $1 group by t.id order by lower(t.name) limit $2`, int64(guild), teamsPer)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ts []team
	var ns []int
	for rows.Next() {
		var n int
		t, err := scanTeam(rows, &n)
		if err != nil {
			return nil, nil, err
		}
		ts, ns = append(ts, t), append(ns, n)
	}
	return ts, ns, rows.Err()
}

func addTeam(ctx context.Context, db DB, t team) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, "insert into remind_team (guild_id, name, channel_id, created_by) values ($1, $2, $3, $4) returning id",
		int64(t.guild), t.name, int64(t.channel), int64(t.by)).Scan(&id)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return 0, core.Tell("this server already has a team called " + t.name)
	}
	return id, err
}

// dropTeam deletes team id and the reminders that ping it.
func dropTeam(ctx context.Context, db DB, id int64) error {
	if _, err := db.Exec(ctx, "delete from remind where target = 'team' and target_id = $1", id); err != nil {
		return err
	}
	_, err := db.Exec(ctx, "delete from remind_team where id = $1", id)
	return err
}

// enlist puts user on team id, or takes them off, within the cap.
func enlist(ctx context.Context, db DB, id int64, user snowflake.ID, on bool) error {
	if !on {
		_, err := db.Exec(ctx, "delete from remind_team_member where team_id = $1 and user_id = $2", id, int64(user))
		return err
	}
	tag, err := db.Exec(ctx, `insert into remind_team_member (team_id, user_id)
		select $1, $2 where (select count(*) from remind_team_member where team_id = $1) < $3
		on conflict do nothing`, id, int64(user), perTeam)
	if err == nil && tag.RowsAffected() == 0 {
		var in bool
		if err := db.QueryRow(ctx, "select exists (select 1 from remind_team_member where team_id = $1 and user_id = $2)", id, int64(user)).Scan(&in); err != nil || in {
			return err
		}
		return core.Tell(fmt.Sprintf("that team is full at %d", perTeam))
	}
	return err
}

func setTeamCard(ctx context.Context, db DB, id int64, card snowflake.ID) error {
	_, err := db.Exec(ctx, "update remind_team set card_id = $2 where id = $1", id, int64(card))
	return err
}

// nextFor is team id's next reminder, if one is waiting.
func nextFor(ctx context.Context, db DB, id int64) (reminder, bool, error) {
	r, err := scan(db.QueryRow(ctx, "select "+cols+" from remind where target = 'team' and target_id = $1 and state = 'pending' order by due_at limit 1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// answer records user as here for one turn of a group reminder and is
// how many are.
func answer(ctx context.Context, db DB, rid int64, due time.Time, user snowflake.ID) (int, error) {
	if _, err := db.Exec(ctx, "insert into remind_here (remind_id, due_at, user_id) values ($1, $2, $3) on conflict do nothing", rid, due, int64(user)); err != nil {
		return 0, err
	}
	var n int
	err := db.QueryRow(ctx, "select count(*) from remind_here where remind_id = $1 and due_at = $2", rid, due).Scan(&n)
	return n, err
}

// teamCard is the public card members join a team from.
func teamCard(t team, next *reminder) []discord.LayoutComponent {
	rows := [][2]string{{"members", strconv.Itoa(len(t.members))}}
	if next != nil {
		rows = append(rows, [2]string{"next", stamp(next.due, "f") + " · " + stamp(next.due, "R")})
		if !next.r.once() {
			rows = append(rows, [2]string{"repeat", next.r.String()})
		}
	} else {
		rows = append(rows, [2]string{"next", "nothing set yet"})
	}
	sid := strconv.FormatInt(t.id, 10)
	return v2(brand.ColorInfo, "team", "### "+t.name+"\n"+core.Readout(rows)+"-# set up by <@"+t.by.String()+"> · join to be pinged when it's time",
		discord.NewActionRow(
			icon(discord.NewSecondaryButton("join", id+":join:"+sid), "btn_join"),
			icon(discord.NewSecondaryButton("leave", id+":leave:"+sid), "btn_leave"),
		))
}

// called is a group reminder as it fires: the team's members or the role
// pinged on the line under it, nobody else, and a here button with a
// tally of who answered.
func called(r reminder, late time.Duration, who []snowflake.ID, here int) discord.MessageCreate {
	mentions := core.NoPings()
	var pings []string
	switch r.target {
	case "team":
		for _, u := range who {
			pings = append(pings, "<@"+u.String()+">")
		}
		mentions.Users = who
	case "role":
		pings = []string{"<@&" + r.targetID.String() + ">"}
		mentions.Roles = []snowflake.ID{r.targetID}
	}
	sub := "-# " + strings.Join(pings, " ")
	if len(pings) == 0 {
		sub = "-# nobody's on this team yet"
	}
	if !r.r.once() {
		sub += "\n-# " + r.r.String()
	}
	if late >= lateBy {
		sub += "\n-# ! late by " + core.Duration(late) + "; skua was away when it was due"
	}
	if here > 0 {
		sub += "\n✓ " + strconv.Itoa(here) + " here"
	}
	key := strconv.FormatInt(r.id, 10) + ":" + strconv.FormatInt(r.due.UnixMilli(), 10)
	return discord.MessageCreate{
		Components:      v2(brand.ColorInfo, "now", "### "+r.text+"\n"+sub, discord.NewActionRow(icon(discord.NewSecondaryButton("here", id+":here:"+key), "btn_done"))),
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: mentions,
	}
}

// teamsCard is the agenda's teams screen: this server's teams, a select
// to open one, and for admins a button to make one.
func (m *Module) teamsCard(ctx context.Context, guild snowflake.ID, admin bool, note string) ([]discord.LayoutComponent, error) {
	ts, ns, err := teamsOf(ctx, m.db, guild)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for i, t := range ts {
		fmt.Fprintf(&b, "**%s** · %d on it · <#%s>\n", t.name, ns[i], t.channel)
	}
	list := strings.TrimSuffix(b.String(), "\n")
	if len(ts) == 0 {
		list = "no teams yet"
		if admin {
			list += "; make one with new team"
		}
	}
	if note != "" {
		list += "\n" + note
	}
	list += "\n-# a team is pinged by /remind with team; members join from its card"
	var extra []discord.ContainerSubComponent
	if len(ts) > 0 {
		opts := make([]discord.StringSelectMenuOption, 0, len(ts))
		for i, t := range ts {
			opts = append(opts, discord.StringSelectMenuOption{Label: clip(t.name, 100), Value: strconv.FormatInt(t.id, 10), Description: strconv.Itoa(ns[i]) + " on it"})
		}
		extra = append(extra, discord.NewActionRow(discord.NewStringSelectMenu(id+":team", "open a team", opts...)))
	}
	buttons := []discord.InteractiveComponent{agendaButton()}
	if admin && len(ts) < teamsPer {
		buttons = append(buttons, icon(discord.NewSecondaryButton("new team", id+":mkteam"), "btn_team"))
	}
	extra = append(extra, discord.NewActionRow(buttons...))
	return v2(brand.ColorInfo, "teams", list, extra...), nil
}

// teamView is one team as a member sees it from the agenda: its card's
// facts, join or leave, and for admins posting its card again or
// deleting it.
func teamView(t team, next *reminder, user snowflake.ID, admin bool, note string) []discord.LayoutComponent {
	card := teamCard(t, next)
	box := card[0].(discord.ContainerComponent)
	sid := strconv.FormatInt(t.id, 10)
	in := slices.Contains(t.members, user)
	row := []discord.InteractiveComponent{discord.NewSecondaryButton("◂ teams", id+":teams")}
	if in {
		row = append(row, icon(discord.NewSecondaryButton("leave", id+":leave:"+sid), "btn_leave"))
	} else {
		row = append(row, icon(discord.NewSecondaryButton("join", id+":join:"+sid), "btn_join"))
	}
	if admin {
		row = append(row, icon(discord.NewSecondaryButton("post card", id+":post:"+sid), "btn_new"), icon(discord.NewSecondaryButton("delete", id+":rmteam:"+sid), "btn_cancel"))
	}
	text := box.Components[0].(discord.TextDisplayComponent)
	if note != "" {
		text.Content += "\n" + note
	}
	if in {
		text.Content += "\n-# you're on it"
	}
	box.Components = []discord.ContainerSubComponent{text, discord.NewActionRow(row...)}
	return []discord.LayoutComponent{box}
}

func teamForm() discord.ModalCreate {
	one := 1
	return discord.ModalCreate{
		CustomID: teamID,
		Title:    "a new team",
		Components: []discord.LayoutComponent{
			discord.NewLabel("name", discord.TextInputComponent{CustomID: "name", Style: discord.TextInputStyleShort, Required: true, MaxLength: 40, Placeholder: "standup crew"}),
			discord.NewLabel("channel", discord.ChannelSelectMenuComponent{CustomID: "channel", ChannelTypes: textChannels, MinValues: &one, MaxValues: 1, Placeholder: "where its card and reminders go"}),
		},
	}
}

// teamClick is the teams screens' and a team card's controls. Joining
// and leaving are anyone's; making, posting and deleting a team are its
// server's admins'.
func (m *Module) teamClick(ctx context.Context, e *events.ComponentInteractionCreate, f []string) error {
	guild := e.GuildID()
	if guild == nil {
		return core.Tell("teams only work in a server")
	}
	user, admin := e.User().ID, m.admin(e)
	switch f[1] {
	case "teams":
		c, err := m.teamsCard(ctx, *guild, admin, "")
		if err != nil {
			return err
		}
		return e.UpdateMessage(v2Update(c))
	case "mkteam":
		if !admin {
			return core.Tell("only this server's admins can make teams")
		}
		return e.Modal(teamForm())
	}
	var tid int64
	var err error
	switch f[1] {
	case "team":
		v := values(e)
		if len(v) == 1 {
			tid, err = strconv.ParseInt(v[0], 10, 64)
		}
	default:
		if len(f) > 2 {
			tid, err = strconv.ParseInt(f[2], 10, 64)
		}
	}
	if err != nil || tid == 0 {
		return core.Tell("that card is out of date")
	}
	t, ok, err := teamOf(ctx, m.db, tid)
	switch {
	case err != nil:
		return err
	case !ok || t.guild != *guild:
		return core.Tell("that team is gone")
	}
	ephemeral := e.Message.Flags.Has(discord.MessageFlagEphemeral)
	redraw := func(note string) error {
		t, _, err := teamOf(ctx, m.db, tid)
		if err != nil {
			return err
		}
		var next *reminder
		if r, ok, err := nextFor(ctx, m.db, tid); err != nil {
			return err
		} else if ok {
			next = &r
		}
		if !ephemeral {
			u := v2Update(teamCard(t, next))
			u.AllowedMentions = core.NoPings()
			return e.UpdateMessage(u)
		}
		return e.UpdateMessage(v2Update(teamView(t, next, user, admin, note)))
	}
	switch f[1] {
	case "team":
		return redraw("")
	case "join", "leave":
		if f[1] == "join" && !admin && e.Channel().ID() != t.channel {
			// Joining from where the team can't be seen would let anyone
			// on a private channel's team.
			return core.Tell("join " + t.name + " from its card in <#" + t.channel.String() + ">")
		}
		if err := enlist(ctx, m.db, tid, user, f[1] == "join"); err != nil {
			return err
		}
		if ephemeral {
			go m.refresh(tid)
		}
		if f[1] == "join" {
			return redraw("✓ joined " + t.name)
		}
		return redraw("✓ left " + t.name)
	case "post":
		if !admin {
			return core.Tell("only this server's admins can post a team's card")
		}
		// Posting waits on Discord, so the press is answered first.
		if err := e.DeferUpdateMessage(); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		if err := m.postTeam(ctx, e.Client().Rest, t); err != nil {
			return err
		}
		var next *reminder
		if r, ok, err := nextFor(ctx, m.db, tid); err != nil {
			return err
		} else if ok {
			next = &r
		}
		t, _, err := teamOf(ctx, m.db, tid)
		if err != nil {
			return err
		}
		_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), v2Update(teamView(t, next, user, admin, "✓ card posted in <#"+t.channel.String()+">")), rest.WithCtx(ctx))
		return err
	case "rmteam":
		if !admin {
			return core.Tell("only this server's admins can delete a team")
		}
		if err := dropTeam(ctx, m.db, tid); err != nil {
			return err
		}
		c, err := m.teamsCard(ctx, *guild, admin, "✓ deleted "+t.name+" and its reminders")
		if err != nil {
			return err
		}
		return e.UpdateMessage(v2Update(c))
	}
	return core.Tell("that card is out of date")
}

// postTeam posts t's card in its channel, as skua.
func (m *Module) postTeam(ctx context.Context, p Poster, t team) error {
	if err := m.guard.Allow(t.guild, guard.MessageSend); err != nil {
		return fmt.Errorf("%w: %w", core.Tell("skua is posting too much in this server; try again in a few minutes"), err)
	}
	var next *reminder
	if r, ok, err := nextFor(ctx, m.db, t.id); err != nil {
		return err
	} else if ok {
		next = &r
	}
	msg, err := p.CreateMessage(t.channel, discord.MessageCreate{Components: teamCard(t, next), Flags: discord.MessageFlagIsComponentsV2, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx))
	m.guard.Done(t.guild, err)
	if err != nil {
		return fmt.Errorf("%w: %w", core.Tell("skua couldn't post in <#"+t.channel.String()+">; check she can send messages there"), err)
	}
	return setTeamCard(ctx, m.db, t.id, msg.ID)
}

// makeTeam is the new team form sent back: the team is kept and its card
// posted in its channel.
func (m *Module) makeTeam(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil || !m.admin(e) {
		return core.Tell("only this server's admins can make teams")
	}
	name := clip(oneLine(e.Data.Text("name")), 40)
	chs, _ := e.Data.OptChannels("channel")
	switch {
	case name == "":
		return core.Tell("give the team a name")
	case len(chs) != 1:
		return core.Tell("pick the team's channel")
	}
	ts, _, err := teamsOf(ctx, m.db, *guild)
	if err != nil {
		return err
	}
	if len(ts) >= teamsPer {
		return core.Tell(fmt.Sprintf("this server has %d teams, which is the most skua keeps; delete one first", teamsPer))
	}
	t := team{guild: *guild, channel: chs[0].ID, by: e.User().ID, name: name}
	if t.id, err = addTeam(ctx, m.db, t); err != nil {
		return err
	}
	// Posting its card waits on Discord, so the form is answered first.
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	note := "✓ made " + name + " and posted its card in <#" + t.channel.String() + ">"
	if err := m.postTeam(ctx, e.Client().Rest, t); err != nil {
		m.log.Warn("remind: posting a team's card", "err", err)
		note = "✓ made " + name + "; ! its card couldn't be posted in <#" + t.channel.String() + ">, so check skua can send messages there and post it from the team"
	}
	c, err := m.teamsCard(ctx, *guild, true, note)
	if err != nil {
		return err
	}
	_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), v2Update(c), rest.WithCtx(ctx))
	return err
}

// teamChoices is the team list on /remind as a member types.
func (m *Module) teamChoices(ctx context.Context, guild snowflake.ID, typed string) ([]discord.AutocompleteChoice, error) {
	ts, ns, err := teamsOf(ctx, m.db, guild)
	if err != nil {
		return nil, err
	}
	out := []discord.AutocompleteChoice{}
	for i, t := range ts[:min(len(ts), teamsPer)] {
		if strings.Contains(strings.ToLower(t.name), strings.ToLower(strings.TrimSpace(typed))) {
			out = append(out, discord.AutocompleteChoiceString{Name: clip(t.name+" · "+strconv.Itoa(ns[i])+" on it", 100), Value: strconv.FormatInt(t.id, 10)})
		}
	}
	return out, nil
}

// aimGroup aims r at the team or role /remind names, if it names one: a
// team its member or an admin may ping, in the team's channel; a role
// the member may mention, where they could post it themselves.
func (m *Module) aimGroup(ctx context.Context, e *events.ApplicationCommandInteractionCreate, r *reminder) error {
	d := e.SlashCommandInteractionData()
	tv, isTeam := d.OptString("team")
	role, isRole := d.OptRole("role")
	switch {
	case isTeam && isRole:
		return core.Tell("pick a team or a role, not both")
	case isTeam:
		if _, ok := d.OptString("to"); ok {
			return core.Tell("a team's reminder goes to its own channel; leave out to")
		}
		tid, err := strconv.ParseInt(tv, 10, 64)
		if err != nil {
			return core.Tell("pick a team from the list")
		}
		t, ok, err := teamOf(ctx, m.db, tid)
		switch {
		case err != nil:
			return err
		case !ok || t.guild != r.guild:
			return core.Tell("that team is gone")
		case !m.admin(e) && !slices.Contains(t.members, r.owner):
			return core.Tell("join " + t.name + " to remind it of something")
		case !m.admin(e) && e.Channel().ID() != t.channel:
			// The text posts in the team's channel, so it is set from
			// there, by someone who can post there.
			return core.Tell("run this in <#" + t.channel.String() + ">, the team's channel")
		case !m.admin(e) && (e.Member() == nil || !e.Member().Permissions.Has(discord.PermissionSendMessages)):
			return core.Tell("you can't post in <#" + t.channel.String() + ">, so skua won't either")
		}
		if _, ok := d.OptChannel("channel"); ok {
			return core.Tell("a team's reminder goes to its own channel; leave out channel")
		}
		r.target, r.targetID, r.channel = "team", snowflake.ID(t.id), t.channel
	case isRole:
		if _, ok := d.OptString("to"); ok {
			return core.Tell("a role's reminder goes to a channel; leave out to, or pick one with channel")
		}
		mem := e.Member()
		ch, picked := d.OptChannel("channel")
		perms := discord.Permissions(0)
		if mem != nil {
			perms = mem.Permissions
		}
		if picked {
			perms = ch.Permissions
			r.channel = ch.ID
		}
		switch {
		case role.ID == r.guild:
			return core.Tell("skua never pings everyone; pick a role")
		case !perms.Has(discord.PermissionViewChannel | discord.PermissionSendMessages):
			return core.Tell("you can't post in <#" + r.channel.String() + ">, so skua won't either")
		case !role.Mentionable:
			// skua holds no mention everyone, so only a role anyone may
			// ping is pinged at all.
			return core.Tell("@" + role.Name + " can't be mentioned; turn on allow anyone to @mention this role, or pick another")
		}
		r.target, r.targetID = "role", role.ID
	}
	return nil
}

// here is a group reminder's here button: whoever presses it is counted
// once for that turn, and the card's tally line says how many are. The
// rest of the card stays as it fired, whatever has changed since.
func (m *Module) here(ctx context.Context, e *events.ComponentInteractionCreate, f []string) error {
	if len(f) < 4 || e.GuildID() == nil {
		return core.Tell("that card is out of date")
	}
	rid, err1 := strconv.ParseInt(f[2], 10, 64)
	dueMS, err2 := strconv.ParseInt(f[3], 10, 64)
	if err1 != nil || err2 != nil || rid <= 0 {
		return core.Tell("that card is out of date")
	}
	// A tally counts only presses from the reminder's own server; one gone
	// since is counted on its card still.
	if r, ok, err := get(ctx, m.db, rid); err != nil {
		return err
	} else if ok && r.guild != *e.GuildID() {
		return core.Tell("that reminder is gone")
	}
	n, err := answer(ctx, m.db, rid, time.UnixMilli(dueMS), e.User().ID)
	if err != nil {
		return err
	}
	return e.UpdateMessage(tallied(e.Message.Components, n))
}

// tallied is a fired group card's components with its tally line set to
// n here, everything else as it was.
func tallied(cs []discord.LayoutComponent, n int) discord.MessageUpdate {
	out := make([]discord.LayoutComponent, 0, len(cs))
	for _, c := range cs {
		box, ok := c.(discord.ContainerComponent)
		if ok {
			subs := slices.Clone(box.Components)
			for i, s := range subs {
				if t, ok := s.(discord.TextDisplayComponent); ok {
					lines := slices.DeleteFunc(strings.Split(t.Content, "\n"), func(l string) bool {
						return strings.HasPrefix(l, "✓ ") && strings.HasSuffix(l, " here")
					})
					t.Content = strings.Join(append(lines, "✓ "+strconv.Itoa(n)+" here"), "\n")
					subs[i] = t
					break
				}
			}
			box.Components = subs
			c = box
		}
		out = append(out, c)
	}
	u := v2Update(out)
	u.AllowedMentions = core.NoPings()
	return u
}

// refresh redraws team tid's card in its channel, if it has one, so its
// count and next reminder stay true wherever they changed.
func (m *Module) refresh(tid int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t, ok, err := teamOf(ctx, m.db, tid)
	p := m.poster()
	if err != nil || !ok || t.card == 0 || p == nil {
		return
	}
	var next *reminder
	if r, ok, err := nextFor(ctx, m.db, tid); err == nil && ok {
		next = &r
	}
	if err := m.guard.Allow(t.guild, guard.MessageSend); err != nil {
		return
	}
	u := v2Update(teamCard(t, next))
	u.AllowedMentions = core.NoPings()
	_, err = p.UpdateMessage(t.channel, t.card, u, rest.WithCtx(ctx))
	m.guard.Done(t.guild, err)
	if err != nil {
		m.log.Debug("remind: redrawing a team's card", "team", tid, "err", err)
	}
}
