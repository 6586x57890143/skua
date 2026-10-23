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
	if strings.HasPrefix(body, ">") || strings.HasPrefix(body, "-#") || !strings.Contains(body, "[https://example.com/story](<https://example.com/story>)") {
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
	for _, want := range []string{"<:pp_heart_", "<:stat_repost_", "<:stat_reply_", "<t:1792663980:f>", "[quote · Author](<https://x.com/author/status/100>)"} {
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
	if !strings.Contains(got, "https://evil.example/a") || strings.Contains(got, "(<https://localhost/") {
		t.Fatal(got)
	}
	for _, limit := range []int{0, 3, 4, 20, 100, 1800} {
		got := sourceText(strings.Repeat("😀\nhttps://example.com/a?x=1&y=2 ", 200), limit)
		if len(utf16.Encode([]rune(got))) > limit {
			t.Fatalf("%d: %q", limit, got)
		}
	}
	if got := statLine("3 votes · 1.2K likes · 5 views · unknown"); !strings.Contains(got, "3 votes") || !strings.Contains(got, "1.2K") || !strings.Contains(got, "5 views") {
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
	m.suppressOriginal(context.Background(), r, key, tr, 2, 1)
	if r.attempts != 0 {
		t.Fatal("obsolete revision suppressed")
	}
	for range 120 {
		_ = m.guard.Allow(3, guard.MessageSend)
	}
	m.suppressOriginal(context.Background(), r, key, tr, 1, 1)
	if r.attempts != 0 {
		t.Fatal("unguarded suppression")
	}
	if canSuppress("nothing", nil) {
		t.Fatal("no replacement")
	}
}
