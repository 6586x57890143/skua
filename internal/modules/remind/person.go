package remind

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
)

// person is how a member wants their reminders, wherever they are.
type person struct {
	tz        string
	dmChannel snowflake.ID
	// deliver is where a reminder goes when they don't say: "dm", or
	// "here", the channel it was set in.
	deliver string
	// snooze is the fired card's three snooze buttons, in minutes;
	// tomorrow is the same time tomorrow.
	snooze []int
	// quietFrom and quietTo are quiet hours, minutes of the day in their
	// zone, -1 when off. A dm due in them waits until they end.
	quietFrom, quietTo int
}

const tomorrow = -1

func defaults() person {
	return person{deliver: "dm", snooze: []int{10, 60, tomorrow}, quietFrom: -1, quietTo: -1}
}

func personOf(ctx context.Context, db DB, user snowflake.ID) (person, error) {
	p := defaults()
	var dm int64
	var snooze []int32
	var from, to int16
	err := db.QueryRow(ctx, "select tz, dm_channel, deliver, snooze, quiet_from, quiet_to from remind_user where user_id = $1", int64(user)).
		Scan(&p.tz, &dm, &p.deliver, &snooze, &from, &to)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return defaults(), err
	}
	p.dmChannel, p.quietFrom, p.quietTo = snowflake.ID(dm), int(from), int(to)
	p.snooze = p.snooze[:0]
	for _, n := range snooze {
		p.snooze = append(p.snooze, int(n))
	}
	return p, nil
}

func saveSettings(ctx context.Context, db DB, user snowflake.ID, p person) error {
	snooze := make([]int32, len(p.snooze))
	for i, n := range p.snooze {
		snooze[i] = int32(n)
	}
	_, err := db.Exec(ctx, `insert into remind_user (user_id, tz, deliver, snooze, quiet_from, quiet_to) values ($1, $2, $3, $4, $5, $6)
		on conflict (user_id) do update set tz = $2, deliver = $3, snooze = $4, quiet_from = $5, quiet_to = $6`,
		int64(user), p.tz, p.deliver, snooze, int16(p.quietFrom), int16(p.quietTo))
	return err
}

func saveDM(ctx context.Context, db DB, user, ch snowflake.ID) error {
	_, err := db.Exec(ctx, "insert into remind_user (user_id, dm_channel) values ($1, $2) on conflict (user_id) do update set dm_channel = $2", int64(user), int64(ch))
	return err
}

// person is user's settings, read fresh: a row is one indexed read, and
// a cache would go stale while a deploy's two processes overlap.
func (m *Module) person(ctx context.Context, user snowflake.ID) (person, error) {
	return personOf(ctx, m.db, user)
}

// change applies f to user's settings and keeps them.
func (m *Module) change(ctx context.Context, user snowflake.ID, f func(*person)) (person, error) {
	p, err := m.person(ctx, user)
	if err != nil {
		return p, err
	}
	f(&p)
	return p, saveSettings(ctx, m.db, user, p)
}

// quiet is whether t falls in p's quiet hours.
func (p person) quiet(t time.Time) bool {
	if p.quietFrom < 0 || p.quietTo < 0 || p.quietFrom == p.quietTo {
		return false
	}
	l := t.In(zone(p.tz))
	mm := l.Hour()*60 + l.Minute()
	if p.quietFrom < p.quietTo {
		return mm >= p.quietFrom && mm < p.quietTo
	}
	return mm >= p.quietFrom || mm < p.quietTo
}

// quietEnd is when the quiet hours t falls in are over.
func (p person) quietEnd(t time.Time) time.Time {
	loc := zone(p.tz)
	l := t.In(loc)
	end := time.Date(l.Year(), l.Month(), l.Day(), p.quietTo/60, p.quietTo%60, 0, 0, loc)
	if !end.After(t) {
		end = time.Date(l.Year(), l.Month(), l.Day()+1, p.quietTo/60, p.quietTo%60, 0, 0, loc)
	}
	return end
}

// quietWords is p's quiet hours as the settings card says them.
func (p person) quietWords() string {
	if p.quietFrom < 0 || p.quietTo < 0 || p.quietFrom == p.quietTo {
		return "off"
	}
	return clockOf(p.quietFrom) + " → " + clockOf(p.quietTo)
}

func clockOf(min int) string { return fmt.Sprintf("%02d:%02d", min/60, min%60) }

// snoozeWord is a snooze button's label: "10m", "1h", "tomorrow".
func snoozeWord(min int) string {
	switch {
	case min == tomorrow:
		return "tomorrow"
	case min < 60:
		return strconv.Itoa(min) + "m"
	case min%60 == 0:
		return strconv.Itoa(min/60) + "h"
	}
	return fmt.Sprintf("%dh %dm", min/60, min%60)
}

// snoozes are the lengths a member can put on their snooze buttons.
var snoozes = []int{5, 10, 15, 30, 60, 120, 180, 480, tomorrow}

// quiets are the quiet hours a member can pick, off first.
var quiets = [][2]int{{-1, -1}, {21 * 60, 7 * 60}, {22 * 60, 7 * 60}, {22 * 60, 8 * 60}, {23 * 60, 7 * 60}, {23 * 60, 8 * 60}, {0, 8 * 60}}
