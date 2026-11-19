package schemaversion

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Logged is a step of the migration of a snapshot's files that its project's
// migration log holds: the snapshot's ID, as the API spells it, the step, and
// when it was logged.
type Logged struct {
	Snapshot string
	Step
	At time.Time
}

// Record logs each of steps, which Migrate or Render took of the files of the
// snapshot snapshotID of the project projectID, of the workspace
// workspaceID, in the project's migration log (FR-DATA-03 AC2), in db, a
// transaction of the workspace, the one that stores what the migration wrote,
// so that the log holds what a stored migration did not carry forward
// exactly when its files are stored. A step that the log holds already of the
// snapshot, from its version, is left as it was, as it reported the same.
func Record(ctx context.Context, db queries.DBTX, workspaceID, projectID, snapshotID ids.UUID, steps []Step) error {
	q := queries.New(db)
	for _, s := range steps {
		entries := s.Entries
		if entries == nil {
			entries = []Entry{}
		}
		data, err := json.Marshal(entries)
		if err != nil {
			return fmt.Errorf("schemaversion: log the step from version %d: %w", s.From, err)
		}
		if err := q.InsertSchemaMigrationStep(ctx, queries.InsertSchemaMigrationStepParams{ID: pgUUID(ids.New()),
			WorkspaceID: pgUUID(workspaceID), ProjectID: pgUUID(projectID), SnapshotID: pgUUID(snapshotID),
			FromVersion: int32(s.From), ToVersion: int32(s.To), Entries: data}); err != nil {
			return fmt.Errorf("schemaversion: log the step from version %d: %w", s.From, err)
		}
	}
	return nil
}

// Log returns the migration log of the project projectID, of the workspace
// workspaceID, read in db, a transaction of the workspace: the steps that it
// holds, by their snapshots' IDs, and each snapshot's the oldest first.
func Log(ctx context.Context, db queries.DBTX, workspaceID, projectID ids.UUID) ([]Logged, error) {
	rows, err := queries.New(db).SchemaMigrationSteps(ctx, queries.SchemaMigrationStepsParams{
		WorkspaceID: pgUUID(workspaceID), ProjectID: pgUUID(projectID)})
	if err != nil {
		return nil, fmt.Errorf("schemaversion: read the migration log: %w", err)
	}
	out := make([]Logged, 0, len(rows))
	for _, r := range rows {
		var entries []Entry
		if err := json.Unmarshal(r.Entries, &entries); err != nil {
			return nil, fmt.Errorf("schemaversion: read the migration log: %w", err)
		}
		if len(entries) == 0 {
			entries = nil
		}
		out = append(out, Logged{Snapshot: ids.Encode(ids.Snapshot, r.SnapshotID.Bytes),
			Step: Step{From: int(r.FromVersion), To: int(r.ToVersion), Entries: entries}, At: r.CreatedAt.Time})
	}
	return out, nil
}

// pgUUID returns id as a non-null UUID of the queries.
func pgUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
