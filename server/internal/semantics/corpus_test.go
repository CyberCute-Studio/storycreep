package semantics

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"unicode/utf8"
)

// corpusFiles are the files of the expression corpus whose cases apply one
// operation of the language to values, as schema/expr-corpus/README.md
// describes them.
var corpusFiles = []string{"numbers.json", "strings.json", "formatting.json"}

// A corpusCase is one case of the corpus: an operation, its arguments, and
// the result and the runtime error that it gives.
type corpusCase struct {
	Note     string       `json:"note"`
	Op       string       `json:"op"`
	Args     []typedValue `json:"args"`
	Decimals *int         `json:"decimals"`
	Result   typedValue   `json:"result"`
	Error    RuntimeError `json:"error"`
}

// A typedValue is a value of the language, as the corpus writes it: its type
// and its JSON, which the type says how to read, never its JSON form.
type typedValue struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// A value is a value of the language, read: one of its fields, as its type
// says.
type value struct {
	typ string
	i   int64
	f   float64
	s   string
	b   bool
}

// TestCorpus runs every case of the expression corpus's files of
// operations, as the TypeScript and GDScript evaluators run them too, and
// checks the corpus itself: that each case's arguments are values of the
// language, as an int within ±MaxInt, and a float finite and not -0, and that
// a case with an error yields the zero value of its result's type.
func TestCorpus(t *testing.T) {
	t.Parallel()
	for _, name := range corpusFiles {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "expr-corpus", name))
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Cases []corpusCase `json:"cases"`
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&file); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(file.Cases) == 0 {
			t.Errorf("%s has no cases", name)
		}
		notes := make(map[string]bool)
		for _, c := range file.Cases {
			where := fmt.Sprintf("%s: %s", name, c.Note)
			if c.Note == "" || notes[c.Note] {
				t.Errorf("%s: a case's note is empty or another case's", where)
			}
			notes[c.Note] = true
			args := make([]value, len(c.Args))
			for i, a := range c.Args {
				args[i] = read(t, where, a)
			}
			want := read(t, where, c.Result)
			if c.Error != "" && want != (value{typ: want.typ}) {
				t.Errorf("%s: the case has the error %s, and a result other than its type's zero value", where,
					c.Error)
			}
			if c.Decimals != nil && c.Op != "interpolate" {
				t.Errorf("%s: only interpolate has decimals", where)
			}
			if c.Decimals != nil && (*c.Decimals < 0 || *c.Decimals > MaxDecimals) {
				t.Errorf("%s: decimals is %d, not from 0 to %d, as a format specifier's is", where, *c.Decimals,
					MaxDecimals)
			}
			got, problem, ok := apply(c.Op, args, c.Decimals)
			switch {
			case !ok:
				t.Errorf("%s: %s of %v is no operation of the language", where, c.Op, types(args))
			case got.typ != want.typ || !same(got, want) || problem != c.Error:
				t.Errorf("%s: %s(%v) = %v, %q; want %v, %q", where, c.Op, args, got, problem, want, c.Error)
			}
		}
	}
}

// read returns the value that v writes, as its type says, and fails the test
// when it is not a value of that type. It reads a float with ParseFloat, as
// to_float reads a string, which the corpus's README says that a runner
// does, and a string only when it is of code points: UTF-8, and without an
// escape of a lone surrogate, which json.Unmarshal reads as U+FFFD rather
// than refusing it.
func read(t *testing.T, where string, v typedValue) value {
	t.Helper()
	out := value{typ: v.Type}
	var err error
	switch v.Type {
	case "int":
		out.i, err = strconv.ParseInt(string(v.Value), 10, 64)
		if err == nil && !isInt(out.i) {
			err = fmt.Errorf("beyond ±%d", int64(MaxInt))
		}
	case "float":
		// A JSON number is of to_float's grammar, which a JSON string, or
		// any other value, is not. ParseFloat reads -0 as 0, so the sign is
		// checked on the number as it is written.
		var problem RuntimeError
		out.f, problem = ParseFloat(string(v.Value))
		switch {
		case problem != "":
			err = fmt.Errorf("to_float reports %s", problem)
		case out.f == 0 && bytes.HasPrefix(v.Value, []byte("-")):
			err = fmt.Errorf("-0")
		}
	case "string":
		err = json.Unmarshal(v.Value, &out.s)
		if err == nil && (!utf8.Valid(v.Value) || loneSurrogate(string(v.Value))) {
			err = fmt.Errorf("not of code points")
		}
	case "bool":
		err = json.Unmarshal(v.Value, &out.b)
	default:
		err = fmt.Errorf("no type")
	}
	if err != nil {
		t.Fatalf("%s: %s is no value of the type %q: %v", where, v.Value, v.Type, err)
	}
	return out
}

// loneSurrogate reports whether raw, a JSON string, escapes a UTF-16
// surrogate other than as a high one followed at once by a low one, which is
// how JSON escapes a code point beyond U+FFFF.
func loneSurrogate(raw string) bool {
	// Whether the escape just read is of a high surrogate, which the next
	// escape must be of a low one after.
	high := false
	for i := 0; i < len(raw); i++ {
		var escaped uint64
		isEscape := raw[i] == '\\' && i+5 < len(raw) && raw[i+1] == 'u'
		if isEscape {
			// raw is JSON, so four hexadecimal digits follow \u.
			escaped, _ = strconv.ParseUint(raw[i+2:i+6], 16, 32)
		}
		switch {
		case isEscape && escaped >= 0xD800 && escaped < 0xDC00:
			if high {
				return true
			}
			high = true
		case isEscape && escaped >= 0xDC00 && escaped < 0xE000:
			if !high {
				return true
			}
			high = false
		case high:
			return true
		}
		switch {
		case isEscape:
			i += 5
		case raw[i] == '\\':
			// An escape of one character, as \" or \\, whose second
			// character starts no escape.
			i++
		}
	}
	return high
}

// same reports whether a and b, of one type, are the same value: a float's
// bits, so that -0 is not 0.
func same(a, b value) bool {
	if a.typ == "float" {
		return math.Float64bits(a.f) == math.Float64bits(b.f)
	}
	return a == b
}

// types returns the types of args.
func types(args []value) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a.typ
	}
	return out
}

// apply applies the operation op to args, as the language types it
// (§5.8.3): an int beside a float is widened, and + of two strings
// concatenates them. It reports whether op takes args.
func apply(op string, args []value, decimals *int) (value, RuntimeError, bool) {
	if op == "interpolate" {
		if len(args) != 1 {
			return value{}, "", false
		}
		return value{typ: "string", s: interpolate(args[0], decimals)}, "", true
	}
	switch len(args) {
	case 1:
		return unary(op, args[0])
	case 2:
		return binary(op, args[0], args[1])
	case 3:
		return clamp(op, args[0], args[1], args[2])
	}
	return value{}, "", false
}

// clamp applies clamp, the one operation of three arguments, to x, lo, and
// hi: min(max(x, lo), hi), of the widened type of all three, so that it is
// hi when lo is greater than hi.
func clamp(op string, x, lo, hi value) (value, RuntimeError, bool) {
	numeric := func(v value) bool { return v.typ == "int" || v.typ == "float" }
	if op != "clamp" || !numeric(x) || !numeric(lo) || !numeric(hi) {
		return value{}, "", false
	}
	if x.typ == "int" && lo.typ == "int" && hi.typ == "int" {
		return value{typ: "int", i: min(max(x.i, lo.i), hi.i)}, "", true
	}
	asFloat := func(v value) float64 {
		if v.typ == "int" {
			return float64(v.i)
		}
		return v.f
	}
	return value{typ: "float", f: min(max(asFloat(x), asFloat(lo)), asFloat(hi))}, "", true
}

// interpolate returns v as a text value reference shows it, with decimals
// digits, if any, when it is a number (FR-TEXT-02).
func interpolate(v value, decimals *int) string {
	switch {
	case decimals != nil && v.typ == "int":
		return FormatFixed(float64(v.i), *decimals)
	case decimals != nil && v.typ == "float":
		return FormatFixed(v.f, *decimals)
	}
	return toString(v)
}

// toString returns v as to_string writes it.
func toString(v value) string {
	switch v.typ {
	case "int":
		return FormatInt(v.i)
	case "float":
		return FormatFloat(v.f)
	case "bool":
		return strconv.FormatBool(v.b)
	}
	return v.s
}

// unary applies the operation op to the one argument a.
func unary(op string, a value) (value, RuntimeError, bool) {
	ints := func(n int64, e RuntimeError) (value, RuntimeError, bool) { return value{typ: "int", i: n}, e, true }
	floats := func(f float64, e RuntimeError) (value, RuntimeError, bool) {
		return value{typ: "float", f: f}, e, true
	}
	str := func(s string) (value, RuntimeError, bool) { return value{typ: "string", s: s}, "", true }
	switch {
	case op == "-" && a.typ == "int":
		return ints(NegInt(a.i))
	case op == "-" && a.typ == "float":
		return floats(NegFloat(a.f))
	case op == "abs" && a.typ == "int":
		return ints(max(a.i, -a.i), "")
	case op == "abs" && a.typ == "float":
		return floats(math.Abs(a.f), "")
	case op == "to_int" && a.typ == "float":
		return ints(Trunc(a.f))
	case op == "to_int" && a.typ == "string":
		return ints(ParseInt(a.s))
	case op == "to_float" && a.typ == "int":
		return floats(float64(a.i), "")
	case op == "to_float" && a.typ == "string":
		return floats(ParseFloat(a.s))
	case op == "to_string":
		return str(toString(a))
	case op == "floor" && a.typ == "float":
		return ints(Floor(a.f))
	case op == "ceil" && a.typ == "float":
		return ints(Ceil(a.f))
	case op == "round" && a.typ == "float":
		return ints(Round(a.f))
	case op == "len" && a.typ == "string":
		return ints(int64(Len(a.s)), "")
	case op == "upper" && a.typ == "string":
		return str(Upper(a.s))
	case op == "lower" && a.typ == "string":
		return str(Lower(a.s))
	case op == "trim" && a.typ == "string":
		return str(Trim(a.s))
	}
	return value{}, "", false
}

// binary applies the operation op to the arguments a and b.
func binary(op string, a, b value) (value, RuntimeError, bool) {
	boolean := func(v bool) (value, RuntimeError, bool) { return value{typ: "bool", b: v}, "", true }
	numeric := func(v value) bool { return v.typ == "int" || v.typ == "float" }
	asFloat := func(v value) float64 {
		if v.typ == "int" {
			return float64(v.i)
		}
		return v.f
	}
	switch {
	case a.typ == "int" && b.typ == "int":
		var n int64
		var e RuntimeError
		switch op {
		case "+":
			n, e = AddInt(a.i, b.i)
		case "-":
			n, e = SubInt(a.i, b.i)
		case "*":
			n, e = MulInt(a.i, b.i)
		case "/":
			n, e = DivInt(a.i, b.i)
		case "%":
			n, e = ModInt(a.i, b.i)
		case "min":
			n = min(a.i, b.i)
		case "max":
			n = max(a.i, b.i)
		default:
			return compare(op, cmp.Compare(a.i, b.i))
		}
		return value{typ: "int", i: n}, e, true
	case numeric(a) && numeric(b):
		x, y := asFloat(a), asFloat(b)
		var f float64
		var e RuntimeError
		switch op {
		case "+":
			f, e = AddFloat(x, y)
		case "-":
			f, e = SubFloat(x, y)
		case "*":
			f, e = MulFloat(x, y)
		case "/":
			f, e = DivFloat(x, y)
		case "%":
			// % takes ints only.
			return value{}, "", false
		case "min":
			f = min(x, y)
		case "max":
			f = max(x, y)
		default:
			return compare(op, cmp.Compare(x, y))
		}
		return value{typ: "float", f: f}, e, true
	case a.typ == "string" && b.typ == "string":
		switch op {
		case "+":
			s, e := Concat(a.s, b.s)
			return value{typ: "string", s: s}, e, true
		case "contains":
			return boolean(Contains(a.s, b.s))
		case "starts_with":
			return boolean(StartsWith(a.s, b.s))
		case "ends_with":
			return boolean(EndsWith(a.s, b.s))
		}
		return compare(op, Compare(a.s, b.s))
	case a.typ == "bool" && b.typ == "bool" && (op == "==" || op == "!="):
		return boolean(a.b == b.b == (op == "=="))
	}
	return value{}, "", false
}

// compare returns the comparison op of two values that order is the order
// of, -1, 0, or +1.
func compare(op string, order int) (value, RuntimeError, bool) {
	var v bool
	switch op {
	case "==":
		v = order == 0
	case "!=":
		v = order != 0
	case "<":
		v = order < 0
	case "<=":
		v = order <= 0
	case ">":
		v = order > 0
	case ">=":
		v = order >= 0
	default:
		return value{}, "", false
	}
	return value{typ: "bool", b: v}, "", true
}
