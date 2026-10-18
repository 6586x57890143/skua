package pp

import (
	"bytes"
	"fmt"
	"image"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/pixel"
)

// A class's lore card is the character before it's a pick: the portrait
// large on the left, and on the right who they are, told the way an old
// game's character select tells it, a page at a time. The world they live
// in is LORE.md's.

// Lore is a class's character: a line on who they are, their story in
// paragraphs and a line in their own voice.
type Lore struct {
	Epithet string
	Story   []string
	Quote   string
}

// loreW and loreH are the lore card's size, taller than the other
// screens so the story has room.
const loreW, loreH = 384, 288

// loreLine is the gap between the story's lines, tighter than a pane's so
// a page holds more of it.
const loreLine = 12

// loreText is the story's box on the card.
var loreText = image.Rect(164, 4+pixel.TabDrop+6, 368, loreH-10)

// loreLineOf is one set line of a story: its text and whether it's the
// quote, which is set in the book hand.
type loreLineOf struct {
	text  string
	quote bool
}

// height is how far down the page l takes: a story line loreLine, the
// quote its face's height, a gap between paragraphs half a story line.
func (l loreLineOf) height() int {
	switch {
	case l.quote:
		return pixel.Serif.Height
	case l.text == "":
		return loreLine / 2
	}
	return loreLine
}

// loreHeight is how far down the page lines reach.
func loreHeight(lines []loreLineOf) int {
	h := 0
	for _, l := range lines {
		h += l.height()
	}
	return h
}

// lorePages is l's story set into pages that fit the card. A paragraph
// that won't fit what's left of a page breaks at the last sentence that
// does, so a page never ends mid sentence, and the quote comes last, whole.
func lorePages(l Lore) [][]loreLineOf {
	var pages [][]loreLineOf
	var page []loreLineOf
	set := func(lines []string, quote bool) {
		if len(page) > 0 {
			page = append(page, loreLineOf{})
		}
		for _, s := range lines {
			page = append(page, loreLineOf{text: s, quote: quote})
		}
	}
	turn := func() { pages, page = append(pages, page), nil }
	// room is how many story lines still fit on the page, after a gap.
	room := func() int {
		if len(page) == 0 {
			return loreText.Dy() / loreLine
		}
		return (loreText.Dy() - loreHeight(page) - loreLine/2) / loreLine
	}
	for _, p := range l.Story {
		sentences := strings.SplitAfter(p, ". ")
		for len(sentences) > 0 {
			// The most sentences that fit what's left, at least two lines'
			// worth, or a fresh page.
			n, lines := 0, []string(nil)
			for k := len(sentences); k > 0; k-- {
				w := pixel.Light.Wrap(strings.TrimSpace(strings.Join(sentences[:k], "")), loreText.Dx())
				if len(w) <= room() && (k == len(sentences) || len(w) >= 2) {
					n, lines = k, w
					break
				}
			}
			if n == 0 && len(page) > 0 {
				turn()
				continue
			}
			if n == 0 {
				// A sentence longer than a page runs over; the layout test
				// catches it.
				n, lines = 1, pixel.Light.Wrap(strings.TrimSpace(sentences[0]), loreText.Dx())
			}
			set(lines, false)
			sentences = sentences[n:]
		}
	}
	if l.Quote != "" {
		q := pixel.Serif.Wrap("\""+l.Quote+"\"", loreText.Dx())
		if len(page) > 0 && loreHeight(page)+loreLine/2+len(q)*pixel.Serif.Height > loreText.Dy() {
			turn()
		}
		set(q, true)
	}
	return append(pages, page)
}

// loreID is the press that shows class i's card at page.
func loreID(i, page int) string { return fmt.Sprintf("pp:lore:%d:%d", i, page) }

// parseLore reads a loreID back, refusing one that names no class or page.
func parseLore(id string) (i, page int, ok bool) {
	f := strings.Split(strings.TrimPrefix(id, "pp:lore:"), ":")
	if len(f) != 2 {
		return 0, 0, false
	}
	i, err1 := strconv.Atoi(f[0])
	page, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil || i < 0 || i >= len(Classes) {
		return 0, 0, false
	}
	return i, page, page >= 0 && page < len(lorePages(ClassLore[Classes[i].Key]))
}

// loreMessage is class i's lore card at page, the class browser: the
// picture, the titles the class wears and what walking it costs, and the
// buttons to page through the classes and the story and to walk this path.
// mine is the member's class once they've picked one.
func loreMessage(i, page int, mine string, cost, purse int64) (discord.MessageCreate, error) {
	cl := Classes[i]
	pic, err := LoreScreen(cl, page)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	l := ClassLore[cl.Key]
	alt := clip(cl.Name()+", "+l.Epithet+": "+strings.Join(l.Story, " ")+" \""+l.Quote+"\"", altLimit)
	price := "your first pick this season is free. after it a change costs pp, more the higher your level and more again each time"
	if cost > 0 {
		price = fmt.Sprintf("changing class now costs %s pp and you have %s. the first pick of a new season is free again", grouped(cost/unit), grouped(purse/unit))
	}
	note := "-# " + strings.Join(cl.Titles[:], " · ") + "\n-# " + price

	pages := len(lorePages(l))
	n := len(Classes)
	turn := "read on"
	if page+1 >= pages {
		turn = "from the top"
	}
	walk := withIcon(discord.NewPrimaryButton("walk this path", "pp:pick:"+cl.Key), classIcons[cl.Key])
	if cl.Key == mine {
		walk = withIcon(discord.NewSuccessButton("your path", "pp:pick:"+cl.Key), classIcons[cl.Key]).AsDisabled()
	}
	row := discord.NewActionRow(
		discord.NewSecondaryButton("◂", loreID((i+n-1)%n, 0)),
		discord.NewSecondaryButton(turn, loreID(i, (page+1)%pages)),
		discord.NewSecondaryButton("▸", loreID((i+1)%n, 0)),
		walk)
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "lore", note,
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}),
			row)},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// altLimit is the most Discord takes for a picture's description; past it
// the whole message is refused.
const altLimit = 1024

// clip is s cut at the last sentence that ends within limit, or at limit
// when no sentence does.
func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	// A sentence may end right at limit, its space just past it.
	if end := strings.LastIndex(s[:limit+1], ". "); end > 0 {
		return s[:end+1]
	}
	return s[:limit]
}

// classIndex is where key sits in Classes, the wanderer's 0 for one it
// doesn't know.
func classIndex(key string) int {
	for i, c := range Classes {
		if c.Key == key {
			return i
		}
	}
	return 0
}

// LoreScreen draws c's lore card at page.
func LoreScreen(c Class, page int) ([]byte, error) { return loreCanvas(c, page).Soft(screenScale) }

func loreCanvas(cl Class, page int) *pixel.Canvas {
	c := pixel.New(loreW, loreH)
	l := ClassLore[cl.Key]

	left := image.Rect(4, 4, 150, loreH-4)
	c.Window(left)
	if img := Portrait(cl); img != nil {
		c.Portrait(img, image.Pt(13, 13), 4)
	}
	y := 13 + 48*4 + 12
	// The tab names the bird; under the portrait, the job.
	iconText(c, classIcons[cl.Key], 12, y, cl.Key, pixel.Ink)
	y += 16
	for _, s := range pixel.Serif.Wrap(l.Epithet, left.Dx()-16) {
		c.Print(pixel.Serif, 12, y, s, pixel.Dim)
		y += 14
	}

	text := image.Rect(154, 4, 380, loreH-4)
	c.Window(text)
	c.Tab(text, cl.Bird+" "+cl.Key)
	pages := lorePages(l)
	page = min(max(page, 0), len(pages)-1)
	y = loreText.Min.Y
	for _, s := range pages[page] {
		face := pixel.Light
		if s.quote {
			face = pixel.Serif
		}
		c.Print(face, loreText.Min.X, y, s.text, pixel.Ink)
		y += s.height()
	}
	if len(pages) > 1 {
		tabName(c, text, fmt.Sprintf("%d / %d", page+1, len(pages)))
	}
	return c
}
