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
		{TypeInt, "0.000e16383", "0"},
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
		{TypeFloat, "1e-16383", "0"},
		{TypeFloat, "0.5e-16382", "0"},
		{TypeInt, "0e-16383", "0"},
		{TypeInt, "-0e-16383", "0"},
		{TypeInt, "0e16384", "0"},
		{TypeInt, "0.0e+0001073741823", "0"},
		{TypeFloat, "-0e1073741823", "0"},
		{TypeInt, "0." + strings.Repeat("0", 16382) + "1e16383", "1"},
		// Long exponents and mantissas, which strconv.ParseFloat would read
		// as 0 and 1e-201.
		{TypeFloat, "-0." + strings.Repeat("0", 99999) + "1e100000", "-1"},
		{TypeFloat, "1" + strings.Repeat("0", 1000) + "e-1000", "1"},
		{TypeFloat, "1" + strings.Repeat("0", 1000) + ".5e-1000", "1"},
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
		// More digits after the point than the operation log's jsonb holds,
		// its exponent counted, though each reads as a value of its type.
		{TypeFloat, "1e-16384"},
		{TypeFloat, "-1E-16384"},
		{TypeFloat, "0.5e-16383"},
		{TypeFloat, "1." + strings.Repeat("0", 16384)},
		{TypeFloat, "1e-99999999999"},
		{TypeInt, "0e-16384"},
		{TypeInt, "-0e-99999999999"},
		{TypeInt, "3." + strings.Repeat("0", 16384)},
		{TypeInt, "3" + strings.Repeat("0", 16384) + "e-16384"},
		// An exponent beyond what the operation log's jsonb reads, either way,
		// though each reads as 0.
		{TypeInt, "0e1073741824"},
		{TypeFloat, "0e1073741824"},
		{TypeInt, "0.000e99999999999"},
		{TypeFloat, "-0.0E+99999999999"},
		{TypeInt, "0e-1073741824"},
		// Beyond a double's range, which strconv.ParseFloat would read as 0
		// and 1e300.
		{TypeFloat, "0." + strings.Repeat("0", 16382) + "1e1073741823"},
		{TypeFloat, "1" + strings.Repeat("0", 1000) + "e-500"},
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

// FuzzIntegerValue checks that an int's default is read exactly: a JSON
// number is taken as an int exactly when math/big reads it as an integer
// from -MaxInt to MaxInt, and as that integer, however it is written.
func FuzzIntegerValue(f *testing.F) {
	for _, seed := range []string{"3", "-0", "3.0", "30E-1", "4503599627370495.5", "9007199254740991",
		"9007199254740992", "1e16", "0.000e5", "100000000000000E-14", "-9.007199254740991e15", "1e-7", "12e+1"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		var probe any
		if !numberForm.MatchString(raw) || json.Unmarshal([]byte(raw), &probe) != nil {
			return
		}
		// An exponent of more than three digits, which big.Rat computes
		// slowly, is left out: TestNumberForms checks such exponents, to
		// maxExponent and beyond it, which formProblem refuses.
		if i := strings.IndexAny(raw, "eE"); i >= 0 && len(strings.TrimLeft(raw[i+1:], "+-0")) > 3 {
			return
		}
		// A number written with more than maxScale digits after its point,
		// its exponent counted, is refused whatever it reads as, which
		// TestValueOf checks; with an exponent of three digits at most, a
		// shorter one is written with fewer.
		if len(raw) > maxScale-1000 {
			return
		}
		want, ok := new(big.Rat).SetString(raw)
		if !ok {
			t.Fatalf("big.Rat does not read %s", raw)
		}
		integral := want.IsInt() && want.Num().CmpAbs(big.NewInt(MaxInt)) <= 0
		got, problem := Value{raw: json.RawMessage(raw)}.Of(TypeInt)
		switch {
		case integral && (problem != "" || string(got.raw) != want.Num().String()):
			t.Errorf("%s is %s, %q; want %s", raw, got.raw, problem, want.Num())
		case !integral && problem == "":
			t.Errorf("%s, which is no int, is taken as %s", raw, got.raw)
		}
	})
}

// TestNumberForms checks that a number written within the limits that the
// operation log holds, an exponent within ±maxExponent and at most maxScale
// digits after its point, its exponent counted, is read exactly however long
// its exponent and its mantissa are, as strconv.ParseFloat alone does not
// read them: as an int exactly when math/big reads it as an integer from
// -MaxInt to MaxInt, and as a float exactly when math/big rounds it to a
// finite double, as that double; and that a number written past either limit
// is neither, whatever it reads as. TestLogHoldsNumbers checks that the
// operation log's jsonb holds each that is taken.
//
// Verifies: FR-VAR-01/AC2
func TestNumberForms(t *testing.T) {
	t.Parallel()
	zeros := strings.Repeat("0", maxScale-1)
	mantissas := []string{"0", "-0", "0.0", "1", "-1.5", "3.000", "9007199254740991", "1.7976931348623157",
		"0." + zeros + "1", "1." + zeros + "0", "1" + zeros, "-0." + strings.Repeat("0", 99999) + "1",
		"1" + strings.Repeat("0", 1000)}
	exponents := []string{"", "e0", "E+5", "e-1", "e308", "e309", "e-324", "e-1000", "e-16382", "e-16383", "e-16384",
		"e16382", "e16383", "e16384", "e100000", "e1073741823", "e+0001073741823", "e1073741824", "E-1073741823",
		"e99999999999"}
	// shown is a number as a failure shows it, its middle left out.
	shown := func(raw string) string {
		if len(raw) <= 40 {
			return raw
		}
		return fmt.Sprintf("%s...%s (%d characters)", raw[:16], raw[len(raw)-24:], len(raw))
	}
	for _, m := range mantissas {
		for _, e := range exponents {
			raw := m + e
			gotInt, intProblem := Value{raw: json.RawMessage(raw)}.Of(TypeInt)
			gotFloat, floatProblem := Value{raw: json.RawMessage(raw)}.Of(TypeFloat)
			if formProblem(raw) != "" {
				if intProblem == "" || floatProblem == "" {
					t.Errorf("%s, past the limits, is taken as the int %s or the float %s", shown(raw), gotInt.raw,
						gotFloat.raw)
				}
				continue
			}
			want, ok := new(big.Rat).SetString(raw)
			if !ok {
				// math/big reads no number but 0 whose exponent, net of the
				// digits after its point, passes a million, which is beyond
				// both ranges.
				if intProblem == "" || floatProblem == "" {
					t.Errorf("%s, beyond both ranges, is taken as the int %s or the float %s", shown(raw),
						gotInt.raw, gotFloat.raw)
				}
				continue
			}
			switch integral := want.IsInt() && want.Num().CmpAbs(big.NewInt(MaxInt)) <= 0; {
			case integral && (intProblem != "" || string(gotInt.raw) != want.Num().String()):
				t.Errorf("%s is the int %s, %q; want %s", shown(raw), gotInt.raw, intProblem, want.Num())
			case !integral && intProblem == "":
				t.Errorf("%s, which is no int, is taken as %s", shown(raw), gotInt.raw)
			}
			f, _ := want.Float64()
			if f == 0 {
				// -0 is written as 0.
				f = 0
			}
			switch wantFloat, _ := json.Marshal(f); {
			case math.IsInf(f, 0) && floatProblem == "":
				t.Errorf("%s, beyond a double's range, is taken as %s", shown(raw), gotFloat.raw)
			case !math.IsInf(f, 0) && (floatProblem != "" || string(gotFloat.raw) != string(wantFloat)):
				t.Errorf("%s is the float %s, %q; want %s", shown(raw), gotFloat.raw, floatProblem, wantFloat)
			}
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
