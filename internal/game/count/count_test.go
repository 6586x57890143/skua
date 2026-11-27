package count

import (
	"cmp"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"
)

const guild, ch = 7, 9

var t0 = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

// table is a channel and a clock: each move is a fresh message a second
// after the last, unless it says otherwise.
type table struct {
	t   *testing.T
	en  *Engine
	msg uint64
	at  time.Time
	gap time.Duration // between moves; a second when unset
}

func newTable(t *testing.T, r Ruleset) *table {
	en := &Engine{}
	en.Open(guild, ch, r)
	return &table{t: t, en: en, at: t0}
}

func (tb *table) say(user uint64, text string) Verdict {
	return tb.sayAfter(cmp.Or(tb.gap, time.Second), user, text)
}

func (tb *table) sayAfter(d time.Duration, user uint64, text string) Verdict {
	tb.msg++
	tb.at = tb.at.Add(d)
	return tb.en.Submit(ch, Move{User: user, Msg: tb.msg, Text: text, At: tb.at})
}

// run counts from the next number to n, members taking turns.
func (tb *table) run(n int64) {
	tb.t.Helper()
	s, _ := tb.en.Channel(ch)
	for i := s.N + 1; i <= n; i++ {
		text := Say(i*s.Rules.Step, s.Rules)
		if s.Rules.Math == MathOnly {
			text += "+0"
		}
		user := uint64(100 + i%2)
		if i%bossEvery == 0 || i%bossEvery > bossEvery-bossLength {
			// A boss stretch takes ten different members.
			user = uint64(1000 + i)
		}
		if v := tb.say(user, text); v.Kind != Accept {
			tb.t.Fatalf("count %d: %+v", i, v)
		}
	}
}

func (tb *table) state() State { s, _ := tb.en.Channel(ch); return s }

func TestRead(t *testing.T) {
	dec := Standard()
	for _, c := range []struct {
		in      string
		r       Ruleset
		counted bool
		v       int64
		word    string
	}{
		{"42", dec, true, 42, ""},
		{"  42 is the answer", dec, true, 42, ""},
		{"2 3", dec, false, 0, ""},
		{"2\n3", dec, false, 0, ""},
		{"42 after 41!", dec, false, 0, ""},
		{"42 or 6*7", dec, false, 0, ""},
		{"42 <@175928847299117063> <:skua:123> <t:1700000000:R>", dec, true, 42, ""},
		{"42 lol 2nd try", dec, true, 42, ""},
		{"xlii xliii", Ruleset{Format: Roman}.Normal(), true, 42, ""},
		{"2a 2b", Ruleset{Format: Hex}.Normal(), false, 0, ""},
		{"42\nnext line", dec, true, 42, ""},
		{"**42**", dec, true, 42, ""},
		{"42!", dec, true, 42, ""},
		{"042", dec, true, 42, ""},
		{"hello", dec, false, 0, ""},
		{"", dec, false, 0, ""},
		{"1234567890123456789", dec, true, 1234567890123456789, ""},
		{"12345678901234567890", dec, false, 0, ""},
		{"010+1", dec, true, 11, ""},
		{"08+1", dec, true, 9, ""},
		{"007*6", dec, true, 42, ""},
		{"100+0", dec, true, 100, ""},
		{"0+42", dec, true, 42, ""},
		{"1111111111111111111111111111111111111111", Ruleset{Format: Binary}.Normal(), true, 1<<40 - 1, ""},
		{"4000", Ruleset{Format: Roman}.Normal(), true, 4000, ""},
		{"6*7", dec, true, 42, ""},
		{"(40+4)/2*2-2", dec, true, 42, ""},
		{"85/2", dec, false, 0, ""}, // not whole
		{"84/2", dec, true, 42, ""},
		{"-(-42)", dec, true, 42, ""},
		{"85%43", dec, true, 42, ""},
		{"8/4%3", dec, true, 2, ""},
		{"3/2%1", dec, false, 0, ""},
		{"1/0", dec, false, 0, ""},
		{"5%0", dec, false, 0, ""},
		{"2^5", dec, true, 32, ""},
		// What a counting channel actually types.
		{"Γ(7)/Γ(5)", dec, true, 30, ""},
		{"(√(6!) × √5)/2", dec, true, 30, ""},
		{"(sqrt(6!)*sqrt(5))/2", dec, true, 30, ""},
		{"((6!)^(1/2)*5^(1/2))/2", dec, true, 30, ""},
		{"((6!)**(1/2)*5**(1/2))/2", dec, true, 30, ""},
		{"3!!/4!", dec, true, 30, ""},
		{"((3!)! + 4!)/4!", dec, true, 31, ""},
		{"((3!)! + 4!)/4!\nthat's disappointing", dec, true, 31, ""},
		{"6 * 7 is 42", dec, false, 0, ""}, // not one expression, and two numbers
		{"6 × 7", dec, true, 42, ""},
		{"84 ÷ 2", dec, true, 42, ""},
		{"43 − 1", dec, true, 42, ""},
		{"6·7", dec, true, 42, ""},
		{"2^-1*84", dec, true, 42, ""},
		{"-2^2+46", dec, true, 42, ""}, // a sign binds looser than a power
		{"2^3^2", dec, true, 512, ""},  // right to left
		{"8^(1/3)*21", dec, true, 42, ""},
		{"(-8)^(1/3)+44", dec, true, 42, ""},
		{"(-4)^(1/2)", dec, false, 0, ""},
		{"2^(1/2)", dec, false, 0, ""},
		{"7²-7", dec, true, 42, ""},
		{"3³+15", dec, true, 42, ""},
		{"(0)!", dec, true, 1, ""},
		{"gamma(1)", dec, true, 1, ""},
		{"Γ(0)", dec, false, 0, ""},
		{"Γ(1/2)", dec, false, 0, ""},
		{"(1/2)!", dec, false, 0, ""},
		{"61!/61!", dec, false, 0, ""}, // past the largest factorial
		{"2^300/2^299", dec, false, 0, ""},
		{"2^255/2^254", dec, true, 2, ""},
		{"1^999999999999999999", dec, true, 1, ""},
		{"0^-1", dec, false, 0, ""},
		{"√-4", dec, false, 0, ""},
		{"6!", dec, true, 6, ""}, // excitement; 720 too, below
		{"6!", Ruleset{Math: MathOnly}.Normal(), true, 720, ""},
		{"42!", Ruleset{Math: MathOnly}.Normal(), false, 0, ""},
		{"6!", Ruleset{Math: MathOff}.Normal(), true, 6, ""},
		{"1<<62", dec, false, 0, ""},
		{"0x2a+0", dec, false, 0, ""},
		{"1_0+1", dec, false, 0, ""},
		{"1.5*2", dec, false, 0, ""},
		{"x+1", dec, false, 0, ""},
		{"f(1)+1", dec, false, 0, ""},
		{"(((((((((1)))))))))+1", dec, false, 0, ""},
		{"1+1+1+1+1+1+1+1+1+1+1+1+1", dec, false, 0, ""},
		{"999999999999999999*999999999999999999", dec, false, 0, ""}, // past int64
		{"6*7", Ruleset{Math: MathOff}.Normal(), false, 0, ""},
		{"42", Ruleset{Math: MathOnly}.Normal(), false, 0, ""},
		{"40+2", Ruleset{Math: MathOnly}.Normal(), true, 42, ""},
		{"101010", Ruleset{Format: Binary}.Normal(), true, 42, ""},
		{"0b101010", Ruleset{Format: Binary}.Normal(), true, 42, ""},
		{"102", Ruleset{Format: Binary}.Normal(), false, 0, ""},
		{"2a", Ruleset{Format: Hex}.Normal(), true, 42, ""},
		{"0x2A", Ruleset{Format: Hex}.Normal(), true, 42, ""},
		{"xlii", Ruleset{Format: Roman}.Normal(), true, 42, ""},
		{"XXXXII", Ruleset{Format: Roman}.Normal(), false, 0, ""},
		{"Fizz", Ruleset{Format: FizzBuzz}.Normal(), true, 0, "fizz"},
		{"fizzbuzz", Ruleset{Format: FizzBuzz}.Normal(), true, 0, "fizzbuzz"},
		{"41+1", Ruleset{Format: FizzBuzz}.Normal(), true, 42, ""},
	} {
		got := read(c.in, c.r)
		if got.counted != c.counted || got.value != c.v || got.word != c.word {
			t.Errorf("read(%q, %s/%s) = %+v", c.in, c.r.Format, c.r.Math, got)
		}
	}
	long := "1"
	for len(long) <= maxToken {
		long += "+1"
	}
	if read(long, dec).counted {
		t.Error("a word past the cap was read")
	}
}

func TestRoman(t *testing.T) {
	for v := int64(1); v <= 3999; v++ {
		if got, ok := roman(toRoman(v)); !ok || got != v {
			t.Fatalf("roman %d: %s -> %d", v, toRoman(v), got)
		}
	}
	for _, bad := range []string{"", "IIII", "IC", "VX", "MMMM", "ABC"} {
		if _, ok := roman(bad); ok {
			t.Errorf("%q read as roman", bad)
		}
	}
	if Say(4000, Ruleset{Format: Roman}.Normal()) != "4000" {
		t.Error("past roman")
	}
}

func TestSay(t *testing.T) {
	for _, c := range []struct {
		f    Format
		v    int64
		want string
	}{{Decimal, 42, "42"}, {Binary, 5, "101"}, {Hex, 255, "ff"}, {Roman, 14, "XIV"}, {FizzBuzz, 9, "fizz"}, {FizzBuzz, 10, "buzz"}, {FizzBuzz, 30, "fizzbuzz"}, {FizzBuzz, 7, "7"}} {
		if got := Say(c.v, Ruleset{Format: c.f}.Normal()); got != c.want {
			t.Errorf("Say(%d, %s) = %s", c.v, c.f, got)
		}
	}
}

func TestNormal(t *testing.T) {
	r := Ruleset{Name: "x", Fail: "nope", Format: "nope", Math: "nope", Step: 99999, Lives: 50, Cooldown: -1, Grace: time.Hour, Timeout: 48 * time.Hour}.Normal()
	if r.Fail != Reset || r.Format != Decimal || r.Math != MathOn || r.Step != maxStep || r.Lives != 0 || r.Cooldown != 0 || r.Grace != maxGrace || r.Timeout != maxTimeout || r.Name != "custom" {
		t.Fatalf("%+v", r)
	}
	if l := (Ruleset{Fail: Lives, Lives: 50}).Normal(); l.Lives != maxLives {
		t.Fatalf("lives %d", l.Lives)
	}
	if l := (Ruleset{Fail: Lives}).Normal(); l.Lives != 1 {
		t.Fatalf("lives %d", l.Lives)
	}
	if r := (Ruleset{Format: Roman, Step: 100, Math: MathOn}).Normal(); r.Step != 10 || r.Math != MathOff {
		t.Fatalf("roman %+v", r)
	}
	names := Presets()
	if names[0] != "standard" || len(names) != len(presets) {
		t.Fatalf("presets %v", names)
	}
	for _, n := range names {
		p, ok := Preset(n)
		if !ok || p.Name != n || p != p.Normal() {
			t.Fatalf("preset %s is not normal: %+v", n, p)
		}
	}
	if _, ok := Preset("nope"); ok {
		t.Fatal("unknown preset")
	}
}

// Every preset counts its own way to a thousand and past, the boss
// stretch included.
func TestEveryPresetCounts(t *testing.T) {
	for _, name := range Presets() {
		t.Run(name, func(t *testing.T) {
			r, _ := Preset(name)
			tb := newTable(t, r)
			tb.gap = r.Cooldown/2 + time.Second
			tb.run(1010)
			if s := tb.state(); s.N != 1010 || s.Best != 1010 || s.Total != 1010 {
				t.Fatalf("%+v", s)
			}
		})
	}
}

func TestChatAndTheBasics(t *testing.T) {
	tb := newTable(t, Standard())
	if v := tb.say(1, "hi all"); v.Kind != Ignore {
		t.Fatalf("chat %+v", v)
	}
	v := tb.say(1, "1")
	if v.Kind != Accept || v.N != 1 || v.Best != 1 || v.Record {
		t.Fatalf("first %+v", v)
	}
	if v := tb.say(2, "2 yay"); v.Kind != Accept || v.N != 2 {
		t.Fatalf("second %+v", v)
	}
	if s := tb.state(); s.Next() != "3" || s.Last != 2 {
		t.Fatalf("next %s", s.Next())
	}
}

func TestCountMsgAndSays(t *testing.T) {
	tb := newTable(t, Standard())
	tb.say(1, "1")
	tb.say(2, "hello")
	s := tb.state()
	if s.CountMsg != 1 || !s.Says("1 yes") || !s.Says("2-1") || s.Says("2") || s.Says("hello") {
		t.Fatalf("%+v", s)
	}
	tb.say(2, "5")
	tb.say(2, "5")
	tb.say(2, "5")
	if s := tb.state(); s.CountMsg != 0 {
		t.Fatal("a broken run kept its count message")
	}
}

// "2 3" is chat: read as 2 it would fail whoever counts 3 next.
func TestTwoNumbersAreChat(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore}.Normal())
	tb.say(1, "1")
	if v := tb.say(2, "2 3"); v.Kind != Ignore {
		t.Fatalf("%+v", v)
	}
	if v := tb.say(3, "2"); v.Kind != Accept {
		t.Fatalf("%+v", v)
	}
}

// "5!" at 5 is a count said loudly, and at 120 a factorial: either is right.
func TestBangIsLoudOrFactorial(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore}.Normal())
	tb.en.Set(ch, 4)
	if v := tb.say(1, "5!"); v.Kind != Accept {
		t.Fatalf("5! at 5 %+v", v)
	}
	tb.en.Set(ch, 119)
	if v := tb.say(2, "5!"); v.Kind != Accept {
		t.Fatalf("5! at 120 %+v", v)
	}
	if !tb.state().Says("5!") {
		t.Fatal("an edit to the same 5! no longer says 120")
	}
}

func TestStaleMovesChangeNothing(t *testing.T) {
	tb := newTable(t, Standard())
	tb.say(1, "1")
	before := tb.state()
	// An older message arriving late, even the right number.
	v := tb.en.Submit(ch, Move{User: 2, Msg: tb.msg, Text: "2", At: tb.at})
	if v.Kind != Stale || tb.state().N != before.N {
		t.Fatalf("%+v", v)
	}
	if v := tb.en.Submit(ch, Move{User: 2, Msg: tb.msg - 1, Text: "99", At: tb.at}); v.Kind != Stale {
		t.Fatalf("an old miss %+v", v)
	}
}

// Moves read back after skua was away: the right counts stand, the misses
// change nothing.
func TestForgivenMoves(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore}.Normal())
	tb.run(5)
	back := func(user uint64, text string) Verdict {
		tb.msg++
		tb.at = tb.at.Add(time.Second)
		return tb.en.Submit(ch, Move{User: user, Msg: tb.msg, Text: text, At: tb.at, Forgive: true})
	}
	if v := back(1, "6"); v.Kind != Accept {
		t.Fatalf("%+v", v)
	}
	if v := back(1, "7"); v.Kind != Ignore || v.Miss != Twice {
		t.Fatalf("twice %+v", v)
	}
	if v := back(2, "70"); v.Kind != Ignore || v.Miss != Wrong {
		t.Fatalf("wrong %+v", v)
	}
	if v := back(2, "7"); v.Kind != Accept {
		t.Fatalf("%+v", v)
	}
	if s := tb.state(); s.N != 7 || s.Run != 1 {
		t.Fatalf("%+v", s)
	}
	if m := tb.en.Member(Who{guild, 2}); m.Misses != 0 {
		t.Fatal("a forgiven miss counted")
	}
}

func TestSnipeIsATieNotAMiss(t *testing.T) {
	tb := newTable(t, Standard())
	tb.say(1, "1")
	tb.say(2, "2")
	if v := tb.sayAfter(200*time.Millisecond, 3, "2"); v.Kind != Sniped {
		t.Fatalf("snipe %+v", v)
	}
	// The sniper can still count next.
	if v := tb.say(3, "3"); v.Kind != Accept {
		t.Fatalf("after snipe %+v", v)
	}
	// Past the grace the same number is a miss.
	if v := tb.sayAfter(2*time.Second, 1, "3"); v.Kind == Sniped || v.Kind == Accept {
		t.Fatalf("late snipe %+v", v)
	}
	// The counter repeating themselves is a miss, not a snipe.
	tb2 := newTable(t, Ruleset{Fail: Hardcore, Grace: time.Second}.Normal())
	tb2.say(1, "1")
	tb2.say(2, "2")
	if v := tb2.sayAfter(100*time.Millisecond, 2, "2"); v.Kind != Broken {
		t.Fatalf("self repeat %+v", v)
	}
}

func TestTwiceInARow(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore}.Normal())
	tb.say(1, "1")
	if v := tb.say(1, "2"); v.Kind != Broken || v.Miss != Twice || v.Length != 1 || v.Expected != "2" {
		t.Fatalf("%+v", v)
	}
	solo, _ := Preset("solo")
	solo.Cooldown = 0
	tb = newTable(t, solo)
	tb.say(1, "1")
	if v := tb.say(1, "2"); v.Kind != Accept {
		t.Fatalf("solo %+v", v)
	}
}

func TestCooldown(t *testing.T) {
	r := Standard()
	r.Solo, r.Cooldown = true, 5*time.Second
	tb := newTable(t, r)
	tb.say(1, "1")
	if v := tb.sayAfter(2*time.Second, 1, "2"); v.Kind != Early {
		t.Fatalf("%+v", v)
	}
	if v := tb.sayAfter(2*time.Second, 2, "2"); v.Kind != Accept {
		t.Fatalf("another member %+v", v)
	}
	if v := tb.sayAfter(4*time.Second, 1, "3"); v.Kind != Accept {
		t.Fatalf("after the wait %+v", v)
	}
}

func TestFuzzyWordsFarOffAreChat(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore}.Normal())
	tb.run(40)
	// A date or a score reads as an expression far from the count.
	for _, chat := range []string{"2026-11-26", "10-15", "3-1"} {
		if v := tb.say(5, chat); v.Kind != Ignore {
			t.Fatalf("%q %+v", chat, v)
		}
	}
	// One that lands near the count is a miss.
	if v := tb.say(5, "40+2"); v.Kind != Broken || v.Miss != Wrong {
		t.Fatalf("near miss %+v", v)
	}
	// A plain number is always a count.
	tb.run(40)
	if v := tb.say(5, "9000"); v.Kind != Broken {
		t.Fatalf("plain far miss %+v", v)
	}
	rr := newTable(t, Ruleset{Fail: Hardcore, Format: Roman}.Normal())
	rr.run(3)
	if v := rr.say(5, "MIX"); v.Kind != Ignore {
		t.Fatalf("roman chat %+v", v)
	}
	if v := rr.say(5, "VI"); v.Kind != Broken {
		t.Fatalf("roman miss %+v", v)
	}
}

func TestFormatsNeverStall(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore, Format: Binary, Step: 1000}.Normal())
	tb.run(300) // past 2^18, 19 binary digits
	rr := newTable(t, Ruleset{Fail: Hardcore, Format: Roman, Step: 10}.Normal())
	rr.run(410) // past 3999 the count goes on in decimal
	if s := rr.state(); s.Next() != "4110" {
		t.Fatalf("roman past 3999 %s", s.Next())
	}
	// Under 3999 a roman channel wants its numeral, not the digits.
	ro := newTable(t, Ruleset{Fail: Hardcore, Format: Roman}.Normal())
	ro.run(3)
	if v := ro.say(9, "4"); v.Kind != Broken {
		t.Fatalf("digits for a numeral %+v", v)
	}
	if s, _ := rr.en.Set(ch, 5000); s.Next() != "5010" {
		t.Fatalf("set past 3999 %s", s.Next())
	}
	if v := rr.say(1, "5010"); v.Kind != Accept {
		t.Fatalf("%+v", v)
	}
}

func TestLetterWordsAreChat(t *testing.T) {
	ro := newTable(t, Ruleset{Fail: Hardcore, Format: Roman}.Normal())
	ro.run(3)
	for _, chat := range []string{"I think so", "i", "mix", "vi"} {
		if v := ro.say(9, chat); v.Kind != Ignore {
			t.Fatalf("%q %+v", chat, v)
		}
	}
	if v := ro.say(9, "iv"); v.Kind != Accept {
		t.Fatalf("a right numeral in lowercase %+v", v)
	}
	if v := ro.say(8, "VI"); v.Kind != Broken {
		t.Fatalf("an uppercase numeral near the count is a miss %+v", v)
	}
	hx := newTable(t, Ruleset{Fail: Hardcore, Format: Hex}.Normal())
	hx.run(5)
	for _, chat := range []string{"a lot", "add", "be", "fed"} {
		if v := hx.say(9, chat); v.Kind != Ignore {
			t.Fatalf("%q %+v", chat, v)
		}
	}
	if v := hx.say(9, "12"); v.Kind != Broken {
		t.Fatalf("hex digits miss %+v", v)
	}
}

func TestCooldownIsPerChannel(t *testing.T) {
	en := &Engine{}
	free, slow := Standard(), Standard()
	slow.Cooldown = time.Hour
	en.Open(guild, 1, free)
	en.Open(guild, 2, slow)
	en.Submit(1, Move{User: 5, Msg: 1, Text: "1", At: t0})
	if v := en.Submit(2, Move{User: 5, Msg: 2, Text: "1", At: t0.Add(time.Minute)}); v.Kind != Accept {
		t.Fatalf("another channel's count started the cooldown %+v", v)
	}
	if v := en.Submit(2, Move{User: 6, Msg: 3, Text: "2", At: t0.Add(2 * time.Minute)}); v.Kind != Accept {
		t.Fatal(v)
	}
	if v := en.Submit(2, Move{User: 5, Msg: 4, Text: "3", At: t0.Add(3 * time.Minute)}); v.Kind != Early {
		t.Fatalf("%+v", v)
	}
	if s, _ := en.Channel(2); len(s.Waits) != 2 {
		t.Fatalf("waits %v", s.Waits)
	}
	if v := en.Submit(2, Move{User: 7, Msg: 5, Text: "3", At: t0.Add(2 * time.Hour)}); v.Kind != Accept {
		t.Fatal(v)
	}
	if s, _ := en.Channel(2); len(s.Waits) != 1 {
		t.Fatalf("old waits kept: %v", s.Waits)
	}
}

func TestFizzBuzzSnipe(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore, Format: FizzBuzz, Grace: time.Second}.Normal())
	tb.run(3)
	if v := tb.sayAfter(200*time.Millisecond, 9, "fizz"); v.Kind != Sniped {
		t.Fatalf("a fizz tie %+v", v)
	}
	if v := tb.sayAfter(200*time.Millisecond, 8, "3"); v.Kind != Broken {
		t.Fatalf("the number where fizz was due is no tie %+v", v)
	}
}

// A best is a run's length: an imported run from 5000 has counted one at
// 5001, and beats a best of 120 only at its 121st count.
func TestRecordAfterImport(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore}.Normal())
	tb.run(120)
	tb.say(1, "5")
	s, _ := tb.en.Set(ch, 5000)
	if v := tb.say(1, "5001"); v.Record || v.Best != 120 {
		t.Fatalf("one count claimed a best of 5001: %+v", v)
	}
	for n := int64(5002); n <= 5120; n++ {
		if v := tb.say(uint64(1+(n+1)%2), strconv.FormatInt(n, 10)); v.Record {
			t.Fatalf("record at %d, a run of %d", n, n-s.Start)
		}
	}
	if v := tb.say(1, "5121"); !v.Record || v.Best != 121 {
		t.Fatalf("the 121st count of the run %+v", v)
	}
}

func TestFizzBuzz(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore, Format: FizzBuzz}.Normal())
	tb.run(2)
	if v := tb.say(1, "3"); v.Kind != Broken {
		t.Fatalf("number where fizz is due %+v", v)
	}
	tb.run(2)
	if v := tb.say(1, "buzz"); v.Kind != Broken || v.Expected != "fizz" {
		t.Fatalf("wrong word %+v", v)
	}
	tb.run(3)
	if v := tb.say(1, "fizz"); v.Kind != Broken || v.Expected != "4" {
		t.Fatalf("word where a number is due %+v", v)
	}
}

func TestSteps(t *testing.T) {
	r := Standard()
	r.Step = 5
	tb := newTable(t, r)
	tb.say(1, "5")
	if v := tb.say(2, "10"); v.Kind != Accept || v.N != 2 {
		t.Fatalf("%+v", v)
	}
	if tb.state().Next() != "15" {
		t.Fatal("next")
	}
}

func TestSavesMemberThenChannel(t *testing.T) {
	tb := newTable(t, Standard())
	// Members 100 and 101 take turns: each has 125 counts at 250, so each
	// earned a save at 100, and the channel one at 250.
	tb.run(250)
	s := tb.state()
	if s.Saves != 1 {
		t.Fatalf("bank %d", s.Saves)
	}
	m := tb.en.Member(Who{guild, 101})
	if m.Saves != 1 || m.Counts != 125 {
		t.Fatalf("member %+v", m)
	}
	v := tb.say(101, "7")
	if v.Kind != Saved || v.Saver != ByMember || v.Left != 0 {
		t.Fatalf("own save %+v", v)
	}
	v = tb.say(101, "7")
	if v.Kind != Saved || v.Saver != ByChannel || v.Left != 0 {
		t.Fatalf("bank save %+v", v)
	}
	if tb.state().N != 250 {
		t.Fatal("a save moved the count")
	}
	v = tb.say(101, "7")
	if v.Kind != Broken || v.Length != 250 || v.Best != 250 || v.N != 0 || v.Run != 1 {
		t.Fatalf("no saves left %+v", v)
	}
	if m := tb.en.Member(Who{guild, 101}); m.SavesUsed != 2 || m.Misses != 3 || m.Breaks != 1 {
		t.Fatalf("member after %+v", m)
	}
	// Saves cap.
	tb2 := newTable(t, Standard())
	tb2.run(1500)
	if s := tb2.state(); s.Saves != bankMax {
		t.Fatalf("bank cap %d", s.Saves)
	}
	if m := tb2.en.Member(Who{guild, 100}); m.Saves != memberMax {
		t.Fatalf("member cap %d", m.Saves)
	}
}

func TestLives(t *testing.T) {
	r, _ := Preset("lives")
	tb := newTable(t, r)
	tb.run(5)
	if v := tb.say(1, "99"); v.Kind != Lost || v.Left != 2 {
		t.Fatalf("%+v", v)
	}
	if v := tb.say(1, "99"); v.Kind != Lost || v.Left != 1 {
		t.Fatalf("%+v", v)
	}
	tb.run(100) // the hundredth refills
	if s := tb.state(); s.Lives != 3 {
		t.Fatalf("refill %d", s.Lives)
	}
	for range 2 {
		tb.say(1, "1")
	}
	if v := tb.say(1, "1"); v.Kind != Broken || v.Length != 100 {
		t.Fatalf("last life %+v", v)
	}
	if s := tb.state(); s.Lives != 3 || s.N != 0 || s.Run != 2 {
		t.Fatalf("new run %+v", s)
	}
}

func TestHardcoreTimeout(t *testing.T) {
	r, _ := Preset("hardcore")
	r.Timeout = 10 * time.Minute
	tb := newTable(t, r)
	tb.run(3)
	v := tb.say(9, "5")
	if v.Kind != Broken || v.Timeout != 10*time.Minute || v.Miss != Wrong {
		t.Fatalf("%+v", v)
	}
	if tb.en.Member(Who{guild, 9}).Saves != 0 {
		t.Fatal("hardcore earned saves")
	}
}

func TestRecordMilestoneAndFlock(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore, Arcade: true}.Normal())
	var flock []int
	for i := int64(1); i <= 100; i++ {
		v := tb.say(uint64(1+i%2), strconv.FormatInt(i, 10))
		if v.Record {
			t.Fatal("first run set a record")
		}
		if v.Milestone != (i == 100) {
			t.Fatalf("milestone at %d", i)
		}
		if v.Flock > 0 {
			flock = append(flock, v.Flock)
		}
	}
	if len(flock) != 1 || flock[0] != 1 {
		t.Fatalf("flock %v", flock)
	}
	tb.say(1, "1") // a miss: 1 counted twice in a row is a wrong number here
	tb.run(100)
	if v := tb.say(2, "101"); !v.Record {
		t.Fatalf("record %+v", v)
	}
	if v := tb.say(1, "102"); v.Record {
		t.Fatal("record twice")
	}
}

func TestArcadeOffIsJustCounting(t *testing.T) {
	r := Standard()
	r.Arcade = false
	tb := newTable(t, r)
	tb.run(1000)
	v := tb.say(1, "1001")
	if v.Combo != 0 || v.Flock != 0 || v.Rank != 0 || v.Boss {
		t.Fatalf("%+v", v)
	}
	if s := tb.state(); s.Bounty != 0 {
		t.Fatal("bounty without arcade")
	}
	if m := tb.en.Member(Who{guild, 100}); m.XP != m.Counts {
		t.Fatalf("without arcade a count is a point of xp, no more: %+v", m)
	}
	// The boss rule is arcade too: one member may come back within ten.
	tb.run(1989)
	tb.say(7, "1990")
	tb.say(8, "1991")
	if v := tb.say(7, "1992"); v.Kind != Accept {
		t.Fatalf("boss without arcade %+v", v)
	}
}

func TestBoss(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore, Arcade: true}.Normal())
	tb.run(990)
	if v := tb.say(50, "991"); !v.Boss {
		t.Fatal("stretch start")
	}
	tb.say(51, "992")
	if v := tb.say(50, "993"); v.Kind != Broken || v.Miss != Boss {
		t.Fatalf("a member twice in the stretch %+v", v)
	}
	tb.run(990)
	for i := int64(991); i <= 1000; i++ {
		if v := tb.say(uint64(200+i), strconv.FormatInt(i, 10)); v.Kind != Accept || !v.Boss {
			t.Fatalf("%d %+v", i, v)
		}
	}
	if v := tb.say(1191, "1001"); v.Kind != Accept || v.Boss {
		t.Fatalf("after the stretch a member may come back %+v", v)
	}
}

func TestBountyIsInTheChain(t *testing.T) {
	tb := newTable(t, Standard())
	tb.run(100)
	s := tb.state()
	if s.Bounty <= 100 || s.Bounty > 199 {
		t.Fatalf("bounty %d", s.Bounty)
	}
	// Anyone holding the hundredth link can work it out.
	again := newTable(t, Standard())
	again.run(100)
	if again.state().Bounty != s.Bounty {
		t.Fatal("same chain, different bounty")
	}
	hits := 0
	for i := int64(101); i <= 200; i++ {
		if v := tb.say(uint64(100+i%2), strconv.FormatInt(i, 10)); v.Bounty {
			hits++
			if i != s.Bounty {
				t.Fatalf("bounty at %d, want %d", i, s.Bounty)
			}
		}
	}
	if hits != 1 {
		t.Fatalf("%d bounties", hits)
	}
}

func TestComboAndRanks(t *testing.T) {
	tb := newTable(t, Standard())
	var top, ranks int
	for i := int64(1); i <= 200; i++ {
		v := tb.sayAfter(300*time.Millisecond, uint64(1+i%2), strconv.FormatInt(i, 10))
		top = max(top, v.Combo)
		if v.Rank > 0 {
			ranks++
		}
	}
	if top != 3 {
		t.Fatalf("fast play topped out at %d", top)
	}
	if ranks == 0 {
		t.Fatal("no rank ups")
	}
	if v := tb.sayAfter(time.Minute, 1, "201"); v.Combo != 0 {
		t.Fatalf("a minute's pause kept the combo %d", v.Combo)
	}
	if m := tb.en.Member(Who{guild, 1}); m.BestCombo != 3 || m.XP <= m.Counts {
		t.Fatalf("member %+v", m)
	}
}

func TestChainVerifies(t *testing.T) {
	tb := newTable(t, Standard())
	var entries []Entry
	for i := int64(1); i <= 50; i++ {
		u := uint64(1 + i%2)
		v := tb.say(u, strconv.FormatInt(i, 10))
		entries = append(entries, Entry{N: i, User: u, Msg: tb.msg})
		if v.Hash != tb.state().Head {
			t.Fatal("verdict hash is not the head")
		}
	}
	s := tb.state()
	if !Verify(guild, ch, s.Run, 0, entries, s.Head) {
		t.Fatal("an honest run failed to verify")
	}
	for _, tamper := range []func([]Entry){
		func(e []Entry) { e[10].User = 99 },
		func(e []Entry) { e[10].Msg++ },
		func(e []Entry) { e[10], e[11] = e[11], e[10] },
	} {
		bad := append([]Entry(nil), entries...)
		tamper(bad)
		if Verify(guild, ch, s.Run, 0, bad, s.Head) {
			t.Fatal("a tampered run verified")
		}
	}
	if Verify(guild, ch, s.Run, 0, entries[:49], s.Head) || Verify(guild, ch+1, s.Run, 0, entries, s.Head) || Verify(guild, ch, s.Run+1, 0, entries, s.Head) {
		t.Fatal("a short, moved or renumbered run verified")
	}
}

func TestSetImportsARun(t *testing.T) {
	tb := newTable(t, Standard())
	tb.run(3)
	s, ok := tb.en.Set(ch, 5000)
	if !ok || s.N != 5000 || s.Start != 5000 || s.Run != 2 {
		t.Fatalf("%+v", s)
	}
	if v := tb.say(1, "5001"); v.Kind != Accept || v.N != 5001 {
		t.Fatalf("%+v", v)
	}
	if !Verify(guild, ch, 2, 5000, []Entry{{5001, 1, tb.msg}}, tb.state().Head) {
		t.Fatal("imported run doesn't verify")
	}
	if v := tb.say(1, "5001"); v.Kind == Sniped {
		t.Fatal("a member's own repeat sniped")
	}
	if _, ok := tb.en.Set(12345, 1); ok {
		t.Fatal("set on no channel")
	}
}

func TestOpenAfterNumbersRunsOn(t *testing.T) {
	en := &Engine{}
	en.OpenAfter(guild, ch, Standard(), 7)
	if s, _ := en.Channel(ch); s.Run != 8 || s.Head != Genesis(guild, ch, 8, 0) {
		t.Fatalf("%+v", s)
	}
	// An open channel keeps its run whatever lastRun says.
	en.OpenAfter(guild, ch, Standard(), 100)
	if s, _ := en.Channel(ch); s.Run != 8 {
		t.Fatalf("%+v", s)
	}
}

func TestAnchorPicksUpFromTheNextCount(t *testing.T) {
	tb := newTable(t, Standard())
	tb.run(5)
	anchor := func(user uint64, text string) Verdict {
		tb.msg++
		tb.at = tb.at.Add(time.Second)
		return tb.en.Anchor(ch, Move{User: user, Msg: tb.msg, Text: text, At: tb.at})
	}
	for _, chat := range []string{"hi", "2026-11-26", "0"} {
		if v := anchor(1, chat); v.Kind != Ignore {
			t.Fatalf("%q anchored: %+v", chat, v)
		}
	}
	v := anchor(1, "901")
	if v.Kind != Accept || v.N != 901 || v.Run != 2 {
		t.Fatalf("%+v", v)
	}
	s := tb.state()
	if s.Start != 900 || s.Best != 5 || !Verify(guild, ch, 2, 900, []Entry{{901, 1, tb.msg}}, s.Head) {
		t.Fatalf("%+v", s)
	}
	if v := tb.say(2, "902"); v.Kind != Accept {
		t.Fatalf("%+v", v)
	}
	// An older message can't anchor.
	if v := tb.en.Anchor(ch, Move{User: 3, Msg: 1, Text: "5", At: tb.at}); v.Kind != Ignore {
		t.Fatalf("%+v", v)
	}
	if v := tb.en.Anchor(12345, Move{User: 3, Msg: 99, Text: "5"}); v.Kind != Ignore {
		t.Fatal("anchored a channel that doesn't count")
	}
}

func TestNewBestOnABreak(t *testing.T) {
	tb := newTable(t, Ruleset{Fail: Hardcore}.Normal())
	tb.run(10)
	if v := tb.say(1, "99"); v.NewBest {
		t.Fatal("the first run beat nothing")
	}
	tb.run(10)
	if v := tb.say(1, "99"); v.NewBest {
		t.Fatal("a tie is not a new best")
	}
	tb.run(11)
	if v := tb.say(1, "99"); !v.NewBest {
		t.Fatal("11 beat 10")
	}
}

func TestTitlesAndMembers(t *testing.T) {
	for lvl, want := range map[int]string{0: "egg", 1: "hatchling", 2: "hatchling", 5: "fledgling", 39: "flockkeeper", 40: "skylord", 99: "skylord"} {
		if got := Title(lvl); got != want {
			t.Errorf("Title(%d) = %s, want %s", lvl, got, want)
		}
	}
	tb := newTable(t, Standard())
	tb.run(4)
	tb.en.RestoreMember(Who{guild + 1, 9}, Member{Counts: 1})
	got := tb.en.Members(guild)
	if len(got) != 2 || got[100].Counts != 2 || got[101].Counts != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestOpenChangesRules(t *testing.T) {
	tb := newTable(t, Standard())
	tb.run(10)
	// Turning combos off keeps the run.
	r := Standard()
	r.Arcade = false
	tb.en.Open(guild, ch, r)
	if s := tb.state(); s.N != 10 || s.Run != 1 || s.Rules.Arcade {
		t.Fatalf("%+v", s)
	}
	// Changing the step restarts it.
	r.Step = 2
	tb.en.Open(guild, ch, r)
	if s := tb.state(); s.N != 0 || s.Run != 2 || s.Best != 10 {
		t.Fatalf("%+v", s)
	}
	// Moving to lives fills them.
	l, _ := Preset("lives")
	l.Step = 2
	tb.en.Open(guild, ch, l)
	if s := tb.state(); s.Lives != 3 {
		t.Fatalf("lives %+v", s)
	}
	tb.en.Close(ch)
	if v := tb.say(1, "1"); v.Kind != Ignore {
		t.Fatal("closed channel counted")
	}
	if len(tb.en.Channels()) != 0 {
		t.Fatal("channels")
	}
}

func TestStateRoundTrips(t *testing.T) {
	tb := newTable(t, Standard())
	tb.run(150)
	s := tb.state()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back State
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	en := &Engine{}
	en.Restore(ch, back)
	en.RestoreMember(Who{guild, 100}, tb.en.Member(Who{guild, 100}))
	got, _ := en.Channel(ch)
	if got.Head != s.Head || got.N != 150 || got.Bounty != s.Bounty || !got.LastAt.Equal(s.LastAt) {
		t.Fatalf("restored %+v", got)
	}
	if v := en.Submit(ch, Move{User: 101, Msg: tb.msg + 1, Text: "151", At: tb.at.Add(time.Second)}); v.Kind != Accept {
		t.Fatalf("after restore %+v", v)
	}
	if en.Member(Who{guild, 100}) != tb.en.Member(Who{guild, 100}) {
		t.Fatal("member")
	}
}

// Many channels and members at once, the race detector watching.
func TestConcurrentChannels(t *testing.T) {
	en := &Engine{}
	for c := range uint64(8) {
		en.Open(guild, c, Standard())
	}
	var wg sync.WaitGroup
	for c := range uint64(8) {
		wg.Go(func() {
			at := t0
			for i := range int64(300) {
				at = at.Add(time.Second)
				// The same two members count in every channel.
				en.Submit(c, Move{User: uint64(1 + i%2), Msg: uint64(i + 1), Text: strconv.FormatInt(i+1, 10), At: at})
			}
		})
	}
	wg.Wait()
	for c := range uint64(8) {
		if s, _ := en.Channel(c); s.N != 300 {
			t.Fatalf("channel %d at %d", c, s.N)
		}
	}
	if m := en.Member(Who{guild, 1}); m.Counts != 8*150 {
		t.Fatalf("member counts %d: lost updates across channels", m.Counts)
	}
}

func FuzzRead(f *testing.F) {
	for _, s := range []string{"42", "6*7", "(1+2)*3", "xlii", "0x2a", "fizz", "-1", "1/0", "((((1))))", "3!!/4!", "(√(6!) × √5)/2", "8^(1/3)", "Γ(7)"} {
		f.Add(s)
	}
	rules := []Ruleset{Standard(), {Math: MathOnly}, {Format: Binary}, {Format: Hex}, {Format: Roman}, {Format: FizzBuzz}}
	f.Fuzz(func(t *testing.T, s string) {
		for _, r := range rules {
			got := read(s, r.Normal())
			if got.counted && got.letters && r.Format == Roman && (got.value < 1 || got.value > 3999) {
				t.Fatalf("roman %q = %d", s, got.value)
			}
		}
	})
}

// What one count costs on the hot path, before any reaction is sent.
func BenchmarkSubmit(b *testing.B) {
	for _, text := range []string{"plain", "6*7+1-1"} {
		b.Run(text, func(b *testing.B) {
			en := &Engine{}
			en.Open(guild, ch, Standard())
			at := t0
			b.ReportAllocs()
			for i := range b.N {
				n := int64(i + 1)
				at = at.Add(time.Second)
				s := strconv.FormatInt(n, 10)
				if text != "plain" {
					s += "+0"
				}
				en.Submit(ch, Move{User: uint64(1000 + i%11), Msg: uint64(n), Text: s, At: at})
			}
		})
	}
}
