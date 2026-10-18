package asset

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

// thumbnailAttempts is how many attempts the job of a thumbnail has: a few,
// for a store that does not answer for a while, but not the 25 of a job
// that must happen, because the image is its own thumbnail until then.
const thumbnailAttempts = 5

// thumbnailJob is the job that makes the thumbnail of an asset (FR-JOBS-01,
// FR-ASSET-02). Its jobs run one at a time in a process, on a queue of their
// own (jobs.Serial), so that one image at a time is decoded, and they never
// hold the workers of the other jobs, as those that email a link, while a
// burst of uploads waits for its thumbnails.
type thumbnailJob struct {
	WorkspaceID string `json:"workspace_id"`
	AssetID     string `json:"asset_id"`
}

func (thumbnailJob) Kind() string { return "asset_thumbnail" }

// reconciliationInterval is how often the orphan reconciliation runs. A blob
// that no asset refers to any longer, as after its project is purged, is
// deleted within this long, or, on an instance with more workspaces than a
// run reaches, by a later run.
const reconciliationInterval = time.Hour

// orphans is the job that deletes the blobs that no asset refers to (ADR-009).
type orphans struct{}

func (orphans) Kind() string { return "asset_orphans" }

// RegisterJobs registers the jobs of the assets in r: the job that makes an
// asset's thumbnail, which an upload enqueues, and which runs one at a time,
// and the orphan reconciliation, which runs every hour.
func (s *Service) RegisterJobs(r *jobs.Registry) {
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[thumbnailJob]) error {
		return s.withDB(job.DB).makeThumbnail(ctx, job.Args)
	})
	jobs.Serial(r, thumbnailJob{})
	jobs.Register(r, func(ctx context.Context, job *jobs.Job[orphans]) error {
		_, err := s.withDB(job.DB).Reconcile(ctx)
		return err
	})
	jobs.Periodic(r, reconciliationInterval, orphans{})
}

// withDB returns a Service like s that reads and writes db, as a job does on
// the workers' pool that it carries (package jobs). The storage reads the
// bucket that setup chose through s's own.
func (s *Service) withDB(db *database.DB) *Service {
	if db == nil {
		return s
	}
	c := *s
	c.db = db
	return &c
}

// makeThumbnail makes the thumbnail of the asset of args, unless the asset no
// longer exists, has one, or is one of the assets of its content whose
// thumbnail another job made, and records it for every asset of the
// workspace of the same content that has none. It decodes the image outside
// any transaction, and stores the thumbnail under the blob's lock, which the
// orphan reconciliation takes too, so that it never deletes a thumbnail that
// is about to be recorded, unless another job recorded one meanwhile. An
// image that does not decode, or whose decoding would take more memory than
// a thumbnail may (Thumbnail), keeps no thumbnail of its own: it is recorded
// as its own thumbnail, as a small image is, so that an upload of the same
// content to another project shares that, rather than enqueue a job that
// would fail the same way, and the job ends without another attempt. So it
// is safe to repeat.
func (s *Service) makeThumbnail(ctx context.Context, args thumbnailJob) error {
	workspaceID, err := ids.ParseUUID(args.WorkspaceID)
	if err != nil {
		return jobs.Cancel(fmt.Errorf("asset: the job's workspace: %w", err))
	}
	assetID, err := ids.ParseUUID(args.AssetID)
	if err != nil {
		return jobs.Cancel(fmt.Errorf("asset: the job's asset: %w", err))
	}
	var a queries.Asset
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		var err error
		a, err = queries.New(tx).AssetByID(ctx, queries.AssetByIDParams{WorkspaceID: nullUUID(workspaceID),
			ID: nullUUID(assetID)})
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The asset was deleted, as with its project.
		return nil
	case err != nil:
		return fmt.Errorf("asset: read the asset: %w", err)
	case a.ThumbnailKey.Valid:
		return nil
	}
	store, err := s.storage.Store(ctx)
	if err != nil {
		return err
	}
	r, _, err := store.Open(ctx, a.StorageKey)
	switch {
	case errors.Is(err, blob.ErrNotFound):
		return jobs.Cancel(fmt.Errorf("asset: the store has no file of asset %s", assetID))
	case err != nil:
		return fmt.Errorf("asset: %w", err)
	}
	data, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		return fmt.Errorf("asset: read the file of asset %s: %w", assetID, err)
	}
	thumb, failed := Thumbnail(data, a.Mime)
	if ctx.Err() != nil {
		return fmt.Errorf("asset: make the thumbnail of asset %s: %w", assetID, ctx.Err())
	}
	// A file that does not decode is its own thumbnail.
	key := a.StorageKey
	if failed == nil {
		key = thumbnailKey(workspaceID, a.Sha256)
	}
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := lockBlob(ctx, q, workspaceID, a.Sha256); err != nil {
			return err
		}
		// Another job of the content may have recorded its thumbnail since,
		// as on another instance, which this one then need not store.
		switch _, err := q.ContentThumbnail(ctx, queries.ContentThumbnailParams{
			WorkspaceID: nullUUID(workspaceID), Sha256: a.Sha256}); {
		case err == nil:
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("asset: read the thumbnail of the content: %w", err)
		}
		if failed == nil {
			if err := store.Put(ctx, key, thumb, MimePNG); err != nil {
				return fmt.Errorf("asset: %w", err)
			}
		}
		if err := q.SetThumbnail(ctx, queries.SetThumbnailParams{WorkspaceID: nullUUID(workspaceID),
			Sha256: a.Sha256, ThumbnailKey: textOf(key)}); err != nil {
			return fmt.Errorf("asset: record the thumbnail: %w", err)
		}
		return nil
	})
	switch {
	case err != nil:
		return err
	case failed != nil:
		return jobs.Cancel(fmt.Errorf("asset: make the thumbnail of asset %s: %w", assetID, failed))
	}
	return nil
}

// workspaceKey matches the key of a blob of a workspace that this package
// writes, a file of any of the types (extensions) or a thumbnail, and
// captures the hash of its content.
var workspaceKey = regexp.MustCompile(`^[0-9a-f-]{36}/([0-9a-f]{64})\.(?:` +
	strings.Join(slices.Sorted(maps.Values(extensions)), "|") + `|thumb\.png)$`)

// errSpent stops the listing of a workspace's blobs once the run has spent
// its budget.
var errSpent = errors.New("asset: the run has spent its budget")

// Reconcile deletes every blob of a workspace that no asset of the workspace
// refers to, as its file or its thumbnail, as after the assets' project is
// purged, or an upload stored its blob and failed, and returns how many it
// deleted (ADR-009). It works on each workspace in transactions of the
// workspace (§5.4): one that reads the keys that its rows refer to, and then
// one for each blob of the store that none referred to, which takes the
// blob's lock, which an upload holds while it checks that the store has its
// blob and adds the row that refers to it, and the thumbnail job while it
// stores a thumbnail and records it, and checks the rows again, so that it
// never deletes a blob that a row is about to refer to. It starts no
// workspace, and lists or checks no blob, once it has run for sweep.Budget,
// going on from a workspace picked at random (sweep.Workspaces), and it
// leaves alone a key that this package would not write. So it is safe to
// repeat. The blobs of a workspace that no longer exists are deleted with it
// (plan item 110).
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	store, err := s.storage.Store(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	more := sweep.Within(sweep.Budget)
	err = sweep.Workspaces(ctx, s.db, more, func(workspaceID ids.UUID) error {
		var referenced []string
		if err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
			var err error
			referenced, err = queries.New(tx).ReferencedKeys(ctx, nullUUID(workspaceID))
			return err
		}); err != nil {
			return fmt.Errorf("asset: read the blobs that workspace %s refers to: %w", workspaceID, err)
		}
		known := make(map[string]bool, len(referenced))
		for _, k := range referenced {
			known[k] = true
		}
		// The blobs that no row referred to when the rows were read; each is
		// checked again under its lock before it is deleted. The listing of
		// a workspace of many blobs, which takes a request for each thousand
		// of a bucket's, stops within the run's budget too.
		var unreferenced []string
		switch err := store.List(ctx, workspaceID.String()+"/", func(i blob.Info) error {
			if !more() {
				return errSpent
			}
			if !known[i.Key] {
				unreferenced = append(unreferenced, i.Key)
			}
			return nil
		}); {
		case errors.Is(err, errSpent):
			return nil
		case err != nil:
			return fmt.Errorf("asset: list the blobs of workspace %s: %w", workspaceID, err)
		}
		for _, key := range unreferenced {
			// The run stops within its budget, here too, and leaves the rest
			// to the next run.
			if !more() {
				return nil
			}
			m := workspaceKey.FindStringSubmatch(key)
			if m == nil {
				continue
			}
			sum, _ := hex.DecodeString(m[1])
			var gone bool
			err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
				q := queries.New(tx)
				if err := lockBlob(ctx, q, workspaceID, sum); err != nil {
					return err
				}
				referenced, err := q.BlobReferenced(ctx, queries.BlobReferencedParams{
					WorkspaceID: nullUUID(workspaceID), Key: key})
				if err != nil || referenced {
					return err
				}
				if err := store.Delete(ctx, key); err != nil {
					return fmt.Errorf("asset: %w", err)
				}
				gone = true
				return nil
			})
			if err != nil {
				return fmt.Errorf("asset: reconcile the blob %s: %w", key, err)
			}
			if gone {
				deleted++
			}
		}
		return nil
	})
	return deleted, err
}
