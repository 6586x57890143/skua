package forever

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// What a member reads. UX.md's card, with two of forever's own: a region
// is its flag, and a card about one character is accented in its class's
// colour, the way the game colours a name.

// flags are the regions as Discord draws them.
var flags = map[string]string{"us": "🇺🇸", "eu": "🇪🇺", "kr": "🇰🇷", "tw": "🇹🇼"}

// classColor is Blizzard's colour for each class.
var classColor = map[string]int{
	"warrior": 0xC69B6D, "paladin": 0xF48CBA, "hunter": 0xAAD372, "rogue": 0xFFF468, "priest": 0xFFFFFF,
	"shaman": 0x0070DD, "mage": 0x3FC7EB, "warlock": 0x8788EE, "druid": 0xFF7C0A,
}

func accent(class string) int {
	if c, ok := classColor[class]; ok {
		return c
	}
	return brand.ColorNotice
}

func itoa(n int) string { return strconv.Itoa(n) }

// num is a count with thousands marked: 20,590.
func num(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// classMark is a class's emoji and a space, or nothing before the sync.
func classMark(class string) string {
	if e := brand.Mention("fv_" + strings.ReplaceAll(class, " ", "")); e != "" {
		return e + " "
	}
	return ""
}

// where is a character's region flag and ruleset: "🇪🇺 hardcore".
func where(c char) string {
	w := flags[c.region]
	if w == "" {
		w = c.region
	}
	if c.ruleset != "" {
		w += " " + c.ruleset
	}
	return w
}

// fullName is a character's name as the game writes it.
func fullName(c char) string { return c.name + " " + c.surname }

// bar is a level as ten cells to the cap, and how far that is.
func bar(level int) string {
	n := min(max(level*10/Cap, 0), 10)
	return strings.Repeat("▰", n) + strings.Repeat("▱", 10-n) + " " + itoa(min(level, Cap)*100/Cap) + "%"
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

// button wears icon, always: UX.md has every button read by its picture.
func button(style discord.ButtonStyle, label, customID, icon string) discord.ButtonComponent {
	b := discord.ButtonComponent{Style: style, Label: label, CustomID: customID}
	b.Emoji = brand.ComponentEmoji(icon)
	if b.Emoji == nil && label == "" {
		b.Label = map[string]string{"fv_prev": "◂", "fv_next": "▸"}[icon]
	}
	return b
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

// section puts the head beside a Blizzard portrait when there is one. Only
// the head goes there (UX.md): beside a thumbnail a phone gives text a
// narrow column.
func section(t discord.TextDisplayComponent, img string) discord.ContainerSubComponent {
	if img == "" {
		return t
	}
	return discord.NewSection(t).WithAccessory(discord.NewThumbnail(img))
}

// The Legacy track: Forever's account-wide checkpoints that skua can read
// from the armory. A class pays at 25, 45 and 60, a crafting profession at
// 150, 225 and 300.
var (
	classPoints = []int{25, 45, Cap}
	craftPoints = []int{150, 225, 300}
	crafting    = []string{"alchemy", "blacksmithing", "enchanting", "engineering", "leatherworking", "tailoring"}
)

// legacyLine is one character's track: each checkpoint reached marked ✦,
// each profession's next with how far it is.
func legacyLine(c char) string {
	var parts []string
	for _, l := range classPoints {
		if c.level >= l {
			parts = append(parts, itoa(l)+" ✦")
		} else {
			parts = append(parts, itoa(l))
		}
	}
	for _, p := range crafting {
		skill, ok := c.crafts[p]
		if !ok {
			continue
		}
		done, next := 0, 0
		for _, l := range craftPoints {
			if skill >= l {
				done = l
			} else if next == 0 {
				next = l
			}
		}
		switch {
		case next == 0:
			parts = append(parts, p+" "+itoa(done)+" ✦")
		case done > 0:
			parts = append(parts, p+" "+itoa(done)+" ✦ "+itoa(skill)+"/"+itoa(next))
		default:
			parts = append(parts, p+" "+itoa(skill)+"/"+itoa(next))
		}
	}
	return "-# legacy · " + strings.Join(parts, " · ")
}

// legacyPoints is how many Legacy points a member's listed characters show:
// each class and profession checkpoint once, however many reach it.
func legacyPoints(chars []*char) int {
	seen := map[string]bool{}
	for _, c := range chars {
		for _, l := range classPoints {
			if c.level >= l {
				seen[c.class+itoa(l)] = true
			}
		}
		for p, skill := range c.crafts {
			for _, l := range craftPoints {
				if skill >= l {
					seen[p+itoa(l)] = true
				}
			}
		}
	}
	return len(seen)
}

// hub is a member's own card: each character's level, place and Legacy
// track, in the colour of their highest.
func (m *Module) hub(guild, user snowflake.ID, note string) discord.ContainerComponent {
	m.mu.Lock()
	chars := m.mine(guild, user)
	board := m.board(guild)
	role := m.guilds[guild].role
	var blocks []string
	for _, c := range chars {
		place := slices.IndexFunc(board, func(b *char) bool { return b.key == c.key }) + 1
		b := classMark(c.class) + "**" + fullName(*c) + "** · lv " + itoa(c.level) + " · " + where(*c) + " · #" + itoa(place) + " here"
		b += "\n" + bar(c.level) + " · " + c.race + " " + c.class
		if c.level < Cap {
			b += " · " + num(c.xp) + " xp into " + itoa(c.level)
		}
		b += "\n" + legacyLine(*c)
		if c.missing > 0 {
			b += "\n-# ! blizzard hasn't answered for it lately; it shows again after its next logout"
		}
		blocks = append(blocks, b)
	}
	points := legacyPoints(chars)
	var portrait, class string
	if len(chars) > 0 {
		portrait, class = chars[0].avatar, chars[0].class
	}
	m.mu.Unlock()

	sub := "-# " + itoa(len(chars)) + " of " + itoa(perMember) + " characters"
	if points > 0 {
		sub += " · " + itoa(points) + " legacy points seen"
	}
	parts := []discord.ContainerSubComponent{section(discord.NewTextDisplay(headLine("you")+"\n"+sub), portrait)}
	if note != "" {
		parts = append(parts, discord.NewTextDisplay(note))
	}
	if len(blocks) == 0 {
		parts = append(parts, discord.NewTextDisplay("you haven't added a character yet\n-# blizzard shows one from level 10, once it has logged out"))
	}
	for i, b := range blocks {
		if i > 0 || note != "" {
			parts = append(parts, discord.NewSmallSeparator())
		}
		parts = append(parts, discord.NewTextDisplay(b))
	}
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
			opts = append(opts, discord.StringSelectMenuOption{Label: fullName(*c), Value: c.region + "." + strconv.FormatInt(c.id, 10), Description: "lv " + itoa(c.level) + " · " + c.region + " " + c.ruleset, Emoji: brand.ComponentEmoji("fv_" + c.class)})
		}
		parts = append(parts, discord.NewActionRow(discord.NewStringSelectMenu(id+":drop", "take a character off...", opts...)))
	}
	return discord.NewContainer(parts...).WithAccentColor(accent(class))
}

// board is a server's characters in ladder order. m.mu held.
func (m *Module) board(guild snowflake.ID) []*char {
	var out []*char
	seen := map[key]bool{}
	for _, c := range m.claims {
		ch := m.chars[c.k]
		if c.guild != guild || ch == nil || seen[c.k] {
			continue
		}
		seen[c.k] = true
		out = append(out, ch)
	}
	slices.SortFunc(out, byLevel)
	return out
}

// byLevel is the ladder's order: higher first, then further into the
// level, and a tie to whoever got there first.
func byLevel(a, b *char) int {
	// A reach time skua didn't see sorts after every one she did.
	return cmp.Or(cmp.Compare(b.level, a.level), cmp.Compare(b.xp, a.xp), boolCmp(a.reached.IsZero(), b.reached.IsZero()), a.reached.Compare(b.reached), cmp.Compare(a.name, b.name))
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

// move is a place against the day's start, the way a chart shows it: ▲2,
// ▼1, = held, new for a character the day's chart didn't have.
func move(was, now int) string {
	switch {
	case was == 0:
		return "new"
	case was > now:
		return "▲" + itoa(was-now)
	case was < now:
		return "▼" + itoa(now-was)
	}
	return "="
}

const pageRows = 10

// ladder is a page of the server's board: one board for every region and
// ruleset, each line its place, its move since the day began, the class,
// the name and the level.
func (m *Module) ladder(guild snowflake.ID, page int) discord.ContainerComponent {
	m.mu.Lock()
	board := m.board(guild)
	owner := map[key]snowflake.ID{}
	for _, c := range m.claims {
		if c.guild == guild {
			owner[c.k] = c.user
		}
	}
	day := m.days[guild]
	pages := max((len(board)+pageRows-1)/pageRows, 1)
	page = min(max(page, 0), pages-1)
	var lines []string
	for i, c := range board[page*pageRows : min(len(board), (page+1)*pageRows)] {
		n := page*pageRows + i + 1
		line := "**" + itoa(n) + "** `" + move(day[c.key], n) + "` " + classMark(c.class) + "**" + fullName(*c) + "** · lv " + itoa(c.level) + " · " + where(*c)
		if c.level >= Cap && !c.reached.IsZero() {
			line += " · " + core.Duration(c.reached.Sub(Launch))
		}
		lines = append(lines, line+" · <@"+owner[c.key].String()+">")
	}
	var top string
	if len(board) > 0 {
		top = board[0].avatar
	}
	m.mu.Unlock()

	sub := "-# " + itoa(len(board)) + " characters · every region · moves since 00:00 utc"
	parts := []discord.ContainerSubComponent{section(discord.NewTextDisplay(headLine("ladder")+"\n"+sub), top)}
	if len(lines) == 0 {
		parts = append(parts, discord.NewTextDisplay("nobody here yet\n-# /forever me adds your characters"))
	} else {
		parts = append(parts, discord.NewSmallSeparator(), discord.NewTextDisplay(strings.Join(lines, "\n")))
	}
	pb := button(discord.ButtonStyleSecondary, "", fmt.Sprintf("%s:page:%d", id, page-1), "fv_prev")
	pb.Disabled = page == 0
	nb := button(discord.ButtonStyleSecondary, "", fmt.Sprintf("%s:page:%d", id, page+1), "fv_next")
	nb.Disabled = page >= pages-1
	parts = append(parts,
		discord.NewActionRow(pb, nb, button(discord.ButtonStyleSecondary, "who", id+":who", "fv_who")),
		discord.NewTextDisplay("-# page "+itoa(page+1)+" of "+itoa(pages)+" · a tie goes to whoever got there first"),
	)
	return discord.NewContainer(parts...).WithAccentColor(brand.ColorNotice)
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
		groups[g] = append(groups[g], classMark(ch.class)+"**"+fullName(*ch)+"** · lv "+itoa(ch.level)+" · <@"+c.user.String()+">")
	}
	m.mu.Unlock()
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	slices.Sort(names)
	var parts []string
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
