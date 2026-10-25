package published

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// decodeAll reads examples, each of the type T.
func decodeAll[T any](t *testing.T, examples []string) []T {
	t.Helper()
	out := make([]T, len(examples))
	for i, ex := range examples {
		if err := Decode([]byte(ex), &out[i]); err != nil {
			t.Fatalf("example %d: %v", i, err)
		}
	}
	return out
}

// exampleProject returns what the examples' expressions can name: the
// variables, the components and their attributes, and the boards' nodes.
func exampleProject(t *testing.T) *expr.Project {
	t.Helper()
	var variables []Variable
	for _, f := range decodeAll[Variables](t, variablesExamples) {
		variables = append(variables, f.Variables...)
	}
	var types []ComponentType
	for _, f := range decodeAll[ComponentTypes](t, componentTypesExamples) {
		types = append(types, f.ComponentTypes...)
	}
	var components []Component
	for _, f := range decodeAll[Components](t, componentsExamples) {
		components = append(components, f.Components...)
	}
	var nodes []Node
	for _, b := range decodeAll[Board](t, boardExamples) {
		nodes = append(nodes, b.Nodes...)
	}
	p, err := ExprProject(variables, types, components, nodes)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// expression is an expression of a board's example, at the board.
type expression struct {
	board, source string
	ast           expr.Node
	ctx           expr.Context
}

// boardExpressions returns every condition and script of the boards'
// examples.
func boardExpressions(t *testing.T) []expression {
	t.Helper()
	var out []expression
	cond := func(board string, c *Condition) {
		if c != nil {
			out = append(out, expression{board, c.Source, c.AST, expr.ContextCondition})
		}
	}
	script := func(board string, s *Script) {
		if s != nil {
			out = append(out, expression{board, s.Source, s.AST, expr.ContextScript})
		}
	}
	for _, b := range decodeAll[Board](t, boardExamples) {
		id := b.Board.ID
		for _, c := range b.Connections {
			cond(id, c.Condition)
			script(id, c.OnTraverse)
		}
		for _, n := range b.Nodes {
			switch n := n.(type) {
			case *Element:
				script(id, n.OnEnter)
				script(id, n.OnExit)
			case *Branch:
				script(id, n.OnEnter)
				script(id, n.OnExit)
				for _, o := range n.Outputs {
					cond(id, o.Condition)
				}
			}
		}
	}
	return out
}

// TestExampleExpressions checks that the AST of every expression of the
// boards' examples is what expr.Compile gives of its source, at its board,
// against the project that the examples declare, and that the AST's document
// holds exactly the ASTs of the boards' examples, so that the examples are
// what a publish writes.
func TestExampleExpressions(t *testing.T) {
	p := exampleProject(t)
	var asts []string
	for _, e := range boardExpressions(t) {
		got, err := expr.Encode(e.ast)
		if err != nil {
			t.Fatal(err)
		}
		n, err := expr.Compile(e.source, e.ctx, p, e.board)
		if err != nil {
			t.Errorf("%q does not compile: %v", e.source, err)
			continue
		}
		want, err := expr.Encode(n)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("the AST of %q is\n%s\nnot what it compiles to,\n%s", e.source, got, want)
		}
		if !slices.Contains(asts, string(got)) {
			asts = append(asts, string(got))
		}
	}
	var docs []string
	for _, ex := range astExamples {
		var a AST
		if err := Decode([]byte(ex), &a); err != nil {
			t.Fatal(err)
		}
		data, err := expr.Encode(a.Node)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, string(data))
	}
	slices.Sort(asts)
	slices.Sort(docs)
	if !slices.Equal(asts, docs) {
		t.Errorf("the AST's examples are\n%q\nnot the boards' ASTs,\n%q", docs, asts)
	}
}

// exampleNames names what the examples' rich texts refer to, as the
// examples' project names them.
type exampleNames struct{}

func (exampleNames) Component(id string) string {
	if id == "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF" {
		return "Elara"
	}
	return ""
}

func (exampleNames) Value(ref, attribute string) string {
	switch {
	case ref == "var_01HQ8Z5C2K8W1D5J9NTQZR4YBE":
		return "harbour_rumours"
	case ref == "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF" && attribute == "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF":
		return "elara.trust"
	}
	return ""
}

// exampleTexts are the rich-text documents of the boards' examples' texts
// and labels, by the ID of their node or connection, those of the rich text's
// own examples (schema/rich-text/v1/rich-text.schema.json) where they are
// one.
var exampleTexts = map[string]string{
	"nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF": `{"content":[{"content":[{"text":"Harbour's closed to strangers. State your business.","type":"text"}],"type":"paragraph"}],"type":"doc"}`,
	"cn_01HQ8ZC4M7D1F8K3NRTQW9XJVB": `{"content":[{"content":[{"text":"The keeper sent me.","type":"text"}],"type":"paragraph"}],"type":"doc"}`,
	"nd_01HQ8Z9R2M5K8W3XTVNPQ7DCBA": `{"content":[{"content":[{"attrs":{"id":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"},"type":"mention"},{"text":" has heard ","type":"text"},` +
		`{"attrs":{"ref":"var_01HQ8Z5C2K8W1D5J9NTQZR4YBE"},"type":"valueRef"},{"text":" rumours.","type":"text"},{"type":"hardBreak"},` +
		`{"text":"Trust: ","type":"text"},{"attrs":{"attribute":"atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF","decimals":1,"ref":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"},` +
		`"marks":[{"type":"bold"}],"type":"valueRef"}],"type":"paragraph"}],"type":"doc"}`,
	"cn_01HQ8ZH3M6K9W2R5XTVNPQ7DCB": `{"content":[{"content":[{"text":"Ask about the dark light.","type":"text"}],"type":"paragraph"}],"type":"doc"}`,
}

// TestExampleTexts checks that every text and label of the boards' examples
// is what richtext derives of its document, its segments and its preview.
func TestExampleTexts(t *testing.T) {
	derived := func(id string) richtext.Derived {
		t.Helper()
		src, ok := exampleTexts[id]
		if !ok {
			t.Fatalf("%s's text has no document", id)
		}
		doc, err := richtext.Parse([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		return doc.Derive(exampleNames{})
	}
	same := func(id string, got, want richtext.Segments) {
		t.Helper()
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Errorf("%s's segments are\n%s\nnot what its document derives,\n%s", id, a, b)
		}
	}
	seen := 0
	for _, b := range decodeAll[Board](t, boardExamples) {
		for _, c := range b.Connections {
			if c.Label != nil {
				same(c.ID, c.Label.Segments, derived(c.ID).Segments)
				seen++
			}
		}
		for _, n := range b.Nodes {
			if e, ok := n.(*Element); ok && e.Text != nil {
				d := derived(e.ID)
				same(e.ID, e.Text.Segments, d.Segments)
				if e.Text.Preview != d.Preview {
					t.Errorf("%s's preview is %q, not %q", e.ID, e.Text.Preview, d.Preview)
				}
				seen++
			}
		}
	}
	if seen != len(exampleTexts) {
		t.Errorf("the examples have %d texts and labels, and exampleTexts %d", seen, len(exampleTexts))
	}
}
