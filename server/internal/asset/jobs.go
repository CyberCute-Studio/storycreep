package asset

import (
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"sync"
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
// asset's thumbnail, which an upload, or the orphan reconciliation, enqueues,
// and which runs one at a time, and the orphan reconciliation, which runs
// every hour.
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
// longer exists, or has one, and records it for every asset of the
// workspace of the same content that has none. It decodes the image outside
// any transaction, and stores the thumbnail under the blob's lock, which the
// orphan reconciliation takes too, so that it never deletes a thumbnail that
// is about to be recorded, unless another job recorded one meanwhile, which
// it then records for the assets that have none instead. An image that does
// not decode, or whose decoding would take more memory than a thumbnail may
// (Thumbnail), which an upload finds from the image's header, keeps no
// thumbnail of its own: it is recorded as its own thumbnail, as a small image
// is, so that an upload of the same content to another project shares that,
// rather than enqueue a job that would fail the same way, and the job ends
// without another attempt, cancelled with the error that kept it from making
// the thumbnail. So it is safe to repeat.
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
		// as on another instance, which this one then need not store, but
		// records for the assets that have none.
		switch recorded, err := q.ContentThumbnail(ctx, queries.ContentThumbnailParams{
			WorkspaceID: nullUUID(workspaceID), Sha256: a.Sha256}); {
		case err == nil:
			key, failed = recorded, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("asset: read the thumbnail of the content: %w", err)
		case failed == nil:
			// The store has lockedStoreTimeout to take the thumbnail, as an
			// upload's file under the lock, so that one that does not answer
			// keeps an upload of the content waiting no longer; a thumbnail that
			// it stores after all is one that no row refers to, which the
			// reconciliation deletes.
			storeCtx, cancel := context.WithTimeout(ctx, lockedStoreTimeout)
			err := store.Put(storeCtx, key, thumb, MimePNG)
			cancel()
			if err != nil {
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
// its budget, or the listing its share of it (listBudget).
var errSpent = errors.New("asset: the run has spent its budget")

// errListed stops the listing of a workspace's blobs once it has gone past
// the last to the first and reached the key that it started after.
var errListed = errors.New("asset: the workspace's blobs are listed")

// listBudget is how much of a run's budget the listing of a workspace's blobs
// may take, so that the run has time left to delete the blobs that the
// listing found that no asset refers to, and to mend the thumbnails, of a
// workspace whose listing takes longer, as one of hundreds of thousands of
// blobs in a slow bucket, which takes a request for each thousand.
const listBudget = sweep.Budget / 2

// Reconcile deletes every blob of a workspace that no asset of the workspace
// refers to, as its file or its thumbnail, as after the assets' project is
// purged, or an upload stored its blob and failed, and returns how many it
// deleted (ADR-009). It works on each workspace in transactions of the
// workspace (§5.4): one that reads the keys that its rows refer to, and then
// one for each batch of the blobs of the store that none referred to
// (deleteOrphans), which takes the blobs' locks, one of which an upload
// holds while it checks that the store has its blob and adds the row that
// refers to it, and the thumbnail job while it stores a thumbnail and
// records it, and checks the rows again, so that it never deletes a blob
// that a row is about to refer to. A batch that fails, as one of a blob that
// the store refuses to delete, is left to the next run, and the batches after
// it, and the thumbnails, still have their turn. It starts no workspace, and
// lists or checks no blob, once it has run for sweep.Budget, going on from a
// workspace picked at random (sweep.Workspaces), and it leaves alone a key
// that this package would not write. So it is safe to repeat. The blobs of a
// workspace that no longer exists are deleted with it (plan item 110). It
// opens the store once it reaches a workspace, so that a run of an instance
// whose setup is pending, which has none, does nothing.
//
// It lists a workspace's blobs from a key picked at random, past the last to
// the first, for listBudget at most, so that a run that cannot list all of a
// workspace's blobs in that time leaves others than the run before did, and
// a run after another reaches them. It then mends the thumbnails of the
// contents whose files the listing found, and whose thumbnails it reached
// (mendThumbnails): it enqueues the job of a content that has no thumbnail,
// as when its job ran out of attempts while the store did not answer, unless
// one waits, and forgets a thumbnail that the store lost, and enqueues its
// job, so that the thumbnail route serves neither a large file for good nor
// nothing.
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	var store blob.Store
	var openErr error
	deleted := 0
	more := sweep.Within(sweep.Budget)
	// A store that cannot be opened ends the run, at the first workspace.
	goOn := func() bool { return openErr == nil && more() }
	err := sweep.Workspaces(ctx, s.db, goOn, func(workspaceID ids.UUID) error {
		if store == nil {
			if store, openErr = s.storage.Store(ctx); openErr != nil {
				return openErr
			}
		}
		n, err := s.reconcileWorkspace(ctx, store, workspaceID, more)
		deleted += n
		return err
	})
	return deleted, err
}

// reconcileWorkspace reconciles the blobs of the workspace in store, as
// Reconcile says, for as long as more reports that the run has time left,
// and returns how many it deleted.
func (s *Service) reconcileWorkspace(ctx context.Context, store blob.Store, workspaceID ids.UUID,
	more func() bool) (int, error) {
	var contents []queries.ContentKeysRow
	if err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		var err error
		contents, err = queries.New(tx).ContentKeys(ctx, nullUUID(workspaceID))
		return err
	}); err != nil {
		return 0, fmt.Errorf("asset: read the blobs that workspace %s refers to: %w", workspaceID, err)
	}
	known := make(map[string]bool, len(contents))
	for _, c := range contents {
		known[c.StorageKey] = true
		if c.ThumbnailKey.Valid {
			known[c.ThumbnailKey.String] = true
		}
	}
	// The blobs that no row referred to when the rows were read; each is
	// checked again under its lock before it is deleted.
	l, err := listWorkspace(ctx, store, workspaceID, more)
	if err != nil {
		return 0, err
	}
	var candidates []orphan
	for _, key := range l.keys {
		if known[key] {
			continue
		}
		m := workspaceKey.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		sum, _ := hex.DecodeString(m[1])
		candidates = append(candidates, orphan{key: key, sum: sum})
	}
	deleted := 0
	var errs []error
	for batch := range slices.Chunk(candidates, orphanBatch) {
		// The run stops within its budget, here too, and leaves the rest to
		// the next run.
		if !more() {
			return deleted, errors.Join(errs...)
		}
		if err := ctx.Err(); err != nil {
			return deleted, errors.Join(append(errs, err)...)
		}
		// A batch that fails, as one of a blob that the store refuses to
		// delete, keeps neither the next ones nor the thumbnails from their
		// turn: the candidates are in the order of their keys, so a blob that
		// the store refuses every time would keep every one after it for good.
		n, err := s.deleteOrphans(ctx, store, workspaceID, batch)
		deleted += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.mendThumbnails(ctx, store, workspaceID, contents, l, more); err != nil {
		errs = append(errs, err)
	}
	return deleted, errors.Join(errs...)
}

// orphanBatch is how many of a workspace's blobs that no row referred to the
// reconciliation checks again and deletes in a transaction, which holds all
// of their locks while it does, and orphanDeletes how many of their
// deletions it sends the store at once, so that the blobs of a project of
// thousands of images that was purged are deleted within a run or a few,
// rather than one request after another, each of which waits for the last,
// as a bucket answers a few dozen a second. A batch holds a worker's
// connection, as the job does, and makes an upload of one of its contents
// wait for its deletions, which take moments. Its deletions are not cut
// short, as an upload's store requests under the lock are
// (lockedStoreTimeout), but for the end of the job's own time: the store
// may carry out a deletion that its client gave up on, after the locks are
// released, and so after an upload found the blob and added the row that
// refers to it.
const (
	orphanBatch   = 64
	orphanDeletes = 8
)

// orphan is a blob of a workspace that no row referred to when the
// reconciliation read the rows: its key, and the hash of its content, whose
// lock it takes before it deletes it.
type orphan struct {
	key string
	sum []byte
}

// deleteOrphans deletes the blobs of batch, of the workspace, in store, that
// no row refers to, and returns how many it deleted. It takes their locks in
// a transaction of the workspace, in the order of their integers
// (database.LockObject), so that no other transaction that takes several of
// them waits for one that this one holds while it holds one that this one
// waits for, checks each blob's rows again, and deletes those that no row
// refers to, orphanDeletes at once, before the transaction ends, so that no
// upload or thumbnail job refers to one of them in between. The locks hold
// only for as long as the session does, so the transaction first switches
// off the timeouts that would end it while it waits for the store
// (queries.KeepSession), as an operator may set one that ends a transaction
// that waits idle for a few seconds, which would release the locks while the
// deletions go on.
func (s *Service) deleteOrphans(ctx context.Context, store blob.Store, workspaceID ids.UUID, batch []orphan) (int,
	error) {
	batch = slices.Clone(batch)
	slices.SortFunc(batch, func(a, b orphan) int {
		return cmp.Compare(database.LockObject(workspaceID, a.sum), database.LockObject(workspaceID, b.sum))
	})
	deleted := 0
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := q.KeepSession(ctx); err != nil {
			return fmt.Errorf("asset: switch off the timeouts of the session: %w", err)
		}
		for _, o := range batch {
			if err := lockBlob(ctx, q, workspaceID, o.sum); err != nil {
				return err
			}
		}
		var gone []string
		for _, o := range batch {
			referenced, err := q.BlobReferenced(ctx, queries.BlobReferencedParams{
				WorkspaceID: nullUUID(workspaceID), Key: o.key})
			if err != nil {
				return fmt.Errorf("asset: read the assets of the blob %s: %w", o.key, err)
			}
			if !referenced {
				gone = append(gone, o.key)
			}
		}
		var err error
		deleted, err = deleteBlobs(ctx, store, gone)
		return err
	})
	if err != nil {
		return deleted, fmt.Errorf("asset: reconcile the blobs of workspace %s: %w", workspaceID, err)
	}
	return deleted, nil
}

// deleteBlobs deletes the blobs of keys from store, orphanDeletes at once,
// and returns how many it deleted, and the errors of those that it did not.
// Once ctx ends, it sends no more deletions, and waits for those that it
// sent.
func deleteBlobs(ctx context.Context, store blob.Store, keys []string) (int, error) {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		deleted int
		errs    []error
	)
	sending := make(chan struct{}, orphanDeletes)
	for _, key := range keys {
		sending <- struct{}{}
		if err := ctx.Err(); err != nil {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
			break
		}
		wg.Go(func() {
			defer func() { <-sending }()
			err := store.Delete(ctx, key)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("asset: %w", err))
				return
			}
			deleted++
		})
	}
	wg.Wait()
	return deleted, errors.Join(errs...)
}

// listing is what a run listed of a workspace's blobs: from after the key
// from, past the last to the first, up to last, or all of them once done.
type listing struct {
	// keys are the keys listed, in their order, once listWorkspace returns.
	keys []string
	from string
	// last is the last key listed since the listing went on from the first,
	// if wrapped is set, or else the last key listed.
	last    string
	wrapped bool
	done    bool
}

// has reports whether the listing listed key.
func (l *listing) has(key string) bool {
	_, found := slices.BinarySearch(l.keys, key)
	return found
}

// covers reports whether the listing reached key, so that it lists key if
// the store has it.
func (l *listing) covers(key string) bool {
	switch {
	case l.done:
		return true
	case key > l.from:
		return l.wrapped || key <= l.last
	}
	return l.wrapped && key <= l.last
}

// listWorkspace lists the workspace's blobs in store from after a key of the
// workspace picked at random, past the last to the first, until it reaches
// that key again, or the listing has taken listBudget, or more reports that
// the run has no time left.
func listWorkspace(ctx context.Context, store blob.Store, workspaceID ids.UUID, more func() bool) (*listing,
	error) {
	prefix := workspaceID.String() + "/"
	// The key that the listing starts after has the form of a blob's key,
	// with no extension, so that it is none.
	from := prefix + fmt.Sprintf("%016x%016x%016x%016x", rand.Uint64(), rand.Uint64(), rand.Uint64(),
		rand.Uint64())
	l := &listing{from: from}
	within := sweep.Within(listBudget)
	visit := func(i blob.Info) error {
		if l.wrapped && i.Key > l.from {
			return errListed
		}
		if !more() || !within() {
			return errSpent
		}
		l.keys = append(l.keys, i.Key)
		l.last = i.Key
		return nil
	}
	err := store.List(ctx, prefix, l.from, visit)
	if err == nil {
		l.wrapped, l.last = true, ""
		err = store.List(ctx, prefix, "", visit)
	}
	switch {
	case err == nil || errors.Is(err, errListed):
		l.done = true
	case !errors.Is(err, errSpent):
		return nil, fmt.Errorf("asset: list the blobs of workspace %s: %w", workspaceID, err)
	}
	// The keys after from were listed before those up to it.
	slices.Sort(l.keys)
	return l, nil
}

// mendThumbnails mends the thumbnails of the contents of the workspace's
// assets whose files are among the keys that the store listed, which were
// read before the store was listed: for a content whose thumbnail the
// listing reached but did not find, under the blob's lock, once it has asked
// the store again, it forgets the thumbnail, and enqueues its job, and it
// enqueues the job of each content that has no thumbnail, unless one waits
// (enqueueThumbnail), mendBatch of them in a transaction, as many may have
// none while their jobs wait, after a burst of uploads, which a transaction
// each would spend the run on. A content whose file the store lost is left
// alone, which only an upload of the file mends. It stops once more reports
// that the run has spent its budget.
func (s *Service) mendThumbnails(ctx context.Context, store blob.Store, workspaceID ids.UUID,
	contents []queries.ContentKeysRow, l *listing, more func() bool) error {
	scope := database.Scope{WorkspaceID: workspaceID}
	var missing []queries.ContentKeysRow
	for _, c := range contents {
		if !l.has(c.StorageKey) {
			continue
		}
		thumb := c.ThumbnailKey.String
		switch {
		case !c.ThumbnailKey.Valid:
			missing = append(missing, c)
			continue
		case thumb == c.StorageKey || !l.covers(thumb) || l.has(thumb):
			continue
		}
		if !more() {
			return nil
		}
		err := s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
			q := queries.New(tx)
			if err := lockBlob(ctx, q, workspaceID, c.Sha256); err != nil {
				return err
			}
			// A thumbnail is stored before it is recorded, so one that the
			// listing missed is missing still, but the store has the last
			// word.
			switch _, err := store.Stat(ctx, thumb); {
			case err == nil:
				return nil
			case !errors.Is(err, blob.ErrNotFound):
				return fmt.Errorf("asset: %w", err)
			}
			n, err := q.ForgetThumbnail(ctx, queries.ForgetThumbnailParams{WorkspaceID: nullUUID(workspaceID),
				Sha256: c.Sha256, ThumbnailKey: c.ThumbnailKey})
			if err != nil || n == 0 {
				return err
			}
			return s.enqueueThumbnail(ctx, tx, workspaceID, c.ID.Bytes)
		})
		if err != nil {
			return fmt.Errorf("asset: mend the thumbnail of %s: %w", c.StorageKey, err)
		}
	}
	for batch := range slices.Chunk(missing, mendBatch) {
		if !more() {
			return nil
		}
		if err := s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
			for _, c := range batch {
				if err := s.enqueueThumbnail(ctx, tx, workspaceID, c.ID.Bytes); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("asset: mend the thumbnails of workspace %s: %w", workspaceID, err)
		}
	}
	return nil
}

// mendBatch is how many jobs of thumbnails that are missing mendThumbnails
// enqueues in a transaction.
const mendBatch = 256
