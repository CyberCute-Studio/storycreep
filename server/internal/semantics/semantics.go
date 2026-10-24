// Package semantics holds the numeric and string semantics of the
// expression language (PRD §5.8.9), which every evaluator of it shares, so
// that Go, TypeScript, and GDScript give the same result for the same
// expression where each language's own operations differ: an int is an
// integer within ±MaxInt, which nothing wraps; a float is a finite double,
// never -0; a string converts to a number by an exact grammar, and to the
// nearest double; a number is written the same way by every runtime; and a
// string is a sequence of code points, cased and trimmed by the tables of
// one version of Unicode, UnicodeVersion, which are generated from the
// Unicode Character Database's files in schema/unicode/ucd (TestTables).
//
// An operation that fails reports a runtime error (§5.8.7) and yields the
// zero value of its result's type, 0, 0.0, or "", which evaluation goes on
// with. The expression corpus's cases of each rule are in
// schema/expr-corpus, which TestCorpus runs, and which the TypeScript and
// GDScript evaluators run too.
//
// The operations take values of the language: an int within ±MaxInt, a
// finite float other than -0, and a string of UTF-8, as every value that a
// runtime holds is. An int operand beyond ±MaxInt overflows, so that nothing
// wraps whatever an operation is given.
package semantics

// MaxInt is the greatest int, and -MaxInt the least: 2^53 − 1, which a
// double holds exactly, so that the TypeScript evaluator's numbers hold every
// int, and every int widens to a float exactly (PRD §5.8.1, §5.8.9).
const MaxInt = 1<<53 - 1

// MaxDecimals is the most digits after the point that an interpolation
// format specifier shows a number with (FR-TEXT-02), as a text value
// reference's decimals, from 0 to MaxDecimals, does (FormatFixed).
const MaxDecimals = 6

// A RuntimeError is the code of the runtime error that an operation reports
// (PRD §5.8.3, §5.8.7), or "" for none.
type RuntimeError string

// The runtime errors that the operations of this package report.
const (
	// IntegerOverflow is an int result beyond ±MaxInt.
	IntegerOverflow RuntimeError = "integer_overflow"
	// FloatNotFinite is a float result that is NaN or an infinity, which a
	// result of finite operands is only when it overflows a double.
	FloatNotFinite RuntimeError = "float_not_finite"
	// DivisionByZero is a division by zero, of an int or a float, or an
	// int's modulo by zero.
	DivisionByZero RuntimeError = "division_by_zero"
	// ConversionFailed is a string that to_int or to_float does not read,
	// as one outside its grammar.
	ConversionFailed RuntimeError = "conversion_failed"
)
