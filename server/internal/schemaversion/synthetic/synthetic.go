//go:build synthetic_schema

// Package synthetic is the test-only synthetic version of the published
// game format (PRD FR-DATA-03, §6.4, plan note N2, N84), which exercises the
// machinery of package schemaversion while version 1 is the only real one:
// its number is one above the newest real version's, Number, and it differs
// from that version only in what its schemas take, not in what a runtime
// does with a snapshot, so that a runtime reads its files as it reads the
// real version's, and each implementation's registration of it is its number
// and its schemas:
//
//   - a node's position is in whole units of the canvas, an integer, where
//     version 1's is to hundredths, so that the migration to it rounds each
//     position that is not whole, half away from zero, and reports each one
//     that it rounded (FR-DATA-03 AC2);
//   - a node's title holds at most 400 characters, where version 1's holds
//     200, so that the renderer down to version 1 fails, naming each node,
//     on a title longer than 200 characters, which version 1 cannot express
//     (AC3).
//
// Importing it registers the version (schemaversion.Register), and it
// exists only with the synthetic_schema build tag, which no release build
// sets, so that a release reads the real versions alone. The repository's
// testdata/synthetic-schema, outside /schema, holds its JSON Schemas, which
// Documents generates, its fixture, which is the migration of version 1's
// fixture, and the registration hook of the TypeScript tests;
// testdata/synthetic-schema/README.md describes it.
package synthetic

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Number is the synthetic version's schemaVersion: one above the newest real
// version, the version of package published's types.
const Number = published.SchemaVersion + 1

// Dir is the directory of the synthetic version's files, from the
// repository's root: its JSON Schemas in published/v<Number>, as
// /schema/published holds a real version's, its fixture in
// fixtures/v<Number>, as /schema/fixtures holds a real version's, and the
// TypeScript tests' registration hook.
const Dir = "testdata/synthetic-schema"

// SchemaDir and FixtureDir are the directories, from the repository's root,
// of the synthetic version's JSON Schemas and of its fixture.
var (
	SchemaDir  = fmt.Sprintf("%s/published/v%d", Dir, Number)
	FixtureDir = fmt.Sprintf("%s/fixtures/v%d", Dir, Number)
)

// The longest title of a node, in characters, that the version before holds,
// and that the synthetic version holds.
const (
	previousTitleMax = 200
	titleMax         = 400
)

func init() {
	schemaversion.Register(schemaversion.Version{Number: Number, Validate: validate, Up: up, Down: down})
}

// isBoard reports whether the file at p is a board's.
func isBoard(p string) bool {
	return path.Dir(p) == "boards"
}

// nodesOf returns the nodes of a board's file, each a JSON object.
func nodesOf(p string, file map[string]any) ([]map[string]any, error) {
	list, ok := file["nodes"].([]any)
	if !ok {
		return nil, fmt.Errorf("%s has no nodes", p)
	}
	nodes := make([]map[string]any, len(list))
	for i, n := range list {
		if nodes[i], ok = n.(map[string]any); !ok {
			return nil, fmt.Errorf("%s: node %d is no object", p, i)
		}
	}
	return nodes, nil
}

// idOf returns the ID of an entity of a file.
func idOf(p string, entity map[string]any) (string, error) {
	id, ok := entity["id"].(string)
	if !ok {
		return "", fmt.Errorf("%s: an entity has no ID", p)
	}
	return id, nil
}

// up migrates a snapshot's files of the version before to the synthetic
// version: every file of the synthetic version's number (Relabel), and each
// node's position rounded to whole units, half away from zero, each that was
// not whole reported, with its position before and after.
func up(f schemaversion.Files) (schemaversion.Files, []schemaversion.Entry, error) {
	relabeled, err := schemaversion.Relabel(f, Number)
	if err != nil {
		return nil, nil, err
	}
	var entries []schemaversion.Entry
	out, err := schemaversion.Edit(relabeled, func(p string, file map[string]any) error {
		if !isBoard(p) {
			return nil
		}
		nodes, err := nodesOf(p, file)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			id, err := idOf(p, n)
			if err != nil {
				return err
			}
			pos, ok := n["position"].(map[string]any)
			if !ok {
				return fmt.Errorf("%s: the node %s has no position", p, id)
			}
			before := maps.Clone(pos)
			moved := false
			for _, axis := range []string{"x", "y"} {
				num, ok := pos[axis].(json.Number)
				if !ok {
					return fmt.Errorf("%s: the node %s's position has no %s", p, id, axis)
				}
				x, err := num.Float64()
				if err != nil {
					return fmt.Errorf("%s: the node %s's position: %w", p, id, err)
				}
				whole := math.Round(x)
				if whole != x {
					moved = true
				}
				if whole == 0 {
					// math.Round(-0.4) is -0, which a file writes 0 (§6.2).
					whole = 0
				}
				pos[axis] = json.Number(strconv.FormatFloat(whole, 'f', -1, 64))
			}
			if !moved {
				continue
			}
			b, err := json.Marshal(before)
			if err != nil {
				return err
			}
			a, err := json.Marshal(pos)
			if err != nil {
				return err
			}
			entries = append(entries, schemaversion.Entry{File: p, Entity: id, Field: "position", Before: b, After: a})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sealed, err := schemaversion.Seal(out)
	if err != nil {
		return nil, nil, err
	}
	return sealed, entries, nil
}

// down renders a snapshot's files of the synthetic version as the version
// before: every file of that version's number (Relabel), which takes every
// whole position, or fails with an *UnrepresentableError naming each node
// whose title is longer than that version holds.
func down(f schemaversion.Files) (schemaversion.Files, error) {
	relabeled, err := schemaversion.Relabel(f, Number-1)
	if err != nil {
		return nil, err
	}
	var long []schemaversion.Construct
	out, err := schemaversion.Edit(relabeled, func(p string, file map[string]any) error {
		if !isBoard(p) {
			return nil
		}
		nodes, err := nodesOf(p, file)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			id, err := idOf(p, n)
			if err != nil {
				return err
			}
			title, _ := n["title"].(string)
			if chars := utf8.RuneCountInString(title); chars > previousTitleMax {
				long = append(long, schemaversion.Construct{File: p, Entity: id, Field: "title",
					Why: fmt.Sprintf("a title of %d characters, of which version %d holds at most %d", chars, Number-1,
						previousTitleMax)})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(long) > 0 {
		return nil, &schemaversion.UnrepresentableError{From: Number, To: Number - 1, Constructs: long}
	}
	return schemaversion.Seal(out)
}

// validate checks a snapshot's files of the synthetic version as a consumer
// of it does: each file against its JSON Schema, the manifest's hashes, and
// the snapshot as the reference runtime of the version before loads it once
// its files are of that version's number, since the synthetic version
// differs from it only in what its schemas take.
func validate(f schemaversion.Files) error {
	s, err := schemas()
	if err != nil {
		return err
	}
	if err := s.Check(f); err != nil {
		return err
	}
	if err := schemaversion.Sealed(f); err != nil {
		return err
	}
	relabeled, err := schemaversion.Relabel(f, Number-1)
	if err != nil {
		return err
	}
	sealed, err := schemaversion.Seal(relabeled)
	if err != nil {
		return err
	}
	return schemaversion.RuntimeLoads(sealed)
}

// schemas returns the synthetic version's JSON Schemas, compiled.
var schemas = sync.OnceValues(func() (*schemaversion.Schemas, error) {
	docs, err := Documents()
	if err != nil {
		return nil, err
	}
	return schemaversion.NewSchemas(SchemaDir, docs)
})

// Documents returns the JSON Schema documents of the synthetic version, by
// their paths from the repository's root, in SchemaDir, as
// testdata/synthetic-schema holds them: each document of the version before,
// as package published generates it, but for its $id, which is the URL of
// its path, its title and description, which name the synthetic version, its
// examples, which are of the version before and are left out, every
// schemaVersion, which is Number, each position, whose x and y are integers,
// and each node's title, of at most 400 characters. It fails where a
// document of the version before is not as it expects, so that a change of
// package published's types that the synthetic version's schemas do not
// follow fails its tests.
func Documents() (map[string][]byte, error) {
	files, err := published.Files(false)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	positions, titles := 0, 0
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if path.Dir(p) != published.PublishedDir {
			continue
		}
		tree, err := value.ReadJSON(files[p])
		if err != nil {
			return nil, fmt.Errorf("synthetic: %s: %w", p, err)
		}
		doc, ok := tree.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("synthetic: %s is no object", p)
		}
		name := path.Base(p)
		target := SchemaDir + "/" + name
		doc["$id"] = schemagen.BaseURL + target
		title, _ := doc["title"].(string)
		prefix := fmt.Sprintf("Storycreep published format, version %d: ", Number-1)
		if !strings.HasPrefix(title, prefix) {
			return nil, fmt.Errorf("synthetic: %s's title does not start with %q", p, prefix)
		}
		doc["title"] = fmt.Sprintf("Storycreep published format, synthetic version %d: %s", Number,
			strings.TrimPrefix(title, prefix))
		description, _ := doc["description"].(string)
		const formats = "schema/published/README.md describes the format."
		if !strings.HasSuffix(description, formats) {
			return nil, fmt.Errorf("synthetic: %s's description does not end with %q", p, formats)
		}
		doc["description"] = strings.TrimSuffix(description, formats) + fmt.Sprintf("This is the test-only synthetic "+
			"version %d of the format, which only test builds read; %s/README.md describes it.", Number, Dir)
		delete(doc, "examples")
		defs, ok := doc["$defs"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("synthetic: %s has no $defs", p)
		}
		for _, name := range slices.Sorted(maps.Keys(defs)) {
			def, _ := defs[name].(map[string]any)
			props, _ := def["properties"].(map[string]any)
			if sv, ok := props["schemaVersion"].(map[string]any); ok {
				if sv["const"] != json.Number(strconv.Itoa(Number-1)) {
					return nil, fmt.Errorf("synthetic: %s: %s's schemaVersion is not the constant %d", p, name,
						Number-1)
				}
				sv["const"] = json.Number(strconv.Itoa(Number))
				sv["description"] = fmt.Sprintf("The version of the published format, %d.", Number)
			}
			switch name {
			case "Position":
				for _, axis := range []string{"x", "y"} {
					a, _ := props[axis].(map[string]any)
					if a["type"] != "number" {
						return nil, fmt.Errorf("synthetic: %s: the position's %s is not a number", p, axis)
					}
					a["type"] = "integer"
				}
				def["description"] = "Where the node is on its board's canvas, in whole units."
				positions++
			case "Element", "Branch", "Jumper":
				t, _ := props["title"].(map[string]any)
				if t["maxLength"] != json.Number(strconv.Itoa(previousTitleMax)) {
					return nil, fmt.Errorf("synthetic: %s: the %s's title does not hold at most %d characters", p,
						name, previousTitleMax)
				}
				t["maxLength"] = json.Number(strconv.Itoa(titleMax))
				titles++
			}
		}
		if out[target], err = schemaversion.Encode(doc); err != nil {
			return nil, fmt.Errorf("synthetic: %s: %w", target, err)
		}
	}
	// The board's document alone holds the nodes and their positions.
	if positions != 1 || titles != 3 {
		return nil, fmt.Errorf("synthetic: the documents hold %d positions and %d titles of nodes, and not 1 and 3",
			positions, titles)
	}
	return out, nil
}
