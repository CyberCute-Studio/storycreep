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
	validate := humaValidator(t, s, true)
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
// to huma, with which the API validates requests, both as JSON Schema reads
// it and with the discriminators that Doc gives huma. huma passes over a null
// in a property that a schema does not require, which JSON Schema refuses
// where the property's schema does, so a vector whose document holds a null
// may pass huma unless it is marked schema_accepts, as long as its null is
// all that is wrong with it; and, with the discriminators, a block, an inline
// node, or a mark that is not an object, or whose type is not a string
// (discriminated).
func TestSchemaAgreesWithParse(t *testing.T) {
	s := publishedSchema(t)
	validate, validateAPI := humaValidator(t, s, true), humaValidator(t, s, false)
	v := readVectors(t)
	for _, vec := range v.Valid {
		doc, err := Parse(vec.Doc)
		if err != nil {
			t.Fatalf("%s: %v", vec.Note, err)
		}
		canonical, _ := json.Marshal(doc)
		for name, validate := range map[string]func([]byte) []error{"the schema": validate, "huma": validateAPI} {
			if problems := validate(vec.Doc); problems != nil {
				t.Errorf("%s: %s refuses the document: %v", vec.Note, name, problems)
			}
			if problems := validate(canonical); problems != nil {
				t.Errorf("%s: %s refuses the canonical form: %v", vec.Note, name, problems)
			}
		}
	}
	nulls, passed := 0, 0
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
		if problems != nil && validateAPI(vec.Doc) == nil {
			if !discriminated(t, vec.Doc, vec.Reason, vec.Pointer) {
				t.Errorf("%s: the schema refuses it, but huma, with its discriminators, accepts it", vec.Note)
			}
			passed++
		}
	}
	if nulls == 0 {
		t.Error("no vector has a null in an optional field, which huma passes over")
	}
	if passed == 0 {
		t.Error("no vector has a node that is not an object, which huma's discriminators pass over")
	}
}

// discriminated reports whether an invalid vector's document is wrong only
// where huma's discriminator of a oneOf reads nothing: a block, an inline
// node, or a mark that is not an object, or whose type is not a string, which
// huma lets through without validating it against a member, and which Parse
// refuses with wrong_type at the value.
func discriminated(t *testing.T, doc []byte, reason Reason, pointer string) bool {
	t.Helper()
	var v any
	if err := json.Unmarshal(doc, &v); err != nil || reason != WrongType {
		return false
	}
	value, ok := resolve(v, pointer)
	if !ok {
		return false
	}
	at := pointer
	if node, isType := strings.CutSuffix(pointer, "/type"); isType {
		if _, isString := value.(string); isString {
			return false
		}
		at = node
	} else if _, isObject := value.(map[string]any); isObject {
		return false
	}
	// at is an element of an array: of a node's content or marks.
	i := strings.LastIndexByte(at, '/')
	if _, err := strconv.Atoi(at[i+1:]); i < 0 || err != nil {
		return false
	}
	j := strings.LastIndexByte(at[:i], '/')
	if j < 0 {
		return false
	}
	owner, _ := resolve(v, at[:j])
	o, _ := owner.(map[string]any)
	switch at[j+1 : i] {
	case "marks":
		return o != nil
	case "content":
		return o["type"] == nodeDoc || o["type"] == nodeListItem || o["type"] == nodeParagraph
	}
	return false
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
// TestSchemaFile holds the published schema s to; when strict, without the
// discriminators that Doc gives huma, of which JSON Schema has none, so that
// it validates as the published schema does. A document that encoding/json
// cannot decode, as one with a number too large for a double, is refused, as
// the API refuses a body before huma validates it.
func humaValidator(t testing.TB, s map[string]any, strict bool) func(doc []byte) []error {
	t.Helper()
	if !reflect.DeepEqual(s["$defs"], schemaDefinitions()) {
		t.Fatal("the published schema's definitions are not those that Doc gives huma")
	}
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	root := huma.SchemaFromType(reg, reflect.TypeFor[Doc]())
	discriminators := 0
	var strip func(s *huma.Schema)
	strip = func(s *huma.Schema) {
		if s == nil {
			return
		}
		if s.Discriminator != nil {
			discriminators++
			if strict {
				s.Discriminator = nil
			}
		}
		strip(s.Items)
		for _, p := range s.Properties {
			strip(p)
		}
		for _, o := range s.OneOf {
			strip(o)
		}
	}
	for _, rs := range reg.Map() {
		strip(rs)
	}
	// A block's, an inline node's, and a mark's.
	if discriminators != 3 {
		t.Fatalf("the schema that Doc gives huma has %d discriminators, not 3", discriminators)
	}
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
// deep, and a text with thousands of marks. It counts the schemas that huma
// resolves, which it does for each value that it validates against a
// definition and for each member of a oneOf that it tries, rather than the
// time that it takes, which a loaded machine would make vary: a oneOf of two
// members that both read a list's content made their number double with each
// depth. (An array that is uniqueItems, which huma checks in time that grows
// with the square of its length, is no resolution, but no schema that Doc
// gives huma has one, as its conversion takes no such keyword.)
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
	for name, doc := range map[string][]byte{
		"lists":            nested(`{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph"},`, `]}]}`),
		"nodes of no type": nested(`{"type":"x","content":[{"type":"x","content":[`, `]}]}`),
		"inline nodes with content": []byte(`{"type":"doc","content":[` +
			strings.Repeat(`{"type":"paragraph","content":[{"type":"text","text":"a","content":[`, depth) +
			strings.Repeat(`]}]}`, depth) + `]}`),
		"marks": []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[` +
			strings.Join(marks, ",") + `]}]}]}`),
	} {
		// Each of these resolves fewer schemas than a third of its bytes; a
		// byte each is a budget that only work that grows faster than the
		// document exceeds.
		if !resolvesWithin(t, doc, len(doc)) {
			t.Errorf("%s: huma resolved more than %d schemas, one for each byte, to validate it", name, len(doc))
		}
	}

	// Each node is validated against the definition of its type alone, which
	// a discriminator chooses, rather than against every member of a oneOf, so
	// that huma resolves about one schema for each object of marked text, its
	// nodes, its marks, and a link's attrs. Validating each inline node
	// against each kind of inline node, and each of its marks against each
	// kind of mark, made it resolve fifteen for each, and cost twenty times
	// the time that as many bytes of plain objects cost: 0.9 s for the 5 MiB
	// that the API takes.
	const nodes = 1000
	node := `{"type":"text","text":"a","marks":[{"type":"bold"},{"type":"italic"},{"type":"strike"},` +
		`{"type":"link","attrs":{"href":"https://example.com/"}}]}`
	marked := []byte(`{"type":"doc","content":[{"type":"paragraph","content":[` +
		strings.Join(slices.Repeat([]string{node}, nodes), ",") + `]}]}`)
	// The document, its paragraph, and for each node, the node, its four
	// marks, and its link's attrs.
	objects := 2 + 6*nodes
	if !resolvesWithin(t, marked, 2*objects) {
		t.Errorf("huma resolved more than %d schemas, two for each object, to validate %d objects of marked text",
			2*objects, objects)
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
	// huma refuses a document at the value that is wrong, as the schema's
	// discriminators have it validate each node against its type's
	// definition alone, rather than at the outermost oneOf, whose members'
	// errors it does not keep.
	list := func(inline string) string {
		return `{"text":{"type":"doc","content":[{"type":"bulletList","content":[{"type":"listItem","content":[` +
			`{"type":"paragraph","content":[` + inline + `]}]}]}]}}`
	}
	const item = "text.content[0].content[0].content[0].content[0]"
	for body, at := range map[string]string{
		`{"text":{"type":"doc","content":[{"type":"heading"}]}}`:          "text.content[0]",
		list(`{"type":"text","text":"a","marks":[{"type":"underline"}]}`): item + ".marks[0]",
		list(`{"type":"text","text":"a","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}`): item +
			".marks[0].attrs.href",
		list(`{"type":"text","text":"a` + `\n"}`):                                  item + ".text",
		list(`{"type":"mention","attrs":{"id":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"}}`): item + ".attrs.id",
		`{"text":{"Type":"doc","content":[{"type":"paragraph"}]}}`:                 "text",
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

	// huma (v2.39.1) pushes type onto its path at a node or a mark that has
	// no type, or one that is not a string, and does not pop it, so that it
	// reports the problems after it in the same body at locations with a
	// segment too many, as the README says: here text.content[1] and title.
	// When this fails, huma pops what it pushes: take the exception out of
	// the README, N40, CONTRIBUTING, and discriminator's comment, and have
	// this check that each problem is at its place.
	type titled struct {
		Text  Doc    `json:"text"`
		Title string `json:"title"`
	}
	treg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	tschema := huma.SchemaFromType(treg, reflect.TypeFor[titled]())
	for body, want := range map[string][]string{
		`{"text":{"type":"doc","content":[{"type":5},{"type":"heading"}]},"title":5}`: {
			"text.content[0][1]", "text.title"},
		`{"text":{"type":"doc","content":[{},{"type":"heading"}]},"title":5}`: {
			"text.content[0].type", "text.content[0][1]", "text.title"},
		`{"text":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a",` +
			`"marks":[{"type":["bold"]},{"type":"underline"}]}]}]},"title":5}`: {
			"text.content[0].content[0].marks[0][1]", "text.title"},
		`{"text":{"type":"doc","content":[{"type":"heading"},{"type":5}]},"title":5}`: {
			"text.content[0]", "text.title"},
	} {
		var v any
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatal(err)
		}
		res := &huma.ValidateResult{}
		huma.Validate(treg, tschema, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, res)
		var got []string
		for _, err := range res.Errors {
			var detail *huma.ErrorDetail
			if errors.As(err, &detail) {
				got = append(got, detail.Location)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s is refused at %q, not %q", body, got, want)
		}
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

// TestDiscriminator checks that the conversion to huma gives a oneOf of
// references to definitions of objects that require a type of their own a
// discriminator on type, gives none to a oneOf of schemas of their own, and
// panics at a oneOf of references that cannot have one, rather than leave
// huma to validate a node against every member, unnoticed.
func TestDiscriminator(t *testing.T) {
	ref := func(name string) any { return map[string]any{"$ref": defsPrefix + name} }
	refs := func(n int) []*huma.Schema {
		out := make([]*huma.Schema, n)
		for i := range out {
			out[i] = &huma.Schema{Ref: "#/components/schemas/M" + strconv.Itoa(i)}
		}
		return out
	}
	d := discriminator([]any{ref("paragraph"), ref("list")}, refs(2))
	want := map[string]string{nodeParagraph: "#/components/schemas/M0", nodeBulletList: "#/components/schemas/M1",
		nodeOrderedList: "#/components/schemas/M1"}
	if d == nil || d.PropertyName != "type" || !maps.Equal(d.Mapping, want) {
		t.Errorf("a block's discriminator is %+v", d)
	}
	if d := discriminator([]any{map[string]any{"type": "object"}, map[string]any{"type": "string"}}, refs(2)); d != nil {
		t.Errorf("a oneOf of schemas of their own has the discriminator %+v", d)
	}
	for name, members := range map[string][]any{
		"a reference to a definition without a type": {ref("paragraph"), ref("decimals")},
		"a member that is no reference":              {ref("paragraph"), map[string]any{"type": "object"}},
		"two members of one type":                    {ref("paragraph"), ref("paragraph")},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: discriminator does not panic", name)
				}
			}()
			discriminator(members, refs(len(members)))
		}()
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
	// The discriminators of a block's, an inline node's, and a mark's oneOf,
	// which map each type to its definition, as apicontract checks.
	schemas := d.Components.Schemas
	for _, c := range []struct {
		at     string
		s      *apicontract.Schema
		values int
	}{
		{"RichTextBlock", schemas["RichTextBlock"], 3},
		{"RichTextInline", schemas["RichTextInline"], 4},
		{"RichTextMarks.items", schemas["RichTextMarks"].Items, len(markOrder)},
	} {
		if disc := c.s.Discriminator; disc == nil || disc.PropertyName != "type" || len(disc.Mapping) != c.values {
			t.Errorf("%s's discriminator is %+v", c.at, disc)
		}
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
	validate := humaValidator(t, publishedSchema(t), true)
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
