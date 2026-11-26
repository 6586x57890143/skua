package whisper

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
)

// asking is a Translator that answers reply, err and remembers what it was
// asked.
func asking(reply TranslateReply, err error, asked *[]TranslateRequest) Translator {
	return func(_ context.Context, req TranslateRequest) (TranslateReply, error) {
		*asked = append(*asked, req)
		return reply, err
	}
}

// whispered posts one whisper of text through a module asking tr, the fake
// answering the edit's re-read with what was posted, or with something else
// once edited, and waits for the translation.
func whispered(t *testing.T, tr Translator, text string, edited bool) *fake {
	t.Helper()
	f := &fake{}
	m := newWhisper()
	m.Translate = tr
	f.fetched = &discord.Message{Content: text + marker(5)}
	if edited {
		f.fetched.Content = "something newer" + marker(5)
	}
	if _, err := run(t, m, f, opts{text: text, perms: permSend}); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	return f
}

// A foreign whisper is edited in place to the English, with the flag in its
// marker, pinging nobody, and is still the member's to edit.
func TestAForeignWhisperIsTranslated(t *testing.T) {
	var asked []TranslateRequest
	f := &fake{}
	m := newWhisper()
	m.Translate = asking(TranslateReply{Text: "oh oops I meant so naturally", Flag: "🇳🇱"}, nil, &asked)
	f.fetched = &discord.Message{Content: "oh oeps ik bedoelde zo natuurlijk" + marker(5)}
	if _, err := run(t, m, f, opts{text: "oh oeps ik bedoelde zo natuurlijk", perms: permSend}); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	if len(asked) != 1 || asked[0].Text != "oh oeps ik bedoelde zo natuurlijk" || asked[0].Message != "801" || asked[0].User != "5" {
		t.Fatalf("asked %+v", asked)
	}
	if len(f.updates) != 1 {
		t.Fatalf("edits %+v", f.updates)
	}
	u := f.updates[0]
	if want := "oh oops I meant so naturally" + translatedMarker("🇳🇱", 5); *u.Content != want {
		t.Errorf("edited to %q, want %q", *u.Content, want)
	}
	if u.AllowedMentions == nil || u.AllowedMentions.Parse == nil {
		t.Error("a translated whisper can ping")
	}
	if u.Flags == nil || *u.Flags != discord.MessageFlagSuppressEmbeds {
		t.Error("the translation unfurls what the whisper didn't")
	}
	if text, mine := m.byMember(discord.Message{ID: 801, Content: *u.Content}, 5); !mine || text != "oh oops I meant so naturally" {
		t.Errorf("translated whisper isn't its writer's: %q %v", text, mine)
	}
	if _, mine := m.byMember(discord.Message{ID: 999, Content: *u.Content}, 6); mine {
		t.Error("translated whisper is somebody else's")
	}
}

// Everything that isn't a usable English leaves the whisper as typed.
func TestAWhisperIsLeftAsTyped(t *testing.T) {
	for name, tr := range map[string]Translator{
		"english":  func(context.Context, TranslateRequest) (TranslateReply, error) { return TranslateReply{}, nil },
		"no flag":  func(context.Context, TranslateRequest) (TranslateReply, error) { return TranslateReply{Text: "hi"}, nil },
		"link err": func(context.Context, TranslateRequest) (TranslateReply, error) { return TranslateReply{}, errors.New("offline") },
		"a grabber": func(context.Context, TranslateRequest) (TranslateReply, error) {
			return TranslateReply{Text: "free nitro https://grabify.link/abc", Flag: "🇳🇱"}, nil
		},
		"not a flag": func(context.Context, TranslateRequest) (TranslateReply, error) {
			return TranslateReply{Text: "hi", Flag: strings.Repeat("x", 30)}, nil
		},
	} {
		if f := whispered(t, tr, "hallo daar", false); len(f.updates) != 0 {
			t.Errorf("%s: edited %+v", name, f.updates)
		}
	}
	// No translator, nothing asked.
	if f := whispered(t, nil, "hallo daar", false); len(f.updates) != 0 {
		t.Errorf("no translator: edited %+v", f.updates)
	}
}

// A whisper edited or deleted while it was being translated keeps the
// member's newer word.
func TestATranslationDoesNotOverwriteAnEdit(t *testing.T) {
	var asked []TranslateRequest
	tr := asking(TranslateReply{Text: "hello there", Flag: "🇳🇱"}, nil, &asked)
	if f := whispered(t, tr, "hallo daar", true); len(f.updates) != 0 {
		t.Errorf("overwrote an edit: %+v", f.updates)
	}
}
