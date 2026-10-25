package semantics

import (
	"math"
	"math/big"
	"math/rand/v2"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// randomInt returns an int within ±MaxInt of a random magnitude, so that
// products and sums near the bound are drawn as often as small ones.
func randomInt(rng *rand.Rand) int64 {
	n := rng.Int64N(int64(1) << (1 + rng.IntN(53)))
	if n > MaxInt {
		n = MaxInt
	}
	if rng.IntN(2) == 0 {
		n = -n
	}
	return n
}

// TestIntArithmetic checks the int operations against exact arithmetic
// (big.Int): each gives the exact result when it is within ±MaxInt, and
// IntegerOverflow and 0 otherwise, so that none wraps, for operands near the
// bound as well as small ones, and that an operand beyond the bound, which no
// int is, overflows.
func TestIntArithmetic(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(38, 1))
	bound := big.NewInt(MaxInt)
	for range 200_000 {
		a, b := randomInt(rng), randomInt(rng)
		for _, op := range []struct {
			name  string
			apply func(a, b int64) (int64, RuntimeError)
			exact func(z, x, y *big.Int) *big.Int
		}{
			{"+", AddInt, (*big.Int).Add},
			{"-", SubInt, (*big.Int).Sub},
			{"*", MulInt, (*big.Int).Mul},
		} {
			want := op.exact(new(big.Int), big.NewInt(a), big.NewInt(b))
			got, problem := op.apply(a, b)
			if new(big.Int).Abs(want).Cmp(bound) > 0 {
				if problem != IntegerOverflow || got != 0 {
					t.Fatalf("%d %s %d = %d, %q; want 0, integer_overflow", a, op.name, b, got, problem)
				}
			} else if problem != "" || got != want.Int64() {
				t.Fatalf("%d %s %d = %d, %q; want %s", a, op.name, b, got, problem, want)
			}
		}
		if b != 0 {
			q, problem := DivInt(a, b)
			r, problem2 := ModInt(a, b)
			if problem != "" || problem2 != "" || q*b+r != a || r != 0 && (r < 0) != (a < 0) || abs(r) >= abs(b) {
				t.Fatalf("%d / %d = %d, %d %% %d = %d, %q, %q", a, b, q, a, b, r, problem, problem2)
			}
		}
	}
	for _, n := range []int64{MaxInt + 1, -MaxInt - 1, math.MaxInt64, math.MinInt64} {
		for name, op := range map[string]func(a, b int64) (int64, RuntimeError){"+": AddInt, "-": SubInt,
			"*": MulInt, "/": DivInt, "%": ModInt} {
			if got, problem := op(n, 1); problem != IntegerOverflow || got != 0 {
				t.Errorf("%d %s 1 = %d, %q, though %[1]d is no int", n, name, got, problem)
			}
			if got, problem := op(1, n); problem != IntegerOverflow || got != 0 {
				t.Errorf("1 %s %d = %d, %q, though %[2]d is no int", name, n, got, problem)
			}
		}
		if got, problem := NegInt(n); problem != IntegerOverflow || got != 0 {
			t.Errorf("-%d = %d, %q, though %[1]d is no int", n, got, problem)
		}
	}
}

// randomFloat returns a finite double of random bits, so that every
// exponent, subnormals among them, is drawn, and never -0.
func randomFloat(rng *rand.Rand) float64 {
	for {
		f := math.Float64frombits(rng.Uint64())
		if !math.IsNaN(f) && !math.IsInf(f, 0) && f != 0 {
			return f
		}
	}
}

// TestFloatToInt checks Trunc, Floor, Ceil, and Round against exact
// rounding of the double (big.Float), and that each overflows exactly when
// the rounded value is beyond ±MaxInt.
func TestFloatToInt(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(38, 2))
	bound := new(big.Float).SetInt64(MaxInt)
	half := big.NewFloat(0.5)
	for i := range 200_000 {
		f := randomFloat(rng)
		if i%2 == 0 {
			// As often a float near the ints, and halfway between two.
			f = float64(randomInt(rng)) + []float64{0, 0.5, -0.5, 0.25, 0.75}[rng.IntN(5)]
		}
		x := new(big.Float).SetFloat64(f)
		// The exact roundings, as integers: toward zero, down, up, and half
		// away from zero.
		trunc, _ := x.Int(nil)
		floor := new(big.Int).Set(trunc)
		ceil := new(big.Int).Set(trunc)
		if new(big.Float).SetInt(trunc).Cmp(x) != 0 {
			if f < 0 {
				floor.Sub(floor, big.NewInt(1))
			} else {
				ceil.Add(ceil, big.NewInt(1))
			}
		}
		// With the precision that |x| + 0.5 needs to be exact, which a
		// double's 53 bits would round.
		away := new(big.Float).SetPrec(2200).Add(new(big.Float).Abs(x), half)
		round, _ := away.Int(nil)
		if f < 0 {
			round.Neg(round)
		}
		for _, c := range []struct {
			name  string
			apply func(float64) (int64, RuntimeError)
			want  *big.Int
		}{{"Trunc", Trunc, trunc}, {"Floor", Floor, floor}, {"Ceil", Ceil, ceil}, {"Round", Round, round}} {
			got, problem := c.apply(f)
			if new(big.Float).SetInt(new(big.Int).Abs(c.want)).Cmp(bound) > 0 {
				if problem != IntegerOverflow || got != 0 {
					t.Fatalf("%s(%v) = %d, %q; want 0, integer_overflow", c.name, f, got, problem)
				}
			} else if problem != "" || got != c.want.Int64() {
				t.Fatalf("%s(%v) = %d, %q; want %s", c.name, f, got, problem, c.want)
			}
		}
	}
}

// TestFloatArithmetic checks that the float operations give IEEE 754's
// result where it is finite, never -0, and FloatNotFinite and 0 where it is
// not, and DivisionByZero and 0 for a divisor of zero.
func TestFloatArithmetic(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(38, 3))
	for range 200_000 {
		a, b := randomFloat(rng), randomFloat(rng)
		for _, c := range []struct {
			name      string
			got, want float64
			problem   RuntimeError
		}{
			{"+", 0, a + b, ""}, {"-", 0, a - b, ""}, {"*", 0, a * b, ""}, {"/", 0, a / b, ""},
		} {
			switch c.name {
			case "+":
				c.got, c.problem = AddFloat(a, b)
			case "-":
				c.got, c.problem = SubFloat(a, b)
			case "*":
				c.got, c.problem = MulFloat(a, b)
			case "/":
				c.got, c.problem = DivFloat(a, b)
			}
			switch {
			case math.IsInf(c.want, 0) || math.IsNaN(c.want):
				if c.problem != FloatNotFinite || math.Float64bits(c.got) != 0 {
					t.Fatalf("%v %s %v = %v, %q; want 0, float_not_finite", a, c.name, b, c.got, c.problem)
				}
			case c.want == 0:
				if c.problem != "" || math.Float64bits(c.got) != 0 {
					t.Fatalf("%v %s %v = %v, %q; want 0", a, c.name, b, c.got, c.problem)
				}
			case c.problem != "" || c.got != c.want:
				t.Fatalf("%v %s %v = %v, %q; want %v", a, c.name, b, c.got, c.problem, c.want)
			}
		}
		if got, problem := DivFloat(a, 0); problem != DivisionByZero || math.Float64bits(got) != 0 {
			t.Fatalf("%v / 0 = %v, %q; want 0, division_by_zero", a, got, problem)
		}
	}
	if got, problem := NegFloat(0); problem != "" || math.Signbit(got) {
		t.Errorf("-0.0 = %v, %q; want 0", got, problem)
	}
}

// fusing holds operands whose exact product, 1 - 2^-60, rounds to 1, so that
// adding -1 to the rounded product gives 0, and adding it to the exact one,
// as a fused multiply-add does, gives -2^-60. They are a variable's, so that
// the compiler does not fold the product as a constant.
var fusing = struct{ x, y, one float64 }{1 + 0x1p-30, 1 - 0x1p-30, 1}

// TestNoFusion checks that AddFloat and SubFloat round a product that their
// caller passes them unrounded before they add it or subtract it (§5.8.9),
// which a fused multiply-add, which the Go compiler emits on arm64, does
// not, so that on arm64 it fails when an operation fuses with a product.
func TestNoFusion(t *testing.T) {
	t.Parallel()
	x, y, one := fusing.x, fusing.y, fusing.one
	for name, got := range map[string]func() (float64, RuntimeError){
		"AddFloat(x*y, -1)": func() (float64, RuntimeError) { return AddFloat(x*y, -one) },
		"SubFloat(x*y, 1)":  func() (float64, RuntimeError) { return SubFloat(x*y, one) },
		"SubFloat(1, x*y)":  func() (float64, RuntimeError) { return SubFloat(one, x*y) },
	} {
		if f, problem := got(); f != 0 || problem != "" {
			t.Errorf("%s = %v, %q; want 0", name, f, problem)
		}
	}
}

// The grammars of to_int and to_float (§5.8.9), as regular expressions, to
// check ParseInt's and ParseFloat's own reading of them against.
var (
	intGrammar   = regexp.MustCompile(`^[+-]?[0-9]+$`)
	floatGrammar = regexp.MustCompile(`^[+-]?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
)

// TestParseGrammars checks that ParseInt and ParseFloat refuse with
// ConversionFailed exactly the strings outside their grammars, of random
// strings of the characters that the grammars hold and of others that
// platform parsers read, as white space, _, x, and the letters of inf and nan.
func TestParseGrammars(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(38, 4))
	const alphabet = "0123456789+-.eE _xpinfaINFA٣"
	runes := []rune(alphabet)
	for range 200_000 {
		var b strings.Builder
		for range rng.IntN(8) {
			b.WriteRune(runes[rng.IntN(len(runes))])
		}
		s := b.String()
		if _, problem := ParseInt(s); (problem == ConversionFailed) == intGrammar.MatchString(s) {
			t.Fatalf("ParseInt(%q) reports %q, and the grammar takes it: %t", s, problem,
				intGrammar.MatchString(s))
		}
		if _, problem := ParseFloat(s); (problem == ConversionFailed) == floatGrammar.MatchString(s) {
			t.Fatalf("ParseFloat(%q) reports %q, and the grammar takes it: %t", s, problem,
				floatGrammar.MatchString(s))
		}
	}
}

// TestParseFloat checks that ParseFloat reads a string of its grammar as
// the double nearest it, against an exact reading of it as a fraction
// (big.Rat), for strings of many digits, on either side of the point, and
// with exponents far beyond a double's range, and that ParseInt reads a
// string of its grammar as its exact value.
func TestParseFloat(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(38, 5))
	digits := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('0' + rng.IntN(10))
		}
		return string(b)
	}
	for range 5000 {
		s := digits(1 + rng.IntN(400))
		if rng.IntN(2) == 0 {
			s += "." + digits(1+rng.IntN(400))
		}
		if rng.IntN(2) == 0 {
			s += []string{"e", "E", "e+", "e-"}[rng.IntN(4)] + strconv.Itoa(rng.IntN(800))
		}
		if rng.IntN(2) == 0 {
			s = []string{"-", "+"}[rng.IntN(2)] + s
		}
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			t.Fatalf("big.Rat does not read %s", s)
		}
		want, _ := r.Float64()
		got, problem := ParseFloat(s)
		switch {
		case math.IsInf(want, 0):
			if problem != FloatNotFinite || got != 0 {
				t.Fatalf("ParseFloat(%s) = %v, %q; want 0, float_not_finite", s, got, problem)
			}
		case problem != "" || math.Float64bits(got) != math.Float64bits(want+0):
			// want+0 is 0 for a -0 that rounds from a negative too small for
			// a double.
			t.Fatalf("ParseFloat(%s) = %v, %q; want %v", s, got, problem, want+0)
		}
		if !strings.ContainsAny(s, ".eE") {
			n, problem := ParseInt(s)
			if exact := r.Num(); new(big.Int).Abs(exact).Cmp(big.NewInt(MaxInt)) > 0 {
				if problem != IntegerOverflow || n != 0 {
					t.Fatalf("ParseInt(%s) = %d, %q; want 0, integer_overflow", s, n, problem)
				}
			} else if problem != "" || n != exact.Int64() {
				t.Fatalf("ParseInt(%s) = %d, %q; want %s", s, n, problem, exact)
			}
		}
	}
}

// TestParseFloatDigits checks that ParseFloat reads a string of more
// significant digits than maxDigits, the most that it writes for strconv, as
// the double nearest it, against an exact reading of it as a fraction
// (big.Rat), where only digits far past maxDigits decide that double: the
// point halfway between two doubles written in full, which rounds to the even
// one, the same with a thousand zeros and a 1 after it, which rounds up, and
// the point a little below it, of thousands of nines, which rounds down; and
// strings of thousands of random digits, on either side of the point.
func TestParseFloatDigits(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(38, 7))
	check := func(s string) {
		t.Helper()
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			t.Fatalf("big.Rat does not read %.40s…", s)
		}
		want, _ := r.Float64()
		got, problem := ParseFloat(s)
		switch {
		case math.IsInf(want, 0):
			if problem != FloatNotFinite || got != 0 {
				t.Fatalf("ParseFloat(%.40s…) = %v, %q; want 0, float_not_finite", s, got, problem)
			}
		case problem != "" || math.Float64bits(got) != math.Float64bits(want+0):
			t.Fatalf("ParseFloat(%.40s…, of %d characters) = %v, %q; want %v", s, len(s), got, problem, want+0)
		}
	}
	two := big.NewRat(2, 1)
	tiny := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(2100), nil))
	for range 300 {
		f := math.Abs(randomFloat(rng))
		if f == math.MaxFloat64 {
			continue
		}
		half := new(big.Rat).Add(new(big.Rat).SetFloat64(f), new(big.Rat).SetFloat64(math.Nextafter(f, math.Inf(1))))
		half.Quo(half, two)
		// A double's denominator is at most 2^1075, so that 1,100 digits
		// after the point write the halfway point exactly, and one at least
		// of them is left of an integral one.
		exact := strings.TrimRight(half.FloatString(1100), "0")
		if strings.HasSuffix(exact, ".") {
			exact += "0"
		}
		check(exact)
		check(exact + strings.Repeat("0", 1000) + "1")
		check(new(big.Rat).Sub(half, tiny).FloatString(2100))
	}
	digits := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('0' + rng.IntN(10))
		}
		return string(b)
	}
	for range 300 {
		s := digits(1+rng.IntN(2000)) + "." + digits(1+rng.IntN(2000))
		if rng.IntN(2) == 0 {
			s += "e-" + strconv.Itoa(rng.IntN(2400))
		}
		check(s)
	}
}

// TestParseFloatCost checks that ParseFloat allocates no more for a string of
// a million digits than for one of a thousand, whichever side of the point
// they are on and whatever spells its exponent, so that a float literal of a
// source of any length costs no more than its value. It does not run in
// parallel, so that what other tests allocate is not counted.
func TestParseFloatCost(t *testing.T) {
	million := strings.Repeat("1", 1<<20)
	for _, s := range []string{"1." + million, million + ".5e-1048570", "0." + strings.Repeat("0", 1<<20) + "1e1048577",
		"1." + million + "E5", "1." + strings.Repeat("0", 1<<20)} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, problem := ParseFloat(s)
		runtime.ReadMemStats(&after)
		if problem != "" {
			t.Errorf("ParseFloat(%.20s…) reports %q", s, problem)
		}
		if n := after.TotalAlloc - before.TotalAlloc; n > 16<<10 {
			t.Errorf("ParseFloat(%.20s…) allocated %d bytes, of a string of %d; want at most 16 KiB", s, n, len(s))
		}
	}
}

// The forms of FormatFloat: positional, with one digit at least after the
// point, and with an exponent, whose mantissa has a point only before more
// digits, and whose exponent has a sign and no leading zeros.
var (
	positional  = regexp.MustCompile(`^-?(0|[1-9][0-9]*)\.[0-9]+$`)
	exponential = regexp.MustCompile(`^-?[1-9](\.[0-9]*[1-9])?e[+-][1-9][0-9]*$`)
)

// TestFormatFloat checks that FormatFloat writes every double as a decimal
// that reads as it again, of the form for its magnitude, with the shortest
// digits, as strconv writes them, and the cases of §5.8.9.
func TestFormatFloat(t *testing.T) {
	t.Parallel()
	for f, want := range map[float64]string{0: "0.0", math.Copysign(0, -1): "0.0", 2: "2.0", -2.5: "-2.5",
		0.1: "0.1", 1e20: "100000000000000000000.0", 1e21: "1e+21", 1e-6: "0.000001", 1e-7: "1e-7",
		123456789.125: "123456789.125", math.MaxFloat64: "1.7976931348623157e+308", 5e-324: "5e-324"} {
		if got := FormatFloat(f); got != want {
			t.Errorf("FormatFloat(%v) = %s, want %s", f, got, want)
		}
	}
	rng := rand.New(rand.NewPCG(38, 6))
	for range 200_000 {
		f := randomFloat(rng)
		s := FormatFloat(f)
		back, err := strconv.ParseFloat(s, 64)
		if err != nil || back != f {
			t.Fatalf("FormatFloat(%v) = %s, which reads as %v, %v", f, s, back, err)
		}
		shortest := strings.TrimLeft(strings.Replace(strings.Split(strconv.FormatFloat(math.Abs(f), 'e', -1, 64),
			"e")[0], ".", "", 1), "0")
		digits := strings.Trim(strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, strings.Split(s, "e")[0]), "0")
		if a := math.Abs(f); a >= 1e-6 && a < 1e21 {
			if !positional.MatchString(s) {
				t.Fatalf("FormatFloat(%v) = %s, which is not positional", f, s)
			}
		} else if !exponential.MatchString(s) {
			t.Fatalf("FormatFloat(%v) = %s, which is not exponential", f, s)
		}
		if digits != shortest {
			t.Fatalf("FormatFloat(%v) = %s, whose digits are not the shortest, %s", f, s, shortest)
		}
	}
}

// TestFormatFixed checks FormatFixed against an exact rounding (big.Int) of
// the decimal that FormatFloat writes, half away from zero, for doubles of
// every magnitude and every number of digits that a format specifier has.
func TestFormatFixed(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(38, 7))
	for i := range 100_000 {
		f := randomFloat(rng)
		switch i % 3 {
		case 0:
			// A value whose shortest digits end at a tie, as 2.675.
			f, _ = strconv.ParseFloat(strconv.FormatFloat(rng.Float64()*1000, 'f', rng.IntN(6), 64)+"5", 64)
		case 1:
			f = float64(randomInt(rng)) / 1000
		}
		if rng.IntN(2) == 0 {
			f = -f
		}
		decimals := rng.IntN(7)
		if got, want := FormatFixed(f, decimals), fixed(f, decimals); got != want {
			t.Fatalf("FormatFixed(%v, %d) = %s, want %s", f, decimals, got, want)
		}
	}
}

// fixed returns f with decimals digits as FormatFixed writes it, by exact
// arithmetic on the shortest decimal of f: that decimal times 10^decimals,
// rounded half away from zero, as an integer, then written with the point.
func fixed(f float64, decimals int) string {
	r, _ := new(big.Rat).SetString(strconv.FormatFloat(math.Abs(f), 'e', -1, 64))
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)))
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Quo(r.Num(), r.Denom())
	s := n.String()
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals+1-len(s)) + s
	}
	if f < 0 && n.Sign() != 0 {
		s = "-" + s
	}
	if decimals == 0 {
		return s
	}
	return s[:len(s)-decimals] + "." + s[len(s)-decimals:]
}
