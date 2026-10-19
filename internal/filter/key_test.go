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
	if Key("hi") != Short || Key("h u") != Short || Key("") != 0 || Key("123 !!") != 0 {
		t.Fatal("short and empty keys")
	}
}

func BenchmarkKey(b *testing.B) {
	for b.Loop() {
		Key("just a normal line of chat, nothing to see")
	}
}
