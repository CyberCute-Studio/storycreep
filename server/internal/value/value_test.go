package value

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestValueOf checks how a default is read as a value of each type: an int
// is an integer from -MaxInt to MaxInt however it is written, read exactly as
// a decimal rather than as a double, a float any finite number, read as the
// nearest double and written as the shortest number that reads as it again,
// a string a string of at most MaxStringLength characters without control
// characters but line feeds and tabs, and without an escaped lone surrogate,
// and a value of another kind, or null, none of them.
//
// Verifies: FR-VAR-01/AC2
func TestValueOf(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", MaxStringLength)
	for _, c := range []struct {
		typ, in, want string
	}{
		{Bool, "true", "true"},
		{Bool, "false", "false"},
		{Int, "3", "3"},
		{Int, "-3", "-3"},
		{Int, "-0", "0"},
		{Int, "0.0", "0"},
		{Int, "3.0", "3"},
		{Int, "3e0", "3"},
		{Int, "30E-1", "3"},
		{Int, "1.5e1", "15"},
		{Int, "0.1e1", "1"},
		{Int, "0.000e999", "0"},
		{Int, "0e+000999", "0"},
		{Int, "9007199254740991", "9007199254740991"},
		{Int, "-9007199254740991", "-9007199254740991"},
		{Int, "9.007199254740991e15", "9007199254740991"},
		{Int, "100000000000000E-14", "1"},
		{Float, "3", "3"},
		{Float, "2.0", "2"},
		{Float, "0.1", "0.1"},
		{Float, "-0", "0"},
		{Float, "-0.0", "0"},
		{Float, "1e-400", "0"},
		{Float, "-1e-400", "0"},
		{Float, "1e-999", "0"},
		{Float, "-0e999", "0"},
		{Float, "4.9e-324", "5e-324"},
		{Int, "-0e-999", "0"},
		// The longest that a number may be written with, 327 characters.
		{Int, "3." + strings.Repeat("0", 325), "3"},
		{Float, "-0." + strings.Repeat("0", 319) + "1e320", "-1"},
		{Float, "1e21", "1e+21"},
		{Float, "1.7976931348623157e308", "1.7976931348623157e+308"},
		// As PostgreSQL's jsonb writes a float's default back, without an
		// exponent, the longest, -5e-324, with 327 characters among them.
		{Float, "-0." + strings.Repeat("0", 323) + "5", "-5e-324"},
		{Float, "-17976931348623157" + strings.Repeat("0", 292), "-1.7976931348623157e+308"},
		{Float, "1" + strings.Repeat("0", 100), "1e+100"},
		{Float, "9007199254740993", "9007199254740992"},
		{String, `""`, `""`},
		{String, `"Elara"`, `"Elara"`},
		{String, `"two\nlines\tand a tab"`, `"two\nlines\tand a tab"`},
		{String, `"\u00e9\ud83d\ude00"`, `"é😀"`},
		{String, `" padded "`, `" padded "`},
		{String, `"` + long + `"`, `"` + long + `"`},
	} {
		got, problem := Value{raw: json.RawMessage(c.in)}.Of(c.typ)
		if problem != "" || string(got.raw) != c.want {
			t.Errorf("%s %s is %s, %q; want %s", c.typ, c.in, got.raw, problem, c.want)
		}
	}
	for _, c := range []struct{ typ, in string }{
		{Bool, "0"},
		{Bool, `"true"`},
		{Int, `"3"`},
		{Int, "true"},
		{Int, "3.5"},
		{Int, "0.11e1"},
		{Int, "4503599627370495.5"},
		{Int, "9007199254740992"},
		{Int, "-9007199254740992"},
		{Int, "1e16"},
		{Int, "1e999999999999"},
		{Int, "1e-999999999999"},
		{Int, "12345678901234567890123"},
		{Float, "1e400"},
		{Float, "-1e400"},
		{Float, "1e999"},
		// Written past the bounds, more than 327 characters or an exponent
		// beyond ±999, though each reads as a value of its type.
		{Int, "3." + strings.Repeat("0", 326)},
		{Float, "3." + strings.Repeat("0", 326)},
		{Float, "-0." + strings.Repeat("0", 324) + "5"},
		{Int, "0e1000"},
		{Float, "0e-1000"},
		{Float, "1E-1000"},
		{Int, "0.000e99999999999"},
		{Float, "-0.0E+99999999999999999999"},
		{Int, "0e1073741824"},
		{Float, "1e-16384"},
		{Float, "-0." + strings.Repeat("0", 99999) + "1e100000"},
		{Float, "1" + strings.Repeat("0", 1000) + "e-1000"},
		{Float, `"1.5"`},
		{String, "3"},
		{String, `"` + long + `e"`},
		{String, `"bell\u0007"`},
		{String, `"carriage\rreturn"`},
		{String, `"right\u202eleft"`},
		{String, `"line\u2028separator"`},
		{String, `"lone \ud800"`},
		{String, `"lone \udc00 low"`},
		{String, `"swapped \udc00\ud800"`},
		{String, `"high then text \ud800x"`},
		{Bool, "null"},
		{String, "null"},
		{Int, "[3]"},
		{"bigint", "3"},
	} {
		if got, problem := (Value{raw: json.RawMessage(c.in)}).Of(c.typ); problem == "" {
			t.Errorf("%s %s was taken as %s", c.typ, c.in, got.raw)
		}
	}
	// None is the type's zero value.
	for typ, want := range map[string]string{Bool: "false", Int: "0", Float: "0", String: `""`} {
		if got, problem := (Value{}).Of(typ); problem != "" || string(got.raw) != want {
			t.Errorf("none of %s is %s, %q; want %s", typ, got.raw, problem, want)
		}
	}
	// An escaped backslash before u starts no escape.
	if got, problem := (Value{raw: json.RawMessage(`"\\ud800"`)}).Of(String); problem != "" ||
		string(got.raw) != `"\\ud800"` {
		t.Errorf(`"\\ud800" is %s, %q`, got.raw, problem)
	}
	// A stored default is read without the bound of a number's length, as
	// jsonb writes a float of 1e-999 or an int of 3.000... back, without an
	// exponent, but not without that of its exponent, which jsonb never
	// writes.
	for _, c := range []struct{ typ, in, want string }{
		{Float, "0." + strings.Repeat("0", 998) + "1", "0"},
		{Float, "-1" + strings.Repeat("0", 308) + "." + strings.Repeat("9", 2000), "-1e+308"},
		{Int, "3." + strings.Repeat("0", 2000), "3"},
		{Int, "9007199254740991" + "." + strings.Repeat("0", 500), "9007199254740991"},
		{Int, "1" + strings.Repeat("0", 500), ""},
		{Int, "1e1000", ""},
	} {
		got, problem := Value{raw: json.RawMessage(c.in)}.Read(c.typ, Checks{})
		if c.want == "" && problem == "" || c.want != "" && (problem != "" || string(got.raw) != c.want) {
			t.Errorf("the stored %s %s is %s, %q; want %q", c.typ, c.in, got.raw, problem, c.want)
		}
	}
}

// FuzzNumberValue checks that a default's number is read exactly: a JSON
// number written within the bounds is taken as an int exactly when math/big
// reads it as an integer from -MaxInt to MaxInt, and as that integer, and as
// a float exactly when math/big rounds it to a finite double, as that double,
// however it is written; and one written past them is neither.
func FuzzNumberValue(f *testing.F) {
	for _, seed := range []string{"3", "-0", "3.0", "30E-1", "4503599627370495.5", "9007199254740991",
		"9007199254740992", "1e16", "0.000e5", "100000000000000E-14", "-9.007199254740991e15", "1e-7", "12e+1",
		"4.9e-324", "1.7976931348623157e308", "1e309", "0e1000", "2.2250738585072011e-308"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if !numberForm.MatchString(raw) {
			return
		}
		gotInt, intProblem := Value{raw: json.RawMessage(raw)}.Of(Int)
		gotFloat, floatProblem := Value{raw: json.RawMessage(raw)}.Of(Float)
		if formProblem(raw, true) != "" {
			if intProblem == "" || floatProblem == "" {
				t.Errorf("%s, past the bounds, is taken as the int %s or the float %s", raw, gotInt.raw, gotFloat.raw)
			}
			return
		}
		want, ok := new(big.Rat).SetString(raw)
		if !ok {
			t.Fatalf("big.Rat does not read %s", raw)
		}
		if problem := numberProblem(raw, want, gotInt, intProblem, gotFloat, floatProblem); problem != "" {
			t.Error(problem)
		}
	})
}

// numberProblem says how a number raw, written within the bounds, which
// math/big reads as want, was read wrong as an int, as gotInt with the
// problem intProblem, or as a float, as gotFloat with floatProblem, or
// returns "" if it was read right: as an int exactly when want is an integer
// from -MaxInt to MaxInt, and as want, and as a float exactly when want
// rounds to a finite double, as that double, -0 as 0.
func numberProblem(raw string, want *big.Rat, gotInt Value, intProblem string, gotFloat Value,
	floatProblem string) string {
	switch integral := want.IsInt() && want.Num().CmpAbs(big.NewInt(MaxInt)) <= 0; {
	case integral && (intProblem != "" || string(gotInt.raw) != want.Num().String()):
		return fmt.Sprintf("%s is the int %s, %q; want %s", raw, gotInt.raw, intProblem, want.Num())
	case !integral && intProblem == "":
		return fmt.Sprintf("%s, which is no int, is taken as %s", raw, gotInt.raw)
	}
	f, _ := want.Float64()
	if f == 0 {
		f = 0
	}
	switch wantFloat, _ := json.Marshal(f); {
	case math.IsInf(f, 0) && floatProblem == "":
		return fmt.Sprintf("%s, beyond a double's range, is taken as %s", raw, gotFloat.raw)
	case !math.IsInf(f, 0) && (floatProblem != "" || string(gotFloat.raw) != string(wantFloat)):
		return fmt.Sprintf("%s is the float %s, %q; want %s", raw, gotFloat.raw, floatProblem, wantFloat)
	}
	return ""
}

// TestNumberForms checks that a number written within the bounds, at most 327
// characters and an exponent within ±999, is read exactly, however it is
// written, the longest mantissas with the greatest exponents either way
// among them, as math/big reads it (numberProblem), and that one written past
// them is neither an int nor a float, whatever it reads as.
// TestLogHoldsNumbers checks that the operation log's jsonb holds each that
// is taken.
//
// Verifies: FR-VAR-01/AC2
func TestNumberForms(t *testing.T) {
	t.Parallel()
	// The mantissas leave 5 characters of the 327 for the longest exponent,
	// e-999, as the longest number has them.
	mantissas := []string{"0", "-0", "0.0", "1", "-1.5", "3.000", "9007199254740991", "9007199254740993",
		"1.7976931348623157", "2.2250738585072011", "4.9406564584124654", "-0." + strings.Repeat("0", 318) + "1",
		"1" + strings.Repeat("0", 321), "9." + strings.Repeat("9", 320), "4503599627370495.5",
		"0." + strings.Repeat("0", 320), "-0." + strings.Repeat("0", 318) + "5"}
	exponents := []string{"", "e0", "E+5", "e-1", "e-15", "e15", "e16", "e308", "e309", "e-308", "e-324", "e-325",
		"e+999", "e-999", "e0999", "e1000", "e-1000", "e99999999999"}
	for _, m := range mantissas {
		for _, e := range exponents {
			raw := m + e
			gotInt, intProblem := Value{raw: json.RawMessage(raw)}.Of(Int)
			gotFloat, floatProblem := Value{raw: json.RawMessage(raw)}.Of(Float)
			if formProblem(raw, true) != "" {
				if intProblem == "" || floatProblem == "" {
					t.Errorf("%s, past the bounds, is taken as the int %s or the float %s", raw, gotInt.raw,
						gotFloat.raw)
				}
				continue
			}
			want, ok := new(big.Rat).SetString(raw)
			if !ok {
				t.Fatalf("big.Rat does not read %s", raw)
			}
			if problem := numberProblem(raw, want, gotInt, intProblem, gotFloat, floatProblem); problem != "" {
				t.Error(problem)
			}
		}
	}
	// The bounds' edges: 327 characters and 999 either way are within them,
	// 328 characters and 1000 past them.
	for raw, within := range map[string]bool{
		"1." + strings.Repeat("0", 325): true, "1." + strings.Repeat("0", 326): false, "1e999": true,
		"1e-999": true, "0e+000999": true, "1e1000": false, "1e-1000": false, "1E+01000": false,
	} {
		if got := formProblem(raw, true) == ""; got != within {
			t.Errorf("%s is within the bounds: %t, want %t", raw, got, within)
		}
	}
}

// TestPlainDecimals checks that a float's default, as PostgreSQL's jsonb
// writes it back, without an exponent, as strconv.FormatFloat writes it with
// the format 'f', which TestStoredNumbers checks against PostgreSQL, is
// written within the bounds, and is read as the default again, for doubles of
// every binary exponent, either sign, the least and the greatest of each, and
// others, the longest, -5e-324, among them, so that the undo of a change of a
// default, whose inverse the operation log writes back alike, is taken.
//
// Verifies: FR-VAR-01/AC2
func TestPlainDecimals(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(33, 7))
	longest := 0
	for exponent := range uint64(0x7FF) {
		for i := range 64 {
			mantissa := r.Uint64() & (1<<52 - 1)
			if extremes := []uint64{0, 1, 1<<52 - 1}; i < len(extremes) {
				mantissa = extremes[i]
			}
			for _, sign := range []uint64{0, 1 << 63} {
				f := math.Float64frombits(sign | exponent<<52 | mantissa)
				plain := strconv.FormatFloat(f, 'f', -1, 64)
				longest = max(longest, len(plain))
				if f == 0 {
					f = 0
				}
				want, _ := json.Marshal(f)
				if got, problem := (Value{raw: json.RawMessage(plain)}).Of(Float); problem != "" ||
					string(got.raw) != string(want) {
					t.Errorf("%s, of %s, is %s, %q", plain, want, got.raw, problem)
				}
			}
		}
	}
	if longest != MaxNumberLength {
		t.Errorf("the longest plain decimal of a double has %d characters, want %d", longest, MaxNumberLength)
	}
}

// numberForm is the form of a JSON number.
var numberForm = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// TestValueJSON checks that a Value keeps its JSON as it is sent, null
// included, which Of then refuses, and writes none as null.
func TestValueJSON(t *testing.T) {
	t.Parallel()
	var p struct {
		Default Value `json:"default"`
	}
	for _, in := range []string{`{"default": 3.0}`, `{"default": null}`, `{"default": "x"}`} {
		if err := json.Unmarshal([]byte(in), &p); err != nil {
			t.Fatal(err)
		}
		if out, _ := json.Marshal(p); !strings.Contains(in, string(p.Default.raw)) ||
			string(out) != `{"default":`+string(p.Default.raw)+`}` {
			t.Errorf("%s kept %s, and wrote %s", in, p.Default.raw, out)
		}
	}
	if out, err := json.Marshal(Value{}); err != nil || string(out) != "null" {
		t.Errorf("none writes %s, %v", out, err)
	}
}

// TestCheckJSON checks what CheckJSON refuses of a JSON text: what is not one
// JSON value, a byte that is not UTF-8, null anywhere outside a string, a
// lone surrogate escaped in a string, and a number written with an exponent
// beyond ±999 anywhere, but not a string that holds null, an escaped quote,
// or such a number, nor a pair of surrogates.
func TestCheckJSON(t *testing.T) {
	t.Parallel()
	for in, ok := range map[string]bool{
		`{"a":[1,"b",true,{"c":2.5}]}`: true,
		`"null"`:                       true,
		`{"n":"\"null\\","m":"x\\"}`:   true,
		`{"a":"\ud83d\ude00"}`:         true,
		`{"a":"\u00e9 é"}`:             true,
		`null`:                         false,
		`{"a":null}`:                   false,
		`[1, null]`:                    false,
		`{"a":"\"","b":null}`:          false,
		`{"a":"\\","b":null}`:          false,
		`{"a":"\ud800"}`:               false,
		`{"a":"\udc00\ud800"}`:         false,
		`{"a":1}}`:                     false,
		`{"a":1} {}`:                   false,
		`{"a":`:                        false,
		"{\"a\":\"\xff\"}":             false,
		"{\"a\":\"\xed\xa0\x80\"}":     false,
		`{"a":"x","n":"` + "\t" + `"}`: false,
		` {"unknown":[{"x":"y"}]} `:    true,
		`{"a":[1,-2.5e-999,1E+0999]}`:  true,
		`{"a":"0e1000","b":true}`:      true,
		`{"a":false,"b":-0.0E1}`:       true,
		`{"a":0e1000}`:                 false,
		`[1,2,1E+1000]`:                false,
		`-1e-1000`:                     false,
		`[{"b":0.5e99999999999}]`:      false,
	} {
		if problem := CheckJSON([]byte(in)); (problem == "") != ok {
			t.Errorf("CheckJSON(%q) = %q", in, problem)
		}
	}
}

// TestHoldsNull checks that HoldsNull finds null outside the strings of a
// JSON text, at any depth, and nowhere else, as in a string or a number that
// CheckJSON refuses for its exponent.
func TestHoldsNull(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		`null`:                      true,
		`{"a":[1,{"b":null}]}`:      true,
		`{"n":"\"null\\","m":null}`: true,
		`{"a":"null","null":"n"}`:   false,
		`[true,false,-1.5E+3]`:      false,
		`{"a":0e1000}`:              false,
	} {
		if got := HoldsNull([]byte(in)); got != want {
			t.Errorf("HoldsNull(%s) = %v", in, got)
		}
	}
}
