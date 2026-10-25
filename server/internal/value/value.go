// Package value reads the typed values of a project's state (PRD §5.8.9): a
// variable's default (FR-VAR-01), and an attribute's default and the values
// that its components hold (FR-COMP-01), each a JSON value that its declared
// type says how to read, never its JSON form. It also converts a value of one
// type to another, as an attribute's retype does (FR-COMP-01 AC2).
package value

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
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
)

// The types of a value: a variable's (PRD FR-VAR-01, §5.8.3), and an
// attribute's, which may be text and enum too (FR-COMP-01).
const (
	Bool   = "bool"
	Int    = "int"
	Float  = "float"
	String = "string"
	// Text is a string of several lines, longer than a String may be.
	Text = "text"
	// Enum is one of the options of its attribute, as a string.
	Enum = "enum"
)

// types are the types of a value.
var types = []string{Bool, Int, Float, String, Text, Enum}

// Types returns the types of a value, in the order of the database's checks
// and of the schemas' enums, as a copy that the caller may change.
func Types() []string {
	return slices.Clone(types)
}

// Limits of a value (§5.8.1, §5.8.9).
const (
	// MaxInt is the greatest int, and -MaxInt the least: 2^53 − 1, the
	// expression language's (semantics.MaxInt).
	MaxInt = semantics.MaxInt
	// MaxStringLength is the most characters that a string may have, as a
	// string literal of the expression language may.
	MaxStringLength = 4096
	// MaxTextLength is the most characters that a text may have.
	MaxTextLength = 100_000
	// MaxOptions is the most options that an enum may have, and
	// MaxOptionLength the most characters that an option may have.
	MaxOptions      = 100
	MaxOptionLength = 100
)

// Bounds of how a value's number is written, whatever its value
// (formProblem). No int or float needs more: the shortest form of each, as
// JSON.stringify writes it, has 24 characters at most, and an exponent of 3
// digits at most, and PostgreSQL's jsonb, which holds the operation log's
// inverses, whose steps an undo sends as they are read back, writes it back
// as a plain decimal, without an exponent, of 327 characters at most:
// -5e-324 as -0. and 324 digits, the most that the shortest form of a double
// has after its point, since no two doubles are nearer than 2^-1074, about
// 4.9e-324, and -1.7976931348623157e308 as 310. Within the bounds, every way
// of writing a number is read exactly, by strconv.ParseFloat, which misreads
// an exponent of more than 5 digits or more than 800 digits before a point,
// and is held by the operation log, which keeps a payload's value as the
// client wrote it, in jsonb, whose numbers PostgreSQL's numeric holds only
// within an exponent of ±(2^30 − 1) and 16,383 digits after the point, its
// exponent counted, so that no spelling of a number reaches either limit.
// Package node bounds how a node's position and size are written by them
// too (plan note N43).
const (
	// MaxNumberLength is the most characters that a number may be written
	// with, its sign, its point, and its exponent included.
	MaxNumberLength = 327
	// MaxExponent is the greatest exponent, either way, that a number may be
	// written with, as 1e-999 is, past a double's range in both directions.
	MaxExponent = 999
)

// Value is a typed value, as JSON: a boolean, a number, or a string, which its
// type says how to read (Of). The zero Value is none, as a payload that leaves
// its value out has, which stands for the type's zero value.
type Value struct {
	raw json.RawMessage
}

// FromJSON returns the value whose JSON is raw, as the database holds it, to
// be read as its type says (Read).
func FromJSON(raw []byte) Value {
	return Value{raw: slices.Clone(raw)}
}

// JSON returns the value's JSON, as Of returned it, which is what is stored,
// or nil for none.
func (v Value) JSON() json.RawMessage {
	return v.raw
}

// valueSchema is the schema of a Value: a boolean, a number, or a string, of
// which a value's type takes one kind.
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
// value's type is known.
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
// which no value is.
func (v Value) IsZero() bool {
	return v.raw == nil
}

// Equal reports whether v and w are the same value of a type, as Of returns
// each: one JSON.
func (v Value) Equal(w Value) bool {
	return bytes.Equal(v.raw, w.raw)
}

// Zero returns the zero value of the type typ: false, 0, 0.0, which is
// written as 0, the empty string for a string or a text, or the first of
// options for an enum, or none for an enum without options, or for what is no
// type.
func Zero(typ string, options ...string) Value {
	switch typ {
	case Bool:
		return Value{raw: json.RawMessage("false")}
	case Int, Float:
		return Value{raw: json.RawMessage("0")}
	case String, Text:
		return Value{raw: json.RawMessage(`""`)}
	case Enum:
		if len(options) > 0 {
			return stringValue(options[0])
		}
	}
	return Value{}
}

// Checks are what Read checks of a value beyond what its type takes, which a
// value that a command sets passes (All), and a stored one need not.
type Checks struct {
	// Text checks a string's or a text's characters and its length
	// (input.CheckText).
	Text bool
	// Length checks that a number is written with at most MaxNumberLength
	// characters (formProblem).
	Length bool
}

// All checks everything of a value, as of one that a command sets.
var All = Checks{Text: true, Length: true}

// Of returns v as a value of the type typ, whose options are options for an
// enum, in its canonical form, which is what is stored and shown, or says
// what is wrong with it as a value of the type, in English, for programs and
// logs, as the message of a field's problem. A value of a type is: true or
// false for bool; for int, a number that is an integer from -MaxInt to
// MaxInt, however it is written, as 3, 3.0, or 3e0, which is written as 3;
// for float, a number that a double holds without overflowing to an
// infinity, read as the double nearest it, as JSON.parse reads it, and
// written as the shortest number that reads as that double again, -0 as 0,
// each written with at most MaxNumberLength characters and an exponent
// within ±MaxExponent (formProblem), so that 0e1000 is neither, though it is
// 0; for string, a string of at most MaxStringLength characters, and for
// text one of at most MaxTextLength, without control characters but line
// feeds and tabs (input.CheckText), and without an escape of a lone
// surrogate, which no UTF-8 holds, and which encoding/json would read as
// U+FFFD where JSON.parse keeps it, so that the server would store other
// text than the client holds; and for enum, a string that is one of options,
// as it is written there. A string is written without escaping <, >, and &
// (PRD §6.2). None, the zero Value, is the type's zero value (Zero).
func (v Value) Of(typ string, options ...string) (Value, string) {
	return v.Read(typ, All, options...)
}

// Read returns v as a value of the type typ as Of does, but checks a
// string's characters and its length, and a number's length, only as c
// says. A value that the database holds is read without either (Checks{}):
// the database's own check holds it to its kind and a string to its length,
// but not to its characters, and jsonb writes a number back without an
// exponent, which ParseFloat and integer read exactly however long it is, a
// float's having at most 309 digits before its point, so that a change of
// what input.CheckText or the bounds take, or a row that the pipeline did not
// write, leaves the project's values readable. A command that sets a value,
// the undo of a change of one among them, checks it whole as it applies
// (Of).
func (v Value) Read(typ string, c Checks, options ...string) (Value, string) {
	if !slices.Contains(types, typ) {
		// Neither a value nor none is one of what is no type.
		return Value{}, fmt.Sprintf("is a value of the type %q, which is no type", typ)
	}
	if v.raw == nil {
		z := Zero(typ, options...)
		if z.raw == nil {
			return Value{}, "is missing, and an enum without options has no zero value"
		}
		return z, ""
	}
	raw := string(v.raw)
	kind := kindOf(raw)
	switch {
	case raw == "null":
		return Value{}, "is null, which no value is"
	case kind == "":
		return Value{}, "is neither a boolean, a number, nor a string"
	case (typ == Int || typ == Float) && kind == "number":
		return number(typ, raw, c.Length)
	case typ == Bool && kind == "boolean":
		return Value{raw: json.RawMessage(raw)}, ""
	case typ == String && kind == "string":
		return text(v.raw, c.Text, MaxStringLength)
	case typ == Text && kind == "string":
		return text(v.raw, c.Text, MaxTextLength)
	case typ == Enum && kind == "string":
		return option(v.raw, options)
	}
	return Value{}, fmt.Sprintf("is a %s, not a value of the type %s", kind, typ)
}

// Kind returns the kind of v's JSON, boolean, number, or string, which a
// value of some type is, or "" for none and for any other, as null, an
// object, or an array, which no value is.
func (v Value) Kind() string {
	return kindOf(string(v.raw))
}

// Integral is an integer of a file, as a published file's count or a
// segment's decimals, or of a save state, as a visit count, which a consumer
// reads as an integer by its declaration, whatever its JSON form, and which
// a game may have read back as a float, as Godot's JSON parser reads every
// number (PRD §5.8.9): an integral JSON number however it is written, as 2,
// 2.0, or 2e0, from -MaxInt to MaxInt, as a JSON Schema's integer takes it,
// but for one written with an exponent beyond ±MaxExponent, as 0e1000,
// which no file holds, as Read reads an int without Checks.
type Integral int64

// UnmarshalJSON reads an integral JSON number, however it is written, as an
// Integral, as Read reads an int without Checks (formProblem, integer).
func (n *Integral) UnmarshalJSON(data []byte) error {
	raw := string(data)
	problem := "is not a number"
	var i int64
	if kindOf(raw) == "number" {
		if problem = formProblem(raw, false); problem == "" {
			i, problem = integer(raw)
		}
	}
	if problem != "" {
		return fmt.Errorf("value: %.40s is no integer of at most %d in magnitude: it %s", data, MaxInt, problem)
	}
	*n = Integral(i)
	return nil
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
// float, as Of does, but checks its length only if length is set.
func number(typ, raw string, length bool) (Value, string) {
	if problem := formProblem(raw, length); problem != "" {
		return Value{}, problem
	}
	if typ == Int {
		n, problem := integer(raw)
		if problem != "" {
			return Value{}, problem
		}
		return Value{raw: json.RawMessage(strconv.FormatInt(n, 10))}, ""
	}
	// Within formProblem's bounds, or written without an exponent, as jsonb
	// writes a stored float, of at most 309 digits before its point,
	// ParseFloat reads the number exactly, and returns an error only for one
	// beyond a double's range, which it reads as an infinity; one too small
	// for a double it reads as 0.
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return Value{}, "is beyond the range of a float, a double, which holds no number greater than about 1.8e308"
	}
	return floatValue(f), ""
}

// floatValue returns f, a finite double, as a float's value: the shortest
// number that reads as it again, -0 as 0, as the evaluators normalise it
// (§5.8.9).
func floatValue(f float64) Value {
	if f == 0 {
		f = 0
	}
	// A finite double always encodes.
	out, _ := json.Marshal(f)
	return Value{raw: out}
}

// formProblem says why raw, a JSON number, is not written within the bounds
// of a value's number, more than MaxNumberLength characters, if length is
// set, or an exponent beyond ±MaxExponent, whatever its value, or returns ""
// if it is. The exponent is read without its leading zeros, however many.
func formProblem(raw string, length bool) string {
	exponent := ""
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		exponent = strings.TrimLeft(strings.TrimLeft(raw[i+1:], "+-"), "0")
	}
	if n, err := strconv.Atoi(exponent); length && len(raw) > MaxNumberLength || exponent != "" &&
		(err != nil || n > MaxExponent) {
		return fmt.Sprintf("is written with more than %d characters, or with an exponent beyond ±%d, which no int "+
			"or float needs to be: the longest, -5e-324 written without an exponent, has %[1]d", MaxNumberLength,
			MaxExponent)
	}
	return ""
}

// integer returns the value of raw, a JSON number that formProblem took, if
// it is an integer from -MaxInt to MaxInt, or says why it is not one. It
// reads raw as a decimal, exactly, however long it is, so that a number that
// rounds to an integer as a double, as 4503599627370495.5, is not one.
func integer(raw string) (int64, string) {
	const notInteger = "is not an integer, which an int is"
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
	// the exponent, which is within ±MaxExponent, so that shift is bounded by
	// the number's length, which is checked against MaxInt's before zeros are
	// added; Atoi reads the exponent's sign and its leading zeros, and none as
	// 0.
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

// text returns raw, a JSON string, as a value of the type string or text, of
// at most maxChars characters, as Of does, or as Read does without
// input.CheckText unless checked is set.
func text(raw json.RawMessage, checked bool, maxChars int) (Value, string) {
	s, problem := decodeString(raw)
	if problem != "" {
		return Value{}, problem
	}
	if checked {
		if problem := input.CheckText(s, maxChars); problem != "" {
			return Value{}, problem
		}
	}
	return stringValue(s), ""
}

// option returns raw, a JSON string, as a value of an enum whose options are
// options, if it is one of them, as it is written there, or says why it is
// not.
func option(raw json.RawMessage, options []string) (Value, string) {
	s, problem := decodeString(raw)
	switch {
	case problem != "":
		return Value{}, problem
	case !slices.Contains(options, s):
		return Value{}, "is not one of the enum's options"
	}
	return stringValue(s), ""
}

// decodeString returns the string that raw, a JSON string, holds, or says why
// no string is read from it.
func decodeString(raw json.RawMessage) (string, string) {
	var s string
	switch {
	case !utf8.Valid(raw):
		return "", "is not UTF-8"
	case EscapesLoneSurrogate(raw):
		return "", "escapes a lone surrogate, which is not a character"
	case json.Unmarshal(raw, &s) != nil:
		return "", "is not a JSON string"
	}
	return s, ""
}

// stringValue returns s as a string's value, written without escaping <, >,
// and &, as the API writes its other texts (PRD §6.2), which json.Marshal
// would escape.
func stringValue(s string) Value {
	// A Go string always encodes, a byte that is not UTF-8 as U+FFFD.
	raw, _ := schemagen.Marshal(s)
	return Value{raw: raw}
}

// EscapesLoneSurrogate reports whether raw, a JSON text, escapes a UTF-16
// surrogate that is not one of a pair in one of its strings, as "\ud800",
// which is no character, and which encoding/json reads as U+FFFD, where
// JSON.parse keeps it. Every backslash of a JSON text is in a string, so raw
// may be one string or a whole document.
func EscapesLoneSurrogate(raw []byte) bool {
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

// CheckJSON says what makes data no JSON text that the published format
// holds, as a file or a value of one, or returns "": data must be one JSON
// value, in UTF-8, that holds null nowhere (PRD §6.2), whose strings escape
// no lone surrogate, and whose numbers are each written with an exponent
// within ±MaxExponent, if with one, as every number that the server writes
// is (formProblem), so that 0e1000, which is 0, is refused wherever it is.
// encoding/json reads a byte that is not UTF-8, and a lone surrogate, as
// U+FFFD, so that a reader that did not refuse them would read other text
// than the file holds.
func CheckJSON(data []byte) string {
	switch {
	case !json.Valid(data):
		return "is not one JSON value"
	case !utf8.Valid(data):
		return "is not UTF-8"
	case EscapesLoneSurrogate(data):
		return "escapes a lone surrogate in a string, which is not a character"
	}
	if problem := scanTokens(data, true); problem != "" {
		return problem
	}
	return ""
}

// ReadJSON returns data, one JSON value that CheckJSON takes, as a tree: an
// object as a map[string]any, of a key that it has twice the last, as a
// consumer reads it, an array as an []any, a number as the json.Number that
// it is written as, a string as a string, and a boolean as a bool; or fails,
// with "it" and what CheckJSON says of data, on data that CheckJSON refuses.
// Package published reads a file so, and package expr an AST, by what they
// hold as it is written.
func ReadJSON(data []byte) (any, error) {
	if problem := CheckJSON(data); problem != "" {
		return nil, fmt.Errorf("it %s", problem)
	}
	var tree any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	return tree, nil
}

// HoldsNull reports whether data, one JSON value, holds null anywhere
// outside its strings, which no file holds (PRD §6.2), and which CheckJSON
// refuses.
func HoldsNull(data []byte) bool {
	return scanTokens(data, false) != ""
}

// scanTokens says what of data, one JSON value, a file does not hold outside
// its strings, null or, if exponents is set, a number written with an
// exponent beyond ±MaxExponent, or returns "". Outside a string, null is the
// only literal that starts with n, and a number the only token that starts
// with - or a digit, which runs to the first byte that no number holds, and
// which holds no n.
func scanTokens(data []byte, exponents bool) string {
	inString := false
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case inString && c == '\\':
			// The escaped character, as the quote of \", ends no string.
			i++
		case c == '"':
			inString = !inString
		case inString:
		case c == 'n':
			return "holds null"
		case exponents && (c == '-' || '0' <= c && c <= '9'):
			end := i + 1
			for end < len(data) && strings.IndexByte("+-.eE0123456789", data[end]) >= 0 {
				end++
			}
			if formProblem(string(data[i:end]), false) != "" {
				return fmt.Sprintf("writes the number %.40s with an exponent beyond ±%d, as no number of a file is "+
					"written", data[i:end], MaxExponent)
			}
			i = end - 1
		}
	}
	return ""
}

// hex4 returns the value of four hexadecimal digits, in either case, and
// whether they are four such digits.
func hex4(digits []byte) (rune, bool) {
	n, err := strconv.ParseUint(string(digits), 16, 32)
	return rune(n), err == nil
}

// CheckOptions says what is wrong with options as the options of an enum, or
// returns "": at least one and at most MaxOptions of them, each of 1 to
// MaxOptionLength characters, and no two alike, which the database's check
// holds a stored enum's options to as well. The text rules of a name, which a
// stored option may break once they are tightened, are CheckOptionNames'.
func CheckOptions(options []string) string {
	switch {
	case len(options) == 0:
		return "is empty, and an enum has at least one option"
	case len(options) > MaxOptions:
		return fmt.Sprintf("has more than %d options", MaxOptions)
	}
	seen := make(map[string]bool, len(options))
	for i, o := range options {
		switch n := utf8.RuneCountInString(o); {
		case n == 0 || n > MaxOptionLength:
			return fmt.Sprintf("has an option, the %s, of other than 1 to %d characters", ordinal(i), MaxOptionLength)
		case seen[o]:
			return fmt.Sprintf("has the option %q twice", o)
		}
		seen[o] = true
	}
	return ""
}

// CheckOptionNames says what is wrong with an enum's options as names that
// people are shown, sent without the white space around them
// (input.CheckTrimmedName), or returns "". An option holds no line feed, so
// that options can be written one to a line, and no U+FFFD REPLACEMENT
// CHARACTER, which encoding/json reads an escape of a lone surrogate as,
// where JSON.parse keeps it, so that the server never holds another option
// than the client sent: a value that is the option as the client holds it
// would be refused for its lone surrogate (Of), and could never be set.
func CheckOptionNames(options []string) string {
	for i, o := range options {
		if problem := input.CheckTrimmedName(o, MaxOptionLength); problem != "" {
			return fmt.Sprintf("has an option, the %s, that %s", ordinal(i), problem)
		}
		if strings.ContainsRune(o, utf8.RuneError) {
			return fmt.Sprintf("has an option, the %s, that holds U+FFFD, the replacement character, which an "+
				"escape of a lone surrogate is read as", ordinal(i))
		}
	}
	return ""
}

// ordinal returns the place of the item at index i, as 1st for 0.
func ordinal(i int) string {
	n := i + 1
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// Convert returns v, a value of the type from, whose options are fromOptions
// for an enum, as Of returned it, as a value of the type to, whose options
// are toOptions for an enum, in its canonical form, and whether it converts
// (FR-COMP-01 AC2). A value converts as the expression language's to_int,
// to_float, and to_string convert it (§5.8.4, §5.8.9), and as a string's
// value otherwise:
//
//   - to bool, the bool itself, an int or a float that is 0 or 1, as false
//     and true, or a string, a text, or an enum's option that is true or
//     false;
//   - to int, a bool as 0 or 1, a float truncated toward zero, within ±MaxInt,
//     or a string, a text, or an option of the form [+-]?[0-9]+ within
//     ±MaxInt;
//   - to float, a bool as 0 or 1, an int widened, or a string, a text, or an
//     option of the form [+-]?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?, read as the
//     double nearest it, however many digits it is written with, and however
//     large its exponent, as to_float reads it, 0 for one too small for a
//     double, unless it is beyond a double's range;
//   - to string or text, a bool as true or false, an int or a float as
//     to_string writes it (semantics.FormatInt, semantics.FormatFloat), or a
//     string, a text, or an option as it is, each within the type's length,
//     counted in code points as len counts them (semantics.Len), with its
//     characters as they are, which the text rules checked as the value was
//     set, so that a stored value that today's rules refuse (input.CheckText)
//     converts as it is read (Read);
//   - to enum, a value whose string, as above, is one of toOptions.
//
// A value of another type than from, or of no type, does not convert.
func Convert(v Value, from string, fromOptions []string, to string, toOptions []string) (Value, bool) {
	v, problem := v.Read(from, Checks{}, fromOptions...)
	if problem != "" {
		return Value{}, false
	}
	var s string
	switch from {
	case Bool:
		b := string(v.raw) == "true"
		switch to {
		case Bool:
			return v, true
		case Int, Float:
			if b {
				return Value{raw: json.RawMessage("1")}, true
			}
			return Value{raw: json.RawMessage("0")}, true
		}
		s = strconv.FormatBool(b)
	case Int:
		n, _ := strconv.ParseInt(string(v.raw), 10, 64)
		switch to {
		case Bool:
			return boolOf(float64(n))
		case Int, Float:
			return v, true
		}
		s = semantics.FormatInt(n)
	case Float:
		f, _ := strconv.ParseFloat(string(v.raw), 64)
		switch to {
		case Bool:
			return boolOf(f)
		case Int:
			// Truncated, and compared with ±MaxInt before it is converted, as
			// to_int converts it (§5.8.9).
			n, problem := semantics.Trunc(f)
			if problem != "" {
				return Value{}, false
			}
			return Value{raw: json.RawMessage(strconv.FormatInt(n, 10))}, true
		case Float:
			return v, true
		}
		s = semantics.FormatFloat(f)
	default:
		// A string, a text, or an option, which Read took.
		s, _ = decodeString(v.raw)
	}
	return fromString(s, to, toOptions)
}

// boolOf returns n as a bool, false for 0 and true for 1, and whether it is
// either.
func boolOf(n float64) (Value, bool) {
	switch n {
	case 0:
		return Value{raw: json.RawMessage("false")}, true
	case 1:
		return Value{raw: json.RawMessage("true")}, true
	}
	return Value{}, false
}

// fromString returns s, a string, a text, an option, or what another value is
// written as, as a value of the type to, whose options are options for an
// enum, as Convert does, and whether it converts.
func fromString(s, to string, options []string) (Value, bool) {
	switch to {
	case Bool:
		if s == "true" || s == "false" {
			return Value{raw: json.RawMessage(s)}, true
		}
		return Value{}, false
	case Int:
		n, problem := semantics.ParseInt(s)
		if problem != "" {
			return Value{}, false
		}
		return Value{raw: json.RawMessage(strconv.FormatInt(n, 10))}, true
	case Float:
		f, problem := semantics.ParseFloat(s)
		if problem != "" {
			return Value{}, false
		}
		return floatValue(f), true
	case String:
		return stringOf(s, MaxStringLength)
	case Text:
		return stringOf(s, MaxTextLength)
	case Enum:
		out, problem := option(stringValue(s).raw, options)
		return out, problem == ""
	}
	return Value{}, false
}

// stringOf returns s as a value of a string or a text, of at most maxChars
// characters, with its characters as they are, and whether it is within the
// length.
func stringOf(s string, maxChars int) (Value, bool) {
	if semantics.Len(s) > maxChars {
		return Value{}, false
	}
	return stringValue(s), true
}
