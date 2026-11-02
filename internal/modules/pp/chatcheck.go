package pp

import (
	"math/bits"
	"strconv"
	"strings"
	"unicode"

	"github.com/disgoorg/disgo/discord"
)

// What a message is worth to chat is judged in one pass over its text, with
// nothing allocated, against what the member has said lately:
//
//   - how much it says: the letters and digits in it, lowercased, a key held
//     down past two cut to two, counted once each, so "lolol", a row of
//     emoji or a wall of one key says almost nothing however long it is;
//   - whether it repeats one of their last recentMessages, as that text, so
//     alternating two lines or padding one with punctuation is a repeat;
//   - and whether enough of its words are new: a third of them or more must
//     be missing from the words they've used across their last 8 to 16
//     messages, kept as two 1024 bit sets that take turns.
//
// A member's whole memory is chatMemory, 320 bytes.

// recentMessages is how many of a member's last messages a new one may not
// repeat and still count.
const recentMessages = 8

// wordGen is how many messages fill one set of words before it becomes
// the older one and a fresh set starts.
const wordGen = 8

type wordSet [16]uint64 // 1024 bits

func (w *wordSet) has(h uint64) bool { return w[h>>6&15]&(1<<(h&63)) != 0 }
func (w *wordSet) add(h uint64)      { w[h>>6&15] |= 1 << (h & 63) }

// chatMemory is what chat remembers of a member's messages.
type chatMemory struct {
	recent [recentMessages]uint64
	words  [2]wordSet // the set filling now, then the one before it
	n      uint32     // messages seen
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// judge reads text, remembers it, and says how many different letters and
// digits it holds and whether it's stale: a repeat, or too few new words.
func (c *chatMemory) judge(text string) (distinct int, stale bool) {
	var seen [4]uint64 // the letters and digits in it, by their low 8 bits
	var mine wordSet   // its own words, so a word said twice counts once
	all, word := uint64(fnvOffset), uint64(fnvOffset)
	var last rune
	run, inWord, words, fresh := 0, false, 0, 0
	end := func() {
		if !inWord {
			return
		}
		inWord = false
		h := word ^ word>>29
		word = fnvOffset
		if mine.has(h) {
			return
		}
		mine.add(h)
		words++
		if !c.words[0].has(h) && !c.words[1].has(h) {
			fresh++
		}
	}
	for _, r := range text {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			end()
			last, run = 0, 0
			continue
		}
		r = unicode.ToLower(r)
		if r == last {
			if run++; run > 2 {
				continue
			}
		} else {
			last, run = r, 1
		}
		if !inWord {
			inWord = true
			all = (all ^ ' ') * fnvPrime
		}
		seen[r>>6&3] |= 1 << (r & 63)
		word = (word ^ uint64(r)) * fnvPrime
		all = (all ^ uint64(r)) * fnvPrime
	}
	end()
	for _, s := range seen {
		distinct += bits.OnesCount64(s)
	}

	repeat := false
	for _, h := range c.recent {
		repeat = repeat || h == all
	}
	stale = repeat || fresh == 0 || fresh*3 < words

	c.recent[c.n%recentMessages] = all
	for i, w := range mine {
		c.words[0][i] |= w
	}
	if c.n++; c.n%wordGen == 0 {
		c.words[1], c.words[0] = c.words[0], wordSet{}
	}
	return distinct, stale
}

// saying is what a message says for chat to judge: its text, or for one with
// none, its stickers and files, so posting the same sticker or picture
// again is a repeat like any other. A message with none of them is one
// skua can't read.
func saying(msg discord.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	var b strings.Builder
	for _, st := range msg.StickerItems {
		b.WriteString(" sticker " + st.ID.String())
	}
	for _, a := range msg.Attachments {
		b.WriteString(" file " + a.Filename + " " + strconv.Itoa(a.Size))
	}
	return b.String()
}
