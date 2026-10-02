// Command setup takes skua from "the pipeline is green" to "the bot is in my
// server" in one run, and is safe to run again at any point:
//
//	go run ./tools/setup
//
// It asks for the bot token (hidden), checks it against Discord, picks the
// bootstrap admin (the application's owner unless -admin says otherwise),
// writes both into .env on the host (creating it, with a generated database
// password, on a fresh host), gives the bot its avatar and banner, reports
// the privileged intents the portal has on, points the GitHub deploy at the
// host and switches it on, runs it, waits for the bot to say it is up, and
// prints the invite link. Pointing it at a new ssh alias is how skua moves
// to another server.
//
// No secret appears in a command line, here or on the host: the token goes
// to Discord in a header, and every value to the host over ssh's stdin, into
// a umask 077 file that awk reads.
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"
)

const api = "https://discord.com/api/v10"

// inviteURL is web/'s Worker, which redirects to Discord's bare install
// link. That link asks for the app's Default Install Settings, which skua
// itself writes at every boot from what its running modules declare.
const inviteURL = "https://skua.melting.lol"

type application struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Flags int64  `json:"flags"`
	Owner *struct {
		ID string `json:"id"`
	} `json:"owner"`
	Team *struct {
		OwnerUserID string `json:"owner_user_id"`
	} `json:"team"`
	BotPublic bool `json:"bot_public"`
}

func main() {
	host := flag.String("host", "foundry-deploy", "ssh alias of the deploy host")
	dir := flag.String("dir", "skua", "directory on the host, relative to the deploy user's home")
	repo := flag.String("repo", "6586x57890143/skua", "GitHub repository")
	admin := flag.String("admin", "", "bootstrap admin user ID (default: the application's owner)")
	noProfile := flag.Bool("no-profile", false, "leave the bot's avatar and banner alone")
	noDeploy := flag.Bool("no-deploy", false, "write the host's .env but do not deploy")
	flag.Parse()

	if err := run(*host, *dir, *repo, *admin, *noProfile, *noDeploy); err != nil {
		fmt.Fprintln(os.Stderr, "\n✗", err)
		os.Exit(1)
	}
}

func run(host, dir, repo, admin string, noProfile, noDeploy bool) error {
	step("checking tools")
	for _, tool := range []string{"ssh", "gh"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s is not on PATH", tool)
		}
	}
	fresh, err := probeHost(host, dir, true)
	if err != nil {
		return err
	}
	ok("ssh %s, docker compose, gh", host)

	token, err := readToken()
	if err != nil {
		return err
	}

	step("checking the token with Discord")
	var app application
	if err := discord(token, http.MethodGet, "/applications/@me", nil, &app); err != nil {
		return err
	}
	ok("%s (application %s)", app.Name, app.ID)

	if admin == "" {
		if admin = bootstrapAdmin(app); admin == "" {
			return errors.New("could not tell who owns the application; pass -admin <your user ID>")
		}
	}
	ok("bootstrap admin %s", admin)

	step("writing " + host + ":~/" + dir + "/.env")
	set := [][2]string{{"DISCORD_BOT_TOKEN", token}, {"SKUA_BOOTSTRAP_ADMIN_USER_ID", admin}}
	if fresh {
		// Only for a new .env: Postgres reads its password once, when the
		// volume is first created, so changing it later locks the bot out.
		pw, err := password()
		if err != nil {
			return err
		}
		set = append(set, freshDB(pw)...)
	}
	if err := writeEnv(host, dir, set); err != nil {
		return err
	}
	if fresh {
		ok("new .env with a generated database password")
	}
	ok("DISCORD_BOT_TOKEN and SKUA_BOOTSTRAP_ADMIN_USER_ID set")

	if !noProfile {
		step("avatar and banner")
		if err := profile(token); err != nil {
			// Discord rate limits avatar changes hard; a re-run within the
			// hour hitting it is expected and nothing else depends on it.
			warn("%v (skipped; rerun later or with -no-profile)", err)
		} else {
			ok("set from art/")
		}
	}

	step("privileged intents in the Developer Portal")
	for _, p := range []struct {
		name      string
		on, onLim int64
	}{
		{"Presence", 1 << 12, 1 << 13},
		{"Server Members", 1 << 14, 1 << 15},
		{"Message Content", 1 << 18, 1 << 19},
	} {
		state := "off"
		if app.Flags&(p.on|p.onLim) != 0 {
			state = "on"
		}
		fmt.Printf("    %-16s %s\n", p.name, state)
	}
	fmt.Printf("    No current module requires one. skua only asks for what is on, and\n    restarts itself within 10 minutes of a toggle: https://discord.com/developers/applications/%s/bot\n", app.ID)

	if !noDeploy {
		if err := deploy(repo, host, dir); err != nil {
			return err
		}
	}

	step("invite")
	fmt.Printf("    %s  (redirects to https://discord.com/oauth2/authorize?client_id=%s)\n", inviteURL, app.ID)
	if !app.BotPublic {
		fmt.Println("    The app is private, so only its owner can add it, which is the right default for a test bed.")
	}
	fmt.Println("\n  Then run /status in the server. Commands register the moment skua sees the guild.")
	return nil
}

// probeHost is one round trip: the host answers, has Docker Compose when
// needCompose, and either has ~/<dir>/.env or is fresh.
func probeHost(host, dir string, needCompose bool) (fresh bool, err error) {
	probe := "test -f " + remoteDir(dir) + "/.env && echo have-env; echo reached"
	if needCompose {
		probe = "docker compose version >/dev/null 2>&1 || echo no-compose; " + probe
	}
	out, err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", host, probe).CombinedOutput()
	switch {
	case err != nil || !strings.Contains(string(out), "reached"):
		return false, fmt.Errorf("cannot reach %s over ssh (%s)", host, strings.TrimSpace(string(out)))
	case strings.Contains(string(out), "no-compose"):
		return false, fmt.Errorf("docker compose is not installed on %s", host)
	}
	return !strings.Contains(string(out), "have-env"), nil
}

// bootstrapAdmin is who owns the application: the team's owner when a team
// does, since app.Owner is then the team's pseudo-user, otherwise the owner.
func bootstrapAdmin(app application) string {
	switch {
	case app.Team != nil && app.Team.OwnerUserID != "":
		return app.Team.OwnerUserID
	case app.Owner != nil:
		return app.Owner.ID
	}
	return ""
}

func readToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN")); t != "" {
		return strings.TrimPrefix(t, "Bot "), nil
	}
	fmt.Print("\n  Bot token (Developer Portal > Bot > Reset Token; input hidden): ")
	var raw []byte
	var err error
	if term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
	} else {
		raw, err = bufio.NewReader(os.Stdin).ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			err = nil
		}
	}
	if err != nil {
		return "", err
	}
	t := strings.TrimPrefix(strings.TrimSpace(string(raw)), "Bot ")
	if t == "" {
		return "", errors.New("no token given")
	}
	return t, nil
}

func discord(token, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, api+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/6586x57890143/skua, setup)")
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return errors.New("the token was rejected by Discord (401); copy it again from the portal")
	case res.StatusCode >= 300:
		return fmt.Errorf("%s %s: %s %s", method, path, res.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// envScript sets KEY=VALUE lines in "$HOME/$1/.env", reading them from its
// stdin. Existing lines for those keys are replaced, missing ones appended,
// everything else left byte for byte. A missing directory or .env is
// created, which is what a fresh host has. The pairs go to a file and awk
// splits each at its first "=" with substr, so values are never argv and
// never interpreted: & \ / $ = in a token all come out literally. A temp
// file and mv keep the file whole if anything fails, under umask 077.
const envScript = `set -eu
umask 077
mkdir -p "$HOME/$1"
cd "$HOME/$1"
[ -f .env ] || : > .env
kv=$(mktemp .kv.XXXXXX)
tmp=$(mktemp .env.XXXXXX)
trap 'rm -f "$kv"' EXIT
cat > "$kv"
awk 'NR == FNR { i = index($0, "="); k = substr($0, 1, i - 1); v[k] = substr($0, i + 1); order[++n] = k; next }
  { i = index($0, "="); k = substr($0, 1, i - 1)
    if (i > 1 && (k in v)) { print k "=" v[k]; done[k] = 1; next }
    print }
  END { for (j = 1; j <= n; j++) if (!(order[j] in done)) print order[j] "=" v[order[j]] }' "$kv" .env > "$tmp"
mv "$tmp" .env`

// writeEnv sets each key to its value in the host's .env, replacing
// existing lines or appending missing ones, and leaves everything else in
// the file alone. Values travel on stdin, so none shows in a process
// listing.
func writeEnv(host, dir string, set [][2]string) error {
	in, err := envLines(set)
	if err != nil {
		return err
	}
	return writeEnvVia(host, dir, envScript, in)
}

// envLines is set as envScript's stdin. A newline in a value would split it
// into a second line, so it is refused rather than written.
func envLines(set [][2]string) (string, error) {
	var b strings.Builder
	for _, kv := range set {
		if strings.ContainsAny(kv[0], "=\n\r") || strings.ContainsAny(kv[1], "\n\r") {
			return "", fmt.Errorf("%s: a key or value with a newline or a = in the key cannot go in .env", kv[0])
		}
		b.WriteString(kv[0] + "=" + kv[1] + "\n")
	}
	return b.String(), nil
}

// password is 32 random characters from the URL-safe base64 alphabet, so it
// needs no escaping in .env or in the database URL.
func password() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// freshDB is what a new .env needs for docker-compose.prod.yml's Postgres,
// with SKUA_DATABASE_URL matching the password by construction.
func freshDB(pw string) [][2]string {
	return [][2]string{
		{"POSTGRES_USER", "skua"},
		{"POSTGRES_PASSWORD", pw},
		{"SKUA_DATABASE_URL", "postgres://skua:" + pw + "@postgres:5432/skua?sslmode=disable"},
		{"SKUA_LOG_LEVEL", "info"},
	}
}

// writeEnvVia puts the script in a file first and then runs it with the
// values on stdin: two ssh calls, but neither has to share its stdin between
// a program and that program's input.
func writeEnvVia(host, dir, script, in string) error {
	put := exec.Command("ssh", "-o", "BatchMode=yes", host, "umask 077; mkdir -p "+remoteDir(dir)+"; cat > "+remoteDir(dir)+"/.setup.sh")
	put.Stdin = strings.NewReader(script + "\n")
	if out, err := put.CombinedOutput(); err != nil {
		return fmt.Errorf("copying the setup script: %v %s", err, out)
	}
	runIt := exec.Command("ssh", "-o", "BatchMode=yes", host, "sh "+remoteDir(dir)+"/.setup.sh "+shq(dir)+"; s=$?; rm -f "+remoteDir(dir)+"/.setup.sh; exit $s")
	runIt.Stdin = strings.NewReader(in)
	if out, err := runIt.CombinedOutput(); err != nil {
		return fmt.Errorf("updating .env: %v %s", err, out)
	}
	return nil
}

func profile(token string) error {
	avatar, err := dataURI("art/skua_pfp.png")
	if err != nil {
		return err
	}
	banner, err := dataURI("art/skua_banner.png")
	if err != nil {
		return err
	}
	if err := discord(token, http.MethodPatch, "/users/@me", map[string]string{"avatar": avatar, "banner": banner}, nil); err != nil {
		return err
	}
	return discord(token, http.MethodPatch, "/applications/@me", map[string]string{"icon": avatar}, nil)
}

func dataURI(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%w (run from the repository root)", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b), nil
}

// deploy switches the hold off, runs CI on main by hand, and waits until the
// bot on the host logs that it is up.
func deploy(repo, host, dir string) error {
	step("deploying through GitHub Actions")
	if err := pointCI(repo, host); err != nil {
		return err
	}
	if err := gh("variable", "set", "DEPLOY_ENABLED", "--body", "true", "-R", repo); err != nil {
		return err
	}
	ok("DEPLOY_ENABLED=true")

	since := time.Now().UTC().Add(-5 * time.Second)
	if err := gh("workflow", "run", "ci.yml", "--ref", "main", "-R", repo); err != nil {
		return err
	}
	var id string
	for range 30 {
		out, err := exec.Command("gh", "run", "list", "-R", repo, "-w", "CI", "-e", "workflow_dispatch", "-L", "1",
			"--json", "databaseId,createdAt", "-q", ".[] | select(.createdAt >= \""+since.Format(time.RFC3339)+"\") | .databaseId").Output()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			id = strings.TrimSpace(string(out))
			break
		}
		time.Sleep(2 * time.Second)
	}
	if id == "" {
		return errors.New("the dispatched run never appeared; check the Actions tab")
	}
	fmt.Printf("    run https://github.com/%s/actions/runs/%s\n", repo, id)
	watch := exec.Command("gh", "run", "watch", id, "-R", repo, "--exit-status", "--interval", "10")
	watch.Stdout, watch.Stderr = io.Discard, os.Stderr
	if err := watch.Run(); err != nil {
		return fmt.Errorf("the deploy run failed: gh run view %s -R %s --log-failed", id, repo)
	}
	ok("CI and deploy passed")

	step("waiting for skua to come up on " + host)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("ssh", "-o", "BatchMode=yes", host,
			"cd "+remoteDir(dir)+" && docker compose -f docker-compose.prod.yml logs --since 3m bot 2>&1 | tail -n 50").Output()
		logs := string(out)
		switch {
		case strings.Contains(logs, "skua is up"):
			ok("connected to the gateway")
			return nil
		case strings.Contains(logs, "skua stopped"):
			return fmt.Errorf("skua exited on start:\n%s", logs)
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("no \"skua is up\" within 90s; ssh %s 'cd %s && docker compose -f docker-compose.prod.yml logs bot'", host, dir)
}

// shq single-quotes s for a remote shell.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// remoteDir is the host directory as a remote shell expression.
func remoteDir(dir string) string { return `"$HOME"/` + shq(dir) }

// pointCI makes the repository secret VPS_HOST the machine behind the ssh
// alias, so moving to a new host is this tool plus the CI key, not a trip
// to the repository settings. VPS_SSH_KEY stays with the person: a private
// key never passes through here.
func pointCI(repo, alias string) error {
	cfg, err := exec.Command("ssh", "-G", alias).Output()
	if err != nil {
		return fmt.Errorf("ssh -G %s: %v", alias, err)
	}
	hostname, user := sshField(string(cfg), "hostname"), sshField(string(cfg), "user")
	if hostname == "" {
		return fmt.Errorf("ssh -G %s names no hostname", alias)
	}
	set := exec.Command("gh", "secret", "set", "VPS_HOST", "-R", repo)
	set.Stdin = strings.NewReader(hostname)
	if out, err := set.CombinedOutput(); err != nil {
		return fmt.Errorf("gh secret set VPS_HOST: %v %s", err, strings.TrimSpace(string(out)))
	}
	ok("VPS_HOST=%s", hostname)
	names, err := exec.Command("gh", "secret", "list", "-R", repo, "--json", "name", "-q", ".[].name").Output()
	if err != nil || !strings.Contains("\n"+string(names), "\nVPS_SSH_KEY\n") {
		warn("VPS_SSH_KEY is not set: add the CI deploy key in the repository settings")
	}
	// The workflow logs in as deploy.
	if user != "deploy" {
		warn("%s logs in as %q, but CI deploys as \"deploy\"", alias, user)
	}
	return nil
}

// sshField is one setting from `ssh -G` output, which is "key value" lines.
func sshField(cfg, key string) string {
	for _, line := range strings.Split(cfg, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), " "); ok && k == key {
			return v
		}
	}
	return ""
}

func gh(args ...string) error {
	out, err := exec.Command("gh", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh %s: %v %s", strings.Join(args[:2], " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func step(s string)           { fmt.Printf("\n▸ %s\n", s) }
func ok(f string, a ...any)   { fmt.Printf("    ✓ "+f+"\n", a...) }
func warn(f string, a ...any) { fmt.Printf("    ! "+f+"\n", a...) }
