package notify

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// A post's preview is checked before it is posted: Discord shows a link
// that doesn't answer as an image as "image not found", and keeps it.
// youtube makes its largest preview only for some videos. A stream's card
// carries skua's own picture instead (shot.go), kept current while the
// stream is on (freshen).

// answers is whether u answers as an image.
func answers(ctx context.Context, c *http.Client, u string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	res, err := c.Do(req)
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode/100 == 2 && strings.HasPrefix(res.Header.Get("Content-Type"), "image/")
}

// picture is the first of u and its smaller stand ins that answers as an
// image, or "" when none does, and the card goes without.
func (m *Module) picture(ctx context.Context, u string) string {
	if !strings.HasPrefix(u, "https://") {
		return ""
	}
	tries := []string{u}
	if strings.Contains(u, "/maxresdefault") {
		tries = append(tries, strings.Replace(u, "/maxresdefault", "/hqdefault", 1))
	}
	// A slow host costs the card its preview, not its timing.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, t := range tries {
		if m.looks(ctx, t) {
			return t
		}
	}
	return ""
}

// restyle is how often a live card is brought up to date: its title and
// category, and a fresh preview.
const restyle = 10 * time.Minute

// freshen keeps a live stream's cards current while it is on: a new title
// or category at once, a frame the picture went without once the platform
// has made one, and every restyle a fresh frame and the time on air.
func (m *Module) freshen(ctx context.Context, p Poster, k key, it item) {
	s := stream{k, it.ID}
	m.mu.Lock()
	cards := slices.Clone(m.live[s])
	m.mu.Unlock()
	if len(cards) == 0 {
		return
	}
	if it.Viewers > cards[0].peak {
		m.mu.Lock()
		for i := range m.live[s] {
			m.live[s][i].peak = max(m.live[s][i].peak, it.Viewers)
		}
		m.mu.Unlock()
	}
	was := cards[0].it
	last := cards[0].edited
	if last.IsZero() {
		last = cards[0].at
	}
	changed := it.Title != was.Title || it.Detail != was.Detail
	due := m.now().Sub(last) >= restyle
	missing := was.Image == "" && it.Image != ""
	if !changed && !due && !missing {
		return
	}
	now := it
	if now.Started.IsZero() {
		now.Started = was.Started
	}
	// A fresh frame, on a new link so no cache hands back an old one.
	base, _ := m.frame(ctx, bust(it.Image, m.now()))
	m.mu.Lock()
	if base != nil {
		m.frames[s] = base
	} else {
		base = m.frames[s]
		now.Image = was.Image
	}
	m.mu.Unlock()
	if now.Image == "" && !changed && !due {
		// Still not made: a picture waiting only for it waits on.
		return
	}
	pic, err := compose(base, headline(now), foot(now, m.now().Sub(start(now, cards[0].at)), false))
	if err != nil {
		m.log.Warn("notify: drawing a stream's picture", "account", k.account, "err", err)
		return
	}
	m.mu.Lock()
	for i := range m.live[s] {
		m.live[s][i].it, m.live[s][i].edited = now, m.now()
	}
	m.mu.Unlock()
	for _, c := range cards {
		if !m.on(c.guild) {
			continue
		}
		err := m.guard.Allow(c.guild, guard.MessageSend)
		if err == nil {
			shown, file := shot(now, pic)
			_, err = p.UpdateMessage(c.channel, c.message, relive(k.platform, shown, c.role, m.canGrant(p, c.guild, c.role), file))
			m.guard.Report(c.guild, struggling(err))
		}
		if err != nil {
			m.log.Warn("notify: bringing a live card up to date", "guild", c.guild, "channel", c.channel, "err", err)
		}
	}
}

// start is when a stream began: where the platform says, or its card.
func start(it item, posted time.Time) time.Time {
	if it.Started.IsZero() {
		return posted
	}
	return it.Started
}

// bust is u with the time on it, so each restyle's preview is a link
// Discord hasn't fetched before.
func bust(u string, t time.Time) string {
	if u == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "t=" + strconv.FormatInt(t.Unix(), 10)
}

// relive is a live card again, as an edit carrying its new picture in
// place of the old: it never pings, since the role was pinged when the
// stream started.
func relive(platform string, it item, role snowflake.ID, grant bool, pic *discord.File) discord.MessageUpdate {
	msg := alert(platform, it, role, grant)
	return withPicture(discord.MessageUpdate{Components: &msg.Components, AllowedMentions: core.NoPings()}, pic)
}

// withPicture swaps an edited card's upload for pic, when there is one.
func withPicture(u discord.MessageUpdate, pic *discord.File) discord.MessageUpdate {
	if pic != nil {
		u.Files = []*discord.File{pic}
		u.Attachments = &[]discord.AttachmentUpdate{}
	}
	return u
}
