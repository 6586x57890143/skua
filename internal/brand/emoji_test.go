package brand

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/guard"
)

// fakeEmoji is an app's emoji list; creating one appends it.
type fakeEmoji struct {
	have    []discord.Emoji
	created []string
	listErr error
	fail    map[string]error
}

func (f *fakeEmoji) GetApplicationEmojis(snowflake.ID, ...rest.RequestOpt) ([]discord.Emoji, error) {
	return f.have, f.listErr
}

func (f *fakeEmoji) CreateApplicationEmoji(_ snowflake.ID, c discord.EmojiCreate, _ ...rest.RequestOpt) (*discord.Emoji, error) {
	if err := f.fail[c.Name]; err != nil {
		return nil, err
	}
	if c.Image.Type != discord.IconTypePNG || len(c.Image.Data) == 0 {
		return nil, errors.New("no image")
	}
	e := discord.Emoji{ID: snowflake.ID(1000 + len(f.have)), Name: c.Name}
	f.have = append(f.have, e)
	f.created = append(f.created, c.Name)
	return &e, nil
}

// unsynced puts the icons back on attachments when the test ends.
func unsynced(t *testing.T) {
	t.Cleanup(func() { emojiIDs.Store(nil) })
}

func TestEmojiNamesAreValidAndFollowTheArt(t *testing.T) {
	keys := map[string]bool{}
	names := map[string]bool{}
	for _, e := range Emojis() {
		if len(e.Name) < 2 || len(e.Name) > 32 || strings.Trim(e.Name, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
			t.Errorf("%q is not a name Discord takes", e.Name)
		}
		if !strings.HasPrefix(e.Name, e.Key+"_") || names[e.Name] {
			t.Errorf("%q does not follow its key %q, or repeats", e.Name, e.Key)
		}
		keys[e.Key], names[e.Name] = true, true
	}
	for _, key := range []string{"skua_avatar", "skua_ok", "mod_help", "mod_preen"} {
		if !keys[key] {
			t.Errorf("no emoji for %s", key)
		}
	}
}

// The first boot uploads every emoji; the next finds them all and uploads
// nothing; either way every icon is then a CDN URL with nothing attached.
func TestSyncUploadsOnlyWhatIsMissing(t *testing.T) {
	unsynced(t)
	all := Emojis()
	f := &fakeEmoji{have: []discord.Emoji{{ID: 1, Name: "someone_elses"}, {ID: 7, Name: all[0].Name}}}
	if err := Sync(context.Background(), f, 9, guard.New()); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != len(all)-1 {
		t.Fatalf("created %d, want %d: all but the one already there", len(f.created), len(all)-1)
	}
	f.created = nil
	if err := Sync(context.Background(), f, 9, guard.New()); err != nil || len(f.created) != 0 {
		t.Fatalf("second sync created %v (%v), want nothing", f.created, err)
	}

	e, files := Embed(ColorOK, "t", "d")
	if len(files) != 0 || !strings.HasPrefix(e.Thumbnail.URL, "https://cdn.discordapp.com/emojis/") || !strings.HasSuffix(e.Thumbnail.URL, ".png") {
		t.Fatalf("synced embed: thumbnail %q, %d files", e.Thumbnail.URL, len(files))
	}
	for _, get := range []func() (*discord.File, string){Avatar, func() (*discord.File, string) { return ModuleIcon("purge", ColorWarn) }} {
		if file, url := get(); file != nil || !strings.HasPrefix(url, "https://cdn.discordapp.com/emojis/") {
			t.Errorf("synced icon: file %v, url %q", file, url)
		}
	}
	if m := Mention("mod_purge"); !strings.HasPrefix(m, "<:mod_purge_") || !strings.HasSuffix(m, ">") {
		t.Errorf("mention %q", m)
	}
	if Mention("mod_nobody") != "" {
		t.Error("a mention for an emoji that doesn't exist")
	}
	if e := ComponentEmoji("pf_kick"); e == nil || !strings.HasPrefix(e.Name, "pf_kick_") || e.ID == 0 {
		t.Errorf("component emoji %+v", e)
	}
	if ComponentEmoji("mod_nobody") != nil {
		t.Error("a component emoji that doesn't exist")
	}
}

// Whatever could not be uploaded still goes as an attachment, and the rest
// as emoji. A guard that refuses is the same as a failed upload.
func TestSyncFallsBackPerEmoji(t *testing.T) {
	unsynced(t)
	broken := Emojis()[0]
	f := &fakeEmoji{fail: map[string]error{broken.Name: &rest.Error{Response: &http.Response{StatusCode: 400}}}}
	err := Sync(context.Background(), f, 9, guard.New())
	if err == nil || !strings.Contains(err.Error(), broken.Name) {
		t.Fatalf("err = %v, want one naming %s", err, broken.Name)
	}
	if Mention(broken.Key) != "" || Mention(Emojis()[1].Key) == "" {
		t.Fatal("the failed emoji was served, or the others were not")
	}

	g := guard.New()
	for g.Allow(0, guard.EmojiCreate) == nil {
	}
	emojiIDs.Store(nil)
	if err := Sync(context.Background(), &fakeEmoji{}, 9, g); err == nil || !errors.Is(err, guard.ErrRateLimited) {
		t.Fatalf("over budget: %v", err)
	}

	emojiIDs.Store(nil)
	if err := Sync(context.Background(), &fakeEmoji{listErr: errors.New("down")}, 9, guard.New()); err == nil {
		t.Fatal("a failed list was not reported")
	}
	if file, url := Icon(ColorOK); file == nil || url != "attachment://skua_ok.png" {
		t.Fatalf("after a failed list: %v %q, want the attachment", file, url)
	}
}

func TestModuleIconFallsBack(t *testing.T) {
	unsynced(t)
	if f, url := ModuleIcon("purge", ColorWarn); f == nil || url != "attachment://mod_purge.png" {
		t.Fatalf("unsynced module icon: %v %q", f, url)
	}
	if f, url := ModuleIcon("nobody", ColorWarn); f == nil || url != "attachment://skua_warn.png" {
		t.Fatalf("module without art: %v %q, want the mood icon", f, url)
	}
}

func TestStruggling(t *testing.T) {
	for err, want := range map[error]bool{
		nil:             false,
		errors.New("x"): false,
		&rest.Error{Response: &http.Response{StatusCode: 503}}: true,
		&rest.Error{Response: &http.Response{StatusCode: 400}}: false,
	} {
		if struggling(err) != want {
			t.Errorf("struggling(%v) = %v", err, !want)
		}
	}
}

// Replies read the icons while a sync swaps them in: under -race, a read
// never sees a half built map.
func TestIconsReadDuringSync(t *testing.T) {
	unsynced(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			_ = Sync(context.Background(), &fakeEmoji{}, 9, guard.New())
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
			if _, url := Icon(ColorOK); url != "attachment://skua_ok.png" && !strings.HasPrefix(url, "https://cdn.discordapp.com/emojis/") {
				t.Fatalf("icon %q mid-sync", url)
			}
		}
	}
}
