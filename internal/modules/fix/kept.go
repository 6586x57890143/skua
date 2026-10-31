package fix

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// keptFor is how long a kept preview stays counted, in the database and on
// the report: long enough to line up with a bug report a few weeks late.
const keptFor = 30

// DB keeps the kept-preview counts across restarts. nil keeps them in memory.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// keptDay is one guild's kept previews on one day, by reason.
type keptDay struct {
	day    int64
	reason string
}

func today() int64 { return time.Now().UTC().Unix() / 86400 }

// Keep loads the last keptFor days of counts and writes new ones through db.
func (m *Module) Keep(ctx context.Context, db DB) error {
	if db == nil {
		return nil
	}
	if _, err := db.Exec(ctx, "delete from fix_kept where day < $1", today()-keptFor); err != nil {
		return err
	}
	rows, _ := db.Query(ctx, "select guild_id, day, reason, n from fix_kept")
	var guild, day, n int64
	var reason string
	_, err := pgx.ForEachRow(rows, []any{&guild, &day, &reason, &n}, func() error {
		m.statsMu.Lock()
		defer m.statsMu.Unlock()
		g := snowflake.ID(guild)
		if m.kept[g] == nil {
			m.kept[g] = map[keptDay]uint64{}
		}
		m.kept[g][keptDay{day, reason}] += uint64(max(0, n))
		return nil
	})
	if err != nil {
		return err
	}
	m.db = db
	return nil
}

// keptReason names why a write that would hide the source's preview failed.
func keptReason(denied bool, err error) string {
	var re *rest.Error
	switch {
	case denied:
		return "hourly cap"
	case errors.As(err, &re) && re.Response != nil && re.Response.StatusCode == http.StatusForbidden:
		return "no manage messages"
	default:
		return "discord error"
	}
}

// noteKept records a card posted beside a preview skua couldn't hide: one
// log line for whoever chases it later, and a count that outlives restarts.
func (m *Module) noteKept(key source, author snowflake.ID, reason string, err error) {
	m.log.Warn("fix: source preview kept", "guild", key.guild, "channel", key.channel, "message", key.message, "author", author, "reason", reason, "err", err)
	day := today()
	m.statsMu.Lock()
	if m.kept[key.guild] == nil {
		m.kept[key.guild] = map[keptDay]uint64{}
	}
	m.kept[key.guild][keptDay{day, reason}]++
	db := m.db
	m.statsMu.Unlock()
	if db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.Exec(ctx, "insert into fix_kept (guild_id, day, reason, n) values ($1, $2, $3, 1) on conflict (guild_id, day, reason) do update set n = fix_kept.n + 1", int64(key.guild), day, reason); err != nil {
		m.log.Warn("fix: kept preview not saved", "guild", key.guild, "err", err)
	}
}

// keptRows is the report's last keptFor days, one row per reason.
func (m *Module) keptRows(g snowflake.ID) [][2]string {
	since := today() - keptFor
	by := map[string]uint64{}
	m.statsMu.Lock()
	for k, n := range m.kept[g] {
		if k.day < since {
			delete(m.kept[g], k)
			continue
		}
		by[k.reason] += n
	}
	m.statsMu.Unlock()
	reasons := make([]string, 0, len(by))
	for r := range by {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	rows := make([][2]string, 0, len(reasons))
	for _, r := range reasons {
		rows = append(rows, [2]string{"previews kept, " + r, strconv.FormatUint(by[r], 10)})
	}
	return rows
}
