package pp

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/pixel"
)

// /pp is one reply, the status screen, and everything else is a button on
// it, the way an old rpg's menu opens from one key.

var (
	errNotServer = core.Tell("/pp works in a server, where the points are")
	errNotAClass = core.Tell("that isn't one of the classes; open /pp and pick again")
	errBeta      = core.Tell("/pp is in closed beta here for now: admins and the beta testers only")
	errNotAdmin  = core.Tell("only this server's admins change how /pp runs here")
)

// screenName is the status screen's file on its message.
const screenName = "pp.jpg"

func (m *Module) Commands() []core.Command {
	return []core.Command{{
		Create: discord.SlashCommandCreate{Name: "pp", Description: "your pot points, your level and how your day is going"},
		Tier:   core.Public,
		Run: func(_ context.Context, e *events.ApplicationCommandInteractionCreate) error {
			guild := e.GuildID()
			if guild == nil {
				return errNotServer
			}
			if !m.plays(e) {
				msg, err := m.closedMessage(*guild)
				if err != nil {
					return err
				}
				return e.CreateMessage(msg)
			}
			msg, err := m.status(*guild, e.Member(), true)
			if err != nil {
				return err
			}
			return e.CreateMessage(m.withMenu(*guild, e.User().ID, m.Admin != nil && m.Admin(e), msg, "pp:status").WithEphemeral(true))
		},
	}}
}

func (m *Module) Components() []core.Component {
	return []core.Component{{ID: "pp", Run: m.press}}
}

// press is a button or select on a /pp reply: class opens the picker, pick
// takes a class, show posts the screen for the channel, and every other
// screen opens through answer with the menu under it.
func (m *Module) press(ctx context.Context, e *events.ComponentInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return errNotServer
	}
	switch id := e.Data.CustomID(); {
	case id == "pp:beta":
		return m.setBeta(ctx, *guild, e)
	case id == "pp:log":
		return m.setLog(ctx, *guild, e)
	case id == "pp:selfies":
		return m.setSelfies(ctx, *guild, e)
	case id == "pp:anchor", id == "pp:heralds":
		return m.setLivery(ctx, *guild, e)
	case id == "pp:liveries":
		return m.liveryPanel(*guild, e)
	case id == "pp:openyard":
		return m.openYard(ctx, *guild, e)
	case id == "pp:keys":
		return m.openKeys(e)
	case id == "pp:redeem":
		if m.Grant == nil {
			return errBeta
		}
		return e.Modal(redeemModal())
	case id == "pp:grant":
		if m.Admin == nil || !m.Admin(e) {
			return errNotAdmin
		}
		return e.Modal(grantModal())
	case strings.HasPrefix(id, "pp:herald:"):
		return m.herald(ctx, *guild, e)
	case strings.HasPrefix(id, "pp:approve:"), strings.HasPrefix(id, "pp:refuse:"):
		return m.settle(ctx, *guild, e, strings.HasPrefix(id, "pp:approve:"))
	}
	if !m.plays(e) {
		return errBeta
	}
	id := e.Data.CustomID()
	if strings.HasPrefix(id, "pp:reroll:") {
		return m.rerollPicked(*guild, e)
	}
	var msg discord.MessageCreate
	var err error
	switch {
	case strings.HasPrefix(id, "pp:lore:"):
		// The custom ID comes from the client, so it may name no class.
		i, page, ok := parseLore(id)
		if !ok {
			return errNotAClass
		}
		id = "pp:class"
		msg, err = m.lore(*guild, e.User().ID, i, page)
	case strings.HasPrefix(id, "pp:pick:"):
		if err := m.pick(*guild, e.User().ID, strings.TrimPrefix(id, "pp:pick:")); err != nil {
			return err
		}
		id = "pp:status"
		msg, err = m.status(*guild, e.Member(), true)
	case id == "pp:status":
		msg, err = m.status(*guild, e.Member(), true)
	case id == "pp:class":
		msg, err = m.lore(*guild, e.User().ID, -1, 0)
	case id == "pp:show":
		msg, err := m.status(*guild, e.Member(), false)
		if err != nil {
			return err
		}
		return e.CreateMessage(msg)
	case id == "pp:board":
		msg, err = m.boardMessage(*guild, e.User().ID)
	case id == "pp:treasury":
		if m.Admin == nil || !m.Admin(e) {
			return errNotAdmin
		}
		msg, err = m.treasuryMessage(ctx, *guild)
	case id == "pp:quests":
		msg, err = m.questsMessage(*guild, e.Member())
	case id == "pp:buffs":
		msg, err = m.buffsMessage(*guild, e.Member())
	case id == "pp:shop", id == "pp:climb", id == "pp:look", id == "pp:back":
		return m.shop(ctx, *guild, e)
	case strings.HasPrefix(id, "pp:yard"), id == "pp:dye", strings.HasPrefix(id, "pp:lease:"), strings.HasPrefix(id, "pp:own"):
		return m.yard(ctx, *guild, e)
	case id == "pp:ledger":
		msg, err = m.journal(ctx, *guild, e.Member())
	default:
		return errNotAClass
	}
	if err != nil {
		return err
	}
	return m.answer(*guild, e, msg, id)
}

// answer shows msg, the screen current, with the menu under it: in place of
// the screen pressed on while the chat hasn't moved past it, or as a new
// one at the bottom once it has, so the menu is always at hand.
func (m *Module) answer(guild snowflake.ID, e *events.ComponentInteractionCreate, msg discord.MessageCreate, current string) error {
	msg = m.withMenu(guild, e.User().ID, m.Admin != nil && m.Admin(e), msg, current)
	if moved(e) {
		return e.CreateMessage(msg.WithEphemeral(true))
	}
	return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...))
}

// moved is whether the channel has had a message since the one pressed on.
// Ephemeral replies don't count, so a member paging through /pp in a quiet
// channel keeps one message.
func moved(e *events.ComponentInteractionCreate) bool {
	ch := e.Channel().MessageChannel
	if ch == nil {
		return false
	}
	last := ch.LastMessageID()
	return last != nil && *last > e.Message.ID
}

// withMenu is msg with the menu added to its card, once user has picked a
// class; before that the title and the picker are all there is.
func (m *Module) withMenu(guild, user snowflake.ID, admin bool, msg discord.MessageCreate, current string) discord.MessageCreate {
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, user, DayOf(m.now()))
	class, picked := mem.class, mem.classSeason != -1
	s.mu.Unlock()
	if !picked || len(msg.Components) == 0 {
		return msg
	}
	card, ok := msg.Components[0].(discord.ContainerComponent)
	if !ok {
		return msg
	}
	card.Components = append(card.Components, menuRows(class, admin, current)...)
	msg.Components = append([]discord.LayoutComponent{card}, msg.Components[1:]...)
	return msg
}

// cost is what changing class costs mem today and the first day they may:
// nothing for the season's first pick or for changing it within ClassGrace
// days, then ClassFee pot days, each paid change starting a wait of
// ClassWait days. s.mu held.
func (m *Module) cost(mem *member, season, today int64) (cost, from int64) {
	b := m.st.bal()
	switch {
	case mem.classSeason != season:
		return 0, today
	case mem.classChanges == 0 && today < mem.classDay+b.ClassGrace:
		return 0, today
	case mem.classChanges > 0:
		from = max(today, mem.classDay+b.ClassWait)
	default:
		from = today
	}
	return b.ClassFee * m.st.pot(), from
}

// pick makes user a key: the season's first pick free, every change after
// it paid for from their purse.
func (m *Module) pick(guild, user snowflake.ID, key string) error {
	if ClassOf(key).Key != key {
		return errNotAClass
	}
	today := DayOf(m.now())
	season, _ := m.st.bal().Season(today)
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := m.st.member(s, guild, user, today)
	// Their class already, unless it's the wanderer they start as and
	// haven't picked yet: that first pick still counts.
	if mem.class == key && mem.classSeason != -1 {
		return nil
	}
	cost, from := m.cost(mem, season, today)
	if from > today {
		return core.Tell(fmt.Sprintf("you changed class lately; you can change again <t:%d:R>, or free when a new season starts", dayStart(from).Unix()))
	}
	if cost > mem.purse {
		return core.Tell(fmt.Sprintf("changing class now costs %s pp and you have %s; it's free again when a new season starts",
			grouped(cost/unit), grouped(mem.purse/unit)))
	}
	switch {
	case mem.classSeason != season:
		mem.classSeason, mem.classChanges, mem.classDay = season, 0, today
	case cost > 0:
		mem.purse -= cost
		mem.classChanges++
		mem.classDay = today
		m.st.burn(s, user, today, reasonClass, cost, nil)
	}
	mem.class, mem.dirty = key, true
	return nil
}

// lore is user's class browser at class i and page, or at their own class
// for i -1: the wanderer's until they've picked one.
func (m *Module) lore(guild, user snowflake.ID, i, page int) (discord.MessageCreate, error) {
	today := DayOf(m.now())
	season, _ := m.st.bal().Season(today)
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, user, today)
	cost, from := m.cost(mem, season, today)
	purse := mem.purse
	mine := ""
	if mem.classSeason != -1 {
		mine = mem.class
	}
	s.mu.Unlock()
	if i < 0 {
		i = classIndex(mine)
	}
	return loreMessage(i, page, mine, cost, purse, from-today)
}

// status is who's screen as a message: the card, the picture and, on their
// own copy, the buttons. It counts as a sign of life and refreshes what
// they wear.
func (m *Module) status(guild snowflake.ID, who *discord.ResolvedMember, buttons bool) (discord.MessageCreate, error) {
	now := m.now()
	today := DayOf(now)
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, who.User.ID, today)
	mem.life = now
	seen(mem, &who.Member, who.User, guild)
	sh := m.sheet(s, mem, guild, who.User.ID, today)
	picked := mem.classSeason != -1
	s.mu.Unlock()
	if !picked {
		return titleMessage(buttons)
	}

	sh.Name = who.EffectiveName()
	if pixel.Plain(sh.Name) == "" {
		sh.Name = who.User.Username
	}
	pic, err := StatusScreen(sh)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := fmt.Sprintf("%s, %s, level %d, %s pp to spend", pixel.Plain(sh.Name), sh.Class.Name(), sh.Level, grouped(sh.Purse/unit))
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "status", turnsOver(today),
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// turnsOver is when the day after today starts, in the reader's own time:
// Discord shows a timestamp in each member's timezone.
func turnsOver(today int64) string {
	at := dayStart(today + 1).Unix()
	return fmt.Sprintf("-# the pot turns over <t:%d:t>, <t:%d:R>", at, at)
}

// menuRows are the menu under every /pp screen, as UX.md has them: first
// the screens it opens, then what the member does from it, every one
// wearing the icon of what it opens. The screen open now, current, is
// greyed out; on the status screen share takes the place of status.
func menuRows(class string, admin bool, current string) []discord.ContainerSubComponent {
	btn := func(label, id, icon string) discord.InteractiveComponent {
		b := withIcon(discord.NewSecondaryButton(label, id), icon)
		if id == current {
			b = b.AsDisabled()
		}
		return b
	}
	home := btn("status", "pp:status", "lantern")
	if current == "pp:status" {
		home = btn("share", "pp:show", "bell")
	}
	act := discord.NewActionRow(home, btn("lore", "pp:class", classIcons[class]), btn("buffs", "pp:buffs", "hearth"))
	if admin {
		act = act.AddComponents(btn("treasury", "pp:treasury", "pot"))
	}
	return []discord.ContainerSubComponent{
		discord.NewActionRow(btn("quests", "pp:quests", "compass"), btn("board", "pp:board", "laurel"),
			btn("shop", "pp:shop", "scales"), btn("journal", "pp:ledger", "quill")),
		act,
	}
}

// withIcon is b wearing the pp_<icon> emoji, once the emoji have synced.
func withIcon(b discord.ButtonComponent, icon string) discord.ButtonComponent {
	if e := brand.ComponentEmoji("pp_" + icon); e != nil {
		return b.WithEmoji(*e)
	}
	return b
}

// titleMessage is the title screen as a message, for a member who hasn't
// picked a class: their own copy carries the button to pick one.
func titleMessage(buttons bool) (discord.MessageCreate, error) {
	pic, err := TitleScreen()
	if err != nil {
		return discord.MessageCreate{}, err
	}
	const alt = "melting pot, a pot point journey: every class's bird in a row, and pick a class"
	extra := []discord.ContainerSubComponent{discord.NewMediaGallery(discord.MediaGalleryItem{
		Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt,
	})}
	if buttons {
		extra = append(extra, discord.NewActionRow(withIcon(discord.NewPrimaryButton("pick a class", "pp:class"), "lantern")))
	}
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "melting pot", "", extra...)},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// sheet is mem's status screen. s.mu held.
func (m *Module) sheet(s *server, mem *member, guild, user snowflake.ID, today int64) Sheet {
	b := m.st.bal()
	l, into, need := b.Level(mem.total)
	vigor, phase := b.Vigor(mem.day, m.st.pools(guild, user, today))
	season, week := b.Season(today)
	sh := Sheet{
		Portrait: Portrait(ClassOf(mem.class)), Rung: mem.rung,
		Class: ClassOf(mem.class), Level: l, Into: into, Need: need, Total: mem.total, Purse: mem.purse,
		Vigor: vigor, Phase: phase, Season: season, Week: week, Held: len(mem.quests),
	}
	for _, w := range []struct {
		on   bool
		name string
	}{{mem.wear.Crest, "crest"}, {mem.wear.Banner, "banner"}, {mem.wear.Plume, "plume"}} {
		if w.on {
			sh.Buffs = append(sh.Buffs, Buff{Icon: w.name, Label: w.name})
		}
	}
	if tier := s.hearth(); tier > 0 {
		sh.Buffs = append(sh.Buffs, Buff{Icon: "hearth", Label: hearthWord(tier)})
	}
	// The place on the board the screen's tab names: the season's race,
	// or before the first season the levels board, ranked by everything
	// earned as the board button shows it.
	switch {
	case season > 0 && mem.seasonPP > 0:
		sh.Place = 1
		for _, o := range s.members {
			if o.season == season && o.seasonPP > mem.seasonPP {
				sh.Place++
			}
		}
	case season <= 0 && mem.total > 0:
		sh.Place = 1
		for _, o := range s.members {
			if o.total > mem.total {
				sh.Place++
			}
		}
	}
	return sh
}
