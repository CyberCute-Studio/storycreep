package semantics

import (
	"math"
	"strconv"
	"strings"
)

// CheckInt returns n as an int, or IntegerOverflow and 0 when it is beyond
// ±MaxInt.
func CheckInt(n int64) (int64, RuntimeError) {
	if !isInt(n) {
		return 0, IntegerOverflow
	}
	return n, ""
}

// isInt reports whether n is within ±MaxInt, as every int is.
func isInt(n int64) bool {
	return -MaxInt <= n && n <= MaxInt
}

// AddInt returns a + b, SubInt a - b, and NegInt -a. The exact result of
// operands within ±MaxInt fits in an int64, so the bound is checked on it
// (§5.8.9).
func AddInt(a, b int64) (int64, RuntimeError) {
	if !isInt(a) || !isInt(b) {
		return 0, IntegerOverflow
	}
	return CheckInt(a + b)
}

// SubInt returns a - b, as AddInt says.
func SubInt(a, b int64) (int64, RuntimeError) {
	if !isInt(a) || !isInt(b) {
		return 0, IntegerOverflow
	}
	return CheckInt(a - b)
}

// NegInt returns -a, as AddInt says, which is an int whenever a is.
func NegInt(a int64) (int64, RuntimeError) {
	return CheckInt(-a)
}

// MulInt returns a * b, checked before it multiplies (§5.8.9): the product
// overflows exactly when a ≠ 0 and |b| > floor(MaxInt / |a|), so that no
// product is computed that an int64 could wrap back into range, as it wraps
// 4294967297 * 4294967296, which is 2^64 + 2^32, to 4294967296, or that a
// double would round, as the TypeScript evaluator's would.
func MulInt(a, b int64) (int64, RuntimeError) {
	if !isInt(a) || !isInt(b) || a != 0 && abs(b) > MaxInt/abs(a) {
		return 0, IntegerOverflow
	}
	return a * b, ""
}

// abs returns |n| of an n within ±MaxInt.
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// DivInt returns a / b truncated toward zero, and ModInt the remainder, which
// takes the sign of the dividend, as Go's / and % do (§5.8.3, §5.8.9), or
// DivisionByZero and 0 when b is 0. Neither overflows for ints, -MaxInt / -1
// being MaxInt.
func DivInt(a, b int64) (int64, RuntimeError) {
	switch {
	case !isInt(a) || !isInt(b):
		return 0, IntegerOverflow
	case b == 0:
		return 0, DivisionByZero
	}
	return a / b, ""
}

// ModInt returns a % b, as DivInt says.
func ModInt(a, b int64) (int64, RuntimeError) {
	switch {
	case !isInt(a) || !isInt(b):
		return 0, IntegerOverflow
	case b == 0:
		return 0, DivisionByZero
	}
	return a % b, ""
}

// CheckFloat returns f as a float (§5.8.9): -0 as 0, and NaN or an infinity
// as FloatNotFinite and 0. A result that underflows, to a subnormal or to
// zero, is a float, without an error.
func CheckFloat(f float64) (float64, RuntimeError) {
	switch {
	case math.IsNaN(f) || math.IsInf(f, 0):
		return 0, FloatNotFinite
	case f == 0:
		// -0 as well as 0.
		return 0, ""
	}
	return f, ""
}

// AddFloat returns a + b, SubFloat a - b, MulFloat a * b, and NegFloat -a,
// each rounded to the nearest double and checked as CheckFloat checks it.
// Each converts its result to float64 explicitly, which the Go specification
// says rounds it, since a compiler may otherwise fuse an operation with the
// next, as a multiplication with an addition into one fused multiply-add on
// arm64, which rounds once where the TypeScript evaluator rounds twice.
func AddFloat(a, b float64) (float64, RuntimeError) {
	return CheckFloat(float64(a + b))
}

// SubFloat returns a - b, as AddFloat says.
func SubFloat(a, b float64) (float64, RuntimeError) {
	return CheckFloat(float64(a - b))
}

// MulFloat returns a * b, as AddFloat says.
func MulFloat(a, b float64) (float64, RuntimeError) {
	return CheckFloat(float64(a * b))
}

// NegFloat returns -a, as AddFloat says, so -0.0 is 0.0.
func NegFloat(a float64) (float64, RuntimeError) {
	return CheckFloat(-a)
}

// DivFloat returns a / b, as AddFloat says, or DivisionByZero and 0 when b
// is 0, as an int's division by zero is (§5.8.3), rather than the infinity
// or the NaN that IEEE 754 gives.
func DivFloat(a, b float64) (float64, RuntimeError) {
	if b == 0 {
		return 0, DivisionByZero
	}
	return CheckFloat(float64(a / b))
}

// Trunc returns f truncated toward zero, as to_int converts a float, and
// Floor, Ceil, and Round return it rounded down, up, and half away from zero,
// as floor, ceil, and round do (§5.8.4), each as an int. Each compares the
// rounded value with ±MaxInt as a double before it converts it to an int64
// (§5.8.9), and reports IntegerOverflow and 0 beyond it, since Go's
// conversion of a float beyond int64's range gives what the platform gives.
func Trunc(f float64) (int64, RuntimeError) {
	return toInt(math.Trunc(f))
}

// Floor returns f rounded down, as Trunc says.
func Floor(f float64) (int64, RuntimeError) {
	return toInt(math.Floor(f))
}

// Ceil returns f rounded up, as Trunc says.
func Ceil(f float64) (int64, RuntimeError) {
	return toInt(math.Ceil(f))
}

// Round returns f rounded half away from zero, as Trunc says: round(-2.5) is
// -3, and round(0.49999999999999994) is 0, which adding 0.5 and rounding
// down would make 1. The TypeScript evaluator does not use Math.round, which
// rounds half toward +∞.
func Round(f float64) (int64, RuntimeError) {
	return toInt(math.Round(f))
}

// toInt returns t, an integral double, as an int, or IntegerOverflow and 0
// when it is beyond ±MaxInt, or is not finite, which no float is.
func toInt(t float64) (int64, RuntimeError) {
	if !(math.Abs(t) <= MaxInt) {
		return 0, IntegerOverflow
	}
	return int64(t), ""
}

// ParseInt returns the int that s writes, as to_int reads a string (§5.8.4,
// §5.8.9): s is exactly [+-]?[0-9]+, with ASCII digits and nothing else, no
// white space among it, or ParseInt reports ConversionFailed and 0, and its
// value is within ±MaxInt, however many leading zeros it has, or ParseInt
// reports IntegerOverflow and 0. "-0" is 0.
func ParseInt(s string) (int64, RuntimeError) {
	body, negative := cutSign(s)
	if !isDigits(body) {
		return 0, ConversionFailed
	}
	digits := strings.TrimLeft(body, "0")
	if len(digits) > len(strconv.Itoa(MaxInt)) {
		return 0, IntegerOverflow
	}
	// At most 16 digits, which an int64 holds; none is 0.
	n, _ := strconv.ParseInt("0"+digits, 10, 64)
	if n > MaxInt {
		return 0, IntegerOverflow
	}
	if negative {
		n = -n
	}
	return n, ""
}

// cutSign returns s without the sign that it starts with, if any, and
// whether that sign is -.
func cutSign(s string) (string, bool) {
	if s != "" && (s[0] == '+' || s[0] == '-') {
		return s[1:], s[0] == '-'
	}
	return s, false
}

// isDigits reports whether s is one or more ASCII digits.
func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// isFloatForm reports whether s is of the grammar of a string that to_float
// reads (§5.8.9), [+-]?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?, over the whole of
// it: no white space, no inf or nan, no hexadecimal, no _ between digits, no
// point without digits on both sides of it, and nothing after it.
func isFloatForm(s string) bool {
	body, _ := cutSign(s)
	mantissa, exponent := body, ""
	if i := strings.IndexAny(body, "eE"); i >= 0 {
		mantissa, exponent = body[:i], body[i+1:]
		if exponent, _ = cutSign(exponent); !isDigits(exponent) {
			return false
		}
	}
	whole, fraction, point := strings.Cut(mantissa, ".")
	return isDigits(whole) && (!point || isDigits(fraction))
}

// ParseFloat returns the float that s writes, as to_float reads a string
// (§5.8.4, §5.8.9): s is of the grammar that isFloatForm checks, or
// ParseFloat reports ConversionFailed and 0, and its value is the double
// nearest it, correctly rounded, half to even, however many digits it has
// and however large its exponent, or ParseFloat reports FloatNotFinite and 0
// when it is beyond a double's range. A value too small for a double is 0, or
// the subnormal nearest it, and "-0" is 0. The grammar is checked before
// anything converts, so that no platform parser's leniency is reached, as
// strconv.ParseFloat's, which reads "inf" and hexadecimal floats.
func ParseFloat(s string) (float64, RuntimeError) {
	if !isFloatForm(s) {
		return 0, ConversionFailed
	}
	f, ok := nearest(s)
	if !ok {
		return 0, FloatNotFinite
	}
	return CheckFloat(f)
}

// hugeExponent stands for an exponent of more than 9 digits, either way,
// which no digits that a string holds bring back within a double's range.
const hugeExponent = 1_000_000_000

// nearest returns s, a string of the grammar that isFloatForm checks, as the
// double nearest it, 0 for one too small for a double, and whether it is
// within a double's range, however many digits s has, and however large its
// exponent, which strconv.ParseFloat misreads past 800 digits before a point
// or 5 digits of exponent. It writes s's digits without the zeros around them
// as 0.ddd, with the exponent that gives s's value, which ParseFloat reads
// exactly however many digits follow the point, once it has told that
// exponent from one beyond a double's range, either way.
func nearest(s string) (float64, bool) {
	body, negative := cutSign(s)
	mantissa, exponent, _ := strings.Cut(strings.ToLower(body), "e")
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	significant := strings.TrimRight(digits, "0")
	if significant == "" {
		return 0, true
	}
	e := int64(0)
	if exponent != "" {
		magnitude, negativeExponent := cutSign(exponent)
		magnitude = strings.TrimLeft(magnitude, "0")
		if len(magnitude) > 9 {
			e = hugeExponent
		} else if magnitude != "" {
			n, _ := strconv.ParseInt(magnitude, 10, 64)
			e = n
		}
		if negativeExponent {
			e = -e
		}
	}
	// The value is 0.significant times ten to the power of point: at least
	// 1e310, past a double's range, if point is more than 310, and less than
	// 1e-400, which rounds to 0, if it is less than -400.
	point := int64(len(digits)) - int64(len(fraction)) + e
	switch {
	case point > 310:
		return 0, false
	case point < -400:
		return 0, true
	}
	f, err := strconv.ParseFloat("0."+significant+"e"+strconv.FormatInt(point, 10), 64)
	if err != nil || math.IsInf(f, 0) {
		return 0, false
	}
	if negative {
		f = -f
	}
	return f, true
}

// FormatInt returns n as to_string writes an int (§5.8.9): a plain decimal,
// with a leading - when it is negative.
func FormatInt(n int64) string {
	return strconv.FormatInt(n, 10)
}

// FormatFloat returns f, a finite double, as to_string writes a float
// (§5.8.9), as interpolation shows one without a format specifier, and as a
// float's value is written as a string: the shortest decimal that reads as f
// again, with . as its point, whatever the locale, in positional notation for
// a magnitude from 1e-6 to less than 1e21, with one digit after the point for
// an integral value, as 2.0, and otherwise as the shortest digits, with a
// point after the first only when there are more, e, the exponent's sign, and
// the exponent without leading zeros, as 1e+21 or 1.5e-7, as JavaScript's
// String writes them. 0 and -0 are 0.0.
func FormatFloat(f float64) string {
	if a := math.Abs(f); a == 0 || a >= 1e-6 && a < 1e21 {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if f == 0 {
			s = "0"
		}
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	return mantissa + "e" + exponent[:1] + strings.TrimLeft(exponent[1:], "0")
}

// FormatFixed returns f, a finite double, as a text value reference whose
// format specifier is decimals digits shows a number (FR-TEXT-02, §5.8.9):
// the decimal that FormatFloat writes, rounded half away from zero to
// decimals digits after the point, written in positional notation however
// large or small it is, with exactly decimals digits after the point, and
// without a point when decimals is 0, and without a sign when it rounds to
// zero. It rounds that decimal rather than the double's exact value, so that
// 1.005, whose double is a little less than 1.005, shows as 1.01 with 2
// digits, as it was written, and as to_string writes it, rounded. An int is
// shown as the float that it widens to, exactly: 5 with 2 digits is 5.00. A
// negative decimals is 0.
func FormatFixed(f float64, decimals int) string {
	decimals = max(decimals, 0)
	// The shortest digits of |f|, the first of which stands for 10^e.
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(math.Abs(f), 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	e, _ := strconv.Atoi(exponent)
	// The digits down to 10^-decimals, which are |f| times 10^decimals as an
	// integer, rounded up when the first digit after them is 5 or more: a
	// tie is rounded away from zero, which for |f| is up.
	keep := e + 1 + decimals
	var kept string
	switch {
	case keep < 0:
		// |f| is less than half of 10^-decimals.
	case keep >= len(digits):
		kept = digits + strings.Repeat("0", keep-len(digits))
	case digits[keep] >= '5':
		kept = increment(digits[:keep])
	default:
		kept = digits[:keep]
	}
	kept = strings.TrimLeft(kept, "0")
	sign := ""
	if f < 0 && kept != "" {
		sign = "-"
	}
	if len(kept) <= decimals {
		kept = strings.Repeat("0", decimals+1-len(kept)) + kept
	}
	whole, fraction := kept[:len(kept)-decimals], kept[len(kept)-decimals:]
	if decimals == 0 {
		return sign + whole
	}
	return sign + whole + "." + fraction
}

// increment returns the decimal digits n, which may be none, plus one.
func increment(n string) string {
	b := []byte(n)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < '9' {
			b[i]++
			return string(b)
		}
		b[i] = '0'
	}
	return "1" + string(b)
}
