package authoring

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
)

// differentialRate is the share of the mutations of each document that
// TestFailFastAgreesWithTheFullValidator runs, which keeps it to a few seconds.
const differentialRate = 0.008

const (
	sampleID   = "01HF7YAT0YFJ2B7PVY974WE5S0"
	sampleText = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"About"},` +
		`{"type":"valueRef","attrs":{"ref":"var_` + sampleID + `"}},` +
		`{"type":"mention","attrs":{"id":"cmp_` + sampleID + `"}}]}]}`
	sampleHash = "6fafbce60c110c48e78331626858559b15e5322fb140c00ec2400dba0686adae"
	sampleTime = "2026-11-23T19:58:01.375207Z"
)

// differentialSamples holds a valid file of each section and of the manifest,
// with each of its optional fields.
var differentialSamples = map[string]string{
	ManifestPath: `{"format":"storycreep.authoring","formatVersion":1,"publishedSchemaVersion":1,
		"generator":{"name":"storycreep","version":"1"},"exportedAt":"` + sampleTime + `",
		"project":{"id":"prj_` + sampleID + `","name":"Harbour","slug":"harbour"},
		"source":{"kind":"release","snapshotId":"snp_` + sampleID + `","version":"1.0.0","contentHash":"sha256:` + sampleHash + `"},
		"profile":"full","personalData":true,"includesAssets":true,"includesReleases":true,
		"sections":[{"name":"folders","path":"draft/folders.json","items":1,"sha256":"` + sampleHash + `"}],
		"leftOut":{"comments":1,"playStates":2}}`,
	"draft/project.json": `{"name":"Harbour","slug":"harbour","description":"A port","settings":{"default_locale":"en"}}`,
	"draft/folders.json": `{"items":[{"id":"fld_` + sampleID + `","kind":"board","parentId":"fld_` + sampleID + `",
		"name":"Acts","order":"V"}]}`,
	"draft/boards.json": `{"items":[{"id":"bd_` + sampleID + `","folderId":"fld_` + sampleID + `","order":"V","name":"Intro",
		"slug":"intro","description":"First","color":"teal","entryNodeId":"nd_` + sampleID + `"}]}`,
	"draft/variables.json": `{"items":[{"id":"var_` + sampleID + `","scope":"board","boardId":"bd_` + sampleID + `",
		"folderId":"fld_` + sampleID + `","order":"V","key":"gold","type":"int","default":883,"description":"Coins",
		"resetOnEnter":true}]}`,
	"draft/component_types.json": `{"items":[{"id":"ct_` + sampleID + `","key":"place","name":"Place","icon":"location",
		"attributes":[{"id":"atr_` + sampleID + `","order":"V","key":"mood","name":"Mood","type":"enum",
		"enumOptions":["calm","wild"],"default":"calm","isRuntime":true}]}]}`,
	"draft/components.json": `{"items":[{"id":"cmp_` + sampleID + `","componentTypeId":"ct_` + sampleID + `",
		"folderId":"fld_` + sampleID + `","order":"X","key":"port","name":"Port","description":` + sampleText + `,
		"imageAssetId":"ast_` + sampleID + `","values":{"atr_` + sampleID + `":"calm"}}]}`,
	"draft/nodes.json": `{"items":[{"id":"nd_` + sampleID + `","boardId":"bd_` + sampleID + `","type":"element",
		"kind":"dialogue","title":"Hello","text":` + sampleText + `,"speakerId":"cmp_` + sampleID + `",
		"coverAssetId":"ast_` + sampleID + `","color":"teal","tags":["a","b"],"x":24562.75,"y":6171.0,"width":320,
		"height":180,"isEnding":true,"targetBoardId":"bd_` + sampleID + `","targetNodeId":"nd_` + sampleID + `"}]}`,
	"draft/branch_outputs.json": `{"items":[{"id":"bo_` + sampleID + `","nodeId":"nd_` + sampleID + `","kind":"if",
		"order":"V"}]}`,
	"draft/connections.json": `{"items":[{"id":"cn_` + sampleID + `","boardId":"bd_` + sampleID + `",
		"fromNodeId":"nd_` + sampleID + `","fromOutputId":"bo_` + sampleID + `","toNodeId":"nd_` + sampleID + `",
		"order":"V","label":` + sampleText + `}]}`,
	"draft/expressions.json": `{"items":[{"ownerType":"connection","ownerId":"cn_` + sampleID + `","field":"condition",
		"source":"gold > 1"}]}`,
	"draft/validation_mutes.json": `{"items":[{"ownerType":"connection","ownerId":"cn_` + sampleID + `",
		"code":"choice_duplicate_label","reason":"Meant so","mutedBy":"usr_` + sampleID + `"}]}`,
	"comments.json": `{"items":[{"id":"cmt_` + sampleID + `","threadId":"cmt_` + sampleID + `","parentId":"cmt_` + sampleID + `",
		"targetType":"variable","targetId":"var_` + sampleID + `","body":` + sampleText + `,"deleted":true,
		"authorId":"usr_` + sampleID + `","createdAt":"` + sampleTime + `","editedAt":"` + sampleTime + `",
		"resolvedAt":"` + sampleTime + `","resolvedBy":"usr_` + sampleID + `"}]}`,
	"play_states.json": `{"items":[{"id":"pst_` + sampleID + `","name":"Save","state":{"cursor":{"phase":"ended"},
		"visits":{"nd_` + sampleID + `":1},"stateVersion":1},"names":{},"createdBy":"usr_` + sampleID + `",
		"createdAt":"` + sampleTime + `"}]}`,
	"annotations.json": `{"items":[{"id":"ann_1","boardId":"bd_` + sampleID + `","kind":"note",
		"geometry":{"x":1.5,"y":2,"width":3,"height":4},"text":` + sampleText + `}]}`,
	"releases/index.json": `{"items":[{"id":"snp_` + sampleID + `","version":"1.0.0","note":"First","acknowledgedSeq":203,
		"acknowledgedWarnings":[],"schemaVersion":1,"contentHash":"sha256:` + sampleHash + `","sourceSeq":203,
		"counts":{"nodes":1,"boards":1,"variables":2,"components":0,"connections":0},
		"validationReport":{"seq":203,"findings":[]},"publishedBy":"usr_` + sampleID + `","publishedAt":"` + sampleTime + `",
		"builtAt":"` + sampleTime + `","path":"releases/snp_` + sampleID + `/"}]}`,
	"assets/index.json": `{"items":[{"id":"ast_` + sampleID + `","path":"assets/` + sampleID + `.png","sha256":"` + sampleHash + `",
		"mime":"image/png","bytes":90,"width":8,"height":8}]}`,
	"people.json": `{"items":[{"id":"usr_` + sampleID + `","displayName":"A Person"}]}`,
}

// checkedFile is a file of one schema, as the validator that the import and
// the build use reads it, and as the plain validator does.
type checkedFile struct {
	name  string
	plain *jsonschema.Schema
	// fast is the check under test.
	fast func([]byte) error
	// root is the type that an import decodes the file as, which a file of a
	// release has none of.
	root reflect.Type
}

// differential compares the verdicts of the check under test and the plain
// validator.
type differential struct {
	t         *testing.T
	rng       *rand.Rand
	rate      float64
	values    []any
	documents int
	mutants   int
	accepted  int
	refused   int
	parsers   int
	problems  []string
}

// problem records a disagreement, of which the first few are reported.
func (d *differential) problem(format string, args ...any) {
	if len(d.problems) < 12 {
		d.problems = append(d.problems, fmt.Sprintf(format, args...))
	}
}

// compare runs both validators over the document, as its text, and holds an
// accepted file to what an import then decodes of it.
func (d *differential) compare(f *checkedFile, where string, data []byte) {
	d.mutants++
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		d.t.Fatalf("%s %s: %v", f.name, where, err)
	}
	plain := f.plain.Validate(inst) == nil
	fast := f.fast(data) == nil
	if plain {
		d.accepted++
	} else {
		d.refused++
	}
	if plain != fast {
		d.problem("%s, %s: the full validator says %v, the fail-fast check %v: %.240s", f.name, where, plain, fast, data)
	}
	if fast {
		d.sameRead(f, where, data, inst)
	}
}

// sameRead checks that what an import decodes of an accepted file is what the
// validator saw of it, as a decoder can read a key that a file holds twice
// otherwise than the tree that was validated.
func (d *differential) sameRead(f *checkedFile, where string, data []byte, inst any) {
	if f.root == nil {
		return
	}
	d.parsers++
	seen, err := json.Marshal(inst)
	if err != nil {
		d.t.Fatal(err)
	}
	imported, err := canonicalAs(f.root, data)
	if err != nil {
		// The import refuses it as it stores the file.
		return
	}
	validated, err := canonicalAs(f.root, seen)
	if err != nil {
		d.problem("%s, %s: the import reads what the validated tree cannot be read as: %v: %.240s", f.name, where, err, data)
		return
	}
	if !reflect.DeepEqual(imported, validated) {
		d.problem("%s, %s: the import reads other than what was validated: %.240s", f.name, where, data)
	}
}

// canonicalAs returns the file decoded as root, written back, as a tree.
func canonicalAs(root reflect.Type, data []byte) (any, error) {
	v := reflect.New(root)
	if err := json.Unmarshal(data, v.Interface()); err != nil {
		return nil, err
	}
	out, err := json.Marshal(v.Interface())
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	var tree any
	err = dec.Decode(&tree)
	return tree, err
}

// keep says whether a mutation is among those that this run takes.
func (d *differential) keep() bool { return d.rate >= 1 || d.rng.Float64() < d.rate }

// document runs every kind of mutation over one valid file.
func (d *differential) document(f *checkedFile, data []byte) {
	d.documents++
	if err := f.fast(data); err != nil {
		d.problem("%s: a valid document is refused: %v", f.name, err)
		return
	}
	d.compare(f, "as it is", data)
	root, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		d.t.Fatal(err)
	}
	visit := func(where string) {
		out, err := json.Marshal(root)
		if err != nil {
			d.t.Fatal(err)
		}
		d.compare(f, where, out)
	}
	d.walk(root, func(v any) { root = v }, "", visit)
	d.compound(f, data)
	d.duplicates(f, data)
	d.spellings(f, data)
}

// walk applies each replacement, removal, and addition at each place of v.
func (d *differential) walk(v any, set func(any), where string, visit func(string)) {
	for _, r := range d.values {
		if d.keep() {
			set(r)
			visit(fmt.Sprintf("%s = %v", where, r))
			set(v)
		}
	}
	switch x := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			old := x[k]
			if d.keep() {
				delete(x, k)
				visit(where + "/" + k + " removed")
				x[k] = old
			}
			if upper := strings.ToUpper(k); upper != k && d.keep() {
				delete(x, k)
				x[upper] = old
				visit(where + "/" + k + " in capitals")
				delete(x, upper)
				x[k] = old
			}
			d.walk(old, func(n any) { x[k] = n }, where+"/"+k, visit)
		}
		if d.keep() {
			x["later"] = map[string]any{"a": []any{json.Number("1"), "b", true}}
			visit(where + " with a key of a later version")
			delete(x, "later")
		}
	case []any:
		for i, e := range x {
			if d.keep() {
				set(slices.Delete(slices.Clone(x), i, i+1))
				visit(fmt.Sprintf("%s/%d removed", where, i))
				set(x)
			}
			if d.keep() {
				set(slices.Insert(slices.Clone(x), i, e))
				visit(fmt.Sprintf("%s/%d twice", where, i))
				set(x)
			}
			d.walk(e, func(n any) { x[i] = n }, fmt.Sprintf("%s/%d", where, i), visit)
		}
		if d.keep() {
			set([]any{})
			visit(where + " emptied")
			set(x)
		}
	}
}

// compound replaces a few random places at once, as a check that stops at the
// first failure sees several.
func (d *differential) compound(f *checkedFile, data []byte) {
	rounds := int(200 * min(d.rate*25, 1))
	for range rounds {
		root, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			d.t.Fatal(err)
		}
		for range 2 + d.rng.IntN(3) {
			root = d.replaceOne(root)
		}
		out, err := json.Marshal(root)
		if err != nil {
			d.t.Fatal(err)
		}
		d.compare(f, "several places", out)
	}
}

// replaceOne replaces a random place of v, stopping at each level by chance.
func (d *differential) replaceOne(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if len(x) > 0 && d.rng.IntN(5) > 0 {
			keys := slices.Sorted(maps.Keys(x))
			k := keys[d.rng.IntN(len(keys))]
			x[k] = d.replaceOne(x[k])
			return x
		}
	case []any:
		if len(x) > 0 && d.rng.IntN(5) > 0 {
			i := d.rng.IntN(len(x))
			x[i] = d.replaceOne(x[i])
			return x
		}
	}
	return cloned(d.values[d.rng.IntN(len(d.values))])
}

// cloned returns a copy of the JSON tree v, which a later replacement may
// change in place.
func cloned(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = cloned(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloned(e)
		}
		return out
	}
	return v
}

// twice is a member that a text holds two of.
type twice struct{ first, last any }

// writeJSON writes v, with a twice as both of its values under its key.
func writeJSON(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case map[string]any:
		b.WriteByte('{')
		for i, k := range slices.Sorted(maps.Keys(x)) {
			if i > 0 {
				b.WriteByte(',')
			}
			key, err := json.Marshal(k)
			if err != nil {
				return err
			}
			values := []any{x[k]}
			if t, ok := x[k].(twice); ok {
				values = []any{t.first, t.last}
			}
			for j, value := range values {
				if j > 0 {
					b.WriteByte(',')
				}
				b.Write(key)
				b.WriteByte(':')
				if err := writeJSON(b, value); err != nil {
					return err
				}
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSON(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	default:
		out, err := json.Marshal(x)
		if err != nil {
			return err
		}
		b.Write(out)
	}
	return nil
}

// duplicates holds each member twice, the first copy of an object or an array
// with more than the last: the last is what a tree holds, and a decoder into a
// struct, a map, or a slice keeps what the first has and the last lacks, so
// that a file of an archive that does so is refused.
func (d *differential) duplicates(f *checkedFile, data []byte) {
	root, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		d.t.Fatal(err)
	}
	try := func(where string) {
		var b bytes.Buffer
		if err := writeJSON(&b, root); err != nil {
			d.t.Fatal(err)
		}
		// A consumer of a release reads the last of two, as the validator does.
		if f.root == nil {
			d.compare(f, where, b.Bytes())
			return
		}
		d.mutants++
		if f.fast(b.Bytes()) == nil {
			d.problem("%s, %s: the check takes a file that holds a key twice: %.240s", f.name, where, b.Bytes())
		}
	}
	var walk func(v any, where string)
	walk = func(v any, where string) {
		switch x := v.(type) {
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(x)) {
				old := x[k]
				here := where + "/" + k
				x[k] = twice{first: "first of two", last: old}
				try(here + " twice")
				for _, less := range lessened(old) {
					if d.keep() {
						x[k] = twice{first: old, last: less}
						try(here + " twice, the last with less")
					}
				}
				x[k] = old
				walk(old, here)
			}
		case []any:
			for i, e := range x {
				walk(e, fmt.Sprintf("%s/%d", where, i))
			}
		}
	}
	walk(root, "")
}

// lessened returns copies of v, an object or an array of objects, each without
// one member.
func lessened(v any) []any {
	var out []any
	switch x := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			c := maps.Clone(x)
			delete(c, k)
			out = append(out, c)
		}
	case []any:
		if len(x) > 0 {
			for _, less := range lessened(x[0]) {
				c := slices.Clone(x)
				c[0] = less
				out = append(out, c)
			}
		}
	}
	return out
}

// spellings holds the check of a section's file to JSON's own freedoms: white
// space, each truncation of the text, and what a key may be spelled as.
func (d *differential) spellings(f *checkedFile, data []byte) {
	var indented bytes.Buffer
	if err := json.Indent(&indented, data, " \t", "\r\n  "); err != nil {
		d.t.Fatal(err)
	}
	d.compare(f, "indented", indented.Bytes())
	d.compare(f, "inside white space", []byte(" \r\n\t"+string(data)+"\n\t "))
	step := max(1, len(data)/400)
	for i := 0; i < len(data)-1; i += step {
		d.mutants++
		if f.fast(data[:i]) == nil {
			d.problem("%s: the check takes the document cut at %d: %.120s", f.name, i, data[:i])
		}
	}
	if !listFile(f) {
		return
	}
	body := bytes.TrimSpace(data)
	inner := body[bytes.IndexByte(body, ':')+1 : len(body)-1]
	escaped := `"` + `\` + `u0069tems"`
	for _, variant := range []string{
		`{"items":` + string(inner) + `,"items":` + string(inner) + `}`,
		`{"items":[],"items":` + string(inner) + `}`,
		`{"items":` + string(inner) + `,"items":[]}`,
		`{"ITEMS":` + string(inner) + `}`,
		`{"Items":` + string(inner) + `}`,
		`{` + escaped + `:` + string(inner) + `}`,
		`{"before":1,"items":` + string(inner) + `}`,
		`{"items":` + string(inner) + `,"after":1}`,
	} {
		d.mutants++
		if f.fast([]byte(variant)) == nil {
			d.problem("%s: the check takes a file whose one key is not items, exactly: %.120s", f.name, variant)
		}
	}
}

// listFile says whether the file is a section's list of items.
func listFile(f *checkedFile) bool {
	return f.root != nil && f.root.Kind() == reflect.Struct && f.root.NumField() == 1 &&
		f.root.Field(0).Name == "Items"
}

// compileArchivePlain compiles the format's schema with no rewriting.
func compileArchivePlain(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Schema()))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(SchemaID, doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]*jsonschema.Schema{}
	paths := []string{ManifestPath}
	for _, s := range sections {
		paths = append(paths, s.Path)
	}
	for _, p := range paths {
		if out[p], err = c.Compile(SchemaID + "#/properties/" + strings.ReplaceAll(p, "/", "~1")); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// publishedDocs returns the documents of the published format's version 1.
func publishedDocs(t *testing.T) map[string][]byte {
	t.Helper()
	docs, err := published.Files(false)
	if err != nil {
		t.Fatal(err)
	}
	for p := range docs {
		if path.Dir(p) != published.PublishedDir {
			delete(docs, p)
		}
	}
	return docs
}

// compilePublishedPlain compiles the published format's schemas with no
// rewriting, by the names of their files.
func compilePublishedPlain(t *testing.T, docs map[string][]byte) map[string]*jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	for p, data := range docs {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource(schemagen.BaseURL+p, doc); err != nil {
			t.Fatal(err)
		}
	}
	out := map[string]*jsonschema.Schema{}
	for _, name := range []string{"storycreep", "settings", "variables", "component_types", "components", "board"} {
		s, err := c.Compile(schemagen.BaseURL + published.PublishedDir + "/" + name + ".schema.json")
		if err != nil {
			t.Fatal(err)
		}
		out[name] = s
	}
	return out
}

// publishedSchemaName returns the name of the schema of a snapshot's file.
func publishedSchemaName(p string) string {
	switch {
	case p == published.ManifestPath:
		return "storycreep"
	case path.Dir(p) == "boards":
		return "board"
	}
	return strings.TrimSuffix(path.Base(p), ".json")
}

// mutationValues returns what the mutations put in a place: values of each
// JSON type, and those at and past the bounds of the schemas, whose constants
// and enums select the branches of a oneOf.
func mutationValues(t *testing.T, schemas ...[]byte) []any {
	t.Helper()
	seen := map[string]any{}
	add := func(v any) {
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		seen[string(out)] = v
	}
	for _, v := range []any{nil, true, false, json.Number("0"), json.Number("-1"), json.Number("1"), json.Number("1.5"),
		json.Number("1e300"), json.Number("9007199254740993"), "", "x", " ", []any{}, map[string]any{}, []any{nil},
		[]any{"x"}, []any{json.Number("1")}, map[string]any{"a": nil}, map[string]any{"a": json.Number("1")},
		map[string]any{"kind": "x"}, map[string]any{"type": "x"}} {
		add(v)
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for k, e := range x {
				switch k {
				case "const":
					add(e)
				case "enum":
					if list, ok := e.([]any); ok {
						for _, o := range list {
							add(o)
						}
					}
				case "minimum", "maximum":
					if n, ok := e.(json.Number); ok {
						if f, err := n.Float64(); err == nil {
							add(json.Number(strconv.FormatFloat(f-1, 'f', -1, 64)))
							add(n)
							add(json.Number(strconv.FormatFloat(f+1, 'f', -1, 64)))
						}
					}
				case "maxLength", "minLength":
					if n, ok := e.(json.Number); ok {
						if size, err := n.Int64(); err == nil && size <= 1024 {
							add(strings.Repeat("x", int(size)))
							add(strings.Repeat("x", int(size)+1))
							if size > 0 {
								add(strings.Repeat("x", int(size)-1))
							}
						}
					}
				}
				walk(e)
			}
		}
	}
	for _, data := range schemas {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		walk(doc)
	}
	out := make([]any, 0, len(seen))
	for _, k := range slices.Sorted(maps.Keys(seen)) {
		out = append(out, seen[k])
	}
	return out
}

// differentialFiles returns how each file of an archive and of a release is
// checked.
func differentialFiles(t *testing.T) (archive map[string]*checkedFile, release func(string) *checkedFile,
	values []any) {
	t.Helper()
	plain := compileArchivePlain(t)
	archive = map[string]*checkedFile{ManifestPath: {name: ManifestPath, plain: plain[ManifestPath],
		root: reflect.TypeFor[Manifest](),
		fast: func(data []byte) error { return checkSchema(ManifestPath, "manifest", data) }}}
	for _, s := range sections {
		archive[s.Path] = &checkedFile{name: s.Path, plain: plain[s.Path], root: s.root,
			fast: func(data []byte) error { return checkSchema(s.Path, s.Name, data) }}
	}
	docs := publishedDocs(t)
	plainPublished := compilePublishedPlain(t, docs)
	fast, err := schemaversion.NewSchemas(published.PublishedDir, docs)
	if err != nil {
		t.Fatal(err)
	}
	release = func(p string) *checkedFile {
		return &checkedFile{name: "release " + p, plain: plainPublished[publishedSchemaName(p)],
			fast: func(data []byte) error { return fast.Check(schemaversion.Files{p: data}) }}
	}
	all := [][]byte{Schema()}
	for _, data := range docs {
		all = append(all, data)
	}
	return archive, release, mutationValues(t, all...)
}

// report fails the test on any disagreement, and says how much was compared.
func (d *differential) report() {
	d.t.Helper()
	d.t.Logf("%d documents, %d mutants: the full validator accepted %d and refused %d, %d read back by the import's decoder",
		d.documents, d.mutants, d.accepted, d.refused, d.parsers)
	for _, p := range d.problems {
		d.t.Error(p)
	}
	if d.accepted == 0 || d.refused == 0 {
		d.t.Errorf("the mutants were accepted %d times and refused %d: the comparison shows nothing", d.accepted, d.refused)
	}
}

// TestFailFastAgreesWithTheFullValidator checks that the fail-fast schema
// checks of an archive's files and of a release's take and refuse what the
// plain validator does: over a valid file of each section and the fixture of
// the published format, each mutated at its places by replacing, removing,
// doubling, and adding values, those at the schemas' bounds and the constants
// that select a oneOf's branch among them. It also checks that what an import
// decodes of a file that it accepts is what the validator saw, and that it
// refuses a file of an archive that holds a key twice, however it spells it.
//
// Verifies: NFR-SEC-13
func TestFailFastAgreesWithTheFullValidator(t *testing.T) {
	t.Parallel()
	archive, release, values := differentialFiles(t)
	d := &differential{t: t, rng: rand.New(rand.NewPCG(84, 12)), rate: differentialRate, values: values}
	for _, p := range slices.Sorted(maps.Keys(differentialSamples)) {
		f, ok := archive[p]
		if !ok {
			t.Fatalf("no section is at %s", p)
		}
		d.document(f, []byte(differentialSamples[p]))
	}
	for _, s := range sections {
		if _, ok := differentialSamples[s.Path]; !ok {
			t.Errorf("the section %s has no sample", s.Name)
		}
	}
	base := filepath.Join("..", "..", "..", "schema", "fixtures", "v1")
	err := filepath.WalkDir(base, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		d.document(release(filepath.ToSlash(rel)), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d.report()
}
