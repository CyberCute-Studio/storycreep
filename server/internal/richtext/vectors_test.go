package richtext

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// vectors is schema/rich-text/vectors.json, which the TypeScript client runs
// too. See schema/rich-text/README.md.
type vectors struct {
	Names struct {
		Components map[string]string `json:"components"`
		Values     []struct {
			Ref       string `json:"ref"`
			Attribute string `json:"attribute"`
			Name      string `json:"name"`
		} `json:"values"`
	} `json:"names"`
	Valid []struct {
		Note      string          `json:"note"`
		Doc       json.RawMessage `json:"doc"`
		Canonical json.RawMessage `json:"canonical"`
		Segments  json.RawMessage `json:"segments"`
		Text      string          `json:"text"`
		Preview   string          `json:"preview"`
		Words     int             `json:"words"`
		Hash      string          `json:"hash"`
	} `json:"valid"`
	Invalid []struct {
		Note          string          `json:"note"`
		Doc           json.RawMessage `json:"doc"`
		Reason        Reason          `json:"reason"`
		Pointer       string          `json:"pointer"`
		SchemaAccepts bool            `json:"schema_accepts"`
	} `json:"invalid"`
	Words []struct {
		Note  string `json:"note"`
		Text  string `json:"text"`
		Words int    `json:"words"`
	} `json:"words"`
	Previews []struct {
		Note    string `json:"note"`
		Text    string `json:"text"`
		Preview string `json:"preview"`
	} `json:"previews"`
}

func readVectors(t testing.TB) vectors {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "rich-text", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// A misspelt key would otherwise leave a field empty and weaken a vector
	// without failing it.
	dec.DisallowUnknownFields()
	var v vectors
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if len(v.Names.Components) == 0 || len(v.Names.Values) == 0 || len(v.Valid) == 0 || len(v.Invalid) == 0 ||
		len(v.Words) == 0 || len(v.Previews) == 0 {
		t.Fatal("vectors.json has an empty section")
	}
	return v
}

// mapNames are the names of the vectors.
type mapNames struct {
	components map[string]string
	values     map[[2]string]string
}

func (v vectors) names() mapNames {
	n := mapNames{components: v.Names.Components, values: map[[2]string]string{}}
	for _, val := range v.Names.Values {
		n.values[[2]string{val.Ref, val.Attribute}] = val.Name
	}
	return n
}

func (n mapNames) Value(ref, attribute string) string { return n.values[[2]string{ref, attribute}] }
func (n mapNames) Component(id string) string         { return n.components[id] }

// sameJSON reports whether two JSON texts hold the same value.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

// TestValidVectors checks that every valid document of the vectors is
// accepted, written in its canonical form, and derives the segments, plain
// text, preview, word count, and hash that the vectors give.
func TestValidVectors(t *testing.T) {
	v := readVectors(t)
	names := v.names()
	for _, vec := range v.Valid {
		t.Run(vec.Note, func(t *testing.T) {
			doc, err := Parse(vec.Doc)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			want := vec.Canonical
			if want == nil {
				want = vec.Doc
			}
			if !sameJSON(t, got, want) {
				t.Errorf("canonical form %s, want %s", got, want)
			}
			// The canonical form is a fixed point.
			again, err := Parse(got)
			if err != nil {
				t.Fatalf("Parse of the canonical form: %v", err)
			}
			if gotAgain, _ := json.Marshal(again); !bytes.Equal(gotAgain, got) {
				t.Errorf("canonical form of the canonical form %s, want %s", gotAgain, got)
			}

			d := doc.Derive(names)
			segs, err := json.Marshal(d.Segments)
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(t, segs, vec.Segments) {
				t.Errorf("segments %s, want %s", segs, vec.Segments)
			}
			if d.Text != vec.Text {
				t.Errorf("text %q, want %q", d.Text, vec.Text)
			}
			if d.Preview != vec.Preview {
				t.Errorf("preview %q, want %q", d.Preview, vec.Preview)
			}
			if d.Words != vec.Words {
				t.Errorf("words %d, want %d", d.Words, vec.Words)
			}
			if d.Hash != vec.Hash {
				t.Errorf("hash %s, want %s", d.Hash, vec.Hash)
			}
		})
	}
}

// TestInvalidVectors checks that every invalid document of the vectors is
// refused for its reason, at its pointer: unknown nodes and marks, documents
// that are not of the schema's structure, links that the allowlist does not
// take, and lists nested too deep.
//
// Verifies: NFR-SEC-07, NFR-SEC-08
func TestInvalidVectors(t *testing.T) {
	for _, vec := range readVectors(t).Invalid {
		t.Run(vec.Note, func(t *testing.T) {
			_, err := Parse(vec.Doc)
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("Parse: %v, want an *Error", err)
			}
			if e.Reason != vec.Reason || e.Pointer != vec.Pointer {
				t.Errorf("%s at %q (%s), want %s at %q", e.Reason, e.Pointer, e.Message, vec.Reason, vec.Pointer)
			}
		})
	}
}

func TestWordVectors(t *testing.T) {
	for _, vec := range readVectors(t).Words {
		if got := CountWords(vec.Text); got != vec.Words {
			t.Errorf("%s: CountWords(%q) = %d, want %d", vec.Note, vec.Text, got, vec.Words)
		}
	}
}

func TestPreviewVectors(t *testing.T) {
	for _, vec := range readVectors(t).Previews {
		if got := Preview(vec.Text); got != vec.Preview {
			t.Errorf("%s: Preview(%q) = %q, want %q", vec.Note, vec.Text, got, vec.Preview)
		}
	}
}
