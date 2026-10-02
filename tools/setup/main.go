// Command setup takes skua from "the pipeline is green" to "the bot is in my
// server" in one run, and is safe to run again at any point:
//
//	go run ./tools/setup
//
// It asks for the bot token (hidden), checks it against Discord, picks the
// bootstrap admin (the application's owner unless -admin says otherwise),
// writes both into .env on the host, gives the bot its avatar and banner,
// reports the privileged intents the portal has on, switches the GitHub
// deploy on, runs it, waits for the bot to say it is up, and prints the
// invite link.
//
// The token never appears in a command line, here or on the host: it goes
// to Discord in a header, to the host over ssh's stdin, and into the file
// through the environment.
package main

import (
	"bufio"
	"bytes"
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

// invitePerms is what skua's modules need in a channel: View Channel, Send
// Messages, Embed Links, Attach Files (the mood thumbnails). A module that
// needs more adds its bit here in the same PR.
const invitePerms = 1<<10 | 1<<11 | 1<<14 | 1<<15

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
	if out, err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", host, "test -f "+remoteDir(dir)+"/.env && echo ok").CombinedOutput(); err != nil || !strings.Contains(string(out), "ok") {
		return fmt.Errorf("cannot reach ~/%s/.env on %s (%s)", dir, host, strings.TrimSpace(string(out)))
	}
	ok("ssh %s, gh", host)

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
	if err := writeEnv(host, dir, token, admin); err != nil {
		return err
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
	fmt.Printf("    https://discord.com/oauth2/authorize?client_id=%s&scope=bot+applications.commands&permissions=%d\n", app.ID, invitePerms)
	if !app.BotPublic {
		fmt.Println("    The app is private, so only its owner can add it, which is the right default for a test bed.")
	}
	fmt.Println("\n  Then run /status in the server. Commands register the moment skua sees the guild.")
	return nil
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

// envScript sets DISCORD_BOT_TOKEN and SKUA_BOOTSTRAP_ADMIN_USER_ID in
// "$HOME/$1/.env", reading the two values from its stdin. Existing lines
// are replaced, missing ones appended, everything else left byte for byte.
// The values reach awk through ENVIRON, so they are never argv and never
// interpreted: & \ / $ = in a token all come out literally. A temp file
// and mv keep the file whole if anything fails, under umask 077.
const envScript = `set -eu
cd "$HOME/$1"
IFS= read -r TOK; IFS= read -r ADM; export TOK ADM
umask 077
tmp=$(mktemp .env.XXXXXX)
awk 'BEGIN { t = 0; a = 0 }
  /^DISCORD_BOT_TOKEN=/            { print "DISCORD_BOT_TOKEN=" ENVIRON["TOK"]; t = 1; next }
  /^SKUA_BOOTSTRAP_ADMIN_USER_ID=/ { print "SKUA_BOOTSTRAP_ADMIN_USER_ID=" ENVIRON["ADM"]; a = 1; next }
  { print }
  END {
    if (!t) print "DISCORD_BOT_TOKEN=" ENVIRON["TOK"]
    if (!a) print "SKUA_BOOTSTRAP_ADMIN_USER_ID=" ENVIRON["ADM"]
  }' .env > "$tmp"
mv "$tmp" .env`

// writeEnv sets the two values in the host's .env, replacing existing lines
// or appending missing ones, and leaves everything else in the file alone.
// Values travel on stdin and reach awk through the environment, so neither
// shows in a process listing.
func writeEnv(host, dir, token, admin string) error {
	return writeEnvVia(host, dir, envScript, token, admin)
}

// writeEnvVia puts the script in a file first and then runs it with the
// values on stdin: two ssh calls, but neither has to share its stdin between
// a program and that program's input.
func writeEnvVia(host, dir, script, token, admin string) error {
	put := exec.Command("ssh", "-o", "BatchMode=yes", host, "umask 077; cat > "+remoteDir(dir)+"/.setup.sh")
	put.Stdin = strings.NewReader(script + "\n")
	if out, err := put.CombinedOutput(); err != nil {
		return fmt.Errorf("copying the setup script: %v %s", err, out)
	}
	runIt := exec.Command("ssh", "-o", "BatchMode=yes", host, "sh "+remoteDir(dir)+"/.setup.sh "+shq(dir)+"; s=$?; rm -f "+remoteDir(dir)+"/.setup.sh; exit $s")
	runIt.Stdin = strings.NewReader(token + "\n" + admin + "\n")
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
