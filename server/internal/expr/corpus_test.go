package expr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// corpusPath is the file of the parser's cases in the expression corpus.
var corpusPath = filepath.Join("..", "..", "..", "schema", "expr-corpus", "syntax.json")

// codes are the codes of Parse's errors, in the order of their declaration,
// which the README's table lists them in.
var codes = []Code{
	InvalidCharacter, UnterminatedString, InvalidEscape, StringTooLong, InvalidNumber, IntegerOutOfRange,
	FloatOutOfRange, IdentifierTooLong, UnexpectedToken, UnexpectedEnd, ReservedWord, InvalidName,
	InvalidNodeRef, InvalidStatement, InvalidAssignmentTarget, ConditionMustBePure, ASTTooDeep, ASTTooLarge,
}

// TestProblems checks that every code has the English description that an
// Error's message holds, and that nothing else has one.
func TestProblems(t *testing.T) {
	t.Parallel()
	if len(problems) != len(codes) {
		t.Errorf("problems describes %d codes; want the %d of codes", len(problems), len(codes))
	}
	for _, code := range codes {
		if problems[code] == "" {
			t.Errorf("the code %s has no problem", code)
		}
	}
}

// syntaxCase is one case of the corpus's syntax.json: a source of a context,
// and the AST or the error that it gives, as schema/expr-corpus/README.md
// describes them.
type syntaxCase struct {
	Note    string          `json:"note"`
	Context Context         `json:"context"`
	Source  json.RawMessage `json:"source"`
	AST     json.RawMessage `json:"ast"`
	Error   *Error          `json:"error"`
}

// readSyntaxCorpus returns the cases of syntax.json, each with its source
// read, and fails the test when the file is not of the corpus's format.
func readSyntaxCorpus(t testing.TB) (cases []syntaxCase, sources []string) {
	t.Helper()
	data, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []syntaxCase `json:"cases"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		t.Fatalf("syntax.json: %v", err)
	}
	for _, c := range file.Cases {
		var source string
		if err := json.Unmarshal(c.Source, &source); err != nil || escapesSurrogate(c.Source) {
			t.Fatalf("syntax.json: %s: the source is not a JSON string of characters, each written as itself "+
				"rather than as a surrogate's escape, which runners read alike", c.Note)
		}
		sources = append(sources, source)
	}
	return file.Cases, sources
}

// escapesSurrogate reports whether raw, a JSON string, escapes a UTF-16
// surrogate, which one JSON decoder reads as U+FFFD and another keeps, when it
// is not one of a pair.
func escapesSurrogate(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		// The escaped character, which the loop steps past, so that the \ of
		// \\ starts no escape.
		i++
		if i+4 < len(raw) && raw[i] == 'u' {
			u, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 32)
			if err == nil && u >= 0xD800 && u < 0xE000 {
				return true
			}
		}
	}
	return false
}

// TestSyntaxCorpus runs every case of the expression corpus's syntax.json, as
// the TypeScript parser runs them too: Parse gives each source the AST that
// the case holds, compared by value, as the corpus's README says, in the
// canonical form that Encode writes, each number the double nearest it, or
// the error, of its code and over its characters. It also checks the corpus
// itself: each case's note is its own, its context is a context, it has an
// AST or an error, its error's range is within its source, its AST gives no
// variable or attribute the ref or the attribute that name resolution adds,
// as the README says that no case does, and every code of Parse's errors has
// a case.
func TestSyntaxCorpus(t *testing.T) {
	t.Parallel()
	cases, sources := readSyntaxCorpus(t)
	notes := make(map[string]bool)
	seen := make(map[Code]bool)
	for i, c := range cases {
		where := "syntax.json: " + c.Note
		if c.Note == "" || notes[c.Note] {
			t.Errorf("%s: a case's note is empty or another case's", where)
		}
		notes[c.Note] = true
		if c.Context != ContextCondition && c.Context != ContextScript {
			t.Errorf("%s: %q is no context", where, c.Context)
			continue
		}
		if (c.AST == nil) == (c.Error == nil) {
			t.Errorf("%s: the case has both an AST and an error, or neither", where)
			continue
		}
		source := sources[i]
		got, err := Parse(source, c.Context)
		if c.Error != nil {
			seen[c.Error.Code] = true
			if !slices.Contains(codes, c.Error.Code) {
				t.Errorf("%s: %q is no code of an error", where, c.Error.Code)
			}
			if n := utf8.RuneCountInString(source); c.Error.Start < 0 || c.Error.Start > c.Error.End ||
				c.Error.End > n {
				t.Errorf("%s: the error's range %d to %d is not within the source's %d characters", where,
					c.Error.Start, c.Error.End, n)
			}
			if fmt.Sprint(err) != fmt.Sprint(c.Error) {
				t.Errorf("%s: Parse(%q) = %v, %v; want the error %v", where, source, encoded(got), err, c.Error)
			}
			continue
		}
		var tree any
		if json.Unmarshal(c.AST, &tree) == nil && resolved(tree) {
			t.Errorf("%s: the AST gives a variable or an attribute a ref or an attribute, which name resolution "+
				"adds and no case has", where)
		}
		want, err2 := canonicalJSON(c.AST)
		switch {
		case err2 != nil:
			t.Errorf("%s: the AST is not JSON: %v", where, err2)
		case err != nil:
			t.Errorf("%s: Parse(%q) = %v; want %s", where, source, err, want)
		case encoded(got) != string(want):
			t.Errorf("%s: Parse(%q) = %s; want %s", where, source, encoded(got), want)
		}
	}
	for _, code := range codes {
		if !seen[code] {
			t.Errorf("syntax.json has no case of the error %s", code)
		}
	}
}

// TestSyntaxErrorTable checks that the table of syntax errors in the corpus's
// README lists every code of Parse's errors, in the order of their
// declaration, and nothing else, so that the codes that a client tells errors
// by are the ones that the TypeScript parser is written to.
func TestSyntaxErrorTable(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(corpusPath), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "\n### Syntax errors\n")
	if !ok {
		t.Fatal("README.md has no section ### Syntax errors")
	}
	section, _, _ = strings.Cut(section, "\n#")
	var listed []Code
	for line := range strings.SplitSeq(section, "\n") {
		if rest, ok := strings.CutPrefix(line, "| `"); ok {
			code, _, _ := strings.Cut(rest, "`")
			listed = append(listed, Code(code))
		}
	}
	if !slices.Equal(listed, codes) {
		t.Errorf("README.md's syntax errors are %v; want %v", listed, codes)
	}
}

// resolved reports whether v, a case's AST as encoding/json decodes it, has a
// var or an attr with a ref or an attribute, which name resolution adds (PRD
// §5.8.3), rather than the name alone that the parser gives it.
func resolved(v any) bool {
	switch v := v.(type) {
	case []any:
		return slices.ContainsFunc(v, resolved)
	case map[string]any:
		if kind := v["kind"]; kind == string(KindVar) || kind == string(KindAttr) {
			_, ref := v["ref"]
			_, attribute := v["attribute"]
			if ref || attribute {
				return true
			}
		}
		for _, e := range v {
			if resolved(e) {
				return true
			}
		}
	}
	return false
}

// canonicalJSON returns the JSON raw as Encode writes an AST's: without white
// space, with each object's keys sorted, and with each number written as the
// double nearest it, as the corpus's README says that a runner reads one,
// which encoding/json writes as Encode does, so that Biome, which writes 1e+21
// as 1e21, can format the corpus.
func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	v, err := nearestNumbers(v)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// nearestNumbers returns v, a JSON value decoded with UseNumber, with each of
// its numbers the double nearest it. Every number of an AST is one: an int
// within ±(2^53 − 1), which a double holds exactly, or a float.
func nearestNumbers(v any) (any, error) {
	switch v := v.(type) {
	case json.Number:
		return strconv.ParseFloat(string(v), 64)
	case []any:
		for i, e := range v {
			var err error
			if v[i], err = nearestNumbers(e); err != nil {
				return nil, err
			}
		}
	case map[string]any:
		for k, e := range v {
			var err error
			if v[k], err = nearestNumbers(e); err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}

// encoded returns n's canonical form as a string, or what stops Encode, for a
// test's message.
func encoded(n Node) string {
	if n == nil {
		return "nil"
	}
	b, err := Encode(n)
	if err != nil {
		return err.Error()
	}
	return string(b)
}
