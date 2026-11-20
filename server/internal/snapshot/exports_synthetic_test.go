//go:build synthetic_schema

package snapshot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion/synthetic"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// The tests of the exports' criteria that need two versions of the published
// format, against the test-only synthetic version (plan note N2), with the
// synthetic_schema build tag:
//
//	go test -tags synthetic_schema ./internal/snapshot/...

// releaseFiles returns the files that the build of s, building, would write
// of the project's draft as it is, at version 1, as its release.
func (e *env) releaseFiles(t *testing.T, s snapshot.Snapshot) schemaversion.Files {
	t.Helper()
	var built *serializer.Snapshot
	err := e.db.InSnapshot(context.Background(), e.scope(), func(tx pgx.Tx) error {
		var err error
		built, err = serializer.Build(context.Background(), tx, e.workspace, e.project, serializer.Stamp{
			Generator: serializer.Generator(), At: s.PublishedAt,
			Release: &serializer.Release{ID: s.ID, Version: s.Version, Note: s.Note}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return built.Files
}

// store makes s, building, ready with the files f, of their manifest's
// version, as the superuser, as a build that wrote that version would: the
// stand-in of a snapshot stored at a version newer than the one that the
// serializer writes, as one is once a real version supersedes it.
func (e *env) store(t *testing.T, s snapshot.Snapshot, f schemaversion.Files) {
	t.Helper()
	ctx := context.Background()
	admin := e.d.Admin(t)
	id := snapshotID(t, s)
	for p, data := range f {
		if _, err := admin.Exec(ctx, `INSERT INTO snapshot_file (id, workspace_id, snapshot_id, path, sha256, bytes)
			VALUES ($1, $2, $3, $4, $5, $6)`, ids.New(), e.workspace, id, p, string(published.HashOf(data)),
			data); err != nil {
			t.Fatal(err)
		}
	}
	var m struct {
		SchemaVersion int `json:"schemaVersion"`
		Snapshot      struct {
			ContentHash string `json:"contentHash"`
		} `json:"snapshot"`
		Counts json.RawMessage `json:"counts"`
	}
	if err := json.Unmarshal(f[published.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE snapshot SET status = 'ready', source_seq = acknowledged_seq,
		validation_report = '{"seq": 0, "findings": []}', schema_version = $2, content_hash = $3, counts = $4,
		built_at = now() WHERE id = $1`, id, m.SchemaVersion, m.Snapshot.ContentHash, m.Counts); err != nil {
		t.Fatal(err)
	}
}

// TestExportOlderVersion checks the exports at a version of the published
// format other than the files' own, with the synthetic version beside
// version 1: a snapshot stored at the synthetic version is exported at it
// without schema (PRD FR-SYNC-01 AC1), and at version 1 rendered down by the
// synthetic version's renderer, as files that validate against version 1,
// which are those that version 1's build writes of the same draft, whose
// positions are whole (AC2); a node's title that version 1 cannot hold
// refuses the export at version 1 with an *UnrepresentableError that names
// the node (PRD §6.4); and the draft, which the serializer writes at version
// 1, is exported at the synthetic version migrated forward, as files that
// validate against it, marked as the draft's.
//
// Verifies: FR-SYNC-01/AC1, FR-SYNC-01/AC2
func TestExportOlderVersion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	story := e.newStory(t)
	ctx := context.Background()

	s := e.mustPublish(t, "1.0.0")
	v1 := e.releaseFiles(t, s)
	v2, _, err := schemaversion.Migrate(v1, synthetic.Number)
	if err != nil {
		t.Fatal(err)
	}
	e.store(t, s, v2)
	native, err := e.snapshots.Export(ctx, e.actor(), e.project, snapshotID(t, s), 0)
	if err != nil {
		t.Fatal(err)
	}
	if native.SchemaVersion != synthetic.Number || !maps.EqualFunc(native.Files, v2, bytes.Equal) {
		t.Errorf("the export without schema is of version %d, want the stored files of version %d",
			native.SchemaVersion, synthetic.Number)
	}
	older, err := e.snapshots.Export(ctx, e.actor(), e.project, snapshotID(t, s), 1)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := schemaversion.VersionOf(older.Files); err != nil || v != 1 || older.SchemaVersion != 1 {
		t.Errorf("the export at version 1 is of version %d (%v)", v, err)
	}
	if err := schemaversion.Validate(older.Files); err != nil {
		t.Errorf("the export at version 1 does not validate against it: %v", err)
	}
	if !maps.EqualFunc(older.Files, v1, bytes.Equal) {
		t.Error("the export at version 1 is not what version 1's build writes of the same draft")
	}

	// The synthetic version holds a node's title of 400 characters, and
	// version 1 one of 200.
	long := e.mustPublish(t, "1.0.1")
	edited, err := schemaversion.Edit(v2, func(p string, file map[string]any) error {
		if path.Dir(p) != "boards" {
			return nil
		}
		for _, n := range file["nodes"].([]any) {
			if node := n.(map[string]any); node["id"] == story.start {
				node["title"] = strings.Repeat("Quay ", 60)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited, err = schemaversion.Seal(edited); err != nil {
		t.Fatal(err)
	}
	e.store(t, long, edited)
	_, err = e.snapshots.Export(ctx, e.actor(), e.project, snapshotID(t, long), 1)
	var unrepresentable *schemaversion.UnrepresentableError
	if !errors.As(err, &unrepresentable) || unrepresentable.To != 1 || len(unrepresentable.Constructs) != 1 ||
		unrepresentable.Constructs[0].Entity != story.start || unrepresentable.Constructs[0].Field != "title" {
		t.Errorf("the export at version 1 of a title of 300 characters: %v, want an *UnrepresentableError of "+
			"the node's title", err)
	}

	draft, err := e.snapshots.ExportDraft(ctx, e.actor(), e.project, synthetic.Number)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := schemaversion.VersionOf(draft.Files); err != nil || v != synthetic.Number ||
		draft.SchemaVersion != synthetic.Number {
		t.Errorf("the draft's export at version %d is of version %d (%v)", synthetic.Number, v, err)
	}
	if err := schemaversion.Validate(draft.Files); err != nil {
		t.Errorf("the draft's export at version %d does not validate against it: %v", synthetic.Number, err)
	}
	var m struct {
		Snapshot map[string]any `json:"snapshot"`
	}
	if err := json.Unmarshal(draft.Files[published.ManifestPath], &m); err != nil || m.Snapshot["channel"] !=
		"draft" {
		t.Errorf("the draft's export at version %d says %v (%v), want the draft channel", synthetic.Number,
			m.Snapshot, err)
	}
}
