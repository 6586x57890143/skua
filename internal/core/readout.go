package core

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
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

// Report is facts for a Readout and notes for the lines below it, in
// skua's voice (UX.md). An empty one shows nothing.
type Report struct {
	Rows  [][2]string
	Notes []string
	// Controls are the module's settings for this server, set by its
	// admins: they show under the status, to admins only.
	Controls []discord.ContainerSubComponent
}

// readoutWidth holds a grid's value column to 28, so a line stays inside
// the 40 or so columns a phone shows of a code block.
const readoutWidth = 28

// Readout lays rows out as facts (DESIGN.md): a code block grid, the label
// column the longest label plus two so values line up the same on every
// client, and a value too long for its column wrapping after a comma inside
// it. A code block shows markup as typed, so a row whose value is a
// mention, timestamp or link goes under the grid as a bold-label line,
// where it renders. Ends in a newline.
func Readout(rows [][2]string) string {
	var grid, below []string
	width := 0
	for _, r := range rows {
		if !hasMarkup(r[1]) {
			width = max(width, utf8.RuneCountInString(r[0]))
		}
	}
	for _, r := range rows {
		if hasMarkup(r[1]) {
			below = append(below, "**"+r[0]+"** "+r[1])
			continue
		}
		for n, line := range wrapList(r[1], readoutWidth) {
			label := ""
			if n == 0 {
				label = r[0]
			}
			grid = append(grid, strings.TrimRight(label+strings.Repeat(" ", width+2-utf8.RuneCountInString(label))+line, " "))
		}
	}
	if len(grid) > 0 {
		below = append([]string{"```\n" + strings.Join(grid, "\n") + "\n```"}, below...)
	}
	if len(below) == 0 {
		return ""
	}
	return strings.Join(below, "\n") + "\n"
}

// hasMarkup is whether v holds something a code block would show as typed:
// a mention, emoji or timestamp (<...>) or a masked link.
func hasMarkup(v string) bool { return strings.Contains(v, "<") || strings.Contains(v, "](") }

// wrapList breaks a comma separated list into lines of at most width,
// breaking only after a comma. A single item longer than width stays whole.
func wrapList(s string, width int) []string {
	var lines []string
	line := ""
	for n, item := range strings.Split(s, ", ") {
		if n > 0 {
			item = ", " + item
		}
		if line != "" && utf8.RuneCountInString(line+item) > width {
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
