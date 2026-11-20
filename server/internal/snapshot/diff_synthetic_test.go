//go:build synthetic_schema

package snapshot_test

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/diff"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion/synthetic"
)

// TestDiffAtTheServersVersion checks, with the synthetic version beside
// version 1, that a diff compares both of its sides at the version that the
// server writes (PRD §7.4, FR-SYNC-02): a snapshot stored at the synthetic
// version, rendered down to version 1, has no change from version 1's build
// of the same draft, in either order, nor from the draft itself, and one
// whose node's title version 1 cannot hold is refused with an
// *schemaversion.UnrepresentableError that names it.
func TestDiffAtTheServersVersion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	story := e.newStory(t)
	ctx := context.Background()

	newer := e.mustPublish(t, "1.0.0")
	v1 := e.releaseFiles(t, newer)
	v2, _, err := schemaversion.Migrate(v1, synthetic.Number)
	if err != nil {
		t.Fatal(err)
	}
	e.store(t, newer, v2)
	older := e.mustPublish(t, "1.1.0")
	e.store(t, older, e.releaseFiles(t, older))
	for _, pair := range [][2]ids.UUID{{snapshotID(t, newer), snapshotID(t, older)},
		{snapshotID(t, older), snapshotID(t, newer)}, {snapshotID(t, newer), {}}} {
		d, err := e.snapshots.Diff(ctx, e.actor(), e.project, pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if d.SchemaVersion != published.SchemaVersion || d.Summary != (diff.DiffSummary{}) {
			t.Errorf("the diff of %v at version %d is %+v, want none at version %d", pair, d.SchemaVersion,
				d.Summary, published.SchemaVersion)
		}
	}

	long := e.mustPublish(t, "1.2.0")
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
	_, err = e.snapshots.Diff(ctx, e.actor(), e.project, snapshotID(t, older), snapshotID(t, long))
	var unrepresentable *schemaversion.UnrepresentableError
	if !errors.As(err, &unrepresentable) || unrepresentable.To != published.SchemaVersion ||
		len(unrepresentable.Constructs) != 1 || unrepresentable.Constructs[0].Entity != story.start {
		t.Errorf("the diff into a title of 300 characters at version 1: %v, want an *UnrepresentableError of the "+
			"node's title", err)
	}
}
