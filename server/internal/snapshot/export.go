package snapshot

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
// names each construct. A snapshot stored at a version that the server no
// longer reads, which the migrations of stored content on an upgrade leave
// none of (§6.4), it exports at that version alone, and at another fails
// with an error of the server's, not an *schemaversion.UnsupportedError,
// which would say that the version asked for is the one not read (render).
// The token's scopes are the route's to check.
func (s *Service) Export(ctx context.Context, actor commands.Actor, projectID, snapshotID ids.UUID, schema int) (
	Export, error) {
	if err := checkSchema(schema); err != nil {
		return Export{}, err
	}
	var row queries.SnapshotRow
	var files schemaversion.Files
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		var err error
		row, files, err = readyFiles(ctx, tx, ws, projectID, snapshotID)
		return err
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

// readyFiles returns the row of the snapshot snapshotID of the project
// projectID of the workspace ws, which it reads in tx, and its files, by their
// paths, or ErrNotFound for a snapshot that the project does not have, and a
// *NotReadyError for one that is not ready, which has no files.
func readyFiles(ctx context.Context, tx pgx.Tx, ws, projectID, snapshotID ids.UUID) (queries.SnapshotRow,
	schemaversion.Files, error) {
	row, err := get(ctx, tx, ws, projectID, snapshotID)
	if err != nil {
		return queries.SnapshotRow{}, nil, err
	}
	// A ready snapshot's files never change, so they are read as they were
	// when its row was.
	if status := Status(row.Status); status != Ready {
		return queries.SnapshotRow{}, nil, &NotReadyError{Status: status}
	}
	rows, err := queries.New(tx).SnapshotFiles(ctx, queries.SnapshotFilesParams{WorkspaceID: nullUUID(ws),
		SnapshotID: nullUUID(snapshotID)})
	if err != nil {
		return queries.SnapshotRow{}, nil, fmt.Errorf("snapshot: read the snapshot's files: %w", err)
	}
	files := schemaversion.Files{}
	for _, f := range rows {
		files[f.Path] = f.Bytes
	}
	return row, files, nil
}

// ExportDraft returns the files of the draft of the project projectID, as
// the serializer writes them of the draft as it is now (package serializer),
// read in one transaction that only reads, as of one moment
// (project.Service.ReadSnapshot, serializer.Load), and written after that
// transaction ends, as a build writes a release's, so that the
// serialization holds no connection, at the version schema of the published
// format, or at the version that the serializer writes for 0, rendered as
// Export renders a snapshot's, once the actor may read the project. At the
// serializer's own version, they are the files that it writes, of package
// published's types, which refuse to write what a consumer's decoding
// refuses, and which are not validated against the schemas, as a build
// validates a release's (FR-DATA-04 AC1); at another, schemaversion.Render
// validates them first, so that files that do not validate, a defect of the
// serializer's, fail the export with an error of the server's. Its manifest
// marks it as the draft's (§7.8): its snapshot's channel is draft, with the
// draft's content hash and when it was exported, as pulledAt, and with
// neither an ID nor a version, which a draft has none of; and what the draft
// holds that no snapshot may, the serializer stands in for, so that the
// export of a draft that validation would refuse to publish is never refused
// for it (StandIn).
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
	var draft *serializer.Draft
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		var err error
		draft, err = serializer.Load(ctx, tx, ws, projectID)
		return err
	})
	if err != nil {
		return Export{}, err
	}
	built, err := serializer.Serialize(draft, serializer.Stamp{Generator: serializer.Generator(), At: time.Now()})
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
// at native, as a snapshot's build stored them, which it validated, or as
// the serializer wrote the draft's, and what schemaversion.Render returns at
// another version, which validates f first. Where the server no longer reads
// native, as a snapshot's that an upgrade did not migrate, it renders f at
// no other version, and fails with an error of the server's, not an
// *schemaversion.UnsupportedError, which the API would answer as the version
// to's, which the server reads (checkSchema).
func render(f schemaversion.Files, native, to int) (schemaversion.Files, int, error) {
	if to == 0 || to == native {
		return f, native, nil
	}
	if r := schemaversion.Supported(); !r.Contains(native) {
		return nil, 0, fmt.Errorf("snapshot: the files are of version %d of the published format, and the server "+
			"reads versions %s, so it renders them at no other version", native, r)
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
	if err := json.Unmarshal(f[published.ManifestPath], &m); err != nil {
		return "", fmt.Errorf("snapshot: read the snapshot's manifest: %w", err)
	}
	if m.Project.Slug == "" {
		return "", errors.New("snapshot: the snapshot's manifest names no project's slug")
	}
	return m.Project.Slug + "-" + version + ".zip", nil
}

// zipTime is the modification time of every member of an export's zip: the
// earliest that a zip's MS-DOS time holds, so that a release of the server
// makes the same zip of the same files whenever and wherever it makes it, as
// the files themselves hold no time but the manifest's (§6.2).
var zipTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// Zip returns the export's files as a zip archive of the published layout
// (§6.3), as a pull downloads it (§7.5): a member of each file at its path,
// in the order of their paths, its bytes compressed with Deflate, and no
// member of a directory, which the path rules of a pull would refuse, each
// with zipTime as its modification time, so that a release of the server
// always makes the same archive of the same files. Another release may
// compress them otherwise, since Go's compress/flate does not promise the
// same output from one Go release to the next, so a client compares the
// files, which the manifest hashes, and not the archive's bytes.
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
