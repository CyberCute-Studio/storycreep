package asset

import (
	"bytes"
	"cmp"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

// thumbnailAttempts is how many attempts the job of a thumbnail has: enough
// that its retries, the first a second after the first attempt fails and
// each after twice as long as the last (package jobs), span about 8½
// minutes, and at least 7½ with the 10% that the waits vary by at random, so
// that a store that fails for a few minutes, at once or by not answering,
// has answered again before the last, but not the 25 of a job
// that must happen, because the image is its own thumbnail until then, and
// the orphan reconciliation enqueues the job again of a thumbnail that is
// missing (mendThumbnails).
const thumbnailAttempts = 10

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
// deleted within this long once it is orphanAge old, or, on an instance with
// more workspaces than a run reaches, by a later run.
const reconciliationInterval = time.Hour

// orphanAge is how long ago a blob that no asset refers to must have been
// stored for the orphan reconciliation to delete it, as the store says: long
// enough that an instance on a copy of another's database, which takes the
// store for its own until the other claims it again, within the hour
// (claimStore), finds none of the files that the other's users uploaded
// since the copy was made old enough by then, which the other stored, or,
// if it found them stored already, as no row referred to them, and old,
// stored again (refreshAge), while the files of a purged project, which were
// uploaded before it was, go at once, and those of an upload that failed a
// day later, which take up no storage_bytes meanwhile.
const orphanAge = 24 * time.Hour

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
	r, info, err := store.Open(ctx, a.StorageKey)
	switch {
	case errors.Is(err, blob.ErrNotFound):
		return jobs.Cancel(fmt.Errorf("asset: the store has no file of asset %s", assetID))
	case err != nil:
		return fmt.Errorf("asset: %w", err)
	}
	// The buffer has room for the file, of the size that the store says, up
	// to the most that an upload takes on any instance, which a file stored
	// before STORYCREEP_ASSET_MAX_BYTES was lowered may take, and for the
	// read that finds its end, so that it is not grown while the file is
	// read, which would hold the file about twice before it is decoded. A
	// store that says less than the file has grows it, as io.ReadAll would.
	var buf bytes.Buffer
	buf.Grow(int(min(max(info.Size, 0), config.MaxAssetMaxBytes)) + bytes.MinRead)
	_, err = buf.ReadFrom(r)
	_ = r.Close()
	if err != nil {
		return fmt.Errorf("asset: read the file of asset %s: %w", assetID, err)
	}
	data := buf.Bytes()
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
// purged, or an upload stored its blob and failed, once the store says that
// it was stored at least orphanAge ago, and returns how many it deleted
// (ADR-009). It works on each workspace in transactions of the workspace
// (§5.4): one that reads the keys that its rows refer to, of the blobs that
// it listed, and then one for each batch of those that none referred to
// (deleteOrphans), which takes the blobs' locks, one of which an upload
// holds while it checks that the store has its blob and adds the row that
// refers to it, and the thumbnail job while it stores a thumbnail and
// records it, and checks the rows again, so that it never deletes a blob
// that a row is about to refer to. A batch that fails, as one of a blob that
// the store refuses to delete, is left to the next run, and the batches after
// it, and the thumbnails, still have their turn. It starts no workspace, and
// lists or checks no blob, once it has run for sweep.Budget, or the server
// has begun to stop (jobs.Stopping), going on from a workspace picked at
// random (sweep.Workspaces), and it leaves alone a key that this package
// would not write. So it is safe to repeat. It reaches only the workspaces
// that exist, so the deletion of a workspace, which plan item 110 adds, and
// which nothing does until then, must delete the workspace's blobs itself. It
// opens the store once it reaches a workspace, and does nothing until
// first-run setup has completed, unless the configuration names the store
// (Storage.FinalStore), as setup may yet choose another.
//
// It deletes blobs only in a store that its database owns (claimStore): an
// instance that runs on a copy of another's database, as one to try a new
// version on, or to rehearse a restore, has the same workspaces, and the
// bucket that setup chose, and would otherwise delete every file that the
// other stores after the copy was made, which its own rows do not refer to,
// once it is orphanAge old. In a store that another database owns, or that a
// copy of this database's files writes too, it deletes nothing, mends the
// thumbnails all the same, warns in the service's log, and returns an
// *OwnerError, which ends its job cancelled, with the error, rather than
// attempt it again, as the next hour's run does, unless a workspace failed
// too, when it returns both, and the job is attempted again. It reads the
// store's claim again before each batch of deletions, and deletes no more
// once another has claimed the store since, as one that claimed it at the
// same moment, and asks the store how old each blob is again just before it
// deletes it, as an upload of another instance, whose locks are another
// database's, may have stored it again since the listing (deleteBlobs).
//
// It lists a workspace's blobs from a key picked at random, past the last to
// the first, for listBudget at most, so that a run that cannot list all of a
// workspace's blobs in that time leaves others than the run before did, and
// a run after another reaches them, unless the workspace has at most
// smallListing blobs, which it lists from the first, and then reads the rows
// of those blobs alone, so that the rows of a workspace of more assets than a
// run lists the blobs of are not all read each time. It then mends the
// thumbnails of the contents whose files the listing found, and whose
// thumbnails it reached (mendThumbnails): it enqueues the job of a content
// that has no thumbnail, as when its job ran out of attempts while the store
// did not answer, unless one waits, and forgets a thumbnail that the store
// lost, and enqueues its job, so that the thumbnail route serves neither a
// large file for good nor nothing.
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	var store blob.Store
	// cl is the run's claim of the store, once it reaches a workspace, and
	// stop the error that ends the run at the first workspace, as when the
	// store is not final yet, or cannot be opened or claimed.
	var cl *claim
	var stop error
	deleted := 0
	within, stopping := sweep.Within(sweep.Budget), jobs.Stopping(ctx)
	more := func() bool { return within() && !stopped(stopping) }
	goOn := func() bool { return stop == nil && more() }
	err := sweep.Workspaces(ctx, s.db, goOn, func(workspaceID ids.UUID) error {
		if store == nil {
			st, err := s.storage.FinalStore(ctx)
			if err == nil {
				cl, err = s.claimStore(ctx, st)
			}
			if err != nil {
				stop = err
				return err
			}
			store = st
		}
		n, err := s.reconcileWorkspace(ctx, store, workspaceID, cl, more)
		deleted += n
		return err
	})
	switch {
	case errors.Is(stop, ErrSetupRequired):
		return 0, nil
	case cl == nil || cl.refused == nil:
		return deleted, err
	}
	// The operator who reads why the job failed, or the log, learns why it
	// deletes nothing, whatever else failed: the error says whether another
	// database claimed the store, or a copy of this one, or the claim was
	// deleted while the run went on.
	s.log.WarnContext(ctx, "the orphan reconciliation deletes no more files of the assets, because this "+
		"database does not hold the claim of their store", "error", cl.refused)
	if err != nil {
		return deleted, errors.Join(cl.refused, err)
	}
	return deleted, jobs.Cancel(cl.refused)
}

// OwnerKey is the key of the blob of a store that names the database whose
// orphan reconciliation deletes blobs in it (claimStore). It is no
// workspace's, so no listing of a workspace's blobs finds it.
const OwnerKey = "storycreep-owner"

// ownerTakeover is how long after the database that a store's owner blob
// names last wrote it, as its reconciliation does each hour, another
// database's reconciliation takes the store over: long enough that an
// instance that is stopped for days keeps its store from a second instance
// that runs meanwhile on a copy of its database, and short enough that an
// instance that replaces it, restored from a backup into another server of
// PostgreSQL, as after a disaster, or into another major version, deletes
// orphans again within a week, which until then only take up space. It is
// also how long after a run last found a copy of its database's files
// writing the store it deletes nothing (claimStore).
const ownerTakeover = 7 * 24 * time.Hour

// OwnerError is the error of an orphan reconciliation that deletes no blob,
// because the store's owner blob names another database, or this one with a
// token that this one did not write, as a copy of its cluster's files writes
// (claimStore).
type OwnerError struct {
	// Owner is the identity of the database that the owner blob names
	// (queries.DatabaseIdentity), or "" if the blob was deleted, or emptied,
	// while the run went on, and Self this database's.
	Owner, Self string
	// Since is when the owner blob was last written, or, if Copy is set,
	// when a run last found the copy's claim of the store.
	Since time.Time
	// Copy is set if the blob names this database's identity with a token
	// that this database did not write.
	Copy bool
}

func (e *OwnerError) Error() string {
	since := e.Since.UTC().Format(time.RFC3339)
	switch {
	case e.Owner == "":
		return fmt.Sprintf("asset: the blob %s, which claims the store, was deleted, or emptied, while the "+
			"orphan reconciliation ran, so it deleted no more blobs; the next run claims the store again", OwnerKey)
	case e.Copy:
		return fmt.Sprintf("asset: another instance runs on a copy of this database's files, %q, as one "+
			"restored from a snapshot of its disk or of a managed database, a point-in-time restore, or a branch of "+
			"it, which has the same identity, and claims the same store, as a run last found at %s, so this one "+
			"deletes none of its blobs, which would delete the files that the other stores, nor does the other "+
			"once it finds this one's claim: two instances never share a store. This one deletes them again a week "+
			"after that, or as soon as the blob %s is deleted from the store once the other is gone", e.Self,
			since, OwnerKey)
	}
	return fmt.Sprintf("asset: the store belongs to another database, %q, whose orphan reconciliation last "+
		"claimed it at %s, while this one is %q, so this one deletes none of its blobs, which would delete the "+
		"files that the other stores: two instances never share a store. If this database replaces the other "+
		"one, as one restored from a dump, or one that PostgreSQL was upgraded to a new major version under, it "+
		"takes the store over a week after that, or as soon as the blob %s is deleted from the store", e.Owner,
		since, e.Self, OwnerKey)
}

// claim is a run's claim of the store (claimStore).
type claim struct {
	// self is the database's identity, and content what the run wrote to
	// the owner blob, which it finds there again before each batch of
	// deletions (checkClaim), or "" if it wrote nothing.
	self, content string
	// refused is set once the run deletes no blob, and says why.
	refused *OwnerError
}

// claimStore writes the store's owner blob, OwnerKey, anew, naming this
// database (queries.DatabaseIdentity) and a token that it records too, and
// returns the claim, so that the orphan reconciliation deletes blobs in
// store, if the store has no owner blob, as a new one, or one whose blob an
// operator deleted to hand it over, or one that names this database and the
// last token that it recorded, or the one before, which the blob still holds
// if the store refused the last, or one that was last written more than
// ownerTakeover ago. A database's reconciliation writes the blob each run,
// so that while it runs, no other database's takes the store over, as one
// restored from its backup into another server, or another database of its
// own, which has another identity, does: then it writes nothing, and returns
// a claim refused with an *OwnerError. A copy of the database that runs
// from a copy of its cluster's files has the same identity, and the same
// tokens, until one of the two writes the blob again: the other then finds a
// token of its identity that it did not record, which it records the time
// of, and for ownerTakeover after the last time, it writes its own token
// still, so that the other finds it, and deletes nothing either. A copy that
// runs before the other has claimed the store since the copy was made, as
// one started at once on a copy just made, whose first run comes at its
// start, takes the store for its own until then, but deletes none of the
// files that the other stored since, which are younger than orphanAge, by
// when the other has claimed the store again.
//
// It writes a new token only once it finds the last one that it recorded in
// the store, or none of this database's: if the blob still holds the token
// before, as when the store refused to write the last, or has not written it
// yet for another run of this database at the same time, it writes the last
// again, and records nothing, so that the store holds one of the two tokens
// recorded however many writes in a row fail. It reads the blob, and records
// the token, in a transaction that holds the claim's advisory lock
// (database.StoreClaimLock), so that of two runs of this database at once,
// the second finds what the first recorded, and asks the store for
// lockedStoreTimeout at most meanwhile, which keeps no other writer of the
// instance's row, as first-run setup, waiting for the store. It writes the
// blob once the transaction has committed, so that a token that it writes is
// always recorded. An owner blob that names no database, as one emptied
// rather than deleted, claims the store for none, as no blob does.
func (s *Service) claimStore(ctx context.Context, store blob.Store) (*claim, error) {
	var cl *claim
	var conflict pgtype.Timestamptz
	var token string
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		self, err := q.DatabaseIdentity(ctx)
		if err != nil {
			return fmt.Errorf("asset: read the database's identity, which names the owner of the store, from "+
				"pg_control_system(), which PostgreSQL lets every login call unless its administrator revoked it: "+
				"%w", err)
		}
		if err := q.LockStoreClaim(ctx, database.StoreClaimLock); err != nil {
			return fmt.Errorf("asset: lock the claim of the store: %w", err)
		}
		last, err := q.StoreClaim(ctx)
		if err != nil {
			return fmt.Errorf("asset: read the claim of the store: %w", err)
		}
		storeCtx, cancel := context.WithTimeout(ctx, lockedStoreTimeout)
		owner, err := readOwner(storeCtx, store)
		cancel()
		if err != nil {
			return err
		}
		fresh := owner.found && time.Since(owner.written) < ownerTakeover
		conflict = last.AssetStoreConflictAt
		token = crand.Text()
		record := true
		switch {
		// An owner blob that names no database, as one that an operator
		// emptied rather than deleted, claims the store for none.
		case !owner.found, owner.identity == "", owner.identity != self && !fresh:
			conflict = pgtype.Timestamptz{}
		case owner.identity != self:
			cl = &claim{self: self, refused: &OwnerError{Owner: owner.identity, Self: self, Since: owner.written}}
			return nil
		case owner.token != "" && last.AssetStoreClaim.Valid && owner.token == last.AssetStoreClaimPrevious.String:
			// The last token's write has not reached the store.
			token, record = last.AssetStoreClaim.String, false
		case owner.token != "" && owner.token == last.AssetStoreClaim.String:
		case fresh:
			conflict = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		default:
			conflict = pgtype.Timestamptz{}
		}
		if record {
			if err := q.SetStoreClaim(ctx, queries.SetStoreClaimParams{Claim: token, ConflictAt: conflict}); err != nil {
				return fmt.Errorf("asset: record the claim of the store: %w", err)
			}
		}
		cl = &claim{self: self, content: self + " " + token + "\n"}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case cl.refused != nil:
		return cl, nil
	}
	// A claim whose write fails leaves the blob with the token before, which
	// the next run finds recorded as such.
	if err := store.Put(ctx, OwnerKey, []byte(cl.content), "text/plain"); err != nil {
		return nil, fmt.Errorf("asset: claim the store: %w", err)
	}
	if conflict.Valid && time.Since(conflict.Time) < ownerTakeover {
		cl.refused = &OwnerError{Owner: cl.self, Self: cl.self, Since: conflict.Time, Copy: true}
	}
	return cl, nil
}

// checkClaim reads the store's owner blob again, before a batch of
// deletions, and refuses cl, so that the run deletes no more blobs, unless
// the blob still holds what the run wrote, or a token that this database
// recorded since, as another run of it at the same time does: another
// instance that claimed the store at the same moment, or since, as one on a
// copy of the database that found no claim of this one's, then deletes
// nothing that this one stores, nor this one anything that it stores.
func (s *Service) checkClaim(ctx context.Context, store blob.Store, cl *claim) error {
	owner, err := readOwner(ctx, store)
	switch {
	case err != nil:
		return err
	case owner.found && owner.content == cl.content:
		return nil
	case owner.found && owner.identity == cl.self && owner.token != "":
		var last queries.StoreClaimRow
		if err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			var err error
			last, err = queries.New(tx).StoreClaim(ctx)
			return err
		}); err != nil {
			return fmt.Errorf("asset: read the claim of the store: %w", err)
		}
		if owner.token == last.AssetStoreClaim.String || owner.token == last.AssetStoreClaimPrevious.String {
			cl.content = owner.content
			return nil
		}
	}
	cl.refused = &OwnerError{Owner: owner.identity, Self: cl.self, Since: owner.written,
		Copy: owner.identity == cl.self}
	return nil
}

// ownerBlob is what a store's owner blob holds: the identity of the database
// that claimed the store, and its token, separated by a space, with when it
// was written, if found is set.
type ownerBlob struct {
	found                    bool
	content, identity, token string
	written                  time.Time
}

// maxOwnerBytes is the most of the owner blob that readOwner reads: more than
// an identity and a token take.
const maxOwnerBytes = 256

// readOwner reads the store's owner blob.
func readOwner(ctx context.Context, store blob.Store) (ownerBlob, error) {
	r, info, err := store.Open(ctx, OwnerKey)
	switch {
	case errors.Is(err, blob.ErrNotFound):
		return ownerBlob{}, nil
	case err != nil:
		return ownerBlob{}, fmt.Errorf("asset: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxOwnerBytes))
	_ = r.Close()
	if err != nil {
		return ownerBlob{}, fmt.Errorf("asset: read the store's owner: %w", err)
	}
	o := ownerBlob{found: true, content: string(data), written: info.Modified}
	if fields := strings.Fields(o.content); len(fields) > 0 {
		o.identity = fields[0]
		if len(fields) > 1 {
			o.token = fields[1]
		}
	}
	return o, nil
}

// reconcileWorkspace reconciles the blobs of the workspace in store, as
// Reconcile says, for as long as more reports that the run has time left,
// and returns how many it deleted, which is none once cl is refused.
func (s *Service) reconcileWorkspace(ctx context.Context, store blob.Store, workspaceID ids.UUID, cl *claim,
	more func() bool) (int, error) {
	l, err := listWorkspace(ctx, store, workspaceID, more)
	// A run that spent its budget on the listing reads no rows, as it would
	// neither delete nor mend anything of what they say.
	if err != nil || len(l.keys) == 0 || !more() {
		return 0, err
	}
	// The rows of the blobs that the listing found; each blob that none
	// referred to is checked again under its lock before it is deleted.
	var contents []queries.ContentKeysRow
	if err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		var err error
		contents, err = queries.New(tx).ContentKeys(ctx, queries.ContentKeysParams{
			WorkspaceID: nullUUID(workspaceID), Keys: l.keys})
		return err
	}); err != nil {
		return 0, fmt.Errorf("asset: read the blobs that workspace %s refers to: %w", workspaceID, err)
	}
	known := make(map[string]bool, 2*len(contents))
	for _, c := range contents {
		known[c.StorageKey] = true
		if c.ThumbnailKey.Valid {
			known[c.ThumbnailKey.String] = true
		}
	}
	var candidates []orphan
	for _, key := range l.keys {
		if cl.refused != nil || known[key] || l.young[key] {
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
		if err := s.checkClaim(ctx, store, cl); err != nil {
			errs = append(errs, err)
			break
		}
		if cl.refused != nil {
			break
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
// short when the job's time ends, as an upload's store requests under the
// lock are after lockedStoreTimeout, because the store may carry out a
// deletion that its client gave up on, after the locks are released, and so
// after an upload found the blob and added the row that refers to it: the
// batch sends no deletion that might not end before the job's time does, nor
// any once the server has begun to stop, and waits orphanDeleteTimeout for
// each that it sent (deleteBlobs).
const (
	orphanBatch   = 64
	orphanDeletes = 8
)

// orphanDeleteTimeout is how long the reconciliation waits for the store to
// delete a blob of a batch, from when it sends the deletion, whatever is left
// of the job's time, before it gives up on the deletion, and the batch's
// transaction ends, and with it its locks: far longer than a store that
// answers at all takes, and short enough that the job's time holds it after
// the run's budget, as no blob is asked for once the job's time has less
// than this and orphanStatTimeout left (deleteBlobs), so that the job ends in
// its time. The time that the store takes to say how old the blob is, just
// before, is not part of it, so that a store slow to answer the one leaves
// the other its whole time. It is shorter than the 20 seconds that a server
// that stops gives its jobs to finish before it cancels them (package
// httpserver's shutdownTimeout), in which a batch sends no more deletions
// (jobs.Stopping), so that those that it sent end, and are waited for, before
// the process exits, which would release the locks while the store might
// still carry them out.
const orphanDeleteTimeout = 15 * time.Second

// orphanStatTimeout is how long the reconciliation waits for the store to
// say how old a blob of a batch is, just before it deletes it (deleteOld),
// before it gives up, and sends no deletion, which it leaves to the next run:
// far longer than a store that answers at all takes, and short enough that,
// with orphanDeleteTimeout, it fits in the 20 seconds that the run's budget
// leaves of the minute that a job has (sweep.Budget).
const orphanStatTimeout = 5 * time.Second

// orphan is a blob of a workspace that no row referred to when the
// reconciliation read the rows: its key, and the hash of its content, whose
// lock it takes before it deletes it.
type orphan struct {
	key string
	sum []byte
}

// keepSessionIdle is how long the transaction of a batch of orphans may wait
// idle before the database ends its session (queries.KeepSession): longer
// than the batch ever waits for the store, whose deletions end within
// orphanDeleteTimeout of the last that it sent, so that its locks hold until
// they end, and short enough that the locks of a worker that went away
// without closing its connection, as when its host failed, are released
// within minutes, and an upload of one of the batch's contents waits no
// longer.
const keepSessionIdle = 5 * time.Minute

// deleteOrphans deletes the blobs of batch, of the workspace, in store, that
// no row refers to, and returns how many it deleted. It takes their locks in
// a transaction of the workspace, in the order of their integers
// (database.LockObject), in one statement, so that no other transaction
// that takes several of them waits for one that this one holds while it
// holds one that this one waits for, checks the blobs' rows again, in one
// query, and deletes the blobs that no row refers to, and that the store
// still says are orphanAge old (deleteBlobs), orphanDeletes at once, before
// the transaction ends, so that no upload or thumbnail job refers to one of
// them in between. The locks hold only for as long as the session
// does, so the transaction first switches off the timeout of a transaction
// that runs for long, and makes that of one that waits idle keepSessionIdle
// (queries.KeepSession), as an operator may set one that ends a transaction
// that waits idle for a few seconds, which would release the locks while the
// deletions go on.
func (s *Service) deleteOrphans(ctx context.Context, store blob.Store, workspaceID ids.UUID, batch []orphan) (int,
	error) {
	batch = slices.Clone(batch)
	slices.SortFunc(batch, func(a, b orphan) int {
		return cmp.Compare(database.LockObject(workspaceID, a.sum), database.LockObject(workspaceID, b.sum))
	})
	keys := make([]string, len(batch))
	objects := make([]int32, len(batch))
	for i, o := range batch {
		keys[i], objects[i] = o.key, database.LockObject(workspaceID, o.sum)
	}
	deleted := 0
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := q.KeepSession(ctx, strconv.FormatInt(keepSessionIdle.Milliseconds(), 10)); err != nil {
			return fmt.Errorf("asset: set the timeouts of the session: %w", err)
		}
		if err := lockBlobs(ctx, q, objects); err != nil {
			return err
		}
		refs, err := q.ReferencedBlobs(ctx, queries.ReferencedBlobsParams{WorkspaceID: nullUUID(workspaceID),
			Keys: keys})
		if err != nil {
			return fmt.Errorf("asset: read the assets of the blobs: %w", err)
		}
		gone := slices.DeleteFunc(slices.Clone(keys), func(key string) bool { return slices.Contains(refs, key) })
		deleted, err = deleteBlobs(ctx, jobs.Stopping(ctx), store, gone)
		return err
	})
	if err != nil {
		return deleted, fmt.Errorf("asset: reconcile the blobs of workspace %s: %w", workspaceID, err)
	}
	return deleted, nil
}

// deleteBlobs deletes the blobs of keys from store, orphanDeletes at once,
// and returns how many it deleted, and the errors of those that it did not.
// It asks the store for each blob just before it deletes it (deleteOld), and
// leaves alone one that the store says was stored less than orphanAge ago,
// or no longer has: the locks that the batch holds are this database's, so
// they do not hold back an upload of an instance on a copy of the database,
// which stores again a blob that no row of its own refers to, and that is
// old (refreshAge), and may have done so since the listing found the blob
// old, and added the row that refers to it. Such an upload that stores the
// blob in the moment between the question and the deletion still loses its
// file, which is one reason why two instances never share a store. Once ctx
// ends, or has less than orphanStatTimeout and orphanDeleteTimeout left, or
// stopping, the channel of jobs.Stopping, is closed, as the server has begun
// to stop, it asks for no more blobs, and leaves the rest to the next run,
// and it waits for each deletion that it sent for orphanDeleteTimeout,
// however soon ctx ends.
func deleteBlobs(ctx context.Context, stopping <-chan struct{}, store blob.Store, keys []string) (int, error) {
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
		if end, ok := ctx.Deadline(); (ok && time.Until(end) < orphanStatTimeout+orphanDeleteTimeout) ||
			stopped(stopping) {
			break
		}
		wg.Go(func() {
			defer func() { <-sending }()
			gone, err := deleteOld(ctx, stopping, store, key)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				errs = append(errs, err)
			case gone:
				deleted++
			}
		})
	}
	wg.Wait()
	return deleted, errors.Join(errs...)
}

// deleteOld deletes the blob of key from store if the store says that it was
// stored at least orphanAge ago, and reports whether it deleted it. It waits
// for the store to say for orphanStatTimeout at most, or until ctx ends, and
// then sends no deletion, nor once stopping is closed, and waits for the
// deletion that it sends for orphanDeleteTimeout from when it sends it,
// however soon ctx ends, as the store may carry out a deletion that its
// client gave up on, after the batch's locks are released.
func deleteOld(ctx context.Context, stopping <-chan struct{}, store blob.Store, key string) (bool, error) {
	statCtx, cancel := context.WithTimeout(ctx, orphanStatTimeout)
	info, err := store.Stat(statCtx, key)
	cancel()
	switch {
	case errors.Is(err, blob.ErrNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("asset: %w", err)
	case time.Since(info.Modified) < orphanAge:
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if stopped(stopping) {
		return false, nil
	}
	deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), orphanDeleteTimeout)
	defer cancel()
	if err := store.Delete(deleteCtx, key); err != nil {
		return false, fmt.Errorf("asset: %w", err)
	}
	return true, nil
}

// stopped reports whether stopping, a channel of jobs.Stopping, is closed,
// as once the server has begun to stop; a nil channel never is.
func stopped(stopping <-chan struct{}) bool {
	select {
	case <-stopping:
		return true
	default:
		return false
	}
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
	// young are the keys listed of the blobs stored less than orphanAge ago,
	// which the run deletes none of.
	young map[string]bool
}

// add adds the blob of i, the next that the store listed, to the listing.
func (l *listing) add(i blob.Info) {
	l.keys = append(l.keys, i.Key)
	l.last = i.Key
	if time.Since(i.Modified) < orphanAge {
		if l.young == nil {
			l.young = map[string]bool{}
		}
		l.young[i.Key] = true
	}
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

// smallListing is how many blobs a workspace may have for the
// reconciliation to list them from the first, whole, as one request of S3's
// lists up to a thousand, rather than from a key picked at random, past the
// last to the first, which takes two requests at least, so that a run lists
// a workspace of few blobs, as most are, with one request, and reaches twice
// as many of them in its time.
const smallListing = 1000

// errMany stops the listing of a workspace's blobs from the first once it
// finds more than smallListing.
var errMany = errors.New("asset: the workspace has many blobs")

// listWorkspace lists the workspace's blobs in store from the first, if it
// has at most smallListing, or else from after a key of the workspace picked
// at random, past the last to the first, until it reaches that key again, or
// the listing has taken listBudget, or more reports that the run has no time
// left.
func listWorkspace(ctx context.Context, store blob.Store, workspaceID ids.UUID, more func() bool) (*listing,
	error) {
	prefix := workspaceID.String() + "/"
	within := sweep.Within(listBudget)
	// The listing from the first starts after the prefix, which every key of
	// the workspace sorts after.
	l := &listing{from: prefix}
	err := store.List(ctx, prefix, "", func(i blob.Info) error {
		switch {
		case len(l.keys) == smallListing:
			return errMany
		case !more() || !within():
			return errSpent
		}
		l.add(i)
		return nil
	})
	switch {
	case err == nil:
		l.done = true
		return l, nil
	case errors.Is(err, errSpent):
		return l, nil
	case !errors.Is(err, errMany):
		return nil, fmt.Errorf("asset: list the blobs of workspace %s: %w", workspaceID, err)
	}
	// The key that the listing starts after has the form of a blob's key,
	// with no extension, so that it is none.
	from := prefix + fmt.Sprintf("%016x%016x%016x%016x", rand.Uint64(), rand.Uint64(), rand.Uint64(),
		rand.Uint64())
	l = &listing{from: from}
	visit := func(i blob.Info) error {
		if l.wrapped && i.Key > l.from {
			return errListed
		}
		if !more() || !within() {
			return errSpent
		}
		l.add(i)
		return nil
	}
	err = store.List(ctx, prefix, l.from, visit)
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
// read once the store was listed: for a content whose thumbnail the
// listing reached but did not find, under the blob's lock, once it has asked
// the store again, it forgets the thumbnail, and enqueues its job, and it
// enqueues the job of each content that has no thumbnail, unless one waits
// (enqueueThumbnail), mendBatch of them in a transaction, as many may have
// none while their jobs wait, after a burst of uploads, which a transaction
// each would spend the run on. A content whose file the store lost is left
// alone, which only an upload of the file mends. It goes on past a content
// whose thumbnail it fails to mend, as one that the store keeps refusing to
// say anything of, and returns the errors, as the contents are in the order
// of their hashes, so that one that fails every time would otherwise keep
// those after it from their turn for good. It stops once more reports that
// the run has spent its budget.
func (s *Service) mendThumbnails(ctx context.Context, store blob.Store, workspaceID ids.UUID,
	contents []queries.ContentKeysRow, l *listing, more func() bool) error {
	scope := database.Scope{WorkspaceID: workspaceID}
	var missing []queries.ContentKeysRow
	var errs []error
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
			return errors.Join(errs...)
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		err := s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
			q := queries.New(tx)
			if err := lockBlob(ctx, q, workspaceID, c.Sha256); err != nil {
				return err
			}
			// A thumbnail is stored before it is recorded, so one that the
			// listing missed was stored after the listing passed its key, if
			// it was stored at all: the store has the last word, which it has
			// lockedStoreTimeout to say, as under the lock of an upload.
			storeCtx, cancel := context.WithTimeout(ctx, lockedStoreTimeout)
			_, err := store.Stat(storeCtx, thumb)
			cancel()
			switch {
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
			errs = append(errs, fmt.Errorf("asset: mend the thumbnail of %s: %w", c.StorageKey, err))
		}
	}
	for batch := range slices.Chunk(missing, mendBatch) {
		if !more() {
			break
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if err := s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
			for _, c := range batch {
				if err := s.enqueueThumbnail(ctx, tx, workspaceID, c.ID.Bytes); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("asset: mend the thumbnails of workspace %s: %w", workspaceID, err))
		}
	}
	return errors.Join(errs...)
}

// mendBatch is how many jobs of thumbnails that are missing mendThumbnails
// enqueues in a transaction.
const mendBatch = 256
