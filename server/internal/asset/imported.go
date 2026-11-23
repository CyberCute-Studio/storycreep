package asset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// ErrDuplicateContent is AddImported's error for a file that the project has
// an asset of already, as a project has one asset of each content.
var ErrDuplicateContent = errors.New("asset: the project has an asset of this content already")

// ImportedFile is an imported asset's file, once it is checked and stored
// (StoreImported). It holds what its row needs and not the file's bytes, so
// that an import's memory does not grow with the size of all of its assets.
type ImportedFile struct {
	image     Image
	size      int64
	sum       []byte
	key       string
	thumbnail bool
}

// Sum returns the SHA-256 of the file as it is stored, which sanitizing an SVG
// may have changed.
func (f ImportedFile) Sum() []byte { return f.sum }

// Size returns the size of the file as it is stored.
func (f ImportedFile) Size() int64 { return f.size }

// StoreImported checks data, an imported asset's file, as an upload's is: it
// tells its type from its content, refuses what is not an image that the
// server takes, sanitizes an SVG (FR-ASSET-01), checks that the workspace has
// room for it, as an upload does before it stores anything, and stores it as a
// blob of the workspace, outside any transaction. adding is how many bytes the
// import has stored so far that are new to the workspace, which this adds to.
// AddImported then adds its asset, under the blob's lock, and the orphan
// reconciliation deletes the blob if none ever does. It returns an
// *entitlements.LimitError for a workspace without the room.
func (s *Service) StoreImported(ctx context.Context, workspaceID ids.UUID, data []byte, adding *int64) (ImportedFile,
	error) {
	img, err := Sniff(data, int(s.maxBytes))
	if err != nil {
		return ImportedFile{}, err
	}
	sum := sha256.Sum256(img.Data)
	key := blobKey(workspaceID, sum[:], img.Ext)
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return ImportedFile{}, err
	}
	size := int64(len(img.Data))
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID}, func(tx pgx.Tx) error {
		refs, err := queries.New(tx).ReferencedBlobs(ctx, queries.ReferencedBlobsParams{
			WorkspaceID: nullUUID(workspaceID), Keys: []string{key}})
		if err != nil {
			return fmt.Errorf("asset: read the assets of the blob: %w", err)
		}
		if len(refs) > 0 {
			return nil
		}
		if err := s.ent.Check(ctx, tx, workspaceID, entitlements.StorageBytes, *adding+size); err != nil {
			return err
		}
		*adding += size
		return nil
	})
	if err != nil {
		return ImportedFile{}, err
	}
	if err := ensureBlob(ctx, store, key, img, true); err != nil {
		return ImportedFile{}, err
	}
	f := ImportedFile{image: img, size: size, sum: sum[:], key: key, thumbnail: needsThumbnail(img)}
	f.image.Data = nil
	return f, nil
}

// LockImported takes the locks of the blobs of an import's files, in tx, the
// import's transaction, until it ends: in the order of their integers and in
// one statement, as the orphan reconciliation takes several (deleteOrphans), so
// that two imports that share files never hold one each of what the other
// waits for.
func (s *Service) LockImported(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, files []ImportedFile) error {
	if len(files) == 0 {
		return nil
	}
	objects := make([]int32, len(files))
	for i, f := range files {
		objects[i] = database.LockObject(workspaceID, f.sum)
	}
	slices.Sort(objects)
	return lockBlobs(ctx, queries.New(tx), slices.Compact(objects))
}

// AddImported adds the asset assetID of the file f to the new project
// projectID, in tx, the import's transaction, as an upload adds one: under the
// blob's lock, which keeps the orphan reconciliation from deleting it, storing
// it again if it has, and checking the workspace's storage for a blob that is
// new to it. reload returns the file as the archive holds it, which is read
// only if the blob has to be stored again. It returns ErrDuplicateContent for a
// file that the project has already, and an *entitlements.LimitError for a
// workspace without the room.
func (s *Service) AddImported(ctx context.Context, tx pgx.Tx, workspaceID, projectID, assetID, uploadedBy ids.UUID,
	f ImportedFile, reload func() ([]byte, error)) error {
	q := queries.New(tx)
	if err := lockBlob(ctx, q, workspaceID, f.sum); err != nil {
		return err
	}
	st, err := s.checkStorage(ctx, tx, workspaceID, projectID, f.sum, f.key, f.size)
	if err != nil {
		return err
	}
	if st.found {
		return ErrDuplicateContent
	}
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return err
	}
	storeCtx, cancel := context.WithTimeout(ctx, lockedStoreTimeout)
	err = s.ensureImported(storeCtx, store, f, !st.referenced, reload)
	cancel()
	if err != nil {
		return err
	}
	if _, err := s.addRow(ctx, tx, workspaceID, projectID, assetID, uploadedBy, f.image, f.size, f.sum, f.key,
		f.thumbnail, st.referenced); err != nil {
		return fmt.Errorf("asset: add the imported asset: %w", err)
	}
	return nil
}

// ensureImported stores the file f again if the store no longer has it, or has
// it from too long ago for a blob that no row refers to (blobCurrent), from
// what reload reads, which must be the file that StoreImported checked.
func (s *Service) ensureImported(ctx context.Context, store blob.Store, f ImportedFile, unreferenced bool,
	reload func() ([]byte, error)) error {
	current, err := blobCurrent(ctx, store, f.key, unreferenced)
	if err != nil || current {
		return err
	}
	data, err := reload()
	if err != nil {
		return err
	}
	img, err := Sniff(data, int(s.maxBytes))
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(img.Data); !bytes.Equal(sum[:], f.sum) {
		return errors.New("asset: the imported file is not the one that was checked")
	}
	if err := store.Put(ctx, f.key, img.Data, img.Mime); err != nil {
		return fmt.Errorf("asset: %w", err)
	}
	return nil
}
