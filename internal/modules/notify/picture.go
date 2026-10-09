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

// A card's preview is checked before it is posted: Discord shows a link
// that doesn't answer as an image as "image not found", and keeps it.
// youtube makes its largest preview only for some videos and streams, and
// kick a stream's only a while after it starts. A live card is kept
// current while its stream is on (freshen).

// image is whether u answers as an image.
func image(ctx context.Context, c *http.Client, u string) bool {
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
// or category at once, a preview the card went up without once the
// platform has made one, and every restyle a fresh preview, on a new link
// so Discord fetches it again instead of keeping a stale or missing one.
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
	now.Cover = was.Cover
	if now.Started.IsZero() {
		now.Started = was.Started
	}
	if now.Image = m.picture(ctx, bust(it.Image, m.now())); now.Image == "" {
		// Still not made: a card waiting only for it waits on.
		if !changed && !due {
			return
		}
		now.Image = was.Image
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
			_, err = p.UpdateMessage(c.channel, c.message, relive(k.platform, now, c.role, m.canGrant(p, c.guild, c.role)))
			m.guard.Report(c.guild, struggling(err))
		}
		if err != nil {
			m.log.Warn("notify: bringing a live card up to date", "guild", c.guild, "channel", c.channel, "err", err)
		}
	}
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

// relive is a live card again, as an edit: it never pings, since the role
// was pinged when the stream started.
func relive(platform string, it item, role snowflake.ID, grant bool) discord.MessageUpdate {
	msg := alert(platform, it, role, grant)
	return discord.MessageUpdate{Components: &msg.Components, AllowedMentions: core.NoPings()}
}
