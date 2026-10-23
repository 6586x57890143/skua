package main

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

type changedFile struct {
	Filename string
	Previous string `json:"previous_filename"`
}

type snapshot struct {
	Repository  string `json:"repository"`
	License     string `json:"license"`
	Reviewed    string `json:"reviewed"`
	Observed    string `json:"observed"`
	ReviewedURL string `json:"reviewed_url"`
	ObservedURL string `json:"observed_url"`
}

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+/[A-Za-z0-9_.-]+$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func sourceSnapshot(repo upstream, head string) (snapshot, error) {
	if !repositoryPattern.MatchString(repo.Repository) || !revisionPattern.MatchString(repo.Revision) || !revisionPattern.MatchString(head) {
		return snapshot{}, fmt.Errorf("invalid source repository or revision")
	}
	base := "https://codeload.github.com/" + repo.Repository + "/tar.gz/"
	return snapshot{repo.Repository, repo.License, repo.Revision, head, base + repo.Revision, base + head}, nil
}

func watched(name string, paths []string) bool {
	for _, pattern := range paths {
		pattern = strings.TrimSuffix(pattern, "/")
		match, _ := path.Match(pattern, name)
		if match || strings.HasPrefix(name, pattern+"/") {
			return true
		}
	}
	return false
}

// GitHub caps comparison files at 300, even when commits are paginated.
// A truncated list or rewritten branch must never look like a clean review.
func reviewNeeded(repo upstream, diff comparison) ([]string, string) {
	var files []string
	for _, file := range diff.Files {
		if watched(file.Filename, repo.Paths) || (file.Previous != "" && watched(file.Previous, repo.Paths)) {
			files = append(files, file.Filename)
		}
	}
	switch {
	case diff.Status == "identical":
		return files, ""
	case diff.Status != "ahead":
		return files, "upstream history changed or comparison status is unknown; inspect the complete diff"
	case len(diff.Files) >= 300:
		return files, "GitHub's 300-file comparison limit may hide platform changes; inspect the complete diff"
	case diff.Total > 0 && len(diff.Files) == 0:
		return files, "changed history has no file summary; inspect the complete diff"
	case len(files) > 0:
		return files, "extraction, API, dependencies or license files changed; review before advancing the lock"
	default:
		return files, ""
	}
}
