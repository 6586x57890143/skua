package count

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// A count is the first word of a message. Anything else in it is chat.
const (
	maxToken    = 64 // bytes in the word read as a count
	maxDigits   = 18 // in one number of an expression
	maxLiterals = 12 // numbers in one expression
	maxDepth    = 8  // nesting of one expression
)

// said is what a message's first word says.
type said struct {
	// counted is false for chat: not a number in the channel's format.
	counted bool
	value   int64
	// word is set for a fizzbuzz word, whose value is the word.
	word string
	// fuzzy is true for an expression, which could be chat (a date, a
	// score): a wrong one is only a miss when it lands near the number.
	fuzzy bool
	// letters is a word with no digit (hex, roman), which could be a word
	// of chat ("I", "add"): a wrong one is chat, unless strict.
	letters bool
	// strict is a letters word that can't be chat: an uppercase numeral of
	// two letters or more ("XIV"). A wrong one near the number is a miss.
	strict bool
	// decimal is digits in a roman channel, which count only past 3999,
	// where numerals stop.
	decimal bool
}

// read is what text says under r: its first word, unless another word
// in it says a number too ("2 3"), which makes the message chat. Read as
// its first, it would count a number the channel didn't mean and fail
// whoever counts the right one next.
func read(text string, r Ruleset) said {
	words := strings.Fields(text)
	if len(words) == 0 {
		return said{}
	}
	first := word(words[0], r)
	if !first.counted {
		return first
	}
	for _, w := range words[1:] {
		// Only a word with a digit can be a second count: a mention, a
		// custom emoji or a timestamp (<...>) is not, and a word of
		// letters is left alone, or in hex and roman every sentence
		// after a count would make it chat.
		if strings.HasPrefix(w, "<") || !strings.ContainsAny(w, "0123456789") {
			continue
		}
		if word(w, r).counted {
			return said{}
		}
	}
	return first
}

// word is what one word says under r.
func word(tok string, r Ruleset) said {
	tok = strings.Trim(tok, "*_`~|")
	tok = strings.TrimRight(tok, ".!?,")
	if tok == "" || len(tok) > maxToken {
		return said{}
	}
	switch r.Format {
	case Binary:
		t := strings.TrimPrefix(strings.ToLower(tok), "0b")
		return plain(t, 2)
	case Hex:
		t := strings.TrimPrefix(strings.ToLower(tok), "0x")
		out := plain(t, 16)
		out.letters = !strings.ContainsAny(t, "0123456789")
		return out
	case Roman:
		if digits(tok) {
			out := plain(tok, 10)
			out.decimal = true
			return out
		}
		v, ok := roman(strings.ToUpper(tok))
		return said{counted: ok, value: v, letters: true, strict: tok == strings.ToUpper(tok) && len(tok) > 1}
	case FizzBuzz:
		switch w := strings.ToLower(tok); w {
		case "fizz", "buzz", "fizzbuzz":
			return said{counted: true, word: w}
		}
	}
	if digits(tok) {
		if r.Math == MathOnly {
			return said{}
		}
		return plain(tok, 10)
	}
	if r.Math == MathOff {
		return said{}
	}
	v, ok := eval(tok)
	return said{counted: ok, value: v, fuzzy: true}
}

// plain reads t as a whole number in base, chat when it isn't one or is
// past int64.
func plain(t string, base int) said {
	if t == "" || t[0] == '+' || t[0] == '-' {
		return said{}
	}
	v, err := strconv.ParseInt(t, base, 64)
	if err != nil {
		return said{}
	}
	return said{counted: true, value: v}
}

func digits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// eval is the whole number an expression comes to, exactly: decimal whole
// numbers, + - * / %, brackets and a sign. Anything else, a fraction, a
// division by zero or a result past int64 is not a count.
func eval(s string) (int64, bool) {
	if !strings.ContainsAny(s, "0123456789") || !strings.ContainsAny(s, "+-*/%") {
		return 0, false
	}
	e, err := parser.ParseExpr(unzero(s))
	if err != nil {
		return 0, false
	}
	n := 0
	v, ok := walk(e, 0, &n)
	if !ok {
		return 0, false
	}
	if v = constant.ToInt(v); v.Kind() != constant.Int {
		return 0, false
	}
	out, exact := constant.Int64Val(v)
	if !exact {
		return 0, false
	}
	return out, true
}

// unzero drops each number's leading zeros, which Go would read as octal
// or refuse ("08").
func unzero(s string) string {
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	b := make([]byte, 0, len(s))
	for i := range len(s) {
		starts := len(b) == 0 || !digit(b[len(b)-1])
		if s[i] == '0' && starts && i+1 < len(s) && digit(s[i+1]) {
			continue
		}
		b = append(b, s[i])
	}
	return string(b)
}

func walk(e ast.Expr, depth int, literals *int) (constant.Value, bool) {
	if depth > maxDepth {
		return nil, false
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		*literals++
		if e.Kind != token.INT || !digits(e.Value) || len(e.Value) > maxDigits || *literals > maxLiterals {
			return nil, false
		}
		// Decimal always: Go would read a leading zero as octal.
		v, err := strconv.ParseInt(e.Value, 10, 64)
		if err != nil {
			return nil, false
		}
		return constant.MakeInt64(v), true
	case *ast.ParenExpr:
		return walk(e.X, depth+1, literals)
	case *ast.UnaryExpr:
		if e.Op != token.SUB && e.Op != token.ADD {
			return nil, false
		}
		x, ok := walk(e.X, depth+1, literals)
		if !ok {
			return nil, false
		}
		return constant.UnaryOp(e.Op, x, 0), true
	case *ast.BinaryExpr:
		switch e.Op {
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM:
		default:
			return nil, false
		}
		x, ok := walk(e.X, depth+1, literals)
		if !ok {
			return nil, false
		}
		y, ok := walk(e.Y, depth+1, literals)
		if !ok {
			return nil, false
		}
		if (e.Op == token.QUO || e.Op == token.REM) && constant.Sign(y) == 0 {
			return nil, false
		}
		if e.Op == token.REM {
			// % is for whole numbers only.
			if x, y = constant.ToInt(x), constant.ToInt(y); x.Kind() != constant.Int || y.Kind() != constant.Int {
				return nil, false
			}
		}
		// QUO on two ints is exact: a fraction stays a fraction.
		return constant.BinaryOp(x, e.Op, y), true
	}
	return nil, false
}

var romans = []struct {
	v int64
	s string
}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}}

// toRoman is v in roman numerals, "" past what they write (1 to 3999).
func toRoman(v int64) string {
	if v < 1 || v > 3999 {
		return ""
	}
	var b strings.Builder
	for _, r := range romans {
		for v >= r.v {
			b.WriteString(r.s)
			v -= r.v
		}
	}
	return b.String()
}

// roman reads a numeral written the one standard way.
func roman(s string) (int64, bool) {
	var v int64
	rest := s
	for _, r := range romans {
		for strings.HasPrefix(rest, r.s) {
			v += r.v
			rest = rest[len(r.s):]
		}
	}
	if rest != "" || v == 0 || toRoman(v) != s {
		return 0, false
	}
	return v, true
}

// Say is how v is written under r: what a member types to count it.
func Say(v int64, r Ruleset) string {
	switch r.Format {
	case Binary:
		return strconv.FormatInt(v, 2)
	case Hex:
		return strconv.FormatInt(v, 16)
	case Roman:
		if s := toRoman(v); s != "" {
			return s
		}
	case FizzBuzz:
		if w := fizz(v); w != "" {
			return w
		}
	}
	return strconv.FormatInt(v, 10)
}

func fizz(v int64) string {
	switch {
	case v%15 == 0:
		return "fizzbuzz"
	case v%3 == 0:
		return "fizz"
	case v%5 == 0:
		return "buzz"
	}
	return ""
}
