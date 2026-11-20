package snapshot

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

// The list of a project's snapshots, and the exports of a snapshot's files
// and of the draft's (PRD FR-SNAP-02, FR-SYNC-01, plan item 79, note N86).

// List returns the snapshots of the project projectID, newest first, by
// their IDs, which follow the order in which they were published (FR-SNAP-02):
// those of the status, or of every status for "", after the snapshot of the
// UUID after in that order, or from the newest for the zero UUID, at most
// limit of them, once the actor may read the project
// (project.Service.ReadContent). It returns the errors of
// project.Service.Get. The token's scopes are the route's to check.
func (s *Service) List(ctx context.Context, actor commands.Actor, projectID ids.UUID, status Status, after ids.UUID,
	limit int) ([]Snapshot, error) {
	var out []Snapshot
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		rows, err := queries.New(tx).Snapshots(ctx, queries.SnapshotsParams{WorkspaceID: nullUUID(ws),
			ProjectID: nullUUID(projectID), Status: pgtype.Text{String: string(status), Valid: status != ""},
			After: nullUUID(after), RowLimit: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("snapshot: list the project's snapshots: %w", err)
		}
		out = make([]Snapshot, len(rows))
		for i, row := range rows {
			out[i] = snapshotOf(queries.SnapshotRow(row))
		}
		return nil
	})
	return out, err
}

// NotReadyError is Export's error for a snapshot that is not ready, which has
// no files to export: one that is building, whose build has not ended yet, or
// one that failed, which never has any (§7.3).
type NotReadyError struct {
	// Status is the snapshot's status, Building or Failed.
	Status Status
}

func (e *NotReadyError) Error() string {
	return fmt.Sprintf("snapshot: the snapshot is %s, and only a ready one has files to export", e.Status)
}

// Export is an export of a snapshot's files, or of the draft's, at a version
// of the published format (FR-SYNC-01), which Zip writes as the zip of the
// published layout (§6.3).
type Export struct {
	// Files are the export's files, by their paths, the manifest's among
	// them, of the version SchemaVersion.
	Files         schemaversion.Files
	SchemaVersion int
	// Name is the name of the export's zip: the project's slug, as its
	// manifest has it, and the snapshot's version, or draft for the draft's,
	// as harbour-1.0.0.zip or harbour-draft.zip.
	Name string
}

// Export returns the files of the snapshot snapshotID of the project
// projectID at the version schema of the published format, or at the
// snapshot's own version, the one that its build wrote, for 0 (FR-SYNC-01
// AC1), once the actor may read the project (project.Service.ReadContent).
// At its own version, they are the files that its build stored, byte for
// byte; at an older one, the files that the renderer of each version
// renders down to it, and at a newer one those that the migration to each
// version writes, as schemaversion.Render renders them, which validate
// against the version schema (AC2). A migration to a newer version stores
// nothing, so it records nothing in the project's migration log, which is
// of the migrations of stored content.
//
// It returns the errors of project.Service.Get; ErrNotFound for a snapshot
// that the project does not have; an *schemaversion.UnsupportedError for a
// version that the server does not read (AC3), before it reads the
// snapshot; a *NotReadyError for a snapshot that is building or failed,
// which has no files; and an *schemaversion.UnrepresentableError where the
// older version cannot express what the snapshot holds (PRD §6.4), which
// names each construct. The token's scopes are the route's to check.
func (s *Service) Export(ctx context.Context, actor commands.Actor, projectID, snapshotID ids.UUID, schema int) (
	Export, error) {
	if err := checkSchema(schema); err != nil {
		return Export{}, err
	}
	var row queries.SnapshotRow
	files := schemaversion.Files{}
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		var err error
		if row, err = get(ctx, tx, ws, projectID, snapshotID); err != nil {
			return err
		}
		// A ready snapshot's files never change, so they are read as they
		// were when its row was.
		if status := Status(row.Status); status != Ready {
			return &NotReadyError{Status: status}
		}
		rows, err := queries.New(tx).SnapshotFiles(ctx, queries.SnapshotFilesParams{WorkspaceID: nullUUID(ws),
			SnapshotID: nullUUID(snapshotID)})
		if err != nil {
			return fmt.Errorf("snapshot: read the snapshot's files: %w", err)
		}
		for _, f := range rows {
			files[f.Path] = f.Bytes
		}
		return nil
	})
	if err != nil {
		return Export{}, err
	}
	name, err := nameOf(files, row.Version)
	if err != nil {
		return Export{}, err
	}
	out, version, err := render(files, int(row.SchemaVersion.Int32), schema)
	if err != nil {
		return Export{}, err
	}
	return Export{Files: out, SchemaVersion: version, Name: name}, nil
}

// ExportDraft returns the files of the draft of the project projectID, as
// the serializer writes them of the draft as it is now (package serializer),
// read in one transaction that only reads, as of one moment
// (project.Service.ReadSnapshot), at the version schema of the published
// format, or at the version that the serializer writes for 0, rendered as
// Export renders a snapshot's, once the actor may read the project. Its
// manifest marks it as the draft's (§7.8): its snapshot's channel is draft,
// with the draft's content hash and when it was exported, as pulledAt, and
// with neither an ID nor a version, which a draft has none of; and what the
// draft holds that no snapshot may, the serializer stands in for, so that
// the export of a draft that validation would refuse to publish is never
// refused for it (StandIn).
//
// It returns the errors of project.Service.Get, an
// *schemaversion.UnsupportedError for a version that the server does not
// read, before it reads the draft, and an *schemaversion.UnrepresentableError
// where the older version cannot express what the draft holds. The token's
// scopes are the route's to check: the draft is not a release.
func (s *Service) ExportDraft(ctx context.Context, actor commands.Actor, projectID ids.UUID, schema int) (Export,
	error) {
	if err := checkSchema(schema); err != nil {
		return Export{}, err
	}
	var built *serializer.Snapshot
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		var err error
		built, err = serializer.Build(ctx, tx, ws, projectID, serializer.Stamp{Generator: serializer.Generator(),
			At: time.Now()})
		return err
	})
	if err != nil {
		return Export{}, err
	}
	out, version, err := render(built.Files, int(built.Manifest.SchemaVersion), schema)
	if err != nil {
		return Export{}, err
	}
	return Export{Files: out, SchemaVersion: version, Name: built.Manifest.Project.Slug + "-draft.zip"}, nil
}

// checkSchema returns an *schemaversion.UnsupportedError for schema, a
// version of the published format that an export asks for, if it is not 0,
// which asks for the files' own version, and the server does not read it.
func checkSchema(schema int) error {
	if r := schemaversion.Supported(); schema != 0 && !r.Contains(schema) {
		return &schemaversion.UnsupportedError{Version: schema, Supported: r}
	}
	return nil
}

// render returns f, a snapshot's files of the version native, at the version
// to, or at native for 0, and the version that it returns them at: f itself
// at native, which its build or the serializer wrote and checked, and what
// schemaversion.Render returns at another version, which validates f first.
func render(f schemaversion.Files, native, to int) (schemaversion.Files, int, error) {
	if to == 0 || to == native {
		return f, native, nil
	}
	out, _, err := schemaversion.Render(f, to)
	if err != nil {
		return nil, 0, err
	}
	return out, to, nil
}

// nameOf returns the name of the zip of a snapshot's files f of the version
// version: the slug of the project that its manifest names, and the version.
func nameOf(f schemaversion.Files, version string) (string, error) {
	var m struct {
		Project struct {
			Slug string `json:"slug"`
		} `json:"project"`
	}
	if err := json.Unmarshal(f[published.ManifestPath], &m); err != nil || m.Project.Slug == "" {
		return "", fmt.Errorf("snapshot: the snapshot's manifest names no project's slug: %v", err)
	}
	return m.Project.Slug + "-" + version + ".zip", nil
}

// zipTime is the modification time of every member of an export's zip: the
// earliest that a zip's MS-DOS time holds, so that the same files make the
// same zip, whenever and wherever it is made, as the files themselves hold
// no time but the manifest's (§6.2).
var zipTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// Zip returns the export's files as a zip archive of the published layout
// (§6.3), as a pull downloads it (§7.5): a member of each file at its path,
// in the order of their paths, its bytes compressed with Deflate, and no
// member of a directory, which the path rules of a pull would refuse, each
// with zipTime as its modification time, so that the same files always make
// the same archive.
func (e Export) Zip() ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, path := range slices.Sorted(maps.Keys(e.Files)) {
		f, err := w.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return nil, fmt.Errorf("snapshot: zip %s: %w", path, err)
		}
		if _, err := f.Write(e.Files[path]); err != nil {
			return nil, fmt.Errorf("snapshot: zip %s: %w", path, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("snapshot: zip the files: %w", err)
	}
	return buf.Bytes(), nil
}
