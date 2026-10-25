package fix

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

type iconRest struct{ rest.Rest }

func (iconRest) GetApplicationEmojis(snowflake.ID, ...rest.RequestOpt) ([]discord.Emoji, error) {
	var out []discord.Emoji
	for i, e := range brand.Emojis() {
		out = append(out, discord.Emoji{ID: snowflake.ID(100 + i), Name: e.Name})
	}
	return out, nil
}

func TestCompactLinkedCard(t *testing.T) {
	if err := brand.Sync(context.Background(), iconRest{}, 9, guard.New()); err != nil {
		t.Fatal(err)
	}
	l, _ := Parse("https://x.com/callebtc/status/123")
	p := Post{Author: "calle", Handle: "callebtc", Avatar: "https://cdn.example/large.png", Text: "I'm tired boss\nhttps://example.com/story", Stats: "7215 likes · 737 reposts · 135 replies", Timestamp: "2026-10-22T10:13:00Z", Provider: "fxembed", Sections: []Section{{Title: "quote · Author", Text: "Quoted words", URL: "https://x.com/author/status/100"}}}
	msg := card(l, p)
	box := msg.Components[0].(discord.ContainerComponent)
	head := box.Components[0].(discord.TextDisplayComponent).Content
	if !strings.Contains(head, "<:pf_x_") || !strings.Contains(head, "**[calle](<"+l.URL+">)**") {
		t.Fatal(head)
	}
	body := box.Components[1].(discord.TextDisplayComponent).Content
	if strings.HasPrefix(body, ">") || strings.HasPrefix(body, "-#") || !strings.Contains(body, "<https://example.com/story>") || strings.Contains(body, "](<") {
		t.Fatal(body)
	}
	raw, _ := json.Marshal(msg)
	for _, unwanted := range []string{"large.png", "open original", `"type":9`, `"type":11`, `"type":2`, " utc"} {
		if strings.Contains(string(raw), unwanted) {
			t.Fatal(unwanted, string(raw))
		}
	}
	all := ""
	for _, c := range box.Components {
		if d, ok := c.(discord.TextDisplayComponent); ok {
			all += d.Content + "\n"
		}
	}
	for _, want := range []string{"<:stat_likes_", "<:stat_repost_", "<:stat_reply_", "<t:1792663980:R>", "[Author](<https://x.com/author/status/100>)"} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %s: %s", want, all)
		}
	}
}

func TestLinkedTextCannotInjectMarkdown(t *testing.T) {
	text := "@everyone <@123> **bold** [trusted](https://evil.example/a) https://example.com/a(b)?q=x and https://localhost/secret"
	got := sourceText(text, 1800)
	for _, bad := range []string{"@everyone", "<@123>", "**bold**", "[trusted]("} {
		if strings.Contains(got, bad) {
			t.Fatal(got)
		}
	}
	if !strings.Contains(got, "<https://evil.example/a>") || !strings.Contains(got, "<https://example.com/a%28b%29?q=x>") || strings.Contains(got, "<https://localhost/") {
		t.Fatal(got)
	}
	for _, limit := range []int{0, 3, 4, 20, 100, 1800} {
		got := sourceText(strings.Repeat("😀\nhttps://example.com/a?x=1&y=2 ", 200), limit)
		if len(utf16.Encode([]rune(got))) > limit {
			t.Fatalf("%d: %q", limit, got)
		}
	}
	if got := statLine("3 votes · 1.2K likes · 5 views · unknown"); !strings.Contains(got, "3 votes") || !strings.Contains(got, "1.2K") || !strings.Contains(got, "5") {
		t.Fatal(got)
	}
}

type suppressionRest struct {
	*recordingRest
	target   snowflake.ID
	refusal  error
	attempts int
}

func (r *suppressionRest) UpdateMessage(channel, id snowflake.ID, msg discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error) {
	if msg.Flags != nil && msg.Components == nil {
		r.target = id
		r.attempts++
		if r.refusal != nil {
			return nil, r.refusal
		}
	}
	return r.recordingRest.UpdateMessage(channel, id, msg, opts...)
}

func TestSuppressOnlySuccessfullyReplacedMessages(t *testing.T) {
	const url = "https://x.com/user/status/123"
	for _, tc := range []struct {
		name, text                              string
		fallback, deliveryFail, denied, already bool
		want                                    int
	}{
		{name: "success", text: url, want: 1},
		{name: "unrelated link", text: url + " https://example.com/article", want: 0},
		{name: "beyond cap", text: url + " https://x.com/user/status/124 https://x.com/user/status/125 https://x.com/user/status/126", want: 0},
		{name: "metadata unavailable", text: url, fallback: true, want: 0},
		{name: "delivery failure", text: url, deliveryFail: true, want: 0},
		{name: "permission refused", text: url, denied: true, want: 1},
		{name: "already hidden", text: url, already: true, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := module()
			if tc.fallback {
				m.api = resolverFunc(func(context.Context, Link) (Post, error) { return Post{}, errors.New("metadata") })
			}
			r := &suppressionRest{recordingRest: &recordingRest{}}
			if tc.deliveryFail {
				r.err = errors.New("delivery")
			}
			if tc.denied {
				r.refusal = discordError(403)
			}
			flags := discord.MessageFlagHasThread
			if tc.already {
				flags |= discord.MessageFlagSuppressEmbeds
			}
			key := source{3, 4, 10}
			m.queue(r, key, tc.text, false, flags)
			idle(t, m)
			if r.attempts != tc.want {
				t.Fatalf("suppression calls %d want %d", r.attempts, tc.want)
			}
			if tc.want > 0 && r.target != 10 {
				t.Fatal("edited the wrong message")
			}
			for _, u := range r.suppressed {
				if *u.Flags != flags|discord.MessageFlagSuppressEmbeds || u.Content != nil || u.Components != nil || u.Embeds != nil {
					t.Fatalf("modified original payload: %+v", u)
				}
			}
			// Discord echoes flags with the source's previous edited_timestamp. The
			// unchanged content must not refetch or send another card/suppression.
			before, updates, _ := r.totals()
			m.queue(r, key, tc.text, true, flags|discord.MessageFlagSuppressEmbeds)
			idle(t, m)
			after, nextUpdates, _ := r.totals()
			if before != after || updates != nextUpdates || r.attempts != tc.want {
				t.Fatal("suppression echo retriggered conversion")
			}
		})
	}
}

func TestSuppressionGuardAndObsoleteRevision(t *testing.T) {
	m := module()
	r := &suppressionRest{recordingRest: &recordingRest{}}
	key := source{3, 4, 10}
	tr := &tracking{version: 1, completed: 1, suppressible: true}
	m.suppressOriginal(context.Background(), r, key, tr, 2, []Link{{URL: "https://x.com/user/status/123"}})
	if r.attempts != 0 {
		t.Fatal("obsolete revision suppressed")
	}
	for range 600 {
		_ = m.guard.Allow(3, guard.FixCard)
	}
	m.suppressOriginal(context.Background(), r, key, tr, 1, []Link{{URL: "https://x.com/user/status/123"}})
	if r.attempts != 0 {
		t.Fatal("unguarded suppression")
	}
	if canSuppress("nothing", nil) {
		t.Fatal("no replacement")
	}
}

func TestEverySupportedPlatformHasAnIcon(t *testing.T) {
	if err := brand.Sync(context.Background(), iconRest{}, 9, guard.New()); err != nil {
		t.Fatal(err)
	}
	platforms := map[string]bool{"x": true, "bluesky": true}
	for _, p := range platformHosts {
		platforms[p] = true
	}
	for _, p := range shortHosts {
		platforms[p] = true
	}
	for p := range platforms {
		if brand.Mention("pf_"+p) == "" {
			t.Errorf("no header icon for %s", p)
		}
	}
}

func TestEditsRestoreOnlyBotOwnedSuppression(t *testing.T) {
	const original = "https://x.com/user/status/123"
	for _, which := range []string{"unsupported", "removed", "fallback", "preexisting", "restore refused", "restore transient"} {
		t.Run(which, func(t *testing.T) {
			m := module()
			r := &suppressionRest{recordingRest: &recordingRest{}}
			key := source{3, 4, 10}
			flags := discord.MessageFlags(0)
			if which == "preexisting" {
				flags = discord.MessageFlagSuppressEmbeds
			}
			m.queue(r, key, original, false, flags)
			idle(t, m)
			text := original + " https://example.com/article"
			if which == "removed" {
				text = "the link is gone"
			}
			if which == "fallback" {
				text = "https://x.com/user/status/124"
				m.api = resolverFunc(func(context.Context, Link) (Post, error) { return Post{}, errors.New("metadata") })
			}
			if which == "restore refused" {
				r.refusal = discordError(403)
			}
			if which == "restore transient" {
				r.refusal = discordError(503)
			}
			m.queue(r, key, text, true, discord.MessageFlagSuppressEmbeds)
			idle(t, m)
			m.mu.Lock()
			tr := m.tracked[key]
			owned, dirty := tr.ownsSuppression, tr.dirty
			if m.wake != nil {
				m.wake.Stop()
				m.wake = nil
			}
			m.mu.Unlock()
			if which == "preexisting" {
				if r.attempts != 0 {
					t.Fatal("touched author-owned flag")
				}
				return
			}
			if r.attempts != 2 {
				t.Fatalf("calls=%d", r.attempts)
			}
			if which == "restore refused" || which == "restore transient" {
				if !owned || dirty != (which == "restore transient") {
					t.Fatalf("ownership=%v retry=%v", owned, dirty)
				}
				return
			}
			if owned || len(r.suppressed) != 2 || r.suppressed[1].Flags.Has(discord.MessageFlagSuppressEmbeds) {
				t.Fatal("native previews not restored")
			}
		})
	}
}

type blockedSuppressionRest struct {
	*recordingRest
	started, release chan struct{}
	first            bool
}

func (r *blockedSuppressionRest) UpdateMessage(ch, id snowflake.ID, msg discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error) {
	if msg.Flags != nil && msg.Flags.Has(discord.MessageFlagSuppressEmbeds) && !r.first {
		r.first = true
		close(r.started)
		<-r.release
	}
	return r.recordingRest.UpdateMessage(ch, id, msg, opts...)
}
func TestStaleSuccessfulSuppressionIsRestored(t *testing.T) {
	m := module()
	r := &blockedSuppressionRest{recordingRest: &recordingRest{}, started: make(chan struct{}), release: make(chan struct{})}
	key := source{3, 4, 10}
	m.queue(r, key, "https://x.com/user/status/123", false)
	<-r.started
	// Discord accepted the write despite cancellation while this edit arrived.
	m.queue(r, key, "https://x.com/user/status/123 https://example.com/article", true)
	close(r.release)
	idle(t, m)
	if len(r.suppressed) != 2 || r.suppressed[1].Flags.Has(discord.MessageFlagSuppressEmbeds) {
		t.Fatal("lost stale successful side effect")
	}
}

func TestRedditTitleAndProfileHaveDistinctDestinations(t *testing.T) {
	l, _ := Parse("https://reddit.com/r/test/comments/abc/title/")
	for _, p := range []Post{
		{Author: "u/creator", Title: "A video", AuthorURL: "https://www.reddit.com/user/creator/"},
		{Author: "u/creator", Title: "A video"},
		{Handle: "u/creator", Title: "A video", AuthorURL: "https://evil.example/user/creator/"},
	} {
		box := card(l, p).Components[0].(discord.ContainerComponent)
		head := box.Components[0].(discord.TextDisplayComponent).Content
		title := head
		if !strings.Contains(head, "https://www.reddit.com/user/creator/") || !strings.Contains(title, "[A video](<"+l.URL+">)") {
			t.Fatalf("%s / %s", head, title)
		}
	}
	if redditProfile(Post{Author: "[deleted]", AuthorURL: "https://reddit.com.evil.example/user/a"}) != "" {
		t.Fatal("invalid profile")
	}
	box := card(l, Post{Author: "[deleted]"}).Components[0].(discord.ContainerComponent)
	if !strings.Contains(box.Components[0].(discord.TextDisplayComponent).Content, l.URL) {
		t.Fatal("missing fallback source link")
	}
}

func TestRedditFallbackPointsToPostTitle(t *testing.T) {
	l, _ := Parse("https://reddit.com/r/test/comments/abc/title/")
	raw, _ := json.Marshal(fallback(l))
	if !strings.Contains(string(raw), "follow the post title") || strings.Contains(string(raw), "follow the author link") {
		t.Fatal(string(raw))
	}
}

func TestCommandUsesOneSkuaLoadingCardThenReplacesIt(t *testing.T) {
	if err := brand.Sync(context.Background(), iconRest{}, 9, guard.New()); err != nil {
		t.Fatal(err)
	}
	m := module()
	e, sent := slash(t, "https://x.com/user/status/123", false)
	r := &recordingRest{}
	e.Client().Rest = r
	if err := m.command(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 || len(r.updates) != 1 {
		t.Fatal("loading added extra messages or edits")
	}
	raw, _ := json.Marshal((*sent)[0])
	if !strings.Contains(string(raw), "<a:fix_loading_") && !strings.Contains(string(raw), `\u003ca:fix_loading_`) {
		t.Fatal(string(raw))
	}
	done, _ := json.Marshal(r.updates[0])
	if strings.Contains(string(done), "fetching post") || strings.Contains(string(done), "fix_loading") {
		t.Fatal("loading indicator persisted")
	}
	if (*sent)[0].Flags&discord.MessageFlagEphemeral == 0 || (*sent)[0].AllowedMentions == nil {
		t.Fatal("loading privacy or mention policy changed")
	}
}

func TestPresentationStateAndCompactCounts(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{{"950", "950"}, {"1200", "1.2k"}, {"7215", "7.2k"}, {"12000", "12k"}, {"1300000", "1.3m"}, {"2100000000", "2.1b"}, {"1.2K", "1.2K"}, {"9,500", "9.5k"}} {
		if got := compactCount(tc.raw); got != tc.want {
			t.Errorf("%s=%s want %s", tc.raw, got, tc.want)
		}
	}
	for platform := range map[string]bool{"twitter": true, "bluesky": true, "reddit": true, "instagram": true, "threads": true, "pixiv": true, "bilibili": true, "pinterest": true, "tumblr": true, "deviantart": true, "tiktok": true, "twitch": true, "youtube": true} {
		l := Link{Platform: platform}
		if accent(l, Post{}) == 0 || accent(l, Post{Provider: "unavailable"}) != brand.ColorError || accent(l, Post{Incomplete: true}) != brand.ColorWarn || accent(l, Post{Sensitive: true}) != brand.ColorIdle {
			t.Fatal(platform)
		}
	}
	for _, tc := range []struct {
		l                 Link
		url, handle, want string
	}{
		{Link{Platform: "twitter"}, "https://x.com/author", "author", "https://x.com/author"},
		{Link{Platform: "twitter"}, "https://evil.example/author", "author", "https://x.com/author"},
		{Link{Platform: "twitter"}, "https://x.com/author/status/123", "bad name", ""},
		{Link{Platform: "bluesky"}, "https://bsky.app/profile/example.com", "example.com", "https://bsky.app/profile/example.com"},
		{Link{Platform: "bluesky"}, "https://evil.example/", "example.com", "https://bsky.app/profile/example.com"},
		{Link{Platform: "bluesky"}, "https://%zz", "evil/name", ""},
	} {
		if got := profile(tc.l, tc.url, tc.handle); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
}

func TestFooterStaysVisibleAndQuoteHasOwnIdentity(t *testing.T) {
	l, _ := Parse("https://x.com/author/status/123")
	p := Post{Author: "author", Text: strings.Repeat("line\n", 2000), Stats: strings.Repeat("1 likes · 2 reposts · 3 replies · 4 views · unknown · ", 100), Provider: "fxembed", Timestamp: "2026-10-22T10:13:00Z", Sections: []Section{{Kind: "quote", Author: "another", Handle: "another", URL: "https://x.com/another/status/124", AuthorURL: "https://x.com/another", Text: "A quoted post"}}}
	box := card(l, p).Components[0].(discord.ContainerComponent)
	footer := box.Components[len(box.Components)-1].(discord.TextDisplayComponent).Content
	if !strings.HasSuffix(footer, "fxembed · <t:1792663980:R>") {
		t.Fatal(footer)
	}
	block := sectionText(l, p.Sections[0], 500)
	if !strings.Contains(block, "quoted post") || !strings.Contains(block, "[another](<https://x.com/another/status/124>)") || !strings.Contains(block, "https://x.com/another>") {
		t.Fatal(block)
	}
	for _, limit := range []int{20, 40, 100, 500} {
		if got := sectionText(l, p.Sections[0], limit); len(utf16.Encode([]rune(got))) > limit {
			t.Fatalf("section exceeded %d: %s", limit, got)
		}
	}
}

type finalDeliveryRest struct {
	*recordingRest
	failures int
	errFirst error
}

func (r *finalDeliveryRest) UpdateInteractionResponse(app snowflake.ID, token string, msg discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error) {
	if r.failures > 0 {
		r.failures--
		return nil, r.errFirst
	}
	return r.recordingRest.UpdateInteractionResponse(app, token, msg, opts...)
}
func TestFailedDeliveryEndsLoadingWithoutAnotherPublicMessage(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 503} {
		m := module()
		e, sent := slash(t, "https://x.com/user/status/123", true)
		r := &finalDeliveryRest{recordingRest: &recordingRest{}, failures: 1, errFirst: discordError(status)}
		e.Client().Rest = r
		err := m.command(context.Background(), e)
		terminal := status == 400 || status == 503
		if (err == nil) != terminal {
			t.Fatalf("%d: %v", status, err)
		}
		if len(*sent) != 1 {
			t.Fatal("created another message")
		}
		if terminal {
			if len(r.updates) != 1 {
				t.Fatal("loading card not replaced")
			}
			raw, _ := json.Marshal(r.updates[0])
			if !strings.Contains(string(raw), "preview unavailable") || strings.Contains(string(raw), "fix_loading") {
				t.Fatal(string(raw))
			}
		} else if len(r.updates) != 0 {
			t.Fatal("retried permanent refusal")
		}
	}
}

func TestQuoteBudgetNeverCutsLinkDestinations(t *testing.T) {
	l, _ := Parse("https://x.com/a/status/123")
	s := Section{Kind: "quote", Author: "A quoted author", Handle: "author", URL: "https://x.com/author/status/1234567890", Text: "Words https://example.com/article\nMore words"}
	for limit := 20; limit <= 500; limit++ {
		got := sectionText(l, s, limit)
		if len(utf16.Encode([]rune(got))) > limit || strings.Count(got, "](<") != strings.Count(got, ">)") {
			t.Fatalf("limit%d: %s", limit, got)
		}
	}
}

func TestInstagramCaptionDoesNotRepeatAsHeadline(t *testing.T) {
	for _, tc := range []struct {
		platform, title, body string
		headline              bool
	}{
		{"instagram", "If you're looking for a sign...", "If you're looking for a sign this is it. Send this to your friends.", false},
		{"instagram", "Only available title", "", true},
		{"twitch", "Distinct clip title", "Clip context", true},
	} {
		msg := card(Link{Platform: tc.platform, URL: "https://instagram.com/reel/abc"}, Post{Author: "creator", Title: tc.title, Text: tc.body, Provider: "fixembed"})
		found := false
		bodies := 0
		for _, component := range msg.Components[0].(discord.ContainerComponent).Components {
			if text, ok := component.(discord.TextDisplayComponent); ok {
				if text.Content == "**"+clean(tc.title, 200)+"**" {
					found = true
				}
				if tc.body != "" && text.Content == sourceText(tc.body, 1800) {
					bodies++
				}
			}
		}
		if found != tc.headline || (tc.body != "" && bodies != 1) {
			t.Fatalf("%s: headline=%v body count=%d", tc.platform, found, bodies)
		}
	}
}
