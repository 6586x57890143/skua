package forever

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
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

func itoa(n int) string { return strconv.Itoa(n) }

// classMark is a class's emoji and a space, or nothing before the sync.
func classMark(class string) string {
	if e := brand.Mention("fv_" + strings.ReplaceAll(class, " ", "")); e != "" {
		return e + " "
	}
	return ""
}

// where is a character's region and ruleset: "eu hardcore".
func where(c char) string {
	if c.ruleset == "" {
		return c.region
	}
	return c.region + " " + c.ruleset
}

// bar is a level as ten cells to the cap.
func bar(level int) string {
	n := min(max(level*10/Cap, 0), 10)
	return strings.Repeat("▰", n) + strings.Repeat("▱", 10-n)
}

func v2(c discord.ContainerComponent, ephemeral bool) discord.MessageCreate {
	flags := discord.MessageFlagIsComponentsV2
	if ephemeral {
		flags |= discord.MessageFlagEphemeral
	}
	return discord.MessageCreate{Components: []discord.LayoutComponent{c}, Flags: flags, AllowedMentions: core.NoPings()}
}

func update(c discord.ContainerComponent) discord.MessageUpdate {
	return discord.MessageUpdate{Components: &[]discord.LayoutComponent{c}, Flags: new(discord.MessageFlagIsComponentsV2), AllowedMentions: core.NoPings()}
}

func button(style discord.ButtonStyle, label, customID, icon string) discord.ButtonComponent {
	b := discord.ButtonComponent{Style: style, Label: label, CustomID: customID}
	b.Emoji = brand.ComponentEmoji(icon)
	return b
}

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
		return e.CreateMessage(v2(m.ladder(*guild, view{region: "all", ruleset: "all", sort: "level"}), false))
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

// byLevel is the ladder's order: higher first, and a tie to whoever got
// there first.
func byLevel(a, b *char) int {
	// A reach time skua didn't see sorts after every one she did.
	return cmp.Or(cmp.Compare(b.level, a.level), boolCmp(a.reached.IsZero(), b.reached.IsZero()), a.reached.Compare(b.reached), cmp.Compare(a.name, b.name))
}

func boolCmp(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// hub is a member's own card: their characters, and what they can do.
func (m *Module) hub(guild, user snowflake.ID, note string) discord.ContainerComponent {
	m.mu.Lock()
	chars := m.mine(guild, user)
	board := m.board(guild, view{region: "all", ruleset: "all", sort: "level"})
	role := m.guilds[guild].role
	var lines []string
	for _, c := range chars {
		place := slices.IndexFunc(board, func(b *char) bool { return b.key == c.key }) + 1
		line := classMark(c.class) + "**" + c.name + " " + c.surname + "** · lv " + itoa(c.level) + " " + bar(c.level) + " · " + where(*c) + " · #" + itoa(place) + " here"
		if c.missing > 0 {
			line += "\n-# ! blizzard hasn't answered for it lately; it shows again after its next logout"
		}
		lines = append(lines, line)
	}
	var portrait string
	if len(chars) > 0 {
		portrait = chars[0].avatar
	}
	m.mu.Unlock()

	body := strings.Join(lines, "\n")
	if body == "" {
		body = "you haven't added a character yet\n-# blizzard shows one from level 10, once it has logged out"
	}
	if note != "" {
		body = note + "\n" + body
	}
	head := discord.NewTextDisplay(headLine("you") + "\n-# " + itoa(len(chars)) + " of " + itoa(perMember) + " characters")
	parts := []discord.ContainerSubComponent{section(head, portrait), discord.NewTextDisplay(body)}
	row := []discord.InteractiveComponent{
		button(discord.ButtonStylePrimary, "add", id+":add", "fv_add"),
		button(discord.ButtonStyleSecondary, "ladder", id+":ladder", "fv_ladder"),
		button(discord.ButtonStyleSecondary, "who", id+":who", "fv_who"),
	}
	if role != 0 {
		row = append(row, button(discord.ButtonStyleSecondary, "role", id+":role", "fv_role"))
	}
	parts = append(parts, discord.NewActionRow(row...))
	if len(chars) > 0 {
		var opts []discord.StringSelectMenuOption
		for _, c := range chars {
			opts = append(opts, discord.StringSelectMenuOption{Label: c.name + " " + c.surname, Value: c.region + "." + strconv.FormatInt(c.id, 10), Description: "lv " + itoa(c.level) + " · " + where(*c), Emoji: brand.ComponentEmoji("fv_" + c.class)})
		}
		parts = append(parts, discord.NewActionRow(discord.NewStringSelectMenu(id+":drop", "take a character off...", opts...)))
	}
	return discord.NewContainer(parts...).WithAccentColor(brand.ColorNotice)
}

// headLine is the card's first line, UX.md's: the emoji, forever and what
// the card is.
func headLine(what string) string {
	head := "**" + Name + "** · " + what
	if e := brand.Mention("mod_" + Name); e != "" {
		head = e + " " + head
	}
	return head
}

// section puts text beside a Blizzard render when there is one.
func section(t discord.TextDisplayComponent, img string) discord.ContainerSubComponent {
	if img == "" {
		return t
	}
	return discord.NewSection(t).WithAccessory(discord.NewThumbnail(img))
}

// view is a ladder's page and filters, kept in its buttons' custom IDs.
type view struct {
	page            int
	region, ruleset string
	sort            string // level, points or ilvl
}

func (v view) id(verb string) string {
	return fmt.Sprintf("%s:%s:%d:%s:%s:%s", id, verb, v.page, v.region, v.ruleset, v.sort)
}

// parseView reads a view back from a custom ID's fields. They come from
// the member's client, so anything off is the first page of everything.
func parseView(f []string) view {
	v := view{region: "all", ruleset: "all", sort: "level"}
	if len(f) != 4 {
		return v
	}
	if p, err := strconv.Atoi(f[0]); err == nil && p >= 0 && p < perGuild {
		v.page = p
	}
	if f[1] == "all" || validRegion(f[1]) {
		v.region = f[1]
	}
	if len(f[2]) <= 20 && f[2] != "" {
		v.ruleset = f[2]
	}
	if f[3] == "points" || f[3] == "ilvl" {
		v.sort = f[3]
	}
	return v
}

// board is a server's characters in the view's order. m.mu held.
func (m *Module) board(guild snowflake.ID, v view) []*char {
	var out []*char
	seen := map[key]bool{}
	for _, c := range m.claims {
		ch := m.chars[c.k]
		if c.guild != guild || ch == nil || seen[c.k] {
			continue
		}
		if (v.region != "all" && ch.region != v.region) || (v.ruleset != "all" && rulesetKey(ch.ruleset) != v.ruleset) {
			continue
		}
		seen[c.k] = true
		out = append(out, ch)
	}
	switch v.sort {
	case "points":
		slices.SortFunc(out, func(a, b *char) int { return cmp.Or(cmp.Compare(b.points, a.points), byLevel(a, b)) })
	case "ilvl":
		slices.SortFunc(out, func(a, b *char) int { return cmp.Or(cmp.Compare(b.ilvl, a.ilvl), byLevel(a, b)) })
	default:
		slices.SortFunc(out, byLevel)
	}
	return out
}

func rulesetKey(r string) string {
	if r == "" {
		return "unknown"
	}
	return strings.ReplaceAll(r, " ", "-")
}

const pageRows = 10

// ladder is the server's board, a page of it in view.
func (m *Module) ladder(guild snowflake.ID, v view) discord.ContainerComponent {
	m.mu.Lock()
	board := m.board(guild, v)
	owner := map[key]snowflake.ID{}
	rulesets := map[string]bool{}
	for _, c := range m.claims {
		if c.guild == guild {
			owner[c.k] = c.user
			if ch := m.chars[c.k]; ch != nil {
				rulesets[rulesetKey(ch.ruleset)] = true
			}
		}
	}
	pages := max((len(board)+pageRows-1)/pageRows, 1)
	v.page = min(v.page, pages-1)
	var lines []string
	for i, c := range board[v.page*pageRows : min(len(board), (v.page+1)*pageRows)] {
		n := v.page*pageRows + i + 1
		line := "**" + itoa(n) + "** " + classMark(c.class) + "**" + c.name + " " + c.surname + "** · "
		switch v.sort {
		case "points":
			line += itoa(c.points) + " points"
		case "ilvl":
			line += "ilvl " + itoa(c.ilvl)
		default:
			line += "lv " + itoa(c.level)
		}
		line += " · " + where(*c)
		if c.level >= Cap && v.sort == "level" && !c.reached.IsZero() {
			line += " · " + core.Duration(c.reached.Sub(Launch))
		}
		lines = append(lines, line+" · <@"+owner[c.key].String()+">")
	}
	var top string
	if len(board) > 0 {
		top = board[0].avatar
	}
	m.mu.Unlock()

	body := strings.Join(lines, "\n")
	if body == "" {
		body = "nobody here yet\n-# /forever me adds your characters"
	}
	sub := "-# " + pick(v.region, "every region") + " · " + pick(v.ruleset, "every ruleset") + " · by " + map[string]string{"level": "level", "points": "achievement points", "ilvl": "item level"}[v.sort]
	parts := []discord.ContainerSubComponent{section(discord.NewTextDisplay(headLine("ladder")+"\n"+sub), top), discord.NewTextDisplay(body)}

	prev, next := v, v
	prev.page, next.page = v.page-1, v.page+1
	pb := button(discord.ButtonStyleSecondary, "◂", prev.id("page"), "")
	pb.Disabled = v.page == 0
	nb := button(discord.ButtonStyleSecondary, "▸", next.id("page"), "")
	nb.Disabled = v.page >= pages-1
	parts = append(parts, discord.NewTextDisplay("-# page "+itoa(v.page+1)+" of "+itoa(pages)), discord.NewActionRow(pb, nb))

	regions := []discord.StringSelectMenuOption{{Label: "every region", Value: "all", Default: v.region == "all"}}
	for _, r := range Regions {
		regions = append(regions, discord.StringSelectMenuOption{Label: r, Value: r, Default: v.region == r})
	}
	sets := []discord.StringSelectMenuOption{{Label: "every ruleset", Value: "all", Default: v.ruleset == "all"}}
	for _, r := range sortedKeys(rulesets) {
		sets = append(sets, discord.StringSelectMenuOption{Label: r, Value: r, Default: v.ruleset == r, Emoji: brand.ComponentEmoji("fv_rs_" + r)})
	}
	sorts := []discord.StringSelectMenuOption{
		{Label: "by level", Value: "level", Default: v.sort == "level"},
		{Label: "by achievement points", Value: "points", Default: v.sort == "points"},
		{Label: "by item level", Value: "ilvl", Default: v.sort == "ilvl"},
	}
	parts = append(parts,
		discord.NewActionRow(discord.NewStringSelectMenu(v.id("region"), "region", regions...)),
		discord.NewActionRow(discord.NewStringSelectMenu(v.id("ruleset"), "ruleset", sets...)),
		discord.NewActionRow(discord.NewStringSelectMenu(v.id("sort"), "sort", sorts...)),
	)
	return discord.NewContainer(parts...).WithAccentColor(brand.ColorNotice)
}

func pick(v, all string) string {
	if v == "all" {
		return all
	}
	return v
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// who is the server's characters by region and ruleset, so members find
// someone on their side.
func (m *Module) who(guild snowflake.ID) discord.ContainerComponent {
	m.mu.Lock()
	groups := map[string][]string{}
	for _, c := range m.claims {
		ch := m.chars[c.k]
		if c.guild != guild || ch == nil {
			continue
		}
		g := where(*ch)
		groups[g] = append(groups[g], classMark(ch.class)+ch.name+" "+ch.surname+" · "+itoa(ch.level)+" · <@"+c.user.String()+">")
	}
	m.mu.Unlock()
	var parts []string
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	slices.Sort(names)
	for _, g := range names {
		rows := groups[g]
		more := ""
		if len(rows) > 12 {
			more = fmt.Sprintf("\n-# and %d more", len(rows)-12)
			rows = rows[:12]
		}
		parts = append(parts, "**"+g+"** · "+itoa(len(groups[g]))+"\n"+strings.Join(rows, "\n")+more)
	}
	body := strings.Join(parts, "\n\n")
	if body == "" {
		body = "nobody here yet\n-# /forever me adds your characters"
	}
	if len(body) > 3800 {
		body = body[:strings.LastIndex(body[:3800], "\n")] + "\n-# and more"
	}
	return brand.Card(brand.ColorInfo, Name, "who plays where", body)
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
		return e.CreateMessage(v2(m.ladder(*guild, view{region: "all", ruleset: "all", sort: "level"}), false))
	case "who":
		return e.CreateMessage(v2(m.who(*guild), true))
	case "role":
		return m.toggleRole(ctx, e, *guild)
	case "drop":
		return m.drop(ctx, e, *guild)
	case "page":
		return e.UpdateMessage(update(m.ladder(*guild, parseView(f[2:]))))
	case "region", "ruleset", "sort":
		v := parseView(f[2:])
		vals := e.StringSelectMenuInteractionData().Values
		if len(vals) != 1 {
			return core.Tell("pick one")
		}
		fs := []string{"0", v.region, v.ruleset, v.sort}
		fs[map[string]int{"region": 1, "ruleset": 2, "sort": 3}[f[1]]] = vals[0]
		return e.UpdateMessage(update(m.ladder(*guild, parseView(fs))))
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
	err := m.claimable(*guild, user, char{})
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	note, err := m.list(ctx, *guild, user, region, name, surname)
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
func (m *Module) list(ctx context.Context, guild, user snowflake.ID, region, name, surname string) (string, error) {
	var p profile
	mod, _, err := m.api.get(ctx, region, "profile", charPath(slug(name, surname)), "", &p)
	if errors.Is(err, errGone) {
		return "", core.Tell("blizzard doesn't know " + strings.ToLower(name+" "+surname) + " in " + region + " yet; a character shows from level 10, once it has logged out")
	}
	if err != nil {
		return "", err
	}
	c := char{key: key{region: region}, modified: mod}
	apply(&c, p)
	if c.id == 0 {
		return "", errors.New("forever: a profile without an id")
	}
	m.mu.Lock()
	old, known := m.chars[c.key]
	if known {
		c = *old
	}
	err = m.claimable(guild, user, c)
	m.mu.Unlock()
	if err != nil {
		return "", err
	}
	if !known {
		c.avatar, c.render = m.renders(ctx, c)
		if err := saveChar(ctx, m.db, c); err != nil {
			return "", err
		}
	}
	cl := claim{guild: guild, user: user, k: c.key, at: m.now()}
	m.mu.Lock()
	// Asked again: another add may have taken the place while Blizzard
	// answered.
	if err := m.claimable(guild, user, c); err != nil {
		m.mu.Unlock()
		return "", err
	}
	if _, ok := m.chars[c.key]; !ok {
		m.chars[c.key] = &c
	}
	m.claims = append(m.claims, cl)
	m.mu.Unlock()
	if err := saveClaim(ctx, m.db, cl); err != nil {
		m.mu.Lock()
		m.claims = slices.DeleteFunc(m.claims, func(x claim) bool { return x == cl })
		m.mu.Unlock()
		return "", err
	}
	return "✓ added " + classMark(c.class) + "**" + c.name + " " + c.surname + "**, lv " + itoa(c.level), nil
}

// claimable is why user can't list c in guild, or nil. m.mu held.
func (m *Module) claimable(guild, user snowflake.ID, c char) error {
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
			if cl.user == user {
				return core.Tell("you've added " + c.name + " " + c.surname + " already")
			}
			return core.Tell(c.name + " " + c.surname + " is listed here by someone else already")
		}
	}
	if n >= perMember {
		return core.Tell("you have " + itoa(perMember) + " characters here already; take one off first")
	}
	if len(all) >= perGuild {
		return core.Tell("this server lists " + itoa(perGuild) + " characters, as many as skua keeps")
	}
	return nil
}

// drop takes the picked characters off the member's list.
func (m *Module) drop(ctx context.Context, e *events.ComponentInteractionCreate, guild snowflake.ID) error {
	user := e.User().ID
	var gone []key
	for _, v := range e.StringSelectMenuInteractionData().Values {
		region, idStr, ok := strings.Cut(v, ".")
		n, err := strconv.ParseInt(idStr, 10, 64)
		if ok && err == nil && validRegion(region) {
			gone = append(gone, key{region, n})
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
	m.roleMu.Lock()
	defer m.roleMu.Unlock()
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
	s.role = role.ID
	if err := saveSetup(ctx, m.db, guild, s); err != nil {
		m.log.Warn("forever: keeping the role", "err", err)
	}
	m.mu.Lock()
	m.guilds[guild] = s
	m.mu.Unlock()
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
	rr := roles.WithCtx{R: r, Ctx: ctx}
	has := slices.Contains(member.RoleIDs, role)
	if has && !roles.CanDrop(rr, guild, role) || !has && !roles.CanGrant(rr, guild, self, role) {
		return core.Tell("skua can't hand out that role; an admin can pick one with no permissions or channel overrides of its own, below skua's")
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
		return core.Tell("skua can't hand out that role right now; an admin needs to give her manage roles above it")
	}
	if err != nil {
		return err
	}
	_, err = r.UpdateInteractionResponse(e.ApplicationID(), e.Token(), update(brand.Card(brand.ColorOK, Name, "", text)), rest.WithCtx(ctx))
	return err
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
	m.mu.Lock()
	s := m.guilds[guild]
	m.mu.Unlock()
	var note string
	switch what {
	case "feed":
		ch := e.ChannelSelectMenuInteractionData().Values
		if len(ch) != 1 {
			return core.Tell("pick one channel")
		}
		s.feed, note = ch[0], "✓ the level feed goes to <#"+ch[0].String()+">"
	case "setrole":
		rs := e.RoleSelectMenuInteractionData().Values
		if len(rs) != 1 {
			return core.Tell("pick one role")
		}
		s.role, note = rs[0], "✓ members can pick <@&"+rs[0].String()+"> from /forever me"
	}
	if err := saveSetup(ctx, m.db, guild, s); err != nil {
		return err
	}
	m.mu.Lock()
	m.guilds[guild] = s
	m.mu.Unlock()
	return e.CreateMessage(v2(brand.Card(brand.ColorOK, Name, "settings", note), true))
}
