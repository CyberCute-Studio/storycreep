package snapshot

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/diff"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

// The entity-level diff of two snapshots, or of a snapshot and the draft
// (PRD FR-SNAP-04, FR-SYNC-02, plan item 80, note N87).

// Diff returns the entity-level diff of the snapshot from of the project
// projectID into the snapshot to, or, for the zero UUID, into the project's
// draft as it is now (package diff), once the actor may read the project
// (project.Service.ReadContent). The two snapshots are read in one
// transaction, and a snapshot and the draft in one that reads as of one
// moment (project.Service.ReadSnapshot), as ExportDraft reads the draft,
// which is serialized after that transaction ends, as the draft's export
// writes it, marked as the draft's (§7.8), so that the serialization holds
// no connection. Each side is compared at the version of the published
// format that the server writes, published.SchemaVersion: a snapshot stored
// at another, which the migrations of stored content on an upgrade leave
// none of, is rendered at it first, as Export renders it (render), so that
// the two sides are always at one version (§7.4). Either snapshot may be any
// of the project's that is ready, older than the newest or not, in either
// order (FR-SYNC-02 AC1).
//
// The draft is not a release, so a diff into it needs an API token's
// project:read, and is refused with a *scope.InsufficientError for one that
// holds snapshot:read alone, before anything is read, as the draft's export is
// (PRD §5.6). It returns the errors of project.Service.Get, ErrNotFound for a
// snapshot that the project does not have, a *NotReadyError for one that is
// building or failed, the first of from and to that is, which has no files,
// and an *schemaversion.UnrepresentableError for a snapshot stored at a newer
// version than the server writes that its version cannot express. The token's
// snapshot:read is the route's to check.
func (s *Service) Diff(ctx context.Context, actor commands.Actor, projectID, from, to ids.UUID) (*diff.Diff, error) {
	type side struct {
		files   schemaversion.Files
		version int
		row     *queries.SnapshotRow
	}
	var f, t side
	read := func(tx pgx.Tx, ws, id ids.UUID, s *side) error {
		row, files, err := readyFiles(ctx, tx, ws, projectID, id)
		*s = side{files: files, version: int(row.SchemaVersion.Int32), row: &row}
		return err
	}
	var draft *serializer.Draft
	var err error
	if to == (ids.UUID{}) {
		if !actor.Token.Allows(scope.ProjectRead) {
			return nil, &scope.InsufficientError{Scope: scope.ProjectRead}
		}
		err = s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
			if err := read(tx, ws, from, &f); err != nil {
				return err
			}
			var err error
			draft, err = serializer.Load(ctx, tx, ws, projectID)
			return err
		})
	} else {
		err = s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
			if err := read(tx, ws, from, &f); err != nil {
				return err
			}
			return read(tx, ws, to, &t)
		})
	}
	if err != nil {
		return nil, err
	}
	if draft != nil {
		built, err := serializer.Serialize(draft, serializer.Stamp{Generator: serializer.Generator(), At: time.Now()})
		if err != nil {
			return nil, err
		}
		t = side{files: built.Files, version: int(built.Manifest.SchemaVersion)}
	}
	if f.files, _, err = render(f.files, f.version, published.SchemaVersion); err != nil {
		return nil, err
	}
	if t.files, _, err = render(t.files, t.version, published.SchemaVersion); err != nil {
		return nil, err
	}
	d, err := diff.Compare(f.files, t.files)
	if err != nil {
		return nil, err
	}
	// A snapshot's side is the snapshot that the request names, by its row,
	// which its build's manifest names alike, with the content hash of the
	// files compared, at the version that they were compared at.
	for _, s := range []struct {
		side *diff.DiffSide
		id   ids.UUID
		row  *queries.SnapshotRow
	}{{&d.From, from, f.row}, {&d.To, to, t.row}} {
		if s.row != nil {
			s.side.Kind, s.side.ID, s.side.Version = diff.SideSnapshot, ids.Encode(ids.Snapshot, s.id), s.row.Version
		}
	}
	return d, nil
}
