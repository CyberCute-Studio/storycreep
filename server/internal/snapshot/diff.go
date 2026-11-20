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
// projectID into its snapshot to (package diff), sized for display to at most
// limit changes (diff.CompareSized), once the actor may read the project
// (project.Service.ReadContent), which reads the two in one transaction. Each
// side is compared at the version of the published format that the server
// writes, published.SchemaVersion: a snapshot stored at another, which the
// migrations of stored content on an upgrade leave none of, is rendered at it
// first, as Export renders it (render), so that the two sides are always at
// one version (§7.4). Either snapshot may be any of the project's that is
// ready, older than the newest or not, in either order (FR-SYNC-02 AC1).
//
// It returns the errors of project.Service.Get, ErrNotFound for a snapshot
// that the project does not have, the zero UUID's among them, a
// *NotReadyError for one that is building or failed, the first of from and to
// that is, which has no files, and an *schemaversion.UnrepresentableError for
// a snapshot stored at a newer version than the server writes that its
// version cannot express. The token's snapshot:read is the route's to check.
func (s *Service) Diff(ctx context.Context, actor commands.Actor, projectID, from, to ids.UUID, limit int) (
	*diff.Diff, error) {
	var f, t diffSide
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		if err := f.read(ctx, tx, ws, projectID, from); err != nil {
			return err
		}
		return t.read(ctx, tx, ws, projectID, to)
	})
	if err != nil {
		return nil, err
	}
	return compareSides(f, t, limit)
}

// DiffDraft returns the entity-level diff of the snapshot from of the project
// projectID into the project's draft as it is now, as Diff returns a diff of
// two snapshots, but that the snapshot and the draft are read in one
// transaction that reads as of one moment (project.Service.ReadSnapshot), as
// ExportDraft reads the draft, which is serialized after that transaction
// ends, as the draft's export writes it, marked as the draft's (§7.8), so that
// the serialization holds no connection.
//
// The draft is not a release, so a diff into it needs an API token's
// project:read, and is refused with a *scope.InsufficientError for one that
// holds snapshot:read alone, before anything is read, as the draft's export is
// (PRD §5.6). It returns Diff's errors, of from.
func (s *Service) DiffDraft(ctx context.Context, actor commands.Actor, projectID, from ids.UUID, limit int) (
	*diff.Diff, error) {
	if !actor.Token.Allows(scope.ProjectRead) {
		return nil, &scope.InsufficientError{Scope: scope.ProjectRead}
	}
	var f diffSide
	var draft *serializer.Draft
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		if err := f.read(ctx, tx, ws, projectID, from); err != nil {
			return err
		}
		var err error
		draft, err = serializer.Load(ctx, tx, ws, projectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	built, err := serializer.Serialize(draft, serializer.Stamp{Generator: serializer.Generator(), At: time.Now()})
	if err != nil {
		return nil, err
	}
	return compareSides(f, diffSide{files: built.Files, version: int(built.Manifest.SchemaVersion)}, limit)
}

// diffSide is a side of a diff: its files, at the version of the published
// format that they are of, and the row of a snapshot's, which the draft's has
// none of, and its ID.
type diffSide struct {
	files   schemaversion.Files
	version int
	row     *queries.SnapshotRow
	id      ids.UUID
}

// read reads the side of the snapshot id of the project projectID of the
// workspace ws, a ready one, in tx (readyFiles).
func (d *diffSide) read(ctx context.Context, tx pgx.Tx, ws, projectID, id ids.UUID) error {
	row, files, err := readyFiles(ctx, tx, ws, projectID, id)
	if err != nil {
		return err
	}
	*d = diffSide{files: files, version: int(row.SchemaVersion.Int32), row: &row, id: id}
	return nil
}

// compareSides returns the diff of f into t, each rendered at
// published.SchemaVersion first, sized to at most limit changes, with each
// snapshot's side named by the snapshot that the request names, by its row,
// which its build's manifest names alike, with the content hash of the files
// compared, at the version that they were compared at.
func compareSides(f, t diffSide, limit int) (*diff.Diff, error) {
	var err error
	if f.files, _, err = render(f.files, f.version, published.SchemaVersion); err != nil {
		return nil, err
	}
	if t.files, _, err = render(t.files, t.version, published.SchemaVersion); err != nil {
		return nil, err
	}
	d, err := diff.CompareSized(f.files, t.files, limit)
	if err != nil {
		return nil, err
	}
	for _, s := range []struct {
		out  *diff.DiffSide
		side diffSide
	}{{&d.From, f}, {&d.To, t}} {
		if s.side.row != nil {
			s.out.Kind, s.out.ID, s.out.Version = diff.SideSnapshot, ids.Encode(ids.Snapshot, s.side.id),
				s.side.row.Version
		}
	}
	return d, nil
}
