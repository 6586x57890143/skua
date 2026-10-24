package fix

import (
	"errors"
	"net/http"
	"time"

	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// A 403 counts toward Discord's invalid request limit per IP, shared by every
// bot on the host, and the guard's breaker only opens on 429s and 5xx. After
// one refusal skua stops asking in that channel for a while.
const refusalFor = 10 * time.Minute

// refusal is a channel and what Discord refused there: cards, or hiding the
// source's preview (manage messages).
type refusal struct {
	channel snowflake.ID
	flags   bool
}

func (m *Module) refused(k refusal) bool {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	return time.Now().Before(m.refusals[k])
}

func (m *Module) noteRefusal(k refusal, err error) {
	var re *rest.Error
	if !errors.As(err, &re) || re.Response == nil || re.Response.StatusCode != http.StatusForbidden {
		return
	}
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	now := time.Now()
	if len(m.refusals) >= 1024 {
		for c, until := range m.refusals {
			if now.After(until) {
				delete(m.refusals, c)
			}
		}
	}
	if len(m.refusals) < 1024 {
		m.refusals[k] = now.Add(refusalFor)
	}
}
