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
// $1, and the pairs on stdin as writeEnv sends them.
func runEnvScript(t *testing.T, home string, set ...[2]string) {
	t.Helper()
	in, err := envLines(set)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", envScript, "sh", "skua")
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Stdin = strings.NewReader(in)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("envScript: %v\n%s", err, out)
	}
}

func tokenAdmin(token, admin string) [][2]string {
	return [][2]string{{"DISCORD_BOT_TOKEN", token}, {"SKUA_BOOTSTRAP_ADMIN_USER_ID", admin}}
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
		runEnvScript(t, home, tokenAdmin(token, "42")...)
		got, err := os.ReadFile(env)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("run %d:\n got %q\nwant %q", run+1, got, want)
		}
	}

	// A rotated token replaces in place rather than appending a second line.
	runEnvScript(t, home, tokenAdmin("new", "42")...)
	got, _ := os.ReadFile(env)
	if strings.Count(string(got), "DISCORD_BOT_TOKEN=") != 2 || !strings.Contains(string(got), "\nDISCORD_BOT_TOKEN=new\n") {
		t.Fatalf("rotation:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(home, "skua", ".[ek][nv]*.*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// A fresh host has no directory and no .env: the script makes both, and the
// file it writes is everything the prod compose file reads, with the
// database URL carrying the same password Postgres is started with.
func TestEnvScriptOnAFreshHost(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	home := t.TempDir()
	pw, err := password()
	if err != nil {
		t.Fatal(err)
	}
	runEnvScript(t, home, append(tokenAdmin("tok", "42"), freshDB(pw)...)...)
	got, err := os.ReadFile(filepath.Join(home, "skua", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DISCORD_BOT_TOKEN=tok\nSKUA_BOOTSTRAP_ADMIN_USER_ID=42\nPOSTGRES_USER=skua\nPOSTGRES_PASSWORD=" + pw +
		"\nSKUA_DATABASE_URL=postgres://skua:" + pw + "@postgres:5432/skua?sslmode=disable\nSKUA_LOG_LEVEL=info\n"
	if string(got) != want {
		t.Fatalf("fresh .env:\n got %q\nwant %q", got, want)
	}
}

func TestPasswordIsURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		pw, err := password()
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != 32 || strings.Trim(pw, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			t.Fatalf("password %q is not 32 URL-safe characters", pw)
		}
		if seen[pw] {
			t.Fatal("password repeated")
		}
		seen[pw] = true
	}
}

func TestEnvLinesRefusesNewlines(t *testing.T) {
	for _, kv := range [][2]string{{"A", "x\ny"}, {"A", "x\r"}, {"A=B", "x"}, {"A\nB", "x"}} {
		if _, err := envLines([][2]string{kv}); err == nil {
			t.Errorf("envLines(%q) accepted it", kv)
		}
	}
}

func TestSSHField(t *testing.T) {
	// The shape OpenSSH 10.5 prints: "User" capitalised, the rest not.
	cfg := "User deploy\nhostname 203.0.113.7\nport 22\nidentityfile ~/.ssh/id_ed25519\n"
	if got := sshField(cfg, "hostname"); got != "203.0.113.7" {
		t.Errorf("hostname = %q", got)
	}
	if got := sshField(cfg, "user"); got != "deploy" {
		t.Errorf("user = %q", got)
	}
	if got := sshField(cfg, "proxyjump"); got != "" {
		t.Errorf("missing key = %q", got)
	}
	if got := proxied(cfg); got != "" {
		t.Errorf("direct alias reported as proxied: %q", got)
	}
	for _, c := range []string{"proxyjump bastion.example\n", "proxycommand nc %h %p\n"} {
		if proxied(cfg+c) == "" {
			t.Errorf("%q not reported", c)
		}
	}
	if got := proxied(cfg + "proxycommand none\n"); got != "" {
		t.Errorf("proxycommand none reported as %q", got)
	}
}

// TestFreshHostOverSSH runs the host side for real against a throwaway ssh
// host: SKUA_SETUP_E2E_HOST=<ssh alias with no ~/skua-e2e>. It probes,
// writes a fresh .env, probes again, and rotates the token, all over ssh.
// Skipped unless set, so CI never needs a host.
func TestFreshHostOverSSH(t *testing.T) {
	host := os.Getenv("SKUA_SETUP_E2E_HOST")
	if host == "" {
		t.Skip("SKUA_SETUP_E2E_HOST not set")
	}
	const dir = "skua-e2e"
	if fresh, err := probeHost(host, dir, false); err != nil || !fresh {
		t.Fatalf("first probe: fresh=%v err=%v; want a host without ~/%s", fresh, err, dir)
	}
	pw, err := password()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEnv(host, dir, append(tokenAdmin(`a&b\c/d$e`, "42"), freshDB(pw)...)); err != nil {
		t.Fatal(err)
	}
	if fresh, err := probeHost(host, dir, false); err != nil || fresh {
		t.Fatalf("second probe: fresh=%v err=%v; want the .env just written", fresh, err)
	}
	if err := writeEnv(host, dir, tokenAdmin("rotated", "42")); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ssh", "-o", "BatchMode=yes", host, "stat -c %a "+remoteDir(dir)+"/.env; cat "+remoteDir(dir)+"/.env; ls -A "+remoteDir(dir)).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "600\nDISCORD_BOT_TOKEN=rotated\nSKUA_BOOTSTRAP_ADMIN_USER_ID=42\nPOSTGRES_USER=skua\nPOSTGRES_PASSWORD=" + pw +
		"\nSKUA_DATABASE_URL=postgres://skua:" + pw + "@postgres:5432/skua?sslmode=disable\nSKUA_LOG_LEVEL=info\n.env\n"
	if string(out) != want {
		t.Fatalf("host after two runs:\n got %q\nwant %q", out, want)
	}
}
