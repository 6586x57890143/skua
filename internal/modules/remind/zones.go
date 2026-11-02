package remind

import (
	_ "embed"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // the zones ship in the binary, whatever the image has

	"github.com/disgoorg/disgo/discord"
)

// zones.txt is every canonical IANA zone under a continent or ocean, plus
// UTC, listed once from Go's zoneinfo.zip. zones_test loads each one.
//
//go:embed zones.txt
var zoneList string

var (
	zoneNames = strings.Fields(zoneList)
	zoneCache sync.Map // name -> *time.Location
)

// zone is the location name names, or UTC for one that isn't a zone.
func zone(name string) *time.Location {
	if l, ok := zoneCache.Load(name); ok {
		return l.(*time.Location)
	}
	l, err := time.LoadLocation(name)
	if err != nil || name == "" || name == "Local" {
		l = time.UTC
	}
	zoneCache.Store(name, l)
	return l
}

// known is name as listed, matched without case, or "".
func known(name string) string {
	for _, z := range zoneNames {
		if strings.EqualFold(z, name) {
			return z
		}
	}
	return ""
}

// common is what the tz list offers before anything is typed.
var common = []string{"Europe/London", "Europe/Berlin", "Europe/Helsinki", "America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles", "America/Sao_Paulo", "Asia/Kolkata", "Asia/Singapore", "Asia/Tokyo", "Australia/Sydney", "UTC"}

// zoneChoices is the tz list for what was typed: each zone with the time
// it is there now, so a member finds theirs by the clock as much as by the
// name. A city matches anywhere in the name, "new york" as "New_York".
func zoneChoices(q string, now time.Time) []discord.AutocompleteChoice {
	q = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(q)), " ", "_")
	names := common
	if q != "" {
		names = nil
		for _, z := range zoneNames {
			if strings.Contains(strings.ToLower(z), q) {
				names = append(names, z)
				if len(names) == 25 {
					break
				}
			}
		}
	}
	out := make([]discord.AutocompleteChoice, 0, len(names))
	for _, z := range names {
		out = append(out, discord.AutocompleteChoiceString{Name: strings.ToLower(z) + " · " + now.In(zone(z)).Format("15:04") + " now", Value: z})
	}
	return out
}
