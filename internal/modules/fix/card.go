package fix

import (
	"strings"
	"time"
	"unicode/utf16"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/obs"
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
	parts := []discord.ContainerSubComponent{}
	remaining := 3500 // reserve footer room so long source text cannot hide stats or attribution
	addText := func(text string) {
		if remaining < 4 || text == "" {
			return
		}
		text = clip(text, remaining)
		parts = append(parts, discord.NewTextDisplay(text))
		remaining -= len(utf16.Encode([]rune(text)))
	}
	heading := clip(cardIdentity(l, p), remaining)
	parts = append(parts, cardHeader(l, p, heading))
	remaining -= len(utf16.Encode([]rune(heading)))
	// Instagram titles are generated caption summaries, not separate headlines.
	if platform != "reddit" && (platform != "instagram" || p.Text == "") && p.Title != "" && p.Title != p.Text {
		addText("**" + clean(p.Title, 200) + "**")
	}
	if p.Text != "" && (platform != "reddit" || p.Text != p.Title) {
		addText(sourceText(p.Text, min(1800, remaining)))
	}
	// A whole message has a 4000-character text budget. Source sections share
	// 900 units, so a carousel cannot crowd out provenance or the source link.
	left := min(900, remaining)
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
		block := sectionText(l, s, min(500, left))
		if block != "" {
			parts = append(parts, discord.NewSmallSeparator().WithDivider(false))
			addText(block)
			left -= len(utf16.Encode([]rune(block)))
		}
		appendMedia(s.Media, p.Sensitive || s.Sensitive)
	}
	if p.Context != "" {
		context := clean(p.Context, 160)
		key := ""
		if p.Provider == "unavailable" {
			key = "skua_error"
		} else if p.Incomplete {
			key = "skua_warn"
		}
		if mark := brand.Mention(key); mark != "" {
			context = mark + " " + context
		}
		addText(context)
	}
	if truncated {
		addText("-# more media on the original post")
	}
	// Compact footer: engagement and attribution share one text component.
	parts = append(parts, discord.NewSmallSeparator())
	meta := clean(p.Provider, 30)
	if translated := translationCredit(l, p); translated != "" {
		meta += " · " + translated
	}
	footer := statLineLimit(p.Stats, 500-len(utf16.Encode([]rune(meta)))-8)
	if footer != "" {
		footer += " · "
	}
	footer = "-# " + footer + meta
	// Footer is capped at 500 UTF-16 units, including custom emoji markup.
	remaining += 500
	addText(clip(footer, 500))
	return discord.MessageCreate{Flags: discord.MessageFlagIsComponentsV2, Components: []discord.LayoutComponent{discord.NewContainer(parts...).WithAccentColor(accent(l, p))}, AllowedMentions: core.NoPings()}
}

func fallback(l Link) discord.MessageCreate {
	link := "author link"
	if l.Platform == "reddit" {
		link = "post title"
	}
	return card(l, Post{Title: "preview unavailable", Context: "she couldn't get this post's metadata; follow the " + link + " to view the original", Provider: "unavailable"})
}
