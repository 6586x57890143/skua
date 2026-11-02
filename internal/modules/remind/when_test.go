package remind

import (
	"strings"
	"testing"
	"time"
)

var berlin = zone("Europe/Berlin")

// monday 2 nov 2026, 10:00 in berlin
var now = time.Date(2026, 11, 2, 10, 0, 0, 0, berlin)

func at(mo time.Month, d, h, mi int) time.Time { return time.Date(2026, mo, d, h, mi, 0, 0, berlin) }

func TestRead(t *testing.T) {
	cases := []struct {
		in   string
		want []time.Time
		r    rule
	}{
		{"in 20m", []time.Time{now.Add(20 * time.Minute)}, rule{}},
		{"1h30m", []time.Time{now.Add(90 * time.Minute)}, rule{}},
		{"in an hour", []time.Time{now.Add(time.Hour)}, rule{}},
		{"2 days", []time.Time{now.Add(48 * time.Hour)}, rule{}},
		{"5pm", []time.Time{at(11, 2, 17, 0)}, rule{}},
		{"5 pm", []time.Time{at(11, 2, 17, 0)}, rule{}},
		{"9am", []time.Time{at(11, 3, 9, 0)}, rule{}},
		{"12am", []time.Time{at(11, 3, 0, 0)}, rule{}},
		{"17:30", []time.Time{at(11, 2, 17, 30)}, rule{}},
		{"05:30", []time.Time{at(11, 3, 5, 30)}, rule{}},
		{"5", []time.Time{at(11, 2, 17, 0), at(11, 3, 5, 0)}, rule{}},
		{"12", []time.Time{at(11, 2, 12, 0)}, rule{}},
		{"fri 17:30", []time.Time{at(11, 6, 17, 30)}, rule{}},
		{"Friday, 5:30pm", []time.Time{at(11, 6, 17, 30)}, rule{}},
		{"next thurs", []time.Time{at(11, 5, 9, 0)}, rule{}},
		{"tomorrow 9am", []time.Time{at(11, 3, 9, 0)}, rule{}},
		{"tomorrow night", []time.Time{at(11, 3, 20, 0)}, rule{}},
		{"tonight", []time.Time{at(11, 2, 20, 0)}, rule{}},
		{"noon", []time.Time{at(11, 2, 12, 0)}, rule{}},
		{"midnight", []time.Time{at(11, 3, 0, 0)}, rule{}},
		{"today at 3pm", []time.Time{at(11, 2, 15, 0)}, rule{}},
		{"7 nov", []time.Time{at(11, 7, 9, 0)}, rule{}},
		{"nov 7 at 5pm", []time.Time{at(11, 7, 17, 0)}, rule{}},
		{"7th of november 18:00", []time.Time{at(11, 7, 18, 0)}, rule{}},
		{"1 nov", []time.Time{time.Date(2027, 11, 1, 9, 0, 0, 0, berlin)}, rule{}},
		{"2026-12-24 18:00", []time.Time{at(12, 24, 18, 0)}, rule{}},
		{"every weekday at 9", []time.Time{at(11, 3, 9, 0), at(11, 2, 21, 0)}, rule{mask: weekdays, at: 9 * 60}},
		{"8", []time.Time{at(11, 3, 8, 0), at(11, 2, 20, 0)}, rule{}},
		{"every mon, wed 18:00", []time.Time{at(11, 2, 18, 0)}, rule{mask: 1<<1 | 1<<3, at: 18 * 60}},
		{"every day at 07:15", []time.Time{at(11, 3, 7, 15)}, rule{mask: everyDay, at: 7*60 + 15}},
		{"daily 19:00", []time.Time{at(11, 2, 19, 0)}, rule{mask: everyDay, at: 19 * 60}},
		{"weekly", []time.Time{at(11, 9, 9, 0)}, rule{mask: 1 << 1, at: 9 * 60}},
		{"every week on fri", []time.Time{at(11, 6, 9, 0)}, rule{mask: 1 << 5, at: 9 * 60}},
		{"every weekend noon", []time.Time{at(11, 7, 12, 0)}, rule{mask: weekends, at: 12 * 60}},
		{"every month on the 31st", []time.Time{at(11, 30, 9, 0)}, rule{mday: 31, at: 9 * 60}},
		{"monthly", []time.Time{at(12, 2, 9, 0)}, rule{mday: 2, at: 9 * 60}},
		{"every 1st at 8:00pm", []time.Time{at(12, 1, 20, 0)}, rule{mday: 1, at: 20 * 60}},
		{"t:1800000000", []time.Time{time.Unix(1800000000, 0)}, rule{}},
		{"r:62:0:540", []time.Time{at(11, 3, 9, 0)}, rule{mask: weekdays, at: 540}},
	}
	for _, c := range cases {
		got := read(c.in, now, berlin)
		if len(got) != len(c.want) {
			t.Errorf("%q: %d readings %v, want %d", c.in, len(got), got, len(c.want))
			continue
		}
		for i := range got {
			if !got[i].at.Equal(c.want[i]) {
				t.Errorf("%q [%d]: %v, want %v", c.in, i, got[i].at.In(berlin), c.want[i])
			}
		}
		if got[0].r != c.r {
			t.Errorf("%q: rule %+v, want %+v", c.in, got[0].r, c.r)
		}
	}
}

func TestReadRefuses(t *testing.T) {
	for _, in := range []string{"", "banana", "5 banana", "every", "in 0m", "25:00", "13pm", "5:75", "fri sat 5pm", "2026-02-30",
		"every tomorrow", "every 7 nov", "nov dec 5", "7th 8th", "day", "pm", "in 500 weeks", "in 999999h", "5 6 7",
		"every month fri", "31 apr", "feb 30 5pm", "29 feb", "in 30501w", "in 9000h 9000h", "r:0:0:540", "r:x", "r:1:2", "t:x", "every week the 3rd", "monday the 3rd", "nov"} {
		if got := read(in, now, berlin); len(got) != 0 {
			t.Errorf("%q read as %v", in, got)
		}
	}
}

func TestRuleNextAcrossDST(t *testing.T) {
	r := rule{mask: everyDay, at: 9 * 60}
	// berlin springs forward on 29 mar 2026: 09:00 is still 09:00 local
	got := r.next(time.Date(2026, 3, 28, 10, 0, 0, 0, berlin), berlin)
	if got.In(berlin).Hour() != 9 || got.UTC().Hour() != 7 {
		t.Fatalf("after spring forward: %v", got)
	}
	ny := zone("America/New_York")
	got = r.next(time.Date(2026, 10, 31, 10, 0, 0, 0, ny), ny)
	if !got.Equal(time.Date(2026, 11, 1, 9, 0, 0, 0, ny)) || got.UTC().Hour() != 14 {
		t.Fatalf("after fall back: %v", got.UTC())
	}
	// a monthly 31st lands on the month's last day
	m := rule{mday: 31, at: 0}
	if got := m.next(time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC), time.UTC); got.Day() != 28 {
		t.Fatalf("feb: %v", got)
	}
}

func TestRuleWords(t *testing.T) {
	for r, want := range map[rule]string{
		{}:                         "never",
		{mask: everyDay, at: 90}:   "every day at 01:30",
		{mask: weekdays, at: 540}:  "weekdays at 09:00",
		{mask: weekends, at: 540}:  "weekends at 09:00",
		{mask: 1<<0 | 1<<1, at: 0}: "every mon, sun at 00:00",
		{mday: 11, at: 0}:          "the 11th of each month at 00:00",
		{mday: 22, at: 0}:          "the 22nd of each month at 00:00",
		{mday: 3, at: 0}:           "the 3rd of each month at 00:00",
		{mday: 1, at: 0}:           "the 1st of each month at 00:00",
	} {
		if got := r.String(); got != want {
			t.Errorf("%+v: %q, want %q", r, got, want)
		}
	}
}

// What the form fills in reads back as the same reminder.
func TestTypedReadsBack(t *testing.T) {
	for _, r := range []rule{{}, {mask: everyDay, at: 450}, {mask: weekdays, at: 540}, {mask: weekends, at: 600}, {mask: 1<<2 | 1<<4, at: 1080}, {mday: 15, at: 480}} {
		rem := reminder{due: at(11, 20, 7, 30), r: r, tz: "Europe/Berlin"}
		got := read(typed(rem), now, berlin)
		if len(got) == 0 || got[0].r != r || r.once() && !got[0].at.Equal(rem.due) {
			t.Errorf("%+v typed as %q read back as %+v", r, typed(rem), got)
		}
	}
}

func TestLabel(t *testing.T) {
	rd := reading{at: at(11, 6, 17, 0)}
	if got := rd.label(now, berlin); got != "fri 6 nov 17:00 cet · in 4d 7h" {
		t.Fatalf("label %q", got)
	}
	rd = reading{at: time.Date(2027, 1, 4, 9, 0, 0, 0, berlin)}
	if got := rd.label(now, berlin); !strings.HasPrefix(got, "mon 4 jan 2027 09:00 cet") {
		t.Fatalf("next year %q", got)
	}
	rd = reading{at: at(11, 3, 9, 0), r: rule{mask: weekdays, at: 540}}
	if got := rd.label(now, berlin); got != "weekdays at 09:00 cet · first in 23h 0m" {
		t.Fatalf("repeat %q", got)
	}
	if got := (reading{at: now}).label(now, zone("Asia/Kolkata")); !strings.Contains(got, "ist") || !strings.HasSuffix(got, "a moment") {
		t.Fatalf("zone %q", got)
	}
	if got := (reading{at: now}).label(now, zone("America/Sao_Paulo")); !strings.Contains(got, "utc-03") {
		t.Fatalf("numeric zone %q", got)
	}
}
