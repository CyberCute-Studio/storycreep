package richtext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
)

// segmentsDocument is the root of a test's document of the segments' schema.
type segmentsDocument struct{}

func (segmentsDocument) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return g.Ref(reflect.TypeFor[Segments]())
}

// segmentsSchema compiles the schema that Segments gives the published
// format.
func segmentsSchema(t testing.TB) *jsonschema.Schema {
	t.Helper()
	g := schemagen.New(false)
	d := schemagen.Document{Path: "schema/test/v1/segments.schema.json", Title: "Segments",
		Root: reflect.TypeFor[segmentsDocument]()}
	data, err := g.Document(d)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(d.ID(), doc); err != nil {
		t.Fatal(err)
	}
	return c.MustCompile(d.ID())
}

// TestSegmentsJSON checks that the segments of every valid vector validate
// against the segments' schema of the published format, and that
// Segment.UnmarshalJSON reads them as MarshalJSON writes them.
func TestSegmentsJSON(t *testing.T) {
	s := segmentsSchema(t)
	for _, v := range readVectors(t).Valid {
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(v.Segments))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(inst); err != nil {
			t.Errorf("%s: the segments do not validate: %v", v.Note, err)
		}
		var segs Segments
		if err := json.Unmarshal(v.Segments, &segs); err != nil {
			t.Errorf("%s: the segments do not decode: %v", v.Note, err)
			continue
		}
		out, err := marshalNoEscape(segs)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(compactJSON(t, out), compactJSON(t, v.Segments)) {
			t.Errorf("%s: the segments are written again as %s", v.Note, out)
		}
	}
}

func compactJSON(t testing.TB, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, data); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestSegmentUnmarshalAgreesWithSchema checks that Segment.UnmarshalJSON
// reads exactly the segments that the schema takes, of mutations of every
// kind of segment of the vectors: each field replaced by a value of another
// kind or of another value, and each field that another kind has added.
func TestSegmentUnmarshalAgreesWithSchema(t *testing.T) {
	s := segmentsSchema(t)
	seen := map[string]bool{}
	var segments []map[string]any
	for _, v := range readVectors(t).Valid {
		var segs []map[string]any
		if err := json.Unmarshal(v.Segments, &segs); err != nil {
			t.Fatal(err)
		}
		for _, seg := range segs {
			key := fmt.Sprintf("%v %v %v %v %v %v", seg["kind"], seg["list"], seg["attribute"] != nil,
				seg["href"] != nil, seg["decimals"] != nil, seg["depth"] != nil)
			if !seen[key] {
				seen[key] = true
				segments = append(segments, seg)
			}
		}
	}
	// An integer written as 1.0 or 2e0 is one, as a consumer reads it by its
	// declaration (PRD §5.8.9).
	values := []any{"x", "", 0, -1, 7, 2.5, json.Number("1.0"), json.Number("2e0"), json.Number("-0.0"),
		json.Number("9007199254740991"), json.Number("9007199254740992"), json.Number("1e400"), "1", true,
		[]any{}, []any{"bold", "bold"}, []any{"underline"},
		map[string]any{}, "https://example.com", "javascript:alert(1)", "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF",
		"var_01HQ8Z5C2K8W1D5J9NTQZR4YBE", "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF", "cmp_01hq8z7kd3m9p2w5xrtvnq4bcf",
		"bullet", "ordered", "value", "item", "text"}
	fields := []string{"attribute", "decimals", "depth", "href", "kind", "list", "marks", "name", "number", "ref",
		"value", "future"}
	checked := 0
	for _, seg := range segments {
		for _, f := range fields {
			old, had := seg[f]
			for _, v := range values {
				seg[f] = v
				data, err := json.Marshal([]any{seg})
				if err != nil {
					t.Fatal(err)
				}
				inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				var segs Segments
				decodeErr := json.Unmarshal(data, &segs)
				schemaErr := s.Validate(inst)
				if (schemaErr == nil) != (decodeErr == nil) {
					t.Errorf("%s: the schema says %v, and UnmarshalJSON %v", data, schemaErr, decodeErr)
				}
				checked++
			}
			if had {
				seg[f] = old
			} else {
				delete(seg, f)
			}
		}
	}
	if checked < 1000 || len(segments) < 6 {
		t.Errorf("only %d mutations of %d segments were checked", checked, len(segments))
	}
	if !seen["item ordered false false false true"] {
		t.Errorf("no vector has an ordered item, whose segment the test would mutate: %v", seen)
	}
}
