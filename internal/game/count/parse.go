package count

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

// A count is the first word of a message, or its first line when that is
// one expression with spaces in it. Anything else in it is chat.
const (
	maxToken    = 64  // bytes in the word read as a count
	maxLine     = 128 // bytes in a first line read whole as an expression
	maxDigits   = 18  // in one number of an expression
	maxLiterals = 12  // numbers in one expression
	maxDepth    = 8   // nesting of one expression
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
	// also is a number with a ! ("6!"), which is excitement or a
	// factorial: it counts as either. 0 when there is none; a factorial is
	// never 0.
	also int64
}

// read is what text says under r: its first line when that is one
// expression ("(√(6!) × √5)/2"), or else its first word, unless another
// word in it says a number too ("2 3"), which makes the message chat. Read
// as its first, it would count a number the channel didn't mean and fail
// whoever counts the right one next.
func read(text string, r Ruleset) said {
	var first said
	line, rest, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimSpace(strings.TrimRight(strings.Trim(line, "*_`~| \t"), ".?,"))
	words := strings.Fields(rest)
	// Only a line with an operator in it: "3 !" is 3 said loudly.
	if r.Math != MathOff && (r.Format == Decimal || r.Format == FizzBuzz) && len(line) <= maxLine && strings.ContainsAny(line, " \t") && strings.ContainsFunc(line, sign("!")) {
		if v, ok := eval(line); ok {
			first = said{counted: true, value: v, fuzzy: true}
		}
	}
	if !first.counted {
		if words = strings.Fields(text); len(words) == 0 {
			return said{}
		}
		if first, words = word(words[0], r), words[1:]; !first.counted {
			return first
		}
	}
	for _, w := range words {
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
	// expr keeps a closing ! for a factorial ("3!!/4!"); everywhere else a
	// closing ! is excitement.
	expr := strings.TrimRight(tok, ".?,")
	if tok = strings.TrimRight(tok, ".!?,"); tok == "" || len(expr) > maxToken {
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
		var fact int64
		if expr != tok && r.Math != MathOff {
			fact, _ = eval(expr)
		}
		if r.Math == MathOnly {
			// Only the factorial is maths.
			return said{counted: fact != 0, value: fact, fuzzy: true}
		}
		out := plain(tok, 10)
		out.also = fact
		return out
	}
	if r.Math == MathOff {
		return said{}
	}
	v, ok := eval(expr)
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

// eval is the whole number an expression comes to: whole decimal numbers,
// + - * / % with brackets and a sign, powers (^ or **, ² ³) with whole or
// fractional exponents, √ or sqrt, factorials (n!, and n!! is (n!)!, as
// the calculators counting channels grew up on read it) and Γ or gamma of
// a whole number. The usual typed signs count too: × · ÷ −. It works at
// 512 bits and takes a result within 2^-200 of a whole number as that
// number, so √720·√5 is 60. Anything else, a fraction, a division by zero,
// a value past 2^128 or a result past int64 is not a count.
func eval(s string) (int64, bool) {
	// A number in brackets ("(3)") is no sum.
	if !strings.ContainsAny(s, "0123456789") || !strings.ContainsFunc(s, sign("")) {
		return 0, false
	}
	c := &calc{s: []rune(strings.ToLower(s))}
	x, ok := c.expr()
	if !ok || c.peek() != 0 {
		return 0, false
	}
	n, ok := whole(x)
	if !ok || !n.IsInt64() {
		return 0, false
	}
	return n.Int64(), true
}

const (
	prec    = 512 // bits a value is worked at
	maxBits = 128 // a value is under 2^maxBits, and 0 or over 2^-maxBits
	maxRoot = 12  // the largest root a fractional power takes (x^(1/12))
	maxFact = 34  // the largest factorial under 2^maxBits
)

// sign is a predicate for a rune that does maths: anything but a digit, a
// space, a bracket or one of plain.
func sign(plain string) func(rune) bool {
	return func(r rune) bool {
		return !unicode.IsDigit(r) && !unicode.IsSpace(r) && r != '(' && r != ')' && !strings.ContainsRune(plain, r)
	}
}

// calc reads an expression by recursive descent, lowest binding first.
type calc struct {
	s        []rune
	i        int
	depth    int
	literals int
}

// peek is the next rune past any space, 0 at the end.
func (c *calc) peek() rune {
	for c.i < len(c.s) && unicode.IsSpace(c.s[c.i]) {
		c.i++
	}
	if c.i == len(c.s) {
		return 0
	}
	return c.s[c.i]
}

// eat takes the first of toks that comes next.
func (c *calc) eat(toks ...string) bool {
	c.peek()
	for _, tok := range toks {
		t := []rune(tok)
		if len(c.s)-c.i >= len(t) && string(c.s[c.i:c.i+len(t)]) == tok {
			c.i += len(t)
			return true
		}
	}
	return false
}

func (c *calc) expr() (*big.Float, bool) {
	x, ok := c.term()
	for ok {
		neg := false
		switch {
		case c.eat("+"):
		case c.eat("-", "−"):
			neg = true
		default:
			return x, true
		}
		var y *big.Float
		if y, ok = c.term(); ok {
			if neg {
				y.Neg(y)
			}
			x, ok = fit(num().Add(x, y))
		}
	}
	return nil, false
}

func (c *calc) term() (*big.Float, bool) {
	x, ok := c.unary()
	for ok {
		var op rune
		switch {
		case c.eat("*", "×", "·", "⋅"):
			op = '*'
		case c.eat("/", "÷"):
			op = '/'
		case c.eat("%"):
			op = '%'
		default:
			return x, true
		}
		var y *big.Float
		if y, ok = c.unary(); !ok {
			break
		}
		switch op {
		case '*':
			x, ok = fit(num().Mul(x, y))
		case '/':
			if y.Sign() == 0 {
				return nil, false
			}
			x, ok = fit(num().Quo(x, y))
		default:
			// % is for whole numbers only.
			a, okA := whole(x)
			b, okB := whole(y)
			if !okA || !okB || b.Sign() == 0 {
				return nil, false
			}
			x = num().SetInt(a.Rem(a, b))
		}
	}
	return nil, false
}

// unary is a sign, which binds looser than a power: -2^2 is -4.
func (c *calc) unary() (*big.Float, bool) {
	if c.depth++; c.depth > maxDepth {
		return nil, false
	}
	defer func() { c.depth-- }()
	switch {
	case c.eat("+"):
		return c.unary()
	case c.eat("-", "−"):
		x, ok := c.unary()
		if ok {
			x.Neg(x)
		}
		return x, ok
	}
	x, ok := c.postfix()
	if !ok || !c.eat("**", "^") {
		return x, ok
	}
	// Right to left, and the exponent may carry a sign: 2^-1.
	y, ok := c.unary()
	if !ok {
		return nil, false
	}
	return pow(x, y)
}

func (c *calc) postfix() (*big.Float, bool) {
	x, ok := c.primary()
	for ok {
		switch {
		case c.eat("!"):
			x, ok = factorial(x)
		case c.eat("²"):
			x, ok = fit(num().Mul(x, x))
		case c.eat("³"):
			x, ok = fit(num().Mul(x, num().Mul(x, x)))
		default:
			return x, true
		}
	}
	return nil, false
}

func (c *calc) primary() (*big.Float, bool) {
	switch {
	case c.eat("("):
		x, ok := c.expr()
		if !ok || !c.eat(")") {
			return nil, false
		}
		return x, true
	case c.eat("√", "sqrt"):
		// Its operand takes its factorials first: √4! is √24.
		x, ok := c.postfix()
		if !ok || x.Sign() < 0 {
			return nil, false
		}
		return num().Sqrt(x), true
	case c.eat("γ", "gamma"): // Γ, lowered
		x, ok := c.postfix()
		if !ok {
			return nil, false
		}
		n, ok := whole(x)
		if !ok || n.Sign() <= 0 {
			return nil, false
		}
		return factorial(num().SetInt(n.Sub(n, big.NewInt(1))))
	}
	c.peek()
	j := c.i
	for j < len(c.s) && c.s[j] >= '0' && c.s[j] <= '9' {
		j++
	}
	if c.literals++; j == c.i || j-c.i > maxDigits || c.literals > maxLiterals {
		return nil, false
	}
	// Decimal always, leading zeros and all: 08 is 8.
	v, _ := strconv.ParseInt(string(c.s[c.i:j]), 10, 64)
	c.i = j
	return num().SetInt64(v), true
}

func num() *big.Float { return new(big.Float).SetPrec(prec) }

// fit is x, unless it's past what an expression may reach.
func fit(x *big.Float) (*big.Float, bool) {
	// The work is good to about 2^-384 at this size: under 2^-300 is a
	// zero that rounding left ((√2)²-2), and a true value is at least
	// 2^-128, which keeps a fraction far from whole's 2^-200.
	e := x.MantExp(nil)
	if x.Sign() != 0 && e < -300 {
		return x.SetInt64(0), true
	}
	return x, !x.IsInf() && e <= maxBits && (x.Sign() == 0 || e > -maxBits)
}

// whole is the whole number x is, to within the rounding of the work.
func whole(x *big.Float) (*big.Int, bool) {
	half := num().SetFloat64(0.5)
	if x.Sign() < 0 {
		half.Neg(half)
	}
	n, _ := num().Add(x, half).Int(nil) // to nearest, by truncating
	d := num().Sub(x, num().SetInt(n))
	return n, d.Sign() == 0 || d.MantExp(nil) <= -200
}

func factorial(x *big.Float) (*big.Float, bool) {
	n, ok := whole(x)
	if !ok || n.Sign() < 0 || n.Cmp(big.NewInt(maxFact)) > 0 {
		return nil, false
	}
	return fit(num().SetInt(new(big.Int).MulRange(1, n.Int64())))
}

// pow is x^y for a y that is whole or a fraction p/q, q at most maxRoot.
func pow(x, y *big.Float) (*big.Float, bool) {
	for q := int64(1); q <= maxRoot; q++ {
		p, ok := whole(num().Mul(y, num().SetInt64(q)))
		if !ok {
			continue
		}
		if !p.IsInt64() {
			return nil, false
		}
		b, ok := root(x, q)
		if !ok {
			return nil, false
		}
		return ipow(b, p.Int64())
	}
	return nil, false
}

// root is the real qth root of x.
func root(x *big.Float, q int64) (*big.Float, bool) {
	switch {
	case q == 1 || x.Sign() == 0:
		return x, true
	case x.Sign() < 0:
		if q%2 == 0 {
			return nil, false
		}
		r, ok := root(num().Neg(x), q)
		if ok {
			r.Neg(r)
		}
		return r, ok
	case q == 2:
		return num().Sqrt(x), true
	}
	f, _ := x.Float64()
	if f == 0 {
		return nil, false // too small to start from; no count needs it
	}
	// Newton from float64's guess: each step doubles the bits right.
	z := num().SetFloat64(math.Pow(f, 1/float64(q)))
	qf, q1 := num().SetInt64(q), num().SetInt64(q-1)
	for range 10 {
		zq, _ := ipow(z, q-1)
		z = num().Quo(num().Add(num().Mul(q1, z), num().Quo(x, zq)), qf)
	}
	return z, true
}

// ipow is b^p by squaring, stopping as soon as a step is past fit.
func ipow(b *big.Float, p int64) (*big.Float, bool) {
	if p < 0 {
		if b.Sign() == 0 || p == math.MinInt64 {
			return nil, false
		}
		r, ok := ipow(b, -p)
		if !ok {
			return nil, false
		}
		return fit(num().Quo(num().SetInt64(1), r))
	}
	r, ok := num().SetInt64(1), true
	for p > 0 && ok {
		if p&1 == 1 {
			r, ok = fit(num().Mul(r, b))
		}
		if p >>= 1; p > 0 && ok {
			b, ok = fit(num().Mul(b, b))
		}
	}
	return r, ok
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
