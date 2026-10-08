package notify

import (
	"context"
	"net/http"
	"slices"
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
// kick a stream's only a while after it starts.

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

// fill puts a preview on a live stream's cards that went up without one,
// once the platform answers with it. Checked each round until it does.
func (m *Module) fill(ctx context.Context, p Poster, k key, it item) {
	s := stream{k, it.ID}
	m.mu.Lock()
	cards := slices.Clone(m.live[s])
	m.mu.Unlock()
	if len(cards) == 0 || cards[0].it.Image != "" {
		return
	}
	img := m.picture(ctx, it.Image)
	if img == "" {
		return
	}
	m.mu.Lock()
	for i := range m.live[s] {
		m.live[s][i].it.Image = img
	}
	m.mu.Unlock()
	for _, c := range cards {
		if !m.on(c.guild) {
			continue
		}
		was := c.it
		was.Image = img
		err := m.guard.Allow(c.guild, guard.MessageSend)
		if err == nil {
			_, err = p.UpdateMessage(c.channel, c.message, relive(k.platform, was, c.role, m.canGrant(p, c.guild, c.role)))
			m.guard.Report(c.guild, struggling(err))
		}
		if err != nil {
			m.log.Warn("notify: adding a live card's preview", "guild", c.guild, "channel", c.channel, "err", err)
		}
	}
}

// relive is a live card again, as an edit: it never pings, since the role
// was pinged when the stream started.
func relive(platform string, it item, role snowflake.ID, grant bool) discord.MessageUpdate {
	msg := alert(platform, it, role, grant)
	return discord.MessageUpdate{Components: &msg.Components, AllowedMentions: core.NoPings()}
}
