package published

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
)

var update = flag.Bool("update", false, "rewrite the published JSON Schemas and their TypeScript")

// root is the repository's root, from this package's directory.
const root = "../../.."

// TestSchemaFiles checks that /schema holds the documents that Files
// generates, so that a change of the Go types that does not regenerate them
// fails the build (FR-DATA-04); run go test ./internal/published -run
// TestSchemaFiles -update to rewrite them.
func TestSchemaFiles(t *testing.T) {
	files, err := Files(false)
	if err != nil {
		t.Fatal(err)
	}
	checkFiles(t, files, "TestSchemaFiles")
}

// examplesTypeScriptPath is the TypeScript module of the published format's
// and the save state's examples, typed by the generated types, which the
// TypeScript compiler checks against them.
const examplesTypeScriptPath = "web/shared/src/published/testdata/examples.gen.ts"

// TestTypeScript checks that the web app's TypeScript types are the ones
// that schemagen generates from the documents that /schema holds, and the
// module of the documents' examples is typed by them, so that the types that
// the web app uses are those of the schemas (FR-DATA-04 AC2), which the
// TypeScript compiler checks the examples against; run go test
// ./internal/published -run TestTypeScript -update to rewrite them.
//
// Verifies: FR-DATA-04/AC2
func TestTypeScript(t *testing.T) {
	modules, err := TypeScript()
	if err != nil {
		t.Fatal(err)
	}
	examples, err := examplesModule()
	if err != nil {
		t.Fatal(err)
	}
	modules[examplesTypeScriptPath] = examples
	checkFiles(t, modules, "TestTypeScript")
}

// examplesModule returns the TypeScript module of the documents' examples:
// for each document, an array of its examples, typed as its root's type,
// from the published module or the save state's, so that tsc fails on an
// example that the types do not take, as on a property that they do not
// have of an object literal.
func examplesModule() ([]byte, error) {
	files, err := Files(false)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("// Generated from the examples of schema/published/v1/*.schema.json and\n" +
		"// schema/save-state/v1/save-state.schema.json. Do not edit; run\n" +
		"// \"go test ./internal/published -run TestTypeScript -update\" in server/\n// instead.\n\n")
	var published, saveState []string
	type exported struct{ name, typ, path string }
	var consts []exported
	for _, d := range Documents() {
		var f struct {
			Ref      string            `json:"$ref"`
			Examples []json.RawMessage `json:"examples"`
		}
		if err := json.Unmarshal(files[d.Path], &f); err != nil {
			return nil, err
		}
		typ := schemagen.RefName(f.Ref)
		name := strings.TrimSuffix(filepath.Base(d.Path), ".schema.json")
		if strings.HasPrefix(d.Path, SaveStateDir+"/") {
			saveState = append(saveState, typ)
		} else {
			published = append(published, typ)
		}
		consts = append(consts, exported{name: camel(name) + "Examples", typ: typ, path: d.Path})
	}
	slices.Sort(published)
	slices.Sort(saveState)
	fmt.Fprintf(&b, "import type { %s } from \"../v1.gen\";\n", strings.Join(published, ", "))
	fmt.Fprintf(&b, "import type { %s } from \"../../save-state/v1.gen\";\n", strings.Join(saveState, ", "))
	for _, c := range consts {
		var f struct {
			Examples []json.RawMessage `json:"examples"`
		}
		if err := json.Unmarshal(files[c.path], &f); err != nil {
			return nil, err
		}
		var list bytes.Buffer
		list.WriteString("[")
		for i, ex := range f.Examples {
			if i > 0 {
				list.WriteString(",")
			}
			list.Write(ex)
		}
		list.WriteString("]")
		var indented bytes.Buffer
		if err := json.Indent(&indented, list.Bytes(), "", "  "); err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "\n/** The examples of %s. */\nexport const %s: Array<%s> = %s;\n", c.path, c.name, c.typ,
			indented.String())
	}
	return []byte(b.String()), nil
}

// camel returns name, whose words underscores or hyphens separate, in
// camelCase.
func camel(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })
	for i := 1; i < len(words); i++ {
		words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
	}
	return strings.Join(words, "")
}

// checkFiles checks that each file, by its path from the repository's root,
// holds what it should, or rewrites it when -update is set.
func checkFiles(t *testing.T, files map[string][]byte, test string) {
	t.Helper()
	for _, path := range slices.Sorted(maps.Keys(files)) {
		want := files[path]
		full := filepath.Join(root, filepath.FromSlash(path))
		if *update {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(full)
		if err != nil {
			t.Errorf("%s: %v; run go test ./internal/published -run %s -update", path, err, test)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not what the Go types generate; run go test ./internal/published -run %s -update",
				path, test)
		}
	}
}

// versionDir matches a document's directory, with its format's version.
var versionDir = regexp.MustCompile(`^schema/([a-z0-9-]+)/v([0-9]+)/`)

// TestSchemaIDs checks that each document's $id is the URL at which the
// repository publishes it, which names its format and the format's version,
// the published format's SchemaVersion or the save state's StateVersion, so
// that a consumer can pin a version (FR-DATA-04 AC3), and that no two
// documents have one $id.
//
// Verifies: FR-DATA-04/AC3
func TestSchemaIDs(t *testing.T) {
	files, err := Files(false)
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]int{"published": SchemaVersion, "save-state": StateVersion}
	seen := map[string]bool{}
	for _, d := range Documents() {
		var head struct {
			Schema string `json:"$schema"`
			ID     string `json:"$id"`
		}
		if err := json.Unmarshal(files[d.Path], &head); err != nil {
			t.Fatal(err)
		}
		if head.Schema != schemagen.Dialect {
			t.Errorf("%s: $schema is %q, want %q", d.Path, head.Schema, schemagen.Dialect)
		}
		if want := "https://raw.githubusercontent.com/CyberCute-Studio/storycreep/main/" + d.Path; head.ID != want {
			t.Errorf("%s: $id is %q, want %q", d.Path, head.ID, want)
		}
		m := versionDir.FindStringSubmatch(d.Path)
		if m == nil {
			t.Errorf("%s is in no directory of a format's version", d.Path)
			continue
		}
		version, ok := versions[m[1]]
		if n, _ := strconv.Atoi(m[2]); !ok || n != version {
			t.Errorf("%s: the $id names version %s of %s, which is not the version of the format's types", d.Path,
				m[2], m[1])
		}
		if seen[head.ID] {
			t.Errorf("two documents have the $id %s", head.ID)
		}
		seen[head.ID] = true
	}
}
