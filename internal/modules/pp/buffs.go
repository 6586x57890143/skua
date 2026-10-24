package pp

import (
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
	b := st.bal()
	status := "put this server's invite in your custom status"
	if vanity != "" {
		status = "put .gg/" + strings.ToLower(vanity) + " in your custom status"
	}
	hearth := BuffInfo{Icon: "hearth", Name: "hearth", Effect: fmt.Sprintf("+%d to %d%%", b.Hearth[0], b.Hearth[2]),
		How: st.hearthHow(s), Off: "unlit"}
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

// hearthHow is the hearth's line on the buffs screen: how close the
// server is to lighting it, or to its next tier. It's the one buff nobody
// wears alone, so the line says how far along everyone is. s.mu held.
func (st *state) hearthHow(s *server) string {
	b := st.bal()
	if s.lit >= len(b.HearthAt) {
		return "at its brightest, through tomorrow"
	}
	at := s.raw * 100 / max(b.Usual(s.history), 1)
	// Today's next tier lights at HearthAt[s.lit]. Under a fire still
	// burning from yesterday, reaching it keeps a fire for tomorrow; over
	// it, the hearth rises.
	verb := "lights"
	switch next := s.lit + 1; {
	case s.hearth() >= next:
		verb = "burns on tomorrow"
	case s.hearth() > 0:
		verb = "rises"
	}
	return fmt.Sprintf("the server is at %d%% of its day, %s at %d%%", at, verb, b.HearthAt[s.lit])
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
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorInfo, "pp", "buffs", "",
			discord.NewMediaGallery(screenItem(pic, alt)))},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}
