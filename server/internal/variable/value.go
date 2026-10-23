package variable

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/input"
)

// The types of a variable (PRD FR-VAR-01, §5.8.3).
const (
	TypeBool   = "bool"
	TypeInt    = "int"
	TypeFloat  = "float"
	TypeString = "string"
)

// types are the types of a variable.
var types = []string{TypeBool, TypeInt, TypeFloat, TypeString}

// Types returns the types of a variable, in the order of the database's
// check and of the schemas' enums, as a copy that the caller may change.
func Types() []string {
	return slices.Clone(types)
}

// Limits of a variable's default (§5.8.1, §5.8.9).
const (
	// MaxInt is the greatest int, and -MaxInt the least: 2^53 − 1, which
	// every evaluator, the TypeScript one's doubles among them, represents
	// exactly.
	MaxInt = 1<<53 - 1
	// MaxStringLength is the most characters that a string may have, as a
	// string literal of the expression language may.
	MaxStringLength = 4096
)

// Bounds of how a default's number is written, whatever its value
// (formProblem). No int or float needs more: the shortest form of each, as
// JSON.stringify writes it, has 24 characters at most, and an exponent of 3
// digits at most. Within them, every way of writing a number is read
// exactly, by strconv.ParseFloat, which misreads an exponent of more than 5
// digits or more than 800 digits before a point, and is held by the operation
// log, which keeps a payload's default as the client wrote it, in jsonb,
// whose numbers PostgreSQL's numeric holds only within an exponent of
// ±(2^30 − 1) and 16,383 digits after the point, its exponent counted, so
// that no spelling of a number reaches either limit.
const (
	// maxNumberLength is the most characters that a number may be written
	// with, its sign, its point, and its exponent included.
	maxNumberLength = 64
	// maxExponent is the greatest exponent, either way, that a number may be
	// written with, as 1e-999 is, past a double's range in both directions.
	maxExponent = 999
)

// Value is a variable's default, as JSON: a boolean, a number, or a string,
// which the variable's type says how to read (Of). The zero Value is none, as
// a payload that leaves its default out has, which stands for the type's
// zero value.
type Value struct {
	raw json.RawMessage
}

// valueSchema is the schema of a Value: a boolean, a number, or a string, of
// which the variable's type takes one kind.
func valueSchema() *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{{Type: huma.TypeBoolean}, {Type: huma.TypeNumber},
		{Type: huma.TypeString}}}
}

// Schema gives huma the schema of a Value (huma.SchemaProvider), which
// validates a request's body and a command's payload, and describes the
// field in the OpenAPI document. It is not null, which huma passes over in a
// field that a schema does not require: UnmarshalJSON keeps it, which the
// payload's Check then refuses at the field.
func (Value) Schema(huma.Registry) *huma.Schema {
	return valueSchema()
}

// UnmarshalJSON keeps the JSON of the value as it is, which Of reads once the
// variable's type is known.
func (v *Value) UnmarshalJSON(data []byte) error {
	v.raw = slices.Clone(data)
	return nil
}

// MarshalJSON writes the value as it was read, a payload's as the client wrote
// it, which the operation log keeps, or as Of returned it, or null for none.
func (v Value) MarshalJSON() ([]byte, error) {
	if v.raw == nil {
		return []byte("null"), nil
	}
	return v.raw, nil
}

// IsZero reports whether v is none, so that a payload's field that the
// client left out, tagged omitzero, stays out when the pipeline encodes the
// payload again for the operation log, rather than being written as null,
// which no default is.
func (v Value) IsZero() bool {
	return v.raw == nil
}

// Zero returns the zero value of the type typ, a variable's type: false, 0,
// 0.0, which is written as 0, or the empty string.
func Zero(typ string) Value {
	switch typ {
	case TypeBool:
		return Value{raw: json.RawMessage("false")}
	case TypeString:
		return Value{raw: json.RawMessage(`""`)}
	}
	return Value{raw: json.RawMessage("0")}
}

// Of returns v as a value of the type typ, a variable's type, in its
// canonical form, which is what is stored and shown, or says what is wrong
// with it as a value of the type, in English, for programs and logs, as the
// message of a field's problem. A value of a type is: true or false for
// bool; for int, a number that is an integer from -MaxInt to MaxInt, however
// it is written, as 3, 3.0, or 3e0, which is written as 3; for float, a number
// that a double holds without overflowing to an infinity, read as the double
// nearest it, as JSON.parse reads it, and written as the shortest number that
// reads as that double again, -0 as 0, each written with at most
// maxNumberLength characters and an exponent within ±maxExponent
// (formProblem), so that 0e1000 is neither, though it is 0; and for string, a
// string of at most MaxStringLength characters, without
// control characters but line feeds and tabs (input.CheckText), and without
// an escape of a lone surrogate, which no UTF-8 holds, and which
// encoding/json would read as U+FFFD where JSON.parse keeps it, so that the
// server would store other text than the client holds; a string is written
// without escaping <, >, and & (PRD §6.2). None, the zero Value, is the
// type's zero value (Zero).
func (v Value) Of(typ string) (Value, string) {
	return v.of(typ, true)
}

// of returns v as a value of the type typ as Of does, but checks a string's
// characters and its length (input.CheckText) only if checked is set. A
// default that the database holds is read without that check (stored): the
// database's own check holds it to the length, but not to the characters, so
// that a change of what input.CheckText takes, or a row that the pipeline did
// not write, leaves the project's variables readable. A command that sets a
// default, the undo of a change of one among them, checks it whole as it
// applies (Of); variable.retype's payload check, which the step of an undo
// passes too, checks it without its characters.
func (v Value) of(typ string, checked bool) (Value, string) {
	if v.raw == nil {
		return Zero(typ), ""
	}
	raw := string(v.raw)
	kind := kindOf(raw)
	switch {
	case raw == "null":
		return Value{}, "is null, which no variable's default is"
	case kind == "":
		return Value{}, "is neither a boolean, a number, nor a string"
	case (typ == TypeInt || typ == TypeFloat) && kind == "number":
		return number(typ, raw)
	case typ == TypeBool && kind == "boolean":
		return Value{raw: json.RawMessage(raw)}, ""
	case typ == TypeString && kind == "string":
		return text(v.raw, checked)
	case !slices.Contains(types, typ):
		return Value{}, fmt.Sprintf("is a value of the type %q, which is no variable's type", typ)
	}
	return Value{}, fmt.Sprintf("is a %s, not a value of the variable's type, %s", kind, typ)
}

// kindOf returns the kind of the JSON value raw: boolean, number, or string,
// or "" for any other, as null, an object, or an array.
func kindOf(raw string) string {
	switch {
	case raw == "true" || raw == "false":
		return "boolean"
	case strings.HasPrefix(raw, `"`):
		return "string"
	case raw != "" && (raw[0] == '-' || '0' <= raw[0] && raw[0] <= '9'):
		return "number"
	}
	return ""
}

// number returns raw, a JSON number, as a value of the type typ, int or
// float, as Of does.
func number(typ, raw string) (Value, string) {
	if problem := formProblem(raw); problem != "" {
		return Value{}, problem
	}
	if typ == TypeInt {
		n, problem := integer(raw)
		if problem != "" {
			return Value{}, problem
		}
		return Value{raw: json.RawMessage(strconv.FormatInt(n, 10))}, ""
	}
	// Within formProblem's bounds, ParseFloat reads the number exactly, and
	// returns an error only for one beyond a double's range, which it reads
	// as an infinity; one too small for a double it reads as 0.
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return Value{}, "is beyond the range of a float, a double, which holds no number greater than about 1.8e308"
	}
	if f == 0 {
		// -0 is written as 0, as the evaluators normalise it (§5.8.9).
		f = 0
	}
	out, err := json.Marshal(f)
	if err != nil {
		return Value{}, "is not a number that JSON writes"
	}
	return Value{raw: out}, ""
}

// formProblem says why raw, a JSON number, is not written within the bounds
// of a default's number, more than maxNumberLength characters or an exponent
// beyond ±maxExponent, whatever its value, or returns "" if it is. The
// exponent is read without its leading zeros, which the length bounds.
func formProblem(raw string) string {
	exponent := ""
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		exponent = strings.TrimLeft(strings.TrimLeft(raw[i+1:], "+-"), "0")
	}
	if n, err := strconv.Atoi(exponent); len(raw) > maxNumberLength || exponent != "" &&
		(err != nil || n > maxExponent) {
		return fmt.Sprintf("is written with more than %d characters, or with an exponent beyond ±%d, which no int "+
			"or float needs to be, whose shortest form has 24 characters at most", maxNumberLength, maxExponent)
	}
	return ""
}

// integer returns the value of raw, a JSON number that formProblem took, if
// it is an integer from -MaxInt to MaxInt, or says why it is not one. It
// reads raw as a decimal, exactly, so that a number that rounds to an integer
// as a double, as 4503599627370495.5, is not one.
func integer(raw string) (int64, string) {
	const notInteger = "is not an integer, which an int's default is"
	// MaxInt is converted to int64, the type that holds it on every
	// platform, as int does not on a 32-bit one.
	tooLarge := fmt.Sprintf("is not an integer from -%d to %d, which every runtime holds exactly", int64(MaxInt),
		int64(MaxInt))
	s, negative := strings.CutPrefix(raw, "-")
	mantissa, exponent := s, ""
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, exponent = s[:i], s[i+1:]
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return 0, ""
	}
	// The value is digits times ten to the power of shift. formProblem took
	// the exponent, which is within ±maxExponent, and the number, of at most
	// maxNumberLength characters; Atoi reads the exponent's sign and its
	// leading zeros, and none as 0.
	e, _ := strconv.Atoi(exponent)
	shift := e - len(fraction)
	for strings.HasSuffix(digits, "0") {
		digits = digits[:len(digits)-1]
		shift++
	}
	switch {
	case shift < 0:
		return 0, notInteger
	case len(digits)+shift > len(strconv.FormatInt(MaxInt, 10)):
		return 0, tooLarge
	}
	n, err := strconv.ParseInt(digits+strings.Repeat("0", shift), 10, 64)
	if err != nil || n > MaxInt {
		return 0, tooLarge
	}
	if negative {
		n = -n
	}
	return n, ""
}

// text returns raw, a JSON string, as a value of the type string, as Of
// does, or as of does without input.CheckText unless checked is set.
func text(raw json.RawMessage, checked bool) (Value, string) {
	var s string
	switch {
	case !utf8.Valid(raw):
		return Value{}, "is not UTF-8"
	case loneSurrogate(raw):
		return Value{}, "escapes a lone surrogate, which is not a character"
	case json.Unmarshal(raw, &s) != nil:
		return Value{}, "is not a JSON string"
	}
	if checked {
		if problem := input.CheckText(s, MaxStringLength); problem != "" {
			return Value{}, problem
		}
	}
	// The string is written without escaping <, >, and &, as the API writes
	// its other texts (PRD §6.2), which json.Marshal would escape.
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return Value{}, "is not a string that JSON writes"
	}
	return Value{raw: bytes.TrimSuffix(out.Bytes(), []byte("\n"))}, ""
}

// loneSurrogate reports whether raw, a JSON string, escapes a UTF-16
// surrogate that is not one of a pair, as "\ud800".
func loneSurrogate(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if i+5 >= len(raw) || raw[i+1] != 'u' {
			// An escape of one character, as \" or \n, which the skip below
			// steps over, so that its second character starts no escape.
			i++
			continue
		}
		r, ok := hex4(raw[i+2 : i+6])
		i += 5
		switch {
		case !ok || r < 0xD800 || r >= 0xE000:
		case r >= 0xDC00:
			return true
		case i+6 < len(raw) && raw[i+1] == '\\' && raw[i+2] == 'u':
			if low, ok := hex4(raw[i+3 : i+7]); ok && low >= 0xDC00 && low < 0xE000 {
				i += 6
				continue
			}
			return true
		default:
			return true
		}
	}
	return false
}

// hex4 returns the value of four hexadecimal digits, in either case, and
// whether they are four such digits.
func hex4(digits []byte) (rune, bool) {
	n, err := strconv.ParseUint(string(digits), 16, 32)
	return rune(n), err == nil
}
