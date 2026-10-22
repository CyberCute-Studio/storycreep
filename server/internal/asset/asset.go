// Package asset keeps a project's assets (PRD FR-ASSET-01, FR-ASSET-02,
// ADR-009): the images that its users upload, which its content refers to,
// as a node's cover or a component's portrait. It takes an upload through
// its pipeline, stores the file in the instance's store of package blob,
// checks every read of an asset, and serves it.
//
// An upload's type comes from its content alone (Sniff): a PNG, JPEG, GIF, or
// WebP image, whose header must decode, of a bounded size, or an SVG image,
// which is sanitized of its scripts and external references before it is
// stored, so that the server never stores, or serves, one as it was uploaded
// (FR-ASSET-02 AC1, AC2), and which may then take no more bytes than an
// upload may. Anything else is refused.
//
// A file is stored under {workspace}/{sha256}.{ext}, a key made of its
// workspace and the hash of its content, so that the same file uploaded twice
// in a workspace is stored once (FR-ASSET-01). A project has one asset for
// each content: uploading the same file to it again answers with the asset
// that it has, which makes an upload that a client sends again harmless. A
// blob counts once towards the workspace's storage_bytes, however many assets
// share it, and an upload that would take the workspace past its plan's
// storage is refused (FR-ASSET-02 AC4). The upload adds the asset under the
// blob's lock (database.BlobLock), which the orphan reconciliation takes too,
// before it deletes a blob that no asset refers to (Reconcile), so that it
// never deletes one that an upload is about to refer to: the upload stores
// the blob outside any transaction, so that no connection waits for the
// store, and then, under the lock, checks that the store still has it,
// stores it again if the reconciliation deleted it in between, and adds the
// asset.
//
// Uploading an asset is not a command of the pipeline (package commands): it
// adds a file, which nothing refers to yet, and no change of the project's
// content, which the commands that set a node's cover, or a component's
// portrait, make, and it can be neither undone nor redone. It is authorised
// as a change of the project all the same: it needs owner, admin, or editor
// as the effective role on the project (§5.6 "Upload images (assets)"), and
// an archived project, which is read-only, refuses it, as one in the trash is
// not found.
//
// Reading an asset is reading the project's content, which every role on it
// allows, and the access check reads the asset's row, in a transaction of the
// project's workspace, before any of its blob is read, so that no request
// reads another workspace's blob, whatever key it guesses (FR-ASSET-01 AC2).
// The local driver's blobs are served by the server, and the S3 driver's
// through a URL that the server signs for a few minutes, once it has checked
// the access.
//
// A thumbnail of a raster image larger than ThumbnailSide on a side is made
// by a job, once the asset exists (FR-ASSET-02); until then, and for an
// image that is small enough, an SVG, or one whose decoding would take too
// much memory, the image is its own thumbnail. The orphan reconciliation
// enqueues the job again of a thumbnail that was not made, or that the store
// lost (Reconcile).
package asset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

var (
	// ErrNotFound is returned for an asset that its project does not have,
	// and for a project that ReadProject would answer with
	// project.ErrNotFound, which it is.
	ErrNotFound = errors.New("asset: no such asset")
	// ErrInsufficientRole is returned when the user's role on the project
	// does not allow an upload (§5.6).
	ErrInsufficientRole = authz.ErrInsufficientRole
)

// SignedURLLifetime is how long a URL that the S3 driver signs reads its
// blob: long enough for a browser to follow the redirect, and short enough
// that a URL that leaks, as through a browser's history, soon stops working.
// A browser keeps a file that it has read for as long, whichever driver
// serves it, before the server checks the user's access to it again.
const SignedURLLifetime = 5 * time.Minute

// Asset is an asset as the API shows it.
type Asset struct {
	ID          string    `json:"id" doc:"The asset's ID"`
	ProjectID   string    `json:"project_id" doc:"The ID of the asset's project"`
	ContentType string    `json:"content_type" enum:"image/png,image/jpeg,image/gif,image/webp,image/svg+xml" doc:"The image's type, which the server found from its content"`
	Bytes       int64     `json:"bytes" doc:"The size of the file as it is stored, in bytes: for an SVG, once the server has removed what it does not allow"`
	Width       int       `json:"width,omitempty" doc:"The image's width in pixels, as it is shown, unless it is an SVG that does not say it, or says a size of more than 16,384 pixels on a side"`
	Height      int       `json:"height,omitempty" doc:"The image's height in pixels, as it is shown, unless it is an SVG that does not say it, or says a size of more than 16,384 pixels on a side"`
	SHA256      string    `json:"sha256" doc:"The SHA-256 hash of the file as it is stored, in lowercase hex"`
	UploadedBy  string    `json:"uploaded_by,omitempty" doc:"The ID of the account that uploaded it, unless the account has been deleted"`
	CreatedAt   time.Time `json:"created_at" doc:"When it was uploaded"`
}

// assetOf returns the asset of the row a.
func assetOf(a queries.Asset) Asset {
	out := Asset{ID: ids.Encode(ids.Asset, a.ID.Bytes), ProjectID: ids.Encode(ids.Project, a.ProjectID.Bytes),
		ContentType: a.Mime, Bytes: a.Bytes, Width: int(a.Width.Int32), Height: int(a.Height.Int32),
		SHA256: hex.EncodeToString(a.Sha256), CreatedAt: a.CreatedAt.Time}
	if a.UploadedBy.Valid {
		out.UploadedBy = ids.Encode(ids.User, a.UploadedBy.Bytes)
	}
	return out
}

// Queue enqueues jobs in a transaction, as *jobs.Queue does.
type Queue interface {
	Enqueue(ctx context.Context, tx pgx.Tx, args jobs.Args, opts *jobs.EnqueueOptions) (jobs.Enqueued, error)
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, and the user's role on them. It is
	// required.
	Projects *project.Service
	// Entitlements check the storage limit of the workspaces' plans
	// (FR-ENT-01). It is required.
	Entitlements *entitlements.Service
	// Storage finds the store of the blobs. It is required.
	Storage *Storage
	// Queue enqueues the jobs that make thumbnails. It is required.
	Queue Queue
	// MaxBytes is the most bytes that an upload's file may take
	// (STORYCREEP_ASSET_MAX_BYTES), which the route reads no more of, and
	// which an SVG image may take once it is sanitized too, or the upload
	// is refused with ErrTooLarge. It is required.
	MaxBytes int64
	// Log is where the orphan reconciliation warns that it deletes nothing
	// in a store that another database owns, or that a copy of this one
	// writes too (Reconcile), or nil for nowhere.
	Log *slog.Logger
}

// Service keeps the assets.
type Service struct {
	db       DB
	projects *project.Service
	ent      *entitlements.Service
	storage  *Storage
	queue    Queue
	maxBytes int64
	log      *slog.Logger
}

// New returns a Service that keeps the assets in db, with opts. It panics if
// an option that it requires is nil, or MaxBytes is not positive.
func New(db DB, opts Options) *Service {
	switch {
	case opts.Projects == nil:
		panic("asset: a Service needs the projects that its assets belong to")
	case opts.Entitlements == nil:
		panic("asset: a Service needs the entitlements that check its workspaces' storage limit")
	case opts.Storage == nil:
		panic("asset: a Service needs the storage of its blobs")
	case opts.Queue == nil:
		panic("asset: a Service needs the queue of its thumbnails' jobs")
	case opts.MaxBytes <= 0:
		panic("asset: a Service needs the most bytes that an upload's file may take")
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, projects: opts.Projects, ent: opts.Entitlements, storage: opts.Storage,
		queue: opts.Queue, maxBytes: opts.MaxBytes, log: log}
}

// Storage returns the storage of the blobs.
func (s *Service) Storage() *Storage {
	return s.storage
}

// MaxBytes returns the most bytes that an upload's file may take.
func (s *Service) MaxBytes() int64 {
	return s.maxBytes
}

// CheckUpload returns the error that an upload to the project projectID by
// the actor would be refused with whatever its file holds, as Upload checks
// it, so that a route can refuse the upload before it reads the file: the
// errors of project.Service.Get, then ErrInsufficientRole unless the actor's
// effective role on the project is owner, admin, or editor,
// commands.ErrProjectArchived if the project is archived, and
// ErrSetupRequired while first-run setup may yet choose the store
// (Storage.FinalStore). The scope of the actor's API token is the route's to
// check.
func (s *Service) CheckUpload(ctx context.Context, actor commands.Actor, projectID ids.UUID) error {
	p, err := s.projects.Get(ctx, actor, projectID)
	if err != nil {
		return err
	}
	if err := uploadable(p); err != nil {
		return err
	}
	_, err = s.storage.FinalStore(ctx)
	return err
}

// uploadable returns the error that an upload to the project p is refused
// with, whatever its file holds, or nil.
func uploadable(p project.Visible) error {
	if err := authz.Check(p.Role, authz.UploadAssets); err != nil {
		return err
	}
	if p.ArchivedAt != nil {
		return commands.ErrProjectArchived
	}
	return nil
}

// Upload adds data, a file that the actor uploads, to the project projectID
// as an asset, and returns it, with true if it is new, or the project's asset
// of the same content, which the project has already, with false. It returns
// the errors of CheckUpload, an *ImageError if data is not an image that an
// upload takes, or ErrTooLarge if it is an SVG image that, sanitized, would
// take more than MaxBytes (Sniff), and an *entitlements.LimitError of
// storage_bytes if the workspace does not store the file yet, and storing it
// would take the workspace past its plan's storage, and adds no asset then.
// An upload that the checks under the blob's lock refuse, as when another
// upload took the workspace's storage meanwhile, or the actor's role was
// revoked, has stored its blob already, which no asset refers to, and which
// the orphan reconciliation deletes a day later (Reconcile).
//
// It checks the upload in a transaction, then stores the blob outside any,
// so that no connection of the pool, and no lock, waits for the store to
// take a file of up to 50 MB, and then adds the asset in a transaction that
// holds the blob's lock, which checks again what may have changed since, the
// actor's role, the project's state, and the storage, and stores the blob
// again in the rare case that the orphan reconciliation deleted it in
// between, before the lock was taken, waiting for the store under the lock
// for lockedStoreTimeout at most. An upload of a file that the project
// has already stores its blob again too if the store lost it, and enqueues
// the job of its thumbnail again if it needs one and has none, as when the
// job ran out of attempts while the store did not answer, or found no file,
// so that uploading the file again mends its asset.
func (s *Service) Upload(ctx context.Context, actor commands.Actor, projectID ids.UUID, data []byte) (Asset, bool,
	error) {
	workspaceID, err := s.projects.ActorWorkspace(ctx, actor, projectID)
	if err != nil {
		return Asset{}, false, err
	}
	img, err := Sniff(data, int(s.maxBytes))
	if err != nil {
		return Asset{}, false, err
	}
	// Whether the image gets a thumbnail of its own, which reads every
	// segment of a JPEG file of up to 50 MB, so once.
	thumbnail := needsThumbnail(img)
	sum := sha256.Sum256(img.Data)
	key := blobKey(workspaceID, sum[:], img.Ext)
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return Asset{}, false, err
	}
	scope := database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}
	var st stored
	err = s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
		p, err := s.projects.GetIn(ctx, tx, actor, workspaceID, projectID)
		if err != nil {
			return err
		}
		if err := uploadable(p); err != nil {
			return err
		}
		st, err = s.checkStorage(ctx, tx, workspaceID, projectID, sum[:], key, img)
		return err
	})
	if err != nil {
		return Asset{}, false, err
	}
	// The blob is stored outside any transaction. An asset that the project
	// has already refers to it, so the reconciliation keeps it.
	if err := ensureBlob(ctx, store, key, img); err != nil {
		return Asset{}, false, err
	}
	if st.found {
		if st.mendsThumbnail(thumbnail) {
			if err := s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
				return s.enqueueThumbnail(ctx, tx, workspaceID, st.existing.ID.Bytes)
			}); err != nil {
				return Asset{}, false, err
			}
		}
		return assetOf(st.existing), false, nil
	}
	var out Asset
	created := false
	err = s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := lockBlob(ctx, q, workspaceID, sum[:]); err != nil {
			return err
		}
		// Another upload of the file to the project may have added its asset
		// since, and another of the workspace its blob.
		again, err := s.checkStorage(ctx, tx, workspaceID, projectID, sum[:], key, img)
		switch {
		case err != nil:
			return err
		case again.found:
			out = assetOf(again.existing)
			if again.mendsThumbnail(thumbnail) {
				return s.enqueueThumbnail(ctx, tx, workspaceID, again.existing.ID.Bytes)
			}
			return nil
		}
		// The reconciliation deletes a blob that no row refers to under its
		// lock, which it may have done after the blob was stored, before this
		// transaction took the lock, and cannot do now until the asset is
		// added.
		storeCtx, cancel := context.WithTimeout(ctx, lockedStoreTimeout)
		err = ensureBlob(storeCtx, store, key, img)
		cancel()
		if err != nil {
			return err
		}
		// The project is locked, so that it is neither archived nor deleted
		// until the asset is added, and read again with the actor's role on
		// it, which may have changed while the blob was stored, as the
		// workspace's deletion may have begun.
		if err := q.ShareProject(ctx, queries.ShareProjectParams{WorkspaceID: nullUUID(workspaceID),
			ID: nullUUID(projectID)}); err != nil {
			return fmt.Errorf("asset: lock the project: %w", err)
		}
		p, err := s.projects.GetIn(ctx, tx, actor, workspaceID, projectID)
		if err != nil {
			return err
		}
		if err := uploadable(p); err != nil {
			return err
		}
		row := queries.Asset{ID: nullUUID(ids.New()), WorkspaceID: nullUUID(workspaceID),
			ProjectID: nullUUID(projectID), Sha256: sum[:], Mime: img.Mime, Bytes: int64(len(img.Data)),
			StorageKey: key, UploadedBy: nullUUID(actor.UserID)}
		if img.Width > 0 {
			row.Width = pgtype.Int4{Int32: int32(img.Width), Valid: true}
			row.Height = pgtype.Int4{Int32: int32(img.Height), Valid: true}
		}
		enqueue := thumbnail
		if !thumbnail {
			row.ThumbnailKey = textOf(key)
		} else {
			// An asset of the same content in the workspace shares its
			// thumbnail, once its job has made it, which this one then needs
			// no job of its own for. The thumbnail job records a thumbnail
			// under the blob's lock, which this transaction holds.
			shared, err := q.ContentThumbnail(ctx, queries.ContentThumbnailParams{
				WorkspaceID: nullUUID(workspaceID), Sha256: sum[:]})
			switch {
			case err == nil:
				row.ThumbnailKey, enqueue = textOf(shared), false
			case !errors.Is(err, pgx.ErrNoRows):
				return fmt.Errorf("asset: read the thumbnail of the content: %w", err)
			}
		}
		createdAt, err := q.InsertAsset(ctx, queries.InsertAssetParams{ID: row.ID, WorkspaceID: row.WorkspaceID,
			ProjectID: row.ProjectID, Sha256: row.Sha256, Mime: row.Mime, Bytes: row.Bytes, Width: row.Width,
			Height: row.Height, StorageKey: row.StorageKey, ThumbnailKey: row.ThumbnailKey,
			UploadedBy: row.UploadedBy})
		if err != nil {
			return fmt.Errorf("asset: add the asset: %w", err)
		}
		row.CreatedAt = createdAt
		if !again.referenced {
			if err := usage.Record(ctx, tx, workspaceID, usage.StorageBytes, row.Bytes); err != nil {
				return err
			}
		}
		if enqueue {
			if err := s.enqueueThumbnail(ctx, tx, workspaceID, ids.UUID(row.ID.Bytes)); err != nil {
				return err
			}
		}
		out, created = assetOf(row), true
		return nil
	})
	if err != nil {
		return Asset{}, false, err
	}
	return out, created, nil
}

// enqueueThumbnail enqueues the job that makes the thumbnail of the asset
// assetID of the workspace in tx, unless an equal job waits or runs, which
// makes the thumbnail already.
func (s *Service) enqueueThumbnail(ctx context.Context, tx pgx.Tx, workspaceID, assetID ids.UUID) error {
	if _, err := s.queue.Enqueue(ctx, tx, thumbnailJob{WorkspaceID: workspaceID.String(), AssetID: assetID.String()},
		&jobs.EnqueueOptions{MaxAttempts: thumbnailAttempts, Unique: true}); err != nil {
		return fmt.Errorf("asset: enqueue the thumbnail's job: %w", err)
	}
	return nil
}

// lockedStoreTimeout bounds how long the transaction that holds a blob's lock
// waits for the store, which the S3 driver's client does not bound, so that a
// store that does not answer fails the upload, which its client may send
// again, rather than keep one of the few connections of the pool for as long
// as the client waits, while every upload of the instance may do the same.
// The store answers a HEAD in milliseconds, and the PUT that follows only in
// the rare case that the reconciliation deleted the blob since the upload
// stored it stores the file that it took moments before. It bounds the
// thumbnail job's PUT of a thumbnail under the lock too, and the HEAD with
// which the orphan reconciliation asks the store again for a thumbnail that
// its listing did not find (mendThumbnails), but not the reconciliation's
// deletions (orphanBatch).
const lockedStoreTimeout = 30 * time.Second

// stored is what checkStorage finds of an upload's file in its workspace.
type stored struct {
	// existing is the project's asset of the file, if found is set.
	existing queries.Asset
	found    bool
	// referenced is set if an asset of the workspace refers to the file's
	// blob, which then counts towards its storage_bytes already.
	referenced bool
}

// mendsThumbnail reports whether an upload of an image, which found the
// project's asset of its file, enqueues the job of the asset's thumbnail
// again: if the image needs one (needs, as needsThumbnail says), and the
// asset has none, as when its earlier job ran out of attempts while the store
// did not answer. The job that the asset's upload enqueued may still wait,
// which keeps an equal job from being enqueued (enqueueThumbnail).
func (st stored) mendsThumbnail(needs bool) bool {
	return st.found && !st.existing.ThumbnailKey.Valid && needs
}

// checkStorage returns the project's asset of the content sum, if it has
// one, or else checks that the workspace's storage takes the blob of key,
// which img is, unless an asset of the workspace refers to it already
// (entitlements), in tx.
func (s *Service) checkStorage(ctx context.Context, tx pgx.Tx, workspaceID, projectID ids.UUID, sum []byte,
	key string, img Image) (stored, error) {
	q := queries.New(tx)
	existing, err := q.ProjectAsset(ctx, queries.ProjectAssetParams{WorkspaceID: nullUUID(workspaceID),
		ProjectID: nullUUID(projectID), Sha256: sum})
	switch {
	case err == nil:
		return stored{existing: existing, found: true, referenced: true}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return stored{}, fmt.Errorf("asset: read the project's assets: %w", err)
	}
	refs, err := q.ReferencedBlobs(ctx, queries.ReferencedBlobsParams{WorkspaceID: nullUUID(workspaceID),
		Keys: []string{key}})
	if err != nil {
		return stored{}, fmt.Errorf("asset: read the assets of the blob: %w", err)
	}
	referenced := len(refs) > 0
	if !referenced {
		// The blob is new to the workspace, so it adds to its storage.
		if err := s.ent.Check(ctx, tx, workspaceID, entitlements.StorageBytes, int64(len(img.Data))); err != nil {
			return stored{}, err
		}
	}
	return stored{referenced: referenced}, nil
}

// ensureBlob stores img under key, unless the store has the blob already,
// which, its key being the hash of its content, is the same file.
func ensureBlob(ctx context.Context, store blob.Store, key string, img Image) error {
	_, err := store.Stat(ctx, key)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, blob.ErrNotFound):
		return fmt.Errorf("asset: %w", err)
	}
	if err := store.Put(ctx, key, img.Data, img.Mime); err != nil {
		return fmt.Errorf("asset: %w", err)
	}
	return nil
}

// Get returns the asset assetID of the project projectID, which the actor
// reads. It returns the errors of project.Service.Get, and ErrNotFound if the
// project has no such asset.
func (s *Service) Get(ctx context.Context, actor commands.Actor, projectID, assetID ids.UUID) (Asset, error) {
	loc, err := s.Locate(ctx, actor, projectID, assetID, false)
	if err != nil {
		return Asset{}, err
	}
	return loc.Asset, nil
}

// Located is an asset's file, or its thumbnail, that the actor may read.
type Located struct {
	// Asset is the asset.
	Asset Asset
	// Key is the key of the file, or of its thumbnail, in the store, which
	// is unique to its content.
	Key string
	// ContentType is the file's type.
	ContentType string
	// Pending is set if the thumbnail was asked for, and its job has not
	// made it yet, so that Key is the asset's file, which the thumbnail
	// route serves until then.
	Pending bool
	// Thumbnail is set if Key is a thumbnail that the job made, rather than
	// the asset's file.
	Thumbnail bool
}

// Locate returns the file of the asset assetID of the project projectID, or
// its thumbnail if thumbnail is set, which the actor reads, once it has
// checked that they may, in a transaction of the project's workspace,
// which reads none of the file. The thumbnail of an asset whose thumbnail
// job has not run yet is its file. It returns the errors of Get.
func (s *Service) Locate(ctx context.Context, actor commands.Actor, projectID, assetID ids.UUID, thumbnail bool) (
	Located, error) {
	workspaceID, err := s.projects.ActorWorkspace(ctx, actor, projectID)
	if err != nil {
		return Located{}, err
	}
	var out Located
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		if _, err := s.projects.GetIn(ctx, tx, actor, workspaceID, projectID); err != nil {
			return err
		}
		a, err := queries.New(tx).Asset(ctx, queries.AssetParams{WorkspaceID: nullUUID(workspaceID),
			ProjectID: nullUUID(projectID), ID: nullUUID(assetID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("asset: read the asset: %w", err)
		}
		out = Located{Asset: assetOf(a), Key: a.StorageKey, ContentType: a.Mime,
			Pending: thumbnail && !a.ThumbnailKey.Valid}
		if thumbnail && a.ThumbnailKey.Valid && a.ThumbnailKey.String != a.StorageKey {
			out.Key, out.ContentType, out.Thumbnail = a.ThumbnailKey.String, MimePNG, true
		}
		return nil
	})
	if err != nil {
		return Located{}, err
	}
	return out, nil
}

// SignedURL returns a URL of loc, which Locate returned once it had checked
// the actor's access, that the store signs for 5 minutes, with the file's
// type and Content-Disposition fixed in it, and true, or false if the store
// signs none, as the local driver, whose files the server serves itself
// (Open).
func (s *Service) SignedURL(ctx context.Context, loc Located) (string, bool, error) {
	store, err := s.storage.Store(ctx)
	if err != nil {
		return "", false, err
	}
	url, ok, err := store.SignedURL(ctx, loc.Key, SignedURLLifetime, loc.ContentType, Disposition(loc))
	if err != nil {
		return "", false, fmt.Errorf("asset: %w", err)
	}
	return url, ok, nil
}

// Open returns the file of loc, which Locate returned once it had checked the
// actor's access, which the caller closes, and its size in bytes. It returns
// ErrNotFound if the store lost the file.
func (s *Service) Open(ctx context.Context, loc Located) (io.ReadCloser, int64, error) {
	store, err := s.storage.Store(ctx)
	if err != nil {
		return nil, 0, err
	}
	body, info, err := store.Open(ctx, loc.Key)
	switch {
	case errors.Is(err, blob.ErrNotFound):
		return nil, 0, ErrNotFound
	case err != nil:
		return nil, 0, fmt.Errorf("asset: %w", err)
	}
	return body, info.Size, nil
}

// Disposition returns the Content-Disposition of loc: inline for an image,
// and an attachment for any other type, which a browser then saves rather
// than shows (FR-ASSET-02 AC3), with a file name made of the asset's ID and
// the extension of its key. Every type that an upload takes is an image, so
// no asset is an attachment yet, but a type that a later change takes is
// saved rather than shown unless the change says otherwise.
func Disposition(loc Located) string {
	kind := "attachment"
	if strings.HasPrefix(loc.ContentType, "image/") {
		kind = "inline"
	}
	return kind + `; filename="` + loc.Asset.ID + path.Ext(loc.Key) + `"`
}

// blobKey returns the key of the blob of the workspace whose content has the
// hash sum, of the extension ext: {workspace}/{sha256}.{ext}.
func blobKey(workspaceID ids.UUID, sum []byte, ext string) string {
	return workspaceID.String() + "/" + hex.EncodeToString(sum) + "." + ext
}

// thumbnailKey returns the key of the thumbnail of the blob of the workspace
// whose content has the hash sum: {workspace}/{sha256}.thumb.png.
func thumbnailKey(workspaceID ids.UUID, sum []byte) string {
	return workspaceID.String() + "/" + hex.EncodeToString(sum) + ".thumb.png"
}

// lockBlob takes the lock of the workspace's blob of the content whose hash
// is sum until the transaction of q ends (database.BlobLock), whose second
// integer is the content's (database.LockObject).
func lockBlob(ctx context.Context, q *queries.Queries, workspaceID ids.UUID, sum []byte) error {
	return lockBlobs(ctx, q, []int32{database.LockObject(workspaceID, sum)})
}

// lockBlobs takes the locks of a workspace's blobs whose second integers are
// objects (database.LockObject), in their order, in one statement, until the
// transaction of q ends.
func lockBlobs(ctx context.Context, q *queries.Queries, objects []int32) error {
	if err := q.LockBlobs(ctx, queries.LockBlobsParams{Class: database.BlobLock, Objects: objects}); err != nil {
		return fmt.Errorf("asset: lock the blob: %w", err)
	}
	return nil
}

// textOf returns s as a text column.
func textOf(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
