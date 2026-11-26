package whisper

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
)

// A whisper in another language is put into English where the server has
// a translator: the bird that translates its members' own messages there,
// asked over cygnet's link (TranslateMethod). It decides by that server's
// own settings, so a server that turned translation off, or a member who
// opted out, is honoured. The whisper goes up as typed at once and is
// edited in place when the English comes back; anything else (no
// translator, English, a refusal, a link that is down) leaves it as typed.

// TranslateMethod is the link method that translates a member's message:
// merlin's, which answers by the server's own translation settings.
const TranslateMethod = "merlin.translate"

// TranslateRequest is TranslateMethod's body: the whisper's IDs as decimal
// snowflakes, and its text as posted.
type TranslateRequest struct {
	Guild   string `json:"guild"`
	Channel string `json:"channel"`
	Message string `json:"message"`
	User    string `json:"user"`
	Text    string `json:"text"`
}

// TranslateReply is the answer: Text is "" to leave the whisper alone, Flag
// the language's flag for the marker.
type TranslateReply struct {
	Text     string `json:"text"`
	Language string `json:"language,omitempty"`
	Flag     string `json:"flag,omitempty"`
}

// Translator asks for req's English.
type Translator func(ctx context.Context, req TranslateRequest) (TranslateReply, error)

// maxFlag is the longest marker a language may have: "🌐 " and a name.
const maxFlag = 24

// translateBy bounds the ask and the edit after it.
const translateBy = 30 * time.Second

// translatedMarker is marker for a whisper put into English.
func translatedMarker(flag string, user snowflake.ID) string {
	return "\n-# translated from " + flag + " · whispered through skua by <@" + user.String() + ">"
}

// translated reports whether line is a translated whisper's marker for user.
func translated(line string, user snowflake.ID) bool {
	return strings.HasPrefix(line, "\n-# translated from ") &&
		strings.HasSuffix(line, " · whispered through skua by <@"+user.String()+">")
}

// translate asks for the English of the whisper just posted as sent, and
// swaps it in if the whisper still reads as sent. It runs after the
// interaction is answered, on its own clock.
func (m *Module) translate(r rest.Rest, guild, channel, app snowflake.ID, posted *discord.Message, user snowflake.ID, text string, flags discord.MessageFlags) {
	if m.Translate == nil || posted == nil || posted.WebhookID == nil {
		return
	}
	sent, hook, id := posted.Content, *posted.WebhookID, posted.ID
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), translateBy)
		defer cancel()
		got, err := m.Translate(ctx, TranslateRequest{
			Guild: guild.String(), Channel: channel.String(), Message: id.String(), User: user.String(), Text: text,
		})
		if err != nil || got.Text == "" {
			return
		}
		english := clean(got.Text)
		v := m.screen.Check(english)
		if english == "" || v.Block != "" {
			return
		}
		// A flag, or "🌐 Name" for a language without a country: short.
		flag := strings.TrimSpace(clean(got.Flag))
		if flag == "" || utf8.RuneCountInString(flag) > maxFlag {
			return
		}
		content := v.Text + translatedMarker(flag, user)
		if utf8.RuneCountInString(content) > 2000 {
			return
		}
		// Edited or deleted meanwhile, it is the member's newer word.
		now, err := m.post.Get(ctx, r, channel, app, hook, id)
		if err != nil || now.Content != sent {
			return
		}
		update := discord.WebhookMessageUpdate{Content: &content, AllowedMentions: core.NoPings()}
		if flags.Has(discord.MessageFlagSuppressEmbeds) {
			update.Flags = new(discord.MessageFlagSuppressEmbeds)
		}
		_ = m.post.Edit(ctx, r, guild, channel, app, hook, id, update)
	}()
}
