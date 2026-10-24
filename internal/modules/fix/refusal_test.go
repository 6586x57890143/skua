package fix

import (
	"errors"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// After a 403 in a channel, skua stops asking there instead of spending the
// host's invalid request budget on every later link.
func TestRefusedChannelIsNotAskedAgain(t *testing.T) {
	m := module()
	r := &recordingRest{}
	r.setError(discordError(403))
	m.queue(r, source{3, 4, 10}, "https://x.com/user/status/123", false)
	idle(t, m)
	m.queue(r, source{3, 4, 11}, "https://x.com/user/status/124", false)
	idle(t, m)
	if c, _, _ := r.totals(); c != 1 {
		t.Fatalf("%d creates after a 403", c)
	}
	m.queue(r, source{3, 5, 12}, "https://x.com/user/status/125", false)
	idle(t, m)
	if c, _, _ := r.totals(); c != 2 {
		t.Fatal("another channel was held back")
	}
}

func TestRefusalsAreBounded(t *testing.T) {
	m := module()
	m.noteRefusal(refusal{channel: 1}, errors.New("offline"))
	m.noteRefusal(refusal{channel: 2}, discordError(500))
	if len(m.refusals) != 0 {
		t.Fatal("only a 403 is remembered")
	}
	for i := range 1024 {
		m.refusals[refusal{channel: snowflake.ID(100 + i)}] = time.Now().Add(-time.Second)
	}
	m.noteRefusal(refusal{channel: 7, flags: true}, discordError(403))
	if len(m.refusals) != 1 || !m.refused(refusal{channel: 7, flags: true}) || m.refused(refusal{channel: 7}) {
		t.Fatal("expired refusals not pruned", len(m.refusals))
	}
}
