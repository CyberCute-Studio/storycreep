package schemaversion

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// What a migration or a renderer reads and writes of a snapshot's files: each
// JSON file as a tree, as value.ReadJSON reads it, whose numbers are
// json.Numbers as they are written (Object), and written back laid out as
// every file is (Encode), and the manifest's hashes of the files that it
// lists, computed again once the files have changed (Seal).

// Object returns the JSON object of the file at path of f, as value.ReadJSON
// reads it, or fails where f has no such file, or where it is no object.
func Object(f Files, path string) (map[string]any, error) {
	data, ok := f[path]
	if !ok {
		return nil, fmt.Errorf("schemaversion: the snapshot has no %s", path)
	}
	tree, err := value.ReadJSON(data)
	if err != nil {
		return nil, fmt.Errorf("schemaversion: %s: %w", path, err)
	}
	obj, ok := tree.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schemaversion: %s is no JSON object", path)
	}
	return obj, nil
}

// Encode returns tree, a JSON value as value.ReadJSON reads one, as a file's
// bytes, laid out as every file of the published format is (serializer.Layout,
// PRD §6.2), each number as it is written.
func Encode(tree any) ([]byte, error) {
	data, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("schemaversion: %w", err)
	}
	return serializer.Layout(data)
}

// IsJSON reports whether the file at path of a snapshot is one of its JSON
// files, the manifest or a file of the project or of a board, and not an
// asset, whose bytes no migration reads.
func IsJSON(path string) bool {
	return !strings.HasPrefix(path, "assets/")
}

// Edit returns a copy of f in which edit has changed the tree of each JSON
// file, in the order of their paths, each written back as Encode writes it,
// and every other file is as it was. The manifest's hashes are those of f's
// files until Seal computes them again.
func Edit(f Files, edit func(path string, file map[string]any) error) (Files, error) {
	out := make(Files, len(f))
	for _, path := range slices.Sorted(maps.Keys(f)) {
		if !IsJSON(path) {
			out[path] = f[path]
			continue
		}
		file, err := Object(f, path)
		if err != nil {
			return nil, err
		}
		if err := edit(path, file); err != nil {
			return nil, err
		}
		if out[path], err = Encode(file); err != nil {
			return nil, fmt.Errorf("schemaversion: %s: %w", path, err)
		}
	}
	return out, nil
}

// Relabel returns a copy of f whose every JSON file has the schemaVersion n,
// as every file of a snapshot states its version (PRD §6.1), which a
// migration and a renderer do first, and Seal last.
func Relabel(f Files, n int) (Files, error) {
	return Edit(f, func(path string, file map[string]any) error {
		if _, ok := file["schemaVersion"]; !ok {
			return fmt.Errorf("schemaversion: %s has no schemaVersion", path)
		}
		file["schemaVersion"] = json.Number(fmt.Sprint(n))
		return nil
	})
}

// Seal returns a copy of f whose manifest lists the hash of each of f's other
// files, and whose snapshot's content hash is theirs (published.ContentHash,
// §7.3), as a migration or a renderer that changed the files leaves them. It
// fails where the manifest does not list exactly f's other files, and leaves
// the rest of the manifest, its generator and its snapshot's ID, version,
// note, and moment among it, as it was: a snapshot rendered at another version
// is the same release, of the same moment.
func Seal(f Files) (Files, error) {
	m, err := Object(f, published.ManifestPath)
	if err != nil {
		return nil, err
	}
	listed, ok := m["files"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schemaversion: %s has no files", published.ManifestPath)
	}
	hashes := make(map[string]published.Hash, len(f))
	for path, data := range f {
		if path == published.ManifestPath {
			continue
		}
		if _, ok := listed[path]; !ok {
			return nil, fmt.Errorf("schemaversion: %s does not list %s", published.ManifestPath, path)
		}
		hashes[path] = published.HashOf(data)
	}
	for path := range listed {
		if _, ok := f[path]; !ok {
			return nil, fmt.Errorf("schemaversion: %s lists %s, which the snapshot does not have", published.ManifestPath,
				path)
		}
	}
	snapshot, ok := m["snapshot"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schemaversion: %s has no snapshot", published.ManifestPath)
	}
	files := make(map[string]any, len(hashes))
	for path, h := range hashes {
		files[path] = string(h)
	}
	m["files"] = files
	snapshot["contentHash"] = string(published.ContentHash(hashes))
	out := maps.Clone(f)
	if out[published.ManifestPath], err = Encode(m); err != nil {
		return nil, err
	}
	return out, nil
}

// Sealed checks that the manifest of f lists exactly f's other files, each
// with the hash of its bytes, and that its snapshot's content hash is theirs
// (§7.3), as Seal leaves them, and as a consumer checks them, whatever the
// version, of a manifest that holds them as version 1's does.
func Sealed(f Files) error {
	sealed, err := Seal(f)
	if err != nil {
		return err
	}
	m, err := Object(f, published.ManifestPath)
	if err != nil {
		return err
	}
	want, err := Object(sealed, published.ManifestPath)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(m["files"], want["files"]) {
		return fmt.Errorf("schemaversion: %s does not list each file with the hash of its bytes",
			published.ManifestPath)
	}
	got, _ := m["snapshot"].(map[string]any)
	sealedSnapshot, _ := want["snapshot"].(map[string]any)
	if got["contentHash"] != sealedSnapshot["contentHash"] {
		return fmt.Errorf("schemaversion: the snapshot's content hash is %v, and its files' %v", got["contentHash"],
			sealedSnapshot["contentHash"])
	}
	return nil
}
