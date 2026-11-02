package remind

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// /reminders is a member's agenda: everything they have waiting, by day,
// a select to open one, and buttons to set a new one or change how
// reminders come to them. It is laid out like a /help page (UX.md): the
// module's icon and line, then the body at full width.

const (
	newID  = "remind-new"
	zoneID = "remind-zone"
	// shown is how many reminders the agenda lists: one select's worth.
	shown = 25
)

func agendaButton() discord.ButtonComponent {
	return discord.NewSecondaryButton("◂ agenda", id+":agenda")
}

func (m *Module) agendaCommand() core.Command {
	return core.Command{
		Create: discord.SlashCommandCreate{
			Name:        "reminders",
			Description: "what you have waiting, and how reminders come to you",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		},
		Tier: core.Public,
		Run: func(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
			msg, err := m.agendaPanel(ctx, e.User().ID, "")
			if err != nil {
				return err
			}
			msg.Flags = msg.Flags.Add(discord.MessageFlagEphemeral)
			return e.CreateMessage(msg)
		},
	}
}

// dayWord is the agenda's heading for the day t falls on, in loc.
func dayWord(t, now time.Time, loc *time.Location) string {
	l, n := t.In(loc), now.In(loc)
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, loc) }
	switch day(l).Sub(day(n)).Round(time.Hour) / (24 * time.Hour) {
	case 0:
		return "today"
	case 1:
		return "tomorrow"
	}
	if l.Year() != n.Year() {
		return strings.ToLower(l.Format("Mon 2 Jan 2006"))
	}
	return strings.ToLower(l.Format("Mon 2 Jan"))
}

// short is the rule as the agenda tags a line with.
func (r rule) short() string {
	switch {
	case r.once():
		return ""
	case r.mday > 0:
		return "monthly"
	case r.mask == everyDay:
		return "daily"
	case r.mask == weekdays:
		return "weekdays"
	case r.mask == weekends:
		return "weekends"
	case bits(r.mask) == 1:
		return "weekly"
	}
	return "some days"
}

// agendaPanel is /reminders for user.
func (m *Module) agendaPanel(ctx context.Context, user snowflake.ID, note string) (discord.MessageCreate, error) {
	who, err := m.person(ctx, user)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	rs, err := agenda(ctx, m.db, user, shown+1)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	now, loc := m.now(), zone(who.tz)
	more := len(rs) > shown
	rs = rs[:min(len(rs), shown)]

	file, iconURL := brand.ModuleIcon(id, brand.ColorInfo)
	var files []*discord.File
	if file != nil {
		files = append(files, file)
	}
	zoneWord := "utc, no zone set"
	if who.tz != "" {
		zoneWord = strings.ToLower(who.tz)
	}
	head := "## reminders\n-# " + waiting(len(rs), more) + " · " + zoneWord
	if note != "" {
		head += "\n" + note
	}
	var b strings.Builder
	last := ""
	for _, r := range rs {
		if d := dayWord(r.due, now, loc); d != last {
			if last != "" {
				b.WriteString("\n")
			}
			b.WriteString("**" + d + "**\n")
			last = d
		}
		b.WriteString(stamp(r.due, "t") + " · " + r.text)
		if s := r.r.short(); s != "" {
			b.WriteString(" · " + s)
		}
		b.WriteString("\n")
	}
	list := strings.TrimSuffix(b.String(), "\n")
	if len(rs) == 0 {
		list = "nothing waiting; set one with new or /remind"
	}
	if more {
		list += "\n-# and more after these"
	}
	body := []discord.ContainerSubComponent{
		discord.NewSection(discord.NewTextDisplay(head)).WithAccessory(discord.NewThumbnail(iconURL)),
		discord.NewSmallSeparator(),
		discord.NewTextDisplay(list),
	}
	if len(rs) > 0 {
		opts := make([]discord.StringSelectMenuOption, 0, len(rs))
		for _, r := range rs {
			desc := strings.ToLower(r.due.In(loc).Format("Mon 2 Jan 15:04"))
			if s := r.r.short(); s != "" {
				desc += " · " + s
			}
			opts = append(opts, discord.StringSelectMenuOption{Label: clip(r.text, 100), Value: strconv.FormatInt(r.id, 10), Description: desc})
		}
		body = append(body, discord.NewActionRow(discord.NewStringSelectMenu(id+":open", "open one to change it", opts...)))
	}
	body = append(body,
		discord.NewSmallSeparator(),
		discord.NewActionRow(
			icon(discord.NewPrimaryButton("new", id+":new"), "btn_new"),
			icon(discord.NewSecondaryButton("settings", id+":set"), "btn_settings"),
			icon(discord.NewSecondaryButton("teams", id+":teams"), "btn_team"),
		),
	)
	foot := "-# a dm comes with snooze buttons; times show in your own zone"
	if len(rs) > 0 {
		foot = "-# next one " + stamp(rs[0].due, "R")
	}
	body = append(body, discord.NewTextDisplay(foot))
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{discord.NewContainer(body...).WithAccentColor(brand.ColorInfo)},
		Files:           files,
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

func waiting(n int, more bool) string {
	switch {
	case more:
		return strconv.Itoa(n) + "+ waiting"
	case n == 1:
		return "1 waiting"
	}
	return strconv.Itoa(n) + " waiting"
}

// update is msg as an edit in place, its icon brought again if it still
// needs one.
func update(msg discord.MessageCreate) discord.MessageUpdate {
	u := v2Update(msg.Components)
	u.Files, u.AllowedMentions = msg.Files, msg.AllowedMentions
	return u
}

// v2Update is an edit to c in place, dropping any icon the screen before
// it attached.
func v2Update(c []discord.LayoutComponent) discord.MessageUpdate {
	u := discord.NewMessageUpdateV2(c...)
	u.Attachments = &[]discord.AttachmentUpdate{}
	return u
}

// settingsCard is how user's reminders come to them, and the controls to
// change it.
func (m *Module) settingsCard(ctx context.Context, user snowflake.ID, note string) ([]discord.LayoutComponent, error) {
	who, err := m.person(ctx, user)
	if err != nil {
		return nil, err
	}
	zoneWord, deliverWord := "utc, not set", "your dms"
	if who.tz != "" {
		zoneWord = strings.ToLower(who.tz)
	}
	if who.deliver == "here" {
		deliverWord = "where you set it"
	}
	var words []string
	for _, n := range who.snooze {
		words = append(words, snoozeWord(n))
	}
	body := core.Readout([][2]string{
		{"zone", zoneWord},
		{"delivery", deliverWord},
		{"snooze", strings.Join(words, ", ")},
		{"quiet", who.quietWords()},
	})
	if note != "" {
		body += note + "\n"
	}
	body += "-# quiet hours hold a dm until they end; a reminder in a channel still comes"

	deliver := discord.NewStringSelectMenu(id+":deliver", "where a reminder goes",
		discord.StringSelectMenuOption{Label: "to your dms", Value: "dm", Description: "unless you say otherwise", Default: who.deliver != "here"},
		discord.StringSelectMenuOption{Label: "to where you set it", Value: "here", Description: "the channel you ran /remind in", Default: who.deliver == "here"},
	)
	var snoozeOpts []discord.StringSelectMenuOption
	for _, n := range snoozes {
		snoozeOpts = append(snoozeOpts, discord.StringSelectMenuOption{Label: snoozeWord(n), Value: strconv.Itoa(n), Default: slices.Contains(who.snooze, n)})
	}
	snooze := discord.NewStringSelectMenu(id+":snooze", "snooze buttons, up to three", snoozeOpts...)
	one := 1
	snooze.MinValues, snooze.MaxValues = &one, 3
	var quietOpts []discord.StringSelectMenuOption
	for _, q := range quiets {
		p := person{quietFrom: q[0], quietTo: q[1]}
		quietOpts = append(quietOpts, discord.StringSelectMenuOption{Label: quietLabel(p), Value: strconv.Itoa(q[0]) + "-" + strconv.Itoa(q[1]), Default: q[0] == who.quietFrom && q[1] == who.quietTo || q[0] < 0 && who.quietWords() == "off"})
	}
	quiet := discord.NewStringSelectMenu(id+":quiet", "quiet hours", quietOpts...)
	return v2(brand.ColorInfo, "settings", body,
		discord.NewActionRow(deliver),
		discord.NewActionRow(snooze),
		discord.NewActionRow(quiet),
		discord.NewActionRow(agendaButton(), icon(discord.NewSecondaryButton("zone", id+":zone"), "btn_zone")),
	), nil
}

// quietLabel is a quiet hours choice as the closed select shows it, so it
// says what it is about: "no quiet hours", "quiet 23:00 → 07:00".
func quietLabel(p person) string {
	if w := p.quietWords(); w != "off" {
		return "quiet " + w
	}
	return "no quiet hours"
}

// panelClick is the agenda's and the settings' controls, which act on
// the member who pressed them and nobody else.
func (m *Module) panelClick(ctx context.Context, e *events.ComponentInteractionCreate, verb string) error {
	user := e.User().ID
	settings := func(note string) error {
		c, err := m.settingsCard(ctx, user, note)
		if err != nil {
			return err
		}
		return e.UpdateMessage(v2Update(c))
	}
	switch verb {
	case "agenda":
		msg, err := m.agendaPanel(ctx, user, "")
		if err != nil {
			return err
		}
		return e.UpdateMessage(update(msg))
	case "open":
		v := values(e)
		rid, err := strconv.ParseInt(strings.Join(v, ""), 10, 64)
		if len(v) != 1 || err != nil {
			return core.Tell("pick one")
		}
		r, ok, err := get(ctx, m.db, rid)
		switch {
		case err != nil:
			return err
		case !ok || r.owner != user || r.state != "pending":
			return core.Tell("that reminder is gone")
		}
		return e.UpdateMessage(v2Update(setCard(r, m.now(), false, "")))
	case "new":
		return e.Modal(newForm())
	case "set":
		return settings("")
	case "zone":
		return e.Modal(zoneForm())
	case "deliver":
		v := values(e)
		if len(v) != 1 || v[0] != "dm" && v[0] != "here" {
			return core.Tell("pick one")
		}
		if _, err := m.change(ctx, user, func(p *person) { p.deliver = v[0] }); err != nil {
			return err
		}
		return settings("✓ delivery changed")
	case "snooze":
		var picked []int
		for _, v := range values(e) {
			n, err := strconv.Atoi(v)
			if err != nil || !slices.Contains(snoozes, n) {
				return core.Tell("pick from the list")
			}
			picked = append(picked, n)
		}
		if len(picked) == 0 || len(picked) > 3 {
			return core.Tell("pick one to three")
		}
		// Shortest first, tomorrow last.
		slices.SortFunc(picked, func(a, b int) int { return slices.Index(snoozes, a) - slices.Index(snoozes, b) })
		if _, err := m.change(ctx, user, func(p *person) { p.snooze = picked }); err != nil {
			return err
		}
		return settings("✓ snooze buttons changed")
	case "quiet":
		v := values(e)
		if len(v) != 1 {
			return core.Tell("pick one")
		}
		i := slices.IndexFunc(quiets, func(q [2]int) bool { return strconv.Itoa(q[0])+"-"+strconv.Itoa(q[1]) == v[0] })
		if i < 0 {
			return core.Tell("pick from the list")
		}
		if _, err := m.change(ctx, user, func(p *person) { p.quietFrom, p.quietTo = quiets[i][0], quiets[i][1] }); err != nil {
			return err
		}
		return settings("✓ quiet hours changed")
	}
	return core.Tell("that card is out of date")
}

// newForm is a new reminder from the agenda.
func newForm() discord.ModalCreate {
	return discord.ModalCreate{
		CustomID: newID,
		Title:    "a new reminder",
		Components: []discord.LayoutComponent{
			discord.NewLabel("what", discord.TextInputComponent{CustomID: "what", Style: discord.TextInputStyleShort, Required: true, MaxLength: 300}),
			discord.NewLabel("when", discord.TextInputComponent{CustomID: "when", Style: discord.TextInputStyleShort, Required: true, MaxLength: 100, Placeholder: "in 20m, fri 5pm or every weekday at 9"}),
			discord.NewLabel("repeat", discord.NewStringSelectMenu("repeat", "as when says",
				discord.StringSelectMenuOption{Label: "as when says", Value: "as"},
				discord.StringSelectMenuOption{Label: "never", Value: "never"},
				discord.StringSelectMenuOption{Label: "every day", Value: "day"},
				discord.StringSelectMenuOption{Label: "weekdays", Value: "weekdays"},
				discord.StringSelectMenuOption{Label: "every week", Value: "week"},
				discord.StringSelectMenuOption{Label: "every month", Value: "month"},
			).WithMinValues(0)),
			discord.NewLabel("to", discord.NewStringSelectMenu("to", "as in your settings",
				discord.StringSelectMenuOption{Label: "your dms", Value: "dm"},
				discord.StringSelectMenuOption{Label: "this channel", Value: "here"},
			).WithMinValues(0)),
		},
	}
}

// create is the new reminder form sent back.
func (m *Module) create(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return core.Tell("remind only works in a server")
	}
	rep := strings.Join(e.Data.StringValues("repeat"), "")
	if rep == "as" {
		rep = ""
	}
	r, err := m.draft(ctx, *guild, e.User().ID, e.Channel().ID(), e.Data.Text("when"), e.Data.Text("what"), rep)
	if err != nil {
		return err
	}
	to, said := e.Data.OptStringValues("to")
	said = said && len(to) == 1
	if err := m.aim(ctx, &r, strings.Join(to, ""), said, e.Member()); err != nil {
		return err
	}
	if r, err = m.keep(ctx, r); err != nil {
		return err
	}
	c := setCard(r, m.now(), m.zoneOf(ctx, r.owner) == "", "✓ set")
	return e.UpdateMessage(v2Update(c))
}

func zoneForm() discord.ModalCreate {
	return discord.ModalCreate{
		CustomID: zoneID,
		Title:    "your timezone",
		Components: []discord.LayoutComponent{
			discord.NewLabel("a city or region", discord.TextInputComponent{CustomID: "zone", Style: discord.TextInputStyleShort, Required: true, MaxLength: 64, Placeholder: "berlin, new york or asia/tokyo"}),
		},
	}
}

// abbreviations are the zone names people type that aren't zones.
var abbreviations = map[string]string{
	"est": "America/New_York", "edt": "America/New_York", "et": "America/New_York",
	"cst": "America/Chicago", "cdt": "America/Chicago", "ct": "America/Chicago",
	"mst": "America/Denver", "mdt": "America/Denver",
	"pst": "America/Los_Angeles", "pdt": "America/Los_Angeles", "pt": "America/Los_Angeles",
	"gmt": "Europe/London", "bst": "Europe/London", "utc": "UTC",
	"cet": "Europe/Berlin", "cest": "Europe/Berlin", "eet": "Europe/Helsinki",
	"ist": "Asia/Kolkata", "jst": "Asia/Tokyo", "kst": "Asia/Seoul",
	"aest": "Australia/Sydney", "aedt": "Australia/Sydney",
}

// findZone is the zone q names: exactly, as a common abbreviation, as a
// city ("new york"), as the start of one, and only then anywhere in a
// name, so "est" is never Creston.
func findZone(q string) string {
	if z := known(q); z != "" {
		return z
	}
	q = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(q)), " ", "_")
	if z, ok := abbreviations[q]; ok {
		return z
	}
	if q == "" {
		return ""
	}
	city := func(z string) string { return strings.ToLower(z[strings.LastIndex(z, "/")+1:]) }
	for _, match := range []func(z string) bool{
		func(z string) bool { return city(z) == q },
		func(z string) bool { return strings.HasPrefix(city(z), q) },
		func(z string) bool { return strings.Contains(strings.ToLower(z), q) },
	} {
		for _, z := range zoneNames {
			if match(z) {
				return z
			}
		}
	}
	return ""
}

// setZone is the zone form sent back.
func (m *Module) setZone(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	typed := e.Data.Text("zone")
	z := findZone(typed)
	if z == "" {
		return core.Tell("skua can't find a timezone for " + clip(typed, 40) + "; try a big city near you")
	}
	if err := m.keepZone(ctx, e.User().ID, z); err != nil {
		return err
	}
	c, err := m.settingsCard(ctx, e.User().ID, "✓ zone "+strings.ToLower(z)+", "+m.now().In(zone(z)).Format("15:04")+" there now")
	if err != nil {
		return err
	}
	return e.UpdateMessage(v2Update(c))
}
