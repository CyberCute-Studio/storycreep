package authoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
)

// The workspace exports (PRD FR-DATA-02, plan item 86, note N93): an owner of
// a workspace asks for a zip of the whole of it, which a job builds into the
// assets' store as an authoring export's is (workspace_archive.go), and which
// the owner who asked for it alone reads and downloads, while they are an
// owner, until it expires, Retention after it was asked for. Their jobs have
// an authoring export's attempts and times, and fail and expire alike.

// WorkspaceExport is an export of a workspace, as the API shows it.
type WorkspaceExport struct {
	ID           string
	WorkspaceID  string
	Status       string
	SizeBytes    *int64
	ProjectCount *int32
	FileName     string
	CreatedAt    time.Time
	FinishedAt   *time.Time
	ExpiresAt    time.Time
}

func workspaceExportOf(row queries.WorkspaceExport) WorkspaceExport {
	e := WorkspaceExport{ID: ids.Encode(ids.Job, row.ID.Bytes),
		WorkspaceID: ids.Encode(ids.Workspace, row.WorkspaceID.Bytes), Status: row.Status,
		FileName: row.FileName.String, CreatedAt: row.CreatedAt.Time.UTC(), FinishedAt: timeOf(row.FinishedAt),
		ExpiresAt: row.ExpiresAt.Time.UTC()}
	if row.SizeBytes.Valid {
		e.SizeBytes = &row.SizeBytes.Int64
	}
	if row.ProjectCount.Valid {
		e.ProjectCount = &row.ProjectCount.Int32
	}
	return e
}

// workspaceStorageKey returns the key of the archive of the workspace export
// id of the workspace ws, as storageKey does an authoring export's, which no
// other row's key shares.
func workspaceStorageKey(ws, id ids.UUID) string {
	sum := sha256.Sum256(id[:])
	return ws.String() + "/" + hex.EncodeToString(sum[:]) + ".workspace-export.zip"
}

// inWorkspace runs fn in a transaction of the workspace ws, once the actor may
// export it, with the errors of project.Service.CheckExportWorkspace.
func (s *Service) inWorkspace(ctx context.Context, actor commands.Actor, ws ids.UUID, fn func(tx pgx.Tx) error) error {
	return s.db.InTx(ctx, database.Scope{WorkspaceID: ws, UserID: actor.UserID}, func(tx pgx.Tx) error {
		if err := s.projects.CheckExportWorkspace(ctx, tx, actor.UserID, ws); err != nil {
			return err
		}
		return fn(tx)
	})
}

// CreateWorkspaceExport asks for an export of the workspace ws, which a job
// builds, once the actor may export it, an owner, and records it in the audit
// log, in one transaction. It returns the errors of
// project.Service.CheckExportWorkspace, asset.ErrSetupRequired while first-run
// setup may yet choose the store that the archive would be left behind in,
// and an *InProgressError where the user has one on its way.
func (s *Service) CreateWorkspaceExport(ctx context.Context, actor commands.Actor, ws ids.UUID) (WorkspaceExport,
	error) {
	return s.createWorkspaceExport(ctx, actor, ws, time.Time{})
}

// createWorkspaceExport is CreateWorkspaceExport, whose build job is due at
// buildAt, or at once for the zero time.
func (s *Service) createWorkspaceExport(ctx context.Context, actor commands.Actor, ws ids.UUID, buildAt time.Time) (
	WorkspaceExport, error) {
	// Outside the transaction that adds the export, as reading the store's
	// settings takes a connection of its own, and after the role, so that who
	// may not export the workspace learns nothing of the instance.
	if err := s.inWorkspace(ctx, actor, ws, func(pgx.Tx) error { return nil }); err != nil {
		return WorkspaceExport{}, err
	}
	if _, err := s.storage.FinalStore(ctx); err != nil {
		return WorkspaceExport{}, err
	}
	id := ids.New()
	var out WorkspaceExport
	err := s.inWorkspace(ctx, actor, ws, func(tx pgx.Tx) error {
		q := queries.New(tx)
		expires := time.Now().Add(Retention)
		row, err := insertWorkspaceExport(ctx, q, queries.InsertWorkspaceExportParams{ID: uuid(id),
			WorkspaceID: uuid(ws), RequestedBy: uuid(actor.UserID), StorageKey: workspaceStorageKey(ws, id),
			ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionWorkspaceExport, WorkspaceID: ws,
			ActorUserID: actor.UserID, ActorTokenID: actor.Token.TokenID(), SubjectID: ws,
			Result: audit.ResultSuccess, IP: actor.Client.IP, UserAgent: actor.Client.UserAgent,
			Metadata: map[string]any{"export_id": ids.Encode(ids.Job, id)}}); err != nil {
			return err
		}
		args := jobArgs{WorkspaceID: ws.String(), ExportID: id.String()}
		if _, err := s.queue.Enqueue(ctx, tx, workspaceBuildJob(args), &jobs.EnqueueOptions{At: buildAt,
			MaxAttempts: buildAttempts + failAttempts}); err != nil {
			return fmt.Errorf("authoring: enqueue the workspace's build: %w", err)
		}
		if _, err := s.queue.Enqueue(ctx, tx, workspaceExpireJob(args), &jobs.EnqueueOptions{At: expires,
			MaxAttempts: expireAttempts}); err != nil {
			return fmt.Errorf("authoring: enqueue the workspace export's expiry: %w", err)
		}
		out = workspaceExportOf(row)
		return nil
	})
	if err != nil {
		return WorkspaceExport{}, err
	}
	return out, nil
}

// insertWorkspaceExport adds the export p, or returns the *InProgressError of
// the user's export of the workspace that is on its way, as insert does an
// authoring export's.
func insertWorkspaceExport(ctx context.Context, q *queries.Queries, p queries.InsertWorkspaceExportParams) (
	queries.WorkspaceExport, error) {
	for range insertAttempts {
		row, err := q.InsertWorkspaceExport(ctx, p)
		if err == nil {
			return row, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return row, fmt.Errorf("authoring: add the workspace export: %w", err)
		}
		// One past its time, which its user can no longer read, holds the way
		// no longer. Its expiry job still deletes it, and its file.
		if err := q.FailExpiredWorkspaceExports(ctx, queries.FailExpiredWorkspaceExportsParams{
			WorkspaceID: p.WorkspaceID, RequestedBy: p.RequestedBy}); err != nil {
			return row, fmt.Errorf("authoring: end the user's workspace export past its time: %w", err)
		}
		running, err := q.WorkspaceExportInProgress(ctx, queries.WorkspaceExportInProgressParams{
			WorkspaceID: p.WorkspaceID, RequestedBy: p.RequestedBy})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return row, fmt.Errorf("authoring: read the user's workspace export on its way: %w", err)
		}
		return row, &InProgressError{ID: ids.Encode(ids.Job, running.Bytes)}
	}
	return queries.WorkspaceExport{}, errors.New("authoring: the user's workspace exports kept ending as this one was added")
}

// GetWorkspaceExport returns the export id of the workspace ws, if the actor
// asked for it and may still export the workspace. It returns the errors of
// project.Service.CheckExportWorkspace, and ErrNotFound.
func (s *Service) GetWorkspaceExport(ctx context.Context, actor commands.Actor, ws, id ids.UUID) (WorkspaceExport,
	error) {
	var out WorkspaceExport
	err := s.inWorkspace(ctx, actor, ws, func(tx pgx.Tx) error {
		row, err := ownWorkspaceExport(ctx, tx, ws, actor.UserID, id)
		if err != nil {
			return err
		}
		out = workspaceExportOf(row)
		return nil
	})
	return out, err
}

// ownWorkspaceExport returns the export id of the workspace that user asked
// for.
func ownWorkspaceExport(ctx context.Context, tx pgx.Tx, ws, user, id ids.UUID) (queries.WorkspaceExport, error) {
	row, err := queries.New(tx).WorkspaceExport(ctx, queries.WorkspaceExportParams{WorkspaceID: uuid(ws),
		RequestedBy: uuid(user), ID: uuid(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("authoring: read the workspace export: %w", err)
	}
	return row, nil
}

// WorkspaceExports returns the actor's exports of the workspace ws, newest
// first, after the export after, or from the newest for the zero UUID, at most
// limit of them, with the errors of GetWorkspaceExport but ErrNotFound.
func (s *Service) WorkspaceExports(ctx context.Context, actor commands.Actor, ws, after ids.UUID, limit int) (
	[]WorkspaceExport, error) {
	var out []WorkspaceExport
	err := s.inWorkspace(ctx, actor, ws, func(tx pgx.Tx) error {
		rows, err := queries.New(tx).WorkspaceExports(ctx, queries.WorkspaceExportsParams{WorkspaceID: uuid(ws),
			RequestedBy: uuid(actor.UserID), After: uuid(after), RowLimit: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("authoring: list the workspace exports: %w", err)
		}
		out = make([]WorkspaceExport, len(rows))
		for i, r := range rows {
			out[i] = workspaceExportOf(r)
		}
		return nil
	})
	return out, err
}

// OpenWorkspaceExport returns the archive of the export id of the workspace
// ws, if the actor asked for it and may still export the workspace. It
// returns the errors of GetWorkspaceExport, and a *NotReadyError for an export
// without a file. It holds no transaction while the file is read.
func (s *Service) OpenWorkspaceExport(ctx context.Context, actor commands.Actor, ws, id ids.UUID) (Download, error) {
	var row queries.WorkspaceExport
	err := s.inWorkspace(ctx, actor, ws, func(tx pgx.Tx) error {
		var err error
		row, err = ownWorkspaceExport(ctx, tx, ws, actor.UserID, id)
		return err
	})
	if err != nil {
		return Download{}, err
	}
	if row.Status != StatusReady {
		return Download{}, &NotReadyError{Status: row.Status}
	}
	store, err := s.storage.Store(ctx)
	if err != nil {
		return Download{}, fmt.Errorf("authoring: open the store: %w", err)
	}
	body, info, err := store.Open(ctx, row.StorageKey)
	if errors.Is(err, blob.ErrNotFound) {
		return Download{}, ErrNotFound
	}
	if err != nil {
		return Download{}, fmt.Errorf("authoring: open the workspace's archive: %w", err)
	}
	return Download{Body: body, Size: info.Size, Name: row.FileName.String}, nil
}

// workspaceBuildJob builds a workspace export's archive (FR-JOBS-01), one at a
// time in a process, on a queue of its own, as an authoring export's build
// does, since each reads one project whole at a time.
type workspaceBuildJob jobArgs

func (workspaceBuildJob) Kind() string { return "workspace_export_build" }

// workspaceExpireJob deletes a workspace export once its time has passed.
type workspaceExpireJob jobArgs

func (workspaceExpireJob) Kind() string { return "workspace_export_expire" }

// registerWorkspaceJobs registers the jobs of the workspace exports in r.
func (s *Service) registerWorkspaceJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[workspaceBuildJob]) error {
		workspaceID, id, err := parseArgs(jobArgs(job.Args))
		if err != nil {
			return jobs.Cancel(err)
		}
		svc := s.withDB(job.DB)
		if onlyFails(job.Attempt, job.Snoozes) {
			return svc.failWorkspaceExport(ctx, workspaceID, id,
				errors.New("the build failed, and its attempts are over"))
		}
		err = svc.BuildWorkspaceExport(ctx, workspaceID, id)
		if endsTheExport(ctx, err) {
			return svc.failWorkspaceExport(ctx, workspaceID, id, err)
		}
		return err
	})
	jobs.Serial(r, workspaceBuildJob{})
	jobs.Timeout(r, workspaceBuildJob{}, buildTimeout)
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[workspaceExpireJob]) error {
		workspaceID, id, err := parseArgs(jobArgs(job.Args))
		if err != nil {
			return jobs.Cancel(err)
		}
		svc := s.withDB(job.DB)
		err = svc.ExpireWorkspaceExport(ctx, workspaceID, id)
		if err != nil {
			svc.log.WarnContext(ctx, "the workspace export could not be deleted, and its job will try again",
				"export_id", ids.Encode(ids.Job, id), "attempt", job.Attempt, "error", err)
		}
		return err
	})
}

// failWorkspaceExport fails the export, deletes what an attempt of it stored,
// and ends its job, as fail does an authoring export's.
func (s *Service) failWorkspaceExport(ctx context.Context, ws, id ids.UUID, why error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failTimeout)
	defer cancel()
	var key string
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		var err error
		key, err = queries.New(tx).FailWorkspaceExport(ctx, queries.FailWorkspaceExportParams{WorkspaceID: uuid(ws),
			ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			key = ""
			return nil
		}
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "the workspace export could not be failed, and its job will try again",
			"export_id", ids.Encode(ids.Job, id), "error", err)
		return jobs.Snooze(failRetry)
	}
	if key == "" {
		s.log.InfoContext(ctx, "the workspace export had ended when its job came to fail it",
			"export_id", ids.Encode(ids.Job, id))
		return nil
	}
	s.dropFailed(ctx, id, key)
	s.log.ErrorContext(ctx, "the workspace export failed", "export_id", ids.Encode(ids.Job, id), "error", why)
	return jobs.Cancel(why)
}

// BuildWorkspaceExport builds the archive of the export id of the workspace
// ws, unless it has ended, and stores it under the export's key, then makes
// the export ready, as Build does an authoring export's: it is safe to repeat,
// and an export deleted meanwhile leaves no file behind.
func (s *Service) BuildWorkspaceExport(ctx context.Context, ws, id ids.UUID) error {
	var row queries.WorkspaceExport
	started, gone := false, false
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		var err error
		row, err = q.WorkspaceExportOfJob(ctx, queries.WorkspaceExportOfJobParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			gone = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("authoring: read the workspace export: %w", err)
		}
		n, err := q.StartWorkspaceExport(ctx, queries.StartWorkspaceExportParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if err != nil {
			return fmt.Errorf("authoring: start the workspace export: %w", err)
		}
		started = n > 0
		return nil
	})
	switch {
	case err != nil:
		return err
	case gone:
		store, err := s.storage.FinalStore(ctx)
		if err != nil {
			return fmt.Errorf("authoring: open the store: %w", err)
		}
		if err := store.Delete(ctx, workspaceStorageKey(ws, id)); err != nil {
			return fmt.Errorf("authoring: delete the archive of a workspace export that is gone: %w", err)
		}
		return nil
	case !started:
		return nil
	}
	c, err := s.readWorkspace(ctx, row)
	if err != nil {
		return err
	}
	// The final store, which CreateWorkspaceExport has checked that there is.
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return fmt.Errorf("authoring: open the store: %w", err)
	}
	w, err := store.Create(ctx, row.StorageKey, "application/zip")
	if err != nil {
		return fmt.Errorf("authoring: start the workspace's archive: %w", err)
	}
	defer func() { _ = w.Abort() }()
	counted := &countingWriter{w: w}
	if err := s.writeWorkspace(ctx, counted, store, row, c); err != nil {
		return err
	}
	if err := w.Commit(); err != nil {
		return fmt.Errorf("authoring: store the workspace's archive: %w", err)
	}
	var finished int64
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		var err error
		finished, err = queries.New(tx).FinishWorkspaceExport(ctx, queries.FinishWorkspaceExportParams{
			WorkspaceID: uuid(ws), ID: uuid(id), SizeBytes: pgtype.Int8{Int64: counted.n, Valid: true},
			ProjectCount: pgtype.Int4{Int32: int32(len(c.manifest.Projects)), Valid: true},
			FileName:     pgtype.Text{String: c.fileName, Valid: true}})
		return err
	})
	if err != nil {
		return fmt.Errorf("authoring: finish the workspace export: %w", err)
	}
	if finished == 0 {
		// The export was deleted, or ended, while it was being built, so
		// nothing refers to what was stored, unless the export is ready.
		keep := false
		err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
			now, err := queries.New(tx).WorkspaceExportOfJob(ctx, queries.WorkspaceExportOfJobParams{
				WorkspaceID: uuid(ws), ID: uuid(id)})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			keep = now.Status == StatusReady
			return nil
		})
		if err != nil {
			return fmt.Errorf("authoring: read the workspace export: %w", err)
		}
		if !keep {
			if err := store.Delete(ctx, row.StorageKey); err != nil {
				return fmt.Errorf("authoring: delete the archive of a workspace export that is gone: %w", err)
			}
		}
	}
	return nil
}

// ExpireWorkspaceExport deletes the export id and its file, once its time has
// passed, as Expire does an authoring export's: it ends the export first, then
// deletes the file, then the row.
func (s *Service) ExpireWorkspaceExport(ctx context.Context, ws, id ids.UUID) error {
	var key string
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := q.WorkspaceExportOfJob(ctx, queries.WorkspaceExportOfJobParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		key = row.StorageKey
		_, err = q.FailWorkspaceExport(ctx, queries.FailWorkspaceExportParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("authoring: end the workspace export: %w", err)
	}
	if key == "" {
		return nil
	}
	store, err := s.storage.Store(ctx)
	if err != nil {
		return fmt.Errorf("authoring: open the store: %w", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		return fmt.Errorf("authoring: delete the workspace's archive: %w", err)
	}
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		_, err := queries.New(tx).DeleteWorkspaceExport(ctx, queries.DeleteWorkspaceExportParams{WorkspaceID: uuid(ws),
			ID: uuid(id)})
		return err
	})
	if err != nil {
		return fmt.Errorf("authoring: delete the workspace export: %w", err)
	}
	return nil
}
