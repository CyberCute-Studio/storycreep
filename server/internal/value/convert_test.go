package value

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
)

// TestTextAndEnum checks how a value of a text and of an enum is read: a text
// is a string of at most MaxTextLength characters, longer than a string may
// be, and an enum's value one of its options, as it is written there.
//
// Verifies: FR-COMP-01/AC2
func TestTextAndEnum(t *testing.T) {
	t.Parallel()
	long := `"` + strings.Repeat("é", MaxTextLength) + `"`
	if _, problem := FromJSON([]byte(long)).Of(Text); problem != "" {
		t.Errorf("a text of %d characters: %s", MaxTextLength, problem)
	}
	if _, problem := FromJSON([]byte(long)).Of(String); problem == "" {
		t.Errorf("a string of %d characters was taken", MaxTextLength)
	}
	if _, problem := FromJSON([]byte(`"` + strings.Repeat("a", MaxTextLength+1) + `"`)).Of(Text); problem == "" {
		t.Errorf("a text of %d characters was taken", MaxTextLength+1)
	}
	if _, problem := FromJSON([]byte(`"line\nnext\tcell"`)).Of(Text); problem != "" {
		t.Errorf("a text of lines and tabs: %s", problem)
	}
	options := []string{"calm", "angry"}
	for in, want := range map[string]bool{`"calm"`: true, `"angry"`: true, `"Calm"`: false, `"sad"`: false,
		`3`: false, `true`: false} {
		if _, problem := FromJSON([]byte(in)).Of(Enum, options...); (problem == "") != want {
			t.Errorf("the enum value %s: %q, want it taken: %t", in, problem, want)
		}
	}
	for typ, want := range map[string]string{Bool: "false", Int: "0", Float: "0", String: `""`, Text: `""`,
		Enum: `"calm"`} {
		if got, problem := (Value{}).Of(typ, options...); problem != "" || string(got.JSON()) != want {
			t.Errorf("the zero %s is %s, %q, want %s", typ, got.JSON(), problem, want)
		}
	}
	if _, problem := (Value{}).Of(Enum); problem == "" {
		t.Errorf("an enum without options has a zero value")
	}
}

// TestCheckOptions checks the options of an enum: 1 to MaxOptions, each of 1
// to MaxOptionLength characters, no two alike, and names without control
// characters or white space around them.
func TestCheckOptions(t *testing.T) {
	t.Parallel()
	many := make([]string, MaxOptions+1)
	for i := range many {
		many[i] = "option " + strconv.Itoa(i)
	}
	for _, c := range []struct {
		options []string
		ok      bool
	}{
		{[]string{"calm"}, true},
		{[]string{"calm", "angry"}, true},
		{[]string{strings.Repeat("é", MaxOptionLength)}, true},
		{nil, false},
		{[]string{""}, false},
		{[]string{strings.Repeat("a", MaxOptionLength+1)}, false},
		{[]string{"calm", "calm"}, false},
		{many[:MaxOptions], true},
		{many, false},
	} {
		if got := CheckOptions(c.options); (got == "") != c.ok {
			t.Errorf("the options %q: %q, want them taken: %t", c.options, got, c.ok)
		}
	}
	for _, c := range []struct {
		options []string
		ok      bool
	}{
		{[]string{"calm", "very angry"}, true},
		{[]string{" calm"}, false},
		{[]string{"ca\nlm"}, false},
		{[]string{"   "}, false},
	} {
		if got := CheckOptionNames(c.options); (got == "") != c.ok {
			t.Errorf("the option names %q: %q, want them taken: %t", c.options, got, c.ok)
		}
	}
}

// TestConvert checks how a value of each type converts to every other, as
// the expression language's to_int, to_float, and to_string convert it, and
// what does not convert (FR-COMP-01 AC2).
//
// Verifies: FR-COMP-01/AC2
func TestConvert(t *testing.T) {
	t.Parallel()
	options := []string{"calm", "12", "true", "2.0"}
	for _, c := range []struct {
		from, in, to, want string
	}{
		{Bool, "true", Int, "1"},
		{Bool, "false", Float, "0"},
		{Bool, "true", String, `"true"`},
		{Bool, "true", Enum, `"true"`},
		{Bool, "false", Enum, ""},
		{Int, "0", Bool, "false"},
		{Int, "1", Bool, "true"},
		{Int, "2", Bool, ""},
		{Int, "-7", Float, "-7"},
		{Int, "12", Text, `"12"`},
		{Int, "12", Enum, `"12"`},
		{Int, "13", Enum, ""},
		{Float, "1", Bool, "true"},
		{Float, "0.5", Bool, ""},
		{Float, "3.9", Int, "3"},
		{Float, "-3.9", Int, "-3"},
		{Float, "1e300", Int, ""},
		{Float, "9007199254740991", Int, "9007199254740991"},
		{Float, "2", String, `"2.0"`},
		{Float, "2", Enum, `"2.0"`},
		{Float, "0.1", String, `"0.1"`},
		{Float, "1e21", String, `"1e+21"`},
		{Float, "1.5e-7", String, `"1.5e-7"`},
		{Float, "0.000001", String, `"0.000001"`},
		{String, `"true"`, Bool, "true"},
		{String, `"True"`, Bool, ""},
		{String, `"42"`, Int, "42"},
		{String, `"+42"`, Int, "42"},
		{String, `"-0"`, Int, "0"},
		{String, `" 42"`, Int, ""},
		{String, `"4.2"`, Int, ""},
		{String, `"9007199254740992"`, Int, ""},
		{String, `"4.25"`, Float, "4.25"},
		{String, `"1e400"`, Float, ""},
		{String, `".5"`, Float, ""},
		{String, `"5."`, Float, ""},
		{String, `"inf"`, Float, ""},
		{String, `"0x1p-2"`, Float, ""},
		{String, `"calm"`, Enum, `"calm"`},
		{String, `"sad"`, Enum, ""},
		{String, `"calm"`, Text, `"calm"`},
		{Text, `"` + strings.Repeat("a", MaxStringLength) + `"`, String, `"` + strings.Repeat("a", MaxStringLength) + `"`},
		{Text, `"` + strings.Repeat("a", MaxStringLength+1) + `"`, String, ""},
		{Text, `"12"`, Int, "12"},
		{Enum, `"12"`, Int, "12"},
		{Enum, `"calm"`, Int, ""},
		{Enum, `"true"`, Bool, "true"},
		{Enum, `"calm"`, String, `"calm"`},
		{Int, "3", Int, "3"},
	} {
		got, ok := Convert(FromJSON([]byte(c.in)), c.from, options, c.to, options)
		if c.want == "" && ok || c.want != "" && (!ok || string(got.JSON()) != c.want) {
			t.Errorf("%s %s to %s: %s, %t; want %q", c.from, c.in, c.to, got.JSON(), ok, c.want)
		}
	}
	// A value that is not of its type does not convert.
	if _, ok := Convert(FromJSON([]byte(`"3"`)), Int, nil, Float, nil); ok {
		t.Errorf("a string read as an int converted")
	}
}

// TestFormatFloat checks that a float is written as to_string writes it
// (§5.8.9): the shortest decimal that reads as it again, positional from
// 1e-6 to below 1e21, with one digit after the point for an integral value,
// and with an exponent otherwise, and that it reads as the same double.
func TestFormatFloat(t *testing.T) {
	t.Parallel()
	for f, want := range map[float64]string{0: "0.0", math.Copysign(0, -1): "0.0", 2: "2.0", -2.5: "-2.5",
		0.1: "0.1", 1e20: "100000000000000000000.0", 1e21: "1e+21", 1e-6: "0.000001", 1e-7: "1e-7",
		123456789.125: "123456789.125", math.MaxFloat64: "1.7976931348623157e+308", 5e-324: "5e-324"} {
		got := FormatFloat(f)
		var back float64
		if got != want || json.Unmarshal([]byte(got), &back) != nil || back != f {
			t.Errorf("%v is written %s, want %s", f, got, want)
		}
	}
}
