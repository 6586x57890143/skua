package notify

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// Poster is the REST notify calls as itself: a card, the edit that turns
// a live card into its VOD, and the reads that say whether it can hand out
// a card's ping role.
type Poster interface {
	CreateMessage(channel snowflake.ID, m discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error)
	UpdateMessage(channel, message snowflake.ID, m discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error)
	GetRoles(guild snowflake.ID, opts ...rest.RequestOpt) ([]discord.Role, error)
	GetMember(guild, user snowflake.ID, opts ...rest.RequestOpt) (*discord.Member, error)
}

const (
	// keepIDs is how much of an account's history is remembered: enough
	// that a deleted post can't bring an old one back as news.
	keepIDs = 100
	// burst is the most cards one account gets in one round, so a creator
	// posting twenty clips at once is three cards, not a flood.
	burst = 3
)

// loop polls one platform until ctx ends.
func (m *Module) loop(ctx context.Context, p Poster, platform string, src source) {
	t := time.NewTicker(m.every(src))
	defer t.Stop()
	for {
		m.poll(ctx, p, platform, src)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// every is how often a platform is checked: its own pace, or with push on
// its slower safety net.
func (m *Module) every(src source) time.Duration {
	if ps, ok := src.(pusher); ok && m.hooks.on() {
		return ps.reconcile()
	}
	return src.every()
}

// poll is one round of a platform: every account anyone follows there,
// checked once.
func (m *Module) poll(ctx context.Context, p Poster, platform string, src source) {
	accounts := m.accounts(platform)
	if len(accounts) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, src.every())
	defer cancel()
	got, err := src.check(ctx, accounts)
	m.mu.Lock()
	// A platform is failing when nothing came back. One dead account
	// among many is logged, not shown: it may be another server's.
	h := m.health[platform]
	h.err = nil
	if len(got) > 0 {
		h.ok = m.now()
	} else {
		h.err = err
	}
	m.health[platform] = h
	m.mu.Unlock()
	if err != nil {
		m.log.Warn("notify: checking", "platform", platform, "err", err)
	}
	for _, a := range accounts {
		if items, ok := got[a]; ok {
			m.observe(ctx, p, key{platform, a}, items, src)
		}
	}
}

// accounts is every account followed on platform, each once.
func (m *Module) accounts(platform string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, f := range m.follows {
		if f.platform == platform && !slices.Contains(out, f.account) {
			out = append(out, f.account)
		}
	}
	return out
}

// observe announces what an account shows that wasn't seen last time. The
// first look at an account is a baseline: its backlog isn't news.
func (m *Module) observe(ctx context.Context, p Poster, k key, items []item, src source) {
	if turn := m.turns[k.platform]; turn != nil {
		turn.Lock()
		defer turn.Unlock()
	}
	m.mu.Lock()
	prev, known := m.seen[k]
	m.mu.Unlock()
	next := remember(items, prev)
	var fresh []item
	if known {
		for _, it := range items {
			if !slices.Contains(prev, it.ID) {
				fresh = append(fresh, it)
			}
		}
	}
	// Oldest first, so cards land in the order things happened.
	fresh = fresh[:min(len(fresh), burst)]
	slices.Reverse(fresh)
	kp, _ := src.(keeper)
	for _, it := range fresh {
		if kp != nil && !it.live() && !kp.keep(ctx, it) {
			continue
		}
		m.announce(ctx, p, k, it)
		m.unpushed(k, it, src)
	}
	// A stream still on keeps its cards current.
	for _, it := range items {
		if it.live() && slices.Contains(prev, it.ID) {
			m.freshen(ctx, p, k, it)
		}
	}
	// A stream seen last time and gone now has ended.
	if known {
		for _, id := range prev {
			if strings.HasPrefix(id, "live:") && !slices.ContainsFunc(items, func(it item) bool { return it.ID == id }) {
				m.end(ctx, p, k, id, src)
			}
		}
	}
	if known && slices.Equal(prev, next) {
		return
	}
	m.mu.Lock()
	m.seen[k] = next
	m.mu.Unlock()
	if err := saveSeen(ctx, m.db, k, next); err != nil {
		m.log.Warn("notify: saving what was seen", "account", k.account, "err", err)
	}
}

// remember is what to compare the next round against: what shows now, and
// the posts seen before, but not a stream that has ended.
func remember(items []item, prev []string) []string {
	next := make([]string, 0, len(items)+len(prev))
	for _, it := range items {
		next = append(next, it.ID)
	}
	for _, id := range prev {
		if !strings.HasPrefix(id, "live:") && !slices.Contains(next, id) {
			next = append(next, id)
		}
	}
	return next[:min(len(next), keepIDs)]
}

// announce posts it to every channel following its account, keeping each
// live card so the stream's end can turn it into the VOD.
func (m *Module) announce(ctx context.Context, p Poster, k key, it item) {
	// A stream gets skua's picture of it, on its frame once the platform
	// has made one; a post its preview, if that answers.
	var pic []byte
	if it.live() {
		base, _ := m.frame(ctx, it.Image)
		if base == nil {
			it.Image = ""
		}
		m.mu.Lock()
		m.frames[stream{k, it.ID}] = base
		m.mu.Unlock()
		var err error
		if pic, err = compose(base, headline(it), foot(it, m.now().Sub(start(it, m.now())), false)); err != nil {
			m.log.Warn("notify: drawing a stream's picture", "account", k.account, "err", err)
		}
	} else {
		it.Image = m.picture(ctx, it.Image)
	}
	type card struct {
		f  follow
		to snowflake.ID
	}
	m.mu.Lock()
	var cards []card
	for _, f := range m.follows {
		if f.platform == k.platform && f.account == k.account {
			cards = append(cards, card{f, m.dest(f)})
		}
	}
	m.mu.Unlock()
	// Whether each server's ping role can be handed out, asked once a role.
	grant := map[snowflake.ID]bool{}
	for _, c := range cards {
		// No channel: the panel and /help both say so.
		if c.to == 0 || !m.on(c.f.guild) {
			continue
		}
		err := m.guard.Allow(c.f.guild, guard.MessageSend)
		if err == nil {
			var msg *discord.Message
			if _, asked := grant[c.f.role]; !asked && c.f.role != 0 {
				grant[c.f.role] = m.canGrant(p, c.f.guild, c.f.role)
			}
			shown, create := it, discord.MessageCreate{}
			var file *discord.File
			if pic != nil {
				shown, file = shot(it, pic)
			}
			create = alert(k.platform, shown, c.f.role, grant[c.f.role])
			if file != nil {
				create.Files = []*discord.File{file}
			}
			msg, err = p.CreateMessage(c.to, create)
			m.guard.Report(c.f.guild, struggling(err))
			if err == nil && msg != nil && it.live() {
				m.keepLive(ctx, posted{k: k, guild: c.f.guild, channel: c.to, message: msg.ID, role: c.f.role, it: it, at: m.now()})
			}
		}
		m.mu.Lock()
		if err != nil {
			m.failed[c.to] = why(err)
		} else {
			delete(m.failed, c.to)
		}
		m.mu.Unlock()
		if err != nil {
			m.log.Warn("notify: posting", "guild", c.f.guild, "channel", c.to, "err", err)
		}
	}
}

// dest is where f's cards go: its own channel, or its server's. 0 is
// nowhere yet. Called with mu held.
func (m *Module) dest(f follow) snowflake.ID {
	if f.channel != 0 {
		return f.channel
	}
	return m.bound[f.guild]
}

func struggling(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	return ok && re.Response != nil && (re.Response.StatusCode == http.StatusTooManyRequests || re.Response.StatusCode >= 500)
}

// why is a failed post as an admin reads it on notify's /help page.
func why(err error) string {
	if errors.Is(err, guard.ErrRateLimited) || errors.Is(err, guard.ErrCircuitOpen) {
		return "skua held back: too many posts in this server this hour"
	}
	if re, ok := errors.AsType[*rest.Error](err); ok && re.Response != nil {
		switch re.Response.StatusCode {
		case http.StatusForbidden:
			return "skua can't post there; give her view channel and send messages"
		case http.StatusNotFound:
			return "that channel is gone; /notify remove it"
		}
	}
	return "discord refused the post"
}

// news is what a post's card says happened, by platform.
var news = map[string]string{"youtube": "new on youtube", "x": "posted on x", "tiktok": "posted on tiktok"}

// alert is the card for something new: a post, or a stream going live.
// grant is whether the card carries the role's ping me button.
func alert(platform string, it item, role snowflake.ID, grant bool) discord.MessageCreate {
	if it.live() {
		return card(brand.PlatformColor(platform), platform, "live on "+platform, "watch", it, role, grant)
	}
	return card(brand.PlatformColor(platform), platform, news[platform], "open", it, role, grant)
}

// card is a notify card in color: what happened in its head, the title
// linked, who and what below it, the preview, and a button labelled label
// there. A live card is its platform's colour, so a channel of cards reads
// by platform at a glance. Link buttons can't be coloured: Discord draws
// them grey.
func card(color int, platform, what, label string, it item, role snowflake.ID, grant bool) discord.MessageCreate {
	title := line(it.Title, 200)
	if title == "" {
		title = "something new"
	}
	// Discord leaves a masked link whose text holds an emoji as raw
	// markdown, so such a title stays plain and the button carries it.
	body := "**" + title + "**"
	if strings.HasPrefix(it.URL, "https://") && !emoji(title) {
		body = "**[" + title + "](" + it.URL + ")**"
	}
	// Who it was, behind their platform's tile, as on the /notify panel.
	// Before the emoji sync the tile is left out, not attached.
	who := it.Author
	if mark := brand.Mention("pf_" + platform); mark != "" {
		who = mark + " " + who
	}
	body += "\n-# " + who
	if it.Detail != "" {
		body += " · " + line(it.Detail, 60)
	}
	if it.live() && it.Viewers > 0 {
		body += " · " + count(it.Viewers) + " watching"
	}
	var extra []discord.ContainerSubComponent
	if strings.HasPrefix(it.Image, "https://") || strings.HasPrefix(it.Image, "attachment://") {
		extra = append(extra, discord.NewMediaGallery(discord.MediaGalleryItem{Media: discord.UnfurledMediaItem{URL: it.Image}}))
	}
	var buttons []discord.InteractiveComponent
	if strings.HasPrefix(it.URL, "https://") {
		button := discord.NewLinkButton(label, it.URL)
		button.Emoji = brand.ComponentEmoji("pf_" + platform)
		buttons = append(buttons, button)
	}
	if grant && role != 0 {
		buttons = append(buttons, roleButton(role))
	}
	if len(buttons) > 0 {
		extra = append(extra, discord.NewActionRow(buttons...))
	}
	box := brand.Card(color, "notify", what, body, extra...)
	if role == 0 {
		return discord.MessageCreate{Components: []discord.LayoutComponent{box}, Flags: discord.MessageFlagIsComponentsV2, AllowedMentions: core.NoPings()}
	}
	return discord.MessageCreate{
		Components:      []discord.LayoutComponent{discord.NewTextDisplay("<@&" + role.String() + ">"), box},
		Flags:           discord.MessageFlagIsComponentsV2,
		AllowedMentions: pingRole(role),
	}
}

// pingRole is the one ping skua sends: the role an admin picked for an
// alert, and nothing else.
func pingRole(role snowflake.ID) *discord.AllowedMentions {
	return &discord.AllowedMentions{Parse: []discord.AllowedMentionType{}, Roles: []snowflake.ID{role}}
}

// emoji is whether s holds a pictograph: a symbol, or anything from the
// emoji planes.
func emoji(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r >= 0x1F000 || unicode.Is(unicode.So, r) })
}

// line is s on one line, at most n runes, with nothing that would break a
// markdown link around it.
func line(s string, n int) string {
	s = strings.Join(strings.Fields(strings.NewReplacer("[", "(", "]", ")").Replace(s)), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-3])) + "..."
}

// count is a number as a card reads it: 950, 1.2k, 12k, 1.3m.
func count(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10_000:
		return strings.TrimSuffix(strconv.FormatFloat(float64(n/100)/10, 'f', 1, 64), ".0") + "k"
	case n < 1_000_000:
		return strconv.Itoa(n/1000) + "k"
	}
	return strings.TrimSuffix(strconv.FormatFloat(float64(n/100_000)/10, 'f', 1, 64), ".0") + "m"
}
