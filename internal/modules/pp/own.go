package pp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // a device may come as a gif, jpeg or webp; it is sent as png
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	_ "golang.org/x/image/webp"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/filter"
)

// A livery of one's own is a role a member names and colours, and from the
// drowned bell gives a device, an icon. They write it as a petition, see it
// drawn as Discord will show it on both themes, change it until it's right,
// and send it to the heralds, the moderators, who grant or refuse it. Every
// livery of one's own and every change to one is reviewed; only the yard's
// trade and faction colours are not.
//
// The draft lives in memory until it's sent: a restart loses a petition
// being written, never one sent.

// ownStyle is how a livery of one's own is dyed, and the perch it's worn
// from.
type ownStyle struct {
	key, name string
	rung      int64
	colors    int // how many a member picks
	// price is a week in hundredths of a pot day.
	price int64
}

var ownStyles = []ownStyle{
	{"plain", "plain dye", 3, 1, 100},
	{"silk", "shot silk", 4, 2, 250},
	{"nacre", "nacre", 5, 0, 400},
}

const (
	// deviceRung is the perch a device is sewn on from, and devicePrice
	// what it adds to a week, in hundredths of a pot day.
	deviceRung, devicePrice = 5, 100
	// heraldsFee is a petition's fee, returned if it's refused.
	heraldsFee = 50
	ownForm    = "ppown"
	nameMost   = 32
	// iconMost is the most a device may weigh, Discord's own limit.
	iconMost = 256 << 10
	// iconSide is the side a device is sent at.
	iconSide = 64
)

// nacreColors are Discord's holographic style, the one set its API takes:
// nacre's colours come from the shell, not the dyer.
var nacreColors = []uint32{0xa9c9ff, 0xffbbec, 0xffc3a0}

// draft is a petition being written.
type draft struct {
	name   string
	style  ownStyle
	colors []uint32
	icon   []byte // png, iconSide square at most, or nil
}

var (
	errOwnPerch  = core.Tell("a livery of your own is worn from the chitin perch; climb the stair first")
	errOwnName   = core.Tell("the heralds need a name of one to thirty two letters, without mentions or links")
	errOwnFilter = core.Tell("the heralds won't enter that name; pick another")
	errOwnTaken  = core.Tell("a role here already goes by that name; the heralds won't enter it twice")
	errOwnStyle  = core.Tell("your perch can't wear that weave yet")
	errOwnColors = core.Tell("the dyers need colours as hex, like #c66e34, or the name of a livery in the yard, like ember")
	errOwnIcon   = core.Tell("a device has to be a picture (png, jpeg, gif or webp) of 256 kb at most")
	errOwnDraft  = core.Tell("there's no petition written yet; write one first")
)

// names is the filter a livery's name passes.
var names = filter.Default()

// ownStyleOf is the style with key, and whether there is one.
func ownStyleOf(key string) (ownStyle, bool) {
	i := slices.IndexFunc(ownStyles, func(s ownStyle) bool { return s.key == key })
	if i < 0 {
		return ownStyle{}, false
	}
	return ownStyles[i], true
}

// weekly is what a week of d costs at st's pot day.
func (st *state) weekly(d *draft) int64 {
	p := d.style.price
	if d.icon != nil {
		p += devicePrice
	}
	return st.pot() * p / 100
}

// shown is the colours d is drawn in: nacre's own, or the dyer's.
func (d *draft) shown() []uint32 {
	if d.style.key == "nacre" {
		return nacreColors
	}
	return d.colors
}

// ownModal is the petition form, filled in with d when there is one. The
// styles offered are those mem's perch may wear, and the device only from
// the drowned bell.
func ownModal(mem *member, d *draft) discord.ModalCreate {
	name := discord.TextInputComponent{CustomID: "name", Style: discord.TextInputStyleShort, Required: true, MinLength: new(1), MaxLength: nameMost, Placeholder: "the name your livery goes by"}
	colors := discord.TextInputComponent{CustomID: "colors", Style: discord.TextInputStyleShort, MaxLength: 40, Placeholder: "#c66e34 #6286c3, or ember stair"}
	var opts []discord.StringSelectMenuOption
	for _, s := range ownStyles {
		if mem.rung < s.rung {
			continue
		}
		o := discord.NewStringSelectMenuOption(s.name, s.key)
		o.Description = map[string]string{"plain": "one colour", "silk": "two colours, one into the other", "nacre": "every colour, holographic"}[s.key]
		o.Default = d != nil && d.style.key == s.key
		opts = append(opts, o)
	}
	if d != nil {
		name.Value = d.name
		hex := make([]string, len(d.colors))
		for i, c := range d.colors {
			hex[i] = fmt.Sprintf("#%06x", c)
		}
		colors.Value = strings.Join(hex, " ")
	}
	parts := []discord.LayoutComponent{
		discord.NewLabel("name", name),
		discord.NewLabel("weave", discord.NewStringSelectMenu("style", "pick a weave", opts...)),
		discord.LabelComponent{Label: "colours", Description: "one for plain dye, two for shot silk, none for nacre. the dyers bring each into a shade that reads on light and dark", Component: colors},
	}
	if mem.rung >= deviceRung {
		up := discord.NewFileUpload("device").WithMinValues(0)
		up.Required = false
		parts = append(parts, discord.LabelComponent{Label: "device", Description: "a small picture beside your name, square is best; leave it empty to keep the one you have", Component: up})
	}
	return discord.ModalCreate{CustomID: ownForm, Title: "a petition to the heralds", Components: parts}
}

// openOwn opens the petition form.
func (m *Module) openOwn(guild snowflake.ID, e *events.ComponentInteractionCreate) error {
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, e.User().ID, today)
	d := s.drafts[e.User().ID]
	low := mem.rung < ownStyles[0].rung
	modal := ownModal(mem, d)
	s.mu.Unlock()
	if low {
		return errOwnPerch
	}
	return e.Modal(modal)
}

// submitOwn reads the petition form into the member's draft and shows it
// drawn. Fetching a device can outlast an interaction's three seconds, so
// the answer is deferred.
func (m *Module) submitOwn(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return errNotServer
	}
	if !m.plays(e) {
		return errBeta
	}
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	msg, err := m.readOwn(ctx, *guild, e)
	if err != nil {
		if _, isTell := errors.AsType[core.Tell](err); !isTell {
			m.log.Warn("pp: a petition", "guild", *guild, "err", err)
			err = core.Tell("the heralds couldn't read that just now; try again in a while")
		}
		_, ferr := e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(), discord.MessageCreate{
			Content: err.Error(), Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()}, rest.WithCtx(ctx))
		return ferr
	}
	msg = m.withMenu(*guild, e.User().ID, m.Admin != nil && m.Admin(e), msg, "pp:shop")
	_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(),
		core.Edit(discord.NewMessageUpdateV2(msg.Components...), msg.Files...), rest.WithCtx(ctx))
	return err
}

// readOwn checks the form and keeps it as the member's draft.
func (m *Module) readOwn(ctx context.Context, guild snowflake.ID, e *events.ModalSubmitInteractionCreate) (discord.MessageCreate, error) {
	user := e.User().ID
	today := DayOf(m.now())
	s := m.st.server(guild)
	s.mu.Lock()
	mem := m.st.member(s, guild, user, today)
	rung, old := mem.rung, s.drafts[user]
	s.mu.Unlock()
	if rung < ownStyles[0].rung {
		return discord.MessageCreate{}, errOwnPerch
	}

	name, err := ownName(e.Data.Text("name"))
	if err != nil {
		return discord.MessageCreate{}, err
	}
	if m.rest != nil {
		roles, err := m.rest.GetRoles(guild, rest.WithCtx(ctx))
		if err != nil {
			return discord.MessageCreate{}, err
		}
		if slices.ContainsFunc(roles, func(r discord.Role) bool { return strings.EqualFold(r.Name, name) }) {
			return discord.MessageCreate{}, errOwnTaken
		}
	}
	styles := e.Data.StringValues("style")
	style, ok := ownStyleOf(strings.Join(styles, ""))
	if !ok || style.rung > rung {
		return discord.MessageCreate{}, errOwnStyle
	}
	colors, err := ownColors(e.Data.Text("colors"), style.colors)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	d := &draft{name: name, style: style, colors: colors}
	if old != nil && rung >= deviceRung {
		d.icon = old.icon
	}
	if files := e.Data.Attachments("device"); len(files) > 0 && rung >= deviceRung {
		if d.icon, err = fetchIcon(ctx, files[0]); err != nil {
			return discord.MessageCreate{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafts == nil {
		s.drafts = map[snowflake.ID]*draft{}
	}
	s.drafts[user] = d
	return m.st.ownMessage(mem, d)
}

// ownName is a livery's name as the heralds would enter it.
func ownName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	n := utf8.RuneCountInString(name)
	if n < 1 || n > nameMost || strings.ContainsAny(name, "@#<>:/`*_~|\\") || strings.Contains(strings.ToLower(name), "discord") {
		return "", errOwnName
	}
	if v := names.Check(name); v.Block != "" || v.Text != name {
		return "", errOwnFilter
	}
	return name, nil
}

// ownColors reads want colours from raw: hex or a yard livery's key or
// name, each brought into the band every livery reads in on both themes.
func ownColors(raw string, want int) ([]uint32, error) {
	words := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return r == ' ' || r == ',' })
	var out []uint32
	for i := 0; i < len(words) && len(out) < want; i++ {
		w := strings.TrimPrefix(words[i], "#")
		if c, err := strconv.ParseUint(w, 16, 32); err == nil && len(w) == 6 {
			out = append(out, inBand(uint32(c)))
			continue
		}
		at := slices.IndexFunc(dyes, func(d dye) bool { return d.key == words[i] || strings.Contains(d.name, words[i]) })
		if at < 0 {
			return nil, errOwnColors
		}
		out = append(out, dyes[at].color)
	}
	if len(out) < want {
		return nil, errOwnColors
	}
	return out, nil
}

// The band: a name reads on Discord's white and on its darkest member list
// grey when its luminance sits between these, keeping 3 to 1 against
// each, the same band the yard's liveries are in.
const bandLow, bandHigh = 0.20, 0.30

// inBand is c with its lightness moved, if it must be, into the band,
// keeping its hue and its colour.
func inBand(c uint32) uint32 {
	l := luminance(c)
	if l >= bandLow && l <= bandHigh {
		return c
	}
	target := bandLow
	if l > bandHigh {
		target = bandHigh
	}
	h, s, _ := toHSL(c)
	lo, hi := 0.0, 1.0
	for range 30 {
		mid := (lo + hi) / 2
		if luminance(fromHSL(h, s, mid)) < target {
			lo = mid
		} else {
			hi = mid
		}
	}
	return fromHSL(h, s, (lo+hi)/2)
}

// luminance is c's relative luminance, as WCAG reckons it.
func luminance(c uint32) float64 {
	lin := func(v uint32) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c>>16&0xff) + 0.7152*lin(c>>8&0xff) + 0.0722*lin(c&0xff)
}

func toHSL(c uint32) (h, s, l float64) {
	r, g, b := float64(c>>16&0xff)/255, float64(c>>8&0xff)/255, float64(c&0xff)/255
	hi, lo := max(r, g, b), min(r, g, b)
	l = (hi + lo) / 2
	d := hi - lo
	if d == 0 {
		return 0, 0, l
	}
	s = d / (1 - math.Abs(2*l-1))
	switch hi {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, l
}

func fromHSL(h, s, l float64) uint32 {
	f := func(n float64) uint32 {
		k := math.Mod(n+h/30, 12)
		a := s * min(l, 1-l)
		return uint32(math.Round(255 * (l - a*max(-1, min(k-3, 9-k, 1)))))
	}
	return f(0)<<16 | f(8)<<8 | f(4)
}

// iconClient fetches devices: briefly, and only from Discord.
var iconClient = &http.Client{Timeout: 10 * time.Second}

// fetchIcon is a device uploaded with the form as a PNG at most iconSide
// square. Only Discord's own attachment hosts are fetched, at most iconMost
// read, and the picture decoded and drawn again, so what reaches Discord
// is pixels and nothing the upload carried besides.
func fetchIcon(ctx context.Context, a discord.Attachment) ([]byte, error) {
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() != "cdn.discordapp.com" && u.Hostname() != "media.discordapp.net" {
		return nil, errOwnIcon
	}
	if a.Size > iconMost || a.ContentType != nil && !strings.HasPrefix(*a.ContentType, "image/") {
		return nil, errOwnIcon
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	res, err := iconClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pp: fetching a device: %s", res.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, iconMost+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > iconMost {
		return nil, errOwnIcon
	}
	return redrawIcon(raw)
}

// redrawIcon decodes raw and draws it again as a PNG at most iconSide
// square, nearest neighbour so pixel art stays crisp.
func redrawIcon(raw []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 {
		return nil, errOwnIcon
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errOwnIcon
	}
	b := src.Bounds()
	scale := min(1, float64(iconSide)/float64(max(b.Dx(), b.Dy())))
	w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			out.Set(x, y, src.At(b.Min.X+x*b.Dx()/w, b.Min.Y+y*b.Dy()/h))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ownMessage is the petition as it stands: drawn on both themes, its
// price, and the way to change it. s.mu held.
func (st *state) ownMessage(mem *member, d *draft) (discord.MessageCreate, error) {
	o := Own{Purse: mem.purse, Fee: st.pot() * heraldsFee / 100}
	if d != nil {
		o.Name, o.Style, o.Colors, o.Week = d.name, d.style.name, d.shown(), st.weekly(d)
		if d.icon != nil {
			o.Icon, _ = png.Decode(bytes.NewReader(d.icon))
		}
	}
	pic, err := OwnScreen(o)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	alt := "a livery of your own: no petition written yet"
	write := "write a petition"
	if d != nil {
		alt = fmt.Sprintf("a livery of your own, %q in %s, %s pp a week and a fee of %s pp to the heralds", d.name, d.style.name, grouped(o.Week/unit), grouped(o.Fee/unit))
		if d.icon != nil {
			alt += ", with a device"
		}
		write = "change the petition"
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "shop", "",
			discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: "attachment://" + screenName}, Description: alt}),
			discord.NewActionRow(discord.NewSecondaryButton("◂ the yard", "pp:yard"), withIcon(discord.NewPrimaryButton(write, "pp:ownwrite"), "quill")))},
		Files:           []*discord.File{discord.NewFile(screenName, alt, bytes.NewReader(pic))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}
