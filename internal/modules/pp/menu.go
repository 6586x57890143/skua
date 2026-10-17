package pp

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

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
				return errBeta
			}
			msg, err := m.status(*guild, e.Member(), true)
			if err != nil {
				return err
			}
			return e.CreateMessage(msg.WithEphemeral(true))
		},
	}}
}

func (m *Module) Components() []core.Component {
	return []core.Component{{ID: "pp", Run: m.press}}
}

// press is a button or select on a /pp reply: class opens the picker, pick
// takes a class, show posts the screen for the channel.
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
	case id == "pp:grant":
		if m.Admin == nil || !m.Admin(e) {
			return errNotAdmin
		}
		return e.Modal(grantModal())
	case strings.HasPrefix(id, "pp:approve:"), strings.HasPrefix(id, "pp:refuse:"):
		return m.settle(ctx, *guild, e, strings.HasPrefix(id, "pp:approve:"))
	}
	if !m.plays(e) {
		return errBeta
	}
	switch e.Data.CustomID() {
	case "pp:class":
		return e.CreateMessage(discord.MessageCreate{
			Components:      []discord.LayoutComponent{classCard(m.price(*guild, e.User().ID))},
			Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
			AllowedMentions: core.NoPings(),
		})
	case "pp:pick":
		// The custom ID comes from the client, so the data may not be a
		// select's at all.
		data, ok := e.Data.(discord.StringSelectMenuInteractionData)
		if !ok || len(data.Values) != 1 {
			return errNotAClass
		}
		if err := m.pick(*guild, e.User().ID, data.Values[0]); err != nil {
			return err
		}
		msg, err := m.status(*guild, e.Member(), true)
		if err != nil {
			return err
		}
		return e.UpdateMessage(core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...))
	case "pp:show":
		msg, err := m.status(*guild, e.Member(), false)
		if err != nil {
			return err
		}
		return e.CreateMessage(msg)
	case "pp:quests":
		msg, err := m.questsMessage(*guild, e.Member())
		if err != nil {
			return err
		}
		return e.CreateMessage(msg)
	case "pp:reroll":
		return m.rerollPicked(*guild, e)
	case "pp:shop", "pp:climb":
		return m.shop(ctx, *guild, e, e.Data.CustomID() == "pp:climb")
	case "pp:ledger":
		msg, err := m.receipts(ctx, *guild, e.User().ID)
		if err != nil {
			return err
		}
		return e.CreateMessage(msg)
	}
	return errNotAClass
}

// price is what changing class costs mem now: nothing for the first pick
// of a season, then more with their level and with each change already
// paid for. s.mu held.
func (m *Module) cost(mem *member, season int64) int64 {
	if mem.classSeason != season {
		return 0
	}
	l, _, _ := m.st.b.Level(mem.total)
	return m.st.b.ClassCost(l, mem.classChanges)
}

// price is what changing class costs user now, and what they hold.
func (m *Module) price(guild, user snowflake.ID) (cost, purse int64) {
	today := DayOf(m.now())
	season, _ := m.st.b.Season(today)
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := m.st.member(s, guild, user, today)
	return m.cost(mem, season), mem.purse
}

// pick makes user a key: the season's first pick free, every change after
// it paid for from their purse.
func (m *Module) pick(guild, user snowflake.ID, key string) error {
	if ClassOf(key).Key != key {
		return errNotAClass
	}
	today := DayOf(m.now())
	season, _ := m.st.b.Season(today)
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := m.st.member(s, guild, user, today)
	// Their class already, unless it's the wanderer they start as and
	// haven't picked yet: that first pick still counts.
	if mem.class == key && mem.classSeason != -1 {
		return nil
	}
	cost := m.cost(mem, season)
	if cost > mem.purse {
		return core.Tell(fmt.Sprintf("changing class now costs %s pp and you have %s; it gets cheaper when a new season starts",
			grouped(cost/unit), grouped(mem.purse/unit)))
	}
	if cost == 0 {
		mem.classSeason, mem.classChanges = season, 0
	} else {
		mem.purse -= cost
		mem.classChanges++
		m.st.burn(s, user, today, reasonClass, cost, nil)
	}
	mem.class, mem.dirty = key, true
	return nil
}

// classCard is the picker: every class, its emblem and the title it starts
// with, under what a change costs right now.
func classCard(cost, purse int64) discord.ContainerComponent {
	opts := make([]discord.StringSelectMenuOption, 0, len(Classes))
	for _, c := range Classes {
		o := discord.NewStringSelectMenuOption(c.Name(), c.Key)
		o.Description = "starts as a " + c.Titles[0]
		o.Emoji = brand.ComponentEmoji("pp_" + classIcons[c.Key])
		opts = append(opts, o)
	}
	price := "your first pick this season is free. after it a change costs pp, more the higher your level and more again each time"
	if cost > 0 {
		price = fmt.Sprintf("changing class now costs %s pp and you have %s. the first pick of a new season is free again", grouped(cost/unit), grouped(purse/unit))
	}
	return brand.Card(brand.ColorNotice, "pp", "class", "pick the path you walk\n-# "+price,
		discord.NewActionRow(discord.NewStringSelectMenu("pp:pick", "pick a class", opts...)))
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
	line := "-# " + dayLine(now)
	if mem.rung > 0 {
		line += fmt.Sprintf(" · prestige rung %d", mem.rung)
	}
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
	extra := []discord.ContainerSubComponent{discord.NewMediaGallery(discord.MediaGalleryItem{
		Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt,
	})}
	if buttons {
		class := discord.NewSecondaryButton("class", "pp:class")
		if e := brand.ComponentEmoji("pp_" + classIcons[sh.Class.Key]); e != nil {
			class = class.WithEmoji(*e)
		}
		extra = append(extra, discord.NewActionRow(class, discord.NewSecondaryButton("quests", "pp:quests"), discord.NewSecondaryButton("shop", "pp:shop"), discord.NewSecondaryButton("ledger", "pp:ledger"),
			discord.NewSecondaryButton("show the channel", "pp:show")))
	}
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "status", line, extra...)},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
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
		extra = append(extra, discord.NewActionRow(discord.NewPrimaryButton("pick a class", "pp:class")))
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "melting pot",
			"pick a class to begin\n-# /pp is in beta: the numbers may still move", extra...)},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// dayLine is how long until the day turns, which is when the pool fills
// again.
func dayLine(now time.Time) string {
	next := time.Unix((DayOf(now)+1)*86400, 0)
	return "the day turns in " + core.Duration(next.Sub(now)) + ", at 00:00 utc"
}

// sheet is mem's status screen. s.mu held.
func (m *Module) sheet(s *server, mem *member, guild, user snowflake.ID, today int64) Sheet {
	b := m.st.b
	l, into, need := b.Level(mem.total)
	vigor, phase := b.Vigor(mem.day, m.st.pools(guild, user, today))
	season, week := b.Season(today)
	sh := Sheet{
		Portrait: Portrait(ClassOf(mem.class)),
		Class:    ClassOf(mem.class), Level: l, Into: into, Need: need, Total: mem.total, Purse: mem.purse,
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
	if season > 0 && mem.seasonPP > 0 {
		sh.Place = 1
		for _, o := range s.members {
			if o.season == season && o.seasonPP > mem.seasonPP {
				sh.Place++
			}
		}
	}
	return sh
}
