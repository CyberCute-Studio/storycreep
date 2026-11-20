package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
)

// buildAttempts is how many attempts the job of a build has: the queue's
// default, whose retries span about 13 hours (package jobs), so that a
// database that fails for a while has answered again before the last. The
// last does not build: it fails the snapshot, which would otherwise hold its
// version while it is building, so that no failure of a build itself, as one
// whose time runs out, or a process that a build takes down, which leaves its
// job to River's rescuer, keeps the snapshot building after its job ends.
const buildAttempts = 25

// buildJob is the job that builds a snapshot (FR-JOBS-01, §7.3). Its jobs
// run one at a time in a process, on a queue of their own (jobs.Serial),
// since a build holds a project's draft and its files in memory, so that
// builds of large projects at once do not take the process's memory, nor
// hold the workers of the other jobs while they wait for their turn.
type buildJob struct {
	WorkspaceID string `json:"workspace_id"`
	SnapshotID  string `json:"snapshot_id"`
}

func (buildJob) Kind() string { return "snapshot_build" }

// RegisterJobs registers the job that builds a snapshot in r, which Publish
// enqueues, and which runs one at a time.
func (s *Service) RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, s.work)
	jobs.Serial(r, buildJob{})
}

// work is an attempt at the job that builds a snapshot. Each attempt but the
// last builds it (Build), and returns Build's error, which the queue attempts
// again after; the last attempt, which only one whose builds all failed
// reaches, fails the snapshot with build_failed if it is building still, and
// ends the job (buildAttempts). A server that stops during that attempt has
// it run again later, as it has every attempt whose context it cancels.
func (s *Service) work(ctx context.Context, job *jobs.Job[buildJob]) error {
	workspaceID, err := ids.ParseUUID(job.Args.WorkspaceID)
	if err != nil {
		return jobs.Cancel(fmt.Errorf("snapshot: the job's workspace: %w", err))
	}
	snapshotID, err := ids.ParseUUID(job.Args.SnapshotID)
	if err != nil {
		return jobs.Cancel(fmt.Errorf("snapshot: the job's snapshot: %w", err))
	}
	svc := s.withDB(job.DB)
	if job.Attempt < job.MaxAttempts {
		return svc.Build(ctx, workspaceID, snapshotID)
	}
	failed, err := svc.finish(ctx, workspaceID, snapshotID, &outcome{code: FailureBuild})
	if err != nil || !failed {
		return err
	}
	err = fmt.Errorf("snapshot: the build failed on each attempt of its job before the last, %d, which fails "+
		"the snapshot", job.MaxAttempts)
	svc.log.ErrorContext(ctx, "the snapshot's build failed on every attempt of its job, so the snapshot fails",
		"snapshot_id", ids.Encode(ids.Snapshot, snapshotID), "error", err)
	return jobs.Cancel(err)
}

// withDB returns a Service like s that reads and writes db, as a job does on
// the workers' pool that it carries (package jobs).
func (s *Service) withDB(db *database.DB) *Service {
	if db == nil {
		return s
	}
	c := *s
	c.db = db
	return &c
}

// outcome is what a build found: the report of the draft that it read, if it
// read one, and the files that it wrote of it, or why the snapshot fails.
type outcome struct {
	// code is why the snapshot fails, or "" for one that becomes ready.
	code FailureCode
	// read says that the build read the draft, as of report's Seq.
	read   bool
	report validation.Stored
	// files are the snapshot's files, by their paths, manifest its
	// manifest, and contentHash its content hash, of a snapshot that becomes
	// ready.
	files       map[string][]byte
	manifest    published.Manifest
	contentHash published.Hash
}

// Build builds the snapshot snapshotID of the workspace workspaceID, if it is
// building still (§7.3): it reads the snapshot, and its project's draft, in
// one transaction that only reads, as of one moment, validates the draft
// there, and serializes it at the current version of the published format,
// whose files it validates against the format's schemas (FR-DATA-04 AC1).
// The snapshot becomes failed, with validation_failed, on an error of the
// validation; with validation_changed, on a warning that is neither muted nor
// among those that its author acknowledged, if the draft's sequence is not
// the one whose report they acknowledged; and with build_failed, on a draft
// that the serializer refuses, a file that does not validate, or a project
// that is in its workspace's trash; and ready, with its files, otherwise. The
// snapshot's report is the one that the build computed, of the draft as of
// its source_seq, as its files are (FR-DEP-04 AC4).
//
// It returns an error, changing nothing, for a failure that another attempt
// may not meet, as of the database, and nil for a snapshot that is no longer
// building, or no longer exists, as with its project, so it is safe to
// repeat: one attempt alone finishes the snapshot, under its lock.
func (s *Service) Build(ctx context.Context, workspaceID, snapshotID ids.UUID) error {
	o, err := s.read(ctx, workspaceID, snapshotID)
	if err != nil || o == nil {
		return err
	}
	_, err = s.finish(ctx, workspaceID, snapshotID, o)
	return err
}

// read reads the snapshot snapshotID of the workspace workspaceID, and the
// draft of its project, in one transaction that only reads, as of one
// moment, and returns what its build found of it; nil for a snapshot that is
// no longer building, or no longer exists; or an error for a failure of the
// database, or a context's.
func (s *Service) read(ctx context.Context, workspaceID, snapshotID ids.UUID) (*outcome, error) {
	var row queries.Snapshot
	var o *outcome
	var draft *serializer.Draft
	err := s.db.InSnapshot(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		var err error
		row, err = queries.New(tx).SnapshotToRead(ctx, queries.SnapshotToReadParams{
			WorkspaceID: nullUUID(workspaceID), ID: nullUUID(snapshotID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("snapshot: read the snapshot to build: %w", err)
		case Status(row.Status) != Building:
			return nil
		}
		o = &outcome{}
		var acks []validation.Acknowledgement
		if err := json.Unmarshal(row.AcknowledgedWarnings, &acks); err != nil {
			o.code = FailureBuild
			return s.failure(ctx, row, "its acknowledged warnings do not decode", err)
		}
		projectID := ids.UUID(row.ProjectID.Bytes)
		res, err := validation.Run(ctx, tx, workspaceID, projectID)
		if err != nil {
			return err
		}
		o.read, o.report = true, res.Stored()
		if s.hooks.validated != nil {
			s.hooks.validated()
		}
		switch {
		case len(o.report.Errors()) > 0:
			o.code = FailureValidation
			return nil
		case o.report.Seq != row.AcknowledgedSeq && len(o.report.Unacknowledged(acks)) > 0:
			o.code = FailureChanged
			return nil
		}
		draft, err = serializer.Load(ctx, tx, workspaceID, projectID)
		if errors.Is(err, pgx.ErrNoRows) {
			// The project was moved to its workspace's trash since it was
			// published, which the serializer reads nothing of: its
			// member's choice, not a defect of the server's.
			o.code = FailureBuild
			s.log.InfoContext(ctx, "the snapshot's build failed, since its project is in the trash",
				"snapshot_id", ids.Encode(ids.Snapshot, row.ID.Bytes))
			return nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if o == nil || o.code != "" {
		return o, nil
	}
	stamp := serializer.Stamp{Generator: serializer.Generator(), At: row.PublishedAt.Time,
		Release: &serializer.Release{ID: ids.Encode(ids.Snapshot, row.ID.Bytes), Version: row.Version, Note: row.Note}}
	built, err := serializer.Serialize(draft, stamp)
	if err != nil {
		o.code = FailureBuild
		return o, s.failure(ctx, row, "the serializer refused the draft that the validation passed", err)
	}
	if s.hooks.serialized != nil {
		s.hooks.serialized(built.Files)
	}
	if err := schemaversion.Validate(schemaversion.Files(built.Files)); err != nil {
		o.code = FailureBuild
		return o, s.failure(ctx, row, "a file that it wrote does not validate", err)
	}
	o.files, o.manifest, o.contentHash = built.Files, built.Manifest, built.ContentHash
	return o, nil
}

// failure logs why the build of the snapshot row fails itself, which is a
// defect of the server's, since the validation passed the draft, and returns
// nil, so that the build fails the snapshot rather than attempt it again.
func (s *Service) failure(ctx context.Context, row queries.Snapshot, why string, err error) error {
	s.log.ErrorContext(ctx, "the snapshot's build failed, since "+why, "snapshot_id",
		ids.Encode(ids.Snapshot, row.ID.Bytes), "error", err)
	return nil
}

// finish makes the snapshot snapshotID of the workspace workspaceID what o
// says, if it is building still, in one transaction that holds its lock:
// ready, with its files, its report, and what its manifest says of it, or
// failed, with o's code, and the report of the draft that the build read, if
// it read one, which takes it away from the workspace's usage of snapshots,
// since a failed snapshot is only the record of an attempt. It reports
// whether it changed the snapshot, which it does not to one that is no
// longer building, or no longer exists.
func (s *Service) finish(ctx context.Context, workspaceID, snapshotID ids.UUID, o *outcome) (bool, error) {
	changed := false
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		changed = false
		q := queries.New(tx)
		w, id := nullUUID(workspaceID), nullUUID(snapshotID)
		row, err := q.SnapshotToBuild(ctx, queries.SnapshotToBuildParams{WorkspaceID: w, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("snapshot: lock the snapshot to finish: %w", err)
		}
		if Status(row.Status) != Building {
			return nil
		}
		var report []byte
		var seq pgtype.Int8
		if o.read {
			if report, err = json.Marshal(o.report); err != nil {
				return fmt.Errorf("snapshot: encode the report: %w", err)
			}
			seq = pgtype.Int8{Int64: o.report.Seq, Valid: true}
		}
		if o.code != "" {
			if _, err := q.FailSnapshot(ctx, queries.FailSnapshotParams{WorkspaceID: w, ID: id,
				FailureCode: pgtype.Text{String: string(o.code), Valid: true}, SourceSeq: seq,
				ValidationReport: report}); err != nil {
				return fmt.Errorf("snapshot: fail the snapshot: %w", err)
			}
			changed = true
			return usage.Record(ctx, tx, workspaceID, usage.Snapshots, -1)
		}
		paths := slices.Sorted(maps.Keys(o.files))
		files := queries.InsertSnapshotFilesParams{WorkspaceID: w, SnapshotID: id, Paths: paths,
			Ids: make([]pgtype.UUID, len(paths)), Hashes: make([]string, len(paths)),
			Contents: make([][]byte, len(paths))}
		for i, path := range paths {
			files.Ids[i] = nullUUID(ids.New())
			files.Hashes[i] = string(published.HashOf(o.files[path]))
			files.Contents[i] = o.files[path]
		}
		if err := q.InsertSnapshotFiles(ctx, files); err != nil {
			return fmt.Errorf("snapshot: store the files: %w", err)
		}
		c := o.manifest.Counts
		counts, err := json.Marshal(EntityCounts{Boards: int64(c.Boards), Components: int64(c.Components),
			Connections: int64(c.Connections), Nodes: int64(c.Nodes), Variables: int64(c.Variables)})
		if err != nil {
			return fmt.Errorf("snapshot: encode the counts: %w", err)
		}
		if _, err := q.FinishSnapshot(ctx, queries.FinishSnapshotParams{WorkspaceID: w, ID: id, SourceSeq: seq,
			ValidationReport: report, SchemaVersion: pgtype.Int4{Int32: int32(o.manifest.SchemaVersion), Valid: true},
			ContentHash: pgtype.Text{String: string(o.contentHash), Valid: true}, Counts: counts}); err != nil {
			return fmt.Errorf("snapshot: make the snapshot ready: %w", err)
		}
		changed = true
		return nil
	})
	return changed && err == nil, err
}

// hooks are what the tests run within a build (export_test.go): validated
// once the build validated the draft, in its transaction, and serialized
// with the files that the serializer wrote, before they are validated.
type hooks struct {
	validated  func()
	serialized func(files map[string][]byte)
}
