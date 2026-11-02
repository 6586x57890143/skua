package remind

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/6586x57890143/skua/internal/core"
)

// rule is when a reminder comes back: on the weekdays in mask (bit d is
// time.Weekday d), or on day mday of every month, at minute at of the
// day in the reminder's own zone. The zero rule is a reminder that fires
// once.
type rule struct {
	mask uint8
	mday int
	at   int
}

const (
	everyDay  uint8 = 0x7f
	weekdays  uint8 = 0x3e
	weekends  uint8 = 0x41
	morning         = 9 * 60
	horizonYr       = 366 * 24 * time.Hour
)

func (r rule) once() bool { return r.mask == 0 && r.mday == 0 }

// next is the rule's first occurrence after t. Each candidate is built from
// the wall clock in loc, so a daily 09:00 stays 09:00 across a DST change;
// a wall time a change skips lands where time.Date normalises it.
func (r rule) next(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	if r.mday > 0 {
		for i := range 14 {
			mo := l.Month() + time.Month(i)
			c := time.Date(l.Year(), mo, min(r.mday, daysIn(l.Year(), mo)), r.at/60, r.at%60, 0, 0, loc)
			if c.After(t) {
				return c
			}
		}
		return time.Time{}
	}
	for i := range 9 {
		c := time.Date(l.Year(), l.Month(), l.Day()+i, r.at/60, r.at%60, 0, 0, loc)
		if r.mask&(1<<c.Weekday()) != 0 && c.After(t) {
			return c
		}
	}
	return time.Time{}
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// String is the rule as a member reads it: "weekdays at 09:00".
func (r rule) String() string {
	at := fmt.Sprintf("%02d:%02d", r.at/60, r.at%60)
	switch {
	case r.once():
		return "never"
	case r.mday > 0:
		return "the " + ordinal(r.mday) + " of each month at " + at
	case r.mask == everyDay:
		return "every day at " + at
	case r.mask == weekdays:
		return "weekdays at " + at
	case r.mask == weekends:
		return "weekends at " + at
	}
	var days []string
	for d := range 7 {
		if r.mask&(1<<((d+1)%7)) != 0 { // monday first
			days = append(days, dayNames[(d+1)%7][:3])
		}
	}
	return "every " + strings.Join(days, ", ") + " at " + at
}

// value is the rule as an autocomplete choice carries it back.
func (r rule) value() string { return fmt.Sprintf("r:%d:%d:%d", r.mask, r.mday, r.at) }

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return strconv.Itoa(n) + suffix
}

var (
	dayNames   = [7]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	monthNames = [12]string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
	units      = map[string]time.Duration{
		"s": time.Second, "sec": time.Second, "secs": time.Second, "second": time.Second, "seconds": time.Second,
		"m": time.Minute, "min": time.Minute, "mins": time.Minute, "minute": time.Minute, "minutes": time.Minute,
		"h": time.Hour, "hr": time.Hour, "hrs": time.Hour, "hour": time.Hour, "hours": time.Hour,
		"d": 24 * time.Hour, "day": 24 * time.Hour, "days": 24 * time.Hour,
		"w": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
	}
	span    = regexp.MustCompile(`(\d+|an?)\s*([a-z]+)`)
	clock   = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?(am|pm|a|p)?$`)
	isoDate = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	nth     = regexp.MustCompile(`^(\d{1,2})(st|nd|rd|th)?$`)
	named   = map[string]int{"noon": 12 * 60, "midnight": 0, "morning": morning, "afternoon": 15 * 60, "evening": 18 * 60, "tonight": 20 * 60, "night": 20 * 60}
)

// reading is one way to take what a member typed: the first time it
// fires, and the rule it comes back by.
type reading struct {
	at time.Time
	r  rule
}

// value is the reading as an autocomplete choice carries it back, so the
// command fires at exactly the time the member picked.
func (rd reading) value() string {
	if rd.r.once() {
		return "t:" + strconv.FormatInt(rd.at.Unix(), 10)
	}
	return rd.r.value()
}

// label is the reading as the autocomplete list shows it, in loc:
// "fri 7 nov 17:00 cet · in 4d 2h".
func (rd reading) label(now time.Time, loc *time.Location) string {
	l := rd.at.In(loc)
	zone := strings.ToLower(l.Format("MST"))
	if strings.HasPrefix(zone, "+") || strings.HasPrefix(zone, "-") {
		zone = "utc" + zone
	}
	when := strings.ToLower(l.Format("Mon 2 Jan 15:04")) + " " + zone
	if l.Year() != now.In(loc).Year() {
		when = strings.ToLower(l.Format("Mon 2 Jan 2006 15:04")) + " " + zone
	}
	in := "in " + inWords(rd.at.Sub(now))
	if !rd.r.once() {
		return rd.r.String() + " " + zone + " · first " + in
	}
	return when + " · " + in
}

// inWords is how far off something is, never "0s".
func inWords(d time.Duration) string {
	if d < time.Second {
		return "a moment"
	}
	return core.Duration(d)
}

// read is every sensible way to take s as a time after now in loc,
// likeliest first, at most five. It reads the choice values autocomplete hands back,
// spans ("in 20m", "1h30m"), clock times ("5pm", "17:30", "noon"), days
// ("tomorrow", "fri", "7 nov", "2026-11-07") in either order around a time,
// and repeats ("every weekday at 9", "every mon, wed 18:00", "every month
// on the 1st"). A word it doesn't know reads as nothing, so a guess is
// never offered as an answer. A bare hour under 13 is offered both ways.
func read(s string, now time.Time, loc *time.Location) []reading {
	s = strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(s, ",", " "))), " ")
	if s == "" {
		return nil
	}
	if rd, ok := canonical(s, now, loc); ok {
		return []reading{rd}
	}
	if d, ok := spanOf(s); ok {
		if d <= 0 || d > horizonYr {
			return nil
		}
		return []reading{{at: now.Add(d).Truncate(time.Second)}}
	}
	every := false
	words := strings.Fields(s)
	switch words[0] {
	case "every", "each":
		every, words = true, words[1:]
	case "daily":
		every, words[0] = true, "day"
	case "weekly":
		every, words[0] = true, "week"
	case "monthly":
		every, words[0] = true, "month"
	}
	p, ok := parts(words, every)
	if !ok {
		return nil
	}
	if every {
		return repeats(p, now, loc)
	}
	return onces(p, now, loc)
}

// canonical reads a value autocomplete handed back.
func canonical(s string, now time.Time, loc *time.Location) (reading, bool) {
	switch {
	case strings.HasPrefix(s, "t:"):
		n, err := strconv.ParseInt(s[2:], 10, 64)
		if err != nil {
			return reading{}, false
		}
		return reading{at: time.Unix(n, 0)}, true
	case strings.HasPrefix(s, "r:"):
		f := strings.Split(s[2:], ":")
		if len(f) != 3 {
			return reading{}, false
		}
		mask, e1 := strconv.Atoi(f[0])
		mday, e2 := strconv.Atoi(f[1])
		at, e3 := strconv.Atoi(f[2])
		if e1 != nil || e2 != nil || e3 != nil || mask < 0 || mask > int(everyDay) || mday < 0 || mday > 31 || at < 0 || at >= 24*60 || (mask == 0) == (mday == 0) {
			return reading{}, false
		}
		r := rule{mask: uint8(mask), mday: mday, at: at}
		return reading{at: r.next(now, loc), r: r}, true
	}
	return reading{}, false
}

// spanOf reads s as a stretch of time from now: "in 20m", "1h 30m", "an
// hour".
func spanOf(s string) (time.Duration, bool) {
	s = strings.TrimPrefix(s, "in ")
	m := span.FindAllStringSubmatchIndex(s, -1)
	if len(m) == 0 {
		return 0, false
	}
	var total time.Duration
	end := 0
	for _, g := range m {
		if strings.TrimSpace(s[end:g[0]]) != "" {
			return 0, false
		}
		end = g[1]
		unit, ok := units[s[g[4]:g[5]]]
		if !ok {
			return 0, false
		}
		n := 1
		if v := s[g[2]:g[3]]; v != "a" && v != "an" {
			var err error
			if n, err = strconv.Atoi(v); err != nil || n > 100000 {
				return 0, false
			}
		}
		if time.Duration(n) > horizonYr/unit {
			return 0, false
		}
		if total += time.Duration(n) * unit; total > horizonYr {
			return 0, false
		}
	}
	if strings.TrimSpace(s[end:]) != "" {
		return 0, false
	}
	return total, true
}

// parsed is what the words of a time said.
type parsed struct {
	times []int // minutes of the day it could mean, likeliest first
	mask  uint8 // weekdays named
	date  *time.Time
	day   int // days from today (today, tomorrow), or -1
	month bool
	mday  int
	week  bool
}

// parts sorts words into a time, days and a date. Any word it can't place
// fails the lot.
func parts(words []string, every bool) (parsed, bool) {
	p := parsed{day: -1}
	monthAt := -1
	var bare []int // numbers that weren't hours or dates yet
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch w {
		case "at", "on", "the", "of", "and", "next", "this":
			continue
		case "today":
			p.day = 0
			continue
		case "tomorrow", "tmr", "tmrw", "tom":
			p.day = 1
			continue
		case "day", "days":
			if !every {
				return p, false
			}
			p.mask |= everyDay
			continue
		case "weekday", "weekdays":
			p.mask |= weekdays
			continue
		case "weekend", "weekends":
			p.mask |= weekends
			continue
		case "week":
			p.week = true
			continue
		case "month":
			p.month = true
			continue
		case "am", "pm", "a.m.", "p.m.":
			return p, false // only right after a number, handled below
		}
		if t, ok := named[w]; ok {
			p.times = append(p.times, t)
			if w == "tonight" && p.day < 0 && !every {
				p.day = 0
			}
			continue
		}
		if d, ok := dayOf(w); ok {
			p.mask |= 1 << d
			continue
		}
		if mo, ok := monthOf(w); ok {
			if monthAt >= 0 {
				return p, false
			}
			monthAt = int(mo)
			continue
		}
		if g := isoDate.FindStringSubmatch(w); g != nil {
			y, _ := strconv.Atoi(g[1])
			mo, _ := strconv.Atoi(g[2])
			d, _ := strconv.Atoi(g[3])
			if mo < 1 || mo > 12 || d < 1 || d > daysIn(y, time.Month(mo)) {
				return p, false
			}
			t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
			p.date = &t
			continue
		}
		// "5 pm" is one time in two words.
		if i+1 < len(words) && (words[i+1] == "am" || words[i+1] == "pm") {
			w += words[i+1]
			i++
		}
		if g := clock.FindStringSubmatch(w); g != nil && (g[2] != "" || g[3] != "") {
			ts, ok := hours(g)
			if !ok {
				return p, false
			}
			p.times = append(p.times, ts...)
			continue
		}
		if g := nth.FindStringSubmatch(w); g != nil {
			n, _ := strconv.Atoi(g[1])
			if g[2] != "" { // "7th" is always a day of the month
				if n < 1 || n > 31 || p.mday != 0 {
					return p, false
				}
				p.mday = n
				continue
			}
			bare = append(bare, n)
			continue
		}
		return p, false
	}
	// A bare number beside a month is its day; any other is an hour.
	for _, n := range bare {
		if (monthAt >= 0 || p.month) && p.mday == 0 && n >= 1 && n <= 31 {
			p.mday = n
			continue
		}
		ts, ok := hours([]string{"", strconv.Itoa(n), "", ""})
		if !ok {
			return p, false
		}
		p.times = append(p.times, ts...)
	}
	if monthAt >= 0 {
		if p.mday == 0 || p.date != nil || every {
			return p, false
		}
		if p.mday > daysIn(2000, time.Month(monthAt+1)) {
			return p, false // 31 apr
		}
		t := time.Date(0, time.Month(monthAt+1), p.mday, 0, 0, 0, 0, time.UTC)
		p.date, p.mday = &t, 0
	}
	if len(p.times) > 2 {
		return p, false
	}
	return p, p.date != nil || p.day >= 0 || p.mask != 0 || p.mday != 0 || p.month || p.week || len(p.times) > 0
}

// hours is the minutes of the day a clock match could mean, likeliest
// first: "17:30" one, "5:30" both the evening and the morning, "5pm" one.
func hours(g []string) ([]int, bool) {
	h, _ := strconv.Atoi(g[1])
	m := 0
	if g[2] != "" {
		m, _ = strconv.Atoi(g[2])
	}
	if m > 59 {
		return nil, false
	}
	switch g[3] {
	case "am", "a":
		if h < 1 || h > 12 {
			return nil, false
		}
		return []int{(h%12)*60 + m}, true
	case "pm", "p":
		if h < 1 || h > 12 {
			return nil, false
		}
		return []int{(h%12+12)*60 + m}, true
	}
	switch {
	case h > 23:
		return nil, false
	case h == 0 || h >= 13 || len(g[1]) == 2 && g[1][0] == '0':
		return []int{h*60 + m}, true
	case h == 12:
		return []int{12*60 + m}, true
	}
	// 7 to 11 is likelier the morning, 1 to 6 the afternoon.
	if h >= 7 {
		return []int{h*60 + m, (h+12)*60 + m}, true
	}
	return []int{(h+12)*60 + m, h*60 + m}, true
}

func dayOf(w string) (time.Weekday, bool) {
	w = strings.TrimSuffix(w, "s") // "fridays"
	if len(w) < 3 {
		return 0, false
	}
	for d, name := range dayNames {
		if strings.HasPrefix(name, w) || w == "thurs" && d == 4 || w == "tues" && d == 2 {
			return time.Weekday(d), true
		}
	}
	return 0, false
}

func monthOf(w string) (time.Month, bool) {
	if len(w) < 3 {
		return 0, false
	}
	for m, name := range monthNames {
		if strings.HasPrefix(name, w) || w == "sept" && m == 8 {
			return time.Month(m), true
		}
	}
	return 0, false
}

// onces are the one-off readings of p: each time it could mean, on the
// day it names or the next day that time comes round.
func onces(p parsed, now time.Time, loc *time.Location) []reading {
	if p.month || p.week || p.mday != 0 || bits(p.mask) > 1 {
		return nil
	}
	times := p.times
	if len(times) == 0 {
		times = []int{morning}
	}
	l := now.In(loc)
	var out []reading
	for _, at := range times {
		var t time.Time
		day := func(y int, m time.Month, d int) time.Time {
			if d > daysIn(y, m) {
				return time.Time{} // 31 apr, or 29 feb outside a leap year
			}
			return time.Date(y, m, d, at/60, at%60, 0, 0, loc)
		}
		switch {
		case p.date != nil && p.date.Year() == 0:
			t = day(l.Year(), p.date.Month(), p.date.Day())
			if !t.After(now) {
				t = day(l.Year()+1, p.date.Month(), p.date.Day())
			}
			for y := l.Year() + 2; t.IsZero() && y <= l.Year()+4; y++ {
				t = day(y, p.date.Month(), p.date.Day()) // 29 feb, the next leap year
			}
		case p.date != nil:
			t = day(p.date.Year(), p.date.Month(), p.date.Day())
		case p.mask != 0:
			t = rule{mask: p.mask, at: at}.next(now, loc)
		case p.day >= 0:
			t = day(l.Year(), l.Month(), l.Day()+p.day)
		default:
			t = rule{mask: everyDay, at: at}.next(now, loc)
		}
		if t.After(now) && t.Sub(now) <= horizonYr {
			out = append(out, reading{at: t})
		}
	}
	return tidy(out)
}

// repeats are the readings of an "every" p.
func repeats(p parsed, now time.Time, loc *time.Location) []reading {
	if p.date != nil || p.day >= 0 {
		return nil
	}
	times := p.times
	if len(times) == 0 {
		times = []int{morning}
	}
	var out []reading
	for _, at := range times {
		r := rule{at: at}
		switch {
		case p.month || p.mday != 0:
			if p.mask != 0 || p.week {
				return nil
			}
			r.mday = p.mday
			if r.mday == 0 {
				r.mday = now.In(loc).Day()
			}
		case p.mask != 0:
			r.mask = p.mask
		case p.week:
			r.mask = 1 << now.In(loc).Weekday()
		default:
			return nil
		}
		out = append(out, reading{at: r.next(now, loc), r: r})
	}
	return tidy(out)
}

// tidy drops repeated readings and keeps five, likeliest first.
func tidy(rs []reading) []reading {
	var out []reading
	for _, r := range rs {
		if !slices.ContainsFunc(out, func(o reading) bool { return o.at.Equal(r.at) && o.r == r.r }) {
			out = append(out, r)
		}
	}
	return out[:min(len(out), 5)]
}

func bits(m uint8) int {
	n := 0
	for ; m != 0; m &= m - 1 {
		n++
	}
	return n
}
