package node

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// TestHundredths checks that a coordinate is read exactly as the decimal
// that it is written as, rounded half away from zero to 2 decimal places,
// however it is written, that one outside its bounds, or not a number, or
// written beyond the bounds of a number's form, is refused, and that it is
// written back without trailing zeros, so that one position is written one
// way (PRD FR-EDIT-02 AC4).
//
// Verifies: FR-EDIT-02/AC4
func TestHundredths(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		raw  string
		want int64
		out  string
	}{
		{"240", 24000, "240"},
		{"240.0", 24000, "240"},
		{"2.4e2", 24000, "240"},
		{"2400E-1", 24000, "240"},
		{"240.004", 24000, "240"},
		{"240.005", 24001, "240.01"},
		{"0.125", 13, "0.13"},
		{"-0.125", -13, "-0.13"},
		{"1.005", 101, "1.01"},
		{"120.5", 12050, "120.5"},
		{"120.50", 12050, "120.5"},
		{"-0.004", 0, "0"},
		{"-0", 0, "0"},
		{"0.0049999999999999999999999", 0, "0"},
		{"1e-999", 0, "0"},
		{"0.30000000000000004", 30, "0.3"},
		{"1000000000", 100000000000, "1000000000"},
		{"-1000000000.00", -100000000000, "-1000000000"},
		{"999999999.995", 100000000000, "1000000000"},
	} {
		got, problem := Coord{raw: c.raw}.hundredths()
		if problem != "" || got != c.want {
			t.Errorf("%s reads as %d (%q), want %d", c.raw, got, problem, c.want)
			continue
		}
		if out, err := json.Marshal(coordOf(got)); err != nil || string(out) != c.out {
			t.Errorf("%s is written as %s (%v), want %s", c.raw, out, err, c.out)
		}
	}
	// The bounds hold the number as it is written, before it is rounded, even
	// one that the schema's minimum and maximum, which take the double
	// nearest to it, take, as 1000000000.0000000001.
	for _, raw := range []string{"1000000000.01", "-1000000000.005", "-1000000000.004", "1000000000.0000000001",
		"1e10", "1e999", `"1"`, "null", "true", "01", "1.", ".5", "+1", "0x10", "1e1000", "1e-1000",
		"1e99999999999999999999", "1e-99999999999999999999", "1" + strings.Repeat("0", 327)} {
		if got, problem := (Coord{raw: raw}).hundredths(); problem == "" {
			t.Errorf("%s reads as %d, want it refused", raw, got)
		}
	}
	for _, c := range []struct {
		raw string
		ok  bool
	}{{"1", true}, {"1.004", true}, {"0.995", false}, {"0.999999999999999999", false}, {"100000", true},
		{"99999.995", true}, {"100000.004", false}, {"0", false}, {"-1", false}} {
		if _, problem := (Length{raw: c.raw}).hundredths(); (problem == "") != c.ok {
			t.Errorf("the length %s: %q, want it taken %v", c.raw, problem, c.ok)
		}
	}
}

// TestNumberSchemas checks that each field of a position and of a size is
// described by its own doc in the OpenAPI document, which huma writes into
// the schema that the field's type gives it.
func TestNumberSchemas(t *testing.T) {
	t.Parallel()
	r := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	r.Schema(reflect.TypeFor[NodePosition](), true, "")
	r.Schema(reflect.TypeFor[NodeSize](), true, "")
	for _, c := range []struct{ schema, a, b string }{{"NodePosition", "x", "y"}, {"NodeSize", "width", "height"}} {
		s := r.Map()[c.schema]
		if s == nil {
			t.Fatalf("%s is not registered", c.schema)
		}
		if a, b := s.Properties[c.a].Description, s.Properties[c.b].Description; a == "" || a == b {
			t.Errorf("%s's %s is described as %q, and its %s as %q", c.schema, c.a, a, c.b, b)
		}
	}
}

// TestNumeric checks that hundredths are written to the database as a
// numeric with 2 decimal places, and read back from one whatever its scale.
func TestNumeric(t *testing.T) {
	t.Parallel()
	for _, n := range []int64{0, 1, -1, 12050, -100000000000, 100000000000} {
		got, err := fromNumeric(numeric(n))
		if err != nil || got != n {
			t.Errorf("%d reads back as %d, %v", n, got, err)
		}
	}
	scaled := numeric(12050)
	scaled.Int.SetInt64(1205)
	scaled.Exp = -1
	if got, err := fromNumeric(scaled); err != nil || got != 12050 {
		t.Errorf("120.5 with a scale of 1 reads as %d, %v", got, err)
	}
	scaled.Int.SetInt64(121)
	scaled.Exp = 2
	if got, err := fromNumeric(scaled); err != nil || got != 1210000 {
		t.Errorf("12100 with an exponent of 2 reads as %d, %v", got, err)
	}
	scaled.Int.SetInt64(12051)
	scaled.Exp = -3
	if _, err := fromNumeric(scaled); err == nil {
		t.Error("a number with 3 decimal places reads")
	}
}

// TestTags checks that a node's tags are refused when two are the same tag but
// for their case or their normalization, as ΐ and Ϊ́, a capital and a mark,
// which folding leaves in different forms, are, and that the suggestions
// merge the spellings of a tag, as the most of its nodes write it, the most
// used first.
func TestTags(t *testing.T) {
	t.Parallel()
	if errs := checkTags("tags", []string{"Quest", "Harbour", "Lighthouse"}); len(errs) != 0 {
		t.Errorf("distinct tags: %v", errs)
	}
	for _, tags := range [][]string{{"Quest", "quest"}, {"Straße", "STRASSE"}, {"Café", "café"},
		{"\u0390", "\u03aa\u0301"}, {" Quest"}, {""}, {"a‮b"}, {strings.Repeat("a", 51)}} {
		if errs := checkTags("tags", tags); len(errs) == 0 {
			t.Errorf("%q are taken as a node's tags", tags)
		}
	}
	many := make([]string, MaxTags+1)
	for i := range many {
		many[i] = strings.Repeat("t", i+1)[:min(i+1, MaxTagLength)]
	}
	if errs := checkTags("tags", many); len(errs) == 0 {
		t.Errorf("%d tags are taken", len(many))
	}
	got := suggest([]spelling{{"QUEST", 1}, {"Quest", 3}, {"quest", 3}, {"Harbour", 5}, {"Hidden", 1}, {"Lore", 9}},
		"q", 10)
	if want := []Tag{{"Quest", 7}}; !slices.Equal(got, want) {
		t.Errorf("the suggestions for q are %v, want %v", got, want)
	}
	got = suggest([]spelling{{"QUEST", 1}, {"Quest", 3}, {"Harbour", 5}, {"Hidden", 1}, {"Lore", 9}}, "", 3)
	if want := []Tag{{"Lore", 9}, {"Harbour", 5}, {"Quest", 4}}; !slices.Equal(got, want) {
		t.Errorf("the first 3 suggestions are %v, want %v", got, want)
	}
	if got = suggest([]spelling{{"Lore", 9}}, "", -1); len(got) != 0 {
		t.Errorf("the suggestions for a limit of -1 are %v, want none", got)
	}
	if problem := CheckTagQuery("a\u0007"); problem == "" {
		t.Error("a prefix with a control character is taken")
	}
}
