package authoring

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/asset"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/comment"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/flow"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/playstate"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/search"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
)

// ChunkBytes is the size of each chunk of an upload but its last, which a
// client sends one at a time, and sends again after an interruption
// (FR-DATA-01 AC4). It is well below the API's largest request body.
const ChunkBytes = 8 << 20

// ImportRetention is how long an import is kept from when it was asked for,
// with what is left of its upload, after which its job deletes it.
const ImportRetention = 7 * 24 * time.Hour

// maxImportFileName is the longest file name of an import, in characters.
const maxImportFileName = 255

// importTimeout is how long each attempt of an import has: as for an export,
// within the hour after which River's rescuer would take the job for stuck.
const importTimeout = 50 * time.Minute

// The statuses of an import.
const (
	ImportUploading = "uploading"
	ImportQueued    = "queued"
	ImportImporting = "importing"
	ImportReady     = "ready"
	ImportFailed    = "failed"
)

// The ID policies (PRD §5.2).
const (
	PolicyAuto     = "auto"
	PolicyPreserve = "preserve"
	PolicyRemap    = "remap"
)

// importedName is the display name of an imported identity that the archive
// names no one for, as an ID in a comment's mention that people.json does not
// list.
const importedName = "Imported user"

// ImportInProgressError is CreateImport's error for a user who has an import
// of the workspace on its way, which they resume, or cancel.
type ImportInProgressError struct {
	// ID is that import's public ID.
	ID string
}

func (e *ImportInProgressError) Error() string {
	return "authoring: the user has an import of the workspace on its way: " + e.ID
}

// ImportTooLargeError is CreateImport's error for an archive past the
// instance's import limit.
type ImportTooLargeError struct {
	Size, Max int64
}

func (e *ImportTooLargeError) Error() string {
	return fmt.Sprintf("authoring: an archive of %d bytes is past the import limit of %d", e.Size, e.Max)
}

// ImportStateError is the error of a request that an import's status does not
// allow, as a chunk of one that is no longer uploading.
type ImportStateError struct {
	Status string
}

func (e *ImportStateError) Error() string {
	return "authoring: the import is " + e.Status
}

// ImportIncompleteError is CompleteImport's error for an upload that lacks
// chunks, the first of which it lists.
type ImportIncompleteError struct {
	Missing []int
}

func (e *ImportIncompleteError) Error() string {
	return fmt.Sprintf("authoring: the upload lacks %d chunks or more", len(e.Missing))
}

// ChunkError is PutChunk's error for a chunk that is not of the upload, by its
// index or by its size.
type ChunkError struct {
	Index    int
	Expected int64
	Got      int64
}

func (e *ChunkError) Error() string {
	return fmt.Sprintf("authoring: chunk %d should have %d bytes, and has %d", e.Index, e.Expected, e.Got)
}

// ImportRequest is what an import is asked for.
type ImportRequest struct {
	FileName  string
	SizeBytes int64
	// Remap asks for fresh IDs, whether or not the archive's are in use.
	Remap bool
}

// ImportReport is what a finished import says it did (FR-DATA-01). It says
// which ID policy was applied, which is the one bit of §5.2, and nothing else
// of the instance.
type ImportReport struct {
	Policy        string `json:"policy"`
	ProjectID     string `json:"project_id"`
	ProjectName   string `json:"project_name"`
	Boards        int    `json:"boards"`
	Nodes         int    `json:"nodes"`
	Variables     int    `json:"variables"`
	Components    int    `json:"components"`
	Comments      int    `json:"comments"`
	PlayStates    int    `json:"play_states"`
	Releases      int    `json:"releases"`
	Assets        int    `json:"assets"`
	ImportedUsers int    `json:"imported_users"`
	// MissingAssets counts the references to assets that an archive without
	// its assets could not keep, as a node's cover image.
	MissingAssets int `json:"missing_assets"`
}

// Import is an import, as the API shows it.
type Import struct {
	ID          string
	WorkspaceID string
	Status      string
	FileName    string
	SizeBytes   int64
	ChunkBytes  int
	ChunkCount  int
	// Received are the chunks that are stored, for a client to resume from,
	// while the upload is on its way.
	Received      []int
	IDPolicy      string
	AppliedPolicy string
	ProjectID     string
	Phase         string
	ProgressDone  int64
	ProgressTotal int64
	Report        *ImportReport
	Failure       *ImportError
	CreatedAt     time.Time
	FinishedAt    *time.Time
	ExpiresAt     time.Time
}

// chunkCount returns how many chunks an upload of size bytes has.
func chunkCount(size, chunk int64) int {
	return int((size + chunk - 1) / chunk)
}

// importOf returns the import of row, with the chunks that are stored.
func importOf(row queries.ProjectImport, received []int) (Import, error) {
	out := Import{ID: ids.Encode(ids.Job, row.ID.Bytes), WorkspaceID: ids.Encode(ids.Workspace, row.WorkspaceID.Bytes),
		Status: row.Status, FileName: row.FileName, SizeBytes: row.SizeBytes, ChunkBytes: int(row.ChunkBytes),
		ChunkCount: chunkCount(row.SizeBytes, int64(row.ChunkBytes)), Received: received, IDPolicy: row.IDPolicy,
		AppliedPolicy: row.AppliedPolicy.String, ProjectID: encode(ids.Project, row.CreatedProjectID), Phase: row.Phase,
		ProgressDone: row.ProgressDone, ProgressTotal: row.ProgressTotal, CreatedAt: row.CreatedAt.Time.UTC(),
		FinishedAt: timeOf(row.FinishedAt), ExpiresAt: row.ExpiresAt.Time.UTC()}
	if len(row.Report) > 0 {
		out.Report = &ImportReport{}
		if err := json.Unmarshal(row.Report, out.Report); err != nil {
			return out, fmt.Errorf("authoring: read the import's report: %w", err)
		}
	}
	if len(row.Failure) > 0 {
		out.Failure = &ImportError{}
		if err := json.Unmarshal(row.Failure, out.Failure); err != nil {
			return out, fmt.Errorf("authoring: read the import's failure: %w", err)
		}
	}
	return out, nil
}

// chunkKey returns a new key for a chunk of the import: of the hash of its ID,
// its index, and a fresh ID, so that the orphan reconciliation reads it as a
// blob of the workspace, no other row shares it, and each time that a chunk is
// sent it is a blob of its own. A blob that a row names never changes, so a
// request that outlives the upload cannot change what the job has verified.
func chunkKey(ws, id ids.UUID, index int) string {
	h := sha256.New()
	h.Write(id[:])
	_ = binary.Write(h, binary.BigEndian, int64(index))
	attempt := ids.New()
	h.Write(attempt[:])
	return ws.String() + "/" + hex.EncodeToString(h.Sum(nil)) + ".import.part"
}

// CreateImport begins the import of an archive into the workspace: it checks
// that the actor may create a project there, and that the workspace has room
// for one, so that an upload that could only be refused does not begin (AC9),
// and that the archive is within the instance's limit. It returns the errors of
// project.Service.CheckCreate, asset.ErrSetupRequired while first-run setup may
// yet choose the store, an *ImportTooLargeError, and an *ImportInProgressError.
func (s *Service) CreateImport(ctx context.Context, actor commands.Actor, workspaceID ids.UUID,
	req ImportRequest) (Import, error) {
	if err := s.checkImport(ctx, actor, workspaceID, &req); err != nil {
		return Import{}, err
	}
	id := ids.New()
	policy := PolicyAuto
	if req.Remap {
		policy = PolicyRemap
	}
	var out Import
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		expires := time.Now().Add(ImportRetention)
		p := queries.InsertProjectImportParams{ID: uuid(id), WorkspaceID: uuid(workspaceID),
			RequestedBy: uuid(actor.UserID), FileName: req.FileName, SizeBytes: req.SizeBytes, ChunkBytes: ChunkBytes,
			IDPolicy: policy, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}}
		row, err := insertImport(ctx, q, p)
		if err != nil {
			return err
		}
		args := importArgs{WorkspaceID: workspaceID.String(), ImportID: id.String()}
		if _, err := s.queue.Enqueue(ctx, tx, importExpireJob(args), &jobs.EnqueueOptions{At: expires,
			MaxAttempts: expireAttempts}); err != nil {
			return fmt.Errorf("authoring: enqueue the import's expiry: %w", err)
		}
		out, err = importOf(row, []int{})
		return err
	})
	if err != nil {
		return Import{}, err
	}
	return out, nil
}

// checkImport checks what comes before an archive is read: that the actor may
// create a project in the workspace, which has room for one, that the file's
// name is one, with the white space around it taken off, that the archive is
// within the instance's limit, and that there is a store to keep its assets.
func (s *Service) checkImport(ctx context.Context, actor commands.Actor, workspaceID ids.UUID,
	req *ImportRequest) error {
	if err := s.projects.CheckCreate(ctx, actor.UserID, workspaceID); err != nil {
		return err
	}
	req.FileName = strings.TrimSpace(req.FileName)
	if msg := input.CheckName(req.FileName, maxImportFileName); msg != "" {
		return input.FieldErrors{{Field: "file_name", Message: msg}}
	}
	if req.SizeBytes > s.importMaxBytes {
		return &ImportTooLargeError{Size: req.SizeBytes, Max: s.importMaxBytes}
	}
	_, err := s.storage.FinalStore(ctx)
	return err
}

// insertImport adds the import p, or returns the *ImportInProgressError of the
// user's import of the workspace that is on its way. One past its time holds
// the way no longer.
func insertImport(ctx context.Context, q *queries.Queries, p queries.InsertProjectImportParams) (queries.ProjectImport,
	error) {
	for range insertAttempts {
		row, err := q.InsertProjectImport(ctx, p)
		if err == nil {
			return row, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return row, fmt.Errorf("authoring: add the import: %w", err)
		}
		if err := q.FailExpiredProjectImports(ctx, queries.FailExpiredProjectImportsParams{
			WorkspaceID: p.WorkspaceID, RequestedBy: p.RequestedBy}); err != nil {
			return row, fmt.Errorf("authoring: end the user's import past its time: %w", err)
		}
		running, err := q.ProjectImportInProgress(ctx, queries.ProjectImportInProgressParams{
			WorkspaceID: p.WorkspaceID, RequestedBy: p.RequestedBy})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return row, fmt.Errorf("authoring: read the user's import on its way: %w", err)
		}
		return row, &ImportInProgressError{ID: ids.Encode(ids.Job, running.Bytes)}
	}
	return queries.ProjectImport{}, errors.New("authoring: the user's imports kept ending as this one was added")
}

// ownImport reads, in tx, the import id that the user asked for, once the
// user may still manage the workspace's projects.
func (s *Service) ownImport(ctx context.Context, tx pgx.Tx, user, ws, id ids.UUID) (queries.ProjectImport, error) {
	if err := s.projects.CheckManage(ctx, tx, user, ws); err != nil {
		return queries.ProjectImport{}, err
	}
	row, err := queries.New(tx).ProjectImport(ctx, queries.ProjectImportParams{WorkspaceID: uuid(ws),
		RequestedBy: uuid(user), ID: uuid(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("authoring: read the import: %w", err)
	}
	return row, nil
}

// GetImport returns the import id of the workspace, if the actor asked for it,
// with the chunks that are stored while it uploads.
func (s *Service) GetImport(ctx context.Context, actor commands.Actor, workspaceID, id ids.UUID) (Import, error) {
	var out Import
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		row, err := s.ownImport(ctx, tx, actor.UserID, workspaceID, id)
		if err != nil {
			return err
		}
		received := []int{}
		if row.Status == ImportUploading {
			chunks, err := queries.New(tx).ProjectImportChunks(ctx, queries.ProjectImportChunksParams{
				WorkspaceID: uuid(workspaceID), ImportID: uuid(id)})
			if err != nil {
				return fmt.Errorf("authoring: read the import's chunks: %w", err)
			}
			for _, c := range chunks {
				received = append(received, int(c.ChunkIndex))
			}
		}
		out, err = importOf(row, received)
		return err
	})
	return out, err
}

// ListImports returns the actor's imports of the workspace, newest first,
// after the import after, or from the newest for the zero UUID.
func (s *Service) ListImports(ctx context.Context, actor commands.Actor, workspaceID, after ids.UUID, limit int) (
	[]Import, error) {
	var out []Import
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		if err := s.projects.CheckManage(ctx, tx, actor.UserID, workspaceID); err != nil {
			return err
		}
		rows, err := queries.New(tx).ProjectImports(ctx, queries.ProjectImportsParams{WorkspaceID: uuid(workspaceID),
			RequestedBy: uuid(actor.UserID), After: uuid(after), RowLimit: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("authoring: list the imports: %w", err)
		}
		out = make([]Import, len(rows))
		for i, r := range rows {
			if out[i], err = importOf(r, []int{}); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// PutChunk stores the chunk index of the import's upload, which may be sent
// again, as a resumed upload may. It returns the errors of GetImport, an
// *ImportStateError unless the import is uploading, and a *ChunkError for an
// index or a size that is not the upload's.
func (s *Service) PutChunk(ctx context.Context, actor commands.Actor, workspaceID, id ids.UUID, index int,
	data []byte) error {
	scope := database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}
	var row queries.ProjectImport
	err := s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
		var err error
		row, err = s.ownImport(ctx, tx, actor.UserID, workspaceID, id)
		return err
	})
	if err != nil {
		return err
	}
	if row.Status != ImportUploading {
		return &ImportStateError{Status: row.Status}
	}
	chunk := int64(row.ChunkBytes)
	count := chunkCount(row.SizeBytes, chunk)
	if index < 0 || index >= count {
		return &ChunkError{Index: index, Got: int64(len(data))}
	}
	want := chunk
	if index == count-1 {
		want = row.SizeBytes - int64(count-1)*chunk
	}
	if int64(len(data)) != want {
		return &ChunkError{Index: index, Expected: want, Got: int64(len(data))}
	}
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return err
	}
	key := chunkKey(workspaceID, id, index)
	if err := store.Put(ctx, key, data, "application/octet-stream"); err != nil {
		return fmt.Errorf("authoring: store the chunk: %w", err)
	}
	replaced := ""
	err = s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
		q := queries.New(tx)
		status, err := q.LockProjectImportUpload(ctx, queries.LockProjectImportUploadParams{
			WorkspaceID: uuid(workspaceID), RequestedBy: uuid(actor.UserID), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("authoring: lock the import: %w", err)
		}
		if status != ImportUploading {
			return &ImportStateError{Status: status}
		}
		replaced, err = q.ProjectImportChunkKey(ctx, queries.ProjectImportChunkKeyParams{
			WorkspaceID: uuid(workspaceID), ImportID: uuid(id), ChunkIndex: int32(index)})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("authoring: read the chunk: %w", err)
		}
		return q.UpsertProjectImportChunk(ctx, queries.UpsertProjectImportChunkParams{WorkspaceID: uuid(workspaceID),
			ImportID: uuid(id), ChunkIndex: int32(index), Bytes: int32(len(data)), StorageKey: key})
	})
	if err != nil {
		// A refusal is sure to have recorded nothing. After any other failure
		// the commit may have gone through, so the blob is left to the orphan
		// reconciliation, which keeps it if a row names it.
		var state *ImportStateError
		if errors.As(err, &state) || errors.Is(err, ErrNotFound) {
			s.dropChunks(ctx, []string{key})
		}
		return err
	}
	if replaced != "" {
		s.dropChunks(ctx, []string{replaced})
	}
	return nil
}

// maxMissingListed is how many missing chunks an answer lists.
const maxMissingListed = 100

// CompleteImport hands a whole upload to the import's job. It returns the
// errors of GetImport and CheckCreate, an *ImportStateError unless the import
// is uploading, and an *ImportIncompleteError for an upload that lacks chunks.
func (s *Service) CompleteImport(ctx context.Context, actor commands.Actor, workspaceID, id ids.UUID) (Import, error) {
	if err := s.projects.CheckCreate(ctx, actor.UserID, workspaceID); err != nil {
		return Import{}, err
	}
	var out Import
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := s.ownImport(ctx, tx, actor.UserID, workspaceID, id)
		if err != nil {
			return err
		}
		if row.Status != ImportUploading {
			return &ImportStateError{Status: row.Status}
		}
		chunks, err := q.ProjectImportChunks(ctx, queries.ProjectImportChunksParams{WorkspaceID: uuid(workspaceID),
			ImportID: uuid(id)})
		if err != nil {
			return fmt.Errorf("authoring: read the import's chunks: %w", err)
		}
		count := chunkCount(row.SizeBytes, int64(row.ChunkBytes))
		have := make(map[int]bool, len(chunks))
		for _, c := range chunks {
			have[int(c.ChunkIndex)] = true
		}
		var missing []int
		for i := 0; i < count && len(missing) < maxMissingListed; i++ {
			if !have[i] {
				missing = append(missing, i)
			}
		}
		if len(missing) > 0 {
			return &ImportIncompleteError{Missing: missing}
		}
		n, err := q.QueueProjectImport(ctx, queries.QueueProjectImportParams{WorkspaceID: uuid(workspaceID),
			RequestedBy: uuid(actor.UserID), ID: uuid(id)})
		if err != nil {
			return fmt.Errorf("authoring: queue the import: %w", err)
		}
		if n == 0 {
			// Something ended the upload since it was read, as a cancel.
			now, err := q.ProjectImportOfJob(ctx, queries.ProjectImportOfJobParams{WorkspaceID: uuid(workspaceID),
				ID: uuid(id)})
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("authoring: read the import: %w", err)
			}
			return &ImportStateError{Status: now.Status}
		}
		args := importArgs{WorkspaceID: workspaceID.String(), ImportID: id.String()}
		if _, err := s.queue.Enqueue(ctx, tx, importRunJob(args), &jobs.EnqueueOptions{
			MaxAttempts: buildAttempts + failAttempts}); err != nil {
			return fmt.Errorf("authoring: enqueue the import: %w", err)
		}
		row.Status, row.Phase = ImportQueued, "queued"
		out, err = importOf(row, []int{})
		return err
	})
	if err != nil {
		return Import{}, err
	}
	return out, nil
}

// CancelImport ends an import that is uploading or waiting for its job, and
// deletes what it stored. An import that its job has begun runs to its end, as
// it is all or nothing. It returns the errors of GetImport, and an
// *ImportStateError for one that is importing or has ended.
func (s *Service) CancelImport(ctx context.Context, actor commands.Actor, workspaceID, id ids.UUID) error {
	var keys []string
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		if _, err := s.ownImport(ctx, tx, actor.UserID, workspaceID, id); err != nil {
			return err
		}
		q := queries.New(tx)
		// Under the row's lock, so that the job cannot start or end the import
		// between this check and the update below.
		status, err := q.LockProjectImportUpload(ctx, queries.LockProjectImportUploadParams{
			WorkspaceID: uuid(workspaceID), RequestedBy: uuid(actor.UserID), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("authoring: lock the import: %w", err)
		}
		if status != ImportUploading && status != ImportQueued {
			return &ImportStateError{Status: status}
		}
		if keys, err = q.DeleteProjectImportChunks(ctx, queries.DeleteProjectImportChunksParams{
			WorkspaceID: uuid(workspaceID), ImportID: uuid(id)}); err != nil {
			return fmt.Errorf("authoring: delete the import's chunks: %w", err)
		}
		failure, err := json.Marshal(ImportError{Code: "canceled"})
		if err != nil {
			return err
		}
		_, err = q.FailProjectImport(ctx, queries.FailProjectImportParams{WorkspaceID: uuid(workspaceID), ID: uuid(id),
			Failure: failure})
		return err
	})
	if err != nil {
		return err
	}
	s.dropChunks(ctx, keys)
	return nil
}

// dropChunks deletes the blobs of an upload's chunks, whose rows are gone. A
// store that does not take that leaves them to the orphan reconciliation.
func (s *Service) dropChunks(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	store, err := s.storage.Store(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "the chunks of an import stay until the orphan reconciliation", "error", err)
		return
	}
	for _, key := range keys {
		if err := store.Delete(ctx, key); err != nil {
			s.log.WarnContext(ctx, "a chunk of an import stays until the orphan reconciliation", "error", err)
		}
	}
}

// importArgs are the arguments of the jobs of an import.
type importArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ImportID    string `json:"import_id"`
}

// importRunJob imports an uploaded archive (FR-JOBS-01). Its jobs run one at a
// time in a process, on a queue of their own, as each reads an archive whole.
type importRunJob importArgs

func (importRunJob) Kind() string { return "project_import_run" }

// importExpireJob deletes an import once its time has passed.
type importExpireJob importArgs

func (importExpireJob) Kind() string { return "project_import_expire" }

func parseImportArgs(a importArgs) (ws, id ids.UUID, err error) {
	if ws, err = ids.ParseUUID(a.WorkspaceID); err != nil {
		return ws, id, fmt.Errorf("authoring: the job's workspace: %w", err)
	}
	if id, err = ids.ParseUUID(a.ImportID); err != nil {
		return ws, id, fmt.Errorf("authoring: the job's import: %w", err)
	}
	return ws, id, nil
}

// registerImportJobs registers the jobs of the imports in r.
func (s *Service) registerImportJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[importRunJob]) error {
		ws, id, err := parseImportArgs(importArgs(job.Args))
		if err != nil {
			return jobs.Cancel(err)
		}
		svc := s.withDB(job.DB)
		if onlyFails(job.Attempt, job.Snoozes) {
			return svc.failImport(ctx, ws, id, &ImportError{Code: FailInternal,
				Detail: "the import failed, and its attempts are over"})
		}
		err = svc.RunImport(ctx, ws, id)
		if refused, ok := isRefusal(err); ok {
			return svc.failImport(ctx, ws, id, refused)
		}
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return svc.failImport(ctx, ws, id, &ImportError{Code: FailInternal,
				Detail: "the import ran out of its time"})
		}
		return err
	})
	jobs.Serial(r, importRunJob{})
	jobs.Timeout(r, importRunJob{}, importTimeout)
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[importExpireJob]) error {
		ws, id, err := parseImportArgs(importArgs(job.Args))
		if err != nil {
			return jobs.Cancel(err)
		}
		svc := s.withDB(job.DB)
		if err := svc.ExpireImport(ctx, ws, id); err != nil {
			svc.log.WarnContext(ctx, "the import could not be deleted, and its job will try again",
				"import_id", ids.Encode(ids.Job, id), "attempt", job.Attempt, "error", err)
			return err
		}
		return nil
	})
}

// failImport fails the import with why, deletes what its upload stored, and
// ends its job. Where the database does not take that, the job runs again
// rather than end with the import still on its way.
func (s *Service) failImport(ctx context.Context, ws, id ids.UUID, why *ImportError) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failTimeout)
	defer cancel()
	failure, err := json.Marshal(why)
	if err != nil {
		return jobs.Cancel(err)
	}
	var keys []string
	var failed int64
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		var err error
		if failed, err = q.FailProjectImport(ctx, queries.FailProjectImportParams{WorkspaceID: uuid(ws), ID: uuid(id),
			Failure: failure}); err != nil {
			return err
		}
		keys, err = q.DeleteProjectImportChunks(ctx, queries.DeleteProjectImportChunksParams{WorkspaceID: uuid(ws),
			ImportID: uuid(id)})
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "the import could not be failed, and its job will try again",
			"import_id", ids.Encode(ids.Job, id), "error", err)
		return jobs.Snooze(failRetry)
	}
	s.dropChunks(ctx, keys)
	if failed == 0 {
		return nil
	}
	s.log.WarnContext(ctx, "the import failed", "import_id", ids.Encode(ids.Job, id), "code", why.Code,
		"section", why.Section, "version", why.Version, "check", why.Check)
	return jobs.Cancel(why)
}

// ExpireImport deletes the import id and what is left of its upload, once its
// time has passed: the blobs first, so that a store that fails leaves the row,
// which the job's next attempt reads again.
func (s *Service) ExpireImport(ctx context.Context, ws, id ids.UUID) error {
	var keys []string
	gone := false
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if _, err := q.ProjectImportOfJob(ctx, queries.ProjectImportOfJobParams{WorkspaceID: uuid(ws),
			ID: uuid(id)}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				gone = true
				return nil
			}
			return err
		}
		failure, err := json.Marshal(ImportError{Code: "expired"})
		if err != nil {
			return err
		}
		if _, err := q.FailProjectImport(ctx, queries.FailProjectImportParams{WorkspaceID: uuid(ws), ID: uuid(id),
			Failure: failure}); err != nil {
			return err
		}
		chunks, err := q.ProjectImportChunks(ctx, queries.ProjectImportChunksParams{WorkspaceID: uuid(ws),
			ImportID: uuid(id)})
		if err != nil {
			return err
		}
		for _, c := range chunks {
			keys = append(keys, c.StorageKey)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("authoring: end the import: %w", err)
	}
	if gone {
		return nil
	}
	if len(keys) > 0 {
		store, err := s.storage.Store(ctx)
		if err != nil {
			return fmt.Errorf("authoring: open the store: %w", err)
		}
		for _, key := range keys {
			if err := store.Delete(ctx, key); err != nil {
				return fmt.Errorf("authoring: delete a chunk: %w", err)
			}
		}
	}
	return s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		_, err := queries.New(tx).DeleteProjectImport(ctx, queries.DeleteProjectImportParams{WorkspaceID: uuid(ws),
			ID: uuid(id)})
		return err
	})
}

// RunImport imports the uploaded archive of the import id, unless it has
// ended. It is safe to repeat: everything that it writes of the project, and
// the import's end, commit in one transaction, so that an attempt that a crash
// cut off leaves nothing, and one that follows a commit finds the import ended.
// An archive that is refused returns an *ImportError, which the job records.
func (s *Service) RunImport(ctx context.Context, ws, id ids.UUID) error {
	var row queries.ProjectImport
	var chunks []queries.ProjectImportChunksRow
	started := false
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		var err error
		row, err = q.ProjectImportOfJob(ctx, queries.ProjectImportOfJobParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("authoring: read the import: %w", err)
		}
		n, err := q.StartProjectImport(ctx, queries.StartProjectImportParams{WorkspaceID: uuid(ws), ID: uuid(id)})
		if err != nil {
			return fmt.Errorf("authoring: start the import: %w", err)
		}
		started = n > 0
		if started {
			chunks, err = q.ProjectImportChunks(ctx, queries.ProjectImportChunksParams{WorkspaceID: uuid(ws),
				ImportID: uuid(id)})
		}
		return err
	})
	if err != nil {
		return err
	}
	if !started {
		// An attempt before this one committed, and may have been cut off
		// before it deleted the chunks.
		if row.Status == ImportReady {
			s.deleteChunks(ctx, ws, id)
		}
		return nil
	}
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return fmt.Errorf("authoring: open the store: %w", err)
	}
	keys := make([]string, len(chunks))
	for i, c := range chunks {
		if int(c.ChunkIndex) != i {
			return &ImportError{Code: FailArchiveInvalid, Check: "chunks", Detail: "the upload lacks a chunk"}
		}
		keys[i] = c.StorageKey
	}
	r := newChunkReader(ctx, store, keys, int64(row.ChunkBytes), row.SizeBytes)
	run := importRun{workspace: ws, importID: id, user: row.RequestedBy.Bytes, remap: row.IDPolicy == PolicyRemap}
	if err := s.importArchive(ctx, run, r, row.SizeBytes); err != nil {
		return err
	}
	s.deleteChunks(ctx, ws, id)
	return nil
}

// ImportFile imports the archive r of size bytes, for the command line, which
// has the file at hand and uploads nothing, and runs it here rather than in a
// job. It holds no row while it runs: the transaction that writes the project
// records the finished import, so that a process that dies leaves nothing on
// its way, which no job would come to end. It returns the errors of what comes
// before the archive is read, an *ImportInProgressError for a user with an
// upload on its way, and the ended import, whose Failure says why an archive
// was refused, and which is then recorded nowhere.
func (s *Service) ImportFile(ctx context.Context, actor commands.Actor, workspaceID ids.UUID, name string,
	r io.ReaderAt, size int64, remap bool) (Import, error) {
	req := ImportRequest{FileName: name, SizeBytes: size, Remap: remap}
	if err := s.checkImport(ctx, actor, workspaceID, &req); err != nil {
		return Import{}, err
	}
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		running, err := queries.New(tx).ProjectImportInProgress(ctx, queries.ProjectImportInProgressParams{
			WorkspaceID: uuid(workspaceID), RequestedBy: uuid(actor.UserID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("authoring: read the user's import on its way: %w", err)
		}
		return &ImportInProgressError{ID: ids.Encode(ids.Job, running.Bytes)}
	})
	if err != nil {
		return Import{}, err
	}
	id := ids.New()
	run := importRun{workspace: workspaceID, importID: id, user: actor.UserID, remap: remap, client: actor, file: &req}
	if err := s.importArchive(ctx, run, r, size); err != nil {
		refused, ok := isRefusal(err)
		if !ok {
			return Import{}, err
		}
		return Import{ID: ids.Encode(ids.Job, id), WorkspaceID: ids.Encode(ids.Workspace, workspaceID),
			Status: ImportFailed, FileName: req.FileName, SizeBytes: size, Failure: refused}, nil
	}
	return s.GetImport(ctx, actor, workspaceID, id)
}

// ExportFile builds an export of the project for the command line, here rather
// than in a job, and writes its archive to w. The job that Create enqueues is
// due only once this build's time is over, so that the instance's workers do
// not build the same export at once, and finish it only if this process died.
// It returns the errors of Create, and of the build.
func (s *Service) ExportFile(ctx context.Context, actor commands.Actor, projectID ids.UUID, req Request,
	w io.Writer) (Export, error) {
	e, err := s.create(ctx, actor, projectID, req, time.Now().Add(buildTimeout+time.Minute))
	if err != nil {
		return Export{}, err
	}
	id, err := ids.Decode(ids.Job, e.ID)
	if err != nil {
		return Export{}, err
	}
	ws, err := s.projects.ActorWorkspace(ctx, actor, projectID)
	if err != nil {
		return Export{}, err
	}
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	if err := s.Build(buildCtx, ws, id); err != nil {
		_ = s.fail(ctx, ws, id, err)
		return Export{}, err
	}
	d, err := s.Open(ctx, actor, projectID, id)
	if err != nil {
		return Export{}, err
	}
	defer func() { _ = d.Body.Close() }()
	if _, err := io.Copy(w, d.Body); err != nil {
		return Export{}, fmt.Errorf("authoring: write the archive: %w", err)
	}
	return s.Get(ctx, actor, projectID, id)
}

// deleteChunks deletes what is left of a finished import's upload.
func (s *Service) deleteChunks(ctx context.Context, ws, id ids.UUID) {
	var keys []string
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		var err error
		keys, err = queries.New(tx).DeleteProjectImportChunks(ctx, queries.DeleteProjectImportChunksParams{
			WorkspaceID: uuid(ws), ImportID: uuid(id)})
		return err
	})
	if err != nil {
		s.log.WarnContext(ctx, "the chunks of a finished import stay until it expires", "error", err)
		return
	}
	s.dropChunks(ctx, keys)
}

// importRun is who an archive is imported for, and how.
type importRun struct {
	workspace, importID, user ids.UUID
	remap                     bool
	client                    commands.Actor
	// file is the request of an import of the command line, which has no row
	// until the transaction that writes the project adds it, or nil for a
	// job's.
	file *ImportRequest
}

// progress records how far the job is, which a failure to record leaves as it
// was, as the import goes on regardless. An import of the command line has no
// row to record it in. It takes a connection of its own, so it is never called
// inside the transaction that writes the project, which on a pool of one
// connection would wait for itself.
func (s *Service) progress(ctx context.Context, run importRun, phase string, done, total int64) {
	if run.file != nil {
		return
	}
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: run.workspace}, func(tx pgx.Tx) error {
		return queries.New(tx).SetProjectImportProgress(ctx, queries.SetProjectImportProgressParams{
			WorkspaceID: uuid(run.workspace), ID: uuid(run.importID), Phase: phase, ProgressDone: done,
			ProgressTotal: total})
	})
	if err != nil && ctx.Err() == nil {
		s.log.WarnContext(ctx, "the import's progress could not be recorded", "error", err)
	}
}

// releaseFiles returns the files of the archive under dir, by their paths
// below it, which are held in memory together, within maxJSONTotal.
func releaseFiles(a *archive, dir string) (schemaversion.Files, error) {
	names := a.under(dir)
	files := make(schemaversion.Files, len(names))
	var held int64
	for _, name := range names {
		path := strings.TrimPrefix(name, dir)
		var data []byte
		var err error
		if schemaversion.IsJSON(path) {
			data, err = a.readJSON(name)
		} else {
			data, err = a.read(name, maxJSONMember)
		}
		if err != nil {
			return nil, err
		}
		if held += int64(len(data)); held > maxJSONTotal {
			return nil, invalidArchive("size", "the files of %q hold more than the %d bytes that an import reads", dir,
				maxJSONTotal)
		}
		files[path] = data
	}
	return files, nil
}

// verifyRelease verifies a release of the archive of the project projectID
// before it is accepted (FR-DATA-01): that it is of a version that the server
// reads, each file's hash, the content hash, each file against the schema of
// its version, and, through the reference runtime, its cross-file references
// and its ASTs, which must type-check against the declarations of the snapshot
// and within the sandbox limits, at an astVersion that the server supports, and
// that its sealed manifest is of that project and of the release that the
// archive's index names, by its ID and its version. A failure names the
// release's version and the check (AC11).
func verifyRelease(projectID, id, version string, schemaVersion int, contentHash string,
	files schemaversion.Files) error {
	fail := func(check string, err error) *ImportError {
		e := &ImportError{Code: FailReleaseInvalid, Version: version, Check: check}
		if err != nil {
			e.Detail = firstLine(err.Error())
		}
		return e
	}
	v, err := schemaversion.VersionOf(files)
	if err != nil {
		return fail("manifest", err)
	}
	if schemaVersion != 0 && v != schemaVersion {
		return fail("schema_version", fmt.Errorf("its files are of version %d, and its metadata says %d", v, schemaVersion))
	}
	supported := schemaversion.Supported()
	if v > supported.Max {
		return &ImportError{Code: FailFormatUnsupported, Version: version, Check: "schema_version",
			Detail: fmt.Sprintf("the release is of schema version %d, and this server reads up to version %d", v,
				supported.Max)}
	}
	if !supported.Contains(v) {
		return &ImportError{Code: FailFormatUnsupported, Version: version, Check: "schema_version",
			Detail: fmt.Sprintf("the release is of schema version %d, older than this server reads", v)}
	}
	if err := schemaversion.Sealed(files); err != nil {
		return fail("hashes", err)
	}
	if contentHash != "" {
		_, got, err := manifestFacts(files)
		if err != nil {
			return fail("manifest", err)
		}
		if got != contentHash {
			return fail("content_hash", fmt.Errorf("its manifest's content hash is %s, and its metadata says %s", got,
				contentHash))
		}
	}
	if err := checkReleaseNames(files, projectID, id, version); err != nil {
		return fail("snapshot", err)
	}
	if err := schemaversion.Validate(files); err != nil {
		switch {
		case errors.Is(err, flow.ErrASTUnsupported):
			return fail("ast_version", err)
		case errors.Is(err, flow.ErrLoad):
			return fail("references_and_asts", err)
		default:
			return fail("schema", err)
		}
	}
	return nil
}

// checkReleaseNames checks that the manifest of a release's files is of the
// project projectID, and of the release of the ID id and the version version,
// either of which an archive of a release may leave out of its own manifest,
// so that a row never says one release while its files say another.
func checkReleaseNames(files schemaversion.Files, projectID, id, version string) error {
	m, err := schemaversion.Object(files, published.ManifestPath)
	if err != nil {
		return err
	}
	own, err := ids.Decode(ids.Project, projectID)
	if err != nil {
		return fmt.Errorf("the archive's project: %w", err)
	}
	info, _ := m["project"].(map[string]any)
	named, _ := info["id"].(string)
	if of, err := ids.Decode(ids.Project, named); err != nil || of != own {
		return fmt.Errorf("its manifest is of the project %q, and the archive of %s", named, projectID)
	}
	snap, ok := m["snapshot"].(map[string]any)
	if !ok {
		return errors.New("its manifest has no snapshot")
	}
	if got, _ := snap["version"].(string); version != "" && got != version {
		return fmt.Errorf("its manifest is of version %q, and its metadata says %q", got, version)
	}
	if id == "" {
		return nil
	}
	want, err := ids.Decode(ids.Snapshot, id)
	if err != nil {
		return fmt.Errorf("its metadata's ID: %w", err)
	}
	text, _ := snap["id"].(string)
	got, err := ids.Decode(ids.Snapshot, text)
	if err != nil {
		return fmt.Errorf("its manifest's ID: %w", err)
	}
	if got != want {
		return fmt.Errorf("its manifest is of the release %s, and its metadata says %s", text, id)
	}
	return nil
}

// manifestFacts returns the hash of each file that a release's manifest lists,
// and its content hash, of a manifest of any version that the server reads,
// which package published's types need not be of.
func manifestFacts(files schemaversion.Files) (map[string]string, string, error) {
	m, err := schemaversion.Object(files, published.ManifestPath)
	if err != nil {
		return nil, "", err
	}
	listed, ok := m["files"].(map[string]any)
	if !ok {
		return nil, "", errors.New("its manifest lists no files")
	}
	hashes := make(map[string]string, len(listed))
	for path, h := range listed {
		text, ok := h.(string)
		if !ok {
			return nil, "", fmt.Errorf("its manifest's hash of %s is not a string", path)
		}
		hashes[path] = text
	}
	snap, ok := m["snapshot"].(map[string]any)
	if !ok {
		return nil, "", errors.New("its manifest has no snapshot")
	}
	hash, ok := snap["contentHash"].(string)
	if !ok || hash == "" {
		return nil, "", errors.New("its manifest has no content hash")
	}
	return hashes, hash, nil
}

// atTypesVersion returns the files at the version of package published's
// types, from which a draft is built. A release of the history logs its own
// migration when it is written (importWriter.release).
func atTypesVersion(version string, files schemaversion.Files) (schemaversion.Files, error) {
	v, err := schemaversion.VersionOf(files)
	if err != nil {
		return nil, &ImportError{Code: FailReleaseInvalid, Version: version, Check: "manifest",
			Detail: firstLine(err.Error())}
	}
	var out schemaversion.Files
	switch {
	case v < published.SchemaVersion:
		out, _, err = schemaversion.Migrate(files, published.SchemaVersion)
	case v > published.SchemaVersion:
		out, _, err = schemaversion.Render(files, published.SchemaVersion)
	default:
		return files, nil
	}
	if err != nil {
		return nil, &ImportError{Code: FailReleaseInvalid, Version: version, Check: "migration",
			Detail: firstLine(err.Error())}
	}
	return out, nil
}

// importArchive reads, checks, and imports the archive r of size bytes. Every
// check of the archive comes before the transaction that writes it, which
// writes everything of the project and ends the import.
func (s *Service) importArchive(ctx context.Context, run importRun, r io.ReaderAt, size int64) error {
	s.progress(ctx, run, "checking", 0, 0)
	a, err := openArchive(r, size, s.importMaxBytes)
	if err != nil {
		return err
	}
	c, err := readArchived(a)
	if err != nil {
		return err
	}
	if supported := schemaversion.Supported(); c.manifest.PublishedSchemaVersion > supported.Max {
		return &ImportError{Code: FailFormatUnsupported, Check: "schema_version",
			Detail: fmt.Sprintf("the archive was written at schema version %d, and this server reads up to version %d",
				c.manifest.PublishedSchemaVersion, supported.Max)}
	}
	if c.manifest.Source.Kind == "release" {
		files, err := releaseFiles(a, "release/")
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return &ImportError{Code: FailReleaseInvalid, Version: c.manifest.Source.Version, Check: "missing_file",
				Detail: "the archive of a release holds no release files"}
		}
		if err := verifyRelease(c.manifest.Project.ID, c.manifest.Source.SnapshotID, c.manifest.Source.Version, 0,
			c.manifest.Source.ContentHash, files); err != nil {
			return err
		}
		current, err := atTypesVersion(c.manifest.Source.Version, files)
		if err != nil {
			return err
		}
		c.sourceFiles = current
		if err := c.buildFromRelease(current); err != nil {
			return err
		}
	}
	total := int64(len(c.releases))
	for i, rel := range c.releases {
		s.progress(ctx, run, "verifying", int64(i), total)
		files, err := releaseFiles(a, c.releasePaths[i])
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return &ImportError{Code: FailReleaseInvalid, Version: rel.Version, Check: "missing_file",
				Detail: "the archive holds no files of the release"}
		}
		if err := verifyRelease(c.manifest.Project.ID, rel.ID, rel.Version, rel.SchemaVersion, rel.ContentHash,
			files); err != nil {
			return err
		}
	}
	list, err := c.entityIDs()
	if err != nil {
		return err
	}
	policy := PolicyPreserve
	if run.remap {
		policy = PolicyRemap
	} else {
		if s.idsInUse == nil {
			return errors.New("authoring: this server has no collision check of the IDs")
		}
		inUse, err := s.idsInUse(ctx, list)
		if err != nil {
			return fmt.Errorf("authoring: check the archive's IDs: %w", err)
		}
		if inUse {
			policy = PolicyRemap
		}
	}
	var table map[ids.UUID]ids.UUID
	if policy == PolicyRemap {
		if table, err = c.remapAll(a, list); err != nil {
			return err
		}
	}
	missing := c.dropMissingAssets()
	files := make([]asset.ImportedFile, len(c.assets))
	for i, as := range c.assets {
		s.progress(ctx, run, "assets", int64(i), int64(len(c.assets)))
		if files[i], err = s.storeAsset(ctx, a, run.workspace, as); err != nil {
			return err
		}
	}
	s.progress(ctx, run, "writing", 0, 0)
	err = s.writeImport(ctx, run, a, c, policy, table, files, missing)
	if policy != PolicyPreserve || !idTaken(err) {
		return err
	}
	// A row that came after the collision check took one of the archive's IDs,
	// as a concurrent import of the same archive on another process does. The
	// transaction wrote nothing, so the import is made again with fresh IDs,
	// and a unique violation that remains is the archive's own.
	if table, err = c.remapAll(a, list); err != nil {
		return err
	}
	missing = c.dropMissingAssets()
	return s.writeImport(ctx, run, a, c, PolicyRemap, table, files, missing)
}

// remapAll gives the archive's IDs of list, and those that only its releases'
// files hold, fresh ones, through its sections and the draft of an archive of a
// release, and returns the table of them.
func (c *archived) remapAll(a *archive, list []ids.UUID) (map[ids.UUID]ids.UUID, error) {
	all, err := c.remapIDs(a, list)
	if err != nil {
		return nil, err
	}
	table := freshIDs(all)
	if err := c.remap(table); err != nil {
		return nil, err
	}
	if c.manifest.Source.Kind == "release" {
		if err := c.buildFromRelease(c.sourceFiles); err != nil {
			return nil, err
		}
	}
	return table, nil
}

// idTaken says whether err is the refusal of a row for a unique violation.
func idTaken(err error) bool {
	var refused *ImportError
	return errors.As(err, &refused) && refused.unique
}

// storeAsset reads an asset's file from the archive, checks that it is what
// the index says, and stores it as an upload's is checked and stored.
func (s *Service) storeAsset(ctx context.Context, a *archive, ws ids.UUID, as Asset) (asset.ImportedFile, error) {
	fail := func(check, detail string) *ImportError {
		return &ImportError{Code: FailAssetInvalid, Section: "assets", Check: check,
			Detail: as.ID + ": " + detail}
	}
	if !a.has(as.Path) {
		return asset.ImportedFile{}, fail("missing_file", "the archive does not hold its file")
	}
	data, err := a.read(as.Path, s.maxAssetBytes())
	if err != nil {
		return asset.ImportedFile{}, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != as.SHA256 || int64(len(data)) != as.Bytes {
		return asset.ImportedFile{}, fail("hash", "its file is not what the index says")
	}
	f, err := s.assets.StoreImported(ctx, ws, data)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return asset.ImportedFile{}, err
		}
		var refused *asset.ImageError
		if errors.As(err, &refused) || errors.Is(err, asset.ErrTooLarge) {
			return asset.ImportedFile{}, fail("type", err.Error())
		}
		return asset.ImportedFile{}, err
	}
	return f, nil
}

// maxAssetBytes is the largest file of an asset that an import reads.
func (s *Service) maxAssetBytes() int64 { return s.assets.MaxBytes() }

// dropMissingAssets clears each reference to an asset of an archive that holds
// no assets, which no row could hold, and returns how many it cleared, which
// the report says (FR-DATA-01). An archive that holds its assets keeps every
// reference, and the command that writes one refuses one that dangles.
func (c *archived) dropMissingAssets() int {
	if c.manifest.IncludesAssets {
		return 0
	}
	n := 0
	for i := range c.nodes {
		if c.nodes[i].CoverAssetID != "" {
			c.nodes[i].CoverAssetID = ""
			n++
		}
	}
	for i := range c.components {
		if c.components[i].ImageAssetID != "" {
			c.components[i].ImageAssetID = ""
			n++
		}
	}
	return n
}

// writeImport writes the project of the archive in one transaction, which ends
// the import too, so that it commits whole or not at all (FR-DATA-01).
func (s *Service) writeImport(ctx context.Context, run importRun, a *archive, c *archived, policy string,
	table map[ids.UUID]ids.UUID, files []asset.ImportedFile, missing int) error {
	projectID, err := ids.Decode(ids.Project, c.manifest.Project.ID)
	if err != nil {
		return &ImportError{Code: FailContentInvalid, Section: "manifest", Check: "id", Detail: c.manifest.Project.ID}
	}
	settings, err := c.settings()
	if err != nil {
		return err
	}
	actor := commands.Actor{UserID: run.user, Client: run.client.Client}
	in := project.Imported{ID: projectID, Slug: c.project.Slug, Client: actor.Client,
		Settings: project.Settings{Name: c.project.Name, Description: c.project.Description,
			DefaultLocale: settings.DefaultLocale},
		Metadata: map[string]any{"import_id": ids.Encode(ids.Job, run.importID), "id_policy": policy}}
	report := ImportReport{Policy: policy, ProjectID: ids.Encode(ids.Project, projectID),
		ProjectName: strings.TrimSpace(c.project.Name), Boards: len(c.boards), Nodes: len(c.nodes),
		Variables: len(c.variables), Components: len(c.components), Comments: len(c.comments),
		PlayStates: len(c.playStates), Releases: len(c.releases), Assets: len(c.assets), MissingAssets: missing}
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: run.workspace, UserID: run.user}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if _, err := s.projects.CreateImported(ctx, tx, run.user, run.workspace, in); err != nil {
			return err
		}
		users, imported, err := mapPeople(ctx, q, run.workspace, c)
		if err != nil {
			return err
		}
		report.ImportedUsers = imported
		for i, as := range c.assets {
			id, err := ids.Decode(ids.Asset, as.ID)
			if err != nil {
				return &ImportError{Code: FailContentInvalid, Section: "assets", Check: "id", Detail: as.ID}
			}
			err = s.assets.AddImported(ctx, tx, run.workspace, projectID, id, run.user, files[i], func() ([]byte, error) {
				return a.read(as.Path, s.maxAssetBytes())
			})
			if errors.Is(err, asset.ErrDuplicateContent) {
				return &ImportError{Code: FailAssetInvalid, Section: "assets", Check: "duplicate",
					Detail: as.ID + ": the archive holds the same file twice"}
			}
			if err != nil {
				return err
			}
		}
		err = s.pipeline.Populate(ctx, tx, actor, run.workspace, projectID, func(t *commands.Tx) error {
			if err := applyDraft(ctx, t, c); err != nil {
				return err
			}
			return t.AddUsage(usage.Snapshots, int64(len(c.releases)))
		})
		if err != nil {
			return err
		}
		w := importWriter{q: q, ws: uuid(run.workspace), project: uuid(projectID), users: users, at: time.Now()}
		if err := w.mutes(ctx, c.mutes); err != nil {
			return err
		}
		if err := w.comments(ctx, c.comments); err != nil {
			return err
		}
		// The commands' derivation has run, and did not see the comments, which
		// no command wrote, so search finds them from the import's commit on.
		commentIDs := make([]ids.UUID, len(c.comments))
		for i, x := range c.comments {
			if commentIDs[i], err = ids.Decode(ids.Comment, x.ID); err != nil {
				return &ImportError{Code: FailContentInvalid, Section: "comments", Check: "id", Detail: x.ID}
			}
		}
		if err := search.IndexComments(ctx, q, run.workspace, projectID, commentIDs); err != nil {
			return err
		}
		if err := w.playStates(ctx, c.playStates); err != nil {
			return err
		}
		maxSeq := int64(0)
		for i, rel := range c.releases {
			stored, err := releaseFiles(a, c.releasePaths[i])
			if err != nil {
				return err
			}
			if table != nil {
				if stored, err = rewriteRelease(stored, table); err != nil {
					return err
				}
			}
			steps, err := w.release(ctx, tx, rel, stored)
			if err != nil {
				return err
			}
			if len(steps) > 0 {
				snapshotID, err := ids.Decode(ids.Snapshot, rel.ID)
				if err != nil {
					return err
				}
				if err := schemaversion.Record(ctx, tx, run.workspace, projectID, snapshotID, steps); err != nil {
					return err
				}
			}
			maxSeq = max(maxSeq, rel.SourceSeq, rel.AcknowledgedSeq)
		}
		// A project's sequence is where its next operation goes, and is at
		// least that of the draft that each of its releases was built from.
		if maxSeq > 0 {
			if err := q.SetProjectSeq(ctx, queries.SetProjectSeqParams{WorkspaceID: uuid(run.workspace),
				ID: uuid(projectID), Seq: maxSeq}); err != nil {
				return fmt.Errorf("authoring: set the project's sequence: %w", err)
			}
		}
		// Last, as its check holds the lock of the workspace's projects until
		// this transaction ends.
		if err := s.projects.FinishImported(ctx, tx, run.workspace); err != nil {
			return err
		}
		data, err := json.Marshal(report)
		if err != nil {
			return err
		}
		if run.file != nil {
			return s.recordFileImport(ctx, tx, run, policy, projectID, data)
		}
		n, err := q.FinishProjectImport(ctx, queries.FinishProjectImportParams{WorkspaceID: uuid(run.workspace),
			ID: uuid(run.importID), AppliedPolicy: pgtype.Text{String: policy, Valid: true},
			CreatedProjectID: uuid(projectID), Report: data})
		if err != nil {
			return fmt.Errorf("authoring: finish the import: %w", err)
		}
		if n == 0 {
			return &ImportError{Code: FailInternal, Detail: "the import ended while it was written"}
		}
		return nil
	})
	return importWriteError(err)
}

// recordFileImport records, in tx, the finished import of the command line,
// with the job that deletes it once its time has passed.
func (s *Service) recordFileImport(ctx context.Context, tx pgx.Tx, run importRun, policy string, projectID ids.UUID,
	report []byte) error {
	requested := PolicyAuto
	if run.file.Remap {
		requested = PolicyRemap
	}
	expires := time.Now().Add(ImportRetention)
	if err := queries.New(tx).InsertFinishedProjectImport(ctx, queries.InsertFinishedProjectImportParams{
		ID: uuid(run.importID), WorkspaceID: uuid(run.workspace), RequestedBy: uuid(run.user),
		FileName: run.file.FileName, SizeBytes: run.file.SizeBytes, ChunkBytes: ChunkBytes, IDPolicy: requested,
		AppliedPolicy: pgtype.Text{String: policy, Valid: true}, CreatedProjectID: uuid(projectID), Report: report,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}}); err != nil {
		return fmt.Errorf("authoring: record the import: %w", err)
	}
	args := importArgs{WorkspaceID: run.workspace.String(), ImportID: run.importID.String()}
	if _, err := s.queue.Enqueue(ctx, tx, importExpireJob(args), &jobs.EnqueueOptions{At: expires,
		MaxAttempts: expireAttempts}); err != nil {
		return fmt.Errorf("authoring: enqueue the import's expiry: %w", err)
	}
	return nil
}

// uniqueViolation is PostgreSQL's code of a row that a unique constraint
// refuses.
const uniqueViolation = "23505"

// importWriteError maps what the transaction failed with: a limit or a role
// that the import's own checks could not see coming, or a row that the
// database's constraints refuse, which is what is wrong with the archive, is
// the import's failure. Anything else is the server's, and worth another
// attempt.
func importWriteError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := isRefusal(err); ok {
		return err
	}
	var limit *entitlements.LimitError
	if errors.As(err, &limit) {
		return &ImportError{Code: FailLimitExceeded, Check: string(limit.Key),
			Detail: fmt.Sprintf("the workspace has no room for it under its %s limit", limit.Key)}
	}
	if errors.Is(err, authz.ErrInsufficientRole) || errors.Is(err, authz.ErrOwnerRequired) ||
		errors.Is(err, project.ErrNotFound) || errors.Is(err, commands.ErrNotFound) {
		return &ImportError{Code: FailForbidden, Detail: "the user may no longer create a project in the workspace"}
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (strings.HasPrefix(pg.Code, "22") || strings.HasPrefix(pg.Code, "23")) {
		return &ImportError{Code: FailContentInvalid, Check: pg.ConstraintName,
			Detail: "a row of the archive is refused by the database: " + pg.Message,
			unique: pg.Code == uniqueViolation}
	}
	if field, message, ok := commands.PartRefusal(err); ok {
		return &ImportError{Code: FailContentInvalid, Check: field, Detail: message,
			unique: errors.Is(commands.PartReason(err), commands.ErrIDTaken)}
	}
	// The project's own fields, which CreateImported holds to a new project's
	// checks, as a name or a default locale that is not one.
	var fields input.FieldErrors
	if errors.As(err, &fields) && len(fields) > 0 {
		return &ImportError{Code: FailContentInvalid, Section: "project", Check: fields[0].Field,
			Detail: fields[0].Field + " " + fields[0].Message}
	}
	return err
}

// mapPeople returns the account that each person whom the archive names is
// attributed to on this instance (FR-DATA-01): themselves if they are a member
// of the workspace, and otherwise the workspace's imported identity of them,
// which carries the display name from the archive. An ID that names anyone
// else on the instance is never kept, so that an archive cannot show another
// workspace's user. It also returns how many imported identities the archive's
// people take.
func mapPeople(ctx context.Context, q *queries.Queries, ws ids.UUID, c *archived) (map[ids.UUID]ids.UUID, int, error) {
	seen := map[ids.UUID]bool{}
	add := func(s string) {
		if s == "" {
			return
		}
		if id, err := ids.Decode(ids.User, s); err == nil {
			seen[id] = true
		}
	}
	for _, x := range c.comments {
		add(x.AuthorID)
		add(x.ResolvedBy)
		if !x.Deleted {
			mentionsIn(x.Body, seen)
		}
	}
	for _, x := range c.playStates {
		add(x.CreatedBy)
	}
	for _, x := range c.mutes {
		add(x.MutedBy)
	}
	for _, x := range c.releases {
		add(x.PublishedBy)
		reportUsersIn(x.ValidationReport, seen)
	}
	out := make(map[ids.UUID]ids.UUID, len(seen))
	if len(seen) == 0 {
		return out, 0, nil
	}
	names := make(map[ids.UUID]string, len(c.people))
	for _, p := range c.people {
		id, err := ids.Decode(ids.User, p.ID)
		if err != nil {
			return nil, 0, &ImportError{Code: FailContentInvalid, Section: "people", Check: "id", Detail: p.ID}
		}
		names[id] = p.DisplayName
	}
	list := make([]pgtype.UUID, 0, len(seen))
	for id := range seen {
		list = append(list, uuid(id))
	}
	slices.SortFunc(list, func(a, b pgtype.UUID) int { return bytes.Compare(a.Bytes[:], b.Bytes[:]) })
	members, err := q.WorkspaceMembersAmong(ctx, queries.WorkspaceMembersAmongParams{WorkspaceID: uuid(ws),
		UserIds: list})
	if err != nil {
		return nil, 0, fmt.Errorf("authoring: read the workspace's members: %w", err)
	}
	for _, m := range members {
		out[ids.UUID(m.Bytes)] = ids.UUID(m.Bytes)
	}
	var strangers []pgtype.UUID
	for _, id := range list {
		if _, ok := out[ids.UUID(id.Bytes)]; !ok {
			strangers = append(strangers, id)
		}
	}
	if len(strangers) == 0 {
		return out, 0, nil
	}
	existing, err := q.ImportedUsers(ctx, queries.ImportedUsersParams{WorkspaceID: uuid(ws), SourceIds: strangers})
	if err != nil {
		return nil, 0, fmt.Errorf("authoring: read the workspace's imported identities: %w", err)
	}
	type identity struct {
		source ids.UUID
		name   string
	}
	have := make(map[identity]ids.UUID, len(existing))
	for _, e := range existing {
		have[identity{e.SourceUserID.Bytes, e.DisplayName}] = e.UserID.Bytes
	}
	for _, s := range strangers {
		source := ids.UUID(s.Bytes)
		name := cleanDisplayName(names[source])
		k := identity{source, name}
		user, ok := have[k]
		if !ok {
			user = ids.New()
			// The identity first, whose account its foreign key waits for
			// until the commit, so that an import that loses the race for it
			// to a concurrent one adds no account, and takes the winner's.
			_, err := q.InsertImportedUser(ctx, queries.InsertImportedUserParams{WorkspaceID: uuid(ws),
				SourceUserID: uuid(source), DisplayName: name, UserID: uuid(user)})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				winner, err := q.ImportedUserOf(ctx, queries.ImportedUserOfParams{WorkspaceID: uuid(ws),
					SourceUserID: uuid(source), DisplayName: name})
				if err != nil {
					return nil, 0, fmt.Errorf("authoring: read an imported identity: %w", err)
				}
				user = winner.Bytes
			case err != nil:
				return nil, 0, fmt.Errorf("authoring: record an imported identity: %w", err)
			default:
				if err := q.InsertImportedAccount(ctx, queries.InsertImportedAccountParams{ID: uuid(user),
					Email: "imported-" + user.String() + "@imported.invalid", DisplayName: name}); err != nil {
					return nil, 0, fmt.Errorf("authoring: add an imported identity: %w", err)
				}
			}
			have[k] = user
		}
		out[source] = user
	}
	return out, len(strangers), nil
}

// cleanDisplayName returns the archive's display name of a person as an
// account's is held to, or the imported identity's own name for one that it
// does not hold, or that is not one.
func cleanDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if input.CheckName(name, account.MaxDisplayNameLength) != "" {
		return importedName
	}
	return name
}

// importWriter writes the rows of an archive that carry who made them and when,
// which no command takes.
type importWriter struct {
	q       *queries.Queries
	ws      pgtype.UUID
	project pgtype.UUID
	users   map[ids.UUID]ids.UUID
	at      time.Time
}

// user returns the account that the archive's user s is attributed to.
func (w importWriter) user(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	id, err := ids.Decode(ids.User, s)
	if err != nil {
		return pgtype.UUID{}
	}
	mapped, ok := w.users[id]
	if !ok {
		return pgtype.UUID{}
	}
	return uuid(mapped)
}

func optional(k ids.Kind, s string) (pgtype.UUID, error) {
	if s == "" {
		return pgtype.UUID{}, nil
	}
	id, err := ids.Decode(k, s)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return uuid(id), nil
}

func stamp(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// mutes adds the archive's muted findings, to the owners that its commands
// have created.
func (w importWriter) mutes(ctx context.Context, list []ValidationMute) error {
	for _, m := range list {
		p := queries.InsertImportedMuteParams{WorkspaceID: w.ws, ProjectID: w.project, Code: m.Code, Reason: m.Reason,
			MutedBy: w.user(m.MutedBy)}
		var err error
		switch m.OwnerType {
		case "node":
			p.NodeID, err = optional(ids.Node, m.OwnerID)
		case "connection":
			p.ConnectionID, err = optional(ids.Connection, m.OwnerID)
		case "branch_output":
			p.BranchOutputID, err = optional(ids.BranchOutput, m.OwnerID)
		case "variable":
			p.VariableID, err = optional(ids.Variable, m.OwnerID)
		case "board":
			p.BoardID, err = optional(ids.Board, m.OwnerID)
		default:
			err = fmt.Errorf("an owner of the kind %q", m.OwnerType)
		}
		if err != nil {
			return &ImportError{Code: FailContentInvalid, Section: "validation_mutes", Check: "owner", Detail: err.Error()}
		}
		if problems := validation.CheckImported(m.OwnerID, m.Code, m.Reason); len(problems) > 0 {
			return &ImportError{Code: FailContentInvalid, Section: "validation_mutes", Check: problems[0].Field,
				Detail: m.OwnerID + ": " + problems[0].Field + " " + problems[0].Message}
		}
		if err := w.q.InsertImportedMute(ctx, p); err != nil {
			return sectionError("validation_mutes", m.OwnerID, err)
		}
	}
	return nil
}

// sectionError names the section of a row that the database refuses, and
// passes on any other failure.
func sectionError(section, id string, err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (strings.HasPrefix(pg.Code, "22") || strings.HasPrefix(pg.Code, "23")) {
		return &ImportError{Code: FailContentInvalid, Section: section, Check: pg.ConstraintName,
			Detail: id + ": " + pg.Message, unique: pg.Code == uniqueViolation}
	}
	return fmt.Errorf("authoring: add %s of the section %s: %w", id, section, err)
}

// emptyBody is the body of a root comment that its author deleted, whose
// thread keeps its place without it.
var emptyBody = func() []byte {
	data, err := json.Marshal(richtext.Doc{Blocks: []richtext.Block{&richtext.Paragraph{}}})
	if err != nil {
		panic("authoring: the empty document is not valid: " + err.Error())
	}
	return data
}()

// comments adds the archive's threads, each root before its replies, with who
// wrote each and when, after holding each body to the commands' rules.
func (w importWriter) comments(ctx context.Context, list []Comment) error {
	roots := make(map[string]Comment, len(list))
	for _, x := range list {
		if x.ParentID == "" {
			roots[x.ID] = x
		}
	}
	ordered := slices.Clone(list)
	slices.SortStableFunc(ordered, func(a, b Comment) int {
		switch {
		case a.ParentID == "" && b.ParentID != "":
			return -1
		case a.ParentID != "" && b.ParentID == "":
			return 1
		}
		return 0
	})
	fail := func(id, check, detail string) error {
		return &ImportError{Code: FailContentInvalid, Section: "comments", Check: check, Detail: id + ": " + detail}
	}
	for _, x := range ordered {
		id, err := ids.Decode(ids.Comment, x.ID)
		if err != nil {
			return fail(x.ID, "id", err.Error())
		}
		root := x
		p := queries.InsertImportedCommentParams{ID: uuid(id), WorkspaceID: w.ws, ProjectID: w.project,
			ResolvedBy: w.user(x.ResolvedBy), ResolvedAt: stamp(x.ResolvedAt), EditedAt: stamp(x.EditedAt),
			CreatedBy: w.user(x.AuthorID), CreatedAt: pgtype.Timestamptz{Time: x.CreatedAt, Valid: true}}
		if x.ParentID != "" {
			parent, ok := roots[x.ParentID]
			if !ok || x.ThreadID != x.ParentID {
				return fail(x.ID, "thread", "a reply's parent is the root of its thread")
			}
			root = parent
			if p.ParentID, err = optional(ids.Comment, x.ParentID); err != nil {
				return fail(x.ID, "parent", err.Error())
			}
		} else {
			if x.ThreadID != x.ID {
				return fail(x.ID, "thread", "a root is its own thread")
			}
			var target pgtype.UUID
			kind, ok := targetKinds[x.TargetType]
			if !ok {
				return fail(x.ID, "target", "a root's target is of no kind that a comment takes")
			}
			if target, err = optional(kind, x.TargetID); err != nil || !target.Valid {
				return fail(x.ID, "target", "a root has no target of its kind")
			}
			switch x.TargetType {
			case "node":
				p.NodeID = target
			case "connection":
				p.ConnectionID = target
			case "board":
				p.BoardID = target
			case "variable":
				p.VariableID = target
			}
		}
		p.ThreadAt = pgtype.Timestamptz{Time: root.CreatedAt, Valid: true}
		if x.Deleted {
			p.Body = emptyBody
			p.DeletedAt = pgtype.Timestamptz{Time: w.at, Valid: true}
			p.CreatedBy = pgtype.UUID{}
		} else {
			if len(x.Body) == 0 {
				return fail(x.ID, "body", "a comment that is not deleted has a body")
			}
			if p.Body, err = rewriteMentions(x.Body, w.users); err != nil {
				return fail(x.ID, "body", err.Error())
			}
			problems, err := comment.CheckImported(p.Body)
			if err != nil {
				return fail(x.ID, "body", err.Error())
			}
			if len(problems) > 0 {
				return fail(x.ID, "body", problems[0].Field+" "+problems[0].Message)
			}
		}
		if err := w.q.InsertImportedComment(ctx, p); err != nil {
			return sectionError("comments", x.ID, err)
		}
	}
	return nil
}

// playStates adds the archive's play states as they were saved, each held to
// what a saved play state is held to.
func (w importWriter) playStates(ctx context.Context, list []PlayState) error {
	for _, x := range list {
		id, err := ids.Decode(ids.PlayState, x.ID)
		if err != nil {
			return &ImportError{Code: FailContentInvalid, Section: "play_states", Check: "id", Detail: x.ID}
		}
		names := []byte(x.Names)
		if len(names) == 0 || string(names) == "null" {
			names = []byte("{}")
		}
		if problems := playstate.CheckImported(w.project.Bytes, x.Name, x.State, names); len(problems) > 0 {
			return &ImportError{Code: FailContentInvalid, Section: "play_states", Check: problems[0].Field,
				Detail: x.ID + ": " + problems[0].Field + " " + problems[0].Message}
		}
		if err := w.q.InsertImportedPlayState(ctx, queries.InsertImportedPlayStateParams{ID: uuid(id), WorkspaceID: w.ws,
			ProjectID: w.project, Name: x.Name, State: x.State, Names: names, CreatedBy: w.user(x.CreatedBy),
			CreatedAt: pgtype.Timestamptz{Time: x.CreatedAt, Valid: true}}); err != nil {
			return sectionError("play_states", x.ID, err)
		}
	}
	return nil
}

// maxFilesBatch is how many bytes of a release's files one statement adds.
const maxFilesBatch = 64 << 20

// release adds a release of the archive as a build leaves one: building, with
// its files, and then ready, keeping its version, its hashes, and its bytes
// (FR-DATA-01). It returns the migration steps that reading it at the server's
// newest version takes, for the project's log.
func (w importWriter) release(ctx context.Context, tx pgx.Tx, rel Release, files schemaversion.Files) (
	[]schemaversion.Step, error) {
	fail := func(check string, err error) error {
		return &ImportError{Code: FailReleaseInvalid, Version: rel.Version, Check: check, Detail: firstLine(err.Error())}
	}
	id, err := ids.Decode(ids.Snapshot, rel.ID)
	if err != nil {
		return nil, fail("id", err)
	}
	v, err := snapshot.ParseVersion(rel.Version)
	if err != nil {
		return nil, fail("version", err)
	}
	hashes, contentHash, err := manifestFacts(files)
	if err != nil {
		return nil, fail("manifest", err)
	}
	if msg := snapshot.CheckNote(rel.Note); msg != "" {
		return nil, fail("note", fmt.Errorf("its note %s", msg))
	}
	report, err := rewriteReport(rel.ValidationReport, w.users)
	if err != nil {
		return nil, fail("validation_report", err)
	}
	warnings := []byte(rel.AcknowledgedWarnings)
	if len(warnings) == 0 || string(warnings) == "null" {
		warnings = []byte("[]")
	}
	if err := w.q.InsertImportedSnapshot(ctx, queries.InsertImportedSnapshotParams{ID: uuid(id), WorkspaceID: w.ws,
		ProjectID: w.project, Version: rel.Version, VersionMajor: v.Major, VersionMinor: v.Minor, VersionPatch: v.Patch,
		Note: rel.Note, AcknowledgedSeq: rel.AcknowledgedSeq, AcknowledgedWarnings: warnings,
		PublishedBy: w.user(rel.PublishedBy),
		PublishedAt: pgtype.Timestamptz{Time: rel.PublishedAt, Valid: true}}); err != nil {
		return nil, sectionError("releases", rel.Version, err)
	}
	paths := slices.Sorted(maps.Keys(files))
	var batch queries.InsertSnapshotFilesParams
	size := 0
	flush := func() error {
		if len(batch.Paths) == 0 {
			return nil
		}
		batch.WorkspaceID, batch.SnapshotID = w.ws, uuid(id)
		if err := w.q.InsertSnapshotFiles(ctx, batch); err != nil {
			return sectionError("releases", rel.Version, err)
		}
		batch, size = queries.InsertSnapshotFilesParams{}, 0
		return nil
	}
	for _, p := range paths {
		// The manifest holds the hash of each file but itself.
		hash, ok := hashes[p]
		if !ok {
			hash = string(published.HashOf(files[p]))
		}
		batch.Ids = append(batch.Ids, uuid(ids.New()))
		batch.Paths = append(batch.Paths, p)
		batch.Hashes = append(batch.Hashes, hash)
		batch.Contents = append(batch.Contents, files[p])
		size += len(files[p])
		if size >= maxFilesBatch {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	n, err := w.q.FinishImportedSnapshot(ctx, queries.FinishImportedSnapshotParams{WorkspaceID: w.ws, ID: uuid(id),
		SourceSeq: pgtype.Int8{Int64: rel.SourceSeq, Valid: true}, ValidationReport: report,
		SchemaVersion: pgtype.Int4{Int32: int32(rel.SchemaVersion), Valid: true},
		ContentHash:   pgtype.Text{String: contentHash, Valid: true}, Counts: rel.Counts,
		BuiltAt: pgtype.Timestamptz{Time: rel.BuiltAt, Valid: true}})
	if err != nil {
		return nil, sectionError("releases", rel.Version, err)
	}
	if n == 0 {
		return nil, fail("status", errors.New("the release was not building"))
	}
	if rel.SchemaVersion >= schemaversion.Supported().Max {
		return nil, nil
	}
	_, steps, err := schemaversion.Migrate(files, schemaversion.Supported().Max)
	if err != nil {
		return nil, fail("migration", err)
	}
	return steps, nil
}
