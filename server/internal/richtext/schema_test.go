package richtext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/CyberCute-Studio/storycreep/server/internal/apicontract"
)

var update = flag.Bool("update", false, "rewrite the published rich-text JSON Schema from Schema")

var schemaFile = filepath.Join("..", "..", "..", filepath.FromSlash(SchemaPath))

// TestSchemaFile checks that the published JSON Schema is the one that Schema
// generates, so that it cannot drift from the code (PRD FR-DATA-04). Run
// go test ./internal/richtext -run TestSchemaFile -update to rewrite it.
func TestSchemaFile(t *testing.T) {
	want := Schema()
	if *update {
		if err := os.WriteFile(schemaFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what Schema generates; run go test ./internal/richtext -run TestSchemaFile -update", SchemaPath)
	}
}

// publishedSchema returns the published JSON Schema as JSON decodes it.
func publishedSchema(t testing.TB) map[string]any {
	t.Helper()
	data, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if s["$id"] != SchemaID || !strings.Contains(SchemaID, "/v1/") {
		t.Errorf("the schema's $id is %v, want %s, which names its version", s["$id"], SchemaID)
	}
	return s
}

// TestSchemaExamples checks that Parse accepts each of the published schema's
// examples, which are in their canonical form, and that each validates
// against the schema.
func TestSchemaExamples(t *testing.T) {
	s := publishedSchema(t)
	validate := humaValidator(t, s)
	examples, _ := s["examples"].([]any)
	if len(examples) == 0 {
		t.Fatal("the schema has no examples")
	}
	for i, ex := range examples {
		data, err := json.Marshal(ex)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Parse(data)
		if err != nil {
			t.Errorf("example %d: %v", i, err)
			continue
		}
		canonical, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(canonical, data) {
			t.Errorf("example %d is not in its canonical form %s", i, canonical)
		}
		if problems := validate(data); problems != nil {
			t.Errorf("example %d does not validate against the schema: %v", i, problems)
		}
	}
}

// TestSchemaAgreesWithParse checks the published schema against Parse on the
// vectors: every document that Parse accepts validates against the schema,
// and so does its canonical form, and the schema refuses every document that
// Parse refuses, except those that the vectors mark schema_accepts, which it
// accepts, because what is wrong with them is beyond what JSON Schema
// expresses. The schema is validated with huma's validator, as Doc gives it
// to huma, with which the API validates requests. huma passes over a null in
// a property that a schema does not require, which JSON Schema refuses where
// the property's schema does, so a vector whose document holds a null may
// pass huma unless it is marked schema_accepts, as long as its null is all
// that is wrong with it.
func TestSchemaAgreesWithParse(t *testing.T) {
	validate := humaValidator(t, publishedSchema(t))
	v := readVectors(t)
	for _, vec := range v.Valid {
		doc, err := Parse(vec.Doc)
		if err != nil {
			t.Fatalf("%s: %v", vec.Note, err)
		}
		canonical, _ := json.Marshal(doc)
		if problems := validate(vec.Doc); problems != nil {
			t.Errorf("%s: the schema refuses the document: %v", vec.Note, problems)
		}
		if problems := validate(canonical); problems != nil {
			t.Errorf("%s: the schema refuses the canonical form: %v", vec.Note, problems)
		}
	}
	nulls := 0
	for _, vec := range v.Invalid {
		problems := validate(vec.Doc)
		switch {
		case vec.SchemaAccepts && problems != nil:
			t.Errorf("%s: marked schema_accepts, but the schema refuses it: %v", vec.Note, problems)
		case !vec.SchemaAccepts && problems == nil:
			at, ok := nullAt(t, vec.Doc)
			if !ok || vec.Reason != WrongType || vec.Pointer != at {
				t.Errorf("%s: the schema accepts it, but the vector is not marked schema_accepts", vec.Note)
			}
			nulls++
		}
	}
	if nulls == 0 {
		t.Error("no vector has a null in an optional field, which huma passes over")
	}
}

// nullAt returns the pointer of the first null in the JSON document doc, in
// the order of its keys, or ok false if it holds none.
func nullAt(t *testing.T, doc []byte) (string, bool) {
	t.Helper()
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return "", false
	}
	var walk func(v any, at string) (string, bool)
	walk = func(v any, at string) (string, bool) {
		switch v := v.(type) {
		case nil:
			return at, true
		case []any:
			for i, e := range v {
				if p, ok := walk(e, at+"/"+strconv.Itoa(i)); ok {
					return p, true
				}
			}
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(v)) {
				if p, ok := walk(v[k], at+"/"+strings.NewReplacer("~", "~0", "/", "~1").Replace(k)); ok {
					return p, true
				}
			}
		}
		return "", false
	}
	return walk(v, "")
}

func init() {
	// The API and the commands' pipeline match a field's name in its case
	// (package api, package commands), and so does the validation here.
	huma.ValidateStrictCasing = true
}

// humaValidator returns a function that validates a JSON document with huma,
// against the schema that Doc gives huma, and returns the problems that it
// finds. That schema is converted from the definitions of Schema, which
// TestSchemaFile holds the published schema s to, so it validates as the
// published schema does. A document that encoding/json cannot decode, as one
// with a number too large for a double, is refused, as the API refuses a
// body before huma validates it.
func humaValidator(t testing.TB, s map[string]any) func(doc []byte) []error {
	t.Helper()
	if !reflect.DeepEqual(s["$defs"], schemaDefinitions()) {
		t.Fatal("the published schema's definitions are not those that Doc gives huma")
	}
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	root := huma.SchemaFromType(reg, reflect.TypeFor[Doc]())
	return func(doc []byte) []error {
		var v any
		if err := json.Unmarshal(doc, &v); err != nil {
			return []error{err}
		}
		res := &huma.ValidateResult{}
		huma.Validate(reg, root, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, res)
		return res.Errors
	}
}

// countingRegistry is a registry that counts the schemas that huma's
// validator resolves by their references, which it does for each value that
// it validates against a definition, and for each member of a oneOf that it
// tries, and that stops the validation, panicking with errBudget, once they
// are more than budget, so that a validation whose work grows faster than the
// document ends, rather than holding the test binary.
type countingRegistry struct {
	huma.Registry
	resolved, budget int
}

var errBudget = errors.New("huma resolved more schemas than its budget")

func (r *countingRegistry) SchemaFromRef(ref string) *huma.Schema {
	if r.resolved++; r.resolved > r.budget {
		panic(errBudget)
	}
	return r.Registry.SchemaFromRef(ref)
}

// resolvesWithin reports whether huma resolves at most budget schemas to
// validate the JSON document doc against the schema that Doc gives huma.
func resolvesWithin(t *testing.T, doc []byte, budget int) (ok bool) {
	t.Helper()
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	root := huma.SchemaFromType(reg, reflect.TypeFor[Doc]())
	defer func() {
		if p := recover(); p != nil {
			if p != errBudget {
				panic(p)
			}
			ok = false
		}
	}()
	r := &countingRegistry{Registry: reg, budget: budget}
	huma.Validate(r, root, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, &huma.ValidateResult{})
	return true
}

// TestSchemaValidationCost checks that huma's work to validate a document
// against the schema grows with its size, whatever it holds, so that a body
// of a few kilobytes cannot hold the server's CPU (NFR-SEC-07): lists nested
// as deep as a body nests at most, nodes of no type of the schema nested as
// deep, a text with thousands of marks, and marked text, each of whose marks
// huma validates against each kind of mark once for each kind of inline
// node. It counts the schemas that huma resolves, which it does for each
// value that it validates against a definition and for each member of a
// oneOf that it tries, rather than the time that it takes, which a loaded
// machine would make vary: a oneOf of two members that both read a list's
// content made their number double with each depth. (An array that is
// uniqueItems, which huma checks in time that grows with the square of its
// length, is no resolution, but no schema that Doc gives huma has one, as its
// conversion takes no such keyword; TestSchemaValidationLocations checks the
// memory of huma's problems.)
//
// Verifies: NFR-SEC-07
func TestSchemaValidationCost(t *testing.T) {
	// The API refuses a body nested more than 256 deep, and each list is
	// four levels: the list, its content, an item, and the item's content.
	const depth = 60
	nested := func(open, close string) []byte {
		return []byte(`{"type":"doc","content":[` + strings.Repeat(open, depth) + `{"type":"paragraph"}` +
			strings.Repeat(close, depth) + `]}`)
	}
	marks := make([]string, 20_000)
	for i := range marks {
		marks[i] = fmt.Sprintf(`{"type":"bold","n":%d}`, i)
	}
	node := `{"type":"text","text":"a","marks":[{"type":"bold"},{"type":"italic"},{"type":"strike"},` +
		`{"type":"link","attrs":{"href":"https://example.com/"}}]}`
	for name, doc := range map[string][]byte{
		"lists":            nested(`{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph"},`, `]}]}`),
		"nodes of no type": nested(`{"type":"x","content":[{"type":"x","content":[`, `]}]}`),
		"inline nodes with content": []byte(`{"type":"doc","content":[` +
			strings.Repeat(`{"type":"paragraph","content":[{"type":"text","text":"a","content":[`, depth) +
			strings.Repeat(`]}]}`, depth) + `]}`),
		"marks": []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[` +
			strings.Join(marks, ",") + `]}]}]}`),
		"marked text": []byte(`{"type":"doc","content":[{"type":"paragraph","content":[` +
			strings.Join(slices.Repeat([]string{node}, 1000), ",") + `]}]}`),
	} {
		// Each of these resolves fewer schemas than its bytes, marked text
		// about three fifths as many; a byte each is a budget that only work
		// that grows faster than the document exceeds.
		if !resolvesWithin(t, doc, len(doc)) {
			t.Errorf("%s: huma resolved more than %d schemas, one for each byte, to validate it", name, len(doc))
		}
	}
}

// TestSchemaValidationLocations checks that huma reports each problem of a
// body at its place, however many nodes before it are wrong, and so keeps
// problems whose memory grows with the body (NFR-SEC-07). A discriminator on
// a oneOf has huma (v2.39.1) add a segment to the location of every later
// problem for each object without the discriminator's property, or whose
// property is neither a string nor null, which made a body of 24 KB of {}
// cost it 191 MB, and one of 5 MiB, which the API takes, run the server out
// of memory; so no schema that Doc gives huma has one, which package
// apicontract also refuses.
//
// Verifies: NFR-SEC-07
func TestSchemaValidationLocations(t *testing.T) {
	type titled struct {
		Text  Doc    `json:"text"`
		Title string `json:"title"`
	}
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	schema := huma.SchemaFromType(reg, reflect.TypeFor[titled]())
	var discriminated func(s *huma.Schema) bool
	discriminated = func(s *huma.Schema) bool {
		if s == nil {
			return false
		}
		if s.Discriminator != nil || discriminated(s.Items) || discriminated(s.Not) {
			return true
		}
		for _, sub := range slices.Concat(slices.Collect(maps.Values(s.Properties)), s.OneOf, s.AnyOf, s.AllOf) {
			if discriminated(sub) {
				return true
			}
		}
		return false
	}
	for name, s := range reg.Map() {
		if discriminated(s) {
			t.Errorf("the schema %s that Doc gives huma has a discriminator", name)
		}
	}
	const n = 2000
	for _, wrong := range []string{`{}`, `{"type":5}`, `{"type":["paragraph"]}`, `{"type":null}`, `{"type":"heading"}`,
		`5`, `[]`} {
		// n blocks that are wrong, then a paragraph of n inline nodes that are
		// wrong, then a title that is wrong.
		nodes := strings.Join(slices.Repeat([]string{wrong}, n), ",")
		body := `{"text":{"type":"doc","content":[` + nodes + `,{"type":"paragraph","content":[` + nodes +
			`]}]},"title":5}`
		var v any
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatal(err)
		}
		res := &huma.ValidateResult{}
		huma.Validate(reg, schema, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, res)
		var want []string
		for i := range n + 1 {
			want = append(want, "text.content["+strconv.Itoa(i)+"]")
		}
		want = append(want, "title")
		var got []string
		for _, err := range res.Errors {
			var detail *huma.ErrorDetail
			if errors.As(err, &detail) {
				got = append(got, detail.Location)
			}
		}
		if !slices.Equal(got, want) {
			last := ""
			if len(got) > 0 {
				last = got[len(got)-1]
			}
			t.Errorf("blocks and inline nodes %s: huma reports %d problems, the last at %.200q, not %d, each at its place",
				wrong, len(got), last, len(want))
		}
	}
}

// TestDocField checks that a request's body or a command's payload can hold
// a document in a field: huma validates the field against the document's
// schema, which its registry holds once, with each of the schema's
// definitions, however many fields refer to it, and the field reads as Parse
// reads it, with null leaving it as it is.
func TestDocField(t *testing.T) {
	type payload struct {
		Text  Doc  `json:"text"`
		Label *Doc `json:"label,omitempty"`
	}
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	schema := huma.SchemaFromType(reg, reflect.TypeFor[payload]())
	for _, field := range []string{"text", "label"} {
		ref := schema.Properties[field]
		if ref == nil || ref.Ref != "#/components/schemas/RichTextDoc" {
			t.Errorf("the field %s's schema is %+v, not a reference to RichTextDoc", field, ref)
		}
	}
	for name := range schemaDefinitions() {
		key := "RichText" + strings.ToUpper(name[:1]) + name[1:]
		if s, ok := reg.Map()[key]; !ok || s.Type == "" && s.OneOf == nil {
			t.Errorf("the registry holds no schema of the definition %s as %s", name, key)
		}
	}
	validate := func(body string) []error {
		var v any
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatal(err)
		}
		res := &huma.ValidateResult{}
		huma.Validate(reg, schema, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, res)
		return res.Errors
	}
	body := `{"text":{"type":"doc","content":[{"type":"bulletList","content":[{"type":"listItem","content":[` +
		`{"type":"paragraph","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}]}]}}`
	if problems := validate(body); problems != nil {
		t.Errorf("a document: %v", problems)
	}
	var p payload
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	if got := marshal(t, p.Text); string(got) != `{"content":[{"content":[{"content":[{"content":[`+
		`{"text":"ab","type":"text"}],"type":"paragraph"}],"type":"listItem"}],"type":"bulletList"}],"type":"doc"}` {
		t.Errorf("read %s", got)
	}
	// huma refuses a document with a node or a mark that the schema does not
	// take, or that holds a value that it does not take, at the outermost
	// block that holds it, as it keeps no problem of the members of a oneOf,
	// and a problem outside the blocks at its place.
	list := func(inline string) string {
		return `{"text":{"type":"doc","content":[{"type":"paragraph"},{"type":"bulletList","content":[` +
			`{"type":"listItem","content":[{"type":"paragraph","content":[` + inline + `]}]}]}]}}`
	}
	const block = "text.content[1]"
	for body, at := range map[string]string{
		`{"text":{"type":"doc","content":[{"type":"heading"}]}}`:                                            "text.content[0]",
		list(`{"type":"text","text":"a","marks":[{"type":"underline"}]}`):                                   block,
		list(`{"type":"text","text":"a","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}`): block,
		list(`{"type":"text","text":"a` + `\n"}`):                                                           block,
		list(`{"type":"mention","attrs":{"id":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"}}`):                          block,
		list(`5`):                              block,
		`{"text":{"type":"doc","content":[]}}`: "text.content",
		`{"text":{"Type":"doc","content":[{"type":"paragraph"}]}}`: "text",
		`{"text":null}`: "text",
	} {
		problems := validate(body)
		if len(problems) == 0 {
			t.Errorf("%s validates", body)
			continue
		}
		var detail *huma.ErrorDetail
		if !errors.As(problems[0], &detail) || detail.Location != at {
			t.Errorf("%s is refused at %v, not %s", body, problems, at)
		}
	}
	p = payload{Text: Doc{Blocks: []Block{&Paragraph{Inlines: []Inline{&Text{Text: "kept"}}}}}}
	if err := json.Unmarshal([]byte(`{"text":null}`), &p); err != nil || len(p.Text.Blocks) != 1 {
		t.Errorf("null: %v, %+v", err, p.Text)
	}

	// A registry whose namer names the document's schema otherwise, as the
	// commands' pipeline's does, names the definitions after it.
	reg = huma.NewMapRegistry("#/components/schemas/", func(t reflect.Type, hint string) string {
		return "pkg_" + huma.DefaultSchemaNamer(t, hint)
	})
	if ref := huma.SchemaFromType(reg, reflect.TypeFor[Doc]()).Ref; ref != "#/components/schemas/pkg_RichTextDoc" {
		t.Errorf("the document's schema is %s", ref)
	}
	if _, ok := reg.Map()["pkg_RichTextBlock"]; !ok {
		t.Errorf("the registry holds no pkg_RichTextBlock, but %v", slices.Sorted(maps.Keys(reg.Map())))
	}
}

// TestDocFieldNameTaken checks that a Doc field refuses a registry in which a
// Go type holds the name of one of the schema's definitions, whose schema the
// document's would otherwise refer to.
func TestDocFieldNameTaken(t *testing.T) {
	type RichTextList struct {
		N int `json:"n"`
	}
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	if ref := reg.Schema(reflect.TypeFor[RichTextList](), true, "").Ref; ref != "#/components/schemas/RichTextList" {
		t.Fatalf("the type is registered as %s", ref)
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "RichTextList") {
			t.Errorf("a Doc in a registry that names another type RichTextList: %v", r)
		}
	}()
	huma.SchemaFromType(reg, reflect.TypeFor[Doc]())
}

// TestDocFieldOpenAPI checks that the API's OpenAPI document, once a
// request's body holds a Doc field, is one that package apicontract reads,
// compares, and generates the TypeScript client's types from, as the API's
// tests and its compatibility check do (PRD FR-API-02): the document's schema
// has only the keywords that apicontract reads.
//
// Verifies: FR-API-02/AC1, FR-API-02/AC2
func TestDocFieldOpenAPI(t *testing.T) {
	_, api := humatest.New(t)
	type input struct {
		Body struct {
			Text  Doc  `json:"text"`
			Label *Doc `json:"label,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "put-text", Method: http.MethodPost, Path: "/text"},
		func(context.Context, *input) (*struct{}, error) { return nil, nil })
	data, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	d, err := apicontract.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if changes := apicontract.Breaking(d, d); len(changes) > 0 {
		t.Errorf("the document breaks itself: %v", changes)
	}
	ts, err := apicontract.TypeScript(d, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"export interface RichTextDoc ", "export type RichTextBlock = RichTextParagraph | RichTextList;",
		"text: RichTextDoc;", "label?: RichTextDoc;"} {
		if !strings.Contains(string(ts), want) {
			t.Errorf("the TypeScript types have no %q", want)
		}
	}
}

// TestSchemaPatterns checks that the schema's patterns refuse what Parse
// refuses for the characters that they cover, so that the schema is no looser
// there than it says.
func TestSchemaPatterns(t *testing.T) {
	validate := humaValidator(t, publishedSchema(t))
	withText := func(text string) []byte {
		s, _ := json.Marshal(text)
		return fmt.Appendf(nil, `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":%s}]}]}`, s)
	}
	withHref := func(href string) []byte {
		s, _ := json.Marshal(href)
		return fmt.Appendf(nil, `{"type":"doc","content":[{"type":"paragraph","content":[`+
			`{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":%s}}]}]}]}`, s)
	}
	for _, r := range append([]rune{lineSeparator, paragraphSeparator}, rangeOf(0, 0xA0)...) {
		doc := withText("a" + string(r))
		_, err := Parse(doc)
		if (validate(doc) == nil) != (err == nil) {
			t.Errorf("U+%04X in text: Parse says %v, and the schema the opposite", r, err)
		}
	}
	// The schema refuses a link with a space or a control character, and
	// leaves the rest of what makes one not a URL, as a % that starts no
	// escape, or white space beyond ASCII, to Parse.
	for _, r := range rangeOf(0, 0xA0) {
		doc := withHref("https://example.com/a" + string(r))
		_, err := Parse(doc)
		schemaErr := validate(doc)
		if err == nil && schemaErr != nil {
			t.Errorf("U+%04X in a link: Parse accepts it, and the schema refuses it: %v", r, schemaErr)
		}
		if (r <= 0x20 || r >= 0x7F && r <= 0x9F) && schemaErr == nil {
			t.Errorf("U+%04X in a link: the schema accepts it", r)
		}
	}
}

func rangeOf(lo, hi rune) []rune {
	var rs []rune
	for r := lo; r <= hi; r++ {
		rs = append(rs, r)
	}
	return rs
}
