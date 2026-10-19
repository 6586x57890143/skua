package filter

import "testing"

func TestKeyCollapsesWhatAReaderSeesAsTheSame(t *testing.T) {
	same := []string{"free nitro here", "FREE  nitro 4 here!!", "ｆｒｅｅ ｎｉｔｒｏ ｈｅｒｅ", "free\u200b nitro h\u200bere", "fréé nitro here", "frее nitrо here"}
	for _, s := range same[1:] {
		if Key(s) != Key(same[0]) {
			t.Errorf("Key(%q) differs from Key(%q)", s, same[0])
		}
	}
	if Key("free nitro there") == Key(same[0]) {
		t.Fatal("different words share a key")
	}
	if Key("hi") != Short || Key("h u") != Short || Key("") != 0 || Key(" \u200b ") != 0 || Key("\U0001F62D") != Short {
		t.Fatal("short and empty keys")
	}
	// No letters: keyed by what shows, a held run counting once.
	sob := Key("\U0001F62D\U0001F525\U0001F62D\U0001F525")
	if sob == 0 || sob == Short || Key("\U0001F62D\U0001F62D\U0001F525\U0001F525\U0001F62D\U0001F525\U0001F525") != sob {
		t.Fatal("emoji keys")
	}
	if Key("1234 !!") == 0 || Key("youuuu are here") != Key("you are here") {
		t.Fatal("digits or held letters")
	}
	if MediaKey(nil) != 0 || MediaKey([]string{"s:1"}) == MediaKey([]string{"s:2"}) || MediaKey([]string{"a:1", "b"}) == MediaKey([]string{"a", "1b"}) {
		t.Fatal("media keys")
	}
	if k := MediaKey([]string{"s:1"}); k == 0 || k == Short || k != MediaKey([]string{"s:1"}) {
		t.Fatal("a media key collides with the reserved keys or moves")
	}
}

func BenchmarkKey(b *testing.B) {
	for b.Loop() {
		Key("just a normal line of chat, nothing to see")
	}
}
