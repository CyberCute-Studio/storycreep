package richtext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
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
	Lengths []struct {
		Note   string `json:"note"`
		JSON   string `json:"json"`
		Length int    `json:"length"`
	} `json:"lengths"`
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
		len(v.Words) == 0 || len(v.Previews) == 0 || len(v.Lengths) == 0 {
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

// compact returns the JSON text data without the white space between its
// tokens, and with its keys and strings as data writes them.
func compact(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return buf.Bytes()
}

// TestValidVectors checks that every valid document of the vectors is
// accepted, written in its canonical form, and derives the segments, plain
// text, preview, word count, and hash that the vectors give. The canonical
// form and the segments are compared byte for byte with the vectors', which
// write them as the server does, with sorted keys and HTML not escaped, once
// the white space between their tokens is taken out.
func TestValidVectors(t *testing.T) {
	v := readVectors(t)
	names := v.names()
	for _, vec := range v.Valid {
		t.Run(vec.Note, func(t *testing.T) {
			doc, err := Parse(vec.Doc)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := marshal(t, doc)
			want := vec.Canonical
			if want == nil {
				want = vec.Doc
			}
			if want := compact(t, want); !bytes.Equal(got, want) {
				t.Errorf("canonical form\n%s\nwant\n%s", got, want)
			}
			// The canonical form is a fixed point.
			again, err := Parse(got)
			if err != nil {
				t.Fatalf("Parse of the canonical form: %v", err)
			}
			if gotAgain := marshal(t, again); !bytes.Equal(gotAgain, got) {
				t.Errorf("canonical form of the canonical form %s, want %s", gotAgain, got)
			}

			d := doc.Derive(names)
			if segs, want := marshal(t, d.Segments), compact(t, vec.Segments); !bytes.Equal(segs, want) {
				t.Errorf("segments\n%s\nwant\n%s", segs, want)
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

// TestUnicodeTables checks the rules that read package unicode's tables. The
// README states their version wherever it names the letters, marks, and
// numbers that the server takes, of a word and of a mailto link's local part,
// so that a client whose tables are another version, as a JavaScript engine's
// are, knows where it differs from the server: U+2EBF0, a CJK ideograph that
// Unicode 15.1 assigned, is no letter in Unicode 15.0's. A Go whose tables
// are another version changes the server's word counts of text with the
// letters and numbers that Unicode assigned between the two, which the
// README, N40, and the exports' word counts (FR-TEXT-05) are then to be read
// again for, and takes those letters in a local part too, refusing none that
// it took.
//
// hidden takes the characters that it takes in Unicode 15.0, which are those
// of Unicode 17.0 too: a Go whose tables take another as hidden would refuse
// the stored documents whose links hold it, which could then no longer be
// read, as reading a stored document is parsing it, and one whose tables take
// fewer would take links that a client refuses. Such a Go needs the stored
// documents migrated, and the README read again, before this is changed.
func TestUnicodeTables(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "rich-text", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	major, rest, _ := strings.Cut(unicode.Version, ".")
	minor, _, _ := strings.Cut(rest, ".")
	stated := regexp.MustCompile(`as Unicode (\d+\.\d+) has them`).FindAllSubmatch(readme, -1)
	if len(stated) < 2 {
		t.Errorf("the README states the version of Unicode %d times, not once for a word's letters and once for "+
			"a local part's", len(stated))
	}
	for _, m := range stated {
		if v := string(m[1]); v != major+"."+minor {
			t.Errorf("the README says %q: package unicode's tables are Unicode %s", m[0], unicode.Version)
		}
	}
	if unicode.Version == "15.0.0" {
		if got := CountWords(string(rune(0x2EBF0))); got != 0 {
			t.Errorf("CountWords(U+2EBF0) = %d in Unicode 15.0, which has not assigned it", got)
		}
		if problem := CheckHref("mailto:" + string(rune(0x2EBF0)) + "@example.com"); problem == "" {
			t.Errorf("a local part of U+2EBF0 is taken in Unicode 15.0, which has not assigned it")
		}
	}
	// The characters that hidden takes, which Node 24's
	// /[\p{White_Space}\p{Cc}\p{Cf}\p{Default_Ignorable_Code_Point}]/u, of
	// Unicode 17.0, takes too: their number, and the start of the SHA-256 of
	// each one's code point in hexadecimal and a line feed, in order.
	const hiddenCount, hiddenSum = 4290, "3f41e26a"
	var n int
	h := sha256.New()
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if hidden(r) {
			n++
			fmt.Fprintf(h, "%x\n", r)
		}
	}
	if sum := hex.EncodeToString(h.Sum(nil))[:8]; n != hiddenCount || sum != hiddenSum {
		t.Errorf("hidden takes %d characters, whose hash starts %s, not %d and %s: package unicode's tables "+
			"(Unicode %s) take others as hidden, so that the links of stored documents may be refused, which "+
			"then cannot be read, or links taken that a client refuses", n, sum, hiddenCount, hiddenSum,
			unicode.Version)
	}
}

// TestLengthVectors checks that the size limit counts the length of a
// document's JSON as the vectors do: without the white space between its
// tokens, and with each escape as the character that it stands for, so that
// a document has one length, however it is written.
//
// Verifies: NFR-SEC-07
func TestLengthVectors(t *testing.T) {
	for _, vec := range readVectors(t).Lengths {
		if !json.Valid([]byte(vec.JSON)) {
			t.Errorf("%s: the vector's json is not JSON", vec.Note)
		}
		if got, _ := scan([]byte(vec.JSON)); got != vec.Length {
			t.Errorf("%s: the length is %d, want %d", vec.Note, got, vec.Length)
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
