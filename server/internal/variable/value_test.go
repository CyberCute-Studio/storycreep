package variable

import (
	"encoding/json"
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
		{TypeInt, "0.000e99999999999", "0"},
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
		{TypeInt, "0.0e99999999999", "0"},
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
		// An exponent too large for big.Rat to compute quickly is read by the
		// code's own shortcut, which the seeds cover.
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
