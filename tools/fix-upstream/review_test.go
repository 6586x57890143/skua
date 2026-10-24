package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewNeeded(t *testing.T) {
	repo := upstream{Paths: []string{"src", "*.py", "LICENSE.md"}}
	cases := []struct {
		name string
		diff comparison
		want bool
	}{
		{"unchanged", comparison{Status: "identical"}, false},
		{"docs", comparison{Status: "ahead", Total: 1, Files: []changedFile{{Filename: "README.md"}}}, false},
		{"prefix collision", comparison{Status: "ahead", Files: []changedFile{{Filename: "src-other/a.go"}}}, false},
		{"extractor", comparison{Status: "ahead", Files: []changedFile{{Filename: "src/twitter.ts"}}}, true},
		{"license", comparison{Status: "ahead", Files: []changedFile{{Filename: "LICENSE.md"}}}, true},
		{"new extractor", comparison{Status: "ahead", Files: []changedFile{{Filename: "new_platform_embed.py"}}}, true},
		{"renamed extractor", comparison{Status: "ahead", Files: []changedFile{{Filename: "other/parser.txt", Previous: "twitter_embed.py"}}}, true},
		{"nested unrelated python", comparison{Status: "ahead", Files: []changedFile{{Filename: "scripts/tool.py"}}}, false},
		{"exact file", comparison{Status: "ahead", Files: []changedFile{{Filename: "reddit_embed.py"}}}, true},
		{"renamed away", comparison{Status: "ahead", Files: []changedFile{{Filename: "other/parser.ts", Previous: "src/parser.ts"}}}, true},
		{"rewritten", comparison{Status: "diverged"}, true},
		{"unknown", comparison{}, true},
		{"missing files", comparison{Status: "ahead", Total: 1}, true},
		{"file cap", comparison{Status: "ahead", Files: make([]changedFile, 300)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, reason := reviewNeeded(repo, tc.diff)
			if (reason != "") != tc.want {
				t.Fatalf("review reason = %q", reason)
			}
		})
	}
}

func TestSourceSnapshot(t *testing.T) {
	sha := strings.Repeat("a", 40)
	repo := upstream{Repository: "FxEmbed/FxEmbed", Revision: sha, License: "MIT"}
	got, err := sourceSnapshot(repo, sha)
	if err != nil || got.ObservedURL != "https://codeload.github.com/FxEmbed/FxEmbed/tar.gz/"+sha || got.License != "MIT" {
		t.Fatalf("%+v, %v", got, err)
	}
	for _, bad := range []string{"", "../evil", "repo/name/extra", "repo/$(env)"} {
		repo.Repository = bad
		if _, err := sourceSnapshot(repo, sha); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	repo.Repository = "FxEmbed/FxEmbed"
	if _, err := sourceSnapshot(repo, "main"); err == nil {
		t.Fatal("accepted floating revision")
	}
	repo.Revision = "main"
	if _, err := sourceSnapshot(repo, sha); err == nil {
		t.Fatal("accepted floating lock")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGet(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	for _, tc := range []struct {
		name, body   string
		code         int
		transportErr bool
		wantErr      bool
	}{
		{"ok", `{"status":"ahead"}`, 200, false, false},
		{"refused", "secret response", 403, false, true},
		{"bad json", "{", 200, false, true},
		{"oversize", strings.Repeat("x", (4<<20)+1), 200, false, true},
		{"network", "", 0, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("User-Agent") != "skua-fix-upstream" {
					t.Fatal("missing headers")
				}
				if tc.transportErr {
					return nil, errors.New("network")
				}
				return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			var got comparison
			err := get(context.Background(), client, "test/repo", &got)
			if (err != nil) != tc.wantErr {
				t.Fatalf("get: %v", err)
			}
		})
	}
	if err := get(context.Background(), http.DefaultClient, "\n", nil); err == nil {
		t.Fatal("accepted invalid request URL")
	}
}

func TestRun(t *testing.T) {
	// run uses flags and the default transport. Restore both before other tests.
	oldFlags, oldArgs, oldTransport, oldOut := flag.CommandLine, os.Args, http.DefaultTransport, os.Stdout
	t.Cleanup(func() {
		flag.CommandLine, os.Args, http.DefaultTransport, os.Stdout = oldFlags, oldArgs, oldTransport, oldOut
	})
	dir := t.TempDir()
	lockFile := filepath.Join(dir, "lock.json")
	lockJSON := `{"reviewed":"2026-10-23","repositories":[{"repository":"FxEmbed/FxEmbed","revision":"` + strings.Repeat("a", 40) + `","license":"MIT","paths":["src"]}]}`
	if err := os.WriteFile(lockFile, []byte(lockJSON), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	os.Stdout = out
	for _, tc := range []struct {
		name, mode, path, fail string
		wantErr                bool
	}{
		{"important", "-check-relevant", "src/twitter.ts", "", true},
		{"docs ignored", "-check-relevant", "README.md", "", false},
		{"all drift", "-check", "README.md", "", true},
		{"report", "", "src/twitter.ts", "", false},
		{"head failure", "", "", "/commits/", true},
		{"compare failure", "", "", "/compare/", true},
		{"contributions failure", "", "", "/pulls", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sourceFile := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".json")
			flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
			os.Args = []string{"fix-upstream", "-lock", lockFile, "-sources", sourceFile}
			if tc.mode != "" {
				os.Args = append(os.Args, tc.mode)
			}
			http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if tc.fail != "" && strings.Contains(r.URL.Path, tc.fail) {
					return nil, errors.New("unavailable")
				}
				body := `[]`
				switch {
				case strings.Contains(r.URL.Path, "/commits/"):
					body = `{"sha":"` + strings.Repeat("b", 40) + `"}`
				case strings.Contains(r.URL.Path, "/compare/"):
					if !strings.HasSuffix(r.URL.Path, strings.Repeat("b", 40)) {
						t.Fatal("comparison raced branch head")
					}
					body = `{"status":"ahead","total_commits":1,"files":[{"filename":"` + tc.path + `"}],"commits":[{"commit":{"message":"fix platform"}}]}`
				case strings.Contains(r.URL.Path, "/pulls"):
					body = `[{"number":1,"title":"proposed fix"}]`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			err := run()
			if (err != nil) != tc.wantErr {
				t.Fatalf("run: %v", err)
			}
			if tc.fail != "/commits/" {
				raw, err := os.ReadFile(sourceFile)
				if err != nil || !strings.Contains(string(raw), strings.Repeat("b", 40)) {
					t.Fatalf("source manifest missing after drift: %s %v", raw, err)
				}
			}
		})
	}
}

func TestReviewBranch(t *testing.T) {
	if reviewBranch(upstream{}) != "main" || reviewBranch(upstream{Branch: "master"}) != "master" {
		t.Fatal("branch selection")
	}
}
