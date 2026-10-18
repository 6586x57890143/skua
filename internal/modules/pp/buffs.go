package pp

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// The buffs card explains every buff, worn or not: what it adds, how a
// member comes by it, and whether they have it now. A buff that runs out
// says how long it has left; none do yet, and one that does sets Left.

// buffList is every buff as mem stands in s, crest, banner, plume and
// the hearth. s.mu held.
func (st *state) buffList(s *server, mem *member, vanity string) []BuffInfo {
	b := st.b
	status := "put this server's invite in your custom status"
	if vanity != "" {
		status = "put .gg/" + strings.ToLower(vanity) + " in your custom status"
	}
	hearth := BuffInfo{Icon: "hearth", Name: "hearth", Effect: fmt.Sprintf("+%d to %d%%", b.Hearth[0], b.Hearth[2]),
		How: "lit for all when the server's voice and chat beat its usual day", Off: "unlit"}
	if tier := s.hearth(); tier > 0 {
		hearth.Name, hearth.Effect, hearth.On = hearthWord(tier), fmt.Sprintf("+%d%%", b.Hearth[tier-1]), true
	}
	return []BuffInfo{
		{Icon: "crest", Name: "crest", Effect: fmt.Sprintf("+%d%%", b.Crest), How: "wear this server's tag as yours", On: mem.wear.Crest},
		{Icon: "banner", Name: "banner", Effect: fmt.Sprintf("+%d%%", b.Banner), How: status, On: mem.wear.Banner},
		{Icon: "plume", Name: "plume", Effect: fmt.Sprintf("+%d%%", b.Plume), How: "boost the server; it lasts while you boost", On: mem.wear.Plume},
		hearth,
	}
}

// hearthHint is how close the server is to lighting the hearth, or to its
// next tier, and what feeds it: the buffs card's strong hint, since the
// hearth is the one buff nobody wears alone. s.mu held.
func (st *state) hearthHint(s *server) string {
	b := st.b
	at := s.raw * 100 / max(b.Usual(s.history), 1)
	lit := s.hearth()
	if s.lit >= len(b.HearthAt) {
		return fmt.Sprintf("-# **the hearth:** %s, its brightest, burns today: +%d%% for everyone through tomorrow", hearthWord(len(b.Hearth)), b.Hearth[len(b.Hearth)-1])
	}
	next := s.lit // the tier today's earning reaches next, from 0
	line := fmt.Sprintf("-# **the hearth:** the server has earned %d%% of its usual day so far. at %d%% it lights %s, +%d%% for everyone for the rest of today and all of tomorrow",
		at, b.HearthAt[next], hearthWord(next+1), b.Hearth[next])
	if lit > 0 {
		line = fmt.Sprintf("-# **the hearth:** %s burns now. the server has earned %d%% of its usual day; at %d%% it rises to %s, +%d%%",
			hearthWord(lit), at, b.HearthAt[next], hearthWord(next+1), b.Hearth[next])
	}
	return line + "\n-# every minute in voice with company and every paying message feeds it, so bring people into voice: a full channel lights it fastest"
}

// buffsMessage is the buffs card for who: the screen and, under it, the
// rules every buff shares.
func (m *Module) buffsMessage(guild snowflake.ID, who *discord.ResolvedMember) (discord.MessageCreate, error) {
	vanity := ""
	if m.info != nil {
		_, vanity = m.info(guild)
	}
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	buffs := m.st.buffList(s, m.st.member(s, guild, who.User.ID, today), vanity)
	hint := m.st.hearthHint(s)
	s.mu.Unlock()

	pic, err := BuffScreen(pixelName(who), buffs)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	var worn []string
	for _, b := range buffs {
		if b.On {
			worn = append(worn, b.Name)
		}
	}
	alt := "your buffs: none worn"
	if len(worn) > 0 {
		alt = "your buffs: " + strings.Join(worn, ", ") + " worn"
	}
	note := "-# buffs add to what voice and chat pay you, never to quests, and they stack\n" + hint
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorInfo, "pp", "buffs", note,
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}
