package published

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

var (
	valueType   = reflect.TypeFor[value.Value]()
	segmentType = reflect.TypeFor[richtext.Segment]()
)

// The directories of the formats' documents, from the repository's root.
const (
	// PublishedDir holds the documents of the published format's version
	// SchemaVersion.
	PublishedDir = "schema/published/v1"
	// SaveStateDir holds the document of the save-state format's version
	// StateVersion.
	SaveStateDir = "schema/save-state/v1"
)

// unions are the formats' unions, each an interface whose variants the value
// of its key tells apart, which Generator declares (schemagen.Generator.Union).
var unions = []struct {
	iface    reflect.Type
	key, doc string
	variants []schemagen.Variant
}{
	{reflect.TypeFor[Node](), "type", "A node of a board: an element, a branch, or a jumper, which its type tells " +
		"(FR-EDIT-02).", []schemagen.Variant{
		{Value: NodeElement, Type: reflect.TypeFor[*Element]()},
		{Value: NodeBranch, Type: reflect.TypeFor[*Branch]()},
		{Value: NodeJumper, Type: reflect.TypeFor[*Jumper]()},
	}},
	{reflect.TypeFor[Snapshot](), "channel", "What the snapshot is: a published release, or the working draft as " +
		"it was when it was pulled, which its channel tells (PRD §7.8).", []schemagen.Variant{
		{Value: ChannelRelease, Type: reflect.TypeFor[*ReleaseSnapshot]()},
		{Value: ChannelDraft, Type: reflect.TypeFor[*DraftSnapshot]()},
	}},
}

// Generator returns a generator of the formats' schemas, a closed one if
// closed is set (schemagen.New), which knows the unions of the formats.
func Generator(closed bool) *schemagen.Generator {
	g := schemagen.New(closed)
	for _, u := range unions {
		g.Union(u.iface, u.key, u.doc, u.variants...)
	}
	return g
}

// Documents returns the JSON Schema documents of the published format,
// version SchemaVersion, one of each file of a snapshot and one of an
// expression's AST, and of the save-state format, version StateVersion
// (FR-DATA-04). Plan items 80 and 83 add the diff's and the authoring
// export's.
func Documents() []schemagen.Document {
	const formats = " schema/published/README.md describes the format."
	doc := func(name, title, description string, root reflect.Type, examples []string) schemagen.Document {
		return schemagen.Document{
			Path:        PublishedDir + "/" + name + ".schema.json",
			Title:       "Storycreep published format, version 1: " + title,
			Description: description + formats,
			Root:        root,
			Examples:    examples,
		}
	}
	return []schemagen.Document{
		doc("storycreep", "storycreep.json", "A snapshot's manifest, storycreep.json, at the root of the "+
			"snapshot's files (PRD §6.3, §6.5).", reflect.TypeFor[Manifest](), manifestExamples),
		doc("settings", "project/settings.json", "A snapshot's project/settings.json (PRD §6.3).",
			reflect.TypeFor[Settings](), settingsExamples),
		doc("variables", "project/variables.json", "A snapshot's project/variables.json (PRD §6.3, §6.5).",
			reflect.TypeFor[Variables](), variablesExamples),
		doc("component_types", "project/component_types.json", "A snapshot's project/component_types.json "+
			"(PRD §6.3).", reflect.TypeFor[ComponentTypes](), componentTypesExamples),
		doc("components", "project/components.json", "A snapshot's project/components.json (PRD §6.3).",
			reflect.TypeFor[Components](), componentsExamples),
		doc("board", "boards/<slug>.json", "A snapshot's file of one board, boards/<slug>.json (PRD §6.3, §6.5).",
			reflect.TypeFor[Board](), boardExamples),
		doc("ast", "an expression's AST", "The AST of an expression, a condition's or a script's, as the "+
			"published format holds it beside its source (PRD §5.8.8).", reflect.TypeFor[AST](), astExamples),
		{
			Path:  SaveStateDir + "/save-state.schema.json",
			Title: "Storycreep save state, version 1",
			Description: "The narrative state that a runtime's save_state() returns and load_state() reads (PRD " +
				"§8.5, FR-GODOT-07). schema/save-state/README.md describes the format.",
			Root:     reflect.TypeFor[SaveState](),
			Examples: saveStateExamples,
		},
	}
}

// Files returns each of the Documents, by its path, as /schema holds it, of
// a closed generator if closed is set, for tests. One generator writes them
// all, so that a definition that two of them hold is the same in both: it
// defines every document's root before it writes any document, since a
// union of definitions that one document reaches can change a definition
// that another reaches too (schemagen.Generator.Union).
func Files(closed bool) (map[string][]byte, error) {
	g := Generator(closed)
	docs := Documents()
	for _, d := range docs {
		g.Ref(d.Root)
	}
	files := map[string][]byte{}
	for _, d := range docs {
		data, err := g.Document(d)
		if err != nil {
			return nil, err
		}
		files[d.Path] = data
	}
	return files, nil
}

// The TypeScript modules of the formats, from the repository's root.
const (
	PublishedTypeScriptPath = "web/shared/src/published/v1.gen.ts"
	SaveStateTypeScriptPath = "web/shared/src/save-state/v1.gen.ts"
)

// TypeScript returns the web app's TypeScript modules of the formats, by
// their paths, generated from the documents that Files returns (FR-DATA-04
// AC2): one of the published format's documents, and one of the save state's.
func TypeScript() (map[string][]byte, error) {
	files, err := Files(false)
	if err != nil {
		return nil, err
	}
	modules := map[string][]byte{}
	for path, dir := range map[string]string{PublishedTypeScriptPath: PublishedDir, SaveStateTypeScriptPath: SaveStateDir} {
		var docs [][]byte
		for _, d := range Documents() {
			if strings.HasPrefix(d.Path, dir+"/") {
				docs = append(docs, files[d.Path])
			}
		}
		header := fmt.Sprintf("// Generated from %s/*.schema.json. Do not edit; run\n"+
			"// \"go test ./internal/published -run TestTypeScript -update\" in server/\n// instead.\n", dir)
		data, err := schemagen.TypeScript(header, docs...)
		if err != nil {
			return nil, err
		}
		modules[path] = data
	}
	return modules, nil
}

// AST is an expression's AST, a condition's or a script's, the root of the
// AST's document.
type AST struct {
	Node expr.Node
}

// JSONSchema gives the schemas the definition of an AST (schemagen.Provider).
func (AST) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return &schemagen.Schema{
		OneOf: []*schemagen.Schema{expr.ExpressionSchema(g), expr.ScriptSchema(g)},
		Description: "The AST of an expression: a condition's, an expression, or a script's, a script node, " +
			"whose names are resolved to the IDs of what they name (PRD §5.8.8).",
	}
}

// MarshalJSON writes the AST as expr.Encode writes it, a script's when its
// kind is script and a condition's otherwise, or fails on one that is not
// set, or that expr.Decode does not read back, as a file does not hold it:
// one whose names are not resolved, as expr.Parse returns it.
func (a AST) MarshalJSON() ([]byte, error) {
	ctx := expr.ContextCondition
	if a.Node != nil && a.Node.Kind() == expr.KindScript {
		ctx = expr.ContextScript
	}
	data, err := encodeAST(a.Node, ctx)
	if err != nil {
		return nil, err
	}
	if _, err := expr.Decode(data, ctx); err != nil {
		return nil, fmt.Errorf("published: the AST is not one that a file holds: %w", err)
	}
	return data, nil
}

// UnmarshalJSON reads the AST as expr.Decode reads it, a script's when its
// kind is script, and a condition's otherwise. It reads the kind by its key
// as it is written, as expr.Decode does, rather than as encoding/json reads a
// field, whatever the key's case.
func (a *AST) UnmarshalJSON(data []byte) error {
	var head map[string]json.RawMessage
	var kind string
	ctx := expr.ContextCondition
	if json.Unmarshal(data, &head) == nil && json.Unmarshal(head["kind"], &kind) == nil &&
		kind == string(expr.KindScript) {
		ctx = expr.ContextScript
	}
	n, err := expr.Decode(data, ctx)
	a.Node = n
	return err
}
