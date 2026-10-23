package pp

import (
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

func TestLeavingOrUnavailableGuildStopsVoiceEarning(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		m := testModule(nil)
		s := m.st.server(guild)
		a, b := old(1), old(2)
		m.st.sit(s, guild, a, in(room), false, t0)
		m.st.sit(s, guild, b, in(room), false, t0)
		m.minute()
		before := s.members[a].total
		ge := &events.GenericGuild{GenericEvent: generic(), GuildID: guild}
		var event bot.Event = &events.GuildLeave{GenericGuild: ge}
		if unavailable {
			event = &events.GuildUnavailable{GenericGuild: ge}
		}
		m.OnEvent(event)
		m.now = func() time.Time { return t0.Add(time.Minute) }
		m.minute()
		if len(s.seats) != 0 || s.members[a].total != before {
			t.Fatal("departed guild kept earning voice points")
		}
		// A returning guild supplies a fresh snapshot, not the stale seats.
		vs := in(room)
		vs.GuildID, vs.UserID = guild, a
		snapshot := discord.GatewayGuild{VoiceStates: []discord.VoiceState{vs}, Members: []discord.Member{{User: discord.User{ID: a}}}}
		if unavailable {
			m.OnEvent(&events.GuildAvailable{GenericGuild: ge, Guild: snapshot})
		} else {
			m.OnEvent(&events.GuildJoin{GenericGuild: ge, Guild: snapshot})
		}
		if len(s.seats) != 1 || s.seats[a] == nil || s.seats[b] != nil {
			t.Fatal("returning guild did not restore fresh voice snapshot")
		}
		m.minute()
		if s.members[a].total != before {
			t.Fatal("returning member earned alone using stale company")
		}
	}
}
