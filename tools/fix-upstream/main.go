// Command fix-upstream reports upstream changes for review. It never downloads
// executable code, updates dependencies or changes the reviewed revision lock.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type upstream struct {
	Repository, Revision, License string
	Paths                         []string
}
type lock struct {
	Reviewed     string
	Repositories []upstream
}
type comparison struct {
	HTMLURL string `json:"html_url"`
	Status  string
	Total   int `json:"total_commits"`
	Files   []struct{ Filename string }
	Commits []struct {
		HTMLURL string `json:"html_url"`
		Commit  struct{ Message string }
	}
}
type pull struct {
	Number  int
	Title   string
	HTMLURL string `json:"html_url"`
}

func get(ctx context.Context, c *http.Client, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "skua-fix-upstream")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return fmt.Errorf("github: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil {
		return err
	}
	if len(raw) > 4<<20 {
		return fmt.Errorf("github: response over 4 MiB")
	}
	return json.Unmarshal(raw, out)
}
func run() error {
	file := flag.String("lock", "internal/modules/fix/upstreams.json", "reviewed source revisions")
	check := flag.Bool("check", false, "exit nonzero when reviewed sources have changed")
	flag.Parse()
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var pinned lock
	if err = json.Unmarshal(raw, &pinned); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	changed := false
	fmt.Printf("# embed upstream review\n\nreviewed: %s\n\nMetadata services update independently. Review API and renderer changes before advancing upstreams.json. No upstream code is imported automatically.\n", pinned.Reviewed)
	for _, repo := range pinned.Repositories {
		var diff comparison
		if err = get(ctx, client, repo.Repository+"/compare/"+repo.Revision+"...main", &diff); err != nil {
			return fmt.Errorf("%s comparison: %w", repo.Repository, err)
		}
		fmt.Printf("\n## %s\n\nlicense: %s; reviewed: %s\n\n[%s: %d commits](%s)\n", repo.Repository, repo.License, repo.Revision, diff.Status, diff.Total, diff.HTMLURL)
		if diff.Status != "identical" {
			changed = true
		}
		fmt.Println("\nRelevant files:")
		for _, f := range diff.Files {
			for _, prefix := range repo.Paths {
				if strings.HasPrefix(f.Filename, prefix) {
					fmt.Printf("- %s\n", f.Filename)
					break
				}
			}
		}
		fmt.Println("\nRecent commits (at most 20):")
		for i, c := range diff.Commits {
			if i == 20 {
				break
			}
			title := strings.SplitN(c.Commit.Message, "\n", 2)[0]
			fmt.Printf("- [%s](%s)\n", title, c.HTMLURL)
		}
		var prs []pull
		if err = get(ctx, client, repo.Repository+"/pulls?state=open&per_page=20", &prs); err != nil {
			return fmt.Errorf("%s contributions: %w", repo.Repository, err)
		}
		fmt.Println("\nOpen contributions (at most 20, unreviewed):")
		for _, pr := range prs {
			fmt.Printf("- [#%d %s](%s)\n", pr.Number, pr.Title, pr.HTMLURL)
		}
	}
	if *check && changed {
		return fmt.Errorf("upstream sources changed; review the report and run live canaries before advancing the lock")
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
