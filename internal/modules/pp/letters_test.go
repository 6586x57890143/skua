package pp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// The letter's words are LORE.md's, word for word.
func TestLetterTextIsLORE(t *testing.T) {
	b, err := os.ReadFile("../../../LORE.md")
	if err != nil {
		t.Fatal(err)
	}
	md := strings.Join(strings.Fields(string(b)), " ")
	for _, p := range slices.Concat(letterSealedText, letterOpenText) {
		if !strings.Contains(md, p) {
			t.Errorf("%q isn't LORE.md's", p)
		}
	}
}

// Codes are pasted one a line, trimmed, each once, and bounded.
func TestParseCodes(t *testing.T) {
	got, err := parseCodes(" AAAA-1 \n\nBBBB-2\r\nAAAA-1\n")
	if err != nil || strings.Join(got, ",") != "AAAA-1,BBBB-2" {
		t.Errorf("parsed %q, %v", got, err)
	}
	for _, bad := range []string{"\n \n", strings.Repeat("x", codeMost+1), strings.Repeat("a\n", 0) + manyCodes(mostLetters+1)} {
		if _, err := parseCodes(bad); !errors.Is(err, errLetterCodes) {
			t.Errorf("%.20q: %v", bad, err)
		}
	}
	if last4("ab") != "ab" || last4("abcdef") != "cdef" {
		t.Error("last4")
	}
}

func manyCodes(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(snowflake.ID(i + 1).String())
		b.WriteByte('\n')
	}
	return b.String()
}

// letterGuild is a fresh guild with no letters in db.
func letterGuild(t *testing.T, db DB, g snowflake.ID) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `delete from pp_letter where guild_id = $1`, int64(g)); err != nil {
		t.Fatal(err)
	}
}

// Concurrent claims hand each sealed code to one bird, a bird one code,
// and nothing once they're gone.
func TestClaimLetterFirstN(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	const g = 9301
	letterGuild(t, db, g)
	if _, err := db.Exec(ctx, `insert into pp_letter (guild_id, code) values ($1, 'c1'), ($1, 'c2'), ($1, 'c3')`, g); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	won := map[string]snowflake.ID{}
	var wg sync.WaitGroup
	for u := range 8 {
		wg.Go(func() {
			code, err := claimLetter(ctx, db, g, snowflake.ID(u+1), 1)
			if err != nil {
				t.Error(err)
			}
			if code != "" {
				mu.Lock()
				if _, twice := won[code]; twice {
					t.Errorf("%s went twice", code)
				}
				won[code] = snowflake.ID(u + 1)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(won) != 3 {
		t.Errorf("won %v", won)
	}
	// A bird holding one can't take a second.
	if _, err := db.Exec(ctx, `insert into pp_letter (guild_id, code) values ($1, 'c4')`, g); err != nil {
		t.Fatal(err)
	}
	if _, err := claimLetter(ctx, db, g, won["c1"], 2); err == nil {
		t.Error("a second letter to one bird")
	}
}

// The admins load codes; the loop sends a letter to each member with ten
// full rings, most rings first, while codes last; the winner breaks the
// seal and reads the code; and a load reads it all back. No code reaches
// a log line, the ledger or the help page whole.
func TestSealedLetters(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	letterGuild(t, db, 3)
	m := testModule(db)
	var logs bytes.Buffer
	m.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := m.st.server(3)
	today := DayOf(m.now())
	rings := map[snowflake.ID]int64{old(1): 10, old(2): 12, old(3): 9, old(4): 11}
	for u, n := range rings {
		m.st.member(s, 3, u, today).signs.rings = n
	}
	m.sendLetters(ctx) // nothing sealed yet
	if len(s.letters) != 0 {
		t.Fatal("a letter with no codes")
	}

	const secret = "STEAM-SECRET-0001"
	load := func(codes string) (string, error) {
		e, sent := coretest.Modal(t, letterForm, map[string]string{"codes": codes}, nil)
		if err := m.submitLetters(ctx, e); err != nil {
			return "", err
		}
		return js(*sent), nil
	}
	got, err := load(secret + "\nSTEAM-SECRET-0002\n")
	if err != nil || !strings.Contains(got, "2 sealed") || strings.Contains(got, secret) {
		t.Fatalf("loading: %s, %v", got, err)
	}
	if got, _ := load(secret); !strings.Contains(got, "1 were loaded before") {
		t.Errorf("loading again: %s", got)
	}
	m.sendLetters(ctx)
	// Two codes: old(2) with 12 and old(4) with 11; old(1) is too late and
	// old(3) hasn't earned one.
	if len(s.letters) != 2 || s.letters[old(2)] == nil || s.letters[old(4)] == nil || s.sealed != 0 {
		t.Fatalf("letters %v, %d sealed", s.letters, s.sealed)
	}
	if len(s.pending) != 0 {
		t.Error("a letter wrote the ledger")
	}
	m.sendLetters(ctx) // none left: nothing more, nothing broken
	if len(s.letters) != 2 {
		t.Error("a third letter")
	}

	// The help page says who holds one by its last four only.
	help := js(m.keyControls(3))
	if !strings.Contains(help, "**sealed** 0") || !strings.Contains(help, old(2).String()) || strings.Contains(help, "STEAM-SECRET") {
		t.Errorf("help: %s", help)
	}

	press := func(id string, who snowflake.ID) (string, error) {
		e, sent := coretest.Button(t, id, func(p map[string]any) {
			p["member"].(map[string]any)["user"] = map[string]any{"id": who.String(), "username": "member"}
		})
		if err := m.press(ctx, e); err != nil {
			return "", err
		}
		return js(*sent), nil
	}
	if _, err := press("pp:letter", old(1)); !errors.Is(err, errNoLetter) {
		t.Errorf("a letter for one with none: %v", err)
	}
	code := s.letters[old(2)].code
	sealed, err := press("pp:letter", old(2))
	if err != nil || strings.Contains(sealed, code) || !strings.Contains(sealed, "pp:seal") {
		t.Fatalf("sealed: %s, %v", sealed, err)
	}
	open, err := press("pp:seal", old(2))
	if err != nil || !strings.Contains(open, code) || strings.Contains(open, "pp:seal") {
		t.Fatalf("open: %s, %v", open, err)
	}
	if strings.Contains(logs.String(), "STEAM-SECRET") {
		t.Errorf("a code in the logs: %s", logs.String())
	}

	back := newState(&Default, 42)
	if err := back.loadLetters(ctx, db); err != nil {
		t.Fatal(err)
	}
	bs := back.server(3)
	if l := bs.letters[old(2)]; l == nil || !l.opened || l.code != code || bs.letters[old(4)].opened || bs.sealed != 0 {
		t.Errorf("loaded %v, %d sealed", bs.letters, bs.sealed)
	}
}

// Only admins load codes, and a server's codes are bounded.
func TestLoadLettersGate(t *testing.T) {
	m := testModule(&fakeDB{})
	ctx := context.Background()
	m.st.server(3).sealed = mostLetters
	e, _ := coretest.Modal(t, letterForm, map[string]string{"codes": "a"}, nil)
	if err := m.submitLetters(ctx, e); err == nil || !strings.Contains(err.Error(), "room for 0") {
		t.Errorf("past the bound: %v", err)
	}
	m.Admin = func(discord.Interaction) bool { return false }
	if err := m.submitLetters(ctx, e); !errors.Is(err, errNotAdmin) {
		t.Errorf("a member loaded codes: %v", err)
	}
	b, _ := coretest.Button(t, "pp:letters", nil)
	if err := m.press(ctx, b); !errors.Is(err, errNotAdmin) {
		t.Errorf("a member opened the form: %v", err)
	}
}

// The menu offers the letter to whoever holds one, lit until it's opened.
func TestLetterMenu(t *testing.T) {
	rows := js(menuRows("bard", false, "pp:status", true, true))
	if !strings.Contains(rows, "pp:letter") || !strings.Contains(rows, `"style":1`) {
		t.Errorf("sealed: %s", rows)
	}
	if rows := js(menuRows("bard", false, "pp:status", false, false)); strings.Contains(rows, "pp:letter") {
		t.Errorf("none: %s", rows)
	}
}

// Both of the letter's screens draw.
func TestLetterScreen(t *testing.T) {
	realScreens(t)
	for _, opened := range []bool{false, true} {
		pic, err := LetterScreen(opened)
		if err != nil || len(pic) == 0 {
			t.Errorf("opened %v: %v", opened, err)
		}
	}
}
