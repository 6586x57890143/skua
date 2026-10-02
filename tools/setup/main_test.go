package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapAdmin(t *testing.T) {
	cases := map[string]string{
		`{"owner":{"id":"1"}}`:                                "1",
		`{"owner":{"id":"999"},"team":{"owner_user_id":"2"}}`: "2",
		`{"team":{"owner_user_id":""},"owner":{"id":"3"}}`:    "3",
		`{}`: "",
	}
	for body, want := range cases {
		var app application
		if err := json.Unmarshal([]byte(body), &app); err != nil {
			t.Fatal(err)
		}
		if got := bootstrapAdmin(app); got != want {
			t.Errorf("%s: got %q, want %q", body, got, want)
		}
	}
}

// shq output must survive a real shell unchanged, quotes included.
func TestShq(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	for _, s := range []string{"skua", "my dir", `it's`, `a"b$c\d`} {
		out, err := exec.Command("sh", "-c", "printf %s "+shq(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("shq(%q) round-tripped to %q (%v)", s, out, err)
		}
	}
}

// runEnvScript runs envScript exactly as the host does: sh, the directory as
// $1, and the two values on stdin.
func runEnvScript(t *testing.T, home, token, admin string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", envScript, "sh", "skua")
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Stdin = strings.NewReader(token + "\n" + admin + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("envScript: %v\n%s", err, out)
	}
}

// The .env rewrite is the one place a bug silently destroys the production
// token, so it runs for real rather than being trusted.
func TestEnvScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "skua"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(home, "skua", ".env")
	// An existing token line to replace, no admin line (so it must be
	// appended), and unrelated lines that must survive byte for byte,
	// including ones that look like the keys but are not.
	before := "# comment = keep\nPOSTGRES_PASSWORD=p&ss\\w/rd$x\nDISCORD_BOT_TOKEN=old\nXDISCORD_BOT_TOKEN=not-this\n\nSKUA_LOG_LEVEL=info\n"
	if err := os.WriteFile(env, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	token := `MTA.a&b\c/d$e=f'g"h`
	want := "# comment = keep\nPOSTGRES_PASSWORD=p&ss\\w/rd$x\nDISCORD_BOT_TOKEN=" + token +
		"\nXDISCORD_BOT_TOKEN=not-this\n\nSKUA_LOG_LEVEL=info\nSKUA_BOOTSTRAP_ADMIN_USER_ID=42\n"

	for run := range 2 { // the second run must change nothing
		runEnvScript(t, home, token, "42")
		got, err := os.ReadFile(env)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("run %d:\n got %q\nwant %q", run+1, got, want)
		}
	}

	// A rotated token replaces in place rather than appending a second line.
	runEnvScript(t, home, "new", "42")
	got, _ := os.ReadFile(env)
	if strings.Count(string(got), "DISCORD_BOT_TOKEN=") != 2 || !strings.Contains(string(got), "\nDISCORD_BOT_TOKEN=new\n") {
		t.Fatalf("rotation:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(home, "skua", ".env.*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}
