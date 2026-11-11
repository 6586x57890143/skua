package forever

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/roles"
)

func (m *Module) command(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return core.Tell("forever only works in a server")
	}
	sub := ""
	if s := e.SlashCommandInteractionData().SubCommandName; s != nil {
		sub = *s
	}
	switch sub {
	case "ladder":
		return e.CreateMessage(v2(m.ladder(*guild, 0), false))
	case "who":
		return e.CreateMessage(v2(m.who(*guild), true))
	}
	return e.CreateMessage(v2(m.hub(*guild, e.User().ID, ""), true))
}

// mine is the characters user lists in guild, highest first. m.mu held.
func (m *Module) mine(guild, user snowflake.ID) []*char {
	var out []*char
	for _, c := range m.claims {
		if c.guild == guild && c.user == user {
			if ch := m.chars[c.k]; ch != nil {
				out = append(out, ch)
			}
		}
	}
	slices.SortFunc(out, byLevel)
	return out
}

// click is every forever button and select. Custom IDs and values come
// from the member's client, so all of it is checked again.
func (m *Module) click(ctx context.Context, e *events.ComponentInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return core.Tell("forever only works in a server")
	}
	f := strings.Split(e.Data.CustomID(), ":")
	if len(f) < 2 {
		return core.Tell("that card is out of date; run /forever again")
	}
	switch f[1] {
	case "add":
		return e.Modal(addForm())
	case "ladder":
		return e.CreateMessage(v2(m.ladder(*guild, 0), false))
	case "who":
		return e.CreateMessage(v2(m.who(*guild), true))
	case "role":
		return m.toggleRole(ctx, e, *guild)
	case "drop":
		return m.drop(ctx, e, *guild)
	case "link":
		return m.linkCard(e, *guild)
	case "armory":
		return m.showArmory(ctx, e, *guild)
	case "share":
		if len(f) != 3 {
			return core.Tell("that card is out of date; run /forever again")
		}
		return m.share(ctx, e, *guild, f[2])
	case "page":
		page := 0
		if len(f) == 3 {
			page, _ = strconv.Atoi(f[2])
		}
		return e.UpdateMessage(update(m.ladder(*guild, page)))
	case "feed", "setrole":
		return m.configure(ctx, e, *guild, f[1])
	}
	return core.Tell("that card is out of date; run /forever again")
}

func addForm() discord.ModalCreate {
	var opts []discord.StringSelectMenuOption
	for _, r := range Regions {
		opts = append(opts, discord.StringSelectMenuOption{Label: r, Value: r})
	}
	return discord.ModalCreate{
		CustomID: addID,
		Title:    "add a forever character",
		Components: []discord.LayoutComponent{
			discord.NewLabel("name and surname", discord.TextInputComponent{
				CustomID: "name", Style: discord.TextInputStyleShort, Required: true, MaxLength: 50, Placeholder: "thrall earthcaller",
			}),
			discord.NewLabel("region", discord.NewStringSelectMenu("region", "where it plays", opts...)),
		},
	}
}

// add looks the character up and lists it for the member. Blizzard can
// take a moment, so the reply is deferred.
func (m *Module) add(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return core.Tell("forever only works in a server")
	}
	name, surname, ok := parseName(e.Data.Text("name"))
	if !ok {
		return core.Tell("give the character's name and surname, like thrall earthcaller")
	}
	region := ""
	if v := e.Data.StringValues("region"); len(v) == 1 {
		region = v[0]
	}
	if !validRegion(region) {
		return core.Tell("pick the region the character plays in")
	}
	user := e.User().ID
	m.mu.Lock()
	err := m.claimable(*guild, user, char{}, false)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	res, err := m.list(ctx, *guild, user, region, name, surname, false)
	note := res.note
	if err != nil {
		var t core.Tell
		if !errors.As(err, &t) {
			m.log.Warn("forever: adding a character", "err", err)
			t = "✗ blizzard's armory didn't answer; try again in a moment"
		}
		note = "✗ " + strings.TrimPrefix(string(t), "✗ ")
	} else {
		m.grant(ctx, e.Client().Rest, *guild, user)
	}
	_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), update(m.hub(*guild, user, note)), rest.WithCtx(ctx))
	return err
}

// list reads a character from Blizzard and lists it in guild as user's.
// A character another server lists already keeps the record skua has, so
// the next round still sees its climb for everyone. A new one's reach time
// is unknown: skua didn't see it get there.
func (m *Module) list(ctx context.Context, guild, user snowflake.ID, region, name, surname string, verified bool) (listed, error) {
	var p profile
	mod, _, err := m.api.get(ctx, region, "profile", charPath(slug(name, surname)), "", &p)
	if errors.Is(err, errGone) {
		return listed{}, core.Tell("blizzard doesn't know " + strings.ToLower(name+" "+surname) + " in " + region + " yet; a character shows from level 10, once it has logged out")
	}
	if err != nil {
		return listed{}, err
	}
	c := char{key: key{region: region}, modified: mod}
	apply(&c, p)
	if c.id == 0 {
		return listed{}, errors.New("forever: a profile without an id")
	}
	m.mu.Lock()
	old, known := m.chars[c.key]
	if known {
		// skua's record keeps its climb for the next round; the name is
		// Blizzard's now, so a renamed character comes back by adding it.
		name, surname := c.name, c.surname
		renamed := old.name != name || old.surname != surname
		c = *old
		c.name, c.surname = name, surname
		if renamed {
			old.name, old.surname, old.modified = c.name, c.surname, ""
			c.modified = ""
		}
	}
	err = m.claimable(guild, user, c, verified)
	m.mu.Unlock()
	if err != nil {
		return listed{}, err
	}
	if !known {
		c.avatar, c.render = m.renders(ctx, c)
		c.crafts = m.crafts(ctx, c)
	}
	if err := saveChar(ctx, m.db, c); err != nil {
		return listed{}, err
	}
	cl := claim{guild: guild, user: user, k: c.key, at: m.now(), verified: verified}
	m.mu.Lock()
	// Asked again: another add may have taken the place while Blizzard
	// answered.
	if err := m.claimable(guild, user, c, verified); err != nil {
		m.mu.Unlock()
		return listed{}, err
	}
	if _, ok := m.chars[c.key]; !ok {
		m.chars[c.key] = &c
	}
	// A verified claim replaces whatever claim the character had here.
	var was []claim
	m.claims = slices.DeleteFunc(m.claims, func(x claim) bool {
		if x.guild == guild && x.k == c.key {
			was = append(was, x)
			return true
		}
		return false
	})
	m.claims = append(m.claims, cl)
	m.mu.Unlock()
	if err := saveClaim(ctx, m.db, cl); err != nil {
		m.mu.Lock()
		m.claims = append(slices.DeleteFunc(m.claims, func(x claim) bool { return x == cl }), was...)
		m.mu.Unlock()
		return listed{}, err
	}
	res := listed{note: "✓ added " + classMark(c.class) + "**" + c.name + " " + c.surname + "**, lv " + itoa(c.level)}
	for _, x := range was {
		if x.user != user {
			res.displaced = append(res.displaced, x.user)
		}
	}
	return res, nil
}

// listed is what an add did: what to tell the member, and anyone whose
// claim on the character it replaced.
type listed struct {
	note      string
	displaced []snowflake.ID
}

// claimable is why user can't list c in guild, or nil. A verified claim
// replaces any claim on the character: the latest battle.net sign-in with
// an account is its owner. A name claim takes nothing. m.mu held.
func (m *Module) claimable(guild, user snowflake.ID, c char, verified bool) error {
	n, all := 0, map[key]bool{}
	for _, cl := range m.claims {
		if cl.guild != guild {
			continue
		}
		all[cl.k] = true
		if cl.user == user {
			n++
		}
		if c.id != 0 && cl.k == c.key {
			if verified {
				if cl.user == user {
					n--
				}
				continue
			}
			if cl.user == user {
				return core.Tell("you've added " + c.name + " " + c.surname + " already")
			}
			return core.Tell(c.name + " " + c.surname + " is listed here by someone else already")
		}
	}
	if n >= perMember {
		return core.Tell("you have " + itoa(perMember) + " characters here already; take one off first")
	}
	if len(all) >= perGuild && (!verified || !all[c.key]) {
		return core.Tell("this server lists " + itoa(perGuild) + " characters, as many as skua keeps")
	}
	return nil
}

// drop takes the picked characters off the member's list.
func (m *Module) drop(ctx context.Context, e *events.ComponentInteractionCreate, guild snowflake.ID) error {
	user := e.User().ID
	var gone []key
	for _, v := range e.StringSelectMenuInteractionData().Values {
		if k, ok := parseValue(v); ok {
			gone = append(gone, k)
		}
	}
	m.mu.Lock()
	var dropped []key
	m.claims = slices.DeleteFunc(m.claims, func(c claim) bool {
		if c.guild == guild && c.user == user && slices.Contains(gone, c.k) {
			dropped = append(dropped, c.k)
			return true
		}
		return false
	})
	m.mu.Unlock()
	for _, k := range dropped {
		if err := dropClaim(ctx, m.db, guild, k); err != nil {
			return err
		}
	}
	m.prune(ctx, dropped...)
	note := "✓ taken off"
	if len(dropped) == 0 {
		note = "! that character isn't on your list any more"
	}
	if err := e.UpdateMessage(update(m.hub(guild, user, note))); err != nil {
		return err
	}
	if len(dropped) > 0 {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		m.ungrant(ctx, e.Client().Rest, guild, user)
	}
	return nil
}

// roleColor is the forever role's colour: Forever's gold.
const roleColor = 0xE0A64B

// ensureRole is the server's forever role, made the first time someone
// needs it: no permissions at all, so it is a label and never a privilege,
// and wearing skua's icon where the server's boosts allow role icons.
// 0 when skua can't make one.
func (m *Module) ensureRole(ctx context.Context, r rest.Rest, guild snowflake.ID) snowflake.ID {
	mu, _ := m.roleMu.LoadOrStore(guild, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	m.mu.Lock()
	s := m.guilds[guild]
	m.mu.Unlock()
	if s.role != 0 {
		rs, err := r.GetRoles(guild, rest.WithCtx(ctx))
		if err != nil || slices.ContainsFunc(rs, func(x discord.Role) bool { return x.ID == s.role }) {
			return s.role
		}
		// Deleted since: make a new one.
	}
	if m.guard.Allow(guild, guard.RoleWrite) != nil {
		return 0
	}
	create := discord.RoleCreate{Name: "forever", Permissions: new(discord.Permissions), Color: roleColor}
	if g, err := r.GetGuild(guild, false, rest.WithCtx(ctx)); err == nil && slices.Contains(g.Features, discord.GuildFeatureRoleIcons) {
		create.Icon = roleIcon()
	}
	role, err := r.CreateRole(guild, create, rest.WithCtx(ctx), rest.WithReason("forever: the server's forever role, made for its first character"))
	m.guard.Done(guild, err)
	if err != nil {
		m.log.Info("forever: making the role", "guild", guild, "err", err)
		return 0
	}
	if err := m.setGuild(ctx, guild, func(s *setup) { s.role = role.ID }); err != nil {
		m.log.Warn("forever: keeping the role", "err", err)
	}
	return role.ID
}

// roleIcon is the role's icon: skua's fv_role art.
func roleIcon() *discord.Icon {
	for _, e := range brand.Emojis() {
		if e.Key == "fv_role" {
			return discord.NewIconRaw(discord.IconTypePNG, e.PNG)
		}
	}
	return nil
}

// left takes a member who left a server off its ladder: their characters
// there, and any of them no server lists any more.
func (m *Module) left(ctx context.Context, guild, user snowflake.ID) {
	m.mu.Lock()
	var gone []key
	m.claims = slices.DeleteFunc(m.claims, func(c claim) bool {
		if c.guild == guild && c.user == user {
			gone = append(gone, c.k)
			return true
		}
		return false
	})
	m.mu.Unlock()
	for _, k := range gone {
		if err := dropClaim(ctx, m.db, guild, k); err != nil {
			m.log.Warn("forever: a member left", "guild", guild, "err", err)
		}
	}
	m.prune(ctx, gone...)
}

// prune forgets the characters of ks no server lists any more.
func (m *Module) prune(ctx context.Context, ks ...key) {
	m.mu.Lock()
	var gone []key
	for _, k := range ks {
		if !slices.ContainsFunc(m.claims, func(c claim) bool { return c.k == k }) {
			delete(m.chars, k)
			gone = append(gone, k)
		}
	}
	m.mu.Unlock()
	m.screens.mu.Lock()
	for _, k := range gone {
		delete(m.screens.have, k)
	}
	m.screens.mu.Unlock()
	for _, k := range gone {
		if err := dropChar(ctx, m.db, k); err != nil {
			m.log.Warn("forever: forgetting a character", "err", err)
		}
	}
}

// setGuild changes one server's settings under the lock and keeps them,
// so two changes at once never undo each other.
func (m *Module) setGuild(ctx context.Context, guild snowflake.ID, change func(*setup)) error {
	m.mu.Lock()
	s := m.guilds[guild]
	change(&s)
	m.guilds[guild] = s
	m.mu.Unlock()
	return saveSetup(ctx, m.db, guild, s)
}

// grant gives a member the server's forever role when they add a
// character, making the role if there is none. A refusal is quiet: the
// add still counts.
func (m *Module) grant(ctx context.Context, r rest.Rest, guild, user snowflake.ID) {
	role := m.ensureRole(ctx, r, guild)
	m.mu.Lock()
	self := m.self
	m.mu.Unlock()
	if role == 0 || m.guard.Allow(guild, guard.MemberRole) != nil {
		return
	}
	if !roles.CanGrant(roles.WithCtx{R: r, Ctx: ctx}, guild, self, role) {
		return
	}
	err := r.AddMemberRole(guild, user, role, rest.WithCtx(ctx), rest.WithReason("forever: the member added a character"))
	m.guard.Done(guild, err)
}

// ungrant takes the role back from a member who no longer lists anything.
func (m *Module) ungrant(ctx context.Context, r rest.Rest, guild, user snowflake.ID) {
	m.mu.Lock()
	role, left := m.guilds[guild].role, len(m.mine(guild, user))
	m.mu.Unlock()
	if role == 0 || left > 0 || m.guard.Allow(guild, guard.MemberRole) != nil {
		return
	}
	if !roles.CanDrop(roles.WithCtx{R: r, Ctx: ctx}, guild, role) {
		return
	}
	err := r.RemoveMemberRole(guild, user, role, rest.WithCtx(ctx), rest.WithReason("forever: the member took off their last character"))
	m.guard.Done(guild, err)
}

// toggleRole gives the presser the forever role, or takes it back.
func (m *Module) toggleRole(ctx context.Context, e *events.ComponentInteractionCreate, guild snowflake.ID) error {
	member := e.Member()
	m.mu.Lock()
	role, self := m.guilds[guild].role, m.self
	m.mu.Unlock()
	if member == nil || role == 0 {
		return core.Tell("this server hasn't picked a forever role")
	}
	if err := m.guard.Allow(guild, guard.MemberRole); err != nil {
		return core.Tell("too many role changes here this hour; try again in a bit")
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	r := e.Client().Rest
	text, err := m.flipRole(ctx, r, guild, member, role, self)
	if err != nil {
		var t core.Tell
		if !errors.As(err, &t) {
			m.log.Warn("forever: the role button", "err", err)
			t = "something went wrong on skua's side; try again in a moment"
		}
		text = "✗ " + string(t)
	}
	_, err = r.UpdateInteractionResponse(e.ApplicationID(), e.Token(), update(brand.Card(brand.ColorOK, Name, "", text)), rest.WithCtx(ctx))
	return err
}

// flipRole gives member the role or takes it back, and what to tell them.
func (m *Module) flipRole(ctx context.Context, r rest.Rest, guild snowflake.ID, member *discord.ResolvedMember, role, self snowflake.ID) (string, error) {
	rr := roles.WithCtx{R: r, Ctx: ctx}
	has := slices.Contains(member.RoleIDs, role)
	if has && !roles.CanDrop(rr, guild, role) || !has && !roles.CanGrant(rr, guild, self, role) {
		return "", core.Tell("skua can't hand out that role; an admin can pick one with no permissions or channel overrides of its own, below skua's")
	}
	opts := []rest.RequestOpt{rest.WithCtx(ctx), rest.WithReason("forever: the member pressed role")}
	var err error
	text := "✓ you have <@&" + role.String() + ">"
	if has {
		err, text = r.RemoveMemberRole(guild, member.User.ID, role, opts...), "✓ <@&"+role.String()+"> taken back"
	} else {
		err = r.AddMemberRole(guild, member.User.ID, role, opts...)
	}
	m.guard.Done(guild, err)
	if re, ok := errors.AsType[*rest.Error](err); ok && re.Response != nil && re.Response.StatusCode == http.StatusForbidden {
		return "", core.Tell("skua can't hand out that role right now; an admin needs to give her manage roles above it")
	}
	return text, err
}

// controls is the admin page's settings: where the feed goes and the role
// members pick. m.mu held.
func (m *Module) controls(guild snowflake.ID) []discord.ContainerSubComponent {
	s := m.guilds[guild]
	feed := discord.ChannelSelectMenuComponent{CustomID: id + ":feed", Placeholder: "the forever channel, for the level feed", ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews}, MaxValues: 1}
	if s.feed != 0 {
		feed.DefaultValues = []discord.SelectMenuDefaultValue{discord.NewSelectMenuDefaultChannel(s.feed)}
	}
	role := discord.NewRoleSelectMenu(id+":setrole", "the role members pick")
	if s.role != 0 {
		role.DefaultValues = []discord.SelectMenuDefaultValue{discord.NewSelectMenuDefaultRole(s.role)}
	}
	return []discord.ContainerSubComponent{discord.NewActionRow(feed), discord.NewActionRow(role)}
}

// configure keeps an admin's pick from the controls.
func (m *Module) configure(ctx context.Context, e *events.ComponentInteractionCreate, guild snowflake.ID, what string) error {
	if !m.admin(e) {
		return core.Tell("only this server's admins can change forever's settings")
	}
	var note string
	var change func(*setup)
	switch what {
	case "feed":
		ch := e.ChannelSelectMenuInteractionData().Values
		if len(ch) != 1 {
			return core.Tell("pick one channel")
		}
		change, note = func(s *setup) { s.feed = ch[0] }, "✓ the level feed goes to <#"+ch[0].String()+">"
	case "setrole":
		rs := e.RoleSelectMenuInteractionData().Values
		if len(rs) != 1 {
			return core.Tell("pick one role")
		}
		change, note = func(s *setup) { s.role = rs[0] }, "✓ members can pick <@&"+rs[0].String()+"> from /forever me"
	}
	if err := m.setGuild(ctx, guild, change); err != nil {
		return err
	}
	return e.CreateMessage(v2(brand.Card(brand.ColorOK, Name, "settings", note), true))
}
