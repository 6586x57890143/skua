package fix

import (
	"github.com/6586x57890143/skua/internal/obs"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/disgoorg/disgo/discord"
)

// clean makes metadata inert Discord text. Quoted source text retains its case,
// but cannot inject formatting, masked links, mentions or Discord timestamps.
func clean(s string, limit int) string {
	var b strings.Builder
	used := 0
	for _, r := range s {
		if r == '\r' || r == 0 || (r < 32 && r != '\n' && r != '\t') {
			continue
		}
		piece := string(r)
		if strings.ContainsRune("\\`*_~|[]()#>-", r) {
			piece = "\\" + piece
		}
		if r == '@' {
			piece = "@\u200b"
		}
		if r == '<' {
			piece = "‹"
		}
		n := len(utf16.Encode([]rune(piece)))
		if used+n > limit-3 {
			b.WriteString("...")
			break
		}
		used += n
		b.WriteString(piece)
	}
	return b.String()
}
func quote(text string) string { return "> " + strings.ReplaceAll(text, "\n", "\n> ") }

// clip budgets the final formatted UTF-16 text, including quote prefixes and
// escapes. It never cuts a surrogate pair and leaves room for its truncation.
func clip(s string, limit int) string {
	used := 0
	for i, r := range s {
		n := 1
		if r > 0xffff {
			n = 2
		}
		if used+n > limit-3 {
			return s[:i] + "..."
		}
		used += n
	}
	return s
}

func card(l Link, p Post) discord.MessageCreate {
	start := time.Now()
	defer func() { obs.Default.Add("fix build", obs.Run, time.Since(start)) }()
	platform := l.Platform
	if platform == "twitter" {
		platform = "x"
	}
	heading := "**fix** · " + platform
	if mark := brand.Mention("mod_fix"); mark != "" {
		heading = mark + " " + heading
	}
	parts := []discord.ContainerSubComponent{discord.NewTextDisplay(heading)}
	who := clean(p.Author, 120)
	if who == "" {
		who = "post on " + platform
	}
	if p.Handle != "" {
		who += " · " + clean(p.Handle, 100)
	}
	if avatar := remoteURL(p.Avatar); avatar != "" && !p.Sensitive {
		parts = append(parts, discord.NewSection(discord.NewTextDisplay("**"+who+"**")).WithAccessory(discord.NewThumbnail(avatar)))
	} else {
		parts = append(parts, discord.NewTextDisplay("**"+who+"**"))
	}
	if p.Title != "" && p.Title != p.Text {
		parts = append(parts, discord.NewTextDisplay("### "+clean(p.Title, 200)))
	}
	if p.Text != "" {
		parts = append(parts, discord.NewTextDisplay(clip(quote(clean(p.Text, 1800)), 1800)))
	}
	// A whole message has a 4000-character text budget. Source sections share
	// 900 units, so a carousel cannot crowd out provenance or the source link.
	left := 900
	mediaLeft := 20
	truncated := false
	seen := map[string]bool{}
	appendMedia := func(media []Media, sensitive bool) {
		var items []discord.MediaGalleryItem
		for _, m := range media {
			u := remoteURL(m.URL)
			if mediaLeft == 0 {
				truncated = true
				break
			}
			if u == "" || seen[u] {
				continue
			}
			seen[u] = true
			mediaLeft--
			items = append(items, discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: u}, Description: clean(m.Alt, 1000), Spoiler: sensitive || m.Sensitive})
			if len(items) == 10 {
				parts = append(parts, discord.NewMediaGallery(items...))
				items = nil
			}
		}
		if len(items) > 0 {
			parts = append(parts, discord.NewMediaGallery(items...))
		}
	}
	appendMedia(p.Media, p.Sensitive)
	for i, s := range p.Sections {
		if i == 6 || left < 40 {
			break
		}
		title := clean(s.Title, min(100, left/2))
		text := clean(s.Text, min(400, left))
		block := clip("**"+title+"**\n"+quote(text), min(500, left))
		parts = append(parts, discord.NewTextDisplay(block))
		left -= len(utf16.Encode([]rune(block)))
		appendMedia(s.Media, p.Sensitive || s.Sensitive)
	}
	secondary := clean(p.Stats, 160)
	if p.Context != "" {
		secondary += "\n" + clean(p.Context, 160)
	}
	if secondary != "" {
		parts = append(parts, discord.NewTextDisplay("-# "+secondary))
	}
	stamp := ""
	if t, err := time.Parse(time.RFC3339, p.Timestamp); err == nil {
		stamp = " · " + t.UTC().Format("2006-01-02 15:04 utc")
	}

	parts = append(parts, discord.NewTextDisplay("-# metadata: "+clean(p.Provider, 30)+stamp))
	if truncated {
		parts = append(parts, discord.NewTextDisplay("-# more media on the original post"))
	}
	parts = append(parts, discord.NewActionRow(discord.NewLinkButton("open original", l.URL)))
	return discord.MessageCreate{Flags: discord.MessageFlagIsComponentsV2, Components: []discord.LayoutComponent{discord.NewContainer(parts...).WithAccentColor(brand.PlatformColor(platform))}, AllowedMentions: core.NoPings()}
}

func fallback(l Link) discord.MessageCreate {
	return card(l, Post{Title: "preview unavailable", Context: "she couldn't get this post's metadata; open the original to view it", Provider: "unavailable"})
}
