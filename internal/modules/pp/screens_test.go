package pp

import (
	"context"
	"regexp"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// Every screen the menu opens is a message Discord takes and a phone lays
// out the same each time: no two of its components share a custom ID (the
// board's pages once did, and Discord refused every edit), no row holds
// more than Discord's five, and a row of the screen's own holds four at
// most, in either view of the menu.
func TestScreensDiscordTakes(t *testing.T) {
	m := launched(testModule(&fakeDB{}))
	s := m.st.server(guild)
	for i := range 30 {
		o := m.st.member(s, guild, old(i+1), DayOf(t0))
		o.total, o.season, o.seasonPP, o.classSeason, o.class = int64(1000-i)*1000*unit, 1, int64(i+1)*unit, 1, Classes[i%len(Classes)].Key
	}
	user := snowflake.ID(5)
	mem := m.st.member(s, guild, user, DayOf(t0))
	mem.classSeason, mem.class, mem.purse, mem.total = 1, "knight", 5000*unit, 50000*unit
	mem.quests = []heldQuest{{tier: 2, kind: 0}, {tier: 0, kind: 1}}
	ids := regexp.MustCompile(`"custom_id":"([^"]+)"`)
	screens := []string{"pp:status", "pp:quests", "pp:notices", "pp:board", "pp:board:levels:1", "pp:shop", "pp:ladder", "pp:yard", "pp:class", "pp:buffs", "pp:tide", "pp:gate"}
	for _, compact := range []bool{false, true} {
		mem.compact = compact
		for _, id := range screens {
			e, sent := coretest.Button(t, id, nil)
			if err := m.press(context.Background(), e); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			card := js((*sent)[0].Components)
			seen := map[string]bool{}
			for _, c := range ids.FindAllStringSubmatch(card, -1) {
				if seen[c[1]] {
					t.Errorf("%s: %s twice", id, c[1])
				}
				seen[c[1]] = true
			}
			box, ok := (*sent)[0].Components[0].(discord.ContainerComponent)
			if !ok {
				t.Fatalf("%s: no card", id)
			}
			menu := menuRows(mem.class, true, "", true, false, false, compact, false)
			own := len(box.Components) - len(menu)
			for i, c := range box.Components {
				row, ok := c.(discord.ActionRowComponent)
				if !ok {
					continue
				}
				if n := len(row.Components); n > 5 || i < own && n > 4 {
					t.Errorf("%s: row %d holds %d", id, i, n)
				}
			}
		}
	}
}
