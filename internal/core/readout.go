package core

import (
	"fmt"
	"strings"
	"time"

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

// Readout lays rows out as facts (DESIGN.md), one line each with its label
// in bold and an empty value left off. Plain text, so a value wraps where
// the client needs it to and a mention or link in it renders.
func Readout(rows [][2]string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString("**" + r[0] + "**")
		if r[1] != "" {
			b.WriteString(" " + r[1])
		}
		b.WriteByte('\n')
	}
	return b.String()
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
