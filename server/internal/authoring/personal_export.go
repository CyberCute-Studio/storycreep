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

// The personal-data exports (PRD FR-GDPR-01, plan item 87, note N94): a user
// asks for a zip of what the instance holds of them, which a job builds into
// the assets' store as a workspace export's is (personal_archive.go), reading
// the user's rows of every workspace through the maintenance path (Options'
// Personal), and which that user alone reads and downloads, signed in, until
// it expires, Retention after it was asked for. Their jobs have an authoring
// export's attempts and times, and fail and expire alike. An export is the
// account's, of no workspace, so its row has no row-level security, and every
// read of it names its user.

// PersonalExport is a personal-data export, as the API shows it.
type PersonalExport struct {
	ID         string
	Status     string
	SizeBytes  *int64
	FileName   string
	CreatedAt  time.Time
	FinishedAt *time.Time
	ExpiresAt  time.Time
}

func personalExportOf(row queries.PersonalDataExport) PersonalExport {
	e := PersonalExport{ID: ids.Encode(ids.Job, row.ID.Bytes), Status: row.Status, FileName: row.FileName.String,
		CreatedAt: row.CreatedAt.Time.UTC(), FinishedAt: timeOf(row.FinishedAt), ExpiresAt: row.ExpiresAt.Time.UTC()}
	if row.SizeBytes.Valid {
		e.SizeBytes = &row.SizeBytes.Int64
	}
	return e
}

// personalKeyPrefix is the part of the assets' store that the personal-data
// exports' archives are kept in, which is no workspace's, so that no
// workspace's orphan reconciliation lists them: the build deletes the file of
// an export that is gone, the expiry job deletes the file before the row, and
// the account's hard deletion deletes its exports' files (plan item 111).
const personalKeyPrefix = "personal-data/"

// personalStorageKey returns the key of the archive of the personal-data
// export id, which no other row's key shares.
func personalStorageKey(id ids.UUID) string {
	sum := sha256.Sum256(id[:])
	return personalKeyPrefix + hex.EncodeToString(sum[:]) + ".personal-export.zip"
}

// asUser runs fn in a transaction of the user's own, of no workspace.
func (s *Service) asUser(ctx context.Context, user ids.UUID, fn func(tx pgx.Tx) error) error {
	return s.db.InTx(ctx, database.Scope{UserID: user}, fn)
}

// CreatePersonalExport asks for an export of the actor's personal data, which
// a job builds, and records it in the audit log, in one transaction. It
// returns asset.ErrSetupRequired while first-run setup may yet choose the
// store that the archive would be left behind in, and an *InProgressError
// where the user has one on its way.
func (s *Service) CreatePersonalExport(ctx context.Context, actor commands.Actor) (PersonalExport, error) {
	return s.createPersonalExport(ctx, actor, time.Time{})
}

// createPersonalExport is CreatePersonalExport, whose build job is due at
// buildAt, or at once for the zero time.
func (s *Service) createPersonalExport(ctx context.Context, actor commands.Actor, buildAt time.Time) (
	PersonalExport, error) {
	// Outside the transaction that adds the export, as reading the store's
	// settings takes a connection of its own.
	if _, err := s.storage.FinalStore(ctx); err != nil {
		return PersonalExport{}, err
	}
	id := ids.New()
	var out PersonalExport
	err := s.asUser(ctx, actor.UserID, func(tx pgx.Tx) error {
		q := queries.New(tx)
		expires := time.Now().Add(Retention)
		row, err := insertPersonalExport(ctx, q, queries.InsertPersonalDataExportParams{ID: uuid(id),
			UserID: uuid(actor.UserID), StorageKey: personalStorageKey(id),
			ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionPersonalDataExport,
			ActorUserID: actor.UserID, ActorTokenID: actor.Token.TokenID(), SubjectID: actor.UserID,
			Result: audit.ResultSuccess, IP: actor.Client.IP, UserAgent: actor.Client.UserAgent,
			Metadata: map[string]any{"export_id": ids.Encode(ids.Job, id)}}); err != nil {
			return err
		}
		args := personalJobArgs{UserID: actor.UserID.String(), ExportID: id.String()}
		if _, err := s.queue.Enqueue(ctx, tx, personalBuildJob(args), &jobs.EnqueueOptions{At: buildAt,
			MaxAttempts: buildAttempts + failAttempts}); err != nil {
			return fmt.Errorf("authoring: enqueue the personal-data export's build: %w", err)
		}
		if _, err := s.queue.Enqueue(ctx, tx, personalExpireJob(args), &jobs.EnqueueOptions{At: expires,
			MaxAttempts: expireAttempts}); err != nil {
			return fmt.Errorf("authoring: enqueue the personal-data export's expiry: %w", err)
		}
		out = personalExportOf(row)
		return nil
	})
	if err != nil {
		return PersonalExport{}, err
	}
	return out, nil
}

// insertPersonalExport adds the export p, or returns the *InProgressError of
// the user's export that is on its way, as insert does an authoring export's.
func insertPersonalExport(ctx context.Context, q *queries.Queries, p queries.InsertPersonalDataExportParams) (
	queries.PersonalDataExport, error) {
	for range insertAttempts {
		row, err := q.InsertPersonalDataExport(ctx, p)
		if err == nil {
			return row, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return row, fmt.Errorf("authoring: add the personal-data export: %w", err)
		}
		// One past its time, which its user can no longer read, holds the way
		// no longer. Its expiry job still deletes it, and its file.
		if err := q.FailExpiredPersonalDataExports(ctx, p.UserID); err != nil {
			return row, fmt.Errorf("authoring: end the user's personal-data export past its time: %w", err)
		}
		running, err := q.PersonalDataExportInProgress(ctx, p.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return row, fmt.Errorf("authoring: read the user's personal-data export on its way: %w", err)
		}
		return row, &InProgressError{ID: ids.Encode(ids.Job, running.Bytes)}
	}
	return queries.PersonalDataExport{}, errors.New(
		"authoring: the user's personal-data exports kept ending as this one was added")
}

// GetPersonalExport returns the actor's export id, or ErrNotFound for one of
// another user's, or one whose time has passed.
func (s *Service) GetPersonalExport(ctx context.Context, actor commands.Actor, id ids.UUID) (PersonalExport, error) {
	var out PersonalExport
	err := s.asUser(ctx, actor.UserID, func(tx pgx.Tx) error {
		row, err := ownPersonalExport(ctx, tx, actor.UserID, id)
		if err != nil {
			return err
		}
		out = personalExportOf(row)
		return nil
	})
	return out, err
}

// ownPersonalExport returns the export id that user asked for.
func ownPersonalExport(ctx context.Context, tx pgx.Tx, user, id ids.UUID) (queries.PersonalDataExport, error) {
	row, err := queries.New(tx).PersonalDataExport(ctx, queries.PersonalDataExportParams{UserID: uuid(user),
		ID: uuid(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("authoring: read the personal-data export: %w", err)
	}
	return row, nil
}

// PersonalExports returns the actor's exports, newest first, after the export
// after, or from the newest for the zero UUID, at most limit of them.
func (s *Service) PersonalExports(ctx context.Context, actor commands.Actor, after ids.UUID, limit int) (
	[]PersonalExport, error) {
	var out []PersonalExport
	err := s.asUser(ctx, actor.UserID, func(tx pgx.Tx) error {
		rows, err := queries.New(tx).PersonalDataExports(ctx, queries.PersonalDataExportsParams{
			UserID: uuid(actor.UserID), After: uuid(after), RowLimit: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("authoring: list the personal-data exports: %w", err)
		}
		out = make([]PersonalExport, len(rows))
		for i, r := range rows {
			out[i] = personalExportOf(r)
		}
		return nil
	})
	return out, err
}

// OpenPersonalExport returns the archive of the actor's export id. It returns
// the errors of GetPersonalExport, and a *NotReadyError for an export without
// a file. It holds no transaction while the file is read.
func (s *Service) OpenPersonalExport(ctx context.Context, actor commands.Actor, id ids.UUID) (Download, error) {
	var row queries.PersonalDataExport
	err := s.asUser(ctx, actor.UserID, func(tx pgx.Tx) error {
		var err error
		row, err = ownPersonalExport(ctx, tx, actor.UserID, id)
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
		return Download{}, fmt.Errorf("authoring: open the personal-data archive: %w", err)
	}
	return Download{Body: body, Size: info.Size, Name: row.FileName.String}, nil
}

// personalJobArgs are the arguments of a personal-data export's jobs: the
// user, whose own rows are read, and the export.
type personalJobArgs struct {
	UserID   string `json:"user_id"`
	ExportID string `json:"export_id"`
}

func parsePersonalArgs(a personalJobArgs) (user, id ids.UUID, err error) {
	if user, err = ids.ParseUUID(a.UserID); err != nil {
		return user, id, fmt.Errorf("authoring: the job's user: %w", err)
	}
	if id, err = ids.ParseUUID(a.ExportID); err != nil {
		return user, id, fmt.Errorf("authoring: the job's export: %w", err)
	}
	return user, id, nil
}

// personalBuildJob builds a personal-data export's archive (FR-JOBS-01), one
// at a time in a process, on a queue of its own, as a workspace export's
// build does, since each reads all of a user's rows through the maintenance
// path's few connections.
type personalBuildJob personalJobArgs

func (personalBuildJob) Kind() string { return "personal_export_build" }

// personalExpireJob deletes a personal-data export once its time has passed.
type personalExpireJob personalJobArgs

func (personalExpireJob) Kind() string { return "personal_export_expire" }

// registerPersonalJobs registers the jobs of the personal-data exports in r.
func (s *Service) registerPersonalJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[personalBuildJob]) error {
		user, id, err := parsePersonalArgs(personalJobArgs(job.Args))
		if err != nil {
			return jobs.Cancel(err)
		}
		svc := s.withDB(job.DB)
		if onlyFails(job.Attempt, job.Snoozes) {
			return svc.failPersonalExport(ctx, user, id, errors.New("the build failed, and its attempts are over"))
		}
		err = svc.BuildPersonalExport(ctx, user, id)
		if endsTheExport(ctx, err) {
			return svc.failPersonalExport(ctx, user, id, err)
		}
		return err
	})
	jobs.Serial(r, personalBuildJob{})
	jobs.Timeout(r, personalBuildJob{}, buildTimeout)
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[personalExpireJob]) error {
		user, id, err := parsePersonalArgs(personalJobArgs(job.Args))
		if err != nil {
			return jobs.Cancel(err)
		}
		svc := s.withDB(job.DB)
		err = svc.ExpirePersonalExport(ctx, user, id)
		if err != nil {
			svc.log.WarnContext(ctx, "the personal-data export could not be deleted, and its job will try again",
				"export_id", ids.Encode(ids.Job, id), "attempt", job.Attempt, "error", err)
		}
		return err
	})
}

// failPersonalExport fails the export, deletes what an attempt of it stored,
// and ends its job, as fail does an authoring export's.
func (s *Service) failPersonalExport(ctx context.Context, user, id ids.UUID, why error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failTimeout)
	defer cancel()
	var key string
	err := s.asUser(ctx, user, func(tx pgx.Tx) error {
		var err error
		key, err = queries.New(tx).FailPersonalDataExport(ctx, queries.FailPersonalDataExportParams{
			UserID: uuid(user), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			key = ""
			return nil
		}
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "the personal-data export could not be failed, and its job will try again",
			"export_id", ids.Encode(ids.Job, id), "error", err)
		return jobs.Snooze(failRetry)
	}
	if key == "" {
		s.log.InfoContext(ctx, "the personal-data export had ended when its job came to fail it",
			"export_id", ids.Encode(ids.Job, id))
		return nil
	}
	s.dropFailed(ctx, "personal-data export", id, key)
	s.log.ErrorContext(ctx, "the personal-data export failed", "export_id", ids.Encode(ids.Job, id), "error", why)
	return jobs.Cancel(why)
}

// BuildPersonalExport builds the archive of the export id of the user, unless
// it has ended, and stores it under the export's key, then makes the export
// ready, as Build does an authoring export's: it is safe to repeat, and an
// export deleted meanwhile leaves no file behind.
func (s *Service) BuildPersonalExport(ctx context.Context, user, id ids.UUID) error {
	if s.personal == nil {
		return errors.New("authoring: the personal-data export has no reader of the workspaces")
	}
	var row queries.PersonalDataExport
	started, gone := false, false
	err := s.asUser(ctx, user, func(tx pgx.Tx) error {
		q := queries.New(tx)
		var err error
		row, err = q.PersonalDataExportOfJob(ctx, queries.PersonalDataExportOfJobParams{UserID: uuid(user),
			ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			gone = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("authoring: read the personal-data export: %w", err)
		}
		n, err := q.StartPersonalDataExport(ctx, queries.StartPersonalDataExportParams{UserID: uuid(user),
			ID: uuid(id)})
		if err != nil {
			return fmt.Errorf("authoring: start the personal-data export: %w", err)
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
		if err := store.Delete(ctx, personalStorageKey(id)); err != nil {
			return fmt.Errorf("authoring: delete the archive of a personal-data export that is gone: %w", err)
		}
		return nil
	case !started:
		return nil
	}
	c, err := s.readPersonal(ctx, row)
	if err != nil {
		return err
	}
	// The final store, which CreatePersonalExport has checked that there is.
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return fmt.Errorf("authoring: open the store: %w", err)
	}
	w, err := store.Create(ctx, row.StorageKey, "application/zip")
	if err != nil {
		return fmt.Errorf("authoring: start the personal-data archive: %w", err)
	}
	defer func() { _ = w.Abort() }()
	counted := &countingWriter{w: w}
	if err := s.writePersonal(ctx, counted, user, c); err != nil {
		return err
	}
	if err := w.Commit(); err != nil {
		return fmt.Errorf("authoring: store the personal-data archive: %w", err)
	}
	var finished int64
	err = s.asUser(ctx, user, func(tx pgx.Tx) error {
		var err error
		finished, err = queries.New(tx).FinishPersonalDataExport(ctx, queries.FinishPersonalDataExportParams{
			UserID: uuid(user), ID: uuid(id), SizeBytes: pgtype.Int8{Int64: counted.n, Valid: true},
			FileName: pgtype.Text{String: c.fileName, Valid: true}})
		return err
	})
	if err != nil {
		return fmt.Errorf("authoring: finish the personal-data export: %w", err)
	}
	if finished == 0 {
		// The export was deleted, or ended, while it was being built, so
		// nothing refers to what was stored, unless the export is ready.
		keep := false
		err := s.asUser(ctx, user, func(tx pgx.Tx) error {
			now, err := queries.New(tx).PersonalDataExportOfJob(ctx, queries.PersonalDataExportOfJobParams{
				UserID: uuid(user), ID: uuid(id)})
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
			return fmt.Errorf("authoring: read the personal-data export: %w", err)
		}
		if !keep {
			if err := store.Delete(ctx, row.StorageKey); err != nil {
				return fmt.Errorf("authoring: delete the archive of a personal-data export that is gone: %w", err)
			}
		}
	}
	return nil
}

// ExpirePersonalExport deletes the export id of the user and its file, once
// its time has passed, as Expire does an authoring export's: it ends the
// export first, then deletes the file, then the row. The file of an export
// whose row is gone, as the cascade of its account's deletion deletes it, is
// deleted all the same, as no workspace's orphan reconciliation lists it.
func (s *Service) ExpirePersonalExport(ctx context.Context, user, id ids.UUID) error {
	var key string
	err := s.asUser(ctx, user, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := q.PersonalDataExportOfJob(ctx, queries.PersonalDataExportOfJobParams{UserID: uuid(user),
			ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			key = personalStorageKey(id)
			return nil
		}
		if err != nil {
			return err
		}
		key = row.StorageKey
		_, err = q.FailPersonalDataExport(ctx, queries.FailPersonalDataExportParams{UserID: uuid(user), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("authoring: end the personal-data export: %w", err)
	}
	store, err := s.storage.Store(ctx)
	if err != nil {
		return fmt.Errorf("authoring: open the store: %w", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		return fmt.Errorf("authoring: delete the personal-data archive: %w", err)
	}
	err = s.asUser(ctx, user, func(tx pgx.Tx) error {
		_, err := queries.New(tx).DeletePersonalDataExport(ctx, queries.DeletePersonalDataExportParams{
			UserID: uuid(user), ID: uuid(id)})
		return err
	})
	if err != nil {
		return fmt.Errorf("authoring: delete the personal-data export: %w", err)
	}
	return nil
}
