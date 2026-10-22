package asset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Storage finds the store of package blob that keeps the instance's blobs
// (PRD FR-ASSET-01, ADR-009): the one that STORYCREEP_ASSET_STORAGE names,
// for as long as it names one, or else the bucket that first-run setup
// chose, which the instance's row holds (FR-ADMIN-02), or else the local
// driver's default directory. Which one it is takes configuration alone
// (FR-ASSET-01 AC1).
//
// Setup chooses once, so once setup has completed, the store that Storage
// finds stays the same until the server stops, and Storage keeps it. Before
// that, Storage finds the default store anew each time, so that what setup
// chooses applies at once, and it is not final: an account that `storycreep
// create-user` made may sign in, and have workspaces, before setup has
// completed, but an upload is refused until then (FinalStore), so that no
// file is stored where setup's bucket would leave it behind, and the orphan
// reconciliation deletes nothing until then either.
type Storage struct {
	db         DB
	fixed      *config.AssetStorage
	dir        string
	httpClient *http.Client

	// final is the final store, once there is one, which Store returns
	// without taking mu, as every request of an asset's file reads it.
	final atomic.Pointer[blob.Store]
	// mu guards opened, the stores opened before the final one, by their
	// settings, which Store reuses, so that it opens none twice.
	mu     sync.Mutex
	opened map[string]blob.Store
}

// StorageOptions configure NewStorage.
type StorageOptions struct {
	// Configured is the configuration's STORYCREEP_ASSET_STORAGE, or nil if
	// it sets none.
	Configured *config.AssetStorage
	// Dir is the local driver's directory when neither the configuration nor
	// setup names a store: config.DefaultAssetDir unless it is set. Tests
	// set it.
	Dir string
	// HTTPClient sends the S3 driver's requests, or nil for the SDK's own.
	HTTPClient *http.Client
}

// NewStorage returns a Storage that reads the bucket that setup chose in db.
// It opens no store until one is needed.
func NewStorage(db DB, opts StorageOptions) *Storage {
	dir := opts.Dir
	if dir == "" {
		dir = config.DefaultAssetDir
	}
	return &Storage{db: db, fixed: opts.Configured, dir: dir, httpClient: opts.HTTPClient}
}

// Fixed reports whether the configuration names the store, which then
// overrides the bucket that first-run setup chose.
func (s *Storage) Fixed() bool {
	return s.fixed != nil
}

// ErrSetupRequired is returned for an upload while first-run setup has not
// completed, and the configuration names no store, as setup may yet choose a
// bucket, which would leave behind a file stored before it, in the local
// driver's default directory (FinalStore).
var ErrSetupRequired = errors.New("asset: first-run setup has not completed, and may yet choose the store")

// Store returns the store of the instance's blobs, which it opens on first
// use. It returns an error if the store cannot be opened, as when the local
// driver's directory cannot be created, and opens it again on the next call.
func (s *Storage) Store(ctx context.Context) (blob.Store, error) {
	return s.store(ctx, false)
}

// FinalStore returns the store of the instance's blobs as Store does, once
// it is final: the configuration's, or the one that first-run setup chose,
// or the default directory, once setup has completed. Until then, it returns
// ErrSetupRequired, and opens none.
func (s *Storage) FinalStore(ctx context.Context) (blob.Store, error) {
	return s.store(ctx, true)
}

// store returns the store of the instance's blobs, as Store does, or, if
// final is set, as FinalStore does.
func (s *Storage) store(ctx context.Context, final bool) (blob.Store, error) {
	if f := s.final.Load(); f != nil {
		return *f, nil
	}
	// The settings are read before mu is taken, so that no caller waits for
	// another's transaction, as each does until the store is final, nor
	// holds mu for one that waits for a connection of the pool, whatever
	// its own context: once one caller has stored the final store, mu
	// gives it to those that read the settings before.
	settings, isFinal, err := s.settings(ctx)
	switch {
	case err != nil:
		return nil, err
	case final && !isFinal:
		return nil, ErrSetupRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.final.Load(); f != nil {
		return *f, nil
	}
	// The settings as the configuration writes them, with a bucket's secret
	// key, which tells two buckets apart, and stays in this map.
	name := settings.Dir
	if settings.S3 != nil {
		name = settings.S3.URL() + " " + settings.S3.SecretAccessKey.Reveal()
	}
	store, ok := s.opened[name]
	if !ok {
		if store, err = open(settings, s.httpClient); err != nil {
			return nil, err
		}
		if s.opened == nil {
			s.opened = map[string]blob.Store{}
		}
		s.opened[name] = store
	}
	if isFinal {
		s.final.Store(&store)
	}
	return store, nil
}

// Close closes the stores that Storage opened, as the local driver's, which
// holds its directory open, once nothing reads them any longer, as when the
// server stops. A call of Store after it opens a store anew.
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.final.Store(nil)
	var errs []error
	for _, store := range s.opened {
		if c, ok := store.(io.Closer); ok {
			errs = append(errs, c.Close())
		}
	}
	s.opened = nil
	return errors.Join(errs...)
}

// settings returns the settings of the store, and whether they are final:
// the configuration's, which are, or the bucket that setup chose, or the
// default directory, which are once setup has completed.
func (s *Storage) settings(ctx context.Context) (config.AssetStorage, bool, error) {
	if s.fixed != nil {
		return *s.fixed, true, nil
	}
	var row queries.InstanceS3Row
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		row, err = queries.New(tx).InstanceS3(ctx)
		return err
	})
	if err != nil {
		return config.AssetStorage{}, false, fmt.Errorf("asset: read the bucket that first-run setup chose: %w", err)
	}
	if !row.S3Host.Valid {
		return config.AssetStorage{Dir: s.dir}, row.SetupCompleted, nil
	}
	return config.AssetStorage{S3: &config.S3{
		Host:            row.S3Host.String,
		Port:            int(row.S3Port.Int32),
		Security:        config.S3Security(row.S3Security.String),
		Bucket:          row.S3Bucket.String,
		Region:          row.S3Region.String,
		AccessKeyID:     row.S3AccessKeyID.String,
		SecretAccessKey: secret.New(row.S3SecretAccessKey.String),
	}}, row.SetupCompleted, nil
}

// open opens the store of settings.
func open(settings config.AssetStorage, httpClient *http.Client) (blob.Store, error) {
	if settings.S3 != nil {
		return blob.OpenS3(*settings.S3, blob.S3Options{HTTPClient: httpClient}), nil
	}
	store, err := blob.OpenLocal(settings.Dir)
	if err != nil {
		return nil, fmt.Errorf("asset: %w", err)
	}
	return store, nil
}
