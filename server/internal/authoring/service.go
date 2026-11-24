package authoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/asset"
	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// Retention is how long an export is kept from when it was asked for, after
// which its job deletes it and its file (note N90).
const Retention = 7 * 24 * time.Hour

// buildAttempts is how many attempts may build an export. A failure that
// another attempt would repeat fails it at once (endsTheExport).
const buildAttempts = 4

// failAttempts is how many attempts follow those, which only fail the export,
// so that no failure leaves it on its way, which would keep its user from
// asking for another. There are several, as River's rescuer discards a job
// whose last attempt the process's death cut off: an attempt that dies as it
// fails the export leaves that to the next.
const failAttempts = 3

// buildTimeout is how long each attempt has: a build copies a project's every
// release and asset, up to the gigabytes that an import takes, and it is
// within the hour after which River's rescuer would take a job for stuck. An
// attempt that runs out of it fails the export, as the next would too, and
// would keep the other exports of the instance, which run one at a time,
// waiting for hours.
const buildTimeout = 50 * time.Minute

// failTimeout is how long failing an export may take, on a context of its own,
// as the build's has ended if the build ran out of its time.
const failTimeout = 30 * time.Second

// failRetry is how long a build's job waits before it tries again to fail its
// export, when the attempt that should have done so could not. The job is
// snoozed rather than left to run out of attempts, as a build whose export
// stays on its way keeps its user from asking for another.
const failRetry = time.Minute

// expireAttempts is how many attempts an expiry job has: the most that River's
// column holds, which is years of them at the queue's longest wait of an hour.
// River discards a job on its last attempt, even one that the process's death
// cut off, and nothing else would then delete the file of an export that a row
// still names, so a store that stays down must not bring the job near it.
const expireAttempts = math.MaxInt16

// insertAttempts is how many times Create tries to add an export when each
// attempt finds one on its way that has ended by the time it is read.
const insertAttempts = 3

// The statuses of an export.
const (
	StatusQueued   = "queued"
	StatusBuilding = "building"
	StatusReady    = "ready"
	StatusFailed   = "failed"
)

// ErrNotFound is the error of an export that the project does not have, or
// that another user asked for, whose existence is not told.
var ErrNotFound = errors.New("authoring: no such export of the user's")

// InProgressError is Create's error for a user who has an export of the
// project on its way, and CreateWorkspaceExport's for one who has an export of
// the workspace on its way, each of which reads what it exports whole.
type InProgressError struct {
	// ID is that export's public ID.
	ID string
}

func (e *InProgressError) Error() string {
	return "authoring: the user has an export on its way: " + e.ID
}

// NotReadyError is Open's error for an export that has no file to download.
type NotReadyError struct {
	// Status is the export's status.
	Status string
}

func (e *NotReadyError) Error() string {
	return "authoring: the export is " + e.Status + ", and only a ready one has a file"
}

// DB runs the transactions of the service.
type DB = project.SnapshotDB

// Options configure a Service.
type Options struct {
	Projects *project.Service
	Queue    commands.Enqueuer
	Storage  *asset.Storage
	// ImportMaxBytes is the instance's import limit, which an export is
	// compared with (FR-DATA-01).
	ImportMaxBytes int64
	// Assets store an import's assets as uploads are stored, and Pipeline
	// applies its draft through the commands (plan item 84).
	Assets   *asset.Service
	Pipeline *commands.Pipeline
	// IDsInUse is the import's collision check, of the maintenance path
	// (PRD §5.2), which answers one boolean.
	IDsInUse func(ctx context.Context, list []ids.UUID) (bool, error)
	Log      *slog.Logger
}

// Service keeps the authoring exports.
type Service struct {
	db             DB
	projects       *project.Service
	queue          commands.Enqueuer
	storage        *asset.Storage
	importMaxBytes int64
	assets         *asset.Service
	pipeline       *commands.Pipeline
	idsInUse       func(ctx context.Context, list []ids.UUID) (bool, error)
	log            *slog.Logger
}

// New returns a Service over db.
func New(db DB, o Options) *Service {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, projects: o.Projects, queue: o.Queue, storage: o.Storage,
		importMaxBytes: o.ImportMaxBytes, assets: o.Assets, pipeline: o.Pipeline, idsInUse: o.IDsInUse, log: log}
}

// Request is what an export is asked for.
type Request struct {
	// SnapshotID is the release to export, or nil for the draft.
	SnapshotID      *ids.UUID
	Profile         Profile
	IncludeAssets   bool
	IncludeReleases bool
}

// Export is an export, as the API shows it.
type Export struct {
	ID              string
	ProjectID       string
	SnapshotID      string
	Status          string
	Profile         Profile
	IncludeAssets   bool
	IncludeReleases bool
	PersonalData    bool
	SizeBytes       *int64
	// AssetCount is how many assets a ready archive holds, if it was counted.
	AssetCount     *int32
	ImportMaxBytes int64
	FileName       string
	CreatedAt      time.Time
	FinishedAt     *time.Time
	ExpiresAt      time.Time
}

// MaxImportAssets is the most assets that an archive may hold for an import
// to take it (maxImportAssets), which an export warns of.
const MaxImportAssets = maxImportAssets

// ExceedsImportLimit says whether this instance could not import the archive
// again: it is larger than the instance's import limit, or holds more assets
// than an import takes.
func (e Export) ExceedsImportLimit() bool {
	return e.SizeBytes != nil && *e.SizeBytes > e.ImportMaxBytes || e.AssetCount != nil && *e.AssetCount > MaxImportAssets
}

func (s *Service) exportOf(row queries.AuthoringExport) Export {
	e := Export{ID: ids.Encode(ids.Job, row.ID.Bytes), ProjectID: ids.Encode(ids.Project, row.ProjectID.Bytes),
		SnapshotID: encode(ids.Snapshot, row.SnapshotID), Status: row.Status, Profile: Profile(row.Profile),
		IncludeAssets: row.IncludeAssets, IncludeReleases: row.IncludeReleases,
		PersonalData: Profile(row.Profile) == ProfileFull, ImportMaxBytes: s.importMaxBytes,
		FileName: row.FileName.String, CreatedAt: row.CreatedAt.Time.UTC(), FinishedAt: timeOf(row.FinishedAt),
		ExpiresAt: row.ExpiresAt.Time.UTC()}
	if row.SizeBytes.Valid {
		e.SizeBytes = &row.SizeBytes.Int64
	}
	if row.AssetCount.Valid {
		e.AssetCount = &row.AssetCount.Int32
	}
	return e
}

// storageKey returns the key of the archive of the export id of the workspace
// ws: of the hash of its ID, so that the orphan reconciliation reads it as a
// blob of the workspace, and no other row shares it.
func storageKey(ws, id ids.UUID) string {
	sum := sha256.Sum256(id[:])
	return ws.String() + "/" + hex.EncodeToString(sum[:]) + ".export.zip"
}

// Create asks for an export of the project projectID, which a job builds,
// once the actor may read the project, and records it in the audit log, in
// one transaction. It returns the errors of project.Service.Get,
// asset.ErrSetupRequired while first-run setup may yet choose the store that
// the archive would be left behind in, an *InProgressError where the user has
// one on its way, and, for a release, snapshot.ErrNotFound or a
// *snapshot.NotReadyError. The token's export:read is the route's to check.
func (s *Service) Create(ctx context.Context, actor commands.Actor, projectID ids.UUID, req Request) (Export, error) {
	return s.create(ctx, actor, projectID, req, time.Time{})
}

// create is Create, whose build job is due at buildAt, or at once for the zero
// time.
func (s *Service) create(ctx context.Context, actor commands.Actor, projectID ids.UUID, req Request,
	buildAt time.Time) (Export, error) {
	// Outside the transaction, as reading the store's settings takes a
	// connection of its own, and after the project, so that who may not read
	// it learns nothing of the instance.
	if _, err := s.projects.Get(ctx, actor, projectID); err != nil {
		return Export{}, err
	}
	if _, err := s.storage.FinalStore(ctx); err != nil {
		return Export{}, err
	}
	id := ids.New()
	var out Export
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		q := queries.New(tx)
		snapshotID := pgtype.UUID{}
		if req.SnapshotID != nil {
			snapshotID = pgtype.UUID{Bytes: *req.SnapshotID, Valid: true}
			row, err := q.Snapshot(ctx, queries.SnapshotParams{WorkspaceID: uuid(ws), ProjectID: uuid(projectID),
				ID: snapshotID})
			if errors.Is(err, pgx.ErrNoRows) {
				return snapshot.ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("authoring: read the release: %w", err)
			}
			if status := snapshot.Status(row.Status); status != snapshot.Ready {
				return &snapshot.NotReadyError{Status: status}
			}
		}
		expires := time.Now().Add(Retention)
		row, err := insert(ctx, q, queries.InsertAuthoringExportParams{ID: uuid(id), WorkspaceID: uuid(ws),
			ProjectID: uuid(projectID), RequestedBy: uuid(actor.UserID), SnapshotID: snapshotID,
			Profile: string(req.Profile), IncludeAssets: req.IncludeAssets, IncludeReleases: req.IncludeReleases,
			StorageKey: storageKey(ws, id), ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
		if err != nil {
			return err
		}
		source := "draft"
		if req.SnapshotID != nil {
			source = ids.Encode(ids.Snapshot, *req.SnapshotID)
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionProjectExport, WorkspaceID: ws,
			ActorUserID: actor.UserID, ActorTokenID: actor.Token.TokenID(), SubjectID: projectID,
			Result: audit.ResultSuccess, IP: actor.Client.IP, UserAgent: actor.Client.UserAgent,
			Metadata: map[string]any{"export_id": ids.Encode(ids.Job, id), "source": source,
				"profile": string(req.Profile), "include_assets": req.IncludeAssets,
				"include_releases": req.IncludeReleases}}); err != nil {
			return err
		}
		args := jobArgs{WorkspaceID: ws.String(), ExportID: id.String()}
		if _, err := s.queue.Enqueue(ctx, tx, buildJob(args), &jobs.EnqueueOptions{At: buildAt,
			MaxAttempts: buildAttempts + failAttempts}); err != nil {
			return fmt.Errorf("authoring: enqueue the build: %w", err)
		}
		if _, err := s.queue.Enqueue(ctx, tx, expireJob(args), &jobs.EnqueueOptions{At: expires,
			MaxAttempts: expireAttempts}); err != nil {
			return fmt.Errorf("authoring: enqueue the expiry: %w", err)
		}
		out = s.exportOf(row)
		return nil
	})
	if err != nil {
		return Export{}, err
	}
	return out, nil
}

// insert adds the export p, or returns the *InProgressError of the user's
// export of the project that is on its way. One that stopped the insert and
// ended before it was read leaves the way clear, so the insert is made again.
func insert(ctx context.Context, q *queries.Queries, p queries.InsertAuthoringExportParams) (queries.AuthoringExport,
	error) {
	for range insertAttempts {
		row, err := q.InsertAuthoringExport(ctx, p)
		if err == nil {
			return row, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return row, fmt.Errorf("authoring: add the export: %w", err)
		}
		// One past its time, which its user can no longer read, holds the way
		// no longer. Its expiry job still deletes it, and its file.
		if err := q.FailExpiredAuthoringExports(ctx, queries.FailExpiredAuthoringExportsParams{
			WorkspaceID: p.WorkspaceID, ProjectID: p.ProjectID, RequestedBy: p.RequestedBy}); err != nil {
			return row, fmt.Errorf("authoring: end the user's export past its time: %w", err)
		}
		running, err := q.AuthoringExportInProgress(ctx, queries.AuthoringExportInProgressParams{
			WorkspaceID: p.WorkspaceID, ProjectID: p.ProjectID, RequestedBy: p.RequestedBy})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return row, fmt.Errorf("authoring: read the user's export on its way: %w", err)
		}
		return row, &InProgressError{ID: ids.Encode(ids.Job, running.Bytes)}
	}
	return queries.AuthoringExport{}, errors.New("authoring: the user's exports kept ending as this one was added")
}

func uuid(id ids.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// Get returns the export id of the project, if the actor asked for it and may
// still read the project. It returns the errors of project.Service.Get, and
// ErrNotFound.
func (s *Service) Get(ctx context.Context, actor commands.Actor, projectID, id ids.UUID) (Export, error) {
	var out Export
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		row, err := own(ctx, tx, ws, projectID, actor.UserID, id)
		if err != nil {
			return err
		}
		out = s.exportOf(row)
		return nil
	})
	return out, err
}

// own returns the export id of the project that user asked for.
func own(ctx context.Context, tx pgx.Tx, ws, projectID, user, id ids.UUID) (queries.AuthoringExport, error) {
	row, err := queries.New(tx).AuthoringExport(ctx, queries.AuthoringExportParams{WorkspaceID: uuid(ws),
		ProjectID: uuid(projectID), RequestedBy: uuid(user), ID: uuid(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("authoring: read the export: %w", err)
	}
	return row, nil
}

// List returns the actor's exports of the project, newest first, after the
// export after, or from the newest for the zero UUID, at most limit of them.
func (s *Service) List(ctx context.Context, actor commands.Actor, projectID, after ids.UUID, limit int) ([]Export,
	error) {
	var out []Export
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		rows, err := queries.New(tx).AuthoringExports(ctx, queries.AuthoringExportsParams{WorkspaceID: uuid(ws),
			ProjectID: uuid(projectID), RequestedBy: uuid(actor.UserID), After: uuid(after),
			RowLimit: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("authoring: list the exports: %w", err)
		}
		out = make([]Export, len(rows))
		for i, r := range rows {
			out[i] = s.exportOf(r)
		}
		return nil
	})
	return out, err
}

// Download is the file of a ready export, to read and then close.
type Download struct {
	Body io.ReadCloser
	Size int64
	Name string
}

// Open returns the archive of the export id, if the actor asked for it and
// may still read the project. It returns the errors of Get, and a
// *NotReadyError for an export without a file. It holds no transaction while
// the file is read.
func (s *Service) Open(ctx context.Context, actor commands.Actor, projectID, id ids.UUID) (Download, error) {
	var row queries.AuthoringExport
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		var err error
		row, err = own(ctx, tx, ws, projectID, actor.UserID, id)
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
		return Download{}, fmt.Errorf("authoring: open the archive: %w", err)
	}
	return Download{Body: body, Size: info.Size, Name: row.FileName.String}, nil
}

// jobArgs are the arguments of the jobs of an export.
type jobArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ExportID    string `json:"export_id"`
}

// buildJob builds an export's archive (FR-JOBS-01). Its jobs run one at a time
// in a process, on a queue of their own, since each reads a project whole.
type buildJob jobArgs

func (buildJob) Kind() string { return "authoring_export_build" }

// expireJob deletes an export once its time has passed.
type expireJob jobArgs

func (expireJob) Kind() string { return "authoring_export_expire" }

// RegisterJobs registers the jobs of the exports in r.
func (s *Service) RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[buildJob]) error {
		workspaceID, id, err := parseArgs(jobArgs(job.Args))
		if err != nil {
			return jobs.Cancel(err)
		}
		svc := s.withDB(job.DB)
		if onlyFails(job.Attempt, job.Snoozes) {
			return svc.fail(ctx, workspaceID, id, errors.New("the build failed, and its attempts are over"))
		}
		err = svc.Build(ctx, workspaceID, id)
		if endsTheExport(ctx, err) {
			return svc.fail(ctx, workspaceID, id, err)
		}
		return err
	})
	jobs.Serial(r, buildJob{})
	jobs.Timeout(r, buildJob{}, buildTimeout)
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[expireJob]) error {
		workspaceID, id, err := parseArgs(jobArgs(job.Args))
		if err != nil {
			return jobs.Cancel(err)
		}
		svc := s.withDB(job.DB)
		err = svc.Expire(ctx, workspaceID, id)
		if err != nil {
			svc.log.WarnContext(ctx, "the authoring export could not be deleted, and its job will try again",
				"export_id", ids.Encode(ids.Job, id), "attempt", job.Attempt, "error", err)
		}
		return err
	})
	s.registerImportJobs(r)
	s.registerWorkspaceJobs(r)
}

func parseArgs(a jobArgs) (ws, id ids.UUID, err error) {
	if ws, err = ids.ParseUUID(a.WorkspaceID); err != nil {
		return ws, id, fmt.Errorf("authoring: the job's workspace: %w", err)
	}
	if id, err = ids.ParseUUID(a.ExportID); err != nil {
		return ws, id, fmt.Errorf("authoring: the job's export: %w", err)
	}
	return ws, id, nil
}

// withDB returns a Service like s on db, the workers' pool of a job, and its
// assets' service too, whose transactions an import's job then runs there.
func (s *Service) withDB(db *database.DB) *Service {
	if db == nil {
		return s
	}
	c := *s
	c.db = db
	c.assets = s.assets.WithDB(db)
	return &c
}

// onlyFails says whether a run of a build's job only fails the export: one of
// the attempts after those that may build, or one after a snooze, which only
// fail takes, when the database did not take the failure. River gives a
// snoozed job its attempt's number back, so that run would otherwise build
// again, and could hold the serial queue for another buildTimeout.
func onlyFails(attempt, snoozes int) bool { return attempt > buildAttempts || snoozes > 0 }

// endsTheExport says whether err, of a build that ran under ctx, is a failure
// that another attempt would repeat: a project in the trash, a workspace that
// is no longer active, an asset whose file is gone, or the attempt's own
// timeout. The server's stopping cancels ctx instead, and the build then runs
// again after the restart.
func endsTheExport(ctx context.Context, err error) bool {
	return errors.Is(err, errProjectGone) || errors.Is(err, errWorkspaceGone) ||
		errors.Is(err, errAssetMissing) || (err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded))
}

// fail fails the export, deletes what an attempt of it stored, and ends its
// job. Where the database does not take that, the job runs again rather than
// end with the export still on its way. An export that has ended, as a ready
// one whose job ran again, keeps its file, and its job completes, as nothing
// failed.
func (s *Service) fail(ctx context.Context, ws, id ids.UUID, why error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failTimeout)
	defer cancel()
	var key string
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		var err error
		key, err = queries.New(tx).FailAuthoringExport(ctx, queries.FailAuthoringExportParams{WorkspaceID: uuid(ws),
			ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			key = ""
			return nil
		}
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "the authoring export could not be failed, and its job will try again",
			"export_id", ids.Encode(ids.Job, id), "error", err)
		return jobs.Snooze(failRetry)
	}
	if key == "" {
		s.log.InfoContext(ctx, "the authoring export had ended when its job came to fail it",
			"export_id", ids.Encode(ids.Job, id))
		return nil
	}
	s.dropFailed(ctx, "authoring export", id, key)
	s.log.ErrorContext(ctx, "the authoring export failed", "export_id", ids.Encode(ids.Job, id), "error", why)
	return jobs.Cancel(why)
}

// dropFailed deletes the archive of a failed export, which an attempt may have
// stored before it could record that, and which nobody can download. A store
// that does not take that leaves it to the export's expiry, so it only logs,
// naming the export as kind, an authoring export or a workspace export.
func (s *Service) dropFailed(ctx context.Context, kind string, id ids.UUID, key string) {
	store, err := s.storage.Store(ctx)
	if err == nil {
		err = store.Delete(ctx, key)
	}
	if err != nil {
		s.log.WarnContext(ctx, "the archive of a failed "+kind+" stays until the export expires",
			"export_id", ids.Encode(ids.Job, id), "error", err)
	}
}

// Build builds the archive of the export id of the workspace ws, unless it
// has ended, and stores it under the export's key, then makes the export
// ready. It is safe to repeat: an attempt writes the whole archive again, and
// the store keeps a failed one from being seen. An export deleted meanwhile
// leaves no file behind: an attempt that finds its row gone deletes the file
// again, as the attempt before may have failed to.
func (s *Service) Build(ctx context.Context, ws, id ids.UUID) (err error) {
	var row queries.AuthoringExport
	started, gone := false, false
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		var err error
		row, err = q.AuthoringExportOfJob(ctx, queries.AuthoringExportOfJobParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			gone = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("authoring: read the export: %w", err)
		}
		n, err := q.StartAuthoringExport(ctx, queries.StartAuthoringExportParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if err != nil {
			return fmt.Errorf("authoring: start the export: %w", err)
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
		if err := store.Delete(ctx, storageKey(ws, id)); err != nil {
			return fmt.Errorf("authoring: delete the archive of an export that is gone: %w", err)
		}
		return nil
	case !started:
		return nil
	}
	c, err := s.read(ctx, row)
	if err != nil {
		return err
	}
	// The final store, which Create has checked that there is, as an archive
	// stored anywhere else could be neither downloaded nor deleted.
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return fmt.Errorf("authoring: open the store: %w", err)
	}
	w, err := store.Create(ctx, row.StorageKey, "application/zip")
	if err != nil {
		return fmt.Errorf("authoring: start the archive: %w", err)
	}
	defer func() { _ = w.Abort() }()
	counted := &countingWriter{w: w}
	if err := s.write(ctx, counted, store, row, c); err != nil {
		return err
	}
	if err := w.Commit(); err != nil {
		return fmt.Errorf("authoring: store the archive: %w", err)
	}
	var finished int64
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		var err error
		finished, err = queries.New(tx).FinishAuthoringExport(ctx, queries.FinishAuthoringExportParams{
			WorkspaceID: uuid(ws), ID: uuid(id), SizeBytes: pgtype.Int8{Int64: counted.n, Valid: true},
			AssetCount: pgtype.Int4{Int32: int32(len(c.assets)), Valid: true},
			FileName:   pgtype.Text{String: c.fileName, Valid: true}})
		return err
	})
	if err != nil {
		return fmt.Errorf("authoring: finish the export: %w", err)
	}
	if finished == 0 {
		// The export was deleted, or ended, while it was being built, so
		// nothing refers to what was stored.
		if err := s.dropUnreferenced(ctx, store, ws, id, row.StorageKey); err != nil {
			return err
		}
	}
	return nil
}

// dropUnreferenced deletes the archive of key unless a row of the export is
// ready, as when the row was deleted during the build.
func (s *Service) dropUnreferenced(ctx context.Context, store blob.Store, ws, id ids.UUID, key string) error {
	keep := false
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		row, err := queries.New(tx).AuthoringExportOfJob(ctx, queries.AuthoringExportOfJobParams{WorkspaceID: uuid(ws),
			ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		keep = row.Status == StatusReady
		return nil
	})
	if err != nil {
		return fmt.Errorf("authoring: read the export: %w", err)
	}
	if keep {
		return nil
	}
	if err := store.Delete(ctx, key); err != nil {
		return fmt.Errorf("authoring: delete the archive of an export that is gone: %w", err)
	}
	return nil
}

// Expire deletes the export id and its file, once its time has passed: the
// file first, so that a store that fails leaves the row, whose key the job's
// next attempt deletes, and which no read shows past its time. It first ends
// an export that is still on its way, so that a build that stores its archive
// after the deletion finds it ended, and deletes that archive too.
func (s *Service) Expire(ctx context.Context, ws, id ids.UUID) error {
	var key string
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := q.AuthoringExportOfJob(ctx, queries.AuthoringExportOfJobParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		key = row.StorageKey
		_, err = q.FailAuthoringExport(ctx, queries.FailAuthoringExportParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("authoring: end the export: %w", err)
	}
	if key == "" {
		return nil
	}
	store, err := s.storage.Store(ctx)
	if err != nil {
		return fmt.Errorf("authoring: open the store: %w", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		return fmt.Errorf("authoring: delete the archive: %w", err)
	}
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		_, err := queries.New(tx).DeleteAuthoringExport(ctx, queries.DeleteAuthoringExportParams{WorkspaceID: uuid(ws),
			ID: uuid(id)})
		return err
	})
	if err != nil {
		return fmt.Errorf("authoring: delete the export: %w", err)
	}
	return nil
}

// countingWriter counts what it passes on.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
