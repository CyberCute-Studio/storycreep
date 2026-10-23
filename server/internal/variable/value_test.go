package variable

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
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
		{TypeBool, "true", "true"},
		{TypeBool, "false", "false"},
		{TypeInt, "3", "3"},
		{TypeInt, "-3", "-3"},
		{TypeInt, "-0", "0"},
		{TypeInt, "0.0", "0"},
		{TypeInt, "3.0", "3"},
		{TypeInt, "3e0", "3"},
		{TypeInt, "30E-1", "3"},
		{TypeInt, "1.5e1", "15"},
		{TypeInt, "0.1e1", "1"},
		{TypeInt, "0.000e999", "0"},
		{TypeInt, "0e+000999", "0"},
		{TypeInt, "9007199254740991", "9007199254740991"},
		{TypeInt, "-9007199254740991", "-9007199254740991"},
		{TypeInt, "9.007199254740991e15", "9007199254740991"},
		{TypeInt, "100000000000000E-14", "1"},
		{TypeFloat, "3", "3"},
		{TypeFloat, "2.0", "2"},
		{TypeFloat, "0.1", "0.1"},
		{TypeFloat, "-0", "0"},
		{TypeFloat, "-0.0", "0"},
		{TypeFloat, "1e-400", "0"},
		{TypeFloat, "-1e-400", "0"},
		{TypeFloat, "1e-999", "0"},
		{TypeFloat, "-0e999", "0"},
		{TypeFloat, "4.9e-324", "5e-324"},
		{TypeInt, "-0e-999", "0"},
		// The longest that a number may be written with, 64 characters.
		{TypeInt, "3." + strings.Repeat("0", 62), "3"},
		{TypeFloat, "-0." + strings.Repeat("0", 56) + "1e57", "-1"},
		{TypeFloat, "1e21", "1e+21"},
		{TypeFloat, "1.7976931348623157e308", "1.7976931348623157e+308"},
		{TypeFloat, "9007199254740993", "9007199254740992"},
		{TypeString, `""`, `""`},
		{TypeString, `"Elara"`, `"Elara"`},
		{TypeString, `"two\nlines\tand a tab"`, `"two\nlines\tand a tab"`},
		{TypeString, `"\u00e9\ud83d\ude00"`, `"é😀"`},
		{TypeString, `" padded "`, `" padded "`},
		{TypeString, `"` + long + `"`, `"` + long + `"`},
	} {
		got, problem := Value{raw: json.RawMessage(c.in)}.Of(c.typ)
		if problem != "" || string(got.raw) != c.want {
			t.Errorf("%s %s is %s, %q; want %s", c.typ, c.in, got.raw, problem, c.want)
		}
	}
	for _, c := range []struct{ typ, in string }{
		{TypeBool, "0"},
		{TypeBool, `"true"`},
		{TypeInt, `"3"`},
		{TypeInt, "true"},
		{TypeInt, "3.5"},
		{TypeInt, "0.11e1"},
		{TypeInt, "4503599627370495.5"},
		{TypeInt, "9007199254740992"},
		{TypeInt, "-9007199254740992"},
		{TypeInt, "1e16"},
		{TypeInt, "1e999999999999"},
		{TypeInt, "1e-999999999999"},
		{TypeInt, "12345678901234567890123"},
		{TypeFloat, "1e400"},
		{TypeFloat, "-1e400"},
		{TypeFloat, "1e999"},
		// Written past the bounds, more than 64 characters or an exponent
		// beyond ±999, though each reads as a value of its type.
		{TypeInt, "3." + strings.Repeat("0", 63)},
		{TypeFloat, "3." + strings.Repeat("0", 63)},
		{TypeInt, "0e1000"},
		{TypeFloat, "0e-1000"},
		{TypeFloat, "1E-1000"},
		{TypeInt, "0.000e99999999999"},
		{TypeFloat, "-0.0E+99999999999999999999"},
		{TypeInt, "0e1073741824"},
		{TypeFloat, "1e-16384"},
		{TypeFloat, "-0." + strings.Repeat("0", 99999) + "1e100000"},
		{TypeFloat, "1" + strings.Repeat("0", 1000) + "e-1000"},
		{TypeFloat, `"1.5"`},
		{TypeString, "3"},
		{TypeString, `"` + long + `e"`},
		{TypeString, `"bell\u0007"`},
		{TypeString, `"carriage\rreturn"`},
		{TypeString, `"right\u202eleft"`},
		{TypeString, `"line\u2028separator"`},
		{TypeString, `"lone \ud800"`},
		{TypeString, `"lone \udc00 low"`},
		{TypeString, `"swapped \udc00\ud800"`},
		{TypeString, `"high then text \ud800x"`},
		{TypeBool, "null"},
		{TypeString, "null"},
		{TypeInt, "[3]"},
		{"bigint", "3"},
	} {
		if got, problem := (Value{raw: json.RawMessage(c.in)}).Of(c.typ); problem == "" {
			t.Errorf("%s %s was taken as %s", c.typ, c.in, got.raw)
		}
	}
	// None is the type's zero value.
	for typ, want := range map[string]string{TypeBool: "false", TypeInt: "0", TypeFloat: "0", TypeString: `""`} {
		if got, problem := (Value{}).Of(typ); problem != "" || string(got.raw) != want {
			t.Errorf("none of %s is %s, %q; want %s", typ, got.raw, problem, want)
		}
	}
	// An escaped backslash before u starts no escape.
	if got, problem := (Value{raw: json.RawMessage(`"\\ud800"`)}).Of(TypeString); problem != "" ||
		string(got.raw) != `"\\ud800"` {
		t.Errorf(`"\\ud800" is %s, %q`, got.raw, problem)
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
		gotInt, intProblem := Value{raw: json.RawMessage(raw)}.Of(TypeInt)
		gotFloat, floatProblem := Value{raw: json.RawMessage(raw)}.Of(TypeFloat)
		if formProblem(raw) != "" {
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

// TestNumberForms checks that a number written within the bounds, at most 64
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
	// The mantissas leave 5 characters of the 64 for the longest exponent,
	// e-999, as the longest number has them.
	mantissas := []string{"0", "-0", "0.0", "1", "-1.5", "3.000", "9007199254740991", "9007199254740993",
		"1.7976931348623157", "2.2250738585072011", "4.9406564584124654", "-0." + strings.Repeat("0", 55) + "1",
		"1" + strings.Repeat("0", 58), "9." + strings.Repeat("9", 56), "4503599627370495.5"}
	exponents := []string{"", "e0", "E+5", "e-1", "e-15", "e15", "e16", "e308", "e309", "e-308", "e-324", "e-325",
		"e+999", "e-999", "e0999", "e1000", "e-1000", "e99999999999"}
	for _, m := range mantissas {
		for _, e := range exponents {
			raw := m + e
			gotInt, intProblem := Value{raw: json.RawMessage(raw)}.Of(TypeInt)
			gotFloat, floatProblem := Value{raw: json.RawMessage(raw)}.Of(TypeFloat)
			if formProblem(raw) != "" {
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
	// The bounds' edges: 64 characters and 999 either way are within them,
	// 65 characters and 1000 past them.
	for raw, within := range map[string]bool{
		"1." + strings.Repeat("0", 62): true, "1." + strings.Repeat("0", 63): false, "1e999": true,
		"1e-999": true, "0e+000999": true, "1e1000": false, "1e-1000": false, "1E+01000": false,
	} {
		if got := formProblem(raw) == ""; got != within {
			t.Errorf("%s is within the bounds: %t, want %t", raw, got, within)
		}
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

// TestSchemaEnums checks that the types and the scopes of a variable that the
// package lists are those that the schemas of the commands' payloads and of
// the API's answers take, which their enum tags give, so that a type or a
// scope added to the list and not to a schema, or to a schema and not to the
// list, fails. TestEnumerations checks the database's.
func TestSchemaEnums(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ   reflect.Type
		field string
		want  []string
	}{
		{reflect.TypeFor[createPayload](), "Type", Types()},
		{reflect.TypeFor[retypePayload](), "Type", Types()},
		{reflect.TypeFor[Variable](), "Type", Types()},
		{reflect.TypeFor[createPayload](), "Scope", Scopes()},
		{reflect.TypeFor[scopePayload](), "Scope", Scopes()},
		{reflect.TypeFor[Variable](), "Scope", Scopes()},
	} {
		f, ok := c.typ.FieldByName(c.field)
		if got, want := f.Tag.Get("enum"), strings.Join(c.want, ","); !ok || got != want {
			t.Errorf("%s.%s takes %q, want %q", c.typ.Name(), c.field, got, want)
		}
	}
}
