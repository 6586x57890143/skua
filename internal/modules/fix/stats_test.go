package fix

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

func TestProviderStatFormatsUseSkuaEmoji(t *testing.T) {
	if err := brand.Sync(context.Background(), iconRest{}, 9, guard.New()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, stats string
		keys        []string
	}{
		{"tiktok-live", "❤️ 35.2K  💬 5.5K  🔁 1.5K", []string{"stat_likes", "stat_reply", "stat_repost"}},
		{"youtube-community", "👍 1.2K  💬 41", []string{"stat_likes", "stat_reply"}},
		{"youtube-fallback", "👁️ 8.5K • 👍 123", []string{"stat_views", "stat_likes"}},
		{"twitch", "👁️ 1.2K views", []string{"stat_views"}},
		{"twitch-live", "👁️ 1.2K watching", []string{"stat_views"}},
		{"pixiv", "💬 2 ❤️ 34 👁️ 567 🔖 8", []string{"stat_reply", "stat_likes", "stat_views", "stat_bookmarks"}},
		{"bilibili", "💬 2 ❤️ 34 👁️ 567 🪙 8 🔖 9 🔁 10", []string{"stat_reply", "stat_likes", "stat_views", "stat_coins", "stat_bookmarks", "stat_repost"}},
		{"deviantart", "👁️ 1.2K  ❤️ 123  💬 4  ⬇️ 5", []string{"stat_views", "stat_likes", "stat_reply", "stat_downloads"}},
		{"tumblr", "📝 1,234 notes", []string{"stat_notes"}},
		{"word-aliases", "1 convo | 2 shares | 3 favourites | 4 saves", []string{"stat_reply", "stat_repost", "stat_likes", "stat_bookmarks"}},
		{"no-spaces", "❤️35.2K💬5.5K🔁1.5K", []string{"stat_likes", "stat_reply", "stat_repost"}},
		{"count-first", "1 reply · 2 reposts · 3 likes · 4 views", []string{"stat_reply", "stat_repost", "stat_likes", "stat_views"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := statLine(tc.stats)
			for _, key := range tc.keys {
				if mark := brand.Mention(key); mark == "" || strings.Count(got, mark) != 1 {
					t.Errorf("missing or repeated %s: %s", key, got)
				}
			}
			if strings.ContainsAny(got, "❤♥♡👍💬🗨↩🔁🔄↻📤👁👀📺🔖⭐🪙⬇📝") {
				t.Fatal("provider emoji leaked into stats", got)
			}
		})
	}
}

func TestTikTokMetadataRendersCustomFooterAndPreservesCaption(t *testing.T) {
	if err := brand.Sync(context.Background(), iconRest{}, 9, guard.New()); err != nil {
		t.Fatal(err)
	}
	a := apiFor(t, func(*http.Request) (*http.Response, error) {
		return response(`{"success":true,"platform":"tiktok","data":{"authorName":"Creator","caption":"A caption with ❤️ and 💬 stays as written","stats":"❤️ 35.2K  💬 5.5K  🔁 1.5K","video":{"url":"https://cdn.example/video.mp4"}}}`), nil
	})
	l, _ := Parse("https://www.tiktok.com/@creator/video/123")
	p, err := a.Resolve(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	box := card(l, p).Components[0].(discord.ContainerComponent)
	footer := box.Components[len(box.Components)-1].(discord.TextDisplayComponent).Content
	for _, key := range []string{"stat_reply", "stat_repost", "stat_likes"} {
		if !strings.Contains(footer, brand.Mention(key)) {
			t.Fatal("missing custom stat emoji", footer)
		}
	}
	if strings.ContainsAny(footer, "❤💬🔁") || !strings.Contains(box.Components[1].(discord.TextDisplayComponent).Content, "❤️ and 💬") {
		t.Fatal("only engagement stats should be normalized", box)
	}
}

func TestStatMetricsAndBudgetKeepCompleteMentions(t *testing.T) {
	if err := brand.Sync(context.Background(), iconRest{}, 9, guard.New()); err != nil {
		t.Fatal(err)
	}
	stats := "1 replies 2 reposts 3 quotes 4 likes 5 votes 6 views 7 bookmarks 8 coins 9 downloads 10 notes"
	for limit := 0; limit <= 500; limit++ {
		got := statLineLimit(stats, limit)
		if len(utf16.Encode([]rune(got))) > limit || strings.Count(got, "<:") != strings.Count(got, ">") || strings.Count(got, "**")%2 != 0 {
			t.Fatalf("broken or oversized stats at %d: %s", limit, got)
		}
	}
	got := statLine(stats)
	if !strings.Contains(got, "**3** quotes") || !strings.Contains(got, "**5** votes") {
		t.Fatal("distinct quote and vote counters lost", got)
	}
	// Consecutive word-first metrics must not swallow the next metric's label.
	if got := statLine("likes 1 likes 2 replies 3"); !strings.Contains(got, "**2**") || !strings.Contains(got, "**3**") {
		t.Fatal(got)
	}
	if got := statLine("unrecognized @everyone <@123> label"); strings.Contains(got, "@everyone") || strings.Contains(got, "<@") {
		t.Fatal("unsafe metadata", got)
	}
	if got := statLine(""); got != "" {
		t.Fatal(got)
	}
}

type unavailableIconRest struct{ iconRest }

func (unavailableIconRest) GetApplicationEmojis(snowflake.ID, ...rest.RequestOpt) ([]discord.Emoji, error) {
	return nil, context.Canceled
}

func TestUnsyncedStatsUsePlainLabels(t *testing.T) {
	if err := brand.Sync(context.Background(), unavailableIconRest{}, 9, guard.New()); err == nil {
		t.Fatal("expected failed emoji sync")
	}
	t.Cleanup(func() {
		if err := brand.Sync(context.Background(), iconRest{}, 9, guard.New()); err != nil {
			t.Error(err)
		}
	})
	got := statLine("❤️ 10 💬 2 🔁 3 👁️ 4")
	if strings.ContainsAny(got, "❤💬🔁👁") || !strings.Contains(got, "**10** likes") || !strings.Contains(got, "**2** replies") {
		t.Fatal(got)
	}
}

// Opt-in API canary: the real TikTok wire shape must render with application
// emoji. No Discord credentials or message delivery is involved.
func TestLiveTikTokStatEmoji(t *testing.T) {
	if os.Getenv("SKUA_FIX_LIVE") != "1" {
		t.Skip("set SKUA_FIX_LIVE=1 for public API canaries")
	}
	if err := brand.Sync(context.Background(), iconRest{}, 9, guard.New()); err != nil {
		t.Fatal(err)
	}
	a, err := NewAPI(Config{})
	if err != nil {
		t.Fatal(err)
	}
	l, _ := Parse("https://www.tiktok.com/@scout2015/video/6718335390845095173")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	p, err := a.Resolve(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	box := card(l, p).Components[0].(discord.ContainerComponent)
	footer := box.Components[len(box.Components)-1].(discord.TextDisplayComponent).Content
	for _, key := range []string{"stat_likes", "stat_reply", "stat_repost"} {
		if !strings.Contains(footer, brand.Mention(key)) {
			t.Fatal("live TikTok stats missing application emoji", key)
		}
	}
}
