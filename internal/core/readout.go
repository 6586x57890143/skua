package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// Reporter is a Module with live state worth showing its server's admins,
// on its page in /help: what it is connected to, what it has done, what
// failed. Optional, like Helper.
type Reporter interface {
	// Report is the module's state as it concerns guild, and nothing about
	// any other server. It is called on every page view, so it reads what
	// the module already keeps and makes no network call.
	Report(guild snowflake.ID) Report
}

// Report is facts for a Readout grid and notes for the lines below it, in
// skua's voice (UX.md). An empty one shows nothing.
type Report struct {
	Rows  [][2]string
	Notes []string
}

// valueWidth holds the value column to 28, so a line stays within 40.
const valueWidth = 28

// Readout lays rows out as a code block grid (UX.md): the label column is
// the longest label and two spaces, and a value too long for its column
// wraps after a comma, continuing in the value column. A monospace grid
// lines up the same on every client, where how embed fields wrap is up to
// each client.
func Readout(rows [][2]string) string {
	labels := 0
	for _, r := range rows {
		labels = max(labels, len(r[0]))
	}
	var b strings.Builder
	b.WriteString("```\n")
	for _, r := range rows {
		for i, line := range wrap(r[1], valueWidth) {
			label := ""
			if i == 0 {
				label = r[0]
			}
			fmt.Fprintf(&b, "%-*s%s\n", labels+2, label, line)
		}
	}
	b.WriteString("```\n")
	return b.String()
}

// wrap breaks a comma separated list into lines of at most width, breaking
// only after a comma. A single item longer than width stays whole and runs
// past the grid.
func wrap(s string, width int) []string {
	var lines []string
	line := ""
	for i, item := range strings.Split(s, ", ") {
		if i > 0 {
			item = ", " + item
		}
		if line != "" && len(line)+len(item) > width {
			lines = append(lines, line+",")
			item = strings.TrimPrefix(item, ", ")
			line = ""
		}
		line += item
	}
	return append(lines, line)
}

// Duration is the two largest units that matter: "45s", "12m", "3h 12m",
// "2d 4h".
func Duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}
