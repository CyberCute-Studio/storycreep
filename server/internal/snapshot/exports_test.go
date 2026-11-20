package snapshot_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// TestZip checks that an export's zip holds a member of each of its files,
// at its path, in the order of their paths, its bytes as they are, compressed
// with Deflate, with no member of a directory, which a pull's path rules
// refuse (PRD §7.5), and that the same files make the same zip.
func TestZip(t *testing.T) {
	t.Parallel()
	e := snapshot.Export{Files: schemaversion.Files{
		"storycreep.json":           []byte("{}\n"),
		"boards/harbour-intro.json": []byte(`{"nodes": []}` + "\n"),
		"project/settings.json":     bytes.Repeat([]byte("harbour "), 1000),
	}}
	data, err := e.Zip()
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.Zip()
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("two zips of the same files differ (%v)", err)
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
		if f.FileInfo().IsDir() || f.Method != zip.Deflate {
			t.Errorf("the member %s is a directory, or not deflated", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil || !bytes.Equal(b, e.Files[f.Name]) {
			t.Errorf("the member %s holds %q (%v)", f.Name, b, err)
		}
	}
	if want := slices.Sorted(maps.Keys(e.Files)); !slices.Equal(names, want) {
		t.Errorf("the zip's members are %v, want %v", names, want)
	}
}

// TestExportRelease checks that the export of a ready snapshot at its own
// version is the files that its build stored, named after its project and
// its version (PRD FR-SYNC-01 AC1); that the export of the draft, unchanged
// since, holds the same content files and so the same content hash, with a
// manifest of the draft channel (§7.8); and what the exports refuse: a
// snapshot that is building, one that the project does not have, and a
// version that the server does not read, which is refused before anything
// is read (AC3).
//
// Verifies: FR-SYNC-01/AC1, FR-SYNC-01/AC3
func TestExportRelease(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newStory(t)
	ctx := context.Background()
	s := e.build(t, e.mustPublish(t, "0.1.0"))
	ex, err := e.snapshots.Export(ctx, e.actor(), e.project, snapshotID(t, s), 0)
	if err != nil {
		t.Fatal(err)
	}
	stored := e.files(t, s)
	if !maps.EqualFunc(map[string][]byte(ex.Files), stored, bytes.Equal) || ex.SchemaVersion != 1 ||
		ex.Name != "harbour-0.1.0.zip" {
		t.Errorf("the export is %v of version %d, named %q, want the stored files of version 1",
			slices.Sorted(maps.Keys(ex.Files)), ex.SchemaVersion, ex.Name)
	}

	draft, err := e.snapshots.ExportDraft(ctx, e.actor(), e.project, 0)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Name != "harbour-draft.zip" || draft.SchemaVersion != published.SchemaVersion {
		t.Errorf("the draft's export is named %q, of version %d", draft.Name, draft.SchemaVersion)
	}
	for path, data := range stored {
		if path != published.ManifestPath && !bytes.Equal(draft.Files[path], data) {
			t.Errorf("the draft's %s differs from the release's of the same content", path)
		}
	}
	var m struct {
		Snapshot map[string]any `json:"snapshot"`
	}
	if err := json.Unmarshal(draft.Files[published.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot["channel"] != "draft" || m.Snapshot["contentHash"] != s.ContentHash ||
		m.Snapshot["pulledAt"] == nil || m.Snapshot["id"] != nil || m.Snapshot["version"] != nil {
		t.Errorf("the draft's manifest says %v, want the draft channel of the release's content hash %s",
			m.Snapshot, s.ContentHash)
	}

	building := e.mustPublish(t, "0.2.0")
	var notReady *snapshot.NotReadyError
	if _, err := e.snapshots.Export(ctx, e.actor(), e.project, snapshotID(t, building), 0); !errors.As(err,
		&notReady) || notReady.Status != snapshot.Building {
		t.Errorf("the export of a building snapshot: %v, want a *NotReadyError", err)
	}
	if _, err := e.snapshots.Export(ctx, e.actor(), e.project, ids.New(), 0); !errors.Is(err, snapshot.ErrNotFound) {
		t.Errorf("the export of a snapshot that the project does not have: %v", err)
	}
	newer := schemaversion.Supported().Max + 1
	var unsupported *schemaversion.UnsupportedError
	if _, err := e.snapshots.Export(ctx, e.actor(), e.project, ids.New(), newer); !errors.As(err, &unsupported) ||
		unsupported.Version != newer || unsupported.Supported != schemaversion.Supported() {
		t.Errorf("the export at version %d: %v, want an *UnsupportedError, before the snapshot is read", newer, err)
	}
	if _, err := e.snapshots.ExportDraft(ctx, e.actor(), e.project, newer); !errors.As(err, &unsupported) {
		t.Errorf("the draft's export at version %d: %v, want an *UnsupportedError", newer, err)
	}
}

// TestRenderUnreadVersion checks that the files of a snapshot stored at a
// version that the server does not read are exported at that version alone,
// as they are, and that their export at a version that it reads fails with
// an error of the server's, and not an *schemaversion.UnsupportedError,
// which the API would answer as though the version asked for were the one
// that it does not read.
func TestRenderUnreadVersion(t *testing.T) {
	t.Parallel()
	unread := schemaversion.Supported().Max + 1
	files := schemaversion.Files{published.ManifestPath: []byte(fmt.Sprintf(`{"schemaVersion": %d}`, unread))}
	for _, to := range []int{0, unread} {
		out, version, err := snapshot.Render(files, unread, to)
		if err != nil || version != unread || !maps.EqualFunc(out, files, bytes.Equal) {
			t.Errorf("the export at %d of files of version %d: %v of version %d (%v), want the files as they are",
				to, unread, out, version, err)
		}
	}
	var unsupported *schemaversion.UnsupportedError
	if _, _, err := snapshot.Render(files, unread, published.SchemaVersion); err == nil ||
		errors.As(err, &unsupported) {
		t.Errorf("the export at version %d of files of version %d: %v, want an error of the server's",
			published.SchemaVersion, unread, err)
	}
}

// TestListNewestFirst checks that the list holds the project's snapshots in
// the reverse of the order in which they were published, a page at a time,
// those of a status alone with it, and that a snapshot published after one
// whose ID a server with a clock an hour ahead made still sorts after it, as
// each new snapshot's ID is made after the project's newest
// (ids.NewAfter).
func TestListNewestFirst(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newStory(t)
	ctx := context.Background()
	var order []string
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0"} {
		order = append(order, e.mustPublish(t, v).ID)
	}
	// A snapshot that a server whose clock is an hour ahead published.
	ahead := ids.NewV7(time.Now().Add(time.Hour).UnixMilli(), [10]byte{})
	if _, err := e.d.Admin(t).Exec(ctx, `INSERT INTO snapshot (id, workspace_id, project_id, version,
		version_major, version_minor, version_patch, note, acknowledged_seq, acknowledged_warnings)
		VALUES ($1, $2, $3, '0.4.0', 0, 4, 0, 'Harbour intro', 0, '[]')`, ahead, e.workspace,
		e.project); err != nil {
		t.Fatal(err)
	}
	order = append(order, ids.Encode(ids.Snapshot, ahead))
	for _, v := range []string{"0.5.0", "0.6.0"} {
		order = append(order, e.mustPublish(t, v).ID)
	}
	slices.Reverse(order)

	var listed []string
	after := ids.UUID{}
	for {
		page, err := e.snapshots.List(ctx, e.actor(), e.project, "", after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, s := range page {
			listed = append(listed, s.ID)
		}
		after = snapshotID(t, page[len(page)-1])
	}
	if !slices.Equal(listed, order) {
		t.Errorf("the list is %v, want %v, the newest first", listed, order)
	}
	ready, err := e.snapshots.List(ctx, e.actor(), e.project, snapshot.Ready, ids.UUID{}, 10)
	if err != nil || len(ready) != 0 {
		t.Errorf("the ready snapshots are %v (%v), want none, as none is built", ready, err)
	}
}
