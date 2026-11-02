package pp

import (
	"bytes"
	"context"
	"github.com/6586x57890143/skua/internal/pixel"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// A member gets one sheet a day, the same one whoever asks, and another
// the next day.
func TestMorningOncePerDay(t *testing.T) {
	picked := func() *Module {
		m := launched(testModule(&fakeDB{}))
		if err := m.pick(3, 5, "thief"); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if _, due := testModule(&fakeDB{}).morning(3, 5); due {
		t.Error("a sheet before a class was picked")
	}
	m := picked()
	first, due := m.morning(3, 5)
	if !due || first.Rumour == "" {
		t.Fatal("no sheet on the first /pp of the day")
	}
	if _, due := m.morning(3, 5); due {
		t.Error("a second sheet the same day")
	}
	if other, _ := picked().morning(3, 5); other.Rumour != first.Rumour {
		t.Errorf("the same member's day drew %q and %q", first.Rumour, other.Rumour)
	}
	m.now = func() time.Time { return t0.Add(24 * time.Hour) }
	if _, due := m.morning(3, 5); !due {
		t.Error("no sheet the next day")
	}
}

// The news follows the season's days and runs out past them; the date
// counts the season's days from one.
func TestWorldLine(t *testing.T) {
	b := &Default
	start := b.SeasonStart
	for d, want := range worldLines[1] {
		if got := worldLine(b, start+int64(d)); got != want {
			t.Errorf("day %d reads %q", d, got)
		}
	}
	for _, day := range []int64{start - 1, start + int64(len(worldLines[1])), start + 7*b.SeasonWeeks} {
		if got := worldLine(b, day); got != "" {
			t.Errorf("day %d reads %q", day-start, got)
		}
	}
	if got := sheetDate(b, start+2); got != "season 1, day 3" {
		t.Errorf("the third day is dated %q", got)
	}
	if got := sheetDate(b, start-1); got != "before the season" {
		t.Errorf("the day before is dated %q", got)
	}
}

// The notes are what's true of them, and none when nothing is.
func TestNotes(t *testing.T) {
	m := testModule(&fakeDB{})
	today := DayOf(t0)
	s := m.st.server(3)
	mem := m.st.member(s, 3, 5, today)
	if got := m.notes(s, 0, mem, today); len(got) != 0 {
		t.Errorf("a new member's notes: %q", got)
	}
	mem.livery, mem.liveryUntil = "tanners", today
	_, _, need := m.st.bal().Level(0)
	mem.total = need - unit
	s.lit = 2
	mem.firstRoll = 3
	got := strings.Join(m.notes(s, 0, mem, today), "|")
	for _, want := range []string{"livery fades tonight", "1 pp off level 1", "hearth is lit", "3rd on the first roll"} {
		if !strings.Contains(got, want) {
			t.Errorf("the notes lack %q: %s", want, got)
		}
	}
}

// The first /pp of the day is the sheet, with the way on to the pot; the
// next is the status screen.
func TestMorningFirst(t *testing.T) {
	m := launched(testModule(&fakeDB{}))
	if err := m.pick(3, 5, "thief"); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		e, sent := coretest.Event(t, "pp", nil)
		if err := m.Commands()[0].Run(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		return js((*sent)[0])
	}
	if got := run(); !strings.Contains(got, "the morning sheet") || !strings.Contains(got, "pp:status") || !strings.Contains(got, "overheard") {
		t.Errorf("the first /pp: %s", got)
	}
	if got := run(); strings.Contains(got, "the morning sheet") || !strings.Contains(got, "pp:show") {
		t.Errorf("the second /pp: %s", got)
	}
}

// The sheet lays out clean with the longest news, three quests and every
// note, and with nothing at all.
func TestMorningLayout(t *testing.T) {
	long := ""
	for _, l := range worldLines[1] {
		if len(l) > len(long) {
			long = l
		}
	}
	rumour := ""
	for _, l := range rumours {
		if len(l) > len(rumour) {
			rumour = l
		}
	}
	full := Morning{Date: "season 12, day 28", News: long, Rumour: rumour,
		Notes: []string{"you're 10th on the first roll; wear its ribbon this week", "a raven has you 99,999 pp off level 99", "the hearth is lit, and your work goes further today"}}
	for tier := range 3 {
		full.Quests = append(full.Quests, QuestLine{Icon: "compass", Tier: tier, Reward: 99999 * unit,
			Tasks: []TaskLine{{What: "a selfie that stays a day", Goal: 1}, {What: "messages", Goal: 9}}})
	}
	checkLayout(t, "full", morningCanvas(full))
	checkLayout(t, "empty", morningCanvas(Morning{Date: "before the season", Rumour: rumours[0]}))
	if f := morningFrames(full); len(f) != ambientFrames || bytes.Equal(f[0].Pix, f[ambientFrames/2].Pix) {
		t.Error("the pot square over the day stands still")
	}
	if dir := os.Getenv("SKUA_SHOT"); dir != "" {
		realScreens(t)
		pic, err := MorningScreen(full)
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(dir+"/morning.webp", pic, 0o644)
		png, _ := morningFrames(full)[0].PNG(3)
		_ = os.WriteFile(dir+"/morning.png", png, 0o644)
	}
}

// Every line is in the bird voice: lowercase, no semicolons, no stop.
func TestMorningVoice(t *testing.T) {
	all := append(append([]string{noNews}, rumours...), worldLines[1]...)
	for _, l := range all {
		if l != strings.ToLower(l) || strings.Contains(l, ";") || strings.HasSuffix(l, ".") {
			t.Errorf("off voice: %q", l)
		}
	}
}

// Every kind of task fits its line on the sheet whole, with a second task
// after it and the biggest reward beside it, so nothing is cut.
func TestSheetTasksFit(t *testing.T) {
	room := 368 - pixel.Regular.Width(grouped(slices.Max(Default.QuestPP[:])/unit)) - 4 - (252 + pixel.IconCells + 3)
	if room < sheetTaskRoom {
		t.Fatalf("the sheet leaves %d for a task, not %d", room, sheetTaskRoom)
	}
	seen := map[string]bool{}
	for _, m := range []string{taskVoice, taskTalk, taskHearth, taskSelfie, taskCrowd, taskReply, taskStream, taskCamera} {
		s := sheetTask(Task{Metric: m, Goal: 120})
		if w := pixel.Light.Width(s + "+"); w > sheetTaskRoom {
			t.Errorf("%q is %d wide", s, w)
		}
		if seen[s] {
			t.Errorf("%q twice", s)
		}
		seen[s] = true
	}
}
