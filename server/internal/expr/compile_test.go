package expr

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// compilePath is the file of the checker's cases in the expression corpus.
var compilePath = filepath.Join(filepath.Dir(corpusPath), "compile.json")

// compileCase is one case of the corpus's compile.json: a source of a context,
// on a board or on none, and the AST or the error that it compiles to, as
// schema/expr-corpus/README.md describes them.
type compileCase struct {
	Note    string          `json:"note"`
	Context Context         `json:"context"`
	Board   string          `json:"board"`
	Source  json.RawMessage `json:"source"`
	AST     json.RawMessage `json:"ast"`
	Error   *Error          `json:"error"`
}

// compileCorpus is compile.json: the project that its cases resolve names
// against, and its cases.
type compileCorpus struct {
	Project struct {
		Variables  []Variable  `json:"variables"`
		Components []Component `json:"components"`
		Nodes      []BoardNode `json:"nodes"`
	} `json:"project"`
	Cases []compileCase `json:"cases"`
}

// readCompileCorpus returns compile.json's project and its cases, each with
// its source read, and fails the test when the file is not of the corpus's
// format.
func readCompileCorpus(t testing.TB) (p *Project, cases []compileCase, sources []string) {
	t.Helper()
	data, err := os.ReadFile(compilePath)
	if err != nil {
		t.Fatal(err)
	}
	var file compileCorpus
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		t.Fatalf("compile.json: %v", err)
	}
	pr := file.Project
	if p, err = NewProject(pr.Variables, pr.Components, pr.Nodes); err != nil {
		t.Fatalf("compile.json: the project: %v", err)
	}
	for _, c := range file.Cases {
		var source string
		if err := json.Unmarshal(c.Source, &source); err != nil || escapesSurrogate(c.Source) {
			t.Fatalf("compile.json: %s: the source is not a JSON string of characters, each written as itself "+
				"rather than as a surrogate's escape, which runners read alike", c.Note)
		}
		sources = append(sources, source)
	}
	return p, file.Cases, sources
}

// TestCompileCorpus runs every case of the expression corpus's compile.json,
// as the TypeScript checker runs them too: Compile gives each source, on its
// board, against the corpus's project, the AST that the case holds, compared
// by value, or its error, compared as its JSON, which an error's code, range,
// type, expected types, and suggestions are. It also checks the corpus
// itself: each case's note is its own, its context is a context, it has an AST
// or an error, its error's range is within its source, its AST resolves every
// variable and attribute, and every code of Check's errors has a case, but for
// ast_too_large, which the parser gives every source first.
//
// Verifies: FR-EXPR-02/AC4, FR-VAR-02/AC2
func TestCompileCorpus(t *testing.T) {
	t.Parallel()
	p, cases, sources := readCompileCorpus(t)
	notes := make(map[string]bool)
	seen := make(map[Code]bool)
	for i, c := range cases {
		where := "compile.json: " + c.Note
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
		got, err := Compile(source, c.Context, p, c.Board)
		if c.Error != nil {
			seen[c.Error.Code] = true
			if !slices.Contains(compileCodes, c.Error.Code) && !slices.Contains(syntaxCodes, c.Error.Code) {
				t.Errorf("%s: %q is no code of an error", where, c.Error.Code)
			}
			if n := utf8.RuneCountInString(source); c.Error.Start < 0 || c.Error.Start > c.Error.End ||
				c.Error.End > n {
				t.Errorf("%s: the error's range %d to %d is not within the source's %d characters", where,
					c.Error.Start, c.Error.End, n)
			}
			want, _ := json.Marshal(c.Error)
			gotJSON, _ := json.Marshal(err)
			if _, ok := err.(*Error); !ok || !bytes.Equal(gotJSON, want) {
				t.Errorf("%s: Compile(%q) = %v, %v; want the error %s", where, source, encoded(got), err, want)
			}
			continue
		}
		var tree any
		if json.Unmarshal(c.AST, &tree) == nil && !fullyResolved(tree) {
			t.Errorf("%s: the AST leaves a variable or an attribute without its ref, or its attribute", where)
		}
		want, err2 := canonicalJSON(c.AST)
		switch {
		case err2 != nil:
			t.Errorf("%s: the AST is not JSON: %v", where, err2)
		case err != nil:
			t.Errorf("%s: Compile(%q) = %v; want %s", where, source, err, want)
		case encoded(got) != string(want):
			t.Errorf("%s: Compile(%q) = %s; want %s", where, source, encoded(got), want)
		}
	}
	for _, code := range compileCodes {
		if !seen[code] && code != ASTTooLarge {
			t.Errorf("compile.json has no case of the error %s", code)
		}
	}
}

// fullyResolved reports whether v, a case's AST as encoding/json decodes it,
// gives every var a ref, and every attr a ref and an attribute.
func fullyResolved(v any) bool {
	switch v := v.(type) {
	case []any:
		for _, e := range v {
			if !fullyResolved(e) {
				return false
			}
		}
	case map[string]any:
		_, ref := v["ref"]
		_, attribute := v["attribute"]
		switch v["kind"] {
		case string(KindVar):
			if !ref {
				return false
			}
		case string(KindAttr):
			if !ref || !attribute {
				return false
			}
		}
		for _, e := range v {
			if !fullyResolved(e) {
				return false
			}
		}
	}
	return true
}

// TestCorpusSize checks that the corpus holds at least the 200 expressions,
// valid and invalid, that PRD FR-EXPR-02 AC1 asks of it: the parser's cases
// and the checker's.
func TestCorpusSize(t *testing.T) {
	t.Parallel()
	syntax, _ := readSyntaxCorpus(t)
	_, compile, _ := readCompileCorpus(t)
	if n := len(syntax) + len(compile); n < 200 {
		t.Errorf("the corpus has %d cases of expressions; want at least 200", n)
	}
}

// TestCompileErrorTable checks that the table of compile errors in the
// corpus's README lists every code of Check's errors, in the order of
// compileCodes, and nothing else, so that the codes that a client tells
// errors by are the ones that the TypeScript checker is written to.
func TestCompileErrorTable(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(corpusPath), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "\n### Compile errors\n")
	if !ok {
		t.Fatal("README.md has no section ### Compile errors")
	}
	section, _, _ = strings.Cut(section, "\n#")
	var listed []Code
	for line := range strings.SplitSeq(section, "\n") {
		if rest, ok := strings.CutPrefix(line, "| `"); ok {
			code, _, _ := strings.Cut(rest, "`")
			listed = append(listed, Code(code))
		}
	}
	if !slices.Equal(listed, compileCodes) {
		t.Errorf("README.md's compile errors are %v; want %v", listed, compileCodes)
	}
}
