package pp

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/png"
	"slices"
	"strings"
	"sync"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/pixel"
)

// Sealed letters are the north sea company's letters (LORE.md, sealed
// letters), each holding a code the server's admins loaded. Who gets one
// is kept out of the docs and the lore on purpose; keep it out of these
// comments too. A code lives in pp_letter only: never in the repo, the
// ledger or a log line.

// LetterRings is the count sendLetters reads.
const LetterRings = 10

const (
	letterForm = "ppletters"
	// mostLetters bounds the codes a server holds, and codeMost one code.
	mostLetters, codeMost = 50, 100
)

// letter is a bird's sealed letter: its code, when it went and whether
// they've broken the seal.
type letter struct {
	code   string
	at     int64
	opened bool
}

var (
	errNoLetter    = core.Tell("no letter has come for you")
	errLetterCodes = core.Tell(fmt.Sprintf("paste the codes one a line, each up to %d characters, %d at most", codeMost, mostLetters))
)

// loadLetters reads every server's letters: the codes still sealed and
// who holds the rest.
func (st *state) loadLetters(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select guild_id, code, winner, won_at, opened from pp_letter`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var guild, winner int64
		var l letter
		if err := rows.Scan(&guild, &l.code, &winner, &l.at, &l.opened); err != nil {
			return err
		}
		s := st.server(snowflake.ID(guild))
		s.mu.Lock()
		if winner == 0 {
			s.sealed++
		} else {
			if s.letters == nil {
				s.letters = map[snowflake.ID]*letter{}
			}
			s.letters[snowflake.ID(winner)] = &l
		}
		s.mu.Unlock()
	}
	return rows.Err()
}

// claimLetter gives user one of guild's sealed codes, or "" when none are
// left. The row lock skips a code another claim holds, so concurrent
// claims never share one, and the winner index keeps it one a bird.
func claimLetter(ctx context.Context, db DB, guild, user snowflake.ID, at int64) (string, error) {
	rows, err := db.Query(ctx, `update pp_letter set winner = $2, won_at = $3
		where (guild_id, code) = (select guild_id, code from pp_letter where guild_id = $1 and winner = 0
			order by code limit 1 for update skip locked)
		returning code`, int64(guild), int64(user), at)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var code string
	if rows.Next() {
		if err := rows.Scan(&code); err != nil {
			return "", err
		}
	}
	return code, rows.Err()
}

// sendLetters hands a letter to every member who has earned one and holds
// none, while the server has codes sealed. It runs from the loop after a
// save, so what it reads is written.
func (m *Module) sendLetters(ctx context.Context) {
	now := m.now().Unix()
	for g, s := range m.st.servers() {
		if !m.enabled(g) {
			continue
		}
		s.mu.Lock()
		var due []snowflake.ID
		if s.sealed > 0 {
			for u, mem := range s.members {
				if mem.signs.rings >= LetterRings && s.letters[u] == nil {
					due = append(due, u)
				}
			}
		}
		slices.SortFunc(due, func(a, b snowflake.ID) int {
			return cmp.Or(cmp.Compare(s.members[b].signs.rings, s.members[a].signs.rings), cmp.Compare(a, b))
		})
		s.mu.Unlock()
		for _, u := range due {
			code, err := claimLetter(ctx, m.db, g, u, now)
			if err != nil {
				m.log.Warn("pp: sending a sealed letter", "guild", g, "user", u, "err", err)
				break
			}
			s.mu.Lock()
			if code == "" {
				s.sealed = 0
				s.mu.Unlock()
				break
			}
			if s.letters == nil {
				s.letters = map[snowflake.ID]*letter{}
			}
			s.letters[u] = &letter{code: code, at: now}
			s.sealed--
			s.count(DayOf(m.now()), statLetter, int(s.members[u].signs.rings), 0)
			s.mu.Unlock()
			m.log.Info("pp: sealed letter sent", "guild", g, "user", u)
		}
	}
}

// letterOf is user's letter in guild, nil for none, and a copy.
func (m *Module) letterOf(guild, user snowflake.ID) *letter {
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	if l := s.letters[user]; l != nil {
		c := *l
		return &c
	}
	return nil
}

// letterText is the sealed letters on /pp's help page: how many are
// sealed and who holds the rest. Admins see each holder's code's last
// four, enough to check a ticket by. keyControls sets it by its button.
func (m *Module) letterText(guild snowflake.ID) string {
	s := m.st.server(guild)
	s.mu.Lock()
	sealed := s.sealed
	type held struct {
		user snowflake.ID
		l    letter
	}
	var sent []held
	for u, l := range s.letters {
		sent = append(sent, held{u, *l})
	}
	s.mu.Unlock()
	slices.SortFunc(sent, func(a, b held) int { return cmp.Compare(a.l.at, b.l.at) })
	// Who earns one stays out of every word skua shows, here too.
	text := fmt.Sprintf("### sealed letters\n-# the north sea company writes to a few birds, each letter holding one of these codes, and they claim it with a ticket. nobody is told until one comes\n**sealed** %d", sealed)
	for _, h := range sent {
		state := "unopened"
		if h.l.opened {
			state = "opened"
		}
		text += fmt.Sprintf("\n**sent** <@%s> · ends %s · %s <t:%d:R>", h.user, last4(h.l.code), state, h.l.at)
	}
	return text
}

// last4 is a code's last four characters.
func last4(code string) string {
	r := []rune(code)
	return string(r[max(0, len(r)-4):])
}

// letterModal asks for the codes.
func letterModal() discord.ModalCreate {
	return discord.ModalCreate{
		CustomID: letterForm,
		Title:    "load sealed letters",
		Components: []discord.LayoutComponent{
			discord.NewLabel("codes, one a line", discord.TextInputComponent{CustomID: "codes", Style: discord.TextInputStyleParagraph, Required: true, MaxLength: 4000}),
		},
	}
}

// openLetters opens the codes form for an admin.
func (m *Module) openLetters(e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	return e.Modal(letterModal())
}

// parseCodes is the codes pasted, one a line, trimmed and each once.
func parseCodes(raw string) ([]string, error) {
	var codes []string
	for line := range strings.Lines(raw) {
		c := strings.TrimSpace(line)
		if c == "" || slices.Contains(codes, c) {
			continue
		}
		if len([]rune(c)) > codeMost {
			return nil, errLetterCodes
		}
		codes = append(codes, c)
	}
	if len(codes) == 0 || len(codes) > mostLetters {
		return nil, errLetterCodes
	}
	return codes, nil
}

// submitLetters seals the codes pasted. Only counts are said or logged,
// never a code.
func (m *Module) submitLetters(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil || m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	codes, err := parseCodes(e.Data.Text("codes"))
	if err != nil {
		return err
	}
	s := m.st.server(*guild)
	s.mu.Lock()
	room := mostLetters - s.sealed - len(s.letters)
	s.mu.Unlock()
	if len(codes) > room {
		return core.Tell(fmt.Sprintf("this server holds %d codes at most and has room for %d more", mostLetters, max(room, 0)))
	}
	tag, err := m.db.Exec(ctx, `insert into pp_letter (guild_id, code) select $1, unnest($2::text[]) on conflict do nothing`, int64(*guild), codes)
	if err != nil {
		return err
	}
	added := int(tag.RowsAffected())
	s.mu.Lock()
	s.sealed += added
	sealed := s.sealed
	s.mu.Unlock()
	m.log.Info("pp: sealed letters loaded", "guild", *guild, "by", e.User().ID, "added", added)
	body := fmt.Sprintf("✓ %d sealed, %d sealed in all", added, sealed)
	if dup := len(codes) - added; dup > 0 {
		body += fmt.Sprintf("\n-# %d were loaded before", dup)
	}
	return e.CreateMessage(discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorOK, "pp", "sealed letters", body)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
}

// The letter's text on its screen, LORE.md's word for word.
var (
	letterSealedText = []string{
		"The letter comes up from the bellward by company runner, never by the penny post, sealed in green wax stamped with a gannet over the waves and tied in a blue ribbon gone grey with salt.",
	}
	letterOpenText = []string{
		"Inside is a line of figures and nothing else.",
		"A bird who takes the figures to the company's office is seen by the clerk, who knows them.",
	}
)

//go:embed sprites/letter_sealed.png
var letterSealedPNG []byte

//go:embed sprites/letter_open.png
var letterOpenPNG []byte

// letterArt is the letter sealed and broken open, decoded once.
var letterArt = sync.OnceValues(func() (sealed, open image.Image) {
	sealed, _ = png.Decode(bytes.NewReader(letterSealedPNG))
	open, _ = png.Decode(bytes.NewReader(letterOpenPNG))
	return sealed, open
})

// LetterScreen draws the letter, sealed or broken open, on the left with
// the hand on it, and the company's words beside it.
func LetterScreen(opened bool) ([]byte, error) { return letterCanvas(opened).Screen(screenScale) }

func letterCanvas(opened bool) *pixel.Canvas {
	c := pixel.New(screenW, screenH)
	pic := image.Rect(4, 4, 188, 212)
	c.Window(pic)
	c.Tab(pic, "sealed letter")
	sealed, open := letterArt()
	art, words := sealed, letterSealedText
	if opened {
		art, words = open, letterOpenText
	}
	// Four times over, or less for room for the hand at its left.
	scale := min(4, (pic.Dx()-32)/art.Bounds().Dx())
	size := art.Bounds().Size().Mul(scale)
	top := pic.Min.Y + pixel.TabDrop
	at := image.Pt((pic.Min.X+pic.Max.X-size.X)/2, top+(pic.Max.Y-4-top-size.Y)/2)
	c.Picture(art, at, scale)
	c.Cursor(at.X-2, at.Y+size.Y/2)

	text := image.Rect(192, 4, 380, 212)
	c.Window(text)
	c.Tab(text, "the north sea company")
	x, y := text.Min.X+12, paneTop(text)
	for _, p := range words {
		for _, l := range pixel.Wrap(strings.ToLower(p), text.Dx()-24) {
			c.Text(x, y, l, pixel.Ink, 0)
			y += pixel.LineHeight
		}
		y += pixel.LineHeight / 2
	}
	return c
}

// letterMessage is user's letter: sealed with the button that breaks it,
// or open with its code to copy.
func (m *Module) letterMessage(guild, user snowflake.ID) (discord.MessageCreate, error) {
	l := m.letterOf(guild, user)
	if l == nil {
		return discord.MessageCreate{}, errNoLetter
	}
	pic, err := LetterScreen(l.opened)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := "a letter from the north sea company, sealed in green wax with a gannet over the waves"
	body := ""
	extra := []discord.ContainerSubComponent{discord.NewMediaGallery(screenItem(pic, alt))}
	if l.opened {
		alt = "the north sea company's letter, open: a line of figures"
		extra[0] = discord.NewMediaGallery(screenItem(pic, alt))
		body = "```\n" + l.code + "\n```\n-# open a ticket and give this code to claim what the letter holds. it's yours alone, so keep it to yourself"
	} else {
		extra = append(extra, discord.NewActionRow(withIcon(discord.NewPrimaryButton("break the seal", "pp:seal"), "envelope")))
	}
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorOK, "pp", "sealed letter", body, extra...)},
		Files:           []*discord.File{screenAttachment(pic, alt)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// breakSeal marks user's letter opened, kept so it stays open.
func (m *Module) breakSeal(ctx context.Context, guild, user snowflake.ID) error {
	l := m.letterOf(guild, user)
	if l == nil {
		return errNoLetter
	}
	if l.opened {
		return nil
	}
	if _, err := m.db.Exec(ctx, `update pp_letter set opened = true where guild_id = $1 and winner = $2`, int64(guild), int64(user)); err != nil {
		return err
	}
	s := m.st.server(guild)
	s.mu.Lock()
	if l := s.letters[user]; l != nil {
		l.opened = true
	}
	s.mu.Unlock()
	return nil
}
