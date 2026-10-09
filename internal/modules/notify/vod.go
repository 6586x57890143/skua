package notify

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// When a stream ends, its live card turns into the stream's VOD where it
// stands: "was live on twitch", how long it ran, and a button to the VOD.

// vod is what an ended stream's card points at.
type vod struct {
	url, image string
	length     time.Duration
}

// vodder is a source that can find an ended stream's VOD.
type vodder interface {
	vod(ctx context.Context, account string, it item) (vod, bool)
}

// keepLive records a live card just posted, so its stream's end can find it.
func (m *Module) keepLive(ctx context.Context, p posted) {
	s := stream{p.k, p.it.ID}
	m.mu.Lock()
	m.live[s] = append(m.live[s], p)
	m.mu.Unlock()
	if err := saveLive(ctx, m.db, p); err != nil {
		m.log.Warn("notify: saving a live card", "account", p.k.account, "err", err)
	}
}

// end turns every card of a stream that has ended into its VOD's card.
func (m *Module) end(ctx context.Context, p Poster, k key, id string, src source) {
	s := stream{k, id}
	m.mu.Lock()
	cards := m.live[s]
	delete(m.live, s)
	m.mu.Unlock()
	if len(cards) == 0 {
		return
	}
	if err := dropLive(ctx, m.db, k, id); err != nil {
		m.log.Warn("notify: forgetting a live card", "account", k.account, "err", err)
	}
	it := cards[0].it
	it.Viewers = max(it.Viewers, cards[0].peak)
	v := m.fallback(k, it, cards[0].at)
	if vd, ok := src.(vodder); ok {
		if got, ok := vd.vod(ctx, k.account, it); ok {
			if got.length == 0 {
				got.length = v.length
			}
			v = got
		}
	}
	// The picture keeps the VOD's own frame, or the stream's last, or a
	// fresh one of its link after a restart, or slate.
	base, _ := m.frame(ctx, v.image)
	m.mu.Lock()
	if base == nil {
		base = m.frames[s]
	}
	delete(m.frames, s)
	m.mu.Unlock()
	if base == nil {
		base, _ = m.frame(ctx, it.Image)
	}
	pic, err := compose(base, headline(it), foot(it, v.length, true))
	if err != nil {
		m.log.Warn("notify: drawing a stream's picture", "account", k.account, "err", err)
	}
	for _, c := range cards {
		if !m.on(c.guild) {
			continue
		}
		err := m.guard.Allow(c.guild, guard.MessageSend)
		if err == nil {
			var file *discord.File
			if pic != nil {
				_, file = shot(it, pic)
			}
			_, err = p.UpdateMessage(c.channel, c.message, ended(k, it, v, c.role, m.canGrant(p, c.guild, c.role), file))
			m.guard.Report(c.guild, struggling(err))
		}
		if err != nil {
			m.log.Warn("notify: turning a live card into its vod", "guild", c.guild, "channel", c.channel, "err", err)
		}
	}
}

// fallback is an ended stream's card when no VOD can be found: the
// platform's videos page, or youtube's own watch page, which becomes the
// VOD, and the time from its start, or from the card, until now.
func (m *Module) fallback(k key, it item, posted time.Time) vod {
	start := it.Started
	if start.IsZero() {
		start = posted
	}
	v := vod{url: it.URL, length: m.now().Sub(start)}
	switch k.platform {
	case "twitch":
		v.url = "https://www.twitch.tv/" + url.PathEscape(k.account) + "/videos"
	case "kick":
		// Kick's public API has no VODs.
		v.url = "https://kick.com/" + url.PathEscape(k.account) + "/videos"
	case "youtube":
		// The live preview, without _live, is the VOD's own.
		v.image = strings.Replace(it.Image, "_live.jpg", ".jpg", 1)
	}
	return v
}

// ended is a live card after its stream: the same card in the past tense,
// its length beside the category, its picture redrawn as over, and a
// button to the VOD.
func ended(k key, it item, v vod, role snowflake.ID, grant bool, pic *discord.File) discord.MessageUpdate {
	was := it
	was.ID, was.URL, was.Image = "", v.url, ""
	if pic != nil {
		was.Image = "attachment://" + shotName
	}
	was.Detail = strings.TrimPrefix(was.Detail+" · "+core.Duration(v.length), " · ")
	if was.Viewers > 0 {
		was.Detail += " · peak " + count(was.Viewers)
	}
	label := "vod"
	if strings.HasSuffix(v.url, "/videos") {
		label = "videos"
	}
	msg := card(brand.ColorEnded, k.platform, "was live on "+k.platform, label, was, role, grant)
	// Editing never pings: the role was pinged when the stream started.
	return withPicture(discord.MessageUpdate{Components: &msg.Components, AllowedMentions: core.NoPings()}, pic)
}

// vod is the stream's archive on twitch: its page, length and thumbnail.
// A streamer can switch archives off; then there is none, and false.
func (t *twitch) vod(ctx context.Context, login string, it item) (vod, bool) {
	var users struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := t.app.call(ctx, t.api+"/users?login="+url.QueryEscape(login), t.header(), &users); err != nil || len(users.Data) == 0 {
		return vod{}, false
	}
	var videos struct {
		Data []struct {
			Stream   string `json:"stream_id"`
			URL      string `json:"url"`
			Duration string `json:"duration"`
			Thumb    string `json:"thumbnail_url"`
		} `json:"data"`
	}
	q := url.Values{"user_id": {users.Data[0].ID}, "type": {"archive"}, "first": {"5"}}
	if err := t.app.call(ctx, t.api+"/videos?"+q.Encode(), t.header(), &videos); err != nil {
		return vod{}, false
	}
	for _, v := range videos.Data {
		if "live:"+v.Stream != it.ID {
			continue
		}
		// Twitch writes durations as Go does: 3h8m33s.
		length, err := time.ParseDuration(v.Duration)
		if err != nil {
			return vod{}, false
		}
		// A thumbnail still being made comes back empty.
		thumb := strings.NewReplacer("%{width}", "1280", "%{height}", "720").Replace(v.Thumb)
		return vod{url: v.URL, image: thumb, length: length}, true
	}
	return vod{}, false
}

// kickVideo is one stream session on kick's site, its VOD from the
// moment it starts.
type kickVideo struct {
	Start    string `json:"start_time"` // UTC, "2006-01-02 15:04:05"
	Duration int64  `json:"duration"`   // ms; 0 while live
	Thumb    struct {
		Src string `json:"src"`
	} `json:"thumbnail"`
	Video struct {
		UUID string `json:"uuid"`
	} `json:"video"`
}

// vod is the stream's VOD on kick: its own page, preview and length. The
// public API has none, so this is the list kick's own site reads, which
// is undocumented: anything it doesn't answer, or answers differently,
// is false, and the card points at the videos page instead.
func (k *kick) vod(ctx context.Context, slug string, it item) (vod, bool) {
	var videos []kickVideo
	h := http.Header{"Accept": {"application/json"}}
	if _, err := get(ctx, k.app.c, k.site+"/api/v2/channels/"+url.PathEscape(slug)+"/videos", h, &videos); err != nil {
		return vod{}, false
	}
	for _, v := range videos {
		start, err := time.Parse(time.DateTime, v.Start)
		if err != nil || v.Video.UUID == "" {
			continue
		}
		// Newest first: without a start of its own, the stream is the newest.
		if !it.Started.IsZero() && start.Sub(it.Started).Abs() > 2*time.Minute {
			continue
		}
		out := vod{url: k.site + "/" + url.PathEscape(slug) + "/videos/" + url.PathEscape(v.Video.UUID), image: v.Thumb.Src}
		// 0 when it ended moments ago and kick hasn't written it yet.
		out.length = time.Duration(v.Duration) * time.Millisecond
		return out, true
	}
	return vod{}, false
}
