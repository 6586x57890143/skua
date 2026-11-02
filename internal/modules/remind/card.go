package remind

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// Every surface here is a card (UX.md): the module's emoji, remind and
// what the card is, then the reminder's words as its heading. A time is a
// Discord timestamp, so each reader sees it in their own zone.

func stamp(t time.Time, style string) string {
	return "<t:" + strconv.FormatInt(t.Unix(), 10) + ":" + style + ">"
}

// icon is b wearing the app emoji key, once the emoji have synced.
func icon(b discord.ButtonComponent, key string) discord.ButtonComponent {
	if e := brand.ComponentEmoji(key); e != nil {
		return b.WithEmoji(*e)
	}
	return b
}

func v2(color int, what, body string, extra ...discord.ContainerSubComponent) []discord.LayoutComponent {
	return []discord.LayoutComponent{brand.Card(color, id, what, body, extra...)}
}

// where is where r goes, as its card says it.
func where(r reminder) string {
	switch {
	case r.target == "channel" && r.targetID != 0:
		return "<#" + r.targetID.String() + ">"
	case r.target == "channel":
		return "<#" + r.channel.String() + ">"
	}
	return "your dms"
}

// set is the reply to /remind and to every change made on it: what it
// is, when, how often, where, and the controls to change each.
func set(r reminder, now time.Time, noZone bool) discord.MessageCreate {
	return discord.MessageCreate{
		Components:      setCard(r, now, noZone, ""),
		Flags:           discord.MessageFlagIsComponentsV2 | discord.MessageFlagEphemeral,
		AllowedMentions: core.NoPings(),
	}
}

func setCard(r reminder, now time.Time, noZone bool, note string) []discord.LayoutComponent {
	body := "### " + r.text + "\n" + core.Readout([][2]string{
		{"when", stamp(r.due, "f") + " · " + stamp(r.due, "R")},
		{"repeat", r.r.String()},
		{"to", where(r)},
	})
	foot := "-# kept in " + strings.ToLower(r.tz)
	if noZone {
		foot = "-# times read as utc; add tz to /remind once and she keeps it"
	}
	if note != "" {
		foot = note + "\n" + foot
	}
	sid := strconv.FormatInt(r.id, 10)
	return v2(brand.ColorOK, "set", body+foot,
		discord.NewActionRow(repeatMenu(r, sid)),
		discord.NewActionRow(
			icon(discord.NewSecondaryButton("edit", id+":edit:"+sid), "btn_edit"),
			icon(discord.NewSecondaryButton("cancel", id+":x:"+sid), "btn_cancel"),
		),
	)
}

// repeatMenu offers the repeats that keep r's time of day: daily,
// weekdays, its weekday, its day of the month.
func repeatMenu(r reminder, sid string) discord.StringSelectMenuComponent {
	l := r.due.In(r.loc())
	opts := []discord.StringSelectMenuOption{
		{Label: "doesn't repeat", Value: "never", Description: "just this once"},
		{Label: "every day", Value: "day"},
		{Label: "weekdays", Value: "weekdays", Description: "monday to friday"},
		{Label: "every " + strings.ToLower(l.Weekday().String()), Value: "week"},
		{Label: "the " + ordinal(l.Day()) + " of each month", Value: "month"},
	}
	cur := "never"
	switch {
	case r.r.mday > 0:
		cur = "month"
	case r.r.mask == everyDay:
		cur = "day"
	case r.r.mask == weekdays:
		cur = "weekdays"
	case r.r.mask != 0:
		cur = "week"
	}
	for i := range opts {
		opts[i].Default = opts[i].Value == cur
	}
	return discord.NewStringSelectMenu(id+":rep:"+sid, "repeat", opts...)
}

// fired is the reminder itself. In a channel it pings its owner and no one
// else; in their dms a dm is ping enough.
func fired(r reminder, late time.Duration, inChannel bool) discord.MessageCreate {
	sub := "-# set " + core.Duration(r.due.Sub(r.created)) + " ago"
	if inChannel {
		sub = "-# for <@" + r.owner.String() + "> · set " + core.Duration(r.due.Sub(r.created)) + " ago"
	}
	if !r.r.once() {
		sub += " · " + r.r.String()
	}
	if late >= lateBy {
		sub += "\n-# ! late by " + core.Duration(late) + "; skua was away when it was due"
	}
	key := strconv.FormatInt(r.id, 10) + ":" + strconv.FormatInt(r.due.UnixMilli(), 10)
	row := discord.NewActionRow(
		icon(discord.NewSecondaryButton("done", id+":done:"+key), "btn_done"),
		icon(discord.NewSecondaryButton("10m", id+":snz:"+key+":10"), "btn_snooze"),
		icon(discord.NewSecondaryButton("1h", id+":snz:"+key+":60"), "btn_snooze"),
		icon(discord.NewSecondaryButton("tomorrow", id+":snz:"+key+":tmr"), "btn_snooze"),
	)
	mentions := core.NoPings()
	if inChannel {
		mentions.Users = []snowflake.ID{r.owner}
	}
	return discord.MessageCreate{
		Components:      v2(brand.ColorInfo, "now", "### "+r.text+"\n"+sub, row),
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: mentions,
	}
}

// landed is how close to its due time the card under a press arrived, by
// Discord's clock: "landed 3 ms late". Nothing past lateBy, which the card
// already says.
func landed(msg snowflake.ID, dueMS int64) string {
	off := msg.Time().Sub(time.UnixMilli(dueMS))
	if off.Abs() >= lateBy || msg == 0 {
		return ""
	}
	side := "late"
	if off < 0 {
		side = "early"
	}
	return " · landed " + ms(off.Abs()) + " " + side
}

// click is every button and select on remind's cards. Who pressed and
// what they pressed come from their client, so the reminder is read
// again and has to be theirs.
func (m *Module) click(ctx context.Context, e *events.ComponentInteractionCreate) error {
	f := strings.Split(e.Data.CustomID(), ":")
	if len(f) < 3 {
		return core.Tell("that card is out of date")
	}
	rid, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil {
		return core.Tell("that card is out of date")
	}
	r, ok, err := get(ctx, m.db, rid)
	switch {
	case err != nil:
		return err
	case !ok:
		return core.Tell("that reminder is gone")
	case r.owner != e.User().ID:
		return core.Tell("that's someone else's reminder")
	case r.state == "done" && (f[1] == "edit" || f[1] == "rep"):
		return core.Tell("that reminder has already come; set a new one with /remind")
	}
	now := m.now()
	var dueMS int64
	if len(f) > 3 {
		dueMS, _ = strconv.ParseInt(f[3], 10, 64)
	}
	switch f[1] {
	case "done":
		return e.UpdateMessage(discord.NewMessageUpdateV2(v2(brand.ColorOK, "done", "### "+r.text+"\n-# ✓ done"+landed(e.Message.ID, dueMS))...))
	case "snz":
		if len(f) < 5 {
			return core.Tell("that card is out of date")
		}
		return m.snooze(ctx, e, r, f[4], dueMS, now)
	case "x":
		if _, err := cancel(ctx, m.db, r.id, r.owner); err != nil {
			return err
		}
		return e.UpdateMessage(discord.NewMessageUpdateV2(v2(brand.ColorIdle, "cancelled", "### "+r.text+"\n-# ✓ it won't come")...))
	case "edit":
		return e.Modal(form(r))
	case "rep":
		v := e.StringSelectMenuInteractionData().Values
		if len(v) != 1 {
			return core.Tell("pick one")
		}
		rd := repeatAs(reading{at: r.due, r: r.r}, v[0], r.loc())
		if rd.r.once() && !r.r.once() {
			// A repeat made a one-off keeps its next turn.
			rd.at = r.due
		}
		r.due, r.r = rd.at, rd.r
		if _, err := reschedule(ctx, m.db, r); err != nil {
			return err
		}
		m.hold(r)
		return e.UpdateMessage(discord.NewMessageUpdateV2(setCard(r, now, false, "✓ repeat "+r.r.String())...))
	}
	return core.Tell("that card is out of date")
}

// snooze brings r back after what was pressed: a one-off moves, a repeat
// keeps its turns and gets a one-off copy.
func (m *Module) snooze(ctx context.Context, e *events.ComponentInteractionCreate, r reminder, by string, dueMS int64, now time.Time) error {
	back := now
	switch by {
	case "10":
		back = now.Add(10 * time.Minute)
	case "60":
		back = now.Add(time.Hour)
	case "tmr":
		l, d := now.In(r.loc()), time.UnixMilli(dueMS).In(r.loc())
		back = time.Date(l.Year(), l.Month(), l.Day()+1, d.Hour(), d.Minute(), 0, 0, r.loc())
	default:
		return core.Tell("that card is out of date")
	}
	back = back.Truncate(time.Second)
	snoozed := r
	snoozed.due, snoozed.state = back, "pending"
	if r.r.once() {
		if _, err := reschedule(ctx, m.db, snoozed); err != nil {
			return err
		}
	} else {
		snoozed.r = rule{}
		var err error
		if snoozed.id, err = insert(ctx, m.db, snoozed); err != nil {
			return err
		}
	}
	m.hold(snoozed)
	sid := strconv.FormatInt(snoozed.id, 10)
	return e.UpdateMessage(discord.NewMessageUpdateV2(v2(brand.ColorIdle, "snoozed",
		"### "+r.text+"\n-# back "+stamp(back, "R")+" at "+stamp(back, "t")+landed(e.Message.ID, dueMS),
		discord.NewActionRow(icon(discord.NewSecondaryButton("cancel", id+":x:"+sid), "btn_cancel")),
	)...))
}

// typed is r's time as a member would type it back, which read takes.
func typed(r reminder) string {
	l := r.due.In(r.loc())
	at := fmt.Sprintf("%02d:%02d", r.r.at/60, r.r.at%60)
	switch {
	case r.r.once():
		return l.Format("2006-01-02 15:04")
	case r.r.mday > 0:
		return "every month on the " + ordinal(r.r.mday) + " at " + at
	case r.r.mask == everyDay:
		return "every day at " + at
	case r.r.mask == weekdays:
		return "every weekday at " + at
	case r.r.mask == weekends:
		return "every weekend at " + at
	}
	var days []string
	for d := range 7 {
		if r.r.mask&(1<<d) != 0 {
			days = append(days, dayNames[d][:3])
		}
	}
	return "every " + strings.Join(days, " ") + " at " + at
}

// form is the edit form, filled in with r as it stands.
func form(r reminder) discord.ModalCreate {
	return discord.ModalCreate{
		CustomID: editID + ":" + strconv.FormatInt(r.id, 10),
		Title:    "change this reminder",
		Components: []discord.LayoutComponent{
			discord.NewLabel("what", discord.TextInputComponent{CustomID: "what", Style: discord.TextInputStyleShort, Required: true, MaxLength: 300, Value: r.text}),
			discord.NewLabel("when", discord.TextInputComponent{CustomID: "when", Style: discord.TextInputStyleShort, Required: true, MaxLength: 100, Value: typed(r), Placeholder: "in 20m, fri 5pm or every weekday at 9"}),
		},
	}
}

// submit is the edit form sent back.
func (m *Module) submit(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	rid, err := strconv.ParseInt(strings.TrimPrefix(e.Data.CustomID, editID+":"), 10, 64)
	if err != nil {
		return core.Tell("that form is out of date")
	}
	r, ok, err := get(ctx, m.db, rid)
	switch {
	case err != nil:
		return err
	case !ok:
		return core.Tell("that reminder is gone")
	case r.owner != e.User().ID:
		return core.Tell("that's someone else's reminder")
	case r.state == "done":
		return core.Tell("that reminder has already come; set a new one with /remind")
	}
	now := m.now()
	if e.Data.Text("when") == typed(r) {
		// Left as filled in: the exact time stands, seconds and all.
		if r.text = oneLine(e.Data.Text("what")); r.text == "" {
			return core.Tell("say what to remind you of")
		}
		if _, err := reschedule(ctx, m.db, r); err != nil {
			return err
		}
		m.hold(r)
		return e.UpdateMessage(discord.NewMessageUpdateV2(setCard(r, now, false, "✓ changed")...))
	}
	rs := read(e.Data.Text("when"), now, r.loc())
	if len(rs) == 0 {
		return core.Tell("skua can't read " + clip(e.Data.Text("when"), 40) + " as a time; try in 20m, fri 5pm or 7 nov 09:00")
	}
	if !rs[0].at.After(now) || rs[0].at.Sub(now) > horizonYr {
		return core.Tell("pick a time between now and a year from now")
	}
	if r.text = oneLine(e.Data.Text("what")); r.text == "" {
		return core.Tell("say what to remind you of")
	}
	r.due, r.r, r.state = rs[0].at, rs[0].r, "pending"
	if _, err := reschedule(ctx, m.db, r); err != nil {
		return err
	}
	m.hold(r)
	return e.UpdateMessage(discord.NewMessageUpdateV2(setCard(r, now, false, "✓ changed")...))
}
