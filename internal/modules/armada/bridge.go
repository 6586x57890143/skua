// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/nbd-wtf/go-nostr"

	"github.com/6586x57890143/skua/internal/concord"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

// sendBy bounds one message's trip across, attachments included.
const sendBy = 2 * time.Minute

// derivePuppet is upstream's puppets.ts: HKDF-SHA256 of the master secret
// with info armada-bridge/puppet/v1/<user id>/<counter>, the first counter
// that gives a valid key winning.
func derivePuppet(master []byte, user string) concord.Key {
	for c := 0; ; c++ {
		b, err := hkdf.Key(sha256.New, master, nil, "armada-bridge/puppet/v1/"+user+"/"+strconv.Itoa(c), 32)
		if err != nil {
			panic(err)
		}
		if k, err := concord.KeyFromSecret([32]byte(b)); err == nil {
			return k
		}
	}
}

func proxyTag(guild, channel, message snowflake.ID) []string {
	return []string{"proxy", fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guild, channel, message), "web"}
}

// toArmada publishes a Discord message into its linked Armada channel as
// its author's puppet.
func (m *Module) toArmada(l *link, msg discord.Message) {
	ctx, cancel := context.WithTimeout(m.ctx, sendBy)
	defer cancel()
	pool, c, f, chans := m.state()
	ch, ok := chans[l.armada]
	if !ok {
		return
	}
	user := msg.Author.ID.String()
	puppet := m.puppet(user)
	// A banned member's puppet never speaks: honest clients drop it anyway.
	if f.IsBanned(puppet.PK) {
		return
	}
	guild := snowflake.ID(l.guild.Load())
	content, tags := m.compose(ctx, snowflake.ID(l.guild.Load()), msg, puppet)
	if content == "" {
		return
	}
	tags = append([][]string{proxyTag(guild, l.discord, msg.ID)}, tags...)
	name := msg.Author.EffectiveName()
	if msg.Member != nil && msg.Member.Nick != nil {
		name = *msg.Member.Nick
	}
	m.syncProfile(ctx, pool, user, name, msg.Author.EffectiveAvatarURL(), puppet)
	m.listPacks(ctx, user, puppet)
	m.joinOnce(ctx, pool, c, user, puppet)
	if ref := msg.MessageReference; ref != nil && ref.MessageID != nil {
		// A NIP-C7 quote naming the rumor and its author. A message from
		// before the bridge has no row and goes as a plain one.
		if rows, _ := m.maps.byMessage(ctx, *ref.MessageID); len(rows) > 0 {
			author := rows[0].Author
			if rows[0].Origin == "discord" {
				author = m.puppet(author).PK
			}
			tags = append(tags, []string{"q", rows[0].Rumor, "", author})
		}
	}
	r, err := concord.NewChat(ch, concord.KindMessage, content, tags, puppet.PK, time.Now().UnixMilli())
	if err != nil {
		return
	}
	w, err := concord.SealChat(r, ch, puppet.SK)
	if err == nil {
		err = pool.Publish(ctx, w)
	}
	if err != nil {
		m.log.Warn("armada: publishing a discord message", "channel", l.discord, "err", err)
		l.tally.fail()
		return
	}
	l.tally.crossedOut()
	if err := m.maps.insert(ctx, row{Message: msg.ID, Channel: l.discord, Rumor: r.ID, Origin: "discord", Author: user}); err != nil {
		m.log.Warn("armada: recording a bridged message", "err", err)
	}
}

// compose is a Discord message's Armada text with its emoji, attachment
// and sticker tags.
func (m *Module) compose(ctx context.Context, guild snowflake.ID, msg discord.Message, puppet concord.Key) (string, [][]string) {
	// A GIF-picker send is the page link alone; it crosses as the GIF.
	if m.klipy != nil && len(msg.Attachments) == 0 {
		if g, ok := m.klipy.gif(ctx, msg.Content); ok {
			return g.URL, [][]string{g.tag()}
		}
	}
	content, pasted := m.pasted(ctx, guild, msg.Content)
	urls, metas := m.rehost(ctx, append(slices.Clip(msg.Attachments), pasted...), puppet)
	surls, lines, smetas := crossStickers(msg.StickerItems)
	if len(lines) > 0 {
		content = strings.TrimSpace(strings.Join(append([]string{content}, lines...), "\n"))
	}
	names := map[string]string{}
	for _, u := range msg.Mentions {
		names[u.ID.String()] = u.EffectiveName()
	}
	return toArmada(content, guild, names, append(urls, surls...)), slices.Concat(emojiTags(content), metas, smetas)
}

// cdnLink is a Discord attachment link pasted into a message's text.
var cdnLink = regexp.MustCompile(`https://(?:cdn\.discordapp\.com|media\.discordapp\.net)/attachments/\d+/\d+/[^\s<>()|*~` + "`" + `]+`)

var refreshURLs = rest.NewEndpoint(http.MethodPost, "/attachments/refresh-urls")

// pasted turns the Discord attachment links in content into attachments to
// rehost, and takes them out of the text. Discord only serves such a link
// signed, re-signs it for its own clients and nowhere else, so pasted as is
// it reaches Armada dead. A link Discord won't refresh stays in the text.
func (m *Module) pasted(ctx context.Context, guild snowflake.ID, content string) (string, []discord.Attachment) {
	links := slices.Compact(slices.Sorted(slices.Values(cdnLink.FindAllString(content, maxAttempts))))
	if len(links) == 0 || m.noRefresh.Load() {
		return content, nil
	}
	var res struct {
		Refreshed []struct{ Original, Refreshed string } `json:"refreshed_urls"`
	}
	if err := m.guard.Allow(guild, guard.AttachmentRefresh); err != nil {
		return content, nil
	}
	err := m.rest.Do(refreshURLs.Compile(nil), map[string][]string{"attachment_urls": links}, &res, rest.WithCtx(ctx))
	m.guard.Report(guild, struggling(err))
	// A 401 or 403 says the endpoint is closed to bots, which holds for the
	// whole app; a retry would only count toward the invalid request ban.
	if refused(err) {
		if !m.noRefresh.Swap(true) {
			m.log.Warn("armada: discord refused refresh-urls; pasted links cross as they are until restart", "err", err)
		}
		return content, nil
	}
	if err != nil {
		m.log.Warn("armada: refreshing pasted discord links", "err", err)
		return content, nil
	}
	var atts []discord.Attachment
	var done []string
	for _, r := range res.Refreshed {
		if !slices.Contains(links, r.Original) || !cdnLink.MatchString(r.Refreshed) {
			continue
		}
		done = append(done, r.Original)
		atts = append(atts, discord.Attachment{URL: r.Refreshed, Filename: lastSegment(r.Original)})
	}
	return stripURLs(content, done), atts
}

// discordEdit publishes a Discord edit as a kind 3302 from the author's
// puppet. Discord also sends an update when a link unfurls, so only a new
// edited timestamp counts. Attachments are rehosted again: Blossom is
// content-addressed, so they keep the URLs the original's tags name.
func (m *Module) discordEdit(l *link, msg discord.Message) {
	if msg.EditedTimestamp == nil {
		return
	}
	m.mu.Lock()
	seen := m.edited[msg.ID].Equal(*msg.EditedTimestamp)
	if !seen {
		if len(m.edited) >= 4096 {
			clear(m.edited)
		}
		m.edited[msg.ID] = *msg.EditedTimestamp
	}
	m.mu.Unlock()
	if seen {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, sendBy)
	defer cancel()
	rows, err := m.maps.byMessage(ctx, msg.ID)
	if err != nil || len(rows) == 0 || rows[0].Origin != "discord" {
		return
	}
	pool, _, f, chans := m.state()
	ch, ok := chans[l.armada]
	puppet := m.puppet(rows[0].Author)
	if !ok || f.IsBanned(puppet.PK) {
		return
	}
	content, tags := m.compose(ctx, snowflake.ID(l.guild.Load()), msg, puppet)
	if content == "" {
		return
	}
	tags = append([][]string{{"e", rows[0].Rumor}, proxyTag(snowflake.ID(l.guild.Load()), l.discord, msg.ID)}, tags...)
	r, err := concord.NewChat(ch, concord.KindEdit, content, tags, puppet.PK, time.Now().UnixMilli())
	if err != nil {
		return
	}
	if w, err := concord.SealChat(r, ch, puppet.SK); err == nil {
		if err := pool.Publish(ctx, w); err != nil {
			m.log.Warn("armada: publishing an edit", "err", err)
			l.tally.fail()
		}
	}
}

// rehost puts Discord's attachments on Blossom, signed by the puppet, and
// returns the URLs for the text and NIP-92 tags describing them. Without
// Blossom, or when an upload fails, the Discord link goes instead.
func (m *Module) rehost(ctx context.Context, atts []discord.Attachment, puppet concord.Key) ([]string, [][]string) {
	var urls []string
	var tags [][]string
	for _, a := range atts {
		if m.blob == nil || a.Size > maxFile {
			urls = append(urls, a.URL)
			continue
		}
		data, served, err := fetch(ctx, m.fetcher, a.URL, maxFile)
		data = scrub(data)
		typ := served
		if a.ContentType != nil && *a.ContentType != "" {
			typ = strings.Split(*a.ContentType, ";")[0]
		} else if typ == "application/octet-stream" {
			if t := mimeFromURL(a.URL); t != "" {
				typ = t
			}
		}
		var u, hash string
		if err == nil {
			u, hash, err = m.blob.upload(ctx, data, typ, puppet.SK)
		}
		if err != nil {
			m.log.Warn("armada: rehosting an attachment; sending its discord link", "err", err)
			urls = append(urls, a.URL)
			continue
		}
		tag := []string{"imeta", "url " + u, "m " + typ, "x " + hash, "size " + strconv.Itoa(len(data))}
		tag = append(tag, "name "+filename(a.URL, typ, a.Filename))
		urls, tags = append(urls, u), append(tags, tag)
	}
	return urls, tags
}

// syncProfile keeps the puppet's kind 0 in step with the member's name and
// avatar, publishing only when they change. A failure is retried on their
// next message.
func (m *Module) syncProfile(ctx context.Context, pool relays, user, name, avatar string, puppet concord.Key) {
	fp := name + "|" + avatar
	m.mu.RLock()
	same := m.synced[user] == fp
	m.mu.RUnlock()
	if same {
		return
	}
	meta, _ := json.Marshal(map[string]string{"name": name, "picture": avatar, "about": "bridged from discord by skua"})
	ev := &nostr.Event{Kind: 0, Content: string(meta), CreatedAt: nostr.Now(), Tags: nostr.Tags{}}
	if ev.Sign(puppet.SK) != nil || pool.Publish(ctx, ev) != nil {
		return
	}
	m.mu.Lock()
	m.synced[user] = fp
	m.mu.Unlock()
}

// joinOnce puts the puppet in Armada's member list the first time it
// speaks in this run.
func (m *Module) joinOnce(ctx context.Context, pool relays, c *concord.Community, user string, puppet concord.Key) {
	m.mu.RLock()
	done := m.joined[user]
	m.mu.RUnlock()
	if done {
		return
	}
	j, err := concord.Join(c, puppet.PK, puppet.SK, time.Now().UnixMilli())
	if err != nil || pool.Publish(ctx, j) != nil {
		return
	}
	m.mu.Lock()
	m.joined[user] = true
	m.mu.Unlock()
}

// discordDelete tombstones the rumor a deleted Discord message became, as
// its puppet. Deleting the copy of an Armada member's message stops at
// Discord: a puppet can't, and must not, delete someone else's rumor.
func (m *Module) discordDelete(l *link, guild, id snowflake.ID) {
	ctx, cancel := context.WithTimeout(m.ctx, sendBy)
	defer cancel()
	rows, err := m.maps.byMessage(ctx, id)
	if err != nil || len(rows) == 0 || rows[0].Origin != "discord" {
		return
	}
	pool, _, _, chans := m.state()
	ch, ok := chans[l.armada]
	if !ok {
		return
	}
	puppet := m.puppet(rows[0].Author)
	tags := [][]string{{"e", rows[0].Rumor}, proxyTag(guild, l.discord, id)}
	r, err := concord.NewChat(ch, concord.KindDelete, "", tags, puppet.PK, time.Now().UnixMilli())
	if err != nil {
		return
	}
	if w, err := concord.SealChat(r, ch, puppet.SK); err == nil {
		if err := pool.Publish(ctx, w); err != nil {
			m.log.Warn("armada: publishing a delete", "err", err)
			l.tally.fail()
		}
	}
}

// discordReaction carries a member's reaction over as a kind 7 from their
// puppet, or its removal as a kind 5 naming that kind 7. A custom emoji
// goes as :name: with a NIP-30 tag to its image on Discord's CDN. skua's
// own reactions are Armada's coming the other way and never go back.
func (m *Module) discordReaction(l *link, r reacted) {
	ctx, cancel := context.WithTimeout(m.ctx, sendBy)
	defer cancel()
	if r.clear {
		// skua's shared reaction went with them, so its rows go too, or
		// the next Armada reactor of that emoji would never be shown.
		// Armada keeps what it has: the reactions were made, and a
		// moderator here is no moderator there.
		if err := m.maps.clearReactions(ctx, r.message, r.emoji.Reaction()); err != nil {
			m.log.Warn("armada: clearing bridged reactions", "err", err)
		}
		return
	}
	if r.user == m.self {
		return
	}
	pool, _, f, chans := m.state()
	ch, ok := chans[l.armada]
	user := r.user.String()
	puppet := m.puppet(user)
	if !ok || f.IsBanned(puppet.PK) {
		return
	}
	guild := snowflake.ID(l.guild.Load())
	emoji := r.emoji.Reaction()
	proxy := proxyTag(guild, l.discord, r.message)
	kind, content, tags := concord.KindDelete, "", [][]string(nil)
	if r.add {
		rows, err := m.maps.byMessage(ctx, r.message)
		if err != nil || len(rows) == 0 {
			return
		}
		kind, content = concord.KindReaction, *r.emoji.Name
		tags = [][]string{{"e", rows[0].Rumor}, {"k", strconv.Itoa(concord.KindMessage)}, proxy}
		if r.emoji.ID != nil {
			content = ":" + content + ":"
			tags = append(tags, []string{"emoji", *r.emoji.Name, emojiURL(r.emoji.ID.String(), r.emoji.Animated)})
		}
		m.syncProfile(ctx, pool, user, r.name, r.avatar, puppet)
		m.listPacks(ctx, user, puppet)
	} else {
		x, ok, err := m.maps.reactionByDiscord(ctx, r.message, user, emoji)
		if err != nil || !ok {
			return
		}
		tags = [][]string{{"e", x.Rumor}, {"k", strconv.Itoa(concord.KindReaction)}, proxy}
	}
	rumor, err := concord.NewChat(ch, kind, content, tags, puppet.PK, time.Now().UnixMilli())
	if err != nil {
		return
	}
	w, err := concord.SealChat(rumor, ch, puppet.SK)
	if err == nil {
		err = pool.Publish(ctx, w)
	}
	if err != nil {
		m.log.Warn("armada: publishing a discord reaction", "channel", l.discord, "err", err)
		l.tally.fail()
		return
	}
	if r.add {
		err = m.maps.addReaction(ctx, reaction{Rumor: rumor.ID, Channel: l.discord, Message: r.message, Emoji: emoji, Origin: "discord", Author: user})
	} else {
		err = m.maps.dropReaction(ctx, concord.Tag(tags, "e"))
	}
	if err != nil {
		m.log.Warn("armada: recording a bridged reaction", "err", err)
	}
}

// armadaReaction shows an Armada member's kind 7 on the Discord copy of
// the message, as skua's own reaction: a webhook can't react. Every Armada
// reactor of one emoji shares that one reaction. A custom emoji whose image
// isn't on Discord's CDN has nothing to react with and stays in Armada.
func (m *Module) armadaReaction(ctx context.Context, l *link, guild snowflake.ID, o *concord.Opened) {
	if _, dup, err := m.maps.reactionByRumor(ctx, o.RumorID, l.discord); err != nil || dup {
		return
	}
	emoji, ok := reactionEmoji(o.Content)
	if !ok {
		emoji, ok = armadaEmoji(o.Content, o.Tags)
	}
	target := concord.Tag(o.Tags, "e")
	if !ok || target == "" {
		return
	}
	rows, err := m.maps.byRumor(ctx, target, l.discord)
	if err != nil || len(rows) == 0 {
		return
	}
	msg := rows[0].Message
	if n, err := m.maps.armadaReactions(ctx, msg, emoji); err != nil {
		return
	} else if n == 0 {
		err := m.react(guild, func() error { return m.rest.AddReaction(l.discord, msg, emoji, rest.WithCtx(ctx)) })
		if err != nil {
			m.log.Warn("armada: reacting for an armada member", "channel", l.discord, "err", err)
			return
		}
	}
	if err := m.maps.addReaction(ctx, reaction{Rumor: o.RumorID, Channel: l.discord, Message: msg, Emoji: emoji, Origin: "armada", Author: o.Author}); err != nil {
		m.log.Warn("armada: recording a bridged reaction", "err", err)
	}
}

// armadaUnreact applies a kind 5 that names a kind 7. Its author may take
// it back, and so may a moderator, which takes a Discord member's reaction
// off too. skua's reaction comes off with the last Armada reactor.
func (m *Module) armadaUnreact(ctx context.Context, l *link, guild snowflake.ID, o *concord.Opened, f *concord.Folded, x reaction) {
	own := x.Origin == "armada" && x.Author == o.Author
	if !own && !f.IsModerator(o.Author) {
		return
	}
	// The row goes first, so the removal Discord echoes back finds nothing
	// to carry over.
	if err := m.maps.dropReaction(ctx, x.Rumor); err != nil {
		return
	}
	var err error
	if x.Origin == "discord" {
		user, perr := snowflake.Parse(x.Author)
		if perr != nil {
			return
		}
		err = m.react(guild, func() error {
			return m.rest.RemoveUserReaction(l.discord, x.Message, x.Emoji, user, rest.WithCtx(ctx))
		})
	} else if n, cerr := m.maps.armadaReactions(ctx, x.Message, x.Emoji); cerr == nil && n == 0 {
		err = m.react(guild, func() error { return m.rest.RemoveOwnReaction(l.discord, x.Message, x.Emoji, rest.WithCtx(ctx)) })
	}
	if err != nil {
		m.log.Warn("armada: removing a bridged reaction", "channel", l.discord, "err", err)
	}
}

// react is one reaction call, held to the guild's reaction cap.
func (m *Module) react(guild snowflake.ID, call func() error) error {
	if err := m.guard.Allow(guild, guard.Reaction); err != nil {
		return err
	}
	err := call()
	m.guard.Report(guild, struggling(err))
	return err
}

// toDiscord posts an Armada message into its linked Discord channel, or
// applies a delete.
func (m *Module) toDiscord(l *link, o *concord.Opened) {
	ctx, cancel := context.WithTimeout(m.ctx, sendBy)
	defer cancel()
	guild := snowflake.ID(l.guild.Load())
	if guild == 0 || (m.gate != nil && !m.gate(guild)) {
		return
	}
	_, _, f, _ := m.state()
	// skua's own posts, anything any bridge published, skua's puppets and
	// banned members never come back through.
	if o.Author == m.primary.PK || concord.HasTag(o.Tags, "proxy") || m.isPuppet(o.Author) || f.IsBanned(o.Author) {
		return
	}
	switch o.Kind {
	case concord.KindDelete:
		m.armadaDelete(ctx, l, guild, o, f)
	case concord.KindMessage, concord.KindComment:
		m.armadaPost(ctx, l, guild, o)
	case concord.KindEdit:
		m.armadaEdit(ctx, l, guild, o)
	case concord.KindReaction:
		m.armadaReaction(ctx, l, guild, o)
	}
}

// armadaEdit applies a kind 3302 to the Discord copies of its rumor, as
// Armada folds it: only the rumor's author may edit, and the newest edit
// wins. A grown message gets new posts for its extra parts; parts a shrunk
// one no longer needs are blanked.
func (m *Module) armadaEdit(ctx context.Context, l *link, guild snowflake.ID, o *concord.Opened) {
	target := concord.Tag(o.Tags, "e")
	rows, err := m.maps.byRumor(ctx, target, l.discord)
	if err != nil || len(rows) == 0 || rows[0].Origin != "armada" || rows[0].Author != o.Author {
		return
	}
	m.mu.Lock()
	stale := m.lastEdit[target] >= o.MS
	if !stale {
		if len(m.lastEdit) >= 4096 {
			clear(m.lastEdit)
		}
		m.lastEdit[target] = o.MS
	}
	m.mu.Unlock()
	if stale {
		return
	}
	var urls []string
	for _, t := range o.Tags {
		if len(t) > 0 && t[0] == "imeta" {
			urls = append(urls, imetaOf(t)["url"])
		}
	}
	text, ok := m.screened(o, toDiscord(stripURLs(o.Content, urls), o.Tags))
	if !ok {
		return // the copies keep what was screened before
	}
	parts := capParts(split(withReply(m.replyLine(ctx, l, guild, rows[0].ReplyTo), text)))
	for i, r := range rows {
		content := "-# removed in an edit"
		if i < len(parts) {
			content = parts[i]
		}
		update := discord.WebhookMessageUpdate{Content: &content, AllowedMentions: core.NoPings()}
		if err := m.post.Edit(ctx, m.rest, guild, l.discord, m.app, r.Webhook, r.Message, update); err != nil {
			m.log.Warn("armada: editing a bridged message", "err", err)
			l.tally.fail()
		}
	}
	if len(parts) <= len(rows) {
		return
	}
	name, avatar := m.profile(ctx, o.Author)
	for i := len(rows); i < len(parts); i++ {
		posted, err := m.post.Send(ctx, m.rest, guild, l.discord, m.app, discord.WebhookMessageCreate{Content: parts[i], Username: name, AvatarURL: avatar, AllowedMentions: core.NoPings()})
		if err != nil {
			m.log.Warn("armada: posting an edit's new part", "err", err)
			l.tally.fail()
			return
		}
		var hook snowflake.ID
		if posted.WebhookID != nil {
			hook = *posted.WebhookID
		}
		_ = m.maps.insert(ctx, row{Message: posted.ID, Channel: l.discord, Webhook: hook, Rumor: target, Origin: "armada", Author: o.Author, Part: i, ReplyTo: rows[0].ReplyTo})
	}
}

func (m *Module) armadaPost(ctx context.Context, l *link, guild snowflake.ID, o *concord.Opened) {
	if rows, err := m.maps.byRumor(ctx, o.RumorID, l.discord); err != nil || len(rows) > 0 {
		return
	}
	name, avatar := m.profile(ctx, o.Author)
	files, urls := m.files(ctx, o.Tags)
	text, ok := m.screened(o, toDiscord(stripURLs(o.Content, urls), o.Tags))
	if !ok || (text == "" && len(files) == 0) {
		return
	}
	target := replyTarget(o)
	parts := capParts(split(withReply(m.replyLine(ctx, l, guild, target), text)))
	if len(parts) == 0 {
		parts = []string{""}
	}
	for i, part := range parts {
		msg := discord.WebhookMessageCreate{Content: part, Username: name, AvatarURL: avatar, AllowedMentions: core.NoPings()}
		if i == 0 {
			msg.Files = files
		}
		posted, err := m.post.Send(ctx, m.rest, guild, l.discord, m.app, msg)
		if err != nil {
			m.log.Warn("armada: posting an armada message", "channel", l.discord, "err", err)
			l.tally.fail()
			return
		}
		if i == 0 {
			l.tally.crossedIn()
		}
		var hook snowflake.ID
		if posted.WebhookID != nil {
			hook = *posted.WebhookID
		}
		if err := m.maps.insert(ctx, row{Message: posted.ID, Channel: l.discord, Webhook: hook, Rumor: o.RumorID, Origin: "armada", Author: o.Author, Part: i, ReplyTo: target}); err != nil {
			m.log.Warn("armada: recording a bridged message", "err", err)
		}
	}
}

// screened is the text as skua's filter lets it be posted, as whisper
// screens member text: slurs rewritten, and a message carrying a phishing
// link, an IP grabber or a token not posted at all. Armada moderates its
// own side; this is what skua is willing to post.
func (m *Module) screened(o *concord.Opened, text string) (string, bool) {
	v := m.screen.Check(text)
	if v.Block != "" {
		m.log.Info("armada: not posting a message", "because", v.Block, "rumor", o.RumorID)
		return "", false
	}
	return v.Text, true
}

// armadaDelete removes the Discord side of each rumor a kind 5 names. It
// counts when the deleter wrote the rumor or holds MANAGE_MESSAGES, so a
// moderator's delete reaches Discord, the member's original included. A
// rumor that was a reaction is taken back as one.
func (m *Module) armadaDelete(ctx context.Context, l *link, guild snowflake.ID, o *concord.Opened, f *concord.Folded) {
	for _, t := range o.Tags {
		if len(t) < 2 || t[0] != "e" || t[1] == "" {
			continue
		}
		if x, ok, err := m.maps.reactionByRumor(ctx, t[1], l.discord); err == nil && ok {
			m.armadaUnreact(ctx, l, guild, o, f, x)
			continue
		}
		rows, err := m.maps.byRumor(ctx, t[1], l.discord)
		if err != nil || len(rows) == 0 {
			continue
		}
		own := rows[0].Origin == "armada" && rows[0].Author == o.Author
		if !own && !f.IsModerator(o.Author) {
			continue
		}
		for _, r := range rows {
			if r.Webhook != 0 {
				err = m.post.Delete(ctx, m.rest, guild, l.discord, m.app, r.Webhook, r.Message)
			} else if err = m.guard.Allow(guild, guard.MessageDelete); err == nil {
				err = m.rest.DeleteMessage(l.discord, r.Message, rest.WithCtx(ctx), rest.WithReason("armada: deleted by a moderator in armada"))
				m.guard.Report(guild, struggling(err))
			}
			if err != nil {
				m.log.Warn("armada: deleting a bridged message", "err", err)
			}
		}
	}
}

func struggling(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	if !ok || re.Response == nil {
		return false
	}
	return re.Response.StatusCode == 429 || re.Response.StatusCode >= 500
}

// refused is a 401 or 403, which is never retried.
func refused(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	return ok && re.Response != nil && (re.Response.StatusCode == 401 || re.Response.StatusCode == 403)
}

// replyTarget is the rumor a message replies to: a kind 9's q tag, or a
// comment's parent.
func replyTarget(o *concord.Opened) string {
	target := concord.Tag(o.Tags, "q")
	if target == "" && o.Kind == concord.KindComment {
		if target = concord.Tag(o.Tags, "e"); target == "" {
			target = concord.Tag(o.Tags, "E")
		}
	}
	return target
}

// replyLine is the subtext under a reply, the way whisper marks its posts:
// who it answers, their name linking the Discord copy. A webhook can't make
// a real Discord reply, and an embed for it outweighed the message. A
// mention can't be a link, so a Discord author is named by their puppet's
// kind 0, as an Armada author is by theirs.
func (m *Module) replyLine(ctx context.Context, l *link, guild snowflake.ID, target string) string {
	if target == "" {
		return ""
	}
	rows, err := m.maps.byRumor(ctx, target, l.discord)
	if err != nil || len(rows) == 0 {
		return ""
	}
	pk := rows[0].Author
	if rows[0].Origin != "armada" {
		pk = m.puppet(pk).PK
	}
	name, _ := m.profile(ctx, pk)
	return fmt.Sprintf("-# replying to [%s](https://discord.com/channels/%s/%s/%s)", escape(name), guild, rows[0].Channel, rows[0].Message)
}

// withReply puts the reply line, when there is one, under the text.
func withReply(line, text string) string {
	if line == "" || text == "" {
		return text + line
	}
	return text + "\n" + line
}

// files downloads an Armada message's attachments for upload, decrypting
// the encrypted ones, and returns the URLs they came from. An attachment
// that fails, is too big or uses an encryption skua can't apply stays a
// link in the text.
func (m *Module) files(ctx context.Context, tags [][]string) ([]*discord.File, []string) {
	var files []*discord.File
	var urls []string
	attempts, held := 0, 0
	for _, t := range tags {
		if len(t) == 0 || t[0] != "imeta" {
			continue
		}
		left := maxHeld - held
		if attempts >= maxAttempts || left <= 0 {
			break
		}
		meta := imetaOf(t)
		u := meta["url"]
		if u == "" {
			continue
		}
		algo, key, nonce, ox := meta.sealedWith()
		if algo == "bad" {
			continue
		}
		limit := min(maxFile, left)
		// size is whatever the sender wrote: advisory, never the bound.
		if size, err := strconv.Atoi(meta["size"]); err == nil && size > limit {
			continue
		}
		attempts++
		data, served, err := fetch(ctx, m.fetcher, u, int64(limit))
		if err == nil && algo != "" {
			data, err = decrypt(data, key, nonce, ox)
		}
		if err != nil {
			continue
		}
		// m wins over the served type: on an encrypted file the response
		// describes the ciphertext.
		typ := meta["m"]
		if typ == "" {
			if typ = mimeFromURL(u); typ == "" {
				typ = served
			}
		}
		data = scrub(data)
		files = append(files, discord.NewFile(filename(u, typ, meta["name"]), "", bytes.NewReader(data)))
		held += len(data)
		urls = append(urls, u)
	}
	return files, urls
}

// profile is an Armada member's display name and avatar, from their kind 0,
// cached for ten minutes. A name Discord refuses for a webhook, or the
// filter blocks, falls back to a short form of their key.
func (m *Module) profile(ctx context.Context, pk string) (string, string) {
	m.mu.RLock()
	p, ok := m.profiles[pk]
	m.mu.RUnlock()
	if ok && time.Since(p.at) < 10*time.Minute {
		return p.name, p.avatar
	}
	pool, _, _, _ := m.state()
	p = profile{name: "nostr:" + pk[:8], at: time.Now()}
	var newest *nostr.Event
	for _, ev := range pool.Query(ctx, nostr.Filter{Kinds: []int{0}, Authors: []string{pk}, Limit: 1}) {
		if ev.PubKey == pk && (newest == nil || ev.CreatedAt > newest.CreatedAt) {
			newest = ev
		}
	}
	if newest != nil {
		var meta struct {
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
			Picture     string `json:"picture"`
		}
		_ = json.Unmarshal([]byte(newest.Content), &meta)
		if n := m.webhookName(meta.DisplayName, meta.Name); n != "" {
			p.name = n
		}
		if u, err := url.Parse(meta.Picture); err == nil && u.Scheme == "https" && u.Host != "" {
			p.avatar = meta.Picture
		}
	}
	m.mu.Lock()
	if len(m.profiles) >= 4096 {
		clear(m.profiles)
	}
	m.profiles[pk] = p
	m.mu.Unlock()
	return p.name, p.avatar
}

// webhookName is the first candidate Discord will take as a webhook's
// name and the filter lets through, trimmed to 80 characters.
func (m *Module) webhookName(candidates ...string) string {
	for _, n := range candidates {
		n = strings.TrimSpace(n)
		if r := []rune(n); len(r) > maxNameChars {
			n = string(r[:maxNameChars])
		}
		low := strings.ToLower(n)
		if n == "" || strings.Contains(low, "discord") || strings.Contains(low, "clyde") || low == "everyone" || low == "here" {
			continue
		}
		if v := m.screen.Check(n); v.Block == "" {
			return v.Text
		}
	}
	return ""
}
